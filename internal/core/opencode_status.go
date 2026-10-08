package core

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// OpenCode activity comes from `GET /session/status` on the session's own loopback server,
// with the password that the launcher set (spike fact 6). An absent status requires
// confirming that exact session through its read-only endpoint. The dashboard
// keeps a result for a few seconds; wake submissions always read fresh status.

const openCodeStatusTTL = 5 * time.Second

type openCodeCache struct {
	mu      sync.Mutex
	results map[string]openCodeResult
}

type openCodeResult struct {
	at    time.Time
	value *ObservedValue
}

// openCodeActivity returns the activity of a launched OpenCode session, or nothing.
func (s *Store) openCodeActivity(ctx context.Context, session Session) map[string]ObservedValue {
	if session.Family != "opencode" {
		return nil
	}
	var wake struct {
		LaunchID string `json:"launch_id"`
	}
	if json.Unmarshal(session.WakeTarget, &wake) != nil || wake.LaunchID == "" {
		return nil
	}
	now := s.now()
	s.openCode.mu.Lock()
	cached, found := s.openCode.results[session.ID]
	s.openCode.mu.Unlock()
	if !found || now.Sub(cached.at) >= openCodeStatusTTL || now.Before(cached.at) {
		cached = openCodeResult{at: now, value: s.queryOpenCode(ctx, wake.LaunchID, session.ID)}
		s.openCode.mu.Lock()
		if s.openCode.results == nil || len(s.openCode.results) >= maxObservedSessions {
			s.openCode.results = map[string]openCodeResult{}
		}
		s.openCode.results[session.ID] = cached
		s.openCode.mu.Unlock()
	}
	if cached.value == nil {
		return nil
	}
	return map[string]ObservedValue{"activity": *cached.value}
}

func (s *Store) queryOpenCode(ctx context.Context, launchID, sessionID string) *ObservedValue {
	target, err := s.openCodeTarget(ctx, launchID)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	state, err := openCodeStatus(ctx, target, sessionID)
	if err != nil {
		return nil
	}
	return &ObservedValue{Source: "opencode_status", At: s.now().UnixMilli(), State: state}
}
