package core

// Decision cards for agents on another computer (#746, BOARD-LINK-746 in
// docs/sprints/2026-11-18-board-link/decision.md). The card lives only on its own computer
// (the origin). A recipient NAME@COMPUTER of a paired computer is stored as a remote
// recipient with the pair ID, and each event that it must hear of queues one short notice,
// in the same transaction as the event. A notice holds no card text. Chunk 01 captures the
// notices; chunk 02 sends them, and chunk 03 lets the agent read and acknowledge the card at
// the origin.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/rwcii/koinon/internal/link"
)

// Schema 40: the remote recipient kind with its pair (a rebuild, because SQLite cannot widen
// a CHECK in place), the pair and remote name of a verdict delivery, the notices, the stream
// kind of each link stream (a rebuild, because the stream joins the primary key), and the
// notice of a typed inbox message. Every row is copied unchanged.
const boardLinkTablesV40 = `
	ALTER TABLE decision_recipients RENAME TO decision_recipients_v39;
	DROP INDEX decision_recipients_name;
	CREATE TABLE decision_recipients (
		item_id INTEGER NOT NULL, ordinal INTEGER NOT NULL, name TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('participant','peer','remote','')), reason TEXT NOT NULL,
		pair_id TEXT NOT NULL DEFAULT '', remote_name TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (item_id, ordinal));
	INSERT INTO decision_recipients(item_id,ordinal,name,kind,reason)
		SELECT item_id,ordinal,name,kind,reason FROM decision_recipients_v39;
	DROP TABLE decision_recipients_v39;
	CREATE INDEX decision_recipients_name ON decision_recipients(name, item_id);
	CREATE INDEX decision_recipients_remote ON decision_recipients(pair_id, remote_name, item_id) WHERE pair_id<>'';
	ALTER TABLE decision_deliveries ADD COLUMN pair_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_deliveries ADD COLUMN remote_name TEXT NOT NULL DEFAULT '';
	CREATE INDEX decision_deliveries_remote ON decision_deliveries(pair_id, remote_name, outcome, verdict_id) WHERE pair_id<>'';
	CREATE TABLE decision_notices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		xid TEXT NOT NULL UNIQUE,
		pair_id TEXT NOT NULL, item_id INTEGER NOT NULL, reference TEXT NOT NULL,
		recipient TEXT NOT NULL, address TEXT NOT NULL,
		event TEXT NOT NULL CHECK (event IN ('named','changed','removed','withdrawn','closed','restored','verdict')),
		revision INTEGER NOT NULL, verdict_id INTEGER NOT NULL DEFAULT 0, verdict TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		seq INTEGER, digest TEXT,
		state TEXT NOT NULL CHECK (state IN ('queued','sending','uncertain','received','refused','unsupported','unpaired')),
		reason TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0,
		UNIQUE (pair_id, seq));
	CREATE INDEX decision_notices_head ON decision_notices(pair_id, state, id);
	CREATE INDEX decision_notices_item ON decision_notices(item_id, id);
	CREATE INDEX decision_notices_verdict ON decision_notices(verdict_id, pair_id, recipient) WHERE verdict_id>0;
	ALTER TABLE link_streams RENAME TO link_streams_v39;
	CREATE TABLE link_streams (
		pair_id TEXT NOT NULL,
		stream TEXT NOT NULL DEFAULT 'message' CHECK (stream IN ('message','notice')),
		direction TEXT NOT NULL CHECK (direction IN ('out','in')),
		next_seq INTEGER NOT NULL DEFAULT 1,
		high_water INTEGER NOT NULL DEFAULT 0,
		last_result TEXT NOT NULL DEFAULT '', last_reason TEXT NOT NULL DEFAULT '', last_digest TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (pair_id, stream, direction));
	INSERT INTO link_streams(pair_id,stream,direction,next_seq,high_water,last_result,last_reason,last_digest)
		SELECT pair_id,'message',direction,next_seq,high_water,last_result,last_reason,last_digest FROM link_streams_v39;
	DROP TABLE link_streams_v39;
	ALTER TABLE messages ADD COLUMN decision_notice TEXT NOT NULL DEFAULT '';`

// linkPairRef is one paired computer as the board sees it: its pair ID and the effective
// name that addresses use.
type linkPairRef struct {
	ID        string
	Effective string
}

// boardPairs is the board's view of the paired computers for one write. Known is false when
// the link state could not be read: then no address resolves, and a notice for a stored
// remote recipient is queued as it is, for the reconciliation to settle.
type boardPairs struct {
	pairs []linkPairRef
	known bool
}

func (b boardPairs) byName(computer string) (linkPairRef, bool) {
	i := slices.IndexFunc(b.pairs, func(p linkPairRef) bool { return p.Effective == computer })
	if i < 0 {
		return linkPairRef{}, false
	}
	return b.pairs[i], true
}

func (b boardPairs) paired(id string) bool {
	return slices.ContainsFunc(b.pairs, func(p linkPairRef) bool { return p.ID == id })
}

// pairRefs lists the paired computers of a link state.
func pairRefs(s link.State) []linkPairRef {
	var out []linkPairRef
	for _, p := range s.Pairs {
		if p.State == link.Paired {
			out = append(out, linkPairRef{ID: p.ID, Effective: p.Effective})
		}
	}
	return out
}

// boardFence returns the paired computers for one board write and the release of the fence
// that orders the write with an unpair. The daemon's fence holds the link lock from the read
// of the link state until the write's transaction ends, as an inbound delivery does
// (answerDeliver): the lock order is link, then storage. An unpair writes the link state
// under the same lock and retires the pair's notices after it, so a notice is either captured
// before that write and retired after it, or captured after it as unpaired. Without a daemon
// (tests of the store alone) there is no pair.
func (s *Store) boardFence(ctx context.Context) (boardPairs, func()) {
	if s.linkFence == nil {
		return boardPairs{known: true}, func() {}
	}
	return s.linkFence(ctx)
}

// remoteRecipient is one stored recipient row with its remote identity.
type remoteRecipient struct {
	Name, Kind, Reason, PairID, RemoteName string
}

// boardRecipients resolves the names of a create or an edit. A name in the form
// NAME@COMPUTER resolves to a paired computer's effective name as a remote recipient, with
// the pair ID and NAME; another such name is unresolved with link_computer_unknown, or
// link_state_invalid while the link state cannot be read. A kept remote recipient keeps its
// stored pair and name, so a later rename of the computer does not move it to another
// pairing. Other names resolve as local names do.
func boardRecipients(ctx context.Context, q querier, pairs boardPairs, names []string, kept []remoteRecipient) ([]remoteRecipient, error) {
	out := make([]remoteRecipient, 0, len(names))
	for _, name := range names {
		if i := slices.IndexFunc(kept, func(r remoteRecipient) bool { return r.Name == name && r.Kind == "remote" }); i >= 0 {
			out = append(out, kept[i])
			continue
		}
		if local, computer, err := link.ParseAddress(name); err == nil {
			r := remoteRecipient{Name: name}
			switch p, ok := pairs.byName(computer); {
			case ok:
				r.Kind, r.PairID, r.RemoteName = "remote", p.ID, local
			case !pairs.known:
				r.Reason = "link_state_invalid"
			default:
				r.Reason = "link_computer_unknown"
			}
			out = append(out, r)
			continue
		}
		kind, reason, err := resolveRecipient(ctx, q, name)
		if err != nil {
			return nil, err
		}
		out = append(out, remoteRecipient{Name: name, Kind: kind, Reason: reason})
	}
	return out, nil
}

// storedRecipients reads an item's recipient rows.
func storedRecipients(ctx context.Context, q querier, itemID int64) ([]remoteRecipient, error) {
	rows, err := q.QueryContext(ctx, `SELECT name,kind,reason,pair_id,remote_name FROM decision_recipients WHERE item_id=? ORDER BY ordinal`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []remoteRecipient
	for rows.Next() {
		var r remoteRecipient
		if err := rows.Scan(&r.Name, &r.Kind, &r.Reason, &r.PairID, &r.RemoteName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeRecipients replaces an item's recipient rows.
func writeRecipients(ctx context.Context, tx *writeTx, id int64, recipients []remoteRecipient) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM decision_recipients WHERE item_id=?`, id); err != nil {
		return tx.fail(err)
	}
	for i, r := range recipients {
		if _, err := tx.ExecContext(ctx, `INSERT INTO decision_recipients(item_id,ordinal,name,kind,reason,pair_id,remote_name) VALUES (?,?,?,?,?,?,?)`,
			id, i, r.Name, r.Kind, r.Reason, r.PairID, r.RemoteName); err != nil {
			return tx.fail(err)
		}
	}
	return nil
}

// shownRecipients is the recipient list that a create, an edit and a read show. A local
// name resolves now, as before; a remote recipient shows its stored pair.
func shownRecipients(ctx context.Context, q querier, rows []remoteRecipient) ([]DecisionRecipient, error) {
	out := make([]DecisionRecipient, 0, len(rows))
	for _, r := range rows {
		if r.Kind == "remote" {
			out = append(out, DecisionRecipient{Name: r.Name, Kind: "remote", Resolved: true, Pair: r.PairID})
			continue
		}
		if _, _, err := link.ParseAddress(r.Name); err == nil {
			reason := r.Reason
			if reason == "" {
				reason = "link_computer_unknown"
			}
			out = append(out, DecisionRecipient{Name: r.Name, Reason: reason})
			continue
		}
		kind, reason, err := resolveRecipient(ctx, q, r.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, DecisionRecipient{Name: r.Name, Kind: kind, Resolved: kind != "", Reason: reason})
	}
	return out, nil
}

// noticeEvent is one event that a remote recipient hears of.
type noticeEvent struct {
	ItemID    int64
	Reference string
	Event     string
	Revision  int64
	VerdictID int64
	Verdict   string
}

// queueNotice stores one notice for a remote recipient in the event's transaction. A notice
// for a pair that is no longer paired is stored as unpaired, so the card shows it and nothing
// waits for it.
func queueNotice(ctx context.Context, tx querier, now int64, pairs boardPairs, r remoteRecipient, e noticeEvent) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	state, reason, finished := "queued", "", int64(0)
	if pairs.known && !pairs.paired(r.PairID) {
		state, reason, finished = "unpaired", "unpaired", now
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_notices(xid,pair_id,item_id,reference,recipient,address,event,revision,
		verdict_id,verdict,created_at,state,reason,updated_at,finished_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		hex.EncodeToString(raw[:]), r.PairID, e.ItemID, e.Reference, r.RemoteName, r.Name, e.Event, e.Revision,
		e.VerdictID, e.Verdict, now, state, reason, now, finished); err != nil {
		return err
	}
	return nil
}

// queueNotices stores one notice of event for each remote recipient of rows.
func queueNotices(ctx context.Context, tx *writeTx, now int64, pairs boardPairs, rows []remoteRecipient, e noticeEvent) error {
	for _, r := range rows {
		if r.Kind == "remote" {
			if err := queueNotice(ctx, tx.Tx, now, pairs, r, e); err != nil {
				return tx.fail(err)
			}
		}
	}
	return nil
}

// sameRemote reports whether two rows name one remote recipient: the same pair and name.
func sameRemote(a, b remoteRecipient) bool {
	return a.Kind == "remote" && b.Kind == "remote" && a.PairID == b.PairID && a.RemoteName == b.RemoteName
}

// editNotices stores the notices of an edit: changed for each remote recipient that stays,
// named for each one that is new, and removed for each one that the edit drops.
func editNotices(ctx context.Context, tx *writeTx, now int64, pairs boardPairs, before, after []remoteRecipient, e noticeEvent) error {
	for _, r := range after {
		if r.Kind != "remote" {
			continue
		}
		e := e
		e.Event = "named"
		if slices.ContainsFunc(before, func(b remoteRecipient) bool { return sameRemote(b, r) }) {
			e.Event = "changed"
		}
		if err := queueNotice(ctx, tx.Tx, now, pairs, r, e); err != nil {
			return tx.fail(err)
		}
	}
	for _, r := range before {
		if r.Kind == "remote" && !slices.ContainsFunc(after, func(a remoteRecipient) bool { return sameRemote(a, r) }) {
			e := e
			e.Event = "removed"
			if err := queueNotice(ctx, tx.Tx, now, pairs, r, e); err != nil {
				return tx.fail(err)
			}
		}
	}
	return nil
}

// noticeLive are the states of a notice that may still be sent.
const noticeLive = `state IN ('queued','sending','uncertain','unsupported')`

// retireNotices ends the notices of an unpaired pair in the unpair's retirement transaction.
func retireNotices(ctx context.Context, tx *writeTx, now int64, pairID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE decision_notices SET state='unpaired',reason='unpaired',updated_at=?,finished_at=?
		WHERE pair_id=? AND `+noticeLive, now, now, pairID); err != nil {
		return tx.fail(err)
	}
	return nil
}

// reconcileBoardV40 resolves, once in the 39 to 40 migration, the recipients that cards
// already named as NAME@COMPUTER for a computer paired at that time. On each card that is not
// archived, such a recipient becomes remote and gets a named notice; when the current
// verdict's delivery to that name was skipped with unknown_name, the delivery becomes a
// remote one, queued, with a verdict notice. Archived cards stay as they are.
func reconcileBoardV40(tx *sql.Tx, now int64, pairs []linkPairRef) error {
	if len(pairs) == 0 {
		return nil
	}
	ctx := context.Background()
	rows, err := tx.QueryContext(ctx, `SELECT r.item_id,r.ordinal,r.name,i.reference,i.revision FROM decision_recipients r
		JOIN decision_items i ON i.id=r.item_id WHERE i.state<>'archived' AND r.kind='' ORDER BY r.item_id,r.ordinal`)
	if err != nil {
		return err
	}
	type found struct {
		item, ordinal, revision int64
		name, reference         string
	}
	var candidates []found
	for rows.Next() {
		var f found
		if err := rows.Scan(&f.item, &f.ordinal, &f.name, &f.reference, &f.revision); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	view := boardPairs{pairs: pairs, known: true}
	for _, f := range candidates {
		local, computer, err := link.ParseAddress(f.name)
		if err != nil {
			continue
		}
		p, ok := view.byName(computer)
		if !ok {
			continue
		}
		r := remoteRecipient{Name: f.name, Kind: "remote", PairID: p.ID, RemoteName: local}
		if _, err := tx.ExecContext(ctx, `UPDATE decision_recipients SET kind='remote',reason='',pair_id=?,remote_name=? WHERE item_id=? AND ordinal=?`,
			p.ID, local, f.item, f.ordinal); err != nil {
			return err
		}
		if err := queueNotice(ctx, tx, now, view, r, noticeEvent{ItemID: f.item, Reference: f.reference, Event: "named", Revision: f.revision}); err != nil {
			return err
		}
		var verdictID, verdictRevision int64
		var verdict string
		err = tx.QueryRowContext(ctx, `SELECT id,revision,verdict FROM decision_verdicts WHERE item_id=? AND superseded_at=0 ORDER BY id DESC LIMIT 1`, f.item).
			Scan(&verdictID, &verdictRevision, &verdict)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE decision_deliveries SET kind='remote',outcome='queued',reason='',pair_id=?,remote_name=?
			WHERE verdict_id=? AND name=? AND outcome='skipped' AND reason='unknown_name'`, p.ID, local, verdictID, f.name)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			if err != nil {
				return err
			}
			continue
		}
		if err := queueNotice(ctx, tx, now, view, r, noticeEvent{ItemID: f.item, Reference: f.reference, Event: "verdict", Revision: verdictRevision,
			VerdictID: verdictID, Verdict: verdict}); err != nil {
			return err
		}
	}
	return nil
}

// itemNotices stores one notice of an event that the whole card has, for each of its remote
// recipients now.
func itemNotices(ctx context.Context, tx *writeTx, now int64, pairs boardPairs, e noticeEvent) error {
	rows, err := storedRecipients(ctx, tx.Tx, e.ItemID)
	if err != nil {
		return err
	}
	return queueNotices(ctx, tx, now, pairs, rows, e)
}

// deliverRemote stores a verdict's delivery to a remote recipient, with its stored pair and
// name, and the verdict notice. The delivery waits, queued, for the agent's acknowledgement
// at this computer (chunk 03); it has no inbox message here. A recipient whose computer is no
// longer paired gets a skipped delivery with reason unpaired, and an unpaired notice.
func deliverRemote(ctx context.Context, tx *writeTx, now int64, pairs boardPairs, verdictID int64, ordinal int, r remoteRecipient, e noticeEvent) (DecisionDelivery, error) {
	d := DecisionDelivery{Name: r.Name, Kind: "remote", Outcome: "queued", Pair: r.PairID, Notice: "queued"}
	if pairs.known && !pairs.paired(r.PairID) {
		d.Outcome, d.Reason, d.Notice = "skipped", "unpaired", "unpaired"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_deliveries(verdict_id,ordinal,kind,name,outcome,reason,message_id,pair_id,remote_name)
		VALUES (?,?,'remote',?,?,?,0,?,?)`, verdictID, ordinal, r.Name, d.Outcome, d.Reason, r.PairID, r.RemoteName); err != nil {
		return d, tx.fail(err)
	}
	if err := queueNotice(ctx, tx.Tx, now, pairs, r, e); err != nil {
		return d, tx.fail(err)
	}
	return d, nil
}

// noticeStates sets the state of each remote recipient's newest notice on the shown list.
func noticeStates(ctx context.Context, q querier, itemID int64, stored []remoteRecipient, shown []DecisionRecipient) error {
	for i, r := range stored {
		if r.Kind != "remote" || i >= len(shown) {
			continue
		}
		err := q.QueryRowContext(ctx, `SELECT state FROM decision_notices WHERE item_id=? AND pair_id=? AND recipient=?
			ORDER BY id DESC LIMIT 1`, itemID, r.PairID, r.RemoteName).Scan(&shown[i].Notice)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// boardFence is the daemon's fence for one board write (Store.boardFence). A signed write
// that a paired computer forwards already holds the link lock for its whole run
// (answerMaintainer); it reads the link state under that lock and takes nothing more.
func (d *Daemon) boardFence(ctx context.Context) (boardPairs, func()) {
	if _, forwarded := forwardedPin(ctx); forwarded {
		return d.boardPairsLocked(), func() {}
	}
	d.link.mu.Lock()
	return d.boardPairsLocked(), d.link.mu.Unlock
}

// boardPairsLocked reads the paired computers; the caller holds the link lock.
func (d *Daemon) boardPairsLocked() boardPairs {
	s, err := link.ReadState(d.root)
	if err != nil {
		return boardPairs{}
	}
	return boardPairs{pairs: pairRefs(s), known: true}
}
