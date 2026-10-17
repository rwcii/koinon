package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The command line's cleanup (#388) selects by name, state, family and inactivity, never the
// maintainer session, and changes each session through the dashboard's store path under its
// own audit record by cli.
func TestCleanupSelectionAndChanges(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a := join(t, s, "codex", "synthetic-a", "")
	b := join(t, s, "claude", "synthetic-b", "")
	c := join(t, s, "agy", "synthetic-c", "")
	if _, err := s.Mutate(ctx, Mutation{Family: c.Family, ID: c.ID, IfRevision: c.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Key{a.Family, a.ID}, b.Name, "for the cleanup"); err != nil {
		t.Fatal(err)
	}
	// a and b expire at 1060; c retired at 1000; d registers at 1400.
	clock = time.Unix(1400, 0)
	d := join(t, s, "codex", "synthetic-d", "")
	names := func(f CleanupFilter) []string {
		t.Helper()
		list, more, err := s.SelectCleanup(ctx, f)
		if err != nil || more {
			t.Fatalf("%+v: %v %v", f, err, more)
		}
		var got []string
		for _, c := range list {
			got = append(got, c.Name)
		}
		return got
	}
	same := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, name := range got {
			seen[name] = true
		}
		for _, name := range want {
			if !seen[name] {
				return false
			}
		}
		return true
	}
	for _, c2 := range []struct {
		f    CleanupFilter
		want []string
	}{
		{CleanupFilter{}, []string{a.Name, b.Name, c.Name, d.Name}},
		{CleanupFilter{State: "expired"}, []string{a.Name, b.Name}},
		{CleanupFilter{State: "retired"}, []string{c.Name}},
		{CleanupFilter{State: "active"}, []string{d.Name}},
		{CleanupFilter{Family: "codex"}, []string{a.Name, d.Name}},
		{CleanupFilter{InactiveForSeconds: 350}, []string{c.Name}},
		{CleanupFilter{InactiveForSeconds: 300}, []string{a.Name, b.Name, c.Name}},
		{CleanupFilter{Names: []string{a.Name, d.Name}, State: "expired"}, []string{a.Name}},
	} {
		if got := names(c2.f); !same(got, c2.want...) {
			t.Fatalf("%+v: %v, want %v", c2.f, got, c2.want)
		}
	}
	for _, f := range []CleanupFilter{{Names: []string{"synthetic-nobody"}}, {Names: []string{"maintainer"}}} {
		if _, _, err := s.SelectCleanup(ctx, f); code(err) != "peer_not_found" {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	if _, _, err := s.SelectCleanup(ctx, CleanupFilter{State: "gone"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown state: %v", err)
	}
	selected := func(name string) CleanupSession {
		t.Helper()
		list, _, err := s.SelectCleanup(ctx, CleanupFilter{Names: []string{name}})
		if err != nil || len(list) != 1 {
			t.Fatalf("%s: %v %v", name, list, err)
		}
		return list[0]
	}
	if got := selected(b.Name); got.Unacknowledged != 1 || got.State != "expired" || got.InactiveSince != 1060000 {
		t.Fatalf("b: %+v", got)
	}
	lastAudit := func() AuditRecord {
		t.Helper()
		return auditAll(t, s)[0]
	}
	for _, step := range []struct {
		action, name, target string
	}{
		{"purge", a.Name, "codex:synthetic-a by cli"},
		{"retire", d.Name, "codex:synthetic-d by cli"},
		{"clear", b.Name, "claude:synthetic-b by cli"},
		{"unpurge", a.Name, "codex:synthetic-a by cli"},
	} {
		if r := s.applyCleanup(ctx, step.action, selected(step.name)); r.Result != "accepted" {
			t.Fatalf("%s %s: %+v", step.action, step.name, r)
		}
		if r := lastAudit(); r.Action != step.action || r.Result != "accepted" || r.Target != step.target {
			t.Fatalf("%s audit: %+v", step.action, r)
		}
	}
	if got := selected(d.Name); got.State != "retired" {
		t.Fatalf("d after retire: %+v", got)
	}
	if got := selected(b.Name); got.Unacknowledged != 0 {
		t.Fatalf("b after clear: %+v", got)
	}
	if got := selected(a.Name); got.PurgeMarked {
		t.Fatalf("a after unpurge: %+v", got)
	}
	// A session that changed after its selection is refused and audited.
	stale := selected(a.Name)
	stale.Revision--
	if r := s.applyCleanup(ctx, "purge", stale); r.Result != "refused" || r.Code != "revision_changed" {
		t.Fatalf("stale: %+v", r)
	}
	if r := lastAudit(); r.Result != "refused" || r.Reason != "revision_changed" || r.Target != "codex:synthetic-a by cli" {
		t.Fatalf("stale audit: %+v", r)
	}
	// Retire applies to active sessions only, unpurge to marked ones only.
	if cleanupApplies("retire", selected(d.Name)) || cleanupApplies("unpurge", selected(a.Name)) || !cleanupApplies("purge", selected(a.Name)) {
		t.Fatal("applies")
	}
}

// The routes refuse a change without a selection, show the selection without apply, apply
// it with apply, and run the sweep, which deletes a purge-marked session.
func TestCleanupRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-a", "")
	b := join(t, s, "claude", "synthetic-b", "")
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if status, reply := post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "retire", "apply": true}); status != 400 || reply["code"] != "invalid_request" {
		t.Fatalf("no selection: %d %v", status, reply)
	}
	status, reply := post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "list"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 2 {
		t.Fatalf("list: %d %v", status, reply)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "purge", "family": "codex"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 1 || reply["applied"] != false || reply["results"] != nil {
		t.Fatalf("preview: %d %v", status, reply)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt != 0 || got.State != "active" {
		t.Fatalf("preview changed a: %+v", got)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "purge", "names": []string{a.Name}, "apply": true})
	results, _ := reply["results"].([]any)
	if status != 200 || len(results) != 1 || results[0].(map[string]any)["result"] != "accepted" {
		t.Fatalf("purge: %d %v", status, reply)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt == 0 || got.State != "retired" {
		t.Fatalf("purged a: %+v", got)
	}
	status, reply = post(t, address, secret, "/v1/maintenance/sweep", map[string]any{})
	counts, _ := reply["counts"].(map[string]any)
	if status != 200 || counts == nil || counts["sessions_by_purge"] != float64(1) {
		t.Fatalf("sweep: %d %v", status, reply)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "list"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 1 || listed[0].(map[string]any)["name"] != b.Name {
		t.Fatalf("after the sweep: %d %v", status, reply)
	}
}
