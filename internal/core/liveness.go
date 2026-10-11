package core

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/rwcii/koinon/internal/platform"
)

// liveness records when the maintenance sweep last read each session's host process as
// running. It is kept in memory: after a daemon restart, the first sweep fills it again.
type liveness struct {
	mu       sync.Mutex
	verified map[Key]int64
}

func (l *liveness) set(k Key, at int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.verified == nil {
		l.verified = map[Key]int64{}
	}
	if at == 0 {
		delete(l.verified, k)
		return
	}
	l.verified[k] = at
}

func (l *liveness) at(k Key) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.verified[k]
}

// annotate adds the liveness view to a scanned session: its evidence and when the sweep
// last read its host as running.
func (s *Store) annotate(r *Session) {
	if r.Liveness == "host" {
		r.HostVerifiedAt = s.alive.at(Key{r.Family, r.ID})
	}
}

// MaintainLiveness keeps each active session whose host process still runs: a launched
// session, not a sub-agent, with a host record whose process has the recorded start value.
// A session with less than half of the lifetime setting, or less than two sweep intervals,
// left is extended by that lifetime, without a revision change, so its own renewals still apply. A session whose
// host ended or cannot be read is not extended and expires at its deadline; a session that
// has expired is never revived.
func (s *Store) MaintainLiveness(ctx context.Context) error {
	now := s.now().UnixMilli()
	lifetime := s.Settings().LifetimeSeconds * 1000
	rows, err := s.db.QueryContext(ctx, `SELECT family,id,wake_target,host_pid,host_start,expires_at,revision FROM sessions
		WHERE retired_at=0 AND expires_at>? AND subagent=0 AND host_pid>0 AND host_start>0`, now)
	if err != nil {
		return err
	}
	type candidate struct {
		key                           Key
		pid, start, expires, revision int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var target string
		if err := rows.Scan(&c.key.Family, &c.key.ID, &target, &c.pid, &c.start, &c.expires, &c.revision); err != nil {
			rows.Close()
			return err
		}
		if launched(Session{WakeTarget: []byte(target)}) {
			candidates = append(candidates, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// The hosts are read after the query has ended, so no read holds the database.
	seen := map[Key]bool{}
	var due []candidate
	for _, c := range candidates {
		seen[c.key] = true
		start, err := s.processStart(int(c.pid))
		if err != nil || start != c.start {
			s.alive.set(c.key, 0)
			continue
		}
		s.alive.set(c.key, s.now().UnixMilli())
		// Extend at half the lifetime, and in any case before the next sweep can come too
		// late: with the shortest lifetime, half of it is one sweep interval.
		if c.expires-s.now().UnixMilli() < max(lifetime/2, 2*workSweepInterval.Milliseconds()) {
			due = append(due, c)
		}
	}
	s.alive.forget(seen)
	if len(due) == 0 {
		return nil
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The clock and the row are read again inside the write: a session that expired while
	// its host was read, or that changed since (registered again, as a sub-agent too,
	// renewed or retired; each changes its revision), is left as it is.
	now = s.now().UnixMilli()
	for _, c := range due {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE family=? AND id=? AND revision=?
			AND retired_at=0 AND subagent=0 AND expires_at>? AND expires_at<? AND host_pid=? AND host_start=?`,
			now+lifetime, c.key.Family, c.key.ID, c.revision, now, now+lifetime, c.pid, c.start); err != nil {
			return tx.fail(err)
		}
	}
	return tx.Commit()
}

// forget drops the records of sessions that the sweep no longer reads.
func (l *liveness) forget(seen map[Key]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.verified {
		if !seen[k] {
			delete(l.verified, k)
		}
	}
}

// inactiveReason says why a recipient session takes no message: retired; ended (its host
// process is gone or another process has its ID); no_liveness_evidence (no host record, a
// sub-agent, or a host that cannot be read, and its lifetime passed); or lapsed (its host
// runs, but its registration expired before a sweep kept it, as while the daemon was
// stopped; its next tool call registers it again).
func (s *Store) inactiveReason(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, family, id string) (string, error) {
	var retired, pid, start int64
	var subagent bool
	err := q.QueryRowContext(ctx, `SELECT retired_at,subagent,host_pid,host_start FROM sessions WHERE family=? AND id=?`,
		family, id).Scan(&retired, &subagent, &pid, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRecipientInactive
	}
	if err != nil {
		return "", err
	}
	switch {
	case retired != 0:
		return "retired", nil
	case subagent || pid <= 0 || start <= 0:
		return "no_liveness_evidence", nil
	}
	current, err := s.processStart(int(pid))
	switch {
	case errors.Is(err, platform.ErrProcessGone) || err == nil && current != start:
		return "ended", nil
	case err != nil:
		return "no_liveness_evidence", nil
	}
	return "lapsed", nil
}

// InactiveRecipient refuses a message to an expired or retired session and says why.
type InactiveRecipient struct{ Reason string }

func (e InactiveRecipient) Error() string { return ErrRecipientInactive.Error() + ": " + e.Reason }

func (e InactiveRecipient) Is(target error) bool { return target == ErrRecipientInactive }
