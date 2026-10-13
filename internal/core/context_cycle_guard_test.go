package core

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Regression tests of the cycle's guards (#196, chunk 02 review): pinned reporters,
// deadlines, recorded prompt input, artifact rechecks, complete retries, the reserved
// overwatch name, per-stage audit and the listed checkout.

// readyCycle admits a cycle for f.k and reports its handoff ready.
func (f *cycleFixture) readyCycle(t *testing.T) (CycleView, string) {
	t.Helper()
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic handoff")
	ready, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name)
	if err != nil {
		t.Fatal(err)
	}
	return ready, name
}

func TestContextCycleReportsRecheckPins(t *testing.T) {
	// A changed host or a lost participant refuses ready and changes nothing.
	for _, change := range []string{`UPDATE sessions SET host_pid=5151,host_start=88 WHERE family='claude' AND id=?`,
		`UPDATE names SET holder_id='synthetic-other' WHERE kind='alias' AND holder_id=?`} {
		f := newCycleFixture(t)
		f.observe(t, f.k, 900, "synthetic-epoch-1")
		v := f.status(t, f.k)
		name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
		if _, err := f.s.db.Exec(change, f.k.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name); !errors.Is(err, ErrCycleChanged) {
			t.Fatalf("%s: ready %v", change, err)
		}
		if got := f.status(t, f.k); got.State != cycleWaitingReady || got.Revision != v.Revision {
			t.Fatalf("%s: %+v", change, got)
		}
	}
	// The retained caller in another pane cannot complete; in its pinned pane it can.
	f := newCycleFixture(t)
	_, name := f.readyCycle(t)
	f.step(t)
	f.step(t)
	f.step(t)
	v := f.status(t, f.k)
	f.s.db.Exec(`UPDATE sessions SET tmux_pane='%9' WHERE family='claude' AND id=?`, f.k.ID)
	if _, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("other pane: %v", err)
	}
	f.s.db.Exec(`UPDATE sessions SET tmux_pane='%3' WHERE family='claude' AND id=?`, f.k.ID)
	if done, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic"); err != nil || done.State != cycleComplete {
		t.Fatalf("pinned pane: %v %+v", err, done)
	}
}

func TestContextCycleDeadlinesHold(t *testing.T) {
	// A ready report at its deadline is refused; the cycle times out and enters no clear.
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	f.clock = time.UnixMilli(v.StageDeadline)
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("ready at the deadline: %v", err)
	}
	f.step(t)
	if got := f.status(t, f.k); got.State != cycleFailed || got.Reason != "waiting_ready_timeout" || f.adapter.count("clear") != 0 {
		t.Fatalf("after the deadline: %+v %v", got, f.adapter.calls)
	}
	// One millisecond before it, ready is accepted with the receipt wait.
	g := newCycleFixture(t)
	g.observe(t, g.k, 900, "synthetic-epoch-1")
	v = g.status(t, g.k)
	name = g.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	g.clock = time.UnixMilli(v.StageDeadline - 1)
	ready, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", v.ID, v.Revision, name)
	if err != nil || ready.StageDeadline != g.clock.UnixMilli()+cycleReceiptWait.Milliseconds() {
		t.Fatalf("ready before the deadline: %v %+v", err, ready)
	}
	// Pickup has the two-minute receipt wait, and no stage outlasts the cycle.
	g.step(t)
	g.step(t)
	g.step(t)
	if v = g.status(t, g.k); v.State != cycleWaitingPickup || v.StageDeadline != min(v.UpdatedAt+cycleReceiptWait.Milliseconds(), v.CycleDeadline) {
		t.Fatalf("waiting_pickup deadline: %+v", v)
	}
	// A prompt late in the cycle (after earlier prompt rounds) waits only until its end.
	h := newCycleFixture(t)
	ready, _ = h.readyCycle(t)
	h.s.db.Exec(`UPDATE context_cycles SET stage_deadline=cycle_deadline WHERE cycle_id=?`, ready.ID)
	h.clock = time.UnixMilli(ready.CycleDeadline - 1000)
	h.adapter.pane = PaneState{Prompt: "synthetic_prompt"}
	h.step(t)
	if v = h.status(t, h.k); v.State != cycleWaitingPrompt || v.StageDeadline != v.CycleDeadline {
		t.Fatalf("prompt deadline beyond the cycle: %+v", v)
	}
	// An unreceipted intent or a clear without a proven reset at the cycle's deadline is
	// uncertain, not failed.
	for _, state := range []string{cycleClearIntent, cycleClearReceived} {
		k := newCycleFixture(t)
		ready, _ := k.readyCycle(t)
		if _, err := k.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
			t.Fatal(err)
		}
		if state == cycleClearReceived {
			if _, err := k.s.advanceCycle(context.Background(), ready.ID, ready.Revision+1, cycleClearIntent, cycleClearReceived, "", nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		k.clock = time.UnixMilli(ready.CycleDeadline)
		k.step(t)
		if v := k.status(t, k.k); v.State != cycleUncertain || v.Reason != "cycle_timeout" {
			t.Fatalf("%s at the cycle deadline: %+v", state, v)
		}
	}
}

func TestContextCyclePromptInputRecorded(t *testing.T) {
	// The answer follows its recorded intent; each stage has its audit record.
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.adapter.pane = PaneState{Prompt: "synthetic_prompt"}
	f.step(t)
	f.step(t)
	v := f.status(t, f.k)
	stages := []string{}
	for _, e := range v.Events {
		stages = append(stages, e.Stage)
	}
	if v.State != cycleReady || f.adapter.count("answer") != 1 || len(stages) != 5 || stages[3] != cyclePromptIntent {
		t.Fatalf("answered: %+v %v", v, stages)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle'`); n != int64(len(v.Events)) {
		t.Fatalf("%d events, %d audit records", len(v.Events), n)
	}
	// A prompt intent left by a crash is never answered again: the cycle is uncertain.
	g := newCycleFixture(t)
	g.readyCycle(t)
	g.adapter.pane = PaneState{Prompt: "synthetic_prompt"}
	g.step(t)
	v = g.status(t, g.k)
	if _, err := g.s.advanceCycle(context.Background(), v.ID, v.Revision, cycleWaitingPrompt, cyclePromptIntent, v.Reason, nil, nil); err != nil {
		t.Fatal(err)
	}
	if held, _ := g.s.paneHeldByCycle(context.Background(), g.k); !held {
		t.Fatal("a prompt intent does not hold the pane")
	}
	g.step(t)
	if v = g.status(t, g.k); v.State != cycleUncertain || v.Reason != "prompt_unreceipted" || g.adapter.count("answer") != 0 {
		t.Fatalf("prompt intent recovery: %+v %v", v, g.adapter.calls)
	}
}

func TestContextCycleArtifactRecheckedBeforeInput(t *testing.T) {
	// A removed handoff returns the cycle to readiness before the clear; a new report
	// with a new file proceeds.
	f := newCycleFixture(t)
	ready, name := f.readyCycle(t)
	path := filepath.Join(f.repo, "_handoff", "claude", name)
	os.Remove(path)
	f.step(t)
	v := f.status(t, f.k)
	if v.State != cycleWaitingReady || v.Reason != "artifact_changed" || v.Artifact != "" || f.adapter.count("clear") != 0 ||
		v.StageDeadline != v.CreatedAt+cycleReadyWait.Milliseconds() {
		t.Fatalf("removed handoff: %+v %v", v, f.adapter.calls)
	}
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", ready.ID, ready.Revision, name); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("old ready receipt: %v", err)
	}
	name = f.handoff(t, "claude", "2026-10-13-120001.md", "synthetic rewritten handoff")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleClearReceived {
		t.Fatalf("after a new ready: %+v", v)
	}
	// A replacement under a new inode (the original kept elsewhere, so the inode cannot be
	// reused, with content of the same size), a replacement that may reuse the inode, and
	// a rewrite in place under the same inode all fail the identical ready retry.
	for _, replace := range []func(t *testing.T, path string){
		func(t *testing.T, path string) {
			before, _ := os.Stat(path)
			os.Rename(path, path+".kept")
			os.WriteFile(path, []byte("synthetic HANDOFF"), 0o600)
			after, _ := os.Stat(path)
			if before.Size() != after.Size() || os.SameFile(before, after) {
				t.Fatal("the fixture did not replace the inode at the same size")
			}
		},
		func(t *testing.T, path string) {
			os.Remove(path)
			os.WriteFile(path, []byte("synthetic replacement"), 0o600)
		},
		func(t *testing.T, path string) { os.WriteFile(path, []byte("synthetic rewritten in place"), 0o600) },
	} {
		g := newCycleFixture(t)
		ready, name := g.readyCycle(t)
		replace(t, filepath.Join(g.repo, "_handoff", "claude", name))
		if _, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", ready.ID, ready.Revision-1, name); !errors.Is(err, ErrCycleArtifact) {
			t.Fatalf("retry over a changed file: %v", err)
		}
	}
	// After the clear, a changed handoff fails the cycle before the pickup input.
	h := newCycleFixture(t)
	_, name = h.readyCycle(t)
	h.step(t)
	h.step(t)
	os.Remove(filepath.Join(h.repo, "_handoff", "claude", name))
	h.step(t)
	if v = h.status(t, h.k); v.State != cycleFailed || v.Reason != "artifact_changed" || h.adapter.count("pickup") != 0 {
		t.Fatalf("handoff removed after the clear: %+v %v", v, h.adapter.calls)
	}
}

func TestContextCyclePickupRetryMustMatch(t *testing.T) {
	f := newCycleFixture(t)
	_, name := f.readyCycle(t)
	f.step(t)
	f.step(t)
	f.step(t)
	v := f.status(t, f.k)
	pickup := func(outcome, report string) error {
		_, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, outcome, report)
		return err
	}
	if err := pickup("resumed", "synthetic report"); err != nil {
		t.Fatal(err)
	}
	if err := pickup("resumed", "synthetic report"); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	for _, retry := range [][2]string{{"paused", "synthetic report"}, {"resumed", "synthetic other report"}} {
		if err := pickup(retry[0], retry[1]); !errors.Is(err, ErrCycleChanged) {
			t.Fatalf("conflicting retry %v: %v", retry, err)
		}
	}
}

func TestContextCycleOverwatchNameConflict(t *testing.T) {
	f := newCycleFixture(t)
	other := join(t, f.s, "claude", "synthetic-legacy-overwatch", f.repo)
	if _, err := f.s.db.Exec(`UPDATE names SET name='overwatch' WHERE kind='peer' AND family='claude' AND session_id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if n := count(t, f.s, `SELECT COUNT(*) FROM context_cycles`); n != 0 {
		t.Fatalf("admitted %d cycles", n)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM names WHERE name='overwatch' AND session_id='synthetic-legacy-overwatch'`); n != 1 {
		t.Fatal("the retained record changed")
	}
	enable, _ := f.s.contextEnables(context.Background())
	if row := f.s.contextRowOf(f.reload(t, f.k), enable[f.k], f.s.now().UnixMilli()); row.Reason != "overwatch_name_conflict" {
		t.Fatalf("trigger reason: %q", row.Reason)
	}
}

func TestContextCycleListedCheckoutOnly(t *testing.T) {
	f := newCycleFixture(t)
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", f.repo, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid",
			"-c", "commit.gpgsign=false"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("commit", "--allow-empty", "-m", "synthetic")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--detach", linked)
	registered, err := CheckoutResource(context.Background(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	main, _ := filepath.EvalSymlinks(f.repo)
	if got, err := mainCheckout(context.Background(), linked, registered.Repository); err != nil || got != main {
		t.Fatalf("listed linked worktree: %q %v", got, err)
	}
	pointer, _ := os.ReadFile(filepath.Join(linked, ".git"))
	unlisted := t.TempDir()
	os.WriteFile(filepath.Join(unlisted, ".git"), pointer, 0o600)
	if got, err := mainCheckout(context.Background(), unlisted, registered.Repository); err == nil {
		t.Fatalf("copied pointer resolved %q", got)
	}
}

// Without a loader at launch, the cycle adds it before the clear behind its own intent; an
// unproven bootstrap is uncertain and never repeated, and the pickup expects the loader.
func TestContextCycleInteractiveBootstrap(t *testing.T) {
	f := newCycleFixture(t)
	f.adapter.probe, f.adapter.boot = CycleProbe{Bootstrap: "interactive"}, true
	f.readyCycle(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleBootstrapVerified || f.adapter.count("bootstrap") != 1 || f.adapter.count("clear") != 0 {
		t.Fatalf("bootstrap: %+v %v", v, f.adapter.calls)
	}
	f.step(t)
	f.step(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingPickup || f.adapter.count("pickup interactive") != 1 || f.adapter.count("bootstrap") != 1 {
		t.Fatalf("after bootstrap: %+v %v", v, f.adapter.calls)
	}
	g := newCycleFixture(t)
	g.adapter.probe = CycleProbe{Bootstrap: "interactive"}
	g.readyCycle(t)
	g.step(t)
	if v := g.status(t, g.k); v.State != cycleUncertain || v.Reason != "bootstrap_intent_unreceived" || g.adapter.count("clear") != 0 {
		t.Fatalf("unproven bootstrap: %+v", v)
	}
	h := newCycleFixture(t)
	h.adapter.probe = CycleProbe{Bootstrap: "interactive"}
	ready, _ := h.readyCycle(t)
	if _, err := h.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	if v := h.status(t, h.k); v.State != cycleUncertain || v.Reason != "bootstrap_unreceipted" || h.adapter.count("bootstrap") != 0 {
		t.Fatalf("bootstrap intent after a crash: %+v %v", v, h.adapter.calls)
	}
}

// A pickup entry that Probe cannot verify fails the cycle before any input.
func TestContextCycleProbeBeforeClear(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.adapter.probe = CycleProbe{Reason: "pickup_entry_differs", Bootstrap: "project"}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "pickup_entry_differs" || f.adapter.count("clear") != 0 {
		t.Fatalf("probe before the clear: %+v %v", v, f.adapter.calls)
	}
}

// A dialog the adapter does not answer waits for the native session or the maintainer; the
// cycle returns to its stage when the dialog is gone, without an answer of its own.
func TestContextCycleNativeDialogWaits(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.adapter.pane = PaneState{Prompt: "native_dialog"}
	f.step(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingPrompt || v.Reason != "native_dialog" || f.adapter.count("answer") != 0 {
		t.Fatalf("waiting: %+v %v", v, f.adapter.calls)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleReady || f.adapter.count("answer") != 0 || f.adapter.count("clear") != 0 {
		t.Fatalf("resolved: %+v %v", v, f.adapter.calls)
	}
}

// A Disable that lands between the clear intent and its first key fences the input: no key
// is sent, and the recorded intent makes the cycle uncertain.
func TestContextCycleFenceStopsInput(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.adapter.beforeClear = func() {
		s := f.reload(t, f.k)
		e, _ := f.s.contextEnables(context.Background())
		if _, err := f.s.SetContextEnable(roomCtx("context-enable"), s.Name, s.Revision, e[f.k].Revision, false); err != nil {
			t.Fatal(err)
		}
	}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleUncertain || f.adapter.count("clear") != 0 {
		t.Fatalf("fenced clear: %+v %v", v, f.adapter.calls)
	}
}

// A dialog the adapter does not answer shows on the Sessions page; the maintainer's option is
// applied behind its recorded intent and audit record, only at the shown revision and dialog.
func TestContextCycleMaintainerAnswersDialog(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	adapter := &fakeAdapter{pane: PaneState{Prompt: "native_dialog"}, probe: CycleProbe{Bootstrap: "project"},
		dialog: CycleDialog{Lines: []string{"Do you want to proceed?", "❯ 1. Yes", "2. No"}, Options: []string{"1", "2"}, Digest: strings.Repeat("d", 64)}}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	repo := namedRepo(t, "cycle-dialog")
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("_handoff/\n"), 0o644)
	a := join(t, s, "claude", "synthetic-dialog-a", repo)
	s.db.Exec(`UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, a.ID)
	a, _ = scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, "claude", a.ID), s.now().UnixMilli())
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	k := Key{"claude", a.ID}
	if err := s.Observe(context.Background(), Observation{Caller: k, Context: &ObservedValue{Source: "claude_statusline",
		At: s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(950), Epoch: "synthetic-epoch-1"}}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.ContextCycleStatus(context.Background(), k)
	dir := filepath.Join(repo, "_handoff", "claude")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "2026-10-13-120000.md"), []byte("synthetic"), 0o600)
	if _, err := s.ContextCycleReady(context.Background(), k, "synthetic-epoch-1", v.ID, v.Revision, "2026-10-13-120000.md"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.cycleStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	v, _ = s.ContextCycleStatus(context.Background(), k)
	if v.State != cycleWaitingPrompt || adapter.count("choose") != 0 {
		t.Fatalf("waiting: %+v", v)
	}
	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if !strings.Contains(page.body, "Do you want to proceed?") || !strings.Contains(page.body, `name="digest" value="`+adapter.dialog.Digest+`"`) ||
		!strings.Contains(page.body, "Choose 2") {
		t.Fatalf("sessions page: %s", page.body)
	}
	answer := func(revision int64, digest, option string) string {
		return client.do("context-cycle-answer", url.Values{"cycle_id": {v.ID}, "revision": {strconv.FormatInt(revision, 10)}, "digest": {digest}, "option": {option}})
	}
	if got := answer(v.Revision+1, adapter.dialog.Digest, "1"); got != "cycle_changed" {
		t.Fatalf("stale revision: %s", got)
	}
	if got := answer(v.Revision, strings.Repeat("e", 64), "1"); got != "cycle_dialog_changed" || adapter.count("choose") != 0 {
		t.Fatalf("other dialog: %s", got)
	}
	if got := answer(v.Revision, adapter.dialog.Digest, "2"); got != "cycle_dialog_answered" || adapter.count("choose 2") != 1 {
		t.Fatalf("answer: %s %v", got, adapter.calls)
	}
	v, _ = s.ContextCycleStatus(context.Background(), k)
	if v.State != cycleReady || count(t, s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle-answer' AND result='accepted'`) != 1 ||
		count(t, s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle-answer' AND result='refused'`) != 2 {
		t.Fatalf("after the answer: %+v audit %d", v, count(t, s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle-answer'`))
	}
}

// A crash after the clear's Enter and before its receipt commit leaves clear_intent: on the
// next step the proven new epoch reconciles it to reset_verified, and no second clear is sent.
// Without the new epoch the same state is uncertain (TestContextCycleDeadlinesAndIntentRecovery).
func TestContextCycleClearIntentReconciledByEpoch(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	if _, err := f.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleResetVerified || f.adapter.count("clear") != 0 {
		t.Fatalf("reconciled clear: %+v %v", v, f.adapter.calls)
	}
	f.step(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingPickup || f.adapter.count("clear") != 0 || f.adapter.count("pickup") != 1 {
		t.Fatalf("after reconciliation: %+v %v", v, f.adapter.calls)
	}
}

// A new native epoch before the managed clear is an unrelated clear in the bootstrap stages
// too: after a proven bootstrap it cancels the cycle and ends the Enable; with the bootstrap
// input unproven it is uncertain. No managed clear follows.
func TestContextCycleUnrelatedClearDuringBootstrap(t *testing.T) {
	f := newCycleFixture(t)
	f.adapter.probe, f.adapter.boot = CycleProbe{Bootstrap: "interactive"}, true
	f.readyCycle(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleBootstrapVerified {
		t.Fatalf("setup: %+v", v)
	}
	f.observe(t, f.k, 20, "synthetic-unrelated-clear")
	f.step(t)
	enables, _ := f.s.contextEnables(context.Background())
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "epoch_changed" || enables[f.k].Enabled || f.adapter.count("clear") != 0 {
		t.Fatalf("after bootstrap: %+v Enable %v clears %d", v, enables[f.k].Enabled, f.adapter.count("clear"))
	}
	g := newCycleFixture(t)
	g.adapter.probe = CycleProbe{Bootstrap: "interactive"}
	ready, _ := g.readyCycle(t)
	if _, err := g.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	g.observe(t, g.k, 20, "synthetic-unrelated-clear")
	if v := g.status(t, g.k); v.State != cycleUncertain || v.Reason != "epoch_changed" || g.adapter.count("clear") != 0 {
		t.Fatalf("during the bootstrap intent: %+v", v)
	}
}
