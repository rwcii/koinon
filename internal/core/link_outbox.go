package core

// The message queue between paired computers (#670 chunk 03, LINK-670-OFFLINE): the sending
// daemon's outbox and the ordered stream of each pair, in this daemon's private store.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// Schema 38: the outbox, the streams and the link origin of a received message.
const linkOutboxTablesV38 = `
	CREATE TABLE link_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		xid TEXT NOT NULL UNIQUE,
		pair_id TEXT NOT NULL,
		sender_family TEXT NOT NULL, sender_id TEXT NOT NULL, sender_name TEXT NOT NULL,
		recipient TEXT NOT NULL, computer TEXT NOT NULL, body TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		seq INTEGER, digest TEXT,
		state TEXT NOT NULL CHECK (state IN ('queued','sending','uncertain','received','refused','cancelled')),
		reason TEXT NOT NULL DEFAULT '',
		cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK (cancel_requested IN (0,1)),
		unknown INTEGER NOT NULL DEFAULT 0 CHECK (unknown IN (0,1)),
		updated_at INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0,
		UNIQUE (pair_id, seq)
	);
	CREATE INDEX link_outbox_head ON link_outbox(pair_id, state, id);
	CREATE INDEX link_outbox_finished ON link_outbox(finished_at);
	CREATE TABLE link_streams (
		pair_id TEXT NOT NULL,
		direction TEXT NOT NULL CHECK (direction IN ('out','in')),
		next_seq INTEGER NOT NULL DEFAULT 1,
		high_water INTEGER NOT NULL DEFAULT 0,
		last_result TEXT NOT NULL DEFAULT '', last_reason TEXT NOT NULL DEFAULT '', last_digest TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (pair_id, direction)
	);
	ALTER TABLE messages ADD COLUMN link_pair TEXT;
	ALTER TABLE messages ADD COLUMN link_xid TEXT;
	ALTER TABLE messages ADD COLUMN link_sender TEXT;
	CREATE UNIQUE INDEX messages_link ON messages(link_pair, link_xid) WHERE link_xid IS NOT NULL;`

// Queue limits (LINK-670-OFFLINE item 8): unfinished messages and their body bytes, for each
// pair and for all pairs. A send at a limit is refused; nothing queued is dropped.
const (
	linkQueuePairCount  = 1000
	linkQueuePairBytes  = 16 << 20
	linkQueueTotalCount = 10000
	linkQueueTotalBytes = 64 << 20
	// linkOutboxKeepMS keeps a finished queue entry 30 days after its result.
	linkOutboxKeepMS = 30 * 24 * 60 * 60 * 1000
)

var (
	ErrLinkQueueFull     = Refusal{"link_queue_full", "the queue to other computers is full; nothing was queued"}
	ErrLinkMessageGone   = Refusal{"link_message_not_found", "no queued message to another computer has that ID"}
	ErrLinkReplyPairGone = Refusal{"link_reply_pair_gone", "the computer that this message came from is no longer paired"}
	ErrLinkReplyTarget   = Refusal{"link_reply_target", "reply_to names no message from another computer that this session can read, or to does not match it"}
	ErrLinkReplyLocal    = Refusal{"link_reply_local", "reply_to names a message from this computer; send to its sender by name"}
)

// linkUnfinished are the states whose message still waits for its result.
const linkUnfinished = `('queued','sending','uncertain')`

// LinkQueued is one entry of the queue to another computer, without its body.
type LinkQueued struct {
	LinkID    string `json:"link_id"`
	Recipient string `json:"recipient"`
	Sender    string `json:"sender"`
	Bytes     int    `json:"bytes"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Cancel    bool   `json:"cancel_requested,omitempty"`
	Created   int64  `json:"created_at"`
	Updated   int64  `json:"updated_at"`
	Finished  int64  `json:"finished_at,omitempty"`
}

// linkHead is the message at the front of a pair's outgoing stream.
type linkHead struct {
	ID        int64
	XID       string
	Seq       int64
	Digest    string
	Recipient string
	Sender    string
	Family    string
	Body      string
	State     string
	Cancel    bool
}

// linkDigest binds a sequence to its message: SHA-256 over the length-prefixed sequence,
// cross-node ID, recipient, sender, family and body.
func linkDigest(seq int64, xid, recipient, sender, family, body string) string {
	h := sha256.New()
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(seq))
	h.Write(n[:])
	for _, f := range []string{xid, recipient, sender, family, body} {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// linkEnqueue stores a send to NAME@COMPUTER in the outbox, after the caller and the limits
// are checked, and answers queued with the cross-node ID (LINK-670-OFFLINE item 1).
func (s *Store) linkEnqueue(ctx context.Context, caller Key, pairID, computer, name, body string) (Outcome, error) {
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := activeCaller(ctx, tx.Tx, now, caller, false); err != nil {
		return Outcome{}, err
	}
	sender, err := senderName(ctx, tx.Tx, now, caller)
	if err != nil {
		return Outcome{}, err
	}
	var pairCount, pairBytes, totalCount, totalBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(body AS BLOB))),0),
		COALESCE(SUM(pair_id=?),0),COALESCE(SUM(CASE WHEN pair_id=? THEN length(CAST(body AS BLOB)) ELSE 0 END),0)
		FROM link_outbox WHERE state IN `+linkUnfinished, pairID, pairID).Scan(&totalCount, &totalBytes, &pairCount, &pairBytes); err != nil {
		return Outcome{}, err
	}
	size := int64(len(body))
	if pairCount+1 > linkQueuePairCount || pairBytes+size > linkQueuePairBytes ||
		totalCount+1 > linkQueueTotalCount || totalBytes+size > linkQueueTotalBytes {
		return Outcome{}, ErrLinkQueueFull
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Outcome{}, err
	}
	xid := hex.EncodeToString(raw[:])
	if _, err := tx.ExecContext(ctx, `INSERT INTO link_outbox(xid,pair_id,sender_family,sender_id,sender_name,recipient,computer,
		body,created_at,state,updated_at) VALUES (?,?,?,?,?,?,?,?,?,'queued',?)`,
		xid, pairID, caller.Family, caller.ID, sender, name, computer, body, now, now); err != nil {
		return Outcome{}, tx.fail(err)
	}
	if err := tx.Commit(); err != nil {
		return Outcome{}, err
	}
	return Outcome{Recipient: name + "@" + computer, DeliveryState: "queued", LinkID: xid, UpdatedAt: now}, nil
}

// linkShownState is the state a sender sees: a message on its way shows queued until a frame
// was written without a matching answer (LINK-670-OFFLINE item 7).
func linkShownState(state string) string {
	switch state {
	case "sending":
		return "queued"
	case "uncertain":
		return "may_have_been_delivered"
	}
	return state
}

// linkOutcome reads a queued message's state for the session that sent it, or for the
// current holder of the participant address that sent it.
func (s *Store) linkOutcome(ctx context.Context, caller Key, xid string) (Outcome, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return Outcome{}, err
	}
	held, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return Outcome{}, err
	}
	var o Outcome
	var name, computer, state string
	err = tx.QueryRowContext(ctx, `SELECT recipient,computer,state,reason,updated_at FROM link_outbox
		WHERE xid=? AND ((sender_family=? AND sender_id=?) OR (?<>'' AND sender_name=?))`,
		xid, caller.Family, caller.ID, held, held).Scan(&name, &computer, &state, &o.DeliveryReason, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrLinkMessageGone
	}
	if err != nil {
		return Outcome{}, err
	}
	o.Recipient, o.DeliveryState, o.LinkID = name+"@"+computer, linkShownState(state), xid
	return o, nil
}

// linkReplyTarget reads the link origin of a message that the caller can read: its own inbox
// or the inbox of the participant it holds.
func (s *Store) linkReplyTarget(ctx context.Context, caller Key, id int64) (pairID, sender string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return "", "", err
	}
	held, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return "", "", err
	}
	var pair, from sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT link_pair,link_sender FROM messages WHERE id=? AND
		((recipient_family=? AND recipient_id=?) OR (?<>'' AND recipient_id=?))`,
		id, caller.Family, caller.ID, held, participantKey(held)).Scan(&pair, &from)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrLinkReplyTarget
	}
	if err != nil {
		return "", "", err
	}
	if !pair.Valid {
		return "", "", ErrLinkReplyLocal
	}
	return pair.String, from.String, nil
}

// linkNextHead returns the pair's head: its oldest unfinished message. A head without a
// sequence gets the stream's next one and its digest in the same transaction, so a cross-node
// ID is bound to one sequence and a sequence is never reused (stream rule 1).
func (s *Store) linkNextHead(ctx context.Context, pairID string) (linkHead, bool, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return linkHead{}, false, err
	}
	defer tx.Rollback()
	var h linkHead
	var seq sql.NullInt64
	var digest sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,xid,seq,digest,recipient,sender_name,sender_family,body,state,cancel_requested
		FROM link_outbox WHERE pair_id=? AND state IN `+linkUnfinished+` ORDER BY id LIMIT 1`, pairID).
		Scan(&h.ID, &h.XID, &seq, &digest, &h.Recipient, &h.Sender, &h.Family, &h.Body, &h.State, &h.Cancel)
	if errors.Is(err, sql.ErrNoRows) {
		return linkHead{}, false, nil
	}
	if err != nil {
		return linkHead{}, false, err
	}
	if seq.Valid {
		h.Seq, h.Digest = seq.Int64, digest.String
		return h, true, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO link_streams(pair_id,direction) VALUES (?,'out')`, pairID); err != nil {
		return linkHead{}, false, tx.fail(err)
	}
	if err := tx.QueryRowContext(ctx, `UPDATE link_streams SET next_seq=next_seq+1 WHERE pair_id=? AND direction='out'
		RETURNING next_seq-1`, pairID).Scan(&h.Seq); err != nil {
		return linkHead{}, false, tx.fail(err)
	}
	h.Digest = linkDigest(h.Seq, h.XID, h.Recipient, h.Sender, h.Family, h.Body)
	h.State = "sending"
	if _, err := tx.ExecContext(ctx, `UPDATE link_outbox SET seq=?,digest=?,state='sending',updated_at=? WHERE id=?`,
		h.Seq, h.Digest, s.now().UnixMilli(), h.ID); err != nil {
		return linkHead{}, false, tx.fail(err)
	}
	return h, true, tx.Commit()
}

// linkSettle stores a head's terminal result, only while the row still has that sequence and
// digest and waits for its result (compare and set). It reports whether it stored it.
func (s *Store) linkSettle(ctx context.Context, pairID string, seq int64, digest, result, reason string) (bool, error) {
	if result != "received" && result != "refused" && result != "cancelled" {
		return false, ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE link_outbox SET state=?,reason=?,updated_at=?,finished_at=?
		WHERE pair_id=? AND seq=? AND digest=? AND state IN ('sending','uncertain')`, result, reason, now, now, pairID, seq, digest)
	if err != nil {
		return false, tx.fail(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.Commit()
}

// linkAttempt stores the attempt intent before a frame for the head is written: the head is
// uncertain from then until a matching answer, so a crash, Remote off or a closed generation
// after the write never shows it as unattempted (LINK-670-OFFLINE item 7). It reports whether
// the head still waits for its result with that sequence and digest.
func (s *Store) linkAttempt(ctx context.Context, pairID string, seq int64, digest string) (bool, error) {
	// An attempt that begins while an earlier one is still unresolved makes the earlier result
	// unknown for good: a delayed old handler can still commit it.
	return s.linkMark(ctx, `UPDATE link_outbox SET unknown=CASE WHEN state='uncertain' THEN 1 ELSE unknown END,
		state='uncertain',updated_at=? WHERE pair_id=? AND seq=? AND digest=? AND state IN ('sending','uncertain')`, pairID, seq, digest)
}

// linkRetried moves the head back to sending after a matching retry of an attempt that
// began with no earlier unknown attempt. With an earlier unknown attempt, or a requested
// cancel, the head stays uncertain until a matching terminal result.
func (s *Store) linkRetried(ctx context.Context, pairID string, seq int64, digest string) (bool, error) {
	return s.linkMark(ctx, `UPDATE link_outbox SET state='sending',updated_at=? WHERE pair_id=? AND seq=? AND digest=?
		AND state='uncertain' AND cancel_requested=0 AND unknown=0`, pairID, seq, digest)
}

func (s *Store) linkMark(ctx context.Context, query, pairID string, seq int64, digest string) (bool, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, s.now().UnixMilli(), pairID, seq, digest)
	if err != nil {
		return false, tx.fail(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.Commit()
}

// linkCancel is the maintainer's cancel of a queued message. A message without a sequence is
// cancelled at once. A message with a sequence is resolved by an ordered cancel on the link,
// and shows "may have been delivered" until then (stream rule 4). A finished one is unchanged.
// It returns the pair, so the worker can act.
func (s *Store) linkCancel(ctx context.Context, xid string) (string, LinkQueued, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return "", LinkQueued{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	var pair, state string
	var seq sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT pair_id,state,seq FROM link_outbox WHERE xid=?`, xid).Scan(&pair, &state, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", LinkQueued{}, ErrLinkMessageGone
	}
	if err != nil {
		return "", LinkQueued{}, err
	}
	switch {
	case state == "queued" && !seq.Valid:
		_, err = tx.ExecContext(ctx, `UPDATE link_outbox SET state='cancelled',reason='cancelled',updated_at=?,finished_at=? WHERE xid=?`, now, now, xid)
	case state == "sending" || state == "uncertain":
		_, err = tx.ExecContext(ctx, `UPDATE link_outbox SET state='uncertain',cancel_requested=1,updated_at=? WHERE xid=?`, now, xid)
	}
	if err != nil {
		return "", LinkQueued{}, tx.fail(err)
	}
	q, err := linkQueuedRow(ctx, tx.Tx, `xid=?`, xid)
	if err != nil {
		return "", LinkQueued{}, err
	}
	return pair, q, tx.Commit()
}

func linkQueuedRow(ctx context.Context, q querier, where string, args ...any) (LinkQueued, error) {
	rows, err := linkQueuedRows(ctx, q, where, args...)
	if err != nil {
		return LinkQueued{}, err
	}
	if len(rows) == 0 {
		return LinkQueued{}, ErrLinkMessageGone
	}
	return rows[0], nil
}

func linkQueuedRows(ctx context.Context, q querier, where string, args ...any) ([]LinkQueued, error) {
	rows, err := q.QueryContext(ctx, `SELECT xid,recipient,computer,sender_name,length(CAST(body AS BLOB)),state,reason,cancel_requested,
		created_at,updated_at,finished_at FROM link_outbox WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LinkQueued
	for rows.Next() {
		var e LinkQueued
		var name, computer string
		if err := rows.Scan(&e.LinkID, &name, &computer, &e.Sender, &e.Bytes, &e.State, &e.Reason, &e.Cancel, &e.Created, &e.Updated, &e.Finished); err != nil {
			return nil, err
		}
		e.Recipient, e.State = name+"@"+computer, linkShownState(e.State)
		out = append(out, e)
	}
	return out, rows.Err()
}

// linkQueue lists a pair's queue: its unfinished messages and the finished ones that
// retention keeps, oldest first, at most limit of them.
func (s *Store) linkQueue(ctx context.Context, pairID string, limit int) ([]LinkQueued, error) {
	return linkQueuedRows(ctx, s.db, `id IN (SELECT id FROM link_outbox WHERE pair_id=? ORDER BY id DESC LIMIT ?)`, pairID, limit)
}

// linkRetire ends a pair's queue at unpair (LINK-670-OFFLINE item 7): a message without a
// sequence is cancelled, a message with one may have been delivered, and the pair's streams
// are deleted. A new pairing has a new pair ID and starts empty.
func (s *Store) linkRetire(ctx context.Context, pairID string) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE link_outbox SET state=CASE WHEN seq IS NULL THEN 'cancelled' ELSE 'uncertain' END,
		reason='unpaired',updated_at=?,finished_at=? WHERE pair_id=? AND state IN `+linkUnfinished, now, now, pairID); err != nil {
		return tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_streams WHERE pair_id=?`, pairID); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// linkPairIDs lists the pairs that have live queue entries or streams, for the
// reconciliation with the link state after a crash between an unpair's two writes.
func (s *Store) linkPairIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pair_id FROM link_outbox WHERE state IN `+linkUnfinished+`
		UNION SELECT pair_id FROM link_streams`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// linkPending lists the pairs with unfinished messages, for the delivery workers.
func (s *Store) linkPending(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT pair_id FROM link_outbox WHERE state IN `+linkUnfinished)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// linkShowComputer shows a pair's new local name on the messages that came from it.
func (s *Store) linkShowComputer(ctx context.Context, pairID, computer string) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET sender_name=link_sender||'@'||? WHERE link_pair=?
		AND sender_name<>link_sender||'@'||?`, computer, pairID, computer); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// cullLinkOutbox deletes finished queue entries 30 days after their result; unfinished ones
// never age out (LINK-670-OFFLINE item 8).
func (s *Store) cullLinkOutbox(ctx context.Context) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_outbox WHERE finished_at>0 AND finished_at<=?`,
		s.now().UnixMilli()-linkOutboxKeepMS); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// linkDelivery is one deliver or cancel operation that arrived on a pair's stream.
type linkDelivery struct {
	Pair      string
	Computer  string // this computer's effective name for the pair
	Cancel    bool
	Seq       int64
	XID       string
	Recipient string
	Sender    string
	Family    string
	Body      string
	Digest    string
}

// linkResult is the receiver's answer to a deliver or cancel.
type linkResult struct {
	Result string // received, refused, cancelled, retry or stale
	Reason string
}

// errLinkProtocol is a stream violation: a gap, a changed digest, or a cross-node ID that the
// stream already holds at another sequence. Nothing is stored.
var errLinkProtocol = errors.New("link_protocol_error")

// linkReceive applies one terminal operation to the pair's incoming stream (stream rules 2, 3
// and 5). At high-water+1 it stores the result, the digest and the new high-water in one
// transaction, with the inbox insert only for a receipt; at high-water it answers the stored
// result again; below it answers stale. A busy or full store answers retry and changes nothing.
func (s *Store) linkReceive(ctx context.Context, in linkDelivery) (linkResult, error) {
	if in.Cancel {
		in.Recipient, in.Sender, in.Family, in.Body = "", "", "", ""
	}
	tx, err := s.begin(ctx, ordinary)
	if errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
		return linkResult{Result: "retry", Reason: errorCode(err)}, nil
	}
	if err != nil {
		return linkResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO link_streams(pair_id,direction) VALUES (?,'in')`, in.Pair); err != nil {
		return linkResult{}, tx.fail(err)
	}
	var high int64
	var last, lastReason, lastDigest string
	if err := tx.QueryRowContext(ctx, `SELECT high_water,last_result,last_reason,last_digest FROM link_streams
		WHERE pair_id=? AND direction='in'`, in.Pair).Scan(&high, &last, &lastReason, &lastDigest); err != nil {
		return linkResult{}, err
	}
	switch {
	case in.Seq == high && high > 0:
		if in.Digest != lastDigest {
			return linkResult{}, errLinkProtocol
		}
		return linkResult{Result: last, Reason: lastReason}, nil
	case in.Seq <= high:
		return linkResult{Result: "stale"}, nil
	case in.Seq != high+1:
		return linkResult{}, errLinkProtocol
	}
	result := linkResult{Result: "cancelled"}
	if !in.Cancel {
		result, err = s.linkInsert(ctx, tx, in)
		if err != nil {
			if errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
				return linkResult{Result: "retry", Reason: errorCode(err)}, nil
			}
			return linkResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE link_streams SET high_water=?,last_result=?,last_reason=?,last_digest=?
		WHERE pair_id=? AND direction='in'`, in.Seq, result.Result, result.Reason, in.Digest, in.Pair); err != nil {
		return linkResult{}, tx.fail(err)
	}
	if err := tx.Commit(); errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
		return linkResult{Result: "retry", Reason: errorCode(err)}, nil
	} else if err != nil {
		return linkResult{}, err
	}
	return result, nil
}

// linkInsert resolves NAME as a local send does and inserts the message with its link origin.
// A permanent refusal is a result, not an error.
func (s *Store) linkInsert(ctx context.Context, tx *writeTx, in linkDelivery) (linkResult, error) {
	// A remote message never has the maintainer's or overwatch's origin (LINK-670-WHAT item 3).
	if in.Family == "" || in.Family == "maintainer" || in.Family == "overwatch" {
		return linkResult{Result: "refused", Reason: "link_origin_refused"}, nil
	}
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE link_pair=? AND link_xid=?`, in.Pair, in.XID).Scan(&held); err != nil {
		return linkResult{}, err
	}
	if held > 0 {
		return linkResult{}, errLinkProtocol
	}
	now := s.now().UnixMilli()
	family, sessionID, err := s.resolveRecipient(ctx, tx, now, in.Recipient)
	if err == nil {
		var seq int64
		if seq, err = s.nextInboxSeq(ctx, tx, now, family, sessionID); err == nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,
				sender_name,body,created_at,delivery_updated_at,link_pair,link_xid,link_sender) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				family, sessionID, seq, in.Family, "link:"+in.Pair, in.Sender+"@"+in.Computer, in.Body, now, now,
				in.Pair, in.XID, in.Sender); err != nil {
				return linkResult{}, tx.fail(err)
			}
			if err := bumpInbox(ctx, tx, Key{family, sessionID}); err != nil {
				return linkResult{}, tx.fail(err)
			}
			return linkResult{Result: "received"}, nil
		}
	}
	switch {
	case errors.Is(err, ErrPeerNotFound):
		return linkResult{Result: "refused", Reason: "peer_not_found"}, nil
	case errors.Is(err, ErrAliasUnheld):
		return linkResult{Result: "refused", Reason: "alias_unheld"}, nil
	case errors.Is(err, ErrRecipientInactive):
		return linkResult{Result: "refused", Reason: "recipient_inactive"}, nil
	}
	return linkResult{}, err
}
