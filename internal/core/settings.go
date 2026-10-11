package core

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
)

// Settings are the session timing values that the maintainer sets in the dashboard
// (#249). The daemon applies a change at once, without a restart.
type Settings struct {
	// LifetimeSeconds is the registration lifetime when a registration or renewal names
	// none; liveness extends a running session by it.
	LifetimeSeconds int64 `json:"lifetime_seconds"`
	// RenewalSeconds is how often koinon mcp renews the session that it served last.
	RenewalSeconds int64 `json:"renewal_seconds"`
	// GuardSeconds is how long after the holder's last tool call same-host succession is
	// refused.
	GuardSeconds int64 `json:"guard_seconds"`
}

// DefaultSettings apply until the maintainer changes them.
var DefaultSettings = Settings{LifetimeSeconds: 900, RenewalSeconds: 300, GuardSeconds: 30}

// Valid checks each value's range and that a session renews at least twice within its
// lifetime. The guard cannot go below 30 seconds, because it is a safety bound.
func (v Settings) Valid() bool {
	return v.LifetimeSeconds >= 60 && v.LifetimeSeconds <= 3600 &&
		v.RenewalSeconds >= 30 && v.RenewalSeconds*2 <= v.LifetimeSeconds &&
		v.GuardSeconds >= 30 && v.GuardSeconds <= 600
}

func (v Settings) String() string {
	return fmt.Sprintf("lifetime %ds, renewal %ds, guard %ds", v.LifetimeSeconds, v.RenewalSeconds, v.GuardSeconds)
}

type settingsState struct {
	current atomic.Pointer[Settings]
	// write serializes changes, so the stored and the applied values change in one order.
	write sync.Mutex
}

// Settings returns the values in force.
func (s *Store) Settings() Settings {
	if v := s.settings.current.Load(); v != nil {
		return *v
	}
	return DefaultSettings
}

// loadSettings reads the stored values; a value that is not stored keeps its default.
// A stored set that is not valid as a whole is not applied.
func (s *Store) loadSettings(ctx context.Context) error {
	v := DefaultSettings
	rows, err := s.db.QueryContext(ctx, `SELECT name,value FROM settings`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			return err
		}
		switch name {
		case "lifetime_seconds":
			v.LifetimeSeconds = value
		case "renewal_seconds":
			v.RenewalSeconds = value
		case "guard_seconds":
			v.GuardSeconds = value
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !v.Valid() {
		v = DefaultSettings
	}
	s.settings.current.Store(&v)
	return nil
}

// SetSettings validates the three values together and stores them in one transaction,
// with the dashboard action's audit record; a refused change stores nothing. The new
// values apply from the commit.
func (s *Store) SetSettings(ctx context.Context, v Settings) error {
	if !v.Valid() {
		return ErrInvalid
	}
	s.settings.write.Lock()
	defer s.settings.write.Unlock()
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	for _, setting := range []struct {
		name  string
		value int64
	}{{"lifetime_seconds", v.LifetimeSeconds}, {"renewal_seconds", v.RenewalSeconds}, {"guard_seconds", v.GuardSeconds}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES (?,?,?)
			ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, setting.name, setting.value, now); err != nil {
			return tx.fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.settings.current.Store(&v)
	return nil
}

// settingsUpdatedAt is when the maintainer last changed a setting, or 0.
func (s *Store) settingsUpdatedAt(ctx context.Context) (int64, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(updated_at) FROM settings`).Scan(&at)
	return at.Int64, err
}
