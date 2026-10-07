package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func int64p(v int64) *int64 { return &v }
func boolp(v bool) *bool    { return &v }

func observe(s *Store, o Observation) string { return code(s.Observe(context.Background(), o)) }

func TestObservationValidationAndAttribution(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	now := clock.UnixMilli()
	ctx := context.Background()
	codex := join(t, s, "codex", "synthetic-codex", "")
	claude := join(t, s, "claude", "synthetic-claude", "")
	model := &ObservedValue{Source: "codex_rollout", At: now, ID: "gpt-synthetic"}
	terminal := &ObservedValue{Source: "tmux_env", At: now, Socket: "/tmp/tmux-1/default", Pane: "%3", Session: "work"}
	// Each group takes only its own fields and sources.
	for name, o := range map[string]Observation{
		"no group":            {Caller: Key{"codex", "synthetic-codex"}},
		"foreign source":      {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "claude_registry", At: now, ID: "m"}},
		"extra field":         {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_rollout", At: now, ID: "m", State: "busy"}},
		"control character":   {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_rollout", At: now, ID: "m\n"}},
		"long model":          {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_rollout", At: now, ID: strings.Repeat("m", 129)}},
		"future time":         {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_rollout", At: now + 61_000, ID: "m"}},
		"no time":             {Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_rollout", ID: "m"}},
		"unknown activity":    {Caller: Key{"codex", "synthetic-codex"}, Activity: &ObservedValue{Source: "codex_rollout", At: now, State: "thinking"}},
		"negative tokens":     {Caller: Key{"codex", "synthetic-codex"}, Context: &ObservedValue{Source: "codex_rollout", At: now, LimitTokens: int64p(-1), UsedTokens: int64p(1)}},
		"half a context":      {Caller: Key{"codex", "synthetic-codex"}, Context: &ObservedValue{Source: "codex_rollout", At: now, LimitTokens: int64p(10)}},
		"pane form":           {Caller: Key{"claude", "synthetic-claude"}, Terminal: &ObservedValue{Source: "tmux_env", At: now, Socket: "/tmp/s", Pane: "3"}},
		"terminal without it": {Caller: Key{"claude", "synthetic-claude"}, Terminal: &ObservedValue{Source: "tmux_env", At: now, Pane: "%3"}},
		"unknown naming":      {Caller: Key{"claude", "synthetic-claude"}, Terminal: &ObservedValue{Source: "tmux_env", At: now, Socket: "/tmp/s", Pane: "%3", Naming: "renamed<b>"}},
		"naming elsewhere":    {Caller: Key{"codex", "synthetic-codex"}, Activity: &ObservedValue{Source: "codex_rollout", At: now, State: "busy", Naming: "renamed"}},
	} {
		if err := s.Observe(context.Background(), o); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := s.Observe(ctx, Observation{Caller: Key{"codex", "synthetic-missing"}, Model: model}); !errors.Is(err, ErrMissing) {
		t.Fatalf("unknown session: %v", err)
	}
	// A shared server's environment does not make a terminal a session's own: an unlaunched
	// Codex session is refused, a Claude session is accepted.
	if got := observe(s, Observation{Caller: Key{"codex", "synthetic-codex"}, Terminal: terminal}); got != "terminal_unverified" {
		t.Fatalf("unlaunched terminal: %q", got)
	}
	if got := observe(s, Observation{Caller: Key{"claude", "synthetic-claude"}, Terminal: terminal}); got != "" {
		t.Fatalf("claude terminal: %q", got)
	}
	if got := observe(s, Observation{Caller: Key{"codex", "synthetic-codex"}, Model: model,
		Context:  &ObservedValue{Source: "codex_rollout", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(250), UsageAvailable: boolp(true)},
		Activity: &ObservedValue{Source: "codex_rollout", At: now, State: "busy"}}); got != "" {
		t.Fatalf("codex groups: %q", got)
	}
	// An older report never replaces a newer one.
	if got := observe(s, Observation{Caller: Key{"codex", "synthetic-codex"}, Model: &ObservedValue{Source: "codex_mcp_meta", At: now - 1, ID: "older"}}); got != "" {
		t.Fatal(got)
	}
	views := s.sessionObservations(ctx, codex)
	if views["model"].Value == nil || views["model"].Value.ID != "gpt-synthetic" || views["context"].Value == nil ||
		*views["context"].Value.UsedTokens != 250 || views["activity"].Value.State != "busy" || views["terminal"].Reason != "terminal_unverified" {
		t.Fatalf("codex views: %+v", views)
	}
	views = s.sessionObservations(ctx, claude)
	if views["terminal"].Value == nil || views["terminal"].Value.Pane != "%3" || views["model"].Reason != "not_observed" {
		t.Fatalf("claude views: %+v", views)
	}
	// Families without a source say so.
	opencode := join(t, s, "opencode", "synthetic-open", "")
	if views := s.sessionObservations(ctx, opencode); views["model"].Reason != "no_source" || views["context"].Reason != "no_source" ||
		views["activity"].Reason != "not_observed" || views["terminal"].Reason != "terminal_unverified" {
		t.Fatalf("opencode views: %+v", views)
	}
	agy := join(t, s, "agy", "synthetic-agy", "")
	if views := s.sessionObservations(ctx, agy); views["context"].Reason != "no_source" || views["model"].Reason != "not_observed" {
		t.Fatalf("agy views: %+v", views)
	}
	// An inactive session shows nothing and takes no report; forgetting drops its values.
	retired, err := s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-codex", IfRevision: codex.Revision}, true)
	if err != nil {
		t.Fatal(err)
	}
	if views := s.sessionObservations(ctx, retired); views["model"].Value != nil || views["model"].Reason != "session_retired" {
		t.Fatalf("retired views: %+v", views)
	}
	if err := s.Observe(ctx, Observation{Caller: Key{"codex", "synthetic-codex"}, Model: model}); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("retired report: %v", err)
	}
	s.forget(map[Key]bool{{"claude", "synthetic-claude"}: true})
	if _, found := s.observed.byKey[Key{"codex", "synthetic-codex"}]; found {
		t.Fatal("retired session's observations kept")
	}
	clock = clock.Add(2 * time.Minute)
	if views := s.sessionObservations(ctx, join(t, s, "codex", "synthetic-codex", "")); views["model"].Value != nil {
		t.Fatal("a re-registered session shows forgotten observations")
	}
}

func TestLaunchedTerminalsAndOpenCodeStatus(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	now := s.now().UnixMilli()
	directory := t.TempDir()
	password := strings.Repeat("ab", 32)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		user, pass, ok := r.BasicAuth()
		if r.URL.Path != "/session/status" || r.Method != "GET" || !ok || user != "opencode" || pass != password {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"synthetic-open-a": map[string]any{"type": "busy"},
			"synthetic-open-b": map[string]any{"type": "retry", "attempt": 1}})
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	launch := func(family, password string) string {
		target := LaunchTarget{Family: family, Directory: directory, CLI: filepath.Join(directory, "synthetic-cli"), HostPID: 123}
		if family == "opencode" {
			target.Address, target.Password = address, password
		}
		id, err := s.CreateLaunch(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	register := func(family, id, launchID string) Session {
		session, err := s.Register(ctx, Registration{Family: family, ID: id, Directory: directory, LaunchID: launchID})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	// Successive Codex sessions of one launch each get the launch's terminal.
	codexLaunch := launch("codex", "")
	terminal := &ObservedValue{Source: "tmux_env", At: now, Socket: "/tmp/tmux-1/default", Pane: "%7"}
	for _, id := range []string{"synthetic-thread-1", "synthetic-thread-2"} {
		register("codex", id, codexLaunch)
		if got := observe(s, Observation{Caller: Key{"codex", id}, Terminal: terminal}); got != "" {
			t.Fatalf("launched terminal %s: %q", id, got)
		}
	}
	s.observed.extras = s.openCodeActivity
	a := register("opencode", "synthetic-open-a", launch("opencode", password))
	if v := s.sessionObservations(ctx, a)["activity"].Value; v == nil || v.State != "busy" || v.Source != "opencode_status" {
		t.Fatalf("opencode busy: %+v", v)
	}
	b := register("opencode", "synthetic-open-b", launch("opencode", password))
	if v := s.sessionObservations(ctx, b)["activity"].Value; v == nil || v.State != "busy" {
		t.Fatalf("opencode retry: %+v", v)
	}
	// A cached result is reused within five seconds.
	before := calls
	s.sessionObservations(ctx, a)
	if calls != before {
		t.Fatal("status not cached")
	}
	// A session absent from the status map, and a refused password, show no activity.
	c := register("opencode", "synthetic-open-c", launch("opencode", password))
	if v := s.sessionObservations(ctx, c)["activity"]; v.Value != nil || v.Reason != "not_observed" {
		t.Fatalf("absent session: %+v", v)
	}
	d := register("opencode", "synthetic-open-d", launch("opencode", strings.Repeat("cd", 32)))
	if v := s.sessionObservations(ctx, d)["activity"]; v.Value != nil {
		t.Fatalf("wrong password: %+v", v)
	}
	// An OpenCode session without a launch is never queried.
	before = calls
	plain := register("opencode", "synthetic-open-e", "")
	if v := s.sessionObservations(ctx, plain)["activity"]; v.Value != nil || calls != before {
		t.Fatal("unlaunched OpenCode session queried")
	}
}

func TestObservationCapPrunesEndedSessions(t *testing.T) {
	s, _ := testStore(t)
	saved := maxObservedSessions
	maxObservedSessions = 2
	defer func() { maxObservedSessions = saved }()
	ctx := context.Background()
	now := s.now().UnixMilli()
	report := func(id string) error {
		return s.Observe(ctx, Observation{Caller: Key{"codex", id}, Model: &ObservedValue{Source: "codex_rollout", At: now, ID: "m"}})
	}
	a := join(t, s, "codex", "synthetic-a", "")
	join(t, s, "codex", "synthetic-b", "")
	join(t, s, "codex", "synthetic-c", "")
	if report("synthetic-a") != nil || report("synthetic-b") != nil {
		t.Fatal("reports under the cap refused")
	}
	if err := report("synthetic-c"); code(err) != "capacity" {
		t.Fatalf("over the cap with every session active: %v", err)
	}
	// An ended session's observations make room without a dashboard view.
	if _, err := s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-a", IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if err := report("synthetic-c"); err != nil {
		t.Fatalf("after an ended session: %v", err)
	}
	if _, found := s.observed.byKey[Key{"codex", "synthetic-a"}]; found {
		t.Fatal("ended session kept")
	}
}

func TestObservationFreshness(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	session := join(t, s, "codex", "synthetic-fresh", "")
	at := clock.UnixMilli()
	busy := &ObservedValue{Source: "codex_rollout", At: at, State: "busy"}
	model := &ObservedValue{Source: "codex_rollout", At: at, ID: "gpt-synthetic"}
	if err := s.Observe(ctx, Observation{Caller: Key{"codex", "synthetic-fresh"}, Activity: busy, Model: model}); err != nil {
		t.Fatal(err)
	}
	// The session stays active through renewals, but an unconfirmed activity goes stale
	// after two minutes, while the model is still fresh.
	advance := func(d time.Duration) Session {
		t.Helper()
		for step := 30 * time.Second; d > 0; d -= step {
			clock = clock.Add(min(step, d))
			renewed, err := s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-fresh", IfRevision: session.Revision}, false)
			if err != nil {
				t.Fatal(err)
			}
			session = renewed
		}
		return session
	}
	views := s.sessionObservations(ctx, advance(2*time.Minute))
	if views["activity"].Value == nil {
		t.Fatalf("fresh at the boundary: %+v", views["activity"])
	}
	views = s.sessionObservations(ctx, advance(time.Second))
	if views["activity"].Value != nil || views["activity"].Reason != "observation_stale" || views["model"].Value == nil {
		t.Fatalf("after the boundary: %+v %+v", views["activity"], views["model"])
	}
	// The same content confirms the value and keeps its source time; an older, different
	// report changes nothing.
	if err := s.Observe(ctx, Observation{Caller: Key{"codex", "synthetic-fresh"}, Activity: &ObservedValue{Source: "codex_rollout", At: at, State: "busy"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, Observation{Caller: Key{"codex", "synthetic-fresh"}, Activity: &ObservedValue{Source: "codex_rollout", At: at - 1, State: "idle"}}); err != nil {
		t.Fatal(err)
	}
	views = s.sessionObservations(ctx, session)
	if v := views["activity"].Value; v == nil || v.State != "busy" || v.At != at {
		t.Fatalf("confirmed: %+v", views["activity"])
	}
	// Without confirmation the model goes stale after thirty minutes.
	views = s.sessionObservations(ctx, advance(30*time.Minute+time.Second))
	if views["model"].Reason != "observation_stale" {
		t.Fatalf("model: %+v", views["model"])
	}
}

func TestDashboardSessionPaging(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	want := map[Key]bool{}
	for i := range 2*dashboardSessionPage + 37 {
		family := []string{"claude", "codex", "agy"}[i%3]
		id := fmt.Sprintf("synthetic-%04d", i)
		session := join(t, s, family, id, "")
		if i%5 == 0 {
			if _, err := s.Mutate(ctx, Mutation{Family: family, ID: id, IfRevision: session.Revision}, true); err != nil {
				t.Fatal(err)
			}
		}
		want[Key{family, id}] = true
	}
	seen := map[Key]bool{}
	var after *Key
	var previous string
	for pages := 0; ; pages++ {
		page, next, err := s.dashboardSessions(ctx, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > dashboardSessionPage || pages > 10 {
			t.Fatalf("page of %d at %d", len(page), pages)
		}
		for _, r := range page {
			k, order := Key{r.Family, r.ID}, r.Family+"\x00"+r.ID
			if seen[k] || order <= previous {
				t.Fatalf("duplicate or out of order: %v", k)
			}
			seen[k], previous = true, order
		}
		if next == nil {
			break
		}
		after = next
	}
	if len(seen) != len(want) {
		t.Fatalf("listed %d of %d", len(seen), len(want))
	}
}
