package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// #588: a successor that restored the handoff after the
// verified reset, before Koinon entered the pickup, reports at reset_verified. The report and
// Koinon's pickup input race, and one of them wins under the cycle's revision.

// resetSuccessor registers the successor session in the requester's host and pane, holding
// its participant address, as TestContextCycleEnableCarry does.
func resetSuccessor(t *testing.T, f *cycleFixture) Key {
	t.Helper()
	successor := Key{"claude", "synthetic-epoch-2"}
	join(t, f.s, "claude", successor.ID, f.repo)
	fixtureExec(f.s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, successor.ID)
	fixtureExec(f.s, `UPDATE names SET holder_id=? WHERE name=?`, successor.ID, f.session.Address)
	return successor
}

// A report at reset_verified completes the cycle, carries the Enable to the successor, and
// the controller enters no pickup input after it.
func TestContextCyclePickupAtResetVerified(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	v, name := cycleToReset(t, f)
	successor := resetSuccessor(t, f)
	found := f.status(t, successor)
	if found == nil || found.Role != "successor" || found.State != cycleResetVerified {
		t.Fatalf("successor status: %+v", found)
	}
	// Another epoch than the verified one is refused.
	if _, err := f.s.ContextCyclePickup(ctx, successor, "synthetic-epoch-9", v.ID, v.Revision, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleEpoch) {
		t.Fatalf("other epoch: %v", err)
	}
	done, err := f.s.ContextCyclePickup(ctx, successor, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work")
	if err != nil || done.State != cycleComplete || done.Reason != "resumed" {
		t.Fatalf("report at reset_verified: %v %+v", err, done)
	}
	// The identical retry returns the receipt; a different one is refused.
	if again, err := f.s.ContextCyclePickup(ctx, successor, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work"); err != nil || again.State != cycleComplete {
		t.Fatalf("retry: %v %+v", err, again)
	}
	if _, err := f.s.ContextCyclePickup(ctx, successor, "synthetic-epoch-2", v.ID, v.Revision, name, "paused", "synthetic restored work"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("different retry: %v", err)
	}
	enables, _ := f.s.contextEnables(ctx)
	if !enables[successor].Enabled || enables[f.k].Enabled {
		t.Fatalf("Enable carry: %+v", enables)
	}
	f.step(t)
	if f.adapter.count("pickup") != 0 {
		t.Fatalf("input after the report: %v", f.adapter.calls)
	}
}

// The controller read reset_verified before the report completed the cycle: its pickup input
// loses at its revision and enters nothing.
func TestContextCyclePickupReportBeatsInput(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	v, name := cycleToReset(t, f)
	held := f.cycle(t, v.ID)
	if done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work"); err != nil || done.State != cycleComplete {
		t.Fatalf("report: %v %+v", err, done)
	}
	if err := f.s.stepCycle(ctx, held); err != nil && !errors.Is(err, ErrCycleChanged) {
		t.Fatal(err)
	}
	if got := f.status(t, f.k); got.State != cycleComplete || f.adapter.count("pickup") != 0 {
		t.Fatalf("after the held step: %+v %v", got, f.adapter.calls)
	}
	enables, _ := f.s.contextEnables(ctx)
	if !enables[f.k].Enabled {
		t.Fatalf("the requester's own Enable: %+v", enables)
	}
}

// The report read reset_verified, and the controller recorded its pickup intent before the
// report's write: the intent wins and the report names the stage. After the input's receipt,
// the revision that the report read still names it.
func TestContextCyclePickupInputBeatsReport(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	v, name := cycleToReset(t, f)
	f.s.pickupWrite = func() {
		f.s.pickupWrite = nil
		if _, err := f.s.advanceCycle(ctx, v.ID, v.Revision, cycleResetVerified, cyclePickupIntent, "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic")
	if errorCode(err) != ErrCyclePickupNotReady.Code || !strings.Contains(err.Error(), "the cycle is at pickup_intent; Koinon is entering the pickup;") {
		t.Fatalf("report after the intent: %v", err)
	}
	if got := f.status(t, f.k); got.State != cyclePickupIntent {
		t.Fatalf("intent: %+v", got)
	}
	c := f.cycle(t, v.ID)
	if _, err := f.s.advanceCycle(ctx, v.ID, c.Revision, cyclePickupIntent, cycleWaitingPickup, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work"); err != nil || done.State != cycleComplete {
		t.Fatalf("report at waiting_pickup with the reset_verified revision: %v %+v", err, done)
	}
}

// A dialog that the report raises at reset_verified changes the revision; the report from the
// revision read before it, or during it, completes the cycle.
func TestContextCyclePickupDialogAtResetVerified(t *testing.T) {
	ctx := context.Background()
	for _, from := range []string{"reset_verified", "dialog"} {
		f := newCycleFixture(t)
		v, name := cycleToReset(t, f)
		pickupDialog(f.adapter, 0)
		f.step(t)
		now := *f.status(t, f.k)
		if now.State != cycleWaitingPrompt || now.PromptFrom != cycleResetVerified || f.adapter.count("pickup") != 0 {
			t.Fatalf("dialog at reset_verified: %+v", now)
		}
		revision := v.Revision
		if from == "dialog" {
			revision = now.Revision
		}
		done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, revision, name, "resumed", "synthetic restored work")
		if err != nil || done.State != cycleComplete {
			t.Fatalf("%s: report during the dialog: %v %+v", from, err, done)
		}
	}
}

// A deadline that passes during the report's checks refuses it, and the cycle stays at its
// stage for the controller to end.
func TestContextCyclePickupResetDeadline(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	v, name := cycleToReset(t, f)
	f.s.pickupWrite = func() {
		f.s.pickupWrite = nil
		f.clock = time.UnixMilli(v.StageDeadline)
	}
	if _, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("report at the stage deadline: %v", err)
	}
	if got := f.cycle(t, v.ID); got.State != cycleResetVerified {
		t.Fatalf("after the refused report: %+v", got)
	}
}
