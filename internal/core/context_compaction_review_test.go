package core

// The review findings of PR 517 (#509), kept as regressions: the confirmation reads only a
// fresh reading after the receipt and before the deadline; the input needs fresh evidence at
// the threshold; the fence holds the threshold through the input; a compaction that ends
// before its input, also on a probe failure, arms the next crossing.

import (
	"testing"
	"time"
)

func TestCompactionReviewConfirmationBoundary(t *testing.T) {
	t.Run("reading before submission", func(t *testing.T) {
		f := newCompactFixture(t)
		f.now(t, 800)
		var beforeSend time.Time
		f.compactor.beforeCompact = func() {
			// A native reading is produced after intent was recorded but before
			// the adapter submits Enter; it must not prove this submission.
			beforeSend = f.clock.Add(time.Millisecond)
			f.clock = f.clock.Add(time.Second)
		}
		f.step(t)
		if got := f.status(t, f.k); got.State != cycleCompactSent {
			t.Fatalf("send setup: %+v", got)
		}
		f.read(t, 0, f.k.ID, beforeSend) // MCP first observes the delayed pre-submission record
		if got := f.status(t, f.k); got.State == cycleComplete {
			t.Fatalf("confirmed from pre-submission record: source=%d submission>=%d state=%s reason=%s", beforeSend.UnixMilli(), beforeSend.Add(time.Second).UnixMilli(), got.State, got.Reason)
		}
	})
	for _, scenario := range []string{"stale source", "future source", "late confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCompactFixture(t)
			f.now(t, 800)
			f.step(t)
			f.status(t, f.k)
			source := f.clock.Add(time.Millisecond)
			if scenario == "stale source" {
				f.clock = f.clock.Add(3 * time.Minute)
			} else if scenario == "late confirmation" {
				f.clock = f.clock.Add(cycleCompactConfirm + time.Second)
				source = f.clock.Add(time.Millisecond)
			} else {
				source = f.clock.Add(30 * time.Second)
			}
			f.read(t, 0, f.k.ID, source)
			if got := f.status(t, f.k); got.State == cycleComplete {
				t.Fatalf("confirmed from %s: source=%d now=%d state=%s reason=%s", scenario, source.UnixMilli(), f.clock.UnixMilli(), got.State, got.Reason)
			}
		})
	}
}

func TestCompactionReviewStaleWaitingEvidence(t *testing.T) {
	f := newCompactFixture(t)
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.now(t, 800)
	f.step(t)
	f.status(t, f.k)
	f.clock = f.clock.Add(3 * time.Minute)
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if n := f.compactor.count("enter"); n != 0 {
		t.Fatalf("sent %d /compact with a three-minute-old context record and confirmation; state=%s", n, f.status(t, f.k).State)
	}
}

func TestCompactionReviewWaitingRestartNeedsObservation(t *testing.T) {
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
	if e := f.s.contextEvidence(f.k); e.Found {
		t.Fatal("unexpected retained observation")
	}
	f.step(t)
	if n := f.compactor.count("enter"); n != 0 {
		t.Fatalf("sent %d /compact after restart with no context evidence; state=%s", n, f.status(t, f.k).State)
	}
}

func TestCompactionReviewThresholdAtSubmission(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.compactor.beforeCompact = func() {
		_, err := f.s.SetContextThreshold(roomCtx("context-threshold"), f.reload(t, f.k).Name, 90)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.step(t)
	if n := f.compactor.count("enter"); n != 0 {
		t.Fatalf("sent %d /compact at 80%% with new session threshold 90%%; recorded threshold=%d state=%s", n, f.status(t, f.k).Threshold, f.status(t, f.k).State)
	}
}

func TestCompactionReviewProbeFailureRearms(t *testing.T) {
	f := newCompactFixture(t)
	f.now(t, 800)
	f.compactor.compactProbe = CycleProbe{Reason: "pane_unproven"}
	f.step(t)
	first := f.status(t, f.k)
	if first.Reason != "pane_unproven" || !f.enabled(t) || f.compactor.count("enter") != 0 {
		t.Fatalf("probe failure setup: %+v", first)
	}
	f.compactor.compactProbe = CycleProbe{Entry: "/compact"}
	f.now(t, 900)
	f.step(t)
	if n := f.compactor.count("enter"); n == 0 {
		e := f.s.contextEvidence(f.k)
		f.s.observed.mu.Lock()
		latched := f.s.latches.latched(f.k, e, 75)
		f.s.observed.mu.Unlock()
		_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, CycleAvailable: true, Policy: ContextFamilyPolicy{Mode: ContextNativeCompaction, Threshold: 75}, Evidence: e, Now: f.clock.UnixMilli(), LatchedEpoch: latched})
		t.Fatalf("no input after probe recovered: cycles=%d last=%s/%s trigger=%s", f.cycles(t), f.status(t, f.k).State, f.status(t, f.k).Reason, reason)
	}
}
