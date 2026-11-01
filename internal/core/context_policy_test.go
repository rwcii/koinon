package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// withCycleAvailable makes a verified cycle adapter available to every family for one test.
func withCycleAvailable(t *testing.T) {
	t.Helper()
	previous := contextCycleProbe
	contextCycleProbe = func(*Store, context.Context, Key) CycleProbe { return CycleProbe{Entry: "koinon-pickup"} }
	t.Cleanup(func() { contextCycleProbe = previous })
}

func policyOf(t *testing.T, s *Store, family string) ContextFamilyPolicy {
	t.Helper()
	for _, p := range s.ContextPolicy() {
		if p.Family == family {
			return p
		}
	}
	t.Fatalf("no policy for %s", family)
	return ContextFamilyPolicy{}
}

// Family defaults are the approved ones; a change is checked, stored with its audit
// record, survives a restart and enables nothing; a stored value that is not valid keeps
// the default and names the error; concurrent changes leave one consistent value.
func TestContextFamilyPolicy(t *testing.T) {
	s, root := testStore(t)
	want := map[string]string{"claude": "handoff_cycle", "codex": "native_compaction", "agy": "observe", "opencode": "observe", "grok": "observe", "deepseek": "observe"}
	if len(s.ContextPolicy()) != len(want) {
		t.Fatalf("families: %+v", s.ContextPolicy())
	}
	for family, mode := range want {
		if p := policyOf(t, s, family); p.Mode != mode || p.Threshold != 75 || p.Error != "" {
			t.Fatalf("%s default: %+v", family, p)
		}
	}
	join(t, s, "claude", "synthetic-a", "")
	for name, c := range map[string]struct {
		family, mode string
		threshold    int64
	}{
		"compaction outside codex": {"claude", "native_compaction", 75},
		"zero threshold":           {"claude", "observe", 0},
		"threshold over 100":       {"claude", "observe", 101},
		"unknown mode":             {"claude", "compact", 75},
		"unknown family":           {"maintainer", "observe", 75},
	} {
		if err := s.SetContextPolicy(roomCtx("context-family-settings"), c.family, c.mode, c.threshold); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM settings WHERE name LIKE 'context_%'`) != 0 {
		t.Fatal("a refused change was stored")
	}
	if err := s.SetContextPolicy(roomCtx("context-family-settings"), "claude", "observe", 60); err != nil {
		t.Fatal(err)
	}
	if p := policyOf(t, s, "claude"); p.Mode != "observe" || p.Threshold != 60 {
		t.Fatalf("applied: %+v", p)
	}
	if count(t, s, `SELECT COUNT(*) FROM context_enables`) != 0 || count(t, s, `SELECT COUNT(*) FROM audit WHERE action='context-family-settings' AND result='accepted'`) != 1 {
		t.Fatal("a family change enabled a session or was not audited once")
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 8; i++ {
		wg.Add(1)
		go func(threshold int64) {
			defer wg.Done()
			s.SetContextPolicy(roomCtx("context-family-settings"), "codex", "handoff_cycle", threshold)
		}(i * 10)
	}
	wg.Wait()
	stored := count(t, s, `SELECT value FROM settings WHERE name='context_threshold_codex'`)
	if p := policyOf(t, s, "codex"); p.Threshold != stored || p.Mode != "handoff_cycle" {
		t.Fatalf("concurrent changes: applied %+v, stored %d", p, stored)
	}
	// A restart reads the stored values; a value that is not valid keeps the default, also
	// one that is not an integer, and it does not stop the store from opening.
	if _, err := fixtureExec(s, `INSERT INTO settings(name,value,updated_at) VALUES ('context_mode_agy',1,1),('context_threshold_agy',50,1),
		('context_mode_grok',0,1),('context_threshold_grok','synthetic-not-an-integer',1),('context_mode_opencode',0.5,1),('context_threshold_opencode',75,1)`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	if p := policyOf(t, s, "claude"); p.Mode != "observe" || p.Threshold != 60 {
		t.Fatalf("after restart: %+v", p)
	}
	for _, family := range []string{"agy", "grok", "opencode"} {
		if p := policyOf(t, s, family); p.Mode != "observe" || p.Threshold != 75 || p.Error != "stored_value_invalid" {
			t.Fatalf("invalid stored value: %+v", p)
		}
	}
	if got := s.Settings(); got != DefaultSettings {
		t.Fatalf("timing settings after invalid context values: %+v", got)
	}
}

// Enabling needs a verified cycle, which no family has yet: it refuses and writes nothing.
// With one available, the Enable pins its session; a stale Enable or session revision
// refuses; disabling is always accepted, also for an expired session; deleting the session
// deletes its Enable.
func TestContextEnable(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }
	a := join(t, s, "claude", "synthetic-a", "")
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true); !errors.Is(err, ErrContextCycleUnavailable) {
		t.Fatalf("enable without a cycle: %v", err)
	}
	if count(t, s, `SELECT COUNT(*) FROM context_enables`) != 0 {
		t.Fatal("a refused Enable was stored")
	}
	if e, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, false); err != nil || e.Revision != 0 || count(t, s, `SELECT COUNT(*) FROM context_enables`) != 0 {
		t.Fatalf("disable without a record: %+v %v", e, err)
	}
	withCycleAvailable(t)
	e, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true)
	if err != nil || !e.Enabled || e.Revision != 1 {
		t.Fatalf("enable: %+v %v", e, err)
	}
	if count(t, s, `SELECT session_revision FROM context_enables`) != a.Revision {
		t.Fatal("the Enable did not pin the session revision")
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, false); code(err) != "context_enable_changed" {
		t.Fatalf("stale Enable revision: %v", err)
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision+1, 1, false); code(err) != "revision_changed" {
		t.Fatalf("stale session revision: %v", err)
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), "no-such-peer", 1, 0, false); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("unknown peer: %v", err)
	}
	// The session expires; disabling is still accepted, enabling is not.
	clock = clock.Add(2 * time.Minute)
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 1, true); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("enable an expired session: %v", err)
	}
	if e, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 1, false); err != nil || e.Enabled || e.Revision != 2 {
		t.Fatalf("disable an expired session: %+v %v", e, err)
	}
	if deleted, err := s.deleteSession(ctx, Key{"claude", "synthetic-a"}, "?1=1", 1); err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if count(t, s, `SELECT COUNT(*) FROM context_enables`) != 0 {
		t.Fatal("the deleted session's Enable remained")
	}
}

// The selection reads the policy, Enable, cycle availability, counts, epoch and both clocks
// and returns one fixed reason or eligible.
func TestContextTriggerSelection(t *testing.T) {
	now := int64(10_000_000)
	policy := ContextFamilyPolicy{Family: "claude", Mode: ContextHandoffCycle, Threshold: 75}
	evidence := func(used, limit int64, sourceAge, confirmAge time.Duration) ContextEvidence {
		return ContextEvidence{Found: true, UsedTokens: int64p(used), LimitTokens: int64p(limit), Epoch: "synthetic-epoch",
			SourceAt: now - sourceAge.Milliseconds(), ConfirmedAt: now - confirmAge.Milliseconds()}
	}
	base := ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true, Evidence: evidence(75, 100, time.Second, time.Second), Now: now}
	for name, c := range map[string]struct {
		change func(*ContextTriggerInput)
		want   string
	}{
		"equality is eligible":       {func(*ContextTriggerInput) {}, ""},
		"over the limit is eligible": {func(in *ContextTriggerInput) { in.Evidence = evidence(150, 100, time.Second, time.Second) }, ""},
		"below the threshold":        {func(in *ContextTriggerInput) { in.Evidence = evidence(74, 100, time.Second, time.Second) }, "below_threshold"},
		"inactive":                   {func(in *ContextTriggerInput) { in.Active = false }, "session_inactive"},
		"observe mode":               {func(in *ContextTriggerInput) { in.Policy.Mode = ContextObserve }, "mode_observe"},
		"native compaction":          {func(in *ContextTriggerInput) { in.Policy.Mode = ContextNativeCompaction }, ""},
		"overwatch conflict":         {func(in *ContextTriggerInput) { in.OverwatchConflict = true }, "overwatch_name_conflict"},
		"compaction sends no request": {func(in *ContextTriggerInput) {
			in.Policy.Mode, in.OverwatchConflict = ContextNativeCompaction, true
		}, ""},
		"not enabled":                {func(in *ContextTriggerInput) { in.Enabled = false }, "not_enabled"},
		"no cycle":                   {func(in *ContextTriggerInput) { in.CycleAvailable = false }, "context_cycle_unavailable"},
		"no observation":             {func(in *ContextTriggerInput) { in.Evidence = ContextEvidence{} }, "context_unknown"},
		"unknown limit":              {func(in *ContextTriggerInput) { in.Evidence.LimitTokens, in.Evidence.UsedTokens = nil, nil }, "context_counts_unavailable"},
		"zero limit":                 {func(in *ContextTriggerInput) { in.Evidence.LimitTokens = int64p(0) }, "context_counts_invalid"},
		"no epoch":                   {func(in *ContextTriggerInput) { in.Evidence.Epoch = "" }, "epoch_unverified"},
		"future source":              {func(in *ContextTriggerInput) { in.Evidence.SourceAt = now + 1 }, "observation_future"},
		"future confirmation":        {func(in *ContextTriggerInput) { in.Evidence.ConfirmedAt = now + 1 }, "observation_future"},
		"old usage confirmed lately": {func(in *ContextTriggerInput) { in.Evidence = evidence(90, 100, 2*time.Hour, time.Minute) }, "source_stale"},
		"stale confirmation":         {func(in *ContextTriggerInput) { in.Evidence = evidence(90, 100, time.Minute, 91*time.Second) }, "confirmation_stale"},
		"latched epoch":              {func(in *ContextTriggerInput) { in.LatchedEpoch = "synthetic-epoch" }, "already_selected_for_epoch"},
	} {
		in := base
		c.change(&in)
		eligible, reason := SelectContextTrigger(in)
		if reason != c.want || eligible != (c.want == "") {
			t.Errorf("%s: %v %q, want %q", name, eligible, reason, c.want)
		}
	}
	// The latch holds for unchanged high samples and clears on a new epoch or compaction.
	var latches contextLatches
	k := Key{"claude", "synthetic-a"}
	high := evidence(90, 100, time.Second, time.Second)
	latches.mark(k, "synthetic-epoch")
	if latches.latched(k, high, 75) != "synthetic-epoch" {
		t.Fatal("an unchanged high sample cleared the latch")
	}
	renewed := high
	renewed.Epoch = "synthetic-new-epoch"
	if latches.latched(k, renewed, 75) != "" {
		t.Fatal("a new epoch kept the latch")
	}
	latches.mark(k, "synthetic-epoch")
	if latches.latched(k, evidence(10, 100, time.Second, time.Second), 75) != "" || latches.latched(k, high, 75) != "" {
		t.Fatal("a compaction kept the latch")
	}
}

// A context observation keeps its native epoch privately: the trigger reads it, no view
// shows it, other groups and sources cannot carry one, and a restart forgets it.
func TestContextEpochObservation(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	join(t, s, "claude", "synthetic-a", "")
	k := Key{"claude", "synthetic-a"}
	now := s.now().UnixMilli()
	o := Observation{Caller: k, Context: &ObservedValue{Source: "claude_statusline", At: now, LimitTokens: int64p(200000), UsedTokens: int64p(160000), Epoch: "synthetic-transcript-1"}}
	if err := s.Observe(ctx, o); err != nil {
		t.Fatal(err)
	}
	if e := s.contextEvidence(k); !e.Found || e.Epoch != "synthetic-transcript-1" || *e.UsedTokens != 160000 {
		t.Fatalf("evidence: %+v", e)
	}
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, "claude", "synthetic-a"), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(s.sessionObservations(ctx, session))
	if strings.Contains(string(view), "synthetic-transcript") || strings.Contains(string(view), "epoch") {
		t.Fatalf("a view shows the epoch: %s", view)
	}
	for name, bad := range map[string]Observation{
		"activity epoch": {Caller: k, Activity: &ObservedValue{Source: "claude_registry", At: now, State: "idle", Epoch: "x"}},
		"model epoch":    {Caller: k, Model: &ObservedValue{Source: "claude_statusline", At: now, ID: "m", Epoch: "x"}},
		"invalid epoch":  {Caller: k, Context: &ObservedValue{Source: "claude_statusline", At: now, Epoch: "has space"}},
	} {
		if err := s.Observe(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A /clear reports a new epoch; the evidence follows it.
	o.Context.At, o.Context.Epoch, o.Context.UsedTokens = now+1, "synthetic-transcript-2", int64p(1000)
	if err := s.Observe(ctx, o); err != nil || s.contextEvidence(k).Epoch != "synthetic-transcript-2" {
		t.Fatalf("new epoch: %v %+v", err, s.contextEvidence(k))
	}
	s.db.Close()
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	if e := s.contextEvidence(k); e.Found {
		t.Fatalf("a restart kept the evidence: %+v", e)
	}
}

// The dashboard changes the family policy and a session's Enable only through logged-in,
// same-origin, CSRF-checked actions; the session row shows the trigger reason and ages
// without the native epoch.
func TestDashboardContextPolicy(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "claude", "synthetic-a", "")
	now := s.now().UnixMilli()
	if err := s.Observe(context.Background(), Observation{Caller: Key{"claude", "synthetic-a"}, Context: &ObservedValue{Source: "claude_statusline", At: now,
		LimitTokens: int64p(1000), UsedTokens: int64p(900), Epoch: "synthetic-private-epoch"}}); err != nil {
		t.Fatal(err)
	}
	client := newActionClient(t, d, root)
	form := url.Values{"family": {"claude"}, "mode": {"handoff_cycle"}, "threshold": {"80"}}
	for name, c := range map[string]struct {
		cookie string
		csrf   bool
		origin string
		status int
	}{
		"no session": {"", true, "", 401},
		"no token":   {client.cookie, false, "", 403},
		"foreign":    {client.cookie, true, "http://evil.example", 403},
	} {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		if c.csrf {
			f.Set("csrf", client.csrf)
		}
		change := client.sameOrigin
		if c.origin != "" {
			origin := c.origin
			change = func(r *http.Request) { r.Header.Set("Origin", origin) }
		}
		if r := dashboardDo(t, d, "POST", "/dashboard/actions/context-family-settings", c.cookie, change, f); r.status != c.status {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM settings WHERE name LIKE 'context_%'`) != 0 || len(auditAll(t, s)) != 0 {
		t.Fatal("a refused request changed state")
	}
	if got := client.do("context-family-settings", form); got != "context_policy_saved" || policyOf(t, s, "claude").Threshold != 80 {
		t.Fatalf("family change: %s", got)
	}
	if got := client.do("context-family-settings", url.Values{"family": {"claude"}, "mode": {"native_compaction"}, "threshold": {"80"}}); got != "invalid_request" {
		t.Fatalf("compaction for claude: %s", got)
	}
	enable := url.Values{"peer": {a.Name}, "revision": {strconv.FormatInt(a.Revision, 10)}, "if_revision": {"0"}, "enabled": {"on"}}
	if got := client.do("context-enable", enable); got != "context_cycle_unavailable" || count(t, s, `SELECT COUNT(*) FROM context_enables`) != 0 {
		t.Fatalf("enable: %s", got)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "handoff handoff_cycle at 80%, not enabled; cycle unavailable: host_unpinned; not_enabled; source 0 s, confirmed 0 s ago") ||
		!strings.Contains(page.body, `<button type="submit" disabled title="The handoff cycle is unavailable for this session: host_unpinned.">Enable handoff</button>`) || strings.Contains(page.body, "synthetic-private-epoch") {
		t.Fatalf("sessions page: %d %s", page.status, page.body)
	}
	// A session's own threshold (#391) shows on its row; 0 returns it to the family's.
	if got := client.do("context-threshold", url.Values{"peer": {a.Name}, "threshold": {"101"}}); got != "invalid_request" {
		t.Fatalf("threshold 101: %s", got)
	}
	if got := client.do("context-threshold", url.Values{"peer": {a.Name}, "threshold": {"60"}}); got != "context_threshold_saved" {
		t.Fatalf("threshold: %s", got)
	}
	page = dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if !strings.Contains(page.body, "handoff handoff_cycle at 60% (session), not enabled") || !strings.Contains(page.body, `name="threshold" type="number" min="0" max="100" required value="60"`) {
		t.Fatalf("sessions page with a session threshold: %s", page.body)
	}
	if got := client.do("context-threshold", url.Values{"peer": {a.Name}, "threshold": {"0"}}); got != "context_threshold_saved" {
		t.Fatalf("threshold 0: %s", got)
	}
	if records := auditAll(t, s); records[0].Target != "context threshold "+a.Name+" family by dashboard" {
		t.Fatalf("audit: %+v", records[0])
	}
	settings := dashboardDo(t, d, "GET", "/dashboard/settings", client.cookie, nil, nil)
	if settings.status != 200 || !strings.Contains(settings.body, "native_compaction") || !strings.Contains(settings.body, "<td>claude</td><td>handoff_cycle</td><td>80%</td>") {
		t.Fatalf("settings page: %d %s", settings.status, settings.body)
	}
}

// A missing pickup skill names the install command on the Sessions page and in context show
// (#406).
func TestContextUnavailableStep(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "claude", "synthetic-a", "")
	previous := contextCycleProbe
	t.Cleanup(func() { contextCycleProbe = previous })
	step := "run koinon install to install the koinon-pickup skill at /synthetic/home/.claude/skills/koinon-pickup/SKILL.md"
	contextCycleProbe = func(*Store, context.Context, Key) CycleProbe {
		return CycleProbe{Reason: "pickup_entry_missing", Step: step}
	}
	_, views, _, err := s.ContextShow(context.Background(), "")
	if err != nil || len(views) != 1 || views[0].Peer != a.Name || views[0].Unavailable != "pickup_entry_missing" || views[0].UnavailableStep != step {
		t.Fatalf("context show: %v %+v", err, views)
	}
	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "cycle unavailable: pickup_entry_missing ("+step+")") ||
		!strings.Contains(page.body, "unavailable for this session: pickup_entry_missing; "+step+".") {
		t.Fatalf("sessions page: %d %s", page.status, page.body)
	}
}

// A schema 21 store gains the Enable table without changing its state; older writers refuse.
func TestContextSchemaTwentyTwoKeepsExistingState(t *testing.T) {
	s, root := testStore(t)
	join(t, s, "codex", "synthetic-a", "")
	room := mustRoom(t, s, "synthetic kept room")
	mustHumanPost(t, s, room, "synthetic kept post")
	before := rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaThirtySeven+undoSchemaThirtySix+undoSchemaThirtyFour+undoSchemaThirtyThree+undoSchemaThirtyTwo+undoSchemaThirtyOne+undoSchemaThirty+undoSchemaTwentyNine+undoSchemaTwentyEight+undoSchemaTwentySeven+undoSchemaTwentySix+undoSchemaTwentyFive+undoSchemaTwentyFour+undoSchemaTwentyThree+undoSchemaTwentyTwo+`PRAGMA user_version=21`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 21); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || count(t, upgraded, `SELECT COUNT(*) FROM context_enables`) != 0 || rowsOf(t, upgraded.db) != before {
		t.Fatal("schema 22 changed existing state")
	}
	if err := checkCatalog(upgraded.db, 21); err == nil {
		t.Fatal("a schema 21 writer would accept the schema 22 store")
	}
}

// Selecting a session's trigger state while its Enable changes is race-free.
func TestContextSelectionRacesDisable(t *testing.T) {
	withCycleAvailable(t)
	s, _ := testStore(t)
	a := join(t, s, "claude", "synthetic-a", "")
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			enables, err := s.contextEnables(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			s.contextRowOf(a, enables[Key{"claude", "synthetic-a"}], s.now().UnixMilli())
		}
	}()
	go func() {
		defer wg.Done()
		s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 1, false)
	}()
	wg.Wait()
	enables, _ := s.contextEnables(context.Background())
	if row := s.contextRowOf(a, enables[Key{"claude", "synthetic-a"}], s.now().UnixMilli()); row.Enable.Enabled || row.Reason != "not_enabled" {
		t.Fatalf("after disable: %+v", row)
	}
}
