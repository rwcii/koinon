package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	mu           sync.Mutex
	send         func(context.Context, Session, string) wakeResult
	fault        string
	currentFault *wakeFault
	lastFault    *wakeFault
	registry     string
	allowed      []string
	reply        string
	cleanup      func()
	replyDone    chan struct{}
	// turns is the time of each Codex session's failed turn that checkTurns applied.
	turns map[Key]int64
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
	if errors.Is(err, ErrMissing) {
		// A participant's inbox row exists once a message was sent to its address.
		return session, target, false, nil
	}
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
	// The batch is the first run of due messages: it stops before an unhandled message whose
	// wait has not ended, so a notice's range names only due messages. A later run is the
	// next batch.
	err = s.db.QueryRowContext(ctx, `WITH pending AS (SELECT m.seq,m.wake_attempts,m.wake_notices,(`+dueWake+`) AS due
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE m.recipient_family=? AND m.recipient_id=? AND `+unhandled+`),
 start AS (SELECT MIN(seq) AS first FROM pending WHERE due),
 gap AS (SELECT MIN(p.seq) AS seq FROM pending p,start WHERE NOT p.due AND p.seq>start.first)
 SELECT MIN(p.seq),MAX(p.seq),COALESCE(MAX(p.wake_attempts),0),COALESCE(MAX(p.wake_notices),0)
 FROM pending p,start,gap WHERE p.due AND (gap.seq IS NULL OR p.seq<gap.seq)`, now, later, key.Family, key.ID).
		Scan(&first, &last, &attempts, &notices)
	if err != nil {
		return nil, err
	}
	if !first.Valid {
		return nil, nil
	}
	return &wakeBatch{session, first.Int64, last.Int64, attempts, notices, target}, nil
}

// recordWake records an attempt's start (attempt_in_progress) on the batch's due messages,
// or its result on the messages that its start marked. A message in the range whose wait
// has not ended keeps its count and wait (#250).
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
	marked := `delivery_state='uncertain' AND wake_reason='attempt_in_progress'`
	args := []any{}
	if result.Reason == "attempt_in_progress" {
		marked = `delivery_state IN ('waiting','uncertain','notified') AND (wake_next_at<=? OR wake_next_at>?)`
		due, later := s.dueArgs()
		args = append(args, due, later)
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET delivery_state=?,delivery_updated_at=?,
 delivery_reason=CASE WHEN ?=1 THEN '' WHEN wake_notices>0 AND delivery_reason='' THEN 'not_read' ELSE delivery_reason END,
 wake_reason=?,wake_attempts=?,wake_notices=wake_notices+?,
 wake_next_at=CASE WHEN ?=0 THEN ? WHEN wake_notices=0 THEN ? WHEN wake_notices=1 THEN ? WHEN wake_notices=2 THEN ? ELSE ? END
 WHERE recipient_family=? AND recipient_id=? AND seq BETWEEN ? AND ?
 AND seq>(SELECT max(acked_through,read_through) FROM sessions WHERE family=? AND id=?) AND `+marked,
		append([]any{result.State, now, reached, result.Reason, attempts, reached,
			reached, now + delay.Milliseconds(), now + readWait(1).Milliseconds(), now + readWait(2).Milliseconds(),
			now + readWait(3).Milliseconds(), now + readWait(4).Milliseconds(),
			b.Session.Family, b.Session.ID, b.First, b.Last, b.Session.Family, b.Session.ID}, args...)...)
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
	// A managed context cycle owns the target's pane from ready through pickup (#196): the
	// wake waits, still due and not delivered, so no notice text interleaves with its input.
	if held, err := s.paneHeldByCycle(ctx, Key{b.Target.Family, b.Target.ID}); err != nil || held {
		return "", err
	}
	if b.Notices >= wakeNoticeLimit {
		if err = s.failUnhandled(ctx, key); err != nil {
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
	// A range that holds decision verdict messages names their verdict IDs (#206, chunk 02).
	suffix, err := s.verdictNoticeSuffix(ctx, *b)
	if err != nil {
		return "", err
	}
	notice += suffix
	// A range that holds an unfinished managed cycle's request names the cycle (#445).
	if suffix, err = s.cycleNoticeSuffix(ctx, *b); err != nil {
		return "", err
	}
	notice += suffix
	if offer {
		// The Stop hook returns the notice to the agent's turn: it counts as a notice (#250).
		if err = s.recordWake(ctx, *b, uncertain("stop_offered"), attempts); err != nil {
			return "", err
		}
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

// failUnhandled fails an inbox's due messages that had wakeNoticeLimit notices without a
// read, with the reason of the last notice, and puts one content-free notice in the
// maintainer's inbox in the same transaction. Their wakes stop; the recipient still finds
// them unread. Caller holds wake.mu.
func (s *Store) failUnhandled(ctx context.Context, key Key) error {
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, key.Family, key.ID), s.now().UnixMilli())
	if err != nil {
		return err
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now, later := s.dueArgs()
	rows, err := tx.QueryContext(ctx, `UPDATE messages AS m SET delivery_state='failed',delivery_updated_at=?,wake_next_at=0,
 delivery_reason=CASE WHEN m.delivery_reason='' THEN 'not_read' ELSE m.delivery_reason END
 FROM sessions s WHERE s.family=m.recipient_family AND s.id=m.recipient_id AND m.recipient_family=? AND m.recipient_id=?
 AND `+unhandled+` AND `+dueWake+` AND m.wake_notices>=? RETURNING id,delivery_reason`,
		now, key.Family, key.ID, now, later, wakeNoticeLimit)
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
	recipient := session.Name
	if isParticipantID(key.ID) {
		recipient = key.ID[len(participantPrefix):]
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

// checkTurns records a failed turn that a Codex session reported after a message's last
// notice (#250), in the session's own inbox and in the participant inbox that it holds: the
// reason turn_failed:CODE replaces not_read, and a message at the notice limit fails at
// once. Each reported failure is applied once. Other families report no turn result.
func (s *Store) checkTurns(ctx context.Context) error {
	type failure struct {
		key  Key
		code string
		at   int64
	}
	var failures []failure
	s.observed.mu.Lock()
	for key, groups := range s.observed.byKey {
		if v, ok := groups["activity"]; ok && key.Family == "codex" && v.TurnError != "" {
			failures = append(failures, failure{key, v.TurnError, v.At})
		}
	}
	s.observed.mu.Unlock()
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	seen := map[Key]int64{}
	for _, f := range failures {
		if s.wake.turns[f.key] == f.at {
			seen[f.key] = f.at
			continue
		}
		session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, f.key.Family, f.key.ID), s.now().UnixMilli())
		if errors.Is(err, ErrMissing) {
			continue
		}
		if err != nil {
			return err
		}
		if session.State == "active" {
			inboxes := []Key{f.key}
			if session.Alias != "" {
				inboxes = append(inboxes, Key{f.key.Family, participantKey(session.Alias)})
			}
			for _, inbox := range inboxes {
				if err := s.recordTurnFailure(ctx, inbox, f.code, f.at, s.now().UnixMilli()); err != nil {
					return err
				}
			}
		}
		seen[f.key] = f.at
	}
	s.wake.turns = seen
	return nil
}

// recordTurnFailure applies one failed turn to an inbox's messages noticed before it.
// Caller holds wake.mu.
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

// wakeKeys lists up to 16 active inboxes, other than the maintainer's and, unless agy is
// set, Antigravity's, with unhandled messages that match the condition.
func (s *Store) wakeKeys(ctx context.Context, agy bool, condition string, args ...any) ([]Key, error) {
	excluded := `('agy','maintainer')`
	if agy {
		excluded = `('maintainer')`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.recipient_family,m.recipient_id FROM messages m
 JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE `+condition+` AND `+unhandled+` AND s.retired_at=0 AND s.expires_at>?
 AND s.family NOT IN `+excluded+` ORDER BY m.recipient_family,m.recipient_id LIMIT 16`, append(args, s.now().UnixMilli())...)
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
		return wakePassError{"check_turns", err}
	}
	// Messages at the notice limit fail when their wait ends, in every family's inbox,
	// Antigravity's too, and whether or not a participant has an active holder. They leave
	// this set, so they never hold the 16 places.
	now, later := s.dueArgs()
	exhausted, err := s.wakeKeys(ctx, true, dueWake+` AND m.wake_notices>=?`, now, later, wakeNoticeLimit)
	if err != nil {
		return wakePassError{"list_exhausted", err}
	}
	for _, k := range exhausted {
		s.wake.mu.Lock()
		err = s.failUnhandled(ctx, k)
		s.wake.mu.Unlock()
		if err != nil {
			return wakePassError{"fail_unhandled", err}
		}
	}
	keys, err := s.wakeKeys(ctx, false, dueWake, now, later)
	if err != nil {
		return wakePassError{"list_due", err}
	}
	for _, k := range keys {
		if err = ctx.Err(); err != nil {
			return err
		}
		s.wake.mu.Lock()
		_, err = s.submitWake(ctx, k, false)
		s.wake.mu.Unlock()
		if err != nil {
			return wakePassError{"submit_wake", err}
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
		d.recordWakePass(ctx, d.wakePass(ctx))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// WakeHealth is a content-free projection. Reasons are adapter codes, never provider output.
// It counts the messages to sessions that are not retired, which the wake loop retries (#486).
func (s *Store) WakeHealth(ctx context.Context) (map[string]any, error) {
	var waiting, uncertain, notified, failed int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(m.delivery_state='waiting'),0),COALESCE(SUM(m.delivery_state='uncertain'),0),
 COALESCE(SUM(m.delivery_state='notified'),0),COALESCE(SUM(m.delivery_state='failed'),0)
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE `+unhandled+`
 AND s.retired_at=0 AND s.family!='maintainer'`).Scan(&waiting, &uncertain, &notified, &failed)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT wake_reason,COUNT(*) FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE `+unhandled+` AND s.retired_at=0 AND wake_reason<>'' GROUP BY wake_reason ORDER BY wake_reason`)
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
	var lastFault *wakeFault
	if s.wake.lastFault != nil {
		copy := *s.wake.lastFault
		copy.ErrorTypes = append([]string(nil), copy.ErrorTypes...)
		if copy.SQLiteCode != nil {
			code := *copy.SQLiteCode
			copy.SQLiteCode = &code
		}
		lastFault = &copy
	}
	s.wake.mu.Unlock()
	return map[string]any{"waiting": waiting, "uncertain": uncertain, "notified": notified, "failed": failed, "reasons": reasons, "fault": fault, "last_fault": lastFault}, nil
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
