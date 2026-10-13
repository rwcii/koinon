package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	// A replaced file fails the identical ready retry.
	g := newCycleFixture(t)
	ready, name = g.readyCycle(t)
	path = filepath.Join(g.repo, "_handoff", "claude", name)
	os.Remove(path)
	g.handoff(t, "claude", name, "synthetic replacement")
	if _, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", ready.ID, ready.Revision-1, name); !errors.Is(err, ErrCycleArtifact) {
		t.Fatalf("retry over a replaced file: %v", err)
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
