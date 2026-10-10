package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A naming report stands on its own group: allowlisted result, a published-name target,
// no terminal fields. Peers and the session view show it only for the current published
// name, so a result computed before a holder change never stands for the new name.
func TestNamingObservation(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	repo := namedRepo(t, "koinon")
	holder := join(t, s, "codex", "synthetic-holder", repo)
	if holder.Alias == "" {
		t.Fatalf("no address held: %+v", holder)
	}
	at := clock.UnixMilli()
	report := func(v ObservedValue) error {
		return s.Observe(context.Background(), Observation{Caller: Key{"codex", holder.ID}, Naming: &v})
	}
	for _, bad := range []ObservedValue{
		{Source: "koinon_mcp", At: at, Naming: "made_up", Target: holder.Alias},
		{Source: "koinon_mcp", At: at, Naming: "renamed", Target: "Not A Name"},
		{Source: "koinon_mcp", At: at, Naming: "renamed", Target: holder.Alias, Pane: "%1"},
		{Source: "tmux_env", At: at, Naming: "renamed", Target: holder.Alias},
	} {
		if err := report(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
	if err := report(ObservedValue{Source: "koinon_mcp", At: at, Naming: "attach_pane_not_found", Target: holder.Alias}); err != nil {
		t.Fatal(err)
	}
	naming := func() *PeerNaming {
		t.Helper()
		peers, _, err := s.Peers(context.Background(), Key{"codex", holder.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range peers {
			if p.Name == holder.Name {
				return p.Naming
			}
		}
		t.Fatal("holder not listed")
		return nil
	}
	got := naming()
	if got == nil || got.Result != "attach_pane_not_found" || got.Target != holder.Alias || got.Reason != NamingReason("attach_pane_not_found") || got.Reason == "" {
		t.Fatalf("peer naming: %+v", got)
	}
	view := s.sessionObservations(context.Background(), sessionOf(t, s, "codex", holder.ID))["naming"]
	if view.Value == nil || view.Value.Naming != "attach_pane_not_found" {
		t.Fatalf("session view: %+v", view)
	}

	// The holder loses the address: its result for the address is outdated.
	if _, err := s.db.Exec(`UPDATE names SET holder_id='' WHERE name=?`, holder.Alias); err != nil {
		t.Fatal(err)
	}
	if got := naming(); got != nil {
		t.Fatalf("outdated result shown: %+v", got)
	}
	view = s.sessionObservations(context.Background(), sessionOf(t, s, "codex", holder.ID))["naming"]
	if view.Value != nil || view.Reason != "naming_outdated" {
		t.Fatalf("outdated session view: %+v", view)
	}

	// A result goes stale when no report confirms it.
	if err := report(ObservedValue{Source: "koinon_mcp", At: at + 1, Naming: "not_in_tmux", Target: holder.Name}); err != nil {
		t.Fatal(err)
	}
	if got := naming(); got == nil || got.Result != "not_in_tmux" {
		t.Fatalf("peer name result: %+v", got)
	}
	s.observed.mu.Lock()
	v := s.observed.byKey[Key{"codex", holder.ID}]["naming"]
	v.seen -= (3 * time.Minute).Milliseconds()
	s.observed.byKey[Key{"codex", holder.ID}]["naming"] = v
	s.observed.mu.Unlock()
	if got := naming(); got != nil {
		t.Fatalf("stale result shown: %+v", got)
	}
}

// Naming must be visible even without a terminal observation, and a delayed result for
// the former published name must disappear from both full pages and refresh fragments.
func TestDashboardNamingProjection(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	holder := join(t, s, "codex", "synthetic-naming", namedRepo(t, "namingrepo"))
	caller := Key{holder.Family, holder.ID}
	at := s.now().UnixMilli()
	if err := s.Observe(context.Background(), Observation{Caller: caller,
		Naming: &ObservedValue{Source: "koinon_mcp", At: at, Naming: "attach_pane_not_found", Target: holder.Alias}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET succession=? WHERE family=? AND id=?`,
		`{"result":"refused","reason":"holder_active","address":"codex-namingrepo","at":1800000000000}`,
		holder.Family, holder.ID); err != nil {
		t.Fatal(err)
	}
	client := newActionClient(t, d, root)
	pages := func(want string, known bool) {
		t.Helper()
		for _, path := range []string{"/dashboard/sessions", "/dashboard/sessions?fragment=1"} {
			r := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
			if r.status != http.StatusOK || !strings.Contains(r.body, want) {
				t.Fatalf("%s missing %q: status %d", path, want, r.status)
			}
			if !strings.Contains(r.body, "succession: refused (holder_active)") {
				t.Fatal("last succession result is not shown")
			}
			if known {
				for _, text := range []string{"naming: attach_pane_not_found", NamingReason("attach_pane_not_found"), holder.Alias} {
					if !strings.Contains(r.body, text) {
						t.Fatalf("%s missing %q", path, text)
					}
				}
			} else if strings.Contains(r.body, "naming: attach_pane_not_found") {
				t.Fatal("old naming result is still shown")
			}
		}
	}
	pages("unknown: not_observed", true)
	if _, err := s.db.Exec(`UPDATE names SET holder_id='' WHERE name=?`, holder.Alias); err != nil {
		t.Fatal(err)
	}
	pages("naming unknown: naming_outdated", false)
	s.observed.mu.Lock()
	v := s.observed.byKey[caller]["naming"]
	v.seen -= (3 * time.Minute).Milliseconds()
	s.observed.byKey[caller]["naming"] = v
	s.observed.mu.Unlock()
	pages("naming unknown: observation_stale", false)
}
