package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Native compaction (#509): at the threshold, an enabled Codex session in native_compaction gets
// Codex's own /compact in its pane once it is idle, once per crossing, and a fresh reading below
// the threshold after the submission confirms it.

// fakeCompactor is a synthetic compaction adapter: the fake cycle adapter with the compaction
// probe and input. Compact passes the fence before its input, as the Codex adapter does.
type fakeCompactor struct {
	*fakeAdapter
	compactProbe        CycleProbe
	submitted, received bool
	compactErr          error
	beforeCompact       func()
}

func (f *fakeCompactor) CompactProbe(context.Context, CycleTarget) CycleProbe {
	f.record("compact probe")
	return f.compactProbe
}

func (f *fakeCompactor) Compact(ctx context.Context, _ CycleTarget, fence CycleFence) (bool, bool, error) {
	if f.beforeCompact != nil {
		f.beforeCompact()
	}
	if err := fence(ctx); err != nil {
		return false, false, err
	}
	f.record("enter /compact")
	return f.submitted, f.received, f.compactErr
}

// compactFixture is one enabled, launched Codex session in native_compaction (the Codex
// default) at 75 percent, with a synthetic compaction adapter and a controllable clock.
type compactFixture struct {
	*cycleFixture
	compactor *fakeCompactor
}

func newCompactFixture(t *testing.T) *compactFixture {
	t.Helper()
	s, root := testStore(t)
	// The handoff cycle's Probe fails: a compaction needs neither the pickup skill nor the
	// participant address.
	compactor := &fakeCompactor{fakeAdapter: &fakeAdapter{pane: PaneState{Idle: true}, probe: CycleProbe{Reason: "participant_unheld"}},
		compactProbe: CycleProbe{Entry: "/compact"}, submitted: true, received: true}
	f := &compactFixture{cycleFixture: &cycleFixture{s: s, root: root, adapter: compactor.fakeAdapter}, compactor: compactor}
	f.clock = time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return f.clock }
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"codex": compactor}
	t.Cleanup(func() { cycleAdapters = previous })
	f.repo = namedRepo(t, "compact-repo")
	f.k = Key{"codex", "synthetic-thread"}
	f.session = join(t, s, "codex", f.k.ID, f.repo)
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4343,host_start=88,tmux_socket='/tmp/synthetic-socket',tmux_pane='%4' WHERE family='codex' AND id=?`, f.k.ID); err != nil {
		t.Fatal(err)
	}
	f.session = f.reload(t, f.k)
	if _, err := s.SetContextEnable(roomCtx("context-enable"), f.session.Name, f.session.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	return f
}

// read reports used of 1,000 tokens as the Codex rollout would, recorded at at.
func (f *compactFixture) read(t *testing.T, used int64, epoch string, at time.Time) {
	t.Helper()
	f.clock = f.clock.Add(time.Millisecond)
	if err := f.s.Observe(context.Background(), Observation{Caller: f.k, Context: &ObservedValue{Source: "codex_rollout",
		At: at.UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(used), Epoch: epoch}}); err != nil {
		t.Fatal(err)
	}
}

// now reports used tokens recorded at the fixture's clock.
func (f *compactFixture) now(t *testing.T, used int64) {
	t.Helper()
	f.read(t, used, f.k.ID, f.clock.Add(time.Millisecond))
}

func (f *compactFixture) enabled(t *testing.T) bool {
	t.Helper()
	enables, err := f.s.contextEnables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return enables[f.k].Enabled
}

func (f *compactFixture) cycles(t *testing.T) int64 {
	t.Helper()
	return count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE family='codex'`)
}

// A crossing admits one compaction, without a request; the idle pane gets /compact once; a
// reading recorded before the intent does not confirm it, and a fresh one below the threshold
// does. The Enable stays, the audit log has each step, and the next crossing admits another.
func TestCompactionFlow(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 700)
	if f.status(t, f.k) != nil {
		t.Fatal("a compaction below the threshold")
	}
	for range 3 {
		f.now(t, 800)
	}
	v := f.status(t, f.k)
	if v == nil || v.State != cycleCompactWaiting || v.Mode != ContextNativeCompaction || f.cycles(t) != 1 {
		t.Fatalf("admission: %+v", v)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM messages WHERE recipient_family='codex' AND recipient_id=?`, f.k.ID); n != 0 {
		t.Fatalf("a compaction sent %d messages", n)
	}
	held, err := f.s.paneHeldByCycle(context.Background(), f.k)
	if err != nil || held {
		t.Fatalf("a waiting compaction holds the pane: %v %v", held, err)
	}
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("enter") != 1 {
		t.Fatalf("send: %+v %v", v, f.compactor.calls)
	}
	if held, err := f.s.paneHeldByCycle(context.Background(), f.k); err != nil || !held {
		t.Fatalf("a sent compaction does not hold the pane: %v %v", held, err)
	}
	intent, err := compactIntentAt(context.Background(), f.s.db, cycleRow{ID: v.ID})
	if err != nil {
		t.Fatal(err)
	}
	// High readings during the compaction admit nothing; one recorded before the intent, though
	// it arrives now, confirms nothing.
	f.now(t, 900)
	f.read(t, 0, f.k.ID, time.UnixMilli(intent-1))
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent || f.cycles(t) != 1 {
		t.Fatalf("stale reading: %+v", v)
	}
	f.clock = f.clock.Add(time.Second)
	f.now(t, 0)
	if v = f.status(t, f.k); v.State != cycleComplete || v.Reason != "compacted" || !f.enabled(t) {
		t.Fatalf("confirmation: %+v enabled %v", v, f.enabled(t))
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle' AND target LIKE ?`, "cycle "+v.ID+"%"); n != 4 {
		t.Fatalf("audit records: %d", n)
	}
	if f.debt(t) != 0 {
		t.Fatalf("credit left: %d", f.debt(t))
	}
	f.now(t, 800)
	if v = f.status(t, f.k); v.State != cycleCompactWaiting || f.cycles(t) != 2 {
		t.Fatalf("next crossing: %+v", v)
	}
}

// A pane that is not ready, or shows a dialog, gets no input and the compaction waits; it
// sends once the pane is idle. Past the wait it ends with what the pane showed, keeps the
// Enable, and the crossing is not latched: the next reading admits another.
func TestCompactionWaitsForIdle(t *testing.T) {
	f := newCompactFixture(t)
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.now(t, 800)
	f.step(t)
	f.adapter.pane = PaneState{Prompt: "native_dialog"}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactWaiting || f.compactor.count("enter") != 0 {
		t.Fatalf("busy pane: %+v %v", v, f.compactor.calls)
	}
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.step(t)
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	v := f.status(t, f.k)
	if v.State != cycleCancelled || v.Reason != "compact_waiting_timeout.turn_running" || !f.enabled(t) || f.compactor.count("enter") != 0 {
		t.Fatalf("wait timeout: %+v enabled %v", v, f.enabled(t))
	}
	f.now(t, 800)
	if v = f.status(t, f.k); v.State != cycleCompactWaiting || f.cycles(t) != 2 {
		t.Fatalf("after the busy wait: %+v", v)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent {
		t.Fatalf("idle: %+v", v)
	}
}

// Text that was typed and not submitted returns the compaction to waiting, and it is entered
// again when the pane is idle. An Enter without its receipt is uncertain: the Enable ends and
// no later reading enters /compact again, also when the daemon finds the intent after a
// restart.
func TestCompactionSubmissionOutcomes(t *testing.T) {
	f := newCompactFixture(t)
	f.compactor.submitted, f.compactor.received, f.compactor.compactErr = false, false, errCodexTyped
	f.now(t, 800)
	f.step(t)
	v := f.status(t, f.k)
	if v.State != cycleCompactWaiting || v.Reason != errCodexTyped.Error() {
		t.Fatalf("unsubmitted: %+v", v)
	}
	f.compactor.submitted, f.compactor.received, f.compactor.compactErr = true, true, nil
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent {
		t.Fatalf("retry: %+v", v)
	}

	f = newCompactFixture(t)
	f.compactor.submitted, f.compactor.received, f.compactor.compactErr = true, false, errCodexSubmit
	f.now(t, 800)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleUncertain || v.Reason != errCodexSubmit.Error() || f.enabled(t) {
		t.Fatalf("unreceived: %+v enabled %v", v, f.enabled(t))
	}
	f.s.rearm(f.k) // a restart forgets the crossing
	f.now(t, 900)
	f.step(t)
	if f.cycles(t) != 1 || f.compactor.count("enter") != 1 {
		t.Fatalf("entered again: %d cycles, %v", f.cycles(t), f.compactor.calls)
	}

	// The intent is stored before the input: a daemon that stopped after it ends the
	// compaction uncertain and never enters /compact again.
	f = newCompactFixture(t)
	f.now(t, 800)
	v = f.status(t, f.k)
	if _, err := f.s.advanceCycle(context.Background(), v.ID, v.Revision, cycleCompactWaiting, cycleCompactIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	f.s.db.Close()
	restarted, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.db.Close() })
	restarted.now = func() time.Time { return f.clock }
	if err := restarted.cycleStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.s = restarted
	if v = f.status(t, f.k); v.State != cycleUncertain || v.Reason != "compact_unreceipted" || f.compactor.count("enter") != 0 || f.enabled(t) {
		t.Fatalf("restart after the intent: %+v %v", v, f.compactor.calls)
	}
}

// A disable or a mode change during the input stops it before the key; the compaction ends
// uncertain, as every end after an intent does. A threshold change or a reading below the
// threshold before the input ends the compaction without input and keeps the Enable.
func TestCompactionStopsOnChanges(t *testing.T) {
	for _, change := range []string{"disable", "mode"} {
		f := newCompactFixture(t)
		f.compactor.beforeCompact = func() {
			var err error
			if change == "disable" {
				session := f.reload(t, f.k)
				enables, _ := f.s.contextEnables(context.Background())
				_, err = f.s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, enables[f.k].Revision, false)
			} else {
				err = f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextHandoffCycle, 75)
			}
			if err != nil {
				t.Error(err)
			}
		}
		f.now(t, 800)
		f.step(t)
		v := f.status(t, f.k)
		want := map[string]string{"disable": "disabled", "mode": "mode_changed"}[change]
		if v.State != cycleUncertain || v.Reason != want || f.compactor.count("enter") != 0 {
			t.Fatalf("%s during the input: %+v %v", change, v, f.compactor.calls)
		}
	}

	f := newCompactFixture(t)
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.now(t, 800)
	f.step(t)
	session := f.reload(t, f.k)
	if _, err := f.s.SetContextThreshold(roomCtx("context-threshold"), session.Name, 85); err != nil {
		t.Fatal(err)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "threshold_changed" || !f.enabled(t) || f.compactor.count("enter") != 0 {
		t.Fatalf("threshold change: %+v %v", v, f.compactor.calls)
	}
	f.now(t, 900)
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.step(t)
	f.now(t, 100) // Codex compacted on its own
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "below_threshold" || !f.enabled(t) || f.compactor.count("enter") != 0 {
		t.Fatalf("below the threshold: %+v %v", v, f.compactor.calls)
	}
}

// Another epoch ends the compaction as epoch_changed and keeps the Enable; a compaction that
// is not confirmed within 10 minutes fails unconfirmed and ends the Enable.
func TestCompactionEpochAndTimeout(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.step(t)
	first := f.status(t, f.k)
	// The new epoch's reading is at the threshold too: it admits the next compaction at once.
	f.read(t, 900, "synthetic-thread-2", f.clock.Add(time.Millisecond))
	ended, err := cyclesWhere(context.Background(), f.s.db, `cycle_id=?`, first.ID)
	if err != nil || ended[0].State != cycleCancelled || ended[0].Reason != "epoch_changed" || !f.enabled(t) {
		t.Fatalf("epoch change: %v %+v enabled %v", err, ended, f.enabled(t))
	}
	if v := f.status(t, f.k); v.State != cycleCompactWaiting || v.ID == first.ID {
		t.Fatalf("new epoch: %+v", v)
	}
	f = newCompactFixture(t)
	f.now(t, 800)
	f.step(t)
	f.clock = f.clock.Add(cycleCompactConfirm)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "compact_unconfirmed" || f.enabled(t) {
		t.Fatalf("unconfirmed: %+v enabled %v", v, f.enabled(t))
	}
}

// In native_compaction the Enable and the Sessions page use the compaction's checks, not the
// handoff cycle's; a failing compaction probe refuses the Enable.
func TestCompactionProbe(t *testing.T) {
	f := newCompactFixture(t)
	if !f.enabled(t) || f.compactor.count("compact probe") == 0 || f.adapter.count("probe") != 0 {
		t.Fatalf("enable used the handoff probe: %v", f.compactor.calls)
	}
	row := f.s.contextRowOf(f.reload(t, f.k), ContextEnable{Enabled: true}, f.clock.UnixMilli())
	if !row.Available || row.Pickup != "/compact" || row.Mode != ContextNativeCompaction {
		t.Fatalf("sessions row: %+v", row)
	}
	f.compactor.compactProbe = CycleProbe{Reason: "pane_unproven"}
	session := f.reload(t, f.k)
	enables, _ := f.s.contextEnables(context.Background())
	if _, err := f.s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, enables[f.k].Revision, false); err != nil {
		t.Fatal(err)
	}
	enables, _ = f.s.contextEnables(context.Background())
	if _, err := f.s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, enables[f.k].Revision, true); !errors.Is(err, ErrContextCycleUnavailable) {
		t.Fatalf("enable with a failing compaction probe: %v", err)
	}
}

// The Sessions page names a compaction as such: its checks, its Enable button and the newest
// compaction with its stages and result.
func TestCompactionSessionsPage(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	compactor := &fakeCompactor{fakeAdapter: &fakeAdapter{pane: PaneState{Idle: true}, probe: CycleProbe{Reason: "participant_unheld"}},
		compactProbe: CycleProbe{Entry: "/compact"}, submitted: true, received: true}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"codex": compactor}
	t.Cleanup(func() { cycleAdapters = previous })
	k := Key{"codex", "synthetic-thread"}
	join(t, s, "codex", k.ID, "")
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4343,host_start=88,tmux_socket='/tmp/synthetic-socket',tmux_pane='%4' WHERE family='codex' AND id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	session, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(context.Background(), Observation{Caller: k, Context: &ObservedValue{Source: "codex_rollout",
		At: s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(800), Epoch: k.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.cycleStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := s.ContextCycleStatus(context.Background(), k)
	if err != nil || v == nil || v.State != cycleCompactSent {
		t.Fatalf("compaction: %+v %v", v, err)
	}
	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	for _, want := range []string{"compaction native_compaction at 75%, enabled; compaction through /compact",
		"compaction " + v.ID + " (requester): compact_sent", " compact_intent entered", " compact_sent entered", ">Disable compaction</button>"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("sessions page lacks %q: %s", want, page.body)
		}
	}
}

// A schema 33 store keeps every cycle row through the table rebuild; the rebuilt table takes
// the compaction states, and a schema 33 writer refuses the result (#509).
func TestSchemaThirtyFourKeepsCycles(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	rows := rowsOf(t, f.s.db)
	before := count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE ready_refusal!=''`)
	if _, err := fixtureExec(f.s, undoSchemaThirtyFour+`PRAGMA user_version=33`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(f.s.db, 33); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(f.s, `UPDATE context_cycles SET state='compact_waiting'`); err == nil {
		t.Fatal("a schema 33 table took a compaction state")
	}
	f.s.db.Close()
	upgraded, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != rows ||
		count(t, upgraded, `SELECT COUNT(*) FROM context_cycles WHERE ready_refusal!=''`) != before {
		t.Fatal("schema 34 changed retained state")
	}
	if err := checkCatalog(upgraded.db, 33); err == nil {
		t.Fatal("a schema 33 writer would accept the schema 34 store")
	}
	if _, err := fixtureExec(upgraded, `UPDATE context_cycles SET state='compact_waiting'`); err != nil {
		t.Fatal(err)
	}
}
