package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Wake attempts and acknowledgements share the submission boundary: an ack committed
// before submission can never be included in a notice. Provider calls are bounded.
type wakeState struct {
	mu        sync.Mutex
	send      func(context.Context, Session, string) wakeResult
	fault     string
	registry  string
	allowed   []string
	reply     string
	cleanup   func()
	replyDone chan struct{}
}
type wakeResult struct{ State, Reason string }
type wakeBatch struct {
	Session     Session
	First, Last int64
	Attempts    int64
	// Notices is the highest count of notices that may have reached the agent (#250).
	Notices int64
	// Target is the session that the notice reaches: Session itself, or the current holder
	// of a participant's inbox (chunk 03).
	Target Session
}

const (
	wakeBackoffMax = 5 * time.Minute
	wakeBusyPoll   = 3 * time.Second
	// A notice that may have reached the agent waits for a read: 2 minutes after the first
	// notice, doubling to 16 minutes. A message that is not handled after wakeNoticeLimit
	// notices fails (#250).
	wakeReadWait    = 2 * time.Minute
	wakeReadWaitMax = 16 * time.Minute
	wakeNoticeLimit = 4
)

// unhandled selects the messages that the recipient has neither acknowledged nor read.
const unhandled = `m.seq>max(s.acked_through,s.read_through)`

// readWait is the time that the agent has to read its inbox after its n-th notice.
func readWait(notices int64) time.Duration {
	wait := wakeReadWait
	for i := int64(1); i < notices && wait < wakeReadWaitMax; i++ {
		wait *= 2
	}
	return min(wait, wakeReadWaitMax)
}

// reachedAgent reports whether a wake result may have put the notice in front of the agent:
// a provider accepted it, or the transport cannot confirm that it did not.
func reachedAgent(result wakeResult) bool {
	return result.State == "notified" ||
		result.State == "uncertain" && result.Reason != "attempt_in_progress" && result.Reason != "invalid_adapter_result"
}

func wakeBackoff(attempts int64) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 9 {
		return wakeBackoffMax
	}
	delay := time.Second * time.Duration(1<<uint(attempts-1))
	if delay > wakeBackoffMax {
		return wakeBackoffMax
	}
	return delay
}
func wakeNotice(batch wakeBatch) string {
	// Native IDs are opaque. JSON quoting prevents newlines or quoting in an ID from
	// adding instructions; the only information here is the inbox and sequence range.
	family, _ := json.Marshal(batch.Session.Family)
	id, _ := json.Marshal(batch.Session.ID)
	return fmt.Sprintf("Koinon inbox %s:%s has waiting sequence range %d-%d. Read your Koinon inbox.", family, id, batch.First, batch.Last)
}

// wakeRecipient returns an active inbox's session and the session that its notice reaches:
// the session itself, or the active holder of a participant. ok is false while there is none.
func (s *Store) wakeRecipient(ctx context.Context, key Key) (session, target Session, ok bool, err error) {
	session, err = scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, key.Family, key.ID), s.now().UnixMilli())
	if err != nil || session.State != "active" {
		return session, target, false, err
	}
	target = session
	if isParticipantID(key.ID) {
		// A participant's messages wait while no active session holds it.
		var holder, repository, role string
		if err := s.db.QueryRowContext(ctx, `SELECT holder_id,repository,role FROM names WHERE kind='alias' AND name=?`,
			key.ID[len(participantPrefix):]).Scan(&holder, &repository, &role); err != nil {
			return session, target, false, err
		}
		if holder == "" {
			return session, target, false, nil
		}
		if target, err = scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, key.Family, holder), s.now().UnixMilli()); err != nil {
			return session, target, false, err
		}
		if target.State != "active" || target.Address != key.ID[len(participantPrefix):] || !target.HoldsAddress {
			return session, target, false, nil
		}
	}
	return session, target, true, nil
}

// dueWake is the condition of the messages whose next wake is due. A time beyond the
// longest wait comes from a clock that moved back, and is due as well.
const dueWake = `m.delivery_state IN ('waiting','uncertain','notified') AND (m.wake_next_at<=? OR m.wake_next_at>?)`

func (s *Store) dueArgs() (int64, int64) {
	now := s.now().UnixMilli()
	return now, now + max(wakeBackoffMax, wakeReadWaitMax).Milliseconds()
}

func (s *Store) pendingWake(ctx context.Context, key Key) (*wakeBatch, error) {
	session, target, ok, err := s.wakeRecipient(ctx, key)
	if err != nil || !ok {
		return nil, err
	}
	var first, last sql.NullInt64
	var attempts, notices int64
	now, later := s.dueArgs()
	err = s.db.QueryRowContext(ctx, `SELECT MIN(m.seq),MAX(m.seq),COALESCE(MAX(m.wake_attempts),0),COALESCE(MAX(m.wake_notices),0)
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE m.recipient_family=? AND m.recipient_id=? AND `+unhandled+` AND `+dueWake, key.Family, key.ID, now, later).
		Scan(&first, &last, &attempts, &notices)
	if err != nil {
		return nil, err
	}
	if !first.Valid {
		return nil, nil
	}
	return &wakeBatch{session, first.Int64, last.Int64, attempts, notices, target}, nil
}
func (s *Store) recordWake(ctx context.Context, b wakeBatch, result wakeResult, attempts int64) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	delay := wakeBackoff(attempts)
	if result.State == "waiting" && result.Reason == "receiver_busy" {
		// An observed busy receiver has not failed a submission. Keep its prior
		// attempt count and poll promptly so the next idle turn is not delayed.
		attempts = b.Attempts
		delay = wakeBusyPoll
	}
	reached := 0
	if reachedAgent(result) {
		// Each message has until its own read wait ends to be read; the next notice waits.
		reached = 1
	}
	// delivery_reason says why the last notice was not handled. A new attempt for a message
	// that already had a notice, with no other reason recorded, finds it not read; a notice
	// that may have reached the agent starts with no reason.
	_, err = tx.ExecContext(ctx, `UPDATE messages SET delivery_state=?,delivery_updated_at=?,
 delivery_reason=CASE WHEN ?=1 THEN '' WHEN wake_notices>0 AND delivery_reason='' THEN 'not_read' ELSE delivery_reason END,
 wake_reason=?,wake_attempts=?,wake_notices=wake_notices+?,
 wake_next_at=CASE WHEN ?=0 THEN ? WHEN wake_notices=0 THEN ? WHEN wake_notices=1 THEN ? WHEN wake_notices=2 THEN ? ELSE ? END
 WHERE recipient_family=? AND recipient_id=? AND seq BETWEEN ? AND ?
 AND seq>(SELECT max(acked_through,read_through) FROM sessions WHERE family=? AND id=?) AND delivery_state IN ('waiting','uncertain','notified')`,
		result.State, now, reached, result.Reason, attempts, reached,
		reached, now+delay.Milliseconds(), now+readWait(1).Milliseconds(), now+readWait(2).Milliseconds(),
		now+readWait(3).Milliseconds(), now+readWait(4).Milliseconds(),
		b.Session.Family, b.Session.ID, b.First, b.Last, b.Session.Family, b.Session.ID)
	if err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}
func (s *Store) submitWake(ctx context.Context, key Key, offer bool) (string, error) {
	// Caller holds wake.mu, also held by Ack, Retire and Register. No external
	// provider response or error text is stored; reasons are fixed adapter codes.
	b, err := s.pendingWake(ctx, key)
	if err != nil || b == nil {
		return "", err
	}
	if (b.Target.Family == "agy") != offer {
		return "", nil
	}
	if b.Notices >= wakeNoticeLimit {
		if err = s.failUnhandled(ctx, *b); err != nil {
			return "", err
		}
		if b, err = s.pendingWake(ctx, key); err != nil || b == nil {
			return "", err
		}
	}
	attempts := b.Attempts + 1
	if attempts > 64 {
		attempts = 64
	}
	// Durable before the external side effect. A killed daemon leaves uncertainty
	// that the restarted worker retries, even if the provider already accepted it.
	if err = s.recordWake(ctx, *b, wakeResult{"uncertain", "attempt_in_progress"}, attempts); err != nil {
		return "", err
	}
	notice := wakeNotice(*b)
	if offer {
		return notice, nil
	}
	result := wakeResult{"waiting", "adapter_unavailable"}
	if s.wake.send != nil {
		result = s.wake.send(ctx, b.Target, notice)
	}
	if result.State != "waiting" && result.State != "notified" && result.State != "uncertain" {
		result = wakeResult{"uncertain", "invalid_adapter_result"}
	}
	if err = s.recordWake(ctx, *b, result, attempts); err != nil {
		return "", err
	}
	return notice, nil
}

// failUnhandled fails the batch's messages that had wakeNoticeLimit notices without a read,
// with the reason of the last notice, and puts one content-free notice in the maintainer's
// inbox in the same transaction. Their wakes stop; the recipient still finds them unread.
func (s *Store) failUnhandled(ctx context.Context, b wakeBatch) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now, later := s.dueArgs()
	rows, err := tx.QueryContext(ctx, `UPDATE messages AS m SET delivery_state='failed',delivery_updated_at=?,wake_next_at=0,
 delivery_reason=CASE WHEN m.delivery_reason='' THEN 'not_read' ELSE m.delivery_reason END
 FROM sessions s WHERE s.family=m.recipient_family AND s.id=m.recipient_id AND m.recipient_family=? AND m.recipient_id=?
 AND m.seq BETWEEN ? AND ? AND `+unhandled+` AND `+dueWake+` AND m.wake_notices>=? RETURNING id,delivery_reason`,
		now, b.Session.Family, b.Session.ID, b.First, b.Last, now, later, wakeNoticeLimit)
	if err != nil {
		return tx.fail(err)
	}
	var ids []int64
	reasons := map[string]bool{}
	for rows.Next() {
		var id int64
		var reason string
		if err = rows.Scan(&id, &reason); err != nil {
			rows.Close()
			return err
		}
		ids, reasons[reason] = append(ids, id), true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return tx.fail(err)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var listed []string
	for _, id := range ids[:min(len(ids), 10)] {
		listed = append(listed, strconv.FormatInt(id, 10))
	}
	if len(ids) > 10 {
		listed = append(listed, fmt.Sprintf("and %d more", len(ids)-10))
	}
	var why []string
	for reason := range reasons {
		why = append(why, reason)
	}
	sort.Strings(why)
	recipient := b.Session.Name
	if isParticipantID(b.Session.ID) {
		recipient = b.Session.ID[len(participantPrefix):]
	}
	body := fmt.Sprintf("Koinon: %s %s to %s %s not handled after %d notices (%s). Delivery state: failed.",
		plural(len(ids), "Message", "Messages"), strings.Join(listed, ", "), recipient, plural(len(ids), "was", "were"), wakeNoticeLimit, strings.Join(why, ", "))
	if err = s.noticeMaintainer(ctx, tx, now, body); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// noticeMaintainer puts a daemon notice in the maintainer's inbox. Its body is built from
// fixed text, message IDs, names and reason codes; it never holds peer text.
func (s *Store) noticeMaintainer(ctx context.Context, tx *writeTx, now int64, body string) error {
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq`,
		maintainerKey.Family, maintainerKey.ID).Scan(&seq); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,
		sender_id,sender_name,body,created_at,delivery_updated_at) VALUES (?,?,?,'koinon','koinon','koinon',?,?,?)`,
		maintainerKey.Family, maintainerKey.ID, seq, body, now, now)
	return err
}

// checkTurns records a failed turn that a Codex recipient reported after its last notice
// (#250): the reason turn_failed:CODE replaces the wait for a read, and a message at the
// notice limit fails at once. Other families report no turn result.
func (s *Store) checkTurns(ctx context.Context) error {
	keys, err := s.wakeKeys(ctx, `m.recipient_family='codex' AND m.delivery_state IN ('notified','uncertain') AND m.wake_notices>0
 AND m.delivery_reason='' AND m.wake_reason<>'attempt_in_progress'`)
	if err != nil {
		return err
	}
	for _, k := range keys {
		_, target, ok, err := s.wakeRecipient(ctx, k)
		if err != nil {
			return err
		}
		if !ok || target.Family != "codex" {
			continue
		}
		view := s.sessionObservations(ctx, target)["activity"]
		if view.Value == nil || view.Value.TurnError == "" {
			continue
		}
		now := s.now().UnixMilli()
		s.wake.mu.Lock()
		err = s.recordTurnFailure(ctx, k, view.Value.TurnError, view.Value.At, now)
		s.wake.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recordTurnFailure(ctx context.Context, k Key, code string, at, now int64) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE messages AS m SET delivery_reason=?,delivery_updated_at=?,
 wake_next_at=CASE WHEN m.wake_notices>=? THEN ? ELSE m.wake_next_at END
 FROM sessions s WHERE s.family=m.recipient_family AND s.id=m.recipient_id AND m.recipient_family=? AND m.recipient_id=?
 AND `+unhandled+` AND m.delivery_state IN ('notified','uncertain') AND m.wake_notices>0 AND m.delivery_reason=''
 AND m.wake_reason<>'attempt_in_progress' AND m.delivery_updated_at<?`,
		"turn_failed:"+code, now, wakeNoticeLimit, now, k.Family, k.ID, at); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// wakeKeys lists up to 16 active inboxes, other than Antigravity's and the maintainer's,
// with unhandled messages that match the condition.
func (s *Store) wakeKeys(ctx context.Context, condition string, args ...any) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.recipient_family,m.recipient_id FROM messages m
 JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE `+condition+` AND `+unhandled+` AND s.retired_at=0 AND s.expires_at>?
 AND s.family NOT IN ('agy','maintainer') ORDER BY m.recipient_family,m.recipient_id LIMIT 16`, append(args, s.now().UnixMilli())...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.Family, &k.ID); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) wakeStep(ctx context.Context) error {
	if err := s.checkTurns(ctx); err != nil {
		return err
	}
	now, later := s.dueArgs()
	keys, err := s.wakeKeys(ctx, dueWake, now, later)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err = ctx.Err(); err != nil {
			return err
		}
		s.wake.mu.Lock()
		_, err = s.submitWake(ctx, k, false)
		s.wake.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
func (d *Daemon) maintainWake() {
	defer d.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-d.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := d.store.wakeStep(ctx); err != nil && ctx.Err() == nil {
			d.store.wake.mu.Lock()
			d.store.wake.fault = "storage_error"
			d.store.wake.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// WakeHealth is a content-free projection. Reasons are adapter codes, never provider output.
func (s *Store) WakeHealth(ctx context.Context) (map[string]any, error) {
	var waiting, uncertain, notified, failed int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(m.delivery_state='waiting'),0),COALESCE(SUM(m.delivery_state='uncertain'),0),
 COALESCE(SUM(m.delivery_state='notified'),0),COALESCE(SUM(m.delivery_state='failed'),0)
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE `+unhandled+`
 AND s.family!='maintainer'`).Scan(&waiting, &uncertain, &notified, &failed)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT wake_reason,COUNT(*) FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE `+unhandled+` AND wake_reason<>'' GROUP BY wake_reason ORDER BY wake_reason`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reasons := map[string]int64{}
	for rows.Next() {
		var reason string
		var n int64
		if err = rows.Scan(&reason, &n); err != nil {
			return nil, err
		}
		reasons[reason] = n
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// The database reads are finished before taking the submission boundary.
	s.wake.mu.Lock()
	fault := s.wake.fault
	s.wake.mu.Unlock()
	return map[string]any{"waiting": waiting, "uncertain": uncertain, "notified": notified, "failed": failed, "reasons": reasons, "fault": fault}, nil
}

func (s *Store) closeWake() {
	s.wake.mu.Lock()
	cleanup, done := s.wake.cleanup, s.wake.replyDone
	s.wake.mu.Unlock()
	if cleanup != nil {
		cleanup()
		<-done
	}
}

// offerWake is the notice that an Antigravity session's Stop boundary offers: its own
// inbox's waiting range or, when that has none, the waiting range of the participant inbox
// that it holds. Caller holds wake.mu.
func (s *Store) offerWake(ctx context.Context, caller Key) (string, error) {
	notice, err := s.submitWake(ctx, caller, true)
	if err != nil || notice != "" {
		return notice, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	address, err := heldAddress(ctx, tx, s.now().UnixMilli(), caller)
	tx.Rollback()
	if err != nil || address == "" {
		return "", err
	}
	return s.submitWake(ctx, Key{caller.Family, participantKey(address)}, true)
}
