package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPeerStatusObservationsAndFreshness(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	repo := namedRepo(t, "example")
	caller := Key{"claude", "synthetic-reader"}
	join(t, s, caller.Family, caller.ID, repo)
	target := join(t, s, "codex", "synthetic-target", repo)
	key := Key{target.Family, target.ID}
	at := clock.Add(-time.Minute).UnixMilli()
	if err := s.Observe(ctx, Observation{Caller: key,
		Model:   &ObservedValue{Source: "codex_rollout", At: at, ID: "gpt-synthetic"},
		Context: &ObservedValue{Source: "codex_rollout", At: at, LimitTokens: int64p(1000), UsedTokens: int64p(250), UsageAvailable: boolp(true)},
	}); err != nil {
		t.Fatal(err)
	}
	read := func(name string) PeerStatus {
		t.Helper()
		status, err := s.PeerStatus(ctx, caller, name)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	// Registration and pending/unacknowledged delivery do not imply activity.
	if _, err := s.Send(ctx, caller, target.Name, "synthetic message"); err != nil {
		t.Fatal(err)
	}
	if status := read(target.Name); status.State != "active" || status.Activity.Known || status.Activity.Reason != "not_observed" {
		t.Fatalf("registration/delivery inferred activity: %+v", status)
	}
	for _, state := range []string{"busy", "idle", "waiting"} {
		clock = clock.Add(time.Second)
		if err := s.Observe(ctx, Observation{Caller: key, Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: state}}); err != nil {
			t.Fatal(err)
		}
		status := read(target.Name)
		if !status.Activity.Known || status.Activity.State != state || !status.Context.Known || *status.Context.UsedTokens != 250 ||
			status.Context.At != at || status.Context.ConfirmedAt != time.Unix(1_800_000_000, 0).UnixMilli() || status.Context.StaleAfterMS != 30*time.Minute.Milliseconds() {
			t.Fatalf("status: %+v", status)
		}
		if alias := read(target.Alias); !reflect.DeepEqual(status, alias) {
			t.Fatalf("alias mismatch: %+v %+v", status, alias)
		}
		views := s.sessionObservations(ctx, target)
		if status.Activity.State != views["activity"].Value.State || status.Activity.ConfirmedAt != views["activity"].ConfirmedAt {
			t.Fatal("dashboard projection differs")
		}
	}
	clock = clock.Add(2*time.Minute + time.Millisecond)
	join(t, s, caller.Family, caller.ID, repo)
	target = join(t, s, target.Family, target.ID, repo)
	if status := read(target.Name); status.Activity.Known || status.Activity.Reason != "observation_stale" || !status.Context.Known {
		t.Fatalf("stale activity: %+v", status)
	}
	// A confirmation retains the original source timestamp and reports its new receipt time.
	if err := s.Observe(ctx, Observation{Caller: key, Context: &ObservedValue{Source: "codex_rollout", At: at,
		LimitTokens: int64p(1000), UsedTokens: int64p(250), UsageAvailable: boolp(true)}}); err != nil {
		t.Fatal(err)
	}
	if status := read(target.Name); status.Context.At != at || status.Context.ConfirmedAt != clock.UnixMilli() {
		t.Fatalf("confirmation: %+v", status.Context)
	}
	// Keep the registrations alive while context goes stale.
	for range 31 {
		clock = clock.Add(time.Minute)
		join(t, s, caller.Family, caller.ID, repo)
		join(t, s, target.Family, target.ID, repo)
	}
	if status := read(target.Name); status.Context.Known || status.Context.Reason != "observation_stale" {
		t.Fatalf("stale context: %+v", status.Context)
	}
}

func TestPeerStatusUnknownInactiveAndRestart(t *testing.T) {
	s, root := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	repo := namedRepo(t, "example")
	caller := Key{"claude", "synthetic-reader"}
	join(t, s, caller.Family, caller.ID, repo)
	for _, family := range []string{"claude", "codex", "agy", "opencode", "deepseek"} {
		target := join(t, s, family, "synthetic-"+family, repo)
		status, err := s.PeerStatus(ctx, caller, target.Name)
		if err != nil || status.Activity.Known || status.Context.Known {
			t.Fatalf("%s: %+v %v", family, status, err)
		}
		if status.Activity.Reason != unobserved("activity", family) || status.Context.Reason != unobserved("context", family) {
			t.Fatalf("%s reasons: %+v", family, status)
		}
	}
	target := join(t, s, "codex", "synthetic-codex", repo)
	if err := s.Observe(ctx, Observation{Caller: Key{target.Family, target.ID}, Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: "idle"}}); err != nil {
		t.Fatal(err)
	}
	// Restart retains registrations but never presents retained observations as current.
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	s = reopened
	s.now = func() time.Time { return clock }
	if status, err := s.PeerStatus(ctx, caller, target.Name); err != nil || status.Activity.Known || status.Activity.Reason != "not_observed" {
		t.Fatalf("restart: %+v %v", status, err)
	}
	clock = clock.Add(901 * time.Second)
	join(t, s, caller.Family, caller.ID, repo)
	if status, err := s.PeerStatus(ctx, caller, target.Name); err != nil || status.State != "expired" || status.Activity.Reason != "session_expired" {
		t.Fatalf("expired: %+v %v", status, err)
	}
	if _, err := s.PeerStatus(ctx, caller, target.Alias); !errors.Is(err, ErrAliasUnheld) {
		t.Fatalf("unheld alias: %v", err)
	}
	target = join(t, s, target.Family, target.ID, repo)
	if _, err := s.Mutate(ctx, Mutation{Family: target.Family, ID: target.ID, IfRevision: target.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if status, err := s.PeerStatus(ctx, caller, target.Name); err != nil || status.State != "retired" || status.Context.Reason != "session_retired" {
		t.Fatalf("retired: %+v %v", status, err)
	}
	if _, err := s.PeerStatus(ctx, caller, "codex-missing-aa"); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("missing peer: %v", err)
	}
	if _, err := s.PeerStatus(ctx, Key{"codex", "synthetic-missing"}, target.Name); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("inactive caller: %v", err)
	}
}

func TestPeerStatusAPIFieldsAndProviderBudget(t *testing.T) {
	d, root := startTestDaemon(t)
	ctx := context.Background()
	repo := namedRepo(t, "example")
	caller := Key{"codex", "synthetic-reader"}
	join(t, d.store, caller.Family, caller.ID, repo)
	target := join(t, d.store, "claude", "synthetic-target", repo)
	now := d.store.now().UnixMilli()
	if err := d.store.Observe(ctx, Observation{Caller: Key{target.Family, target.ID},
		Terminal: &ObservedValue{Source: "tmux_env", At: now, Socket: "/tmp/synthetic-private-socket", Pane: "%42", Session: "private-terminal"},
		Activity: &ObservedValue{Source: "claude_registry", At: now, State: "busy"}}); err != nil {
		t.Fatal(err)
	}
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"caller": caller, "peer": target.Alias}
	if code, result := post(t, d.Addresses()[0], "", "/v1/peers/status", body); code != 401 || result["code"] != "unauthorized" {
		t.Fatalf("unauthorized: %d %v", code, result)
	}
	code, result := post(t, d.Addresses()[0], secret, "/v1/peers/status", body)
	if code != 200 {
		t.Fatalf("status: %d %v", code, result)
	}
	peer := result["peer"].(map[string]any)
	for field := range peer {
		switch field {
		case "name", "alias", "family", "state", "repository", "observed_at", "model", "context", "activity":
		default:
			t.Fatalf("private peer field %q", field)
		}
	}
	allowed := map[string]map[string]bool{
		"model": {"id": true}, "context": {"limit_tokens": true, "used_tokens": true, "usage_available": true}, "activity": {"state": true},
	}
	for group, fields := range allowed {
		for field := range peer[group].(map[string]any) {
			if !fields[field] && field != "known" && field != "reason" && field != "source" && field != "at" && field != "confirmed_at" && field != "stale_after_ms" {
				t.Fatalf("private %s field %q", group, field)
			}
		}
	}
	data, _ := json.Marshal(result)
	for _, value := range []string{target.ID, "synthetic-private-socket", "private-terminal", secret} {
		if bytes.Contains(data, []byte(value)) {
			t.Fatalf("private value %q in status", value)
		}
	}
	// Provider reads share the bounded context used by the dashboard projection.
	d.store.observed.mu.Lock()
	d.store.observed.extras = func(ctx context.Context, _ Session) map[string]ObservedValue {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Error("provider read has no two-second budget")
		}
		return map[string]ObservedValue{"activity": {Source: "opencode_status", At: now + 1, State: "waiting"}}
	}
	d.store.observed.mu.Unlock()
	status, err := d.store.PeerStatus(ctx, caller, target.Name)
	if err != nil || status.Activity.State != "waiting" || status.Activity.Source != "opencode_status" {
		t.Fatalf("provider projection: %+v %v", status, err)
	}
}
