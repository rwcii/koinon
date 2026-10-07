package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
}

const (
	wakeBackoffMax = 5 * time.Minute
	wakeBusyPoll   = 3 * time.Second
)

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
func (s *Store) pendingWake(ctx context.Context, key Key) (*wakeBatch, error) {
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, key.Family, key.ID), s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if session.State != "active" {
		return nil, nil
	}
	var first, last sql.NullInt64
	var attempts int64
	now := s.now().UnixMilli()
	err = s.db.QueryRowContext(ctx, `SELECT MIN(m.seq),MAX(m.seq),COALESCE(MAX(m.wake_attempts),0)
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE m.recipient_family=? AND m.recipient_id=? AND m.seq>s.acked_through
 AND m.delivery_state IN ('waiting','uncertain') AND (m.wake_next_at<=? OR m.wake_next_at>?)`, key.Family, key.ID, now, now+wakeBackoffMax.Milliseconds()).Scan(&first, &last, &attempts)
	if err != nil {
		return nil, err
	}
	if !first.Valid {
		return nil, nil
	}
	return &wakeBatch{session, first.Int64, last.Int64, attempts}, nil
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
	_, err = tx.ExecContext(ctx, `UPDATE messages SET delivery_state=?,delivery_reason='',delivery_updated_at=?,
 wake_reason=?,wake_attempts=?,wake_next_at=? WHERE recipient_family=? AND recipient_id=? AND seq BETWEEN ? AND ?
 AND seq>(SELECT acked_through FROM sessions WHERE family=? AND id=?) AND delivery_state IN ('waiting','uncertain')`,
		result.State, now, result.Reason, attempts, now+delay.Milliseconds(), b.Session.Family, b.Session.ID, b.First, b.Last, b.Session.Family, b.Session.ID)
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
	if (b.Session.Family == "agy") != offer {
		return "", nil
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
		result = s.wake.send(ctx, b.Session, notice)
	}
	if result.State != "waiting" && result.State != "notified" && result.State != "uncertain" {
		result = wakeResult{"uncertain", "invalid_adapter_result"}
	}
	if err = s.recordWake(ctx, *b, result, attempts); err != nil {
		return "", err
	}
	return notice, nil
}
func (s *Store) wakeStep(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.recipient_family,m.recipient_id FROM messages m
 JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
 WHERE m.delivery_state IN ('waiting','uncertain') AND m.seq>s.acked_through AND s.retired_at=0 AND s.expires_at>?
 AND s.family<>'agy' AND (m.wake_next_at<=? OR m.wake_next_at>?) ORDER BY m.recipient_family,m.recipient_id LIMIT 16`, s.now().UnixMilli(), s.now().UnixMilli(), s.now().UnixMilli()+wakeBackoffMax.Milliseconds())
	if err != nil {
		return err
	}
	var keys []Key
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.Family, &k.ID); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
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
	var waiting, uncertain, notified int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(m.delivery_state='waiting'),0),COALESCE(SUM(m.delivery_state='uncertain'),0),COALESCE(SUM(m.delivery_state='notified'),0)
 FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE m.seq>s.acked_through`).Scan(&waiting, &uncertain, &notified)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT wake_reason,COUNT(*) FROM messages m JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id WHERE m.seq>s.acked_through AND wake_reason<>'' GROUP BY wake_reason ORDER BY wake_reason`)
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
	return map[string]any{"waiting": waiting, "uncertain": uncertain, "notified": notified, "reasons": reasons, "fault": fault}, nil
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
