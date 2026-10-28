package core

import (
	"context"
	"errors"
	"fmt"
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
		if _, err := fixtureExec(f.s, change, f.k.ID); err != nil {
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
	fixtureExec(f.s, `UPDATE sessions SET tmux_pane='%9' WHERE family='claude' AND id=?`, f.k.ID)
	if _, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("other pane: %v", err)
	}
	fixtureExec(f.s, `UPDATE sessions SET tmux_pane='%3' WHERE family='claude' AND id=?`, f.k.ID)
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
	if got := f.status(t, f.k); got.State != cycleFailed || got.Reason != "waiting_ready_timeout.unread" || f.adapter.count("clear") != 0 {
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
	// Pickup has the five-minute pickup wait (#525), and no stage outlasts the cycle.
	g.step(t)
	g.step(t)
	g.step(t)
	if v = g.status(t, g.k); v.State != cycleWaitingPickup || v.StageDeadline != min(v.UpdatedAt+cyclePickupWait.Milliseconds(), v.CycleDeadline) {
		t.Fatalf("waiting_pickup deadline: %+v", v)
	}
	// A prompt late in the cycle (after earlier prompt rounds) waits only until its end.
	h := newCycleFixture(t)
	ready, _ = h.readyCycle(t)
	fixtureExec(h.s, `UPDATE context_cycles SET stage_deadline=cycle_deadline WHERE cycle_id=?`, ready.ID)
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
		if _, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", ready.ID, ready.Revision-1, name); !artifactRefused(err, "") {
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
	if _, err := fixtureExec(f.s, `UPDATE names SET name='overwatch' WHERE kind='peer' AND family='claude' AND session_id=?`, other.ID); err != nil {
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

// No cycle enters a bootstrap stage since #406: a ready cycle goes straight to its clear, and
// one that an earlier binary left in bootstrap_verified goes on to its clear, while one left in
// bootstrap_intent ends uncertain without any input.
func TestContextCycleStoredBootstrapStages(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleClearReceived || f.adapter.count("clear") != 1 {
		t.Fatalf("ready: %+v %v", v, f.adapter.calls)
	}
	g := newCycleFixture(t)
	ready, _ := g.readyCycle(t)
	intent, err := g.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.advanceCycle(context.Background(), intent.ID, intent.Revision, cycleBootstrapIntent, cycleBootstrapVerified, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	g.step(t)
	g.step(t)
	g.step(t)
	if v := g.status(t, g.k); v.State != cycleWaitingPickup || g.adapter.count("clear") != 1 || g.adapter.count("pickup") != 1 {
		t.Fatalf("stored bootstrap_verified: %+v %v", v, g.adapter.calls)
	}
	h := newCycleFixture(t)
	ready, _ = h.readyCycle(t)
	if _, err := h.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	if v := h.status(t, h.k); v.State != cycleUncertain || v.Reason != "bootstrap_unreceipted" || h.adapter.count("clear") != 0 || h.adapter.count("pickup") != 0 {
		t.Fatalf("stored bootstrap_intent: %+v %v", v, h.adapter.calls)
	}
}

// A pickup entry that Probe cannot verify fails the cycle before any input.
func TestContextCycleProbeBeforeClear(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	f.adapter.probe = CycleProbe{Reason: "pickup_entry_differs", Step: "run koinon install"}
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
	adapter := &fakeAdapter{pane: PaneState{Prompt: "native_dialog"}, probe: CycleProbe{Entry: "koinon-pickup"},
		dialog: CycleDialog{Lines: []string{"Do you want to proceed?", "❯ 1. Yes", "2. No"}, Options: []string{"1", "2"}, Digest: strings.Repeat("d", 64)}}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	repo := namedRepo(t, "cycle-dialog")
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("_handoff/\n"), 0o644)
	a := join(t, s, "claude", "synthetic-dialog-a", repo)
	fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, a.ID)
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
// that an earlier binary stored too: after a proven bootstrap it cancels the cycle and ends the
// Enable; with the bootstrap input unproven it is uncertain. No managed clear follows.
func TestContextCycleUnrelatedClearDuringBootstrap(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	intent, err := f.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.advanceCycle(context.Background(), intent.ID, intent.Revision, cycleBootstrapIntent, cycleBootstrapVerified, "", nil, nil); err != nil {
		t.Fatal(err)
	}
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
	ready, _ = g.readyCycle(t)
	if _, err := g.s.advanceCycle(context.Background(), ready.ID, ready.Revision, cycleReady, cycleBootstrapIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	g.observe(t, g.k, 20, "synthetic-unrelated-clear")
	if v := g.status(t, g.k); v.State != cycleUncertain || v.Reason != "epoch_changed" || g.adapter.count("clear") != 0 {
		t.Fatalf("during the bootstrap intent: %+v", v)
	}
}

// After the ready report, a peer's message does not reach the session: its wake waits while
// the session finishes its turn, and the cycle clears once the pane is at its prompt (#417).
// A stage that times out names what the pane last showed.
func TestContextCycleReadyHoldsWakesAndNamesWhatItSaw(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	sent := 0
	f.s.wake.send = func(context.Context, Session, string) wakeResult { sent++; return accepted() }
	peer := join(t, f.s, "codex", "synthetic-peer", f.repo)
	if _, err := f.s.Send(context.Background(), Key{"codex", peer.ID}, f.session.Name, "synthetic peer notice"); err != nil {
		t.Fatal(err)
	}
	f.adapter.pane = PaneState{Seen: "status_busy"}
	f.step(t)
	if err := f.s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := f.status(t, f.k); v.State != cycleReady || sent != 0 || f.adapter.count("clear") != 0 {
		t.Fatalf("turn after ready: %+v sent=%d %v", v, sent, f.adapter.calls)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if err := f.s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := f.status(t, f.k); v.State != cycleClearReceived || sent != 0 || f.adapter.count("clear") != 1 {
		t.Fatalf("at the prompt: %+v sent=%d %v", v, sent, f.adapter.calls)
	}

	for _, test := range []struct {
		pane    PaneState
		inspect error
		want    string
	}{
		// A pane that was read and is not idle gets the request again (#431).
		{PaneState{Seen: "status_busy"}, nil, "not_idle.status_busy"},
		{PaneState{Seen: "input_not_empty"}, nil, "not_idle.input_not_empty"},
		{PaneState{Seen: "status_unknown"}, nil, "not_idle.status_unknown"},
		{PaneState{Seen: "input_line_not_found"}, nil, "not_idle.input_line_not_found"},
		{PaneState{}, nil, "not_idle.pane_not_ready"},
		// The Codex adapter's running turn is asked again; its unreadable home or rollout is not.
		{PaneState{Seen: "turn_running"}, nil, "not_idle.turn_running"},
		{PaneState{Seen: "turn_not_ended"}, nil, "not_idle.turn_not_ended"},
		{PaneState{Seen: "home_unknown"}, nil, "ready_timeout.home_unknown"},
		{PaneState{Seen: "rollout_unreadable"}, nil, "ready_timeout.rollout_unreadable"},
		{PaneState{}, errClaudeUnproven, "ready_timeout.pane_unproven"},
		// A tmux that cannot run is named apart from an unproven pane (#418).
		{PaneState{}, tmuxUnavailableError{exec.ErrNotFound}, "ready_timeout.tmux_unavailable"},
		{PaneState{}, errors.New("synthetic tmux failure"), "ready_timeout.pane_unreadable"},
	} {
		g := newCycleFixture(t)
		ready, _ := g.readyCycle(t)
		g.adapter.pane, g.adapter.inspect = test.pane, test.inspect
		g.step(t)
		g.clock = time.UnixMilli(ready.StageDeadline)
		g.step(t)
		state := cycleFailed
		if strings.HasPrefix(test.want, "not_idle.") {
			state = cycleWaitingReady
		}
		if v := g.status(t, g.k); v.State != state || v.Reason != test.want || g.adapter.count("clear") != 0 {
			t.Fatalf("%s: %+v", test.want, v)
		}
	}
	// waiting_ready does not wait for the pane: its reason says whether the session read the
	// request (#445).
	for _, read := range []bool{false, true} {
		g := newCycleFixture(t)
		g.observe(t, g.k, 900, "synthetic-epoch-1")
		v := g.status(t, g.k)
		want := "waiting_ready_timeout.unread"
		if read {
			if _, err := g.s.ReadInbox(context.Background(), g.k, 0, 10); err != nil {
				t.Fatal(err)
			}
			want = "waiting_ready_timeout.read"
		}
		g.clock = time.UnixMilli(v.StageDeadline)
		g.step(t)
		if v = g.status(t, g.k); v.Reason != want {
			t.Fatalf("waiting_ready, read %v: %+v", read, v)
		}
	}
}

// A wake that passed the pane hold before the ready report ends before ready commits: the
// report waits for the wake lock, so no notice is accepted during ready (#417 review).
func TestContextCycleReadyWaitsForAWakeInFlight(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	requested := f.status(t, f.k)
	artifact := f.handoff(t, "claude", "2026-10-20-010000.md", "synthetic handoff")
	peer := join(t, f.s, "codex", "synthetic-peer", f.repo)
	if _, err := f.s.Send(context.Background(), Key{"codex", peer.ID}, f.session.Name, "synthetic peer notice"); err != nil {
		t.Fatal(err)
	}
	sending, release := make(chan struct{}), make(chan struct{})
	wakeDone, readyDone := make(chan error, 1), make(chan error, 1)
	f.s.wake.send = func(context.Context, Session, string) wakeResult {
		close(sending)
		<-release
		return accepted()
	}
	go func() { wakeDone <- f.s.wakeStep(context.Background()) }()
	<-sending
	go func() {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", requested.ID, requested.Revision, artifact)
		readyDone <- err
	}()
	select {
	case err := <-readyDone:
		close(release)
		t.Fatalf("ready returned while a wake was being submitted: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-wakeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	if v := f.status(t, f.k); v.State != cycleReady {
		t.Fatalf("after the wake: %+v", v)
	}
}

// After a restart no inspection has run: the timeout reads the pane once, sending no key,
// and names what it saw (#417 review).
func TestContextCycleTimeoutAfterRestartNamesThePane(t *testing.T) {
	for _, test := range []struct {
		inMode  bool
		inspect error
		want    string
	}{{false, nil, "not_idle.status_busy"}, {true, nil, "ready_timeout.pane_in_mode"},
		// An inspection that fails after the restart keeps the timeout (#431).
		{false, errClaudeUnproven, "ready_timeout.pane_unproven"},
		{false, tmuxUnavailableError{exec.ErrNotFound}, "ready_timeout.tmux_unavailable"},
		{false, errors.New("synthetic tmux failure"), "ready_timeout.pane_unreadable"}} {
		f := newCycleFixture(t)
		ready, _ := f.readyCycle(t)
		f.adapter.pane = PaneState{Seen: "status_busy"}
		f.step(t)
		f.s.db.Close()
		reopened, err := openStore(f.root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reopened.Close() })
		reopened.now = f.s.now
		f.s = reopened
		f.adapter.inMode, f.adapter.inspect = test.inMode, test.inspect
		f.clock = time.UnixMilli(ready.StageDeadline)
		f.step(t)
		var audit string
		if err := f.s.db.QueryRow(`SELECT reason FROM audit WHERE action='context-cycle' ORDER BY id DESC LIMIT 1`).Scan(&audit); err != nil {
			t.Fatal(err)
		}
		state := cycleFailed
		if strings.HasPrefix(test.want, "not_idle.") {
			state = cycleWaitingReady
		}
		if v := f.status(t, f.k); v.State != state || v.Reason != test.want || audit != test.want || f.adapter.count("cancel") != 0 {
			t.Fatalf("%s: %+v audit=%q %v", test.want, v, audit, f.adapter.calls)
		}
	}
}

// koinon context show gives the newest cycle's reason (#417 review).
func TestContextShowGivesTheCycleReason(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.inspect = errClaudeUnproven
	f.step(t)
	f.clock = time.UnixMilli(ready.StageDeadline)
	f.step(t)
	f.status(t, f.k)
	_, sessions, _, err := f.s.ContextShow(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range sessions {
		if v.Peer == f.session.Name {
			if v.CycleState != cycleFailed || v.CycleReason != "ready_timeout.pane_unproven" {
				t.Fatalf("context show: %+v", v)
			}
			return
		}
	}
	t.Fatalf("session not shown: %+v", sessions)
}

// overwatchRequests returns the bodies of the overwatch requests in the requester's inbox,
// oldest first, with their cycle IDs.
func (f *cycleFixture) overwatchRequests(t *testing.T) []string {
	t.Helper()
	rows, err := f.s.db.Query(`SELECT body FROM messages WHERE recipient_family=? AND recipient_id=? AND sender_family='overwatch'
		AND system_kind=? ORDER BY seq`, f.k.Family, f.k.ID, cycleRequestKind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
	}
	return bodies
}

// The request asks the session to pause, report ready and stay idle, and a successful ready
// report says to end the turn (#431).
func TestContextCycleRequestAsksToPauseAndStayIdle(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	requests := f.overwatchRequests(t)
	if len(requests) != 1 || requests[0] != cycleRequestBody(v.ID, v.Revision, f.k.Family, f.k.Family, v.StageDeadline) {
		t.Fatalf("requests: %q", requests)
	}
	for _, want := range []string{"managed context cycle " + v.ID + " revision 1 requests your handoff",
		"Pause your work at the next safe stopping point. The clear does not stop your background subagents",
		"do not stop them or wait for them", "list each one that still runs, with its task and where its result goes",
		"the new session gets each result as a message", "call context_cycle_ready",
		"Report ready by " + time.UnixMilli(v.StageDeadline).UTC().Format("2006-01-02 15:04:05 UTC") + ".",
		"into _handoff/" + f.k.Family + "/ of the main checkout (outside a Git checkout, of the directory where this session started), or into the directory that the Handoff path fact of .koinon/skills/handoff.md names",
		"Then end your turn and stay idle", "overwatch clears your context at your idle prompt", "asks again"} {
		if !strings.Contains(requests[0], want) {
			t.Fatalf("request lacks %q: %s", want, requests[0])
		}
	}
	if strings.Contains(requests[0], "stop any that will not finish") {
		t.Fatalf("a Claude request asks to stop subagents (#533): %s", requests[0])
	}
	// Other families keep the stop rule with the deadline (#467).
	other := cycleRequestBody(v.ID, v.Revision, "codex", "codex", v.StageDeadline)
	for _, want := range []string{"Let running background subagents finish, or stop them",
		"stop any that will not finish with enough time left to update the handoff and report ready by"} {
		if !strings.Contains(other, want) || strings.Contains(other, "The clear does not stop") {
			t.Fatalf("codex request lacks %q: %s", want, other)
		}
	}
	if cycleAgent("codex", "review") != "codex-review" || cycleAgent("claude", "") != "claude" {
		t.Fatal("agent ID is the family with -ROLE")
	}
	if strings.Contains(requests[0], "Finish") {
		t.Fatalf("request still offers to finish the step: %s", requests[0])
	}
	if !strings.Contains(cycleReadyNext, "end your turn now and stay idle") {
		t.Fatalf("ready next: %s", cycleReadyNext)
	}
}

// A repeated request names its own ready deadline, which the cycle's lifetime caps (#467).
func TestContextCycleRepeatNamesCappedDeadline(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	// 20 minutes later, 15 more would pass the cycle's 30-minute lifetime.
	f.clock = f.clock.Add(20 * time.Minute)
	f.adapter.pane = PaneState{Seen: subagentAfterReady}
	f.step(t)
	again := f.status(t, f.k)
	if again.State != cycleWaitingReady || again.Revision != ready.Revision+1 || again.StageDeadline != again.CycleDeadline {
		t.Fatalf("repeat: %+v", again)
	}
	by := "Report ready by " + time.UnixMilli(again.CycleDeadline).UTC().Format("2006-01-02 15:04:05 UTC") + "."
	if requests := f.overwatchRequests(t); len(requests) != 2 || !strings.Contains(requests[1], by) {
		t.Fatalf("requests lack %q: %q", by, requests)
	}
}

// A ready requester whose background subagent worked after the report is asked again at
// once under a new revision, without a clear: its handoff lacks that work (#456).
func TestContextCycleAsksAgainAfterSubagentWork(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.pane = PaneState{Seen: subagentAfterReady}
	f.step(t)
	again := f.status(t, f.k)
	if again.State != cycleWaitingReady || again.Reason != "not_idle."+subagentAfterReady || again.Revision != ready.Revision+1 ||
		again.Artifact != "" || f.adapter.count("clear") != 0 {
		t.Fatalf("asked again: %+v clears %d", again, f.adapter.count("clear"))
	}
	if requests := f.overwatchRequests(t); len(requests) != 2 || requests[1] != cycleRequestBody(again.ID, again.Revision, f.k.Family, f.k.Family, again.StageDeadline) {
		t.Fatalf("requests: %q", requests)
	}
	// A waiting_ready cycle reports no ready time, so the same observation repeats nothing.
	f.step(t)
	if v := f.status(t, f.k); v.Revision != again.Revision || len(f.overwatchRequests(t)) != 2 {
		t.Fatalf("second step: %+v", v)
	}
}

// claudeInspectAdapter is the synthetic input adapter with the real Claude inspection.
type claudeInspectAdapter struct {
	*fakeAdapter
	tools hostTools
}

func (a *claudeInspectAdapter) Inspect(ctx context.Context, target CycleTarget, fence CycleFence) (PaneState, error) {
	target.tools = a.tools
	return (claudeCycle{}).Inspect(ctx, target, fence)
}

// A native dialog after the ready report returns the cycle to ready without a new report:
// subagent work during the dialog is still after the handoff (PR 457 review).
func TestContextCycleReadyTimeSurvivesDialog(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	reported := f.clock
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	previous := cycleAdapters["claude"]
	cycleAdapters["claude"] = &claudeInspectAdapter{fakeAdapter: f.adapter, tools: term.target(t).tools}
	t.Cleanup(func() { cycleAdapters["claude"] = previous })
	term.asks = true
	f.clock = reported.Add(time.Second)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingPrompt {
		t.Fatalf("dialog: %+v", v)
	}
	subagentTranscript(t, filepath.Join(term.home, ".claude"), term.epoch, "agent-synthetic.jsonl", reported.Add(2*time.Second))
	term.asks = false
	f.clock = reported.Add(3 * time.Second)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleReady || v.Reason != "prompt_resolved" {
		t.Fatalf("dialog resolved: %+v", v)
	}
	f.clock = reported.Add(4 * time.Second)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingReady || v.Reason != "not_idle."+subagentAfterReady || f.adapter.count("clear") != 0 {
		t.Fatalf("after the dialog: %+v clears %d", v, f.adapter.count("clear"))
	}
}

// A requester that reported ready but kept working is asked again under a new revision; its
// second report, at an idle prompt, gets the clear (#431).
func TestContextCycleAsksAgainWhenThePaneStaysBusy(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.pane = PaneState{Seen: "status_busy"}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleReady || f.adapter.count("clear") != 0 {
		t.Fatalf("before the deadline: %+v", v)
	}
	// The session acknowledged the first request.
	if _, err := f.s.Ack(context.Background(), f.k, 1); err != nil {
		t.Fatal(err)
	}
	f.clock = time.UnixMilli(ready.StageDeadline)
	f.step(t)
	again := f.status(t, f.k)
	if again.State != cycleWaitingReady || again.Reason != "not_idle.status_busy" || again.Revision != ready.Revision+1 ||
		again.Artifact != "" || again.StageDeadline != min(ready.StageDeadline+cycleReadyWait.Milliseconds(), again.CycleDeadline) {
		t.Fatalf("asked again: %+v", again)
	}
	requests := f.overwatchRequests(t)
	if len(requests) != 2 || requests[1] != cycleRequestBody(again.ID, again.Revision, f.k.Family, f.k.Family, again.StageDeadline) {
		t.Fatalf("requests: %q", requests)
	}
	// The cycle no longer owns the pane, so the new request's wake goes out.
	if held, err := f.s.paneHeldByCycle(context.Background(), f.k); err != nil || held {
		t.Fatalf("pane still held after the repeat: %v %v", held, err)
	}
	var seq int64
	if err := f.s.db.QueryRow(`SELECT message_seq FROM context_cycles WHERE cycle_id=?`, again.ID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err := f.s.db.QueryRow(`SELECT MAX(seq) FROM messages WHERE recipient_family=? AND recipient_id=?`, f.k.Family, f.k.ID).Scan(&last); err != nil || seq != last {
		t.Fatalf("message_seq %d, newest %d %v", seq, last, err)
	}
	var audit string
	if err := f.s.db.QueryRow(`SELECT reason FROM audit WHERE action='context-cycle' ORDER BY id DESC LIMIT 1`).Scan(&audit); err != nil || audit != "not_idle.status_busy" {
		t.Fatalf("audit %q %v", audit, err)
	}
	// The new request is unread and due for its wake although the first one was acknowledged.
	var delivery string
	var acked int64
	if err := f.s.db.QueryRow(`SELECT m.delivery_state,s.acked_through FROM messages m JOIN sessions s ON s.family=m.recipient_family
		AND s.id=m.recipient_id WHERE m.recipient_family=? AND m.recipient_id=? AND m.seq=?`, f.k.Family, f.k.ID, seq).Scan(&delivery, &acked); err != nil ||
		delivery != "waiting" || acked >= seq || acked < seq-1 {
		t.Fatalf("new request: %q acked %d seq %d %v", delivery, acked, seq, err)
	}
	// A second repeat of the former revision, as from a concurrent step, changes nothing.
	if err := f.s.repeatCycleRequest(context.Background(), again.ID, ready.Revision, "status_busy"); !errors.Is(err, ErrCycleChanged) ||
		len(f.overwatchRequests(t)) != 2 {
		t.Fatalf("stale repeat: %v", err)
	}
	// The former report, with its discarded handoff, is stale and restores nothing.
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, ready.Revision, ready.Artifact); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("stale ready with the old handoff: %v", err)
	}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingReady || v.Artifact != "" || f.adapter.count("clear") != 0 {
		t.Fatalf("after the stale report: %+v", v)
	}
	// The handoff written before the repeat is not new for the repeated request (#431).
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, again.Revision, ready.Artifact); !artifactRefused(err, "") {
		t.Fatalf("old handoff for the repeated request: %v", err)
	}
	// The former revision is stale; a new handoff under the new revision is ready again.
	f.clock = f.clock.Add(time.Second)
	name := f.handoff(t, "claude", "2026-10-13-130000.md", "synthetic second handoff")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, ready.Revision, name); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("stale ready: %v", err)
	}
	second, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, again.Revision, name)
	if err != nil || second.State != cycleReady {
		t.Fatalf("second ready: %+v %v", second, err)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleClearReceived || f.adapter.count("clear") != 1 {
		t.Fatalf("idle after the second report: %+v %v", v, f.adapter.calls)
	}
}

// Repeats stay within the cycle's bounds: a requester that never becomes idle ends the cycle
// at its lifetime or its event limit, and its Enable ends as for any failed cycle (#431).
func TestContextCycleRepeatsEndAtTheCycleBounds(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.pane = PaneState{Seen: "status_busy"}
	v := &ready
	for round := 0; round < maxCycleEvents && v.State != cycleFailed; round++ {
		if v.State == cycleReady {
			f.clock = time.UnixMilli(v.StageDeadline)
			f.step(t)
		} else {
			name := f.handoff(t, "claude", fmt.Sprintf("2026-10-13-14%04d.md", round), "synthetic repeat")
			if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name); err != nil {
				t.Fatalf("round %d ready: %v", round, err)
			}
		}
		v = f.status(t, f.k)
	}
	if v.State != cycleFailed || v.Reason != "cycle_timeout" && v.Reason != "event_limit" || f.adapter.count("clear") != 0 {
		t.Fatalf("bounded: %+v", v)
	}
	if len(f.overwatchRequests(t)) < 2 {
		t.Fatal("no repeated request before the bound")
	}
	var enabled bool
	if err := f.s.db.QueryRow(`SELECT enabled FROM context_enables WHERE family=? AND session_id=?`, f.k.Family, f.k.ID).Scan(&enabled); err != nil || enabled {
		t.Fatalf("enable after the bound: %v %v", enabled, err)
	}
}

// At the ready deadline a busy pane of a session whose Enable ended, or that left its pin, is
// not asked again: the cycle ends as the timeout did (#431).
func TestContextCycleDoesNotAskAgainWithoutItsEnable(t *testing.T) {
	for name, change := range map[string]func(f *cycleFixture){
		"disabled": func(f *cycleFixture) {
			if _, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, false); err != nil {
				t.Fatal(err)
			}
		},
		"moved": func(f *cycleFixture) {
			if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=5151,host_start=88 WHERE family='claude' AND id=?`, f.k.ID); err != nil {
				t.Fatal(err)
			}
		},
	} {
		f := newCycleFixture(t)
		ready, _ := f.readyCycle(t)
		f.adapter.pane = PaneState{Seen: "status_busy"}
		f.step(t)
		change(f)
		f.clock = time.UnixMilli(ready.StageDeadline)
		f.step(t)
		v := f.status(t, f.k)
		if v.State != cycleFailed && v.State != cycleCancelled || len(f.overwatchRequests(t)) != 1 {
			t.Fatalf("%s: %+v", name, v)
		}
	}
}

// What the first ready stage saw does not decide the second one's timeout: after a restart
// with no inspection in the new stage, a pane that cannot be proven fails the cycle (#431).
func TestContextCycleSecondReadyStageStartsWithoutTheFirstObservation(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.pane = PaneState{Seen: "status_busy"}
	f.step(t)
	f.clock = time.UnixMilli(ready.StageDeadline)
	f.step(t)
	again := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-150000.md", "synthetic second handoff")
	second, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, again.Revision, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.s.paneSeen.get(again.ID); ok {
		t.Fatal("the first stage's observation is kept")
	}
	f.adapter.inspect = errClaudeUnproven
	f.clock = time.UnixMilli(second.StageDeadline)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "ready_timeout.pane_unproven" || len(f.overwatchRequests(t)) != 2 {
		t.Fatalf("second stage: %+v", v)
	}
}

// A handoff that changes after a late repeated request returns to readiness under that
// request's deadline, not the first request's, which has passed (#431 review).
func TestContextCycleArtifactChangeAfterARepeatKeepsTheNewDeadline(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	first := f.status(t, f.k)
	f.clock = f.clock.Add(14 * time.Minute)
	name := f.handoff(t, "claude", "2026-10-13-160000.md", "synthetic handoff")
	ready, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", first.ID, first.Revision, name)
	if err != nil {
		t.Fatal(err)
	}
	f.adapter.pane = PaneState{Seen: "status_busy"}
	f.step(t)
	f.clock = time.UnixMilli(ready.StageDeadline)
	f.step(t)
	again := f.status(t, f.k)
	repeatedAt := f.clock.UnixMilli()
	if again.State != cycleWaitingReady || repeatedAt <= first.CreatedAt+cycleReadyWait.Milliseconds() {
		t.Fatalf("late repeat: %+v", again)
	}
	f.clock = f.clock.Add(time.Minute)
	second := f.handoff(t, "claude", "2026-10-13-161000.md", "synthetic second handoff")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", again.ID, again.Revision, second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "_handoff", "claude", second), []byte("synthetic changed handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	v := f.status(t, f.k)
	want := min(repeatedAt+cycleReadyWait.Milliseconds(), v.CycleDeadline)
	if v.State != cycleWaitingReady || v.Reason != "artifact_changed" || v.StageDeadline != want || v.StageDeadline <= f.clock.UnixMilli() {
		t.Fatalf("artifact change after the repeat: %+v, want deadline %d", v, want)
	}
}

// A wake whose range holds an unfinished cycle's request names the cycle; after the cycle
// ends, the notice for the same unread request names none (#445).
func TestCycleRequestWakeNamesTheCycle(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	var sent []string
	f.s.wake.send = func(_ context.Context, _ Session, notice string) wakeResult {
		sent = append(sent, notice)
		return wakeResult{"notified", "accepted"}
	}
	wake := func() {
		t.Helper()
		f.s.wake.mu.Lock()
		defer f.s.wake.mu.Unlock()
		if _, err := f.s.submitWake(context.Background(), f.k, false); err != nil {
			t.Fatal(err)
		}
	}
	wake()
	if want := " Managed context-cycle request " + v.ID + "."; len(sent) != 1 || !strings.HasSuffix(sent[0], want) {
		t.Fatalf("notice during the cycle: %q", sent)
	}
	f.clock = time.UnixMilli(v.StageDeadline)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleFailed {
		t.Fatalf("cycle: %+v", v)
	}
	wake()
	if len(sent) != 2 || strings.Contains(sent[1], "Managed context-cycle") || !strings.HasSuffix(sent[1], "Read your Koinon inbox.") {
		t.Fatalf("notice after the cycle: %q", sent)
	}
}

// After a repeat, the timeout reason follows the repeated request: an acknowledged first
// request does not make the cycle read (#445).
func TestRepeatedRequestTimeoutFollowsTheNewRequest(t *testing.T) {
	for _, readRepeat := range []bool{false, true} {
		f := newCycleFixture(t)
		ready, _ := f.readyCycle(t)
		f.adapter.pane = PaneState{Seen: "status_busy"}
		if _, err := f.s.Ack(context.Background(), f.k, 1); err != nil {
			t.Fatal(err)
		}
		f.clock = time.UnixMilli(ready.StageDeadline)
		f.step(t)
		again := f.status(t, f.k)
		if again.State != cycleWaitingReady || len(f.overwatchRequests(t)) != 2 {
			t.Fatalf("asked again: %+v", again)
		}
		want := "waiting_ready_timeout.unread"
		if readRepeat {
			if _, err := f.s.ReadInbox(context.Background(), f.k, 1, 10); err != nil {
				t.Fatal(err)
			}
			want = "waiting_ready_timeout.read"
		}
		f.clock = time.UnixMilli(again.StageDeadline)
		f.step(t)
		if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != want {
			t.Fatalf("read the repeat %v: %+v", readRepeat, v)
		}
	}
}
