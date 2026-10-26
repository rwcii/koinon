package core

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func settingsForm(v Settings) url.Values {
	return url.Values{"lifetime_seconds": {strconv.FormatInt(v.LifetimeSeconds, 10)},
		"renewal_seconds": {strconv.FormatInt(v.RenewalSeconds, 10)}, "guard_seconds": {strconv.FormatInt(v.GuardSeconds, 10)}}
}

func storedSettings(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSettingsValidation: each range, and the renewal interval against the lifetime, are
// checked together; a refused set stores and applies nothing.
func TestSettingsValidation(t *testing.T) {
	s, root := testStore(t)
	if s.Settings() != DefaultSettings || !DefaultSettings.Valid() {
		t.Fatalf("defaults %+v", s.Settings())
	}
	for _, v := range []Settings{
		{59, 29, 30}, {3601, 300, 30}, {900, 29, 30}, {900, 451, 30}, {60, 31, 30},
		{900, 300, 29}, {900, 300, 601}, {900, 1<<63 - 1, 30}, {1<<63 - 1, 300, 30},
	} {
		if err := s.SetSettings(context.Background(), v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v accepted: %v", v, err)
		}
	}
	if s.Settings() != DefaultSettings || storedSettings(t, s) != 0 {
		t.Fatalf("a refused change applied: %+v", s.Settings())
	}
	for _, v := range []Settings{{60, 30, 30}, {3600, 1800, 600}} {
		if err := s.SetSettings(context.Background(), v); err != nil || s.Settings() != v {
			t.Fatalf("%+v: %v", v, err)
		}
	}
	// A reopened store applies the stored values.
	s.db.Close()
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if want := (Settings{3600, 1800, 600}); reopened.Settings() != want {
		t.Fatalf("after reopening: %+v", reopened.Settings())
	}
}

// TestSettingsApplyWithoutRestart: a change applies to the next registration's lifetime,
// the liveness extension and the succession guard at once.
func TestSettingsApplyWithoutRestart(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	v := Settings{LifetimeSeconds: 120, RenewalSeconds: 60, GuardSeconds: 90}
	if err := s.SetSettings(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	none, err := s.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-none", Repository: repo, Directory: repo})
	if err != nil || none.ExpiresAt-none.RenewedAt != 120_000 {
		t.Fatalf("lifetime: %+v %v", none, err)
	}
	h.set(400, 3)
	arrive(t, s, arrival{family: "codex", id: "synthetic-live", repo: repo, pid: 400})
	// arrive registers for 900 seconds; with 59 seconds left, less than half the 120-second
	// lifetime remains.
	advance(841 * time.Second)
	if err := s.MaintainLiveness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "synthetic-live"); got.ExpiresAt != s.now().UnixMilli()+120_000 {
		t.Fatalf("extension: %+v", got)
	}
	s.ToolCall(Key{"codex", "synthetic-live"})
	if left := s.guardLeft(Key{"codex", "synthetic-live"}, s.now().UnixMilli()); left != 90_000 {
		t.Fatalf("guard left %d", left)
	}
}

// TestDashboardSettings: the maintainer changes the values on the settings page; an
// accepted change is audited with its values and applies at once, and the daemon's
// registration reply carries the renewal interval. A refused change is audited as
// refused and stores nothing.
func TestDashboardSettings(t *testing.T) {
	d, root := startTestDaemon(t)
	c := newActionClient(t, d, root)
	n := len(auditAll(t, d.store))
	if got := c.do("settings", settingsForm(Settings{900, 600, 30})); got != "invalid_request" {
		t.Fatalf("interval above half the lifetime: %s", got)
	}
	expectAudit(t, d.store, n, "settings", "refused", "invalid_request")
	// A doubled interval would overflow; the check compares with half the lifetime.
	if got := c.do("settings", settingsForm(Settings{900, 1<<63 - 1, 30})); got != "invalid_request" {
		t.Fatalf("an overflowing interval: %s", got)
	}
	expectAudit(t, d.store, n+1, "settings", "refused", "invalid_request")
	n++
	if storedSettings(t, d.store) != 0 || d.store.Settings() != DefaultSettings {
		t.Fatal("a refused change was stored")
	}
	v := Settings{LifetimeSeconds: 600, RenewalSeconds: 120, GuardSeconds: 45}
	if got := c.do("settings", settingsForm(v)); got != "settings_saved" {
		t.Fatalf("change: %s", got)
	}
	if audit := expectAudit(t, d.store, n+1, "settings", "accepted", ""); audit.Target != v.String()+" by dashboard" {
		t.Fatalf("audit target %q", audit.Target)
	}
	if d.store.Settings() != v {
		t.Fatalf("applied %+v", d.store.Settings())
	}
	page := dashboardDo(t, d, "GET", "/dashboard/settings", c.cookie, nil, nil)
	for _, want := range []string{"600 seconds", "120 seconds", "45 seconds", `action="/dashboard/actions/settings"`} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("settings page lacks %q:\n%s", want, page.body)
		}
	}
	secret, _ := ReadSecret(root)
	repo := testRepo(t)
	_, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register", Registration{Family: "deepseek", ID: "synthetic-reply", Repository: repo, Directory: repo})
	if reply["renewal_seconds"] != float64(120) {
		t.Fatalf("register reply: %v", reply)
	}
}

// TestShortestLifetimeKeepsALiveHost: with the 60-second lifetime, the 30-second sweep
// still extends an idle session whose host runs, before each deadline.
func TestShortestLifetimeKeepsALiveHost(t *testing.T) {
	s, h, advance := successionStore(t)
	if err := s.SetSettings(context.Background(), Settings{60, 30, 30}); err != nil {
		t.Fatal(err)
	}
	h.set(500, 4)
	got := arrive(t, s, arrival{family: "codex", id: "synthetic-short", repo: testRepo(t), pid: 500})
	// A renewal without ttl_seconds takes the 60-second lifetime.
	if got, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: got.ID, IfRevision: got.Revision}, false); err != nil || got.ExpiresAt-got.RenewedAt != 60_000 {
		t.Fatalf("renew: %+v %v", got, err)
	}
	for range 6 {
		advance(30 * time.Second)
		if err := s.MaintainLiveness(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := sessionOf(t, s, "codex", "synthetic-short"); got.State != "active" {
		t.Fatalf("lost with the shortest lifetime: %+v", got)
	}
}
