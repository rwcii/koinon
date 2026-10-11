package core

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/rwcii/koinon/internal/platform"
)

// defaultLifetime is the registration lifetime when a registration names none, in
// milliseconds. Liveness keeps a running session past it; the lifetime bounds a session
// without host evidence.
const defaultLifetime = 900_000

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
// A session with less than half of the default lifetime left is extended by the default
// lifetime, without a revision change, so its own renewals still apply. A session whose
// host ended or cannot be read is not extended and expires at its deadline; a session that
// has expired is never revived.
func (s *Store) MaintainLiveness(ctx context.Context) error {
	now := s.now().UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT family,id,wake_target,host_pid,host_start,expires_at FROM sessions
		WHERE retired_at=0 AND expires_at>? AND subagent=0 AND host_pid>0 AND host_start>0`, now)
	if err != nil {
		return err
	}
	type candidate struct {
		key        Key
		pid, start int64
	}
	var due []candidate
	seen := map[Key]bool{}
	for rows.Next() {
		var c candidate
		var target string
		var expires int64
		if err := rows.Scan(&c.key.Family, &c.key.ID, &target, &c.pid, &c.start, &expires); err != nil {
			rows.Close()
			return err
		}
		if !launched(Session{WakeTarget: []byte(target)}) {
			continue
		}
		seen[c.key] = true
		start, err := s.processStart(int(c.pid))
		if err != nil || start != c.start {
			s.alive.set(c.key, 0)
			continue
		}
		s.alive.set(c.key, now)
		if expires-now < defaultLifetime/2 {
			due = append(due, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
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
	for _, c := range due {
		// The guard keeps a session that changed since the read: retired, expired, or
		// registered again with another host.
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE family=? AND id=? AND retired_at=0
			AND expires_at>? AND host_pid=? AND host_start=?`, now+defaultLifetime, c.key.Family, c.key.ID, now, c.pid, c.start); err != nil {
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
