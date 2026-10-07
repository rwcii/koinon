package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// OpenCode activity comes from `GET /session/status` on the session's own loopback server,
// with the password that the launcher set (spike fact 6). It is the only OpenCode endpoint
// the dashboard calls. A result is kept for a few seconds so that refreshes do not repeat it.

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
	var raw string
	if s.db.QueryRowContext(ctx, `SELECT target FROM launches WHERE id=?`, launchID).Scan(&raw) != nil {
		return nil
	}
	var target LaunchTarget
	if json.Unmarshal([]byte(raw), &target) != nil || target.Password == "" || !validAddress(target.Address) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.Address+"/session/status", nil)
	if err != nil {
		return nil
	}
	r.SetBasicAuth("opencode", target.Password)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&statuses) != nil {
		return nil
	}
	status, found := statuses[sessionID]
	state := map[string]string{"idle": "idle", "busy": "busy", "retry": "busy"}[status.Type]
	if !found || state == "" {
		return nil
	}
	return &ObservedValue{Source: "opencode_status", At: s.now().UnixMilli(), State: state}
}
