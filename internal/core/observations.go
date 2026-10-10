package core

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Session observations (sprint chunk 08): the newest model, context, activity and terminal
// of each session, from allowlisted fields only. They live in memory: a daemon restart
// forgets them, and a session that is not active shows none of them.

// Observation sources. A source names where the reporter read the value; the daemon accepts
// each group only from its own sources.
var observationSources = map[string][]string{
	"model":    {"claude_statusline", "codex_rollout", "codex_mcp_meta", "agy_hook"},
	"context":  {"claude_statusline", "codex_rollout"},
	"activity": {"claude_registry", "codex_rollout", "agy_hook", "mcp_call"},
	"terminal": {"tmux_env"},
	"naming":   {"koinon_mcp"},
}

// terminalNaming lists the results that `koinon mcp` reports for naming a terminal, each
// with the reason text that peers and the dashboard show (participants sprint, chunk 05).
var terminalNaming = map[string]string{
	"renamed":                  "the tmux session was renamed to the published name",
	"unchanged":                "the tmux session already has the published name",
	"pane_titled":              "another agent shares the tmux session, so only this pane was titled",
	"name_taken":               "another tmux session has the name; tried again at the next renewal",
	"rename_unconfirmed":       "tmux did not confirm the new name; tried again at the next renewal",
	"pane_not_host":            "the tmux pane does not hold this session's host process",
	"nested_agent":             "another agent process lies between the pane and the host",
	"panes_unknown":            "the other panes of the tmux session could not be read; tried again at the next renewal",
	"tmux_unreadable":          "tmux could not be read; tried again at the next renewal",
	"process_table_unreadable": "the process table could not be read; tried again at the next renewal",
	"invalid_name":             "the published name is not a valid tmux name",
	"not_in_tmux":              "the session does not run in tmux",
	"attach_pane_not_found":    "no tmux pane holds a claude attach client for this job; tried again at the next renewal",
	"attach_pane_ambiguous":    "more than one tmux pane holds a claude attach client for this job; nothing was renamed",
}

// NamingReason is the fixed reason text of a naming result, or "" for an unknown result.
func NamingReason(result string) string { return terminalNaming[result] }

// published is the name that a session's terminal takes: its participant address while it
// holds it, else its peer name.
func published(session Session) string {
	if session.Alias != "" {
		return session.Alias
	}
	return session.Name
}

var maxObservedSessions = 4096 // a variable only so that tests can lower it

// ObservedValue is one observed group: its allowlisted fields, its source and the time the
// source recorded it (milliseconds).
type ObservedValue struct {
	Source string `json:"source"`
	At     int64  `json:"at"`
	// model
	ID string `json:"id,omitempty"`
	// context
	LimitTokens    *int64 `json:"limit_tokens,omitempty"`
	UsedTokens     *int64 `json:"used_tokens,omitempty"`
	UsageAvailable *bool  `json:"usage_available,omitempty"`
	// activity
	State string `json:"state,omitempty"`
	// terminal
	Socket  string `json:"socket,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	// Naming is the result of the last attempt to name the terminal after the session's
	// published name (#156), and Target that name. A naming report needs no terminal: a
	// session outside tmux or a background job without its attach client reports why.
	Naming string `json:"naming,omitempty"`
	Target string `json:"target,omitempty"`
}

// Observation is one report. Absent groups are left as they were.
type Observation struct {
	Caller   Key            `json:"caller"`
	Model    *ObservedValue `json:"model,omitempty"`
	Context  *ObservedValue `json:"context,omitempty"`
	Activity *ObservedValue `json:"activity,omitempty"`
	Terminal *ObservedValue `json:"terminal,omitempty"`
	Naming   *ObservedValue `json:"naming,omitempty"`
}

// storedValue keeps the time the daemon last received a report that confirms the value,
// separate from the time its source recorded it.
type storedValue struct {
	ObservedValue
	seen int64
}

// Freshness windows: a value that no report confirmed within its window is shown as
// unknown with observation_stale. Reporters that poll a source confirm an unchanged value
// every minute; event sources (hooks, tool calls) confirm only by their next event.
var observationFreshness = map[string]time.Duration{
	"model":    30 * time.Minute,
	"context":  30 * time.Minute,
	"activity": 2 * time.Minute,
	"terminal": 2 * time.Minute,
	"naming":   2 * time.Minute,
}

// sameContent compares the observed fields, not their source or time.
func sameContent(a, b ObservedValue) bool {
	a.Source, a.At, b.Source, b.At = "", 0, "", 0
	return reflect.DeepEqual(a, b)
}

type observations struct {
	mu     sync.Mutex
	byKey  map[Key]map[string]storedValue
	extras func(context.Context, Session) map[string]ObservedValue
}

var (
	modelID      = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,128}$`)
	tmuxPane     = regexp.MustCompile(`^%[0-9]{1,9}$`)
	tmuxSession  = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,128}$`)
	namingTarget = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
	maxSafeInt   = int64(1<<53 - 1)
	activityKind = map[string]bool{"busy": true, "idle": true, "waiting": true}
)

func allowed(group, source string) bool {
	for _, s := range observationSources[group] {
		if s == source {
			return true
		}
	}
	return false
}

// valid keeps only the fields of its group and checks their form and range.
func (v *ObservedValue) valid(group string, now int64) bool {
	if v == nil {
		return true
	}
	if !allowed(group, v.Source) || v.At <= 0 || v.At > now+60_000 {
		return false
	}
	switch group {
	case "model":
		return modelID.MatchString(v.ID) && v.LimitTokens == nil && v.UsedTokens == nil && v.UsageAvailable == nil &&
			v.State == "" && v.Socket == "" && v.Pane == "" && v.Session == "" && v.Naming == "" && v.Target == ""
	case "context":
		ok := func(p *int64) bool { return p == nil || *p >= 0 && *p <= maxSafeInt }
		return ok(v.LimitTokens) && ok(v.UsedTokens) && (v.LimitTokens == nil) == (v.UsedTokens == nil) &&
			v.ID == "" && v.State == "" && v.Socket == "" && v.Pane == "" && v.Session == "" && v.Naming == "" && v.Target == ""
	case "activity":
		return activityKind[v.State] && v.ID == "" && v.LimitTokens == nil && v.UsedTokens == nil &&
			v.UsageAvailable == nil && v.Socket == "" && v.Pane == "" && v.Session == "" && v.Naming == "" && v.Target == ""
	case "terminal":
		return len(v.Socket) > 0 && len(v.Socket) <= 4096 && !strings.ContainsAny(v.Socket, "\x00\r\n") &&
			tmuxPane.MatchString(v.Pane) && (v.Session == "" || tmuxSession.MatchString(v.Session)) &&
			(v.Naming == "" || terminalNaming[v.Naming] != "") && v.Target == "" &&
			v.ID == "" && v.State == "" && v.LimitTokens == nil && v.UsedTokens == nil && v.UsageAvailable == nil
	case "naming":
		return terminalNaming[v.Naming] != "" && namingTarget.MatchString(v.Target) && v.Socket == "" && v.Pane == "" &&
			v.Session == "" && v.ID == "" && v.State == "" && v.LimitTokens == nil && v.UsedTokens == nil && v.UsageAvailable == nil
	}
	return false
}

// terminalVerified reports whether a session's terminal can be attributed to it: a launched
// session (its registration carried a verified launch ID), or a Claude session, whose MCP
// server is the child of that Claude process. A shared server's environment alone proves
// nothing about the sessions it serves.
func terminalVerified(s Session) bool {
	if s.Family == "claude" {
		return true
	}
	var target struct {
		LaunchID string `json:"launch_id"`
	}
	return json.Unmarshal(s.WakeTarget, &target) == nil && target.LaunchID != ""
}

// Observe records one report for an active session. A group from another family's source,
// or a terminal that cannot be attributed, is refused.
func (s *Store) Observe(ctx context.Context, o Observation) error {
	now := s.now().UnixMilli()
	groups := map[string]*ObservedValue{"model": o.Model, "context": o.Context, "activity": o.Activity, "terminal": o.Terminal, "naming": o.Naming}
	present := 0
	for group, v := range groups {
		if !v.valid(group, now) {
			return ErrInvalid
		}
		if v != nil {
			present++
		}
	}
	if present == 0 {
		return ErrInvalid
	}
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, o.Caller.Family, o.Caller.ID), now)
	if err != nil {
		return err
	}
	if session.State != "active" {
		return ErrCallerInactive
	}
	if (o.Terminal != nil || o.Naming != nil) && !terminalVerified(session) {
		return Refusal{Code: "terminal_unverified", Message: "this session's terminal cannot be attributed to it"}
	}
	s.observed.mu.Lock()
	full := len(s.observed.byKey) >= maxObservedSessions
	s.observed.mu.Unlock()
	if full {
		// Drop the observations of sessions that ended, without waiting for a dashboard view.
		active, err := s.activeKeys(ctx)
		if err != nil {
			return err
		}
		s.forget(active)
	}
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	if s.observed.byKey == nil {
		s.observed.byKey = map[Key]map[string]storedValue{}
	}
	current, found := s.observed.byKey[o.Caller]
	if !found {
		if len(s.observed.byKey) >= maxObservedSessions {
			return Refusal{Code: "capacity", Message: "too many observed sessions"}
		}
		current = map[string]storedValue{}
		s.observed.byKey[o.Caller] = current
	}
	for group, v := range groups {
		if v == nil {
			continue
		}
		// A newer report replaces the value; a report of the same content confirms it; an
		// older, different report changes nothing.
		previous, found := current[group]
		switch {
		case !found || v.At > previous.At:
			current[group] = storedValue{*v, now}
		case sameContent(*v, previous.ObservedValue):
			previous.seen = now
			current[group] = previous
		}
	}
	return nil
}

func (s *Store) activeKeys(ctx context.Context) (map[Key]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT family,id FROM sessions WHERE retired_at=0 AND expires_at>?`, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := map[Key]bool{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.Family, &k.ID); err != nil {
			return nil, err
		}
		active[k] = true
	}
	return active, rows.Err()
}

// forget drops the observations of sessions that are no longer active.
func (s *Store) forget(active map[Key]bool) {
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	for k := range s.observed.byKey {
		if !active[k] {
			delete(s.observed.byKey, k)
		}
	}
}

// SessionView is how the dashboard shows one group: a value, or unknown with a reason.
type SessionView struct {
	Value  *ObservedValue
	Reason string
	// ConfirmedAt is when the daemon last received confirmation, distinct from Value.At.
	ConfirmedAt int64
}

// sessionObservations returns each group of a session's observations as the dashboard
// shows it. Pulled values (extras) take part when they are newer.
func (s *Store) sessionObservations(ctx context.Context, session Session) map[string]SessionView {
	result := map[string]SessionView{}
	if session.State != "active" {
		for group := range observationSources {
			result[group] = SessionView{Reason: "session_" + session.State}
		}
		return result
	}
	key := Key{session.Family, session.ID}
	now := s.now().UnixMilli()
	s.observed.mu.Lock()
	current := map[string]storedValue{}
	for group, v := range s.observed.byKey[key] {
		current[group] = v
	}
	extras := s.observed.extras
	s.observed.mu.Unlock()
	if extras != nil {
		for group, v := range extras(ctx, session) {
			if v.At >= current[group].At {
				current[group] = storedValue{v, now}
			}
		}
	}
	for group := range observationSources {
		v, found := current[group]
		switch {
		case (group == "terminal" || group == "naming") && !terminalVerified(session):
			result[group] = SessionView{Reason: "terminal_unverified"}
		case found && now-v.seen > observationFreshness[group].Milliseconds():
			result[group] = SessionView{Reason: "observation_stale"}
		case found && group == "naming" && v.Target != published(session):
			// A result for an earlier published name, such as one computed before a holder
			// change, says nothing about the current name.
			result[group] = SessionView{Reason: "naming_outdated"}
		case found:
			value := v.ObservedValue
			result[group] = SessionView{Value: &value, ConfirmedAt: v.seen}
		default:
			result[group] = SessionView{Reason: unobserved(group, session.Family)}
		}
	}
	return result
}

// PeerNaming is a session's last terminal naming result for its current published name,
// with its fixed reason text.
type PeerNaming struct {
	Result string `json:"result"`
	Reason string `json:"reason"`
	Target string `json:"target"`
	At     int64  `json:"at"`
}

// namingOf returns the current naming result of an active session, or nil when none is
// fresh or the result is for an earlier published name.
func (s *Store) namingOf(session Session) *PeerNaming {
	if session.State != "active" || !terminalVerified(session) {
		return nil
	}
	now := s.now().UnixMilli()
	s.observed.mu.Lock()
	v, found := s.observed.byKey[Key{session.Family, session.ID}]["naming"]
	s.observed.mu.Unlock()
	if !found || now-v.seen > observationFreshness["naming"].Milliseconds() || v.Target != published(session) {
		return nil
	}
	return &PeerNaming{Result: v.Naming, Reason: NamingReason(v.Naming), Target: v.Target, At: v.At}
}

// unobserved names why a group has no value: no source exists for the family, or none has
// reported yet.
func unobserved(group, family string) string {
	sources := map[string]map[string]bool{
		"model":    {"claude": true, "codex": true, "agy": true},
		"context":  {"claude": true, "codex": true},
		"activity": {"claude": true, "codex": true, "agy": true, "opencode": true},
		"terminal": {"claude": true, "codex": true, "agy": true, "opencode": true},
		"naming":   {"claude": true, "codex": true, "agy": true, "opencode": true},
	}
	if !sources[group][family] {
		return "no_source"
	}
	return "not_observed"
}

func (d *Daemon) observationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/peers/status", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			Peer   string `json:"peer"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		status, err := d.store.PeerStatus(r.Context(), request.Caller, request.Peer)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "peer": status})
	})
	mux.HandleFunc("POST /v1/sessions/observe", func(w http.ResponseWriter, r *http.Request) {
		var o Observation
		if err := decode(w, r, &o); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.Observe(r.Context(), o); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
	})
}
