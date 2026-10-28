package core

// The re-review findings of PR 517 at 664ad47 (#509): the family threshold is fenced too, a
// restart recovers on fresh evidence, and a compaction that a dashboard action ends before
// its input arms its crossing again.

import (
	"testing"
	"time"
)

func TestCompactionReviewR1FamilyThresholdAtSubmission(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.compactor.beforeCompact = func() {
		if err := f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextNativeCompaction, 90); err != nil {
			t.Fatal(err)
		}
	}
	f.step(t)
	if f.compactor.count("enter") != 0 {
		t.Fatal("submitted despite family threshold change during input")
	}
}

func TestCompactionReviewR1RestartFreshRecovery(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.status(t, f.k)
	f.s.db.Close()
	restarted, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.db.Close() })
	restarted.now = func() time.Time { return f.clock }
	f.s = restarted
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactWaiting || f.compactor.count("enter") != 0 {
		t.Fatalf("without evidence: %+v", v)
	}
	f.now(t, 800)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("enter") != 1 {
		t.Fatalf("fresh recovery: %+v", v)
	}
	f.clock = f.clock.Add(time.Second)
	f.now(t, 0)
	if v := f.status(t, f.k); v.State != cycleComplete || !f.enabled(t) {
		t.Fatalf("fresh confirmation: %+v", v)
	}
}

func TestCompactionReviewR1ModeCancellationRearms(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	first := f.status(t, f.k)
	if err := f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextObserve, 75); err != nil {
		t.Fatal(err)
	}
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "mode_changed" || !f.enabled(t) {
		t.Fatalf("mode cancellation setup: %+v", v)
	}
	if err := f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextNativeCompaction, 75); err != nil {
		t.Fatal(err)
	}
	f.now(t, 900)
	f.step(t)
	if v := f.status(t, f.k); v.ID == first.ID || f.compactor.count("enter") != 1 {
		e := f.s.contextEvidence(f.k)
		f.s.observed.mu.Lock()
		latched := f.s.latches.latched(f.k, e, 75)
		f.s.observed.mu.Unlock()
		_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, CycleAvailable: true, Policy: ContextFamilyPolicy{Mode: ContextNativeCompaction, Threshold: 75}, Evidence: e, Now: f.clock.UnixMilli(), LatchedEpoch: latched})
		t.Fatalf("pre-input mode cancellation kept the old crossing: cycles=%d state=%s reason=%s input=%d enabled=%v trigger=%s", f.cycles(t), v.State, v.Reason, f.compactor.count("enter"), f.enabled(t), reason)
	}
}

func TestCompactionReviewR1ConfirmationDeadlineEndsUnconfirmed(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.step(t)
	f.status(t, f.k)
	f.clock = f.clock.Add(cycleCompactConfirm + time.Second)
	f.now(t, 0)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "compact_unconfirmed" || f.enabled(t) {
		t.Fatalf("late observation: %+v", v)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM audit WHERE action='context-cycle' AND result='complete'`); n != 0 {
		t.Fatal("late observation wrote completed audit")
	}
}
