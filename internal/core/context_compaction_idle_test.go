package core

import (
	"context"
	"testing"
	"time"
)

// Idle compaction (DECISIONS.md#IDLE-COMPACT-564): in native_compaction, a reading older than
// the source age counts when its newest confirmation is caught up; the input then needs a
// confirmation of the same mark received after the compaction's choice.

// caught reports used of 1,000 tokens recorded at at, with the caught-up mark end (nil for a
// read that did not catch up), as the Codex reader would.
func (f *compactFixture) caught(t *testing.T, used int64, epoch string, at time.Time, end *RolloutEnd) {
	t.Helper()
	f.clock = f.clock.Add(time.Millisecond)
	if err := f.s.Observe(context.Background(), Observation{Caller: f.k, Context: &ObservedValue{Source: "codex_rollout",
		At: at.UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(used), Epoch: epoch, End: end}}); err != nil {
		t.Fatal(err)
	}
}

func (f *compactFixture) seen(t *testing.T, id string) string {
	t.Helper()
	seen, _ := f.s.paneSeen.get(id)
	return seen
}

// nextConfirmation is the wait before the reader's next confirmation: the reader confirms
// each minute, and the fixture's session lease is 60 seconds.
const nextConfirmation = 20 * time.Second

func rolloutEnd(size int64) *RolloutEnd {
	return &RolloutEnd{Rollout: "64769-1234", Size: size, Turn: "01a1874e-08bb-75b2-b401-28510d177bc1"}
}

// An idle session whose last reading is old compacts: its caught-up confirmation admits the
// compaction, the first step chooses that mark and waits, and the next confirmation of the
// same mark lets the input go. A fresh reading below the threshold confirms the compaction.
func TestIdleCompactionThroughCaughtUpRead(t *testing.T) {
	f := newCompactFixture(t)
	old := f.clock.Add(-30 * time.Minute)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	v := f.status(t, f.k)
	if v == nil || v.State != cycleCompactWaiting {
		t.Fatalf("admission: %+v", v)
	}
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactWaiting || f.compactor.count("enter") != 0 || f.seen(t, v.ID) != "confirmation_pending" {
		t.Fatalf("choice: %+v seen %q %v", v, f.seen(t, v.ID), f.compactor.calls)
	}
	// A step without a new confirmation still waits.
	f.clock = f.clock.Add(time.Second)
	f.step(t)
	if f.compactor.count("enter") != 0 {
		t.Fatalf("entered without a new confirmation: %v", f.compactor.calls)
	}
	f.clock = f.clock.Add(nextConfirmation)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("enter") != 1 {
		t.Fatalf("send: %+v %v", v, f.compactor.calls)
	}
	f.clock = f.clock.Add(time.Second)
	f.now(t, 0)
	if v = f.status(t, f.k); v.State != cycleComplete || v.Reason != "compacted" {
		t.Fatalf("confirmation: %+v", v)
	}
}

// Without a caught-up mark, with an old confirmation or below the threshold, an old reading
// admits nothing; nor does the handoff cycle accept the mark.
func TestIdleCompactionAdmitsOnlyCaughtUpReadings(t *testing.T) {
	cases := map[string]func(f *compactFixture, old time.Time){
		// A busy session, a batch-bounded or partial read and a read failure all report no mark.
		"no mark": func(f *compactFixture, old time.Time) { f.caught(t, 800, f.k.ID, old, nil) },
		"below threshold": func(f *compactFixture, old time.Time) {
			f.caught(t, 700, f.k.ID, old, rolloutEnd(5000))
		},
		"old confirmation": func(f *compactFixture, old time.Time) {
			// Stored with the mark under another mode, then left unconfirmed past 90 seconds.
			if err := f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextObserve, 75); err != nil {
				t.Fatal(err)
			}
			f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
			f.clock = f.clock.Add(contextConfirmationMaxAge + time.Second)
			if err := f.s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextNativeCompaction, 75); err != nil {
				t.Fatal(err)
			}
			f.s.admitContextCycle(context.Background(), f.k)
		},
	}
	for name, report := range cases {
		f := newCompactFixture(t)
		report(f, f.clock.Add(-30*time.Minute))
		if v := f.status(t, f.k); v != nil || f.cycles(t) != 0 {
			t.Fatalf("%s: admitted %+v", name, v)
		}
		policy := ContextFamilyPolicy{Family: "codex", Mode: ContextNativeCompaction, Threshold: 75}
		_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true,
			Evidence: f.s.contextEvidence(f.k), Now: f.clock.UnixMilli()})
		want := map[string]string{"no mark": "source_stale", "below threshold": "below_threshold", "old confirmation": "confirmation_stale"}[name]
		if reason != want {
			t.Fatalf("%s: reason %q, want %q", name, reason, want)
		}
	}
	f := newCompactFixture(t)
	f.caught(t, 800, f.k.ID, f.clock.Add(-30*time.Minute), rolloutEnd(5000))
	policy := ContextFamilyPolicy{Family: "codex", Mode: ContextHandoffCycle, Threshold: 75}
	if _, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true,
		Evidence: f.s.contextEvidence(f.k), Now: f.clock.UnixMilli()}); reason != "source_stale" {
		t.Fatalf("handoff_cycle with a caught-up mark: %q", reason)
	}
}

// After the choice, a confirmation without the mark (a new turn, a read that stopped at a
// batch or a partial record) ends the compaction without input; a confirmation with another
// mark (a changed size, turn or rollout file) makes a new choice; another epoch ends it.
func TestIdleCompactionNeedsTheChosenMark(t *testing.T) {
	f := newCompactFixture(t)
	old := f.clock.Add(-30 * time.Minute)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	f.clock = f.clock.Add(nextConfirmation)
	f.caught(t, 800, f.k.ID, old, nil)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "source_stale" || f.compactor.count("enter") != 0 || !f.enabled(t) {
		t.Fatalf("mark cleared: %+v %v", v, f.compactor.calls)
	}

	for name, other := range map[string]*RolloutEnd{
		"size":    rolloutEnd(5200),
		"turn":    {Rollout: "64769-1234", Size: 5000, Turn: "01a1874e-0000-75b2-b401-28510d177bc1"},
		"rollout": {Rollout: "64769-9999", Size: 5000, Turn: "01a1874e-08bb-75b2-b401-28510d177bc1"},
	} {
		f := newCompactFixture(t)
		old := f.clock.Add(-30 * time.Minute)
		f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
		f.step(t)
		f.clock = f.clock.Add(nextConfirmation)
		f.caught(t, 800, f.k.ID, old, other)
		f.step(t)
		v := f.status(t, f.k)
		if v.State != cycleCompactWaiting || f.compactor.count("enter") != 0 || f.seen(t, v.ID) != "confirmation_pending" {
			t.Fatalf("%s: another mark entered: %+v %v", name, v, f.compactor.calls)
		}
		f.clock = f.clock.Add(nextConfirmation)
		f.caught(t, 800, f.k.ID, old, other)
		f.step(t)
		if v = f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("enter") != 1 {
			t.Fatalf("%s: the new mark's confirmation: %+v %v", name, v, f.compactor.calls)
		}
	}

	f = newCompactFixture(t)
	f.caught(t, 800, f.k.ID, f.clock.Add(-30*time.Minute), rolloutEnd(5000))
	f.step(t)
	first := f.status(t, f.k)
	f.caught(t, 100, "synthetic-thread-2", f.clock.Add(-29*time.Minute), rolloutEnd(300))
	ended, err := cyclesWhere(context.Background(), f.s.db, `cycle_id=?`, first.ID)
	if err != nil || ended[0].State != cycleCancelled || ended[0].Reason != "epoch_changed" || f.compactor.count("enter") != 0 {
		t.Fatalf("new epoch: %v %+v", err, ended)
	}
}

// A failed read after the post-choice confirmation ends the mark at once: the compaction ends
// without input, and the report that clears the mark confirms nothing.
func TestIdleCompactionAfterFailedRead(t *testing.T) {
	f := newCompactFixture(t)
	old := f.clock.Add(-30 * time.Minute)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	f.clock = f.clock.Add(nextConfirmation)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	confirmed := f.s.contextEvidence(f.k).ConfirmedAt
	f.clock = f.clock.Add(5 * time.Second)
	f.caught(t, 800, f.k.ID, old, nil)
	if e := f.s.contextEvidence(f.k); e.End != nil || e.ConfirmedAt != confirmed {
		t.Fatalf("the clearing report: %+v, confirmed at %d", e, confirmed)
	}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "source_stale" || f.compactor.count("enter") != 0 || !f.enabled(t) {
		t.Fatalf("after a failed read: %+v %v", v, f.compactor.calls)
	}
	// The next report of the reading without a mark confirms it.
	f.caught(t, 800, f.k.ID, old, nil)
	if e := f.s.contextEvidence(f.k); e.ConfirmedAt != f.clock.UnixMilli() {
		t.Fatalf("next report: %+v", e)
	}
}

// A daemon restart forgets the readings and the choice: the compaction waits for a new
// caught-up reading, chooses it, and enters only after its next confirmation.
func TestIdleCompactionAfterRestart(t *testing.T) {
	f := newCompactFixture(t)
	old := f.clock.Add(-30 * time.Minute)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	f.s.db.Close()
	restarted, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.db.Close() })
	restarted.now = func() time.Time { return f.clock }
	f.s = restarted
	f.clock = f.clock.Add(nextConfirmation)
	f.step(t)
	v := f.status(t, f.k)
	if v.State != cycleCompactWaiting || f.seen(t, v.ID) != "context_unknown" || f.compactor.count("enter") != 0 {
		t.Fatalf("no reading after the restart: %+v seen %q", v, f.seen(t, v.ID))
	}
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactWaiting || f.seen(t, v.ID) != "confirmation_pending" || f.compactor.count("enter") != 0 {
		t.Fatalf("first reading after the restart: %+v seen %q", v, f.seen(t, v.ID))
	}
	f.clock = f.clock.Add(nextConfirmation)
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("enter") != 1 {
		t.Fatalf("after the restart: %+v %v", v, f.compactor.calls)
	}
}

// The mark is private and its form is checked: only a Codex rollout's context carries it.
func TestRolloutEndIsPrivateAndChecked(t *testing.T) {
	f := newCompactFixture(t)
	now := f.clock.UnixMilli()
	for name, v := range map[string]ObservedValue{
		"other source": {Source: "claude_statusline", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(1), Epoch: "e", End: rolloutEnd(1)},
		"no epoch":     {Source: "codex_rollout", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(1), End: rolloutEnd(1)},
		"bad file":     {Source: "codex_rollout", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(1), Epoch: "e", End: &RolloutEnd{Rollout: "/path", Turn: "t"}},
		"bad turn":     {Source: "codex_rollout", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(1), Epoch: "e", End: &RolloutEnd{Rollout: "1-2", Turn: "a b"}},
		"bad size":     {Source: "codex_rollout", At: now, LimitTokens: int64p(1000), UsedTokens: int64p(1), Epoch: "e", End: &RolloutEnd{Rollout: "1-2", Size: -1, Turn: "t"}},
	} {
		if v.valid("context", now) {
			t.Fatalf("%s: accepted", name)
		}
	}
	activity := ObservedValue{Source: "codex_rollout", At: now, State: "idle", End: rolloutEnd(1)}
	if activity.valid("activity", now) {
		t.Fatal("activity with a mark accepted")
	}
	f.caught(t, 100, f.k.ID, f.clock, rolloutEnd(5000))
	views := f.s.sessionObservations(context.Background(), f.reload(t, f.k))
	if view := views["context"]; view.Value == nil || view.Value.End != nil || view.Value.Epoch != "" {
		t.Fatalf("context view: %+v", view)
	}
}
