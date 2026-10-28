package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Decision verdicts, delivery, close and history (#206, chunk 02). The maintainer gives a
// verdict, from an authenticated dashboard action or a maintainer client (PDR 009,
// Decision 3); agents read the stored records.

const (
	decisionNoteLimit     = 4 << 10
	decisionHistoryItems  = 16
	decisionHistoryBytes  = 256 << 10
	decisionAckIDs        = 32
	decisionNoticeIDs     = 8
	decisionVerdictKind   = "decision_verdict"
	decisionArchiveAfter  = 30 * 24 * 60 * 60 * 1000 // milliseconds
	decisionArchiveBatch  = 64
	decisionArchiveWindow = 256
)

// Version 26: verdicts with their snapshots, per-recipient deliveries, the deep-archive
// locator, the typed verdict ID of an inbox message, and the close and deep-archive mark
// fields of an item (#206, chunk 02). decision_pending reads (name, outcome, verdict_id) for
// queued deliveries only, so acknowledged history is never scanned.
const decisionVerdictTables = `
	CREATE TABLE decision_verdicts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL, revision INTEGER NOT NULL,
		verdict TEXT NOT NULL CHECK (verdict IN ('approved','declined','dismissed')),
		note TEXT NOT NULL, created_at INTEGER NOT NULL, snapshot TEXT NOT NULL,
		superseded_at INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX decision_verdicts_item ON decision_verdicts(item_id, id);
	CREATE TABLE decision_deliveries (
		verdict_id INTEGER NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL,
		outcome TEXT NOT NULL CHECK (outcome IN ('queued','skipped','acknowledged')), reason TEXT NOT NULL,
		message_id INTEGER NOT NULL DEFAULT 0, acknowledged_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (verdict_id, ordinal));
	CREATE INDEX decision_deliveries_pending ON decision_deliveries(name, outcome, verdict_id);
	CREATE TABLE decision_deep_archive (
		reference TEXT PRIMARY KEY, file TEXT NOT NULL, offset INTEGER NOT NULL, length INTEGER NOT NULL,
		moved_at INTEGER NOT NULL);
	ALTER TABLE messages ADD COLUMN decision_verdict INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE decision_items ADD COLUMN closed_by TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_items ADD COLUMN closed_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE decision_items ADD COLUMN outcome_note TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_items ADD COLUMN mark_revision INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE decision_items ADD COLUMN mark_file TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_items ADD COLUMN mark_offset INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE decision_items ADD COLUMN verdict_count INTEGER NOT NULL DEFAULT 0;`

// DecisionSnapshot is the text that the maintainer saw when giving a verdict.
type DecisionSnapshot struct {
	Title          string   `json:"title"`
	Context        string   `json:"context"`
	Recommendation string   `json:"recommendation"`
	Commands       []string `json:"commands"`
}

// DecisionDelivery is one recipient's delivery record of a verdict. It records delivery and
// acknowledgement only; it is not proof that an action was done.
type DecisionDelivery struct {
	Name           string `json:"name"`
	Kind           string `json:"kind,omitempty"`
	Outcome        string `json:"outcome"`
	Reason         string `json:"reason,omitempty"`
	AcknowledgedAt int64  `json:"acknowledged_at,omitempty"`
}

// DecisionVerdict is one verdict with the snapshot it was given on.
type DecisionVerdict struct {
	ID           int64              `json:"verdict_id"`
	Revision     int64              `json:"revision"`
	Verdict      string             `json:"verdict"`
	Note         string             `json:"note,omitempty"`
	At           int64              `json:"at"`
	SupersededAt int64              `json:"superseded_at,omitempty"`
	Snapshot     DecisionSnapshot   `json:"snapshot"`
	Deliveries   []DecisionDelivery `json:"deliveries,omitempty"`
	// Via is the maintainer client that gave the verdict, dashboard or mcp:NAME (PDR 009,
	// Decision 4). The maintainer's reads fill it; an agent's read leaves it empty.
	Via string `json:"via,omitempty"`
}

// DecisionHistoryPage is one page of an item's verdicts in verdict ID order.
type DecisionHistoryPage struct {
	Reference string            `json:"reference"`
	Verdicts  []DecisionVerdict `json:"verdicts"`
	// Withdrawals is the page of agent withdrawals that a withdrawals read asks for (#504).
	Withdrawals []DecisionWithdrawal `json:"withdrawals,omitempty"`
	More        bool                 `json:"more"`
	NextAfter   *int64               `json:"next_after"`
}

// DecisionPending is one queued delivery of the caller.
type DecisionPending struct {
	VerdictID    int64  `json:"verdict_id"`
	Reference    string `json:"reference"`
	Verdict      string `json:"verdict"`
	Revision     int64  `json:"revision"`
	At           int64  `json:"at"`
	SupersededAt int64  `json:"superseded_at,omitempty"`
}

// DecisionPendingPage is one page of the caller's queued deliveries.
type DecisionPendingPage struct {
	Deliveries []DecisionPending `json:"deliveries"`
	More       bool              `json:"more"`
	NextAfter  *int64            `json:"next_after"`
}

func verdictNoticeBody(reference string, id int64) string {
	return fmt.Sprintf("Decision %s has a verdict (%d). Read it with decision_get.", reference, id)
}

// currentVerdict is the item's current verdict, or nil. A reopen supersedes the current
// verdict and a new verdict needs an active item, so only the latest verdict can be current:
// one index seek, however long the history is.
func currentVerdict(ctx context.Context, q querier, itemID int64, deliveries bool) (*DecisionVerdict, error) {
	var v DecisionVerdict
	var snapshot string
	err := q.QueryRowContext(ctx, `SELECT id,revision,verdict,note,created_at,superseded_at,snapshot FROM decision_verdicts
		WHERE item_id=? ORDER BY id DESC LIMIT 1`, itemID).
		Scan(&v.ID, &v.Revision, &v.Verdict, &v.Note, &v.At, &v.SupersededAt, &snapshot)
	if errors.Is(err, sql.ErrNoRows) || err == nil && v.SupersededAt != 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(snapshot), &v.Snapshot) != nil {
		return nil, workError("decision_unavailable", "the stored snapshot is invalid")
	}
	if deliveries {
		if v.Deliveries, err = verdictDeliveries(ctx, q, v.ID); err != nil {
			return nil, err
		}
	}
	return &v, nil
}

func verdictDeliveries(ctx context.Context, q querier, id int64) ([]DecisionDelivery, error) {
	rows, err := q.QueryContext(ctx, `SELECT name,kind,outcome,reason,acknowledged_at FROM decision_deliveries WHERE verdict_id=? ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DecisionDelivery{}
	for rows.Next() {
		var d DecisionDelivery
		if err := rows.Scan(&d.Name, &d.Kind, &d.Outcome, &d.Reason, &d.AcknowledgedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DecisionVerdict records the maintainer's verdict, from the dashboard or a maintainer
// client, on the revision that the maintainer saw. One ordinary write stores the verdict with
// the client of the request's context as via, its snapshot, the item's new state, one
// delivery and one typed inbox message per resolved recipient, and the audit record.
func (s *Store) DecisionVerdict(ctx context.Context, reference string, revision int64, verdict, note string) (DecisionVerdict, error) {
	if checkReference(reference) != nil || revision < 1 {
		return DecisionVerdict{}, ErrInvalid
	}
	if verdict != "approved" && verdict != "declined" && verdict != "dismissed" {
		return DecisionVerdict{}, ErrInvalid
	}
	if err := checkMarkdown(note, "note", decisionNoteLimit); err != nil {
		return DecisionVerdict{}, err
	}
	if err := checkCredentials(note, "note"); err != nil {
		return DecisionVerdict{}, err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionVerdict{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	id, item, err := decisionRow(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionVerdict{}, err
	}
	if item.Revision != revision {
		return DecisionVerdict{}, WorkRefusal{Code: "stale_revision", Message: "the decision item changed after the page showed it",
			Details: map[string]any{"current_revision": item.Revision}}
	}
	if item.State != "active" {
		return DecisionVerdict{}, workError("decided", "the decision item already has a verdict")
	}
	if (item.Kind == "fyi") != (verdict == "dismissed") {
		return DecisionVerdict{}, workError("invalid_verdict", "a decision item takes approve or decline; an fyi item takes dismiss")
	}
	// A command that fails the printable-ASCII rule is hidden, so the item cannot be
	// approved as displayed (PDR 007, Decision 3). Decline and Dismiss approve nothing.
	for _, c := range item.Commands {
		if verdict == "approved" && !printableCommand(c) {
			return DecisionVerdict{}, workError("hidden_command", "a stored command is hidden; the item cannot be approved as displayed")
		}
	}
	snapshot, _ := json.Marshal(DecisionSnapshot{Title: item.Title, Context: item.Context, Recommendation: item.Recommendation, Commands: item.Commands})
	inserted, err := tx.ExecContext(ctx, `INSERT INTO decision_verdicts(item_id,revision,verdict,note,created_at,snapshot,via) VALUES (?,?,?,?,?,?,?)`,
		id, revision, verdict, note, now, string(snapshot), originOf(ctx))
	if err != nil {
		return DecisionVerdict{}, tx.fail(err)
	}
	verdictID, err := inserted.LastInsertId()
	if err != nil {
		return DecisionVerdict{}, err
	}
	state, archived := "in_progress", int64(0)
	if verdict != "approved" {
		state, archived = "archived", now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decision_items SET state=?,archived_at=?,updated_at=?,mark_revision=0,mark_file='',mark_offset=0,
		verdict_count=verdict_count+1 WHERE id=?`,
		state, archived, now, id); err != nil {
		return DecisionVerdict{}, tx.fail(err)
	}
	if err := bumpChange(ctx, tx, changeDecisions); err != nil {
		return DecisionVerdict{}, tx.fail(err)
	}
	result := DecisionVerdict{ID: verdictID, Revision: revision, Verdict: verdict, Note: note, At: now,
		Snapshot: DecisionSnapshot{Title: item.Title, Context: item.Context, Recommendation: item.Recommendation, Commands: item.Commands}}
	if verdict != "dismissed" {
		names, err := recipientNames(ctx, tx.Tx, id)
		if err != nil {
			return DecisionVerdict{}, err
		}
		for i, name := range names {
			d, err := s.deliverVerdict(ctx, tx, now, verdictID, i, name, reference)
			if err != nil {
				return DecisionVerdict{}, err
			}
			result.Deliveries = append(result.Deliveries, d)
		}
	}
	setAuditTarget(ctx, fmt.Sprintf("decision %s revision %d %s verdict %d", reference, revision, verdict, verdictID))
	return result, tx.Commit()
}

func recipientNames(ctx context.Context, q querier, itemID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM decision_recipients WHERE item_id=? ORDER BY ordinal`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// deliverVerdict resolves one recipient name now and stores its delivery: queued with one
// typed inbox message, or skipped with the reason. A participant's message goes to the
// participant inbox, so its current holder reads it, also after a holder change.
func (s *Store) deliverVerdict(ctx context.Context, tx *writeTx, now, verdictID int64, ordinal int, name, reference string) (DecisionDelivery, error) {
	d := DecisionDelivery{Name: name, Outcome: "skipped"}
	var kind, family, sessionID string
	err := tx.QueryRowContext(ctx, `SELECT kind,family,session_id FROM names WHERE name=?`, name).Scan(&kind, &family, &sessionID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		d.Reason = "unknown_name"
	case err != nil:
		return d, err
	case family == maintainerKey.Family:
		d.Reason = "maintainer"
	case kind == "alias":
		d.Kind = "participant"
		if err := ensureParticipantInbox(ctx, tx.Tx, now, name); err != nil {
			return d, tx.fail(err)
		}
		sessionID = participantKey(name)
	default:
		d.Kind = "peer"
	}
	var messageID int64
	if d.Reason == "" {
		var seq int64
		err = tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? AND retired_at=0 AND expires_at>?
			RETURNING last_seq`, family, sessionID, now).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			d.Reason = "recipient_inactive"
		} else if err != nil {
			return d, tx.fail(err)
		} else {
			inserted, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
				created_at,delivery_updated_at,system_kind,decision_verdict) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				family, sessionID, seq, maintainerKey.Family, maintainerKey.ID, "maintainer", verdictNoticeBody(reference, verdictID),
				now, now, decisionVerdictKind, verdictID)
			if err != nil {
				return d, tx.fail(err)
			}
			if messageID, err = inserted.LastInsertId(); err != nil {
				return d, err
			}
			d.Outcome = "queued"
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_deliveries(verdict_id,ordinal,kind,name,outcome,reason,message_id) VALUES (?,?,?,?,?,?,?)`,
		verdictID, ordinal, d.Kind, name, d.Outcome, d.Reason, messageID); err != nil {
		return d, tx.fail(err)
	}
	return d, nil
}

// callerNames are the recipient names that name the caller now: its peer name and the
// participant address it holds.
func callerNames(ctx context.Context, tx *sql.Tx, now int64, caller Key) ([]string, error) {
	peer, err := peerOf(ctx, tx, caller.Family, caller.ID)
	if err != nil {
		return nil, err
	}
	address, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range []string{peer, address} {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// DecisionPending lists the caller's queued deliveries in verdict ID order, also after the
// inbox message failed or aged out. Reading moves no cursor.
func (s *Store) DecisionPending(ctx context.Context, caller Key, after int64, limit int) (DecisionPendingPage, error) {
	if after < 0 || limit < 0 {
		return DecisionPendingPage{}, decisionInvalid("after and limit", "must not be negative")
	}
	if limit == 0 || limit > decisionPageItems {
		limit = decisionPageItems
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionPendingPage{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return DecisionPendingPage{}, err
	}
	names, err := callerNames(ctx, tx, now, caller)
	if err != nil {
		return DecisionPendingPage{}, err
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, name := range names {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT verdict_id FROM decision_deliveries WHERE name=? AND outcome='queued' AND verdict_id>?
			ORDER BY verdict_id LIMIT ?`, name, after, limit+1)
		if err != nil {
			return DecisionPendingPage{}, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return DecisionPendingPage{}, err
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return DecisionPendingPage{}, err
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	page := DecisionPendingPage{Deliveries: []DecisionPending{}}
	largest := int64(math.MaxInt64)
	envelope, _ := json.Marshal(map[string]any{"ok": true, "result": DecisionPendingPage{Deliveries: []DecisionPending{}, More: true, NextAfter: &largest}})
	used := len(envelope) + 1
	for i, id := range ids {
		var p DecisionPending
		if err := tx.QueryRowContext(ctx, `SELECT v.id,i.reference,v.verdict,v.revision,v.created_at,v.superseded_at FROM decision_verdicts v
			JOIN decision_items i ON i.id=v.item_id WHERE v.id=?`, id).Scan(&p.VerdictID, &p.Reference, &p.Verdict, &p.Revision, &p.At, &p.SupersededAt); err != nil {
			return DecisionPendingPage{}, err
		}
		data, _ := json.Marshal(p)
		size := len(data) + 1
		if i == limit || (len(page.Deliveries) > 0 && used+size > decisionPageBytes) {
			last := page.Deliveries[len(page.Deliveries)-1].VerdictID
			page.More, page.NextAfter = true, &last
			break
		}
		used += size
		page.Deliveries = append(page.Deliveries, p)
	}
	return page, nil
}

// DecisionAck acknowledges the caller's queued deliveries of the named verdicts. It is an
// ordinary write: a repeated or late acknowledgement changes nothing, and when the store is
// full it is refused and the deliveries stay queued.
func (s *Store) DecisionAck(ctx context.Context, caller Key, verdicts []int64) ([]int64, error) {
	if !validKey(caller.Family, caller.ID) {
		return nil, ErrInvalid
	}
	if len(verdicts) == 0 || len(verdicts) > decisionAckIDs {
		return nil, decisionInvalid("verdict_ids", fmt.Sprintf("must name 1 to %d verdicts", decisionAckIDs))
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return nil, err
	}
	names, err := callerNames(ctx, tx.Tx, now, caller)
	if err != nil {
		return nil, err
	}
	acknowledged := []int64{}
	for _, id := range verdicts {
		changed := int64(0)
		for _, name := range names {
			result, err := tx.ExecContext(ctx, `UPDATE decision_deliveries SET outcome='acknowledged',acknowledged_at=? WHERE verdict_id=? AND name=? AND outcome='queued'`,
				now, id, name)
			if err != nil {
				return nil, tx.fail(err)
			}
			n, _ := result.RowsAffected()
			changed += n
		}
		if changed > 0 {
			acknowledged = append(acknowledged, id)
		}
	}
	return acknowledged, tx.Commit()
}

// DecisionClose moves an in-progress item to archived when its work is done, with the
// caller's name, the time and an optional outcome note. key and deadline make a retry return
// the first result.
func (s *Store) DecisionClose(ctx context.Context, caller Key, reference string, ifRevision int64, note, key string, deadline float64) (DecisionItem, error) {
	if !validKey(caller.Family, caller.ID) {
		return DecisionItem{}, ErrInvalid
	}
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	if err := checkMarkdown(note, "note", decisionNoteLimit); err != nil {
		return DecisionItem{}, err
	}
	if err := checkCredentials(note, "note"); err != nil {
		return DecisionItem{}, err
	}
	if err := checkLine(key, "key", decisionKeyLimit, true); err != nil {
		return DecisionItem{}, err
	}
	clock := s.clock()
	if math.IsNaN(deadline) || math.IsInf(deadline, 0) || deadline > clock+s.limits().idemTTL {
		return DecisionItem{}, decisionInvalid("deadline", fmt.Sprintf("must be a Unix time in seconds at most %.0f s ahead", s.limits().idemTTL))
	}
	if deadline <= clock {
		return DecisionItem{}, workError("retry_deadline_expired", "the retry deadline has passed")
	}
	print := decisionFingerprint("close\x00"+reference, DecisionFields{Order: &ifRevision, Context: &note}, deadline)
	key = "close\x00" + key
	read, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionItem{}, err
	}
	if err := active(ctx, read, s.now().UnixMilli(), caller); err != nil {
		read.Rollback()
		return DecisionItem{}, err
	}
	original, err := closeRepeat(ctx, read, caller, key, print)
	read.Rollback()
	if err != nil || original != nil {
		return orItem(original), err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return DecisionItem{}, err
	}
	if original, err := closeRepeat(ctx, tx.Tx, caller, key, print); err != nil || original != nil {
		return orItem(original), err
	}
	id, item, err := decisionRow(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionItem{}, err
	}
	if item.Revision != ifRevision {
		return DecisionItem{}, WorkRefusal{Code: "stale_revision", Message: "the decision item changed; read it again",
			Details: map[string]any{"current_revision": item.Revision}}
	}
	if item.State != "in_progress" {
		return DecisionItem{}, workError("not_in_progress", "only an approved item in progress can be closed")
	}
	closer, err := peerOf(ctx, tx.Tx, caller.Family, caller.ID)
	if err != nil {
		return DecisionItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decision_items SET state='archived',archived_at=?,updated_at=?,closed_by=?,closed_at=?,outcome_note=? WHERE id=?`,
		now, now, closer, now, note, id); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if err := bumpChange(ctx, tx, changeDecisions); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	// The receipt is the closed item itself, so a retry returns this result even after a
	// later edit or verdict changed the item.
	closed, err := decisionItem(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionItem{}, err
	}
	receipt, _ := json.Marshal(closed)
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_replays(family,caller_id,key,fingerprint,deadline,result) VALUES (?,?,?,?,?,?)`,
		caller.Family, caller.ID, key, print, deadline, string(receipt)); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	return closed, tx.Commit()
}

// closeRepeat returns the first result of a close with this key, or nil.
func closeRepeat(ctx context.Context, q querier, caller Key, key, print string) (*DecisionItem, error) {
	var stored, result string
	err := q.QueryRowContext(ctx, `SELECT fingerprint,result FROM decision_replays WHERE family=? AND caller_id=? AND key=?`,
		caller.Family, caller.ID, key).Scan(&stored, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stored != print {
		return nil, workError("idempotency_conflict", "this key was used for a different request")
	}
	var original DecisionItem
	if err := json.Unmarshal([]byte(result), &original); err != nil {
		return nil, err
	}
	return &original, nil
}

// decisionWithdrawReasonLimit bounds a withdrawal reason, in bytes.
const decisionWithdrawReasonLimit = 512

// DecisionWithdraw archives an active item that no longer needs the maintainer (#504): a
// waiting decision item or an open fyi item, at the revision the caller read, with a
// required one-line reason. The caller is the item's author, a session of the author's
// participant or a named recipient. The withdrawal is no verdict and sends no delivery;
// decision_restore with if_revision reopens the item. key and deadline make a retry return
// the first result.
func (s *Store) DecisionWithdraw(ctx context.Context, caller Key, reference string, ifRevision int64, reason, key string, deadline float64) (DecisionItem, error) {
	if !validKey(caller.Family, caller.ID) {
		return DecisionItem{}, ErrInvalid
	}
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	if err := checkLine(reason, "reason", decisionWithdrawReasonLimit, true); err != nil {
		return DecisionItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return DecisionItem{}, decisionInvalid("reason", "must not be blank")
	}
	if err := checkCredentials(reason, "reason"); err != nil {
		return DecisionItem{}, err
	}
	if err := checkLine(key, "key", decisionKeyLimit, true); err != nil {
		return DecisionItem{}, err
	}
	clock := s.clock()
	if math.IsNaN(deadline) || math.IsInf(deadline, 0) || deadline > clock+s.limits().idemTTL {
		return DecisionItem{}, decisionInvalid("deadline", fmt.Sprintf("must be a Unix time in seconds at most %.0f s ahead", s.limits().idemTTL))
	}
	if deadline <= clock {
		return DecisionItem{}, workError("retry_deadline_expired", "the retry deadline has passed")
	}
	print := decisionFingerprint("withdraw\x00"+reference, DecisionFields{Order: &ifRevision, Context: &reason}, deadline)
	key = "withdraw\x00" + key
	read, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionItem{}, err
	}
	if err := active(ctx, read, s.now().UnixMilli(), caller); err != nil {
		read.Rollback()
		return DecisionItem{}, err
	}
	original, err := closeRepeat(ctx, read, caller, key, print)
	read.Rollback()
	if err != nil || original != nil {
		return orItem(original), err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return DecisionItem{}, err
	}
	if original, err := closeRepeat(ctx, tx.Tx, caller, key, print); err != nil || original != nil {
		return orItem(original), err
	}
	id, item, err := decisionRow(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionItem{}, err
	}
	if item.Revision != ifRevision {
		return DecisionItem{}, WorkRefusal{Code: "stale_revision", Message: "the decision item changed; read it again",
			Details: map[string]any{"current_revision": item.Revision}}
	}
	// A verdict moves the item out of active in its own write, so an item that is active in
	// this write has no current verdict.
	if item.State != "active" {
		return DecisionItem{}, workError("not_active", "only a waiting decision item or an open fyi item can be withdrawn")
	}
	peer, err := itemParty(ctx, tx.Tx, now, caller, id, item)
	if err != nil {
		return DecisionItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decision_items SET state='archived',archived_at=?,updated_at=? WHERE id=?`, now, now, id); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_withdrawals(item_id,revision,family,name,reason,at) VALUES (?,?,?,?,?,?)`,
		id, item.Revision, caller.Family, peer, reason, now); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if err := bumpChange(ctx, tx, changeDecisions); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	// The receipt is the withdrawn item itself, so a retry returns this result even after a
	// later reopen or edit changed the item.
	withdrawn, err := decisionItem(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionItem{}, err
	}
	receipt, _ := json.Marshal(withdrawn)
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_replays(family,caller_id,key,fingerprint,deadline,result) VALUES (?,?,?,?,?,?)`,
		caller.Family, caller.ID, key, print, deadline, string(receipt)); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	return withdrawn, tx.Commit()
}

// participantIdentity and participantSession apply to sessions aliased as s: a participant
// is the family, repository and role of a session that qualifies for one (a launched native
// session or DeepSeek, not a sub-agent, with a repository). A plain peer has none (PDR 001).
const (
	participantIdentity = `s.family||char(31)||s.repository||char(31)||s.role`
	participantSession  = `s.subagent=0 AND s.repository!='' AND ` + participantQualifier
)

// authorParticipantOf returns the caller's participant identity, or "" for a plain peer.
func authorParticipantOf(ctx context.Context, tx *sql.Tx, caller Key) (string, error) {
	var identity string
	err := tx.QueryRowContext(ctx, `SELECT `+participantIdentity+` FROM sessions s WHERE s.family=? AND s.id=? AND `+participantSession,
		caller.Family, caller.ID).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return identity, err
}

// itemParty returns the caller's peer name when the caller may withdraw or reopen the item
// (#504): its author, a session of the participant that the item stored for its author at
// create (so a successor after a context cycle qualifies, also after its predecessor's
// session was purged), or a recipient that names the caller's peer name or held address.
// Otherwise it refuses with not_item_party.
func itemParty(ctx context.Context, tx *sql.Tx, now int64, caller Key, id int64, item DecisionItem) (string, error) {
	peer, err := peerOf(ctx, tx, caller.Family, caller.ID)
	if err != nil {
		return "", err
	}
	if caller.Family == item.AuthorFamily && peer != "" && peer == item.AuthorName {
		return peer, nil
	}
	participant, err := authorParticipantOf(ctx, tx, caller)
	if err != nil {
		return "", err
	}
	var author string
	if err := tx.QueryRowContext(ctx, `SELECT author_participant FROM decision_items WHERE id=?`, id).Scan(&author); err != nil {
		return "", err
	}
	if participant != "" && participant == author {
		return peer, nil
	}
	names, err := callerNames(ctx, tx, now, caller)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_recipients WHERE item_id=? AND name=?`, id, name).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return peer, nil
		}
	}
	return "", workError("not_item_party", "only the item's author, a session of its participant or a named recipient can do this")
}

func orItem(item *DecisionItem) DecisionItem {
	if item == nil {
		return DecisionItem{}
	}
	return *item
}

// DecisionHistory pages an item's verdicts with their snapshots in verdict ID order. A page
// stops before the verdict that would take the serialized reply past decisionHistoryBytes
// and always holds at least one: the largest verdict, every field at its limit and every
// character at the longest JSON escape, serializes below that limit.
func (s *Store) DecisionHistory(ctx context.Context, caller Key, reference string, after int64, limit int, withdrawals bool) (DecisionHistoryPage, error) {
	if err := checkReference(reference); err != nil {
		return DecisionHistoryPage{}, err
	}
	if after < 0 || limit < 0 {
		return DecisionHistoryPage{}, decisionInvalid("after and limit", "must not be negative")
	}
	if limit == 0 || limit > decisionHistoryItems {
		limit = decisionHistoryItems
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return DecisionHistoryPage{}, err
	}
	// withdrawals pages the item's agent withdrawals instead of its verdicts (#504).
	if withdrawals {
		return withdrawalHistory(ctx, tx, reference, after, limit)
	}
	// Agents see the maintainer's verdicts without the client that gave them (PDR 009,
	// Decision 4).
	return decisionHistory(ctx, tx, reference, after, limit, false)
}

// decisionHistory reads one page of an item's verdicts with their snapshots, bounded by
// limit verdicts and by decisionHistoryBytes of serialized reply. withVia adds each
// verdict's maintainer client, for the maintainer's own reads.
func decisionHistory(ctx context.Context, tx querier, reference string, after int64, limit int, withVia bool) (DecisionHistoryPage, error) {
	id, _, err := decisionRow(ctx, tx, reference)
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	via := `''`
	if withVia {
		via = `via`
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,revision,verdict,note,created_at,superseded_at,snapshot,`+via+` FROM decision_verdicts
		WHERE item_id=? AND id>? ORDER BY id LIMIT ?`, id, after, limit+1)
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	var verdicts []DecisionVerdict
	for rows.Next() {
		var v DecisionVerdict
		var snapshot string
		if err := rows.Scan(&v.ID, &v.Revision, &v.Verdict, &v.Note, &v.At, &v.SupersededAt, &snapshot, &v.Via); err != nil {
			rows.Close()
			return DecisionHistoryPage{}, err
		}
		if json.Unmarshal([]byte(snapshot), &v.Snapshot) != nil {
			rows.Close()
			return DecisionHistoryPage{}, workError("decision_unavailable", "a stored snapshot is invalid")
		}
		verdicts = append(verdicts, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DecisionHistoryPage{}, err
	}
	page := DecisionHistoryPage{Reference: reference, Verdicts: []DecisionVerdict{}}
	largest := int64(math.MaxInt64)
	envelope, _ := json.Marshal(map[string]any{"ok": true, "result": DecisionHistoryPage{Reference: reference, Verdicts: []DecisionVerdict{}, More: true, NextAfter: &largest}})
	used := len(envelope) + 1
	for i, v := range verdicts {
		data, _ := json.Marshal(v)
		size := len(data) + 1
		if i == limit || (len(page.Verdicts) > 0 && used+size > decisionHistoryBytes) {
			last := page.Verdicts[len(page.Verdicts)-1].ID
			page.More, page.NextAfter = true, &last
			break
		}
		used += size
		page.Verdicts = append(page.Verdicts, v)
	}
	return page, nil
}

// verdictNoticeSuffix names the decision verdicts whose typed messages a wake batch holds:
// at most decisionNoticeIDs IDs, then "+N more". The IDs are integers and carry no card
// text. The read uses the unique (recipient, sequence) key, limited to the batch range.
func (s *Store) verdictNoticeSuffix(ctx context.Context, b wakeBatch) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT decision_verdict FROM messages WHERE recipient_family=? AND recipient_id=?
		AND seq BETWEEN ? AND ? AND decision_verdict>0 ORDER BY seq`, b.Session.Family, b.Session.ID, b.First, b.Last)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	total := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		total++
		if len(ids) < decisionNoticeIDs {
			ids = append(ids, fmt.Sprint(id))
		}
	}
	if err := rows.Err(); err != nil || total == 0 {
		return "", err
	}
	suffix := " Decision verdicts " + strings.Join(ids, ", ")
	if total > len(ids) {
		suffix += fmt.Sprintf(" +%d more", total-len(ids))
	}
	return suffix + ".", nil
}
