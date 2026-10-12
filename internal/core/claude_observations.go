package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
)

// ObservationReporter pins the live native hook process while its request is served.
// This evidence is transient and never appears in peer status or stored observations.
type ObservationReporter struct {
	PID   int   `json:"pid"`
	Start int64 `json:"start"`
}

func observationHostRefusal() error {
	return Refusal{Code: "observation_host_unverified", Message: "the observation cannot be attributed to one live Claude host"}
}

// claudeObservationCaller resolves a post-clear status line to the session of its actual
// native host. The daemon reads ancestry itself; no client-supplied ancestry or identity
// choice can move a registration, alias, claim or cursor.
func (s *Store) claudeObservationCaller(ctx context.Context, caller Key, reporter ObservationReporter) (Key, error) {
	if !validKey(caller.Family, caller.ID) || reporter.PID <= 1 || reporter.Start <= 0 {
		return Key{}, ErrInvalid
	}
	start, err := s.processStart(reporter.PID)
	if err != nil || start != reporter.Start {
		return Key{}, observationHostRefusal()
	}
	parents, err := s.processParents()
	if err != nil {
		return Key{}, observationHostRefusal()
	}
	seen := map[int]bool{}
	for pid, steps := parents[reporter.PID], 0; pid > 1 && steps < 256; pid, steps = parents[pid], steps+1 {
		if seen[pid] {
			return Key{}, observationHostRefusal()
		}
		seen[pid] = true
		rows, err := s.db.QueryContext(ctx, `SELECT family,id,host_start FROM sessions
			WHERE host_pid=? AND retired_at=0 AND expires_at>? AND subagent=0 LIMIT 2`, pid, s.now().UnixMilli())
		if err != nil {
			return Key{}, err
		}
		var keys []Key
		var hostStart int64
		for rows.Next() {
			var key Key
			if err := rows.Scan(&key.Family, &key.ID, &hostStart); err != nil {
				rows.Close()
				return Key{}, err
			}
			keys = append(keys, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Key{}, err
		}
		if len(keys) > 0 {
			if len(keys) != 1 || keys[0].Family != "claude" || hostStart <= 0 {
				return Key{}, observationHostRefusal()
			}
			current, err := s.processStart(pid)
			if err != nil || current != hostStart {
				return Key{}, observationHostRefusal()
			}
			// A known caller must already be this session. The fallback cannot redirect
			// a report belonging to another registered or retired native session.
			var exists int
			err = s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=?`, caller.Family, caller.ID).Scan(&exists)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return Key{}, err
			}
			if err == nil && caller != keys[0] {
				return Key{}, observationHostRefusal()
			}
			current, err = s.processStart(reporter.PID)
			if err != nil || current != reporter.Start {
				return Key{}, observationHostRefusal()
			}
			return keys[0], nil
		}
		// An unregistered nested CLI is islanded, not a source for the outer host.
		exe, args, err := s.processCommand(pid)
		if err != nil || observationAgentProcess(exe, args) {
			return Key{}, observationHostRefusal()
		}
	}
	return Key{}, observationHostRefusal()
}

func observationAgentProcess(exe string, args []string) bool {
	if strings.Contains(exe, "/claude/versions/") {
		return true
	}
	for i, arg := range append([]string{exe}, args...) {
		if strings.Contains(arg, "@anthropic-ai/claude-code/") {
			return true
		}
		if i > 1 {
			continue
		}
		switch filepath.Base(arg) {
		case "claude", "codex", "agy", "opencode", "deepseek", "dsh":
			return true
		}
	}
	return false
}
