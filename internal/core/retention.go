package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Retention (#216). The maintenance sweep deletes acknowledged messages and inactive
// sessions after their retention periods, and the sessions that the maintainer marked for
// purge. Each deletion is a control write in its own short transaction, so a sweep never
// holds the storage boundary longer than one bounded batch.

const (
	// messageRetention runs from the sweep's acknowledgement mark: a message is deleted
	// between one and two periods after its acknowledgement.
	messageRetention = 30 * 24 * time.Hour
	// sessionRetention runs from the end of a session's activity: its expiry, or its
	// retirement when it was retired.
	sessionRetention = 30 * 24 * time.Hour
	// cullBatch messages are deleted per transaction, at most cullBudget per sweep, and at
	// most cullSessions sessions are deleted per sweep.
	cullBatch    = 500
	cullBudget   = 20 * cullBatch
	cullSessions = 100
	// purgeCheckpoint is the checkpoint of a claim that a purge releases.
	purgeCheckpoint = "Released by the maintainer's purge of the session"
)

// RetentionCounts are the results of one sweep: the records it deleted, by rule, and the
// sessions past their retention period that it kept.
type RetentionCounts struct {
	MessagesByRetention int64 `json:"messages_by_retention"`
	MessagesByPurge     int64 `json:"messages_by_purge"`
	SessionsByRetention int64 `json:"sessions_by_retention"`
	SessionsByPurge     int64 `json:"sessions_by_purge"`
	HeldUnacknowledged  int64 `json:"held_unacknowledged"`
	HeldClaims          int64 `json:"held_claims"`
}

// RetentionStatus reports the retention periods, the last completed sweep and the sessions
// that wait for purge.
type RetentionStatus struct {
	MessageRetentionDays int64            `json:"message_retention_days"`
	SessionRetentionDays int64            `json:"session_retention_days"`
	LastSweep            *int64           `json:"last_sweep"`
	Last                 *RetentionCounts `json:"last"`
	MarkedForPurge       int64            `json:"marked_for_purge"`
	Fault                *string          `json:"fault"`
	FaultAt              *int64           `json:"fault_at"`
}

type retentionState struct {
	mu sync.Mutex
	// messages and sessions are the retention periods; zero means the default.
	messages, sessions time.Duration
	lastSweep          *int64
	last               *RetentionCounts
	fault              *string
	faultAt            *int64
}

func (s *Store) retentionPeriods() (time.Duration, time.Duration) {
	s.retention.mu.Lock()
	defer s.retention.mu.Unlock()
	messages, sessions := s.retention.messages, s.retention.sessions
	if messages == 0 {
		messages = messageRetention
	}
	if sessions == 0 {
		sessions = sessionRetention
	}
	return messages, sessions
}

// Cull runs one retention sweep: purges first, then acknowledged messages, then inactive
// sessions. A failure keeps the progress of earlier transactions and is reported.
func (s *Store) Cull(ctx context.Context) error {
	var counts RetentionCounts
	err := s.cull(ctx, &counts)
	now := s.now().UnixMilli()
	s.retention.mu.Lock()
	defer s.retention.mu.Unlock()
	if err != nil {
		code := errorCode(err)
		s.retention.fault, s.retention.faultAt = &code, &now
		return err
	}
	s.retention.lastSweep, s.retention.last, s.retention.fault, s.retention.faultAt = &now, &counts, nil, nil
	return nil
}

func (s *Store) cull(ctx context.Context, counts *RetentionCounts) error {
	budget := int64(cullBudget)
	if err := s.purgeMarked(ctx, counts, &budget); err != nil {
		return err
	}
	if err := s.deleteAcknowledged(ctx, counts, &budget); err != nil {
		return err
	}
	return s.deleteInactive(ctx, counts)
}

// sessionKeys reads the keys a query selects.
func (s *Store) sessionKeys(ctx context.Context, query string, args ...any) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.Family, &k.ID); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// deleteAcknowledged deletes the messages of each due acknowledgement mark, then sets a
// new mark for every session that acknowledged more since its last one. A mark records
// that every message at or below ack_mark was acknowledged by ack_mark_at, so a message
// is deleted at least one retention period after its acknowledgement.
func (s *Store) deleteAcknowledged(ctx context.Context, counts *RetentionCounts, budget *int64) error {
	retention, _ := s.retentionPeriods()
	cutoff := s.now().Add(-retention).UnixMilli()
	due, err := s.sessionKeys(ctx, `SELECT family,id FROM sessions WHERE ack_mark_at!=0 AND ack_mark_at<=?
		ORDER BY ack_mark_at,family,id LIMIT ?`, cutoff, cullSessions)
	if err != nil {
		return err
	}
	for _, k := range due {
		for *budget > 0 {
			deleted, done, err := s.deleteMarked(ctx, k, cutoff)
			if err != nil {
				return err
			}
			counts.MessagesByRetention += deleted
			*budget -= deleted
			if done {
				break
			}
		}
		if *budget <= 0 {
			// The rest waits for the next sweep, also the new marks: a mark is set only
			// when no earlier mark waits.
			return nil
		}
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET ack_mark=acked_through,ack_mark_at=?
		WHERE ack_mark_at=0 AND acked_through>ack_mark`, s.now().UnixMilli()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// deleteMarked deletes one batch of a session's messages at or below its due mark. The
// batch that finishes the mark also clears it, in the same transaction.
func (s *Store) deleteMarked(ctx context.Context, k Key, cutoff int64) (int64, bool, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var mark int64
	err = tx.QueryRowContext(ctx, `SELECT ack_mark FROM sessions WHERE family=? AND id=? AND ack_mark_at!=0 AND ack_mark_at<=?`,
		k.Family, k.ID, cutoff).Scan(&mark)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id IN (SELECT id FROM messages
		WHERE recipient_family=? AND recipient_id=? AND seq<=? ORDER BY seq LIMIT ?)`, k.Family, k.ID, mark, cullBatch)
	if err != nil {
		return 0, false, tx.fail(err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	done := deleted < cullBatch
	if done {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET ack_mark_at=0 WHERE family=? AND id=?`, k.Family, k.ID); err != nil {
			return 0, false, tx.fail(err)
		}
	}
	return deleted, done, tx.Commit()
}

// inactiveSince is the SQL condition of a session whose activity ended at or before ?1.
const inactiveSince = `((s.retired_at!=0 AND s.retired_at<=?1) OR (s.retired_at=0 AND s.expires_at<=?1))`

// liveClaim is the SQL condition of a session that owns an active claim bundle; a claim is
// owned by the consumer key FAMILY:ID.
const liveClaim = `EXISTS (SELECT 1 FROM claim_bundles b WHERE b.active=1 AND b.consumer=s.family||':'||s.id)`

const anyMessage = `EXISTS (SELECT 1 FROM messages m WHERE m.recipient_family=s.family AND m.recipient_id=s.id)`

// deleteInactive deletes the sessions past their retention period that hold no message
// and no live claim, and counts those it keeps for unacknowledged messages or claims. A
// session whose acknowledged messages still wait for their own retention is kept too.
func (s *Store) deleteInactive(ctx context.Context, counts *RetentionCounts) error {
	_, retention := s.retentionPeriods()
	cutoff := s.now().Add(-retention).UnixMilli()
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(s.last_seq>s.acked_through),0),COALESCE(SUM(`+liveClaim+`),0)
		FROM sessions s WHERE s.family!='maintainer' AND s.purge_at=0 AND `+inactiveSince, cutoff).
		Scan(&counts.HeldUnacknowledged, &counts.HeldClaims)
	if err != nil {
		return err
	}
	keys, err := s.sessionKeys(ctx, `SELECT s.family,s.id FROM sessions s WHERE s.family!='maintainer' AND s.purge_at=0
		AND s.last_seq=s.acked_through AND `+inactiveSince+` AND NOT `+liveClaim+` AND NOT `+anyMessage+`
		ORDER BY s.family,s.id LIMIT ?2`, cutoff, cullSessions)
	if err != nil {
		return err
	}
	for _, k := range keys {
		// The conditions are read again in the deleting transaction, so a session that
		// registered again meanwhile is kept.
		deleted, err := s.deleteSession(ctx, k, `s.purge_at=0 AND s.last_seq=s.acked_through AND `+inactiveSince, cutoff)
		if err != nil {
			return err
		}
		if deleted {
			counts.SessionsByRetention++
		}
	}
	return nil
}

// deleteSession deletes a session that meets condition (with ?1 bound to arg) and holds
// no message and no live claim, with its peer name and its alias holding.
func (s *Store) deleteSession(ctx context.Context, k Key, condition string, arg any) (bool, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	tx, err := s.begin(ctx, control)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM sessions AS s WHERE s.family=?2 AND s.id=?3 AND s.family!='maintainer'
		AND `+condition+` AND NOT `+liveClaim+` AND NOT `+anyMessage, arg, k.Family, k.ID)
	if err != nil {
		return false, tx.fail(err)
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM names WHERE kind='peer' AND family=? AND session_id=?`, k.Family, k.ID); err != nil {
		return false, tx.fail(err)
	}
	// The alias stays reserved for its repository; the next active session takes it.
	if _, err := tx.ExecContext(ctx, `UPDATE names SET holder_id='' WHERE kind='alias' AND family=? AND holder_id=?`, k.Family, k.ID); err != nil {
		return false, tx.fail(err)
	}
	return true, tx.Commit()
}

// purgeMarked deletes each session that the maintainer marked for purge, with its whole
// inbox. Its live claims are released first, each as the owner's release with a work
// event that names the maintainer; a claim that cannot be released keeps the session for
// the next sweep.
func (s *Store) purgeMarked(ctx context.Context, counts *RetentionCounts, budget *int64) error {
	keys, err := s.sessionKeys(ctx, `SELECT family,id FROM sessions WHERE purge_at!=0 AND family!='maintainer'
		ORDER BY purge_at,family,id LIMIT ?`, cullSessions)
	if err != nil {
		return err
	}
	for _, k := range keys {
		released, err := s.releasePurged(ctx, k)
		if err != nil {
			return err
		}
		if !released {
			counts.HeldClaims++
			continue
		}
		for *budget > 0 {
			deleted, err := s.deletePurgedBatch(ctx, k)
			if err != nil {
				return err
			}
			counts.MessagesByPurge += deleted
			*budget -= deleted
			if deleted < cullBatch {
				break
			}
		}
		if *budget <= 0 {
			return nil
		}
		deleted, err := s.deleteSession(ctx, k, `s.purge_at!=0 AND ?1=1`, 1)
		if err != nil {
			return err
		}
		if deleted {
			counts.SessionsByPurge++
		}
	}
	return nil
}

// releasePurged releases every live claim of a marked session and reports whether none
// is left. A claim whose lease has expired waits for the work sweep's reconciliation.
func (s *Store) releasePurged(ctx context.Context, k Key) (bool, error) {
	consumer := k.Family + ":" + k.ID
	rows, err := s.db.QueryContext(ctx, `SELECT m.repository,w.work_id,w.revision,b.generation,b.expires_at
		FROM claim_bundles b JOIN work_items w ON w.store=b.store AND w.work_id=b.work_id
		JOIN memory_stores m ON m.store_id=b.store WHERE b.active=1 AND b.consumer=? ORDER BY b.generation`, consumer)
	if err != nil {
		return false, err
	}
	type claim struct {
		repository, workID   string
		revision, generation int64
		expires              float64
	}
	var claims []claim
	for rows.Next() {
		var c claim
		if err := rows.Scan(&c.repository, &c.workID, &c.revision, &c.generation, &c.expires); err != nil {
			rows.Close()
			return false, err
		}
		claims = append(claims, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	all := true
	for _, c := range claims {
		if c.expires <= s.clock() {
			all = false
			continue
		}
		fields := map[string]json.RawMessage{}
		for name, value := range map[string]any{"work_id": c.workID, "if_revision": c.revision,
			"claim_generation": c.generation, "checkpoint": purgeCheckpoint} {
			fields[name], _ = json.Marshal(value)
		}
		caller := MemoryCaller{Repository: c.repository, Family: maintainerKey.Family, Name: "maintainer", Consumer: consumer}
		if _, err := s.Work(ctx, caller, "work-release", fields); err != nil {
			var refusal WorkRefusal
			if errors.As(err, &refusal) {
				// The claim changed since it was read; the next sweep reads it again.
				all = false
				continue
			}
			return false, err
		}
	}
	return all, nil
}

// deletePurgedBatch deletes one batch of a marked session's messages, while the mark stands.
func (s *Store) deletePurgedBatch(ctx context.Context, k Key) (int64, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id IN (SELECT m.id FROM messages m
		JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
		WHERE m.recipient_family=? AND m.recipient_id=? AND s.purge_at!=0 ORDER BY m.seq LIMIT ?)`, k.Family, k.ID, cullBatch)
	if err != nil {
		return 0, tx.fail(err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return deleted, tx.Commit()
}

// markPurge sets or removes the maintainer's purge mark on a session at revision. Setting
// it retires an active session in the same write.
func (s *Store) markPurge(ctx context.Context, k Key, revision int64, mark bool) error {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if !validKey(k.Family, k.ID) || revision < 1 {
		return ErrInvalid
	}
	now := s.now().UnixMilli()
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	current, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), now)
	if err != nil {
		return err
	}
	if current.Revision != revision || (current.PurgeAt != 0) == mark {
		return ErrConflict
	}
	if mark {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET purge_at=?1,
			retired_at=CASE WHEN retired_at=0 AND expires_at>?1 THEN ?1 ELSE retired_at END,revision=revision+1
			WHERE family=?2 AND id=?3`, now, k.Family, k.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET purge_at=0,revision=revision+1 WHERE family=? AND id=?`, k.Family, k.ID)
	}
	if err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// RetentionStatus reports the periods, the last sweep and the marked sessions; it only reads.
func (s *Store) RetentionStatus(ctx context.Context) (RetentionStatus, error) {
	messages, sessions := s.retentionPeriods()
	result := RetentionStatus{MessageRetentionDays: int64(messages / (24 * time.Hour)), SessionRetentionDays: int64(sessions / (24 * time.Hour))}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE purge_at!=0`).Scan(&result.MarkedForPurge); err != nil {
		return RetentionStatus{}, err
	}
	s.retention.mu.Lock()
	defer s.retention.mu.Unlock()
	result.LastSweep, result.Last, result.Fault, result.FaultAt = s.retention.lastSweep, s.retention.last, s.retention.fault, s.retention.faultAt
	return result, nil
}
