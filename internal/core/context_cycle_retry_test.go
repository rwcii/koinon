package core

import (
	"context"
	"testing"
	"time"
)

// A ready timeout before any input keeps the Enable once in each epoch and Enable revision.
// The next cycle waits for the session's own inbox read after the timeout, also across a
// restart; a second timeout ends the Enable; a new Enable gives one more retry, and a repeated
// Enable changes nothing (DECISIONS.md#READY-TIMEOUT-ENABLE-487, #584).
func TestReadyTimeoutKeepsTheEnableOncePerEpoch(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	other := join(t, f.s, "claude", "synthetic-b", f.repo)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	first := f.status(t, f.k)
	// A read before the timeout does not count.
	if _, err := f.s.ReadInbox(ctx, f.k, 0, 0); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "waiting_ready_timeout.read" {
		t.Fatalf("first timeout: %+v", v)
	}
	enabled := func() ContextEnable {
		t.Helper()
		e, err := f.s.contextEnables(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return e[f.k]
	}
	kept := enabled()
	if !kept.Enabled {
		t.Fatal("a first ready timeout ended the Enable")
	}
	// No cycle before the session's own read, also after a restart and after another
	// session's read.
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if _, err := fixtureExec(f.s, `UPDATE sessions SET expires_at=? WHERE family='claude' AND id=?`, f.clock.Add(time.Hour).UnixMilli(), other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ReadInbox(ctx, Key{"claude", other.ID}, 0, 0); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if v := f.status(t, f.k); v.ID != first.ID {
		t.Fatalf("a cycle started before the inbox read: %+v", v)
	}
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = f.s.now
	f.s = reopened
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if v := f.status(t, f.k); v.ID != first.ID {
		t.Fatalf("a cycle started after a restart before the inbox read: %+v", v)
	}
	if row := f.s.contextRowOf(f.reload(t, f.k), kept, f.clock.UnixMilli()); row.Reason != "awaiting_inbox_read" {
		t.Fatalf("reason: %+v", row)
	}
	// An empty read after the timeout lets the next fresh reading start one cycle.
	if _, err := f.s.ReadInbox(ctx, f.k, 1<<40, 0); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	second := f.status(t, f.k)
	if second.ID == first.ID || second.State != cycleWaitingReady {
		t.Fatalf("no cycle after the inbox read: %+v", second)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if v := f.status(t, f.k); v.ID != second.ID || count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE family='claude' AND session_id='synthetic-a'`) != 2 {
		t.Fatalf("a second cycle started: %+v", v)
	}
	// The second timeout in the epoch ends the Enable.
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "waiting_ready_timeout.unread" {
		t.Fatalf("second timeout: %+v", v)
	}
	if enabled().Enabled {
		t.Fatal("a second ready timeout in the epoch kept the Enable")
	}
	// A new Enable starts a cycle in the same epoch at the next fresh reading and gives one
	// more retry.
	on, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, true)
	if err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	third := f.status(t, f.k)
	if third.ID == second.ID || third.State != cycleWaitingReady {
		t.Fatalf("no cycle after a new Enable: %+v", third)
	}
	if again, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, true); err != nil || again.Revision != on.Revision {
		t.Fatalf("a repeated Enable changed it: %+v %v", again, err)
	}
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	if !enabled().Enabled {
		t.Fatal("a new Enable gave no retry")
	}
}

// A cycle that times out after an input still ends the Enable.
func TestATimeoutAfterInputEndsTheEnable(t *testing.T) {
	f := newCycleFixture(t)
	v, _ := f.readyCycle(t)
	if _, err := f.s.advanceCycle(context.Background(), v.ID, v.Revision, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.endCycle(context.Background(), v.ID, 0, cycleFailed, "clear_intent_timeout"); err != nil {
		t.Fatal(err)
	}
	if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled {
		t.Fatal("a timeout after an input kept the Enable")
	}
}

// The session's own read after the timeout counts also after a restart, before the daemon has
// a new context report (#585 review).
func TestAReadAfterARestartCountsBeforeTheFirstReport(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	first := f.status(t, f.k)
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = f.s.now
	f.s = reopened
	if _, err := f.s.ReadInbox(ctx, f.k, 1<<40, 0); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if v := f.status(t, f.k); v.ID == first.ID || v.State != cycleWaitingReady {
		t.Fatalf("the read before the first report was lost: %+v", v)
	}
}

// The lifetime deadline keeps its rule: it ends the Enable.
func TestALifetimeTimeoutEndsTheEnable(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	f.clock = f.clock.Add(cycleLifetime)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "cycle_timeout" {
		t.Fatalf("lifetime: %+v", v)
	}
	if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled {
		t.Fatal("cycle_timeout kept the Enable")
	}
}

// The retention of finished cycles keeps a kept cycle while its Enable is in force, so its
// read gate and the epoch's retry stay; after a new Enable it can go.
func TestRetentionKeepsALiveKeptTimeout(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	first := f.status(t, f.k)
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	old := f.clock.Add(-terminalCycleRetention).UnixMilli() - 1
	if _, err := fixtureExec(f.s, `UPDATE context_cycles SET finished_at=? WHERE cycle_id=?`, old, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.trimCycles(ctx); err != nil {
		t.Fatal(err)
	}
	e, _ := f.s.contextEnables(ctx)
	if id, err := keptTimeout(ctx, f.s.db, f.k, e[f.k].Revision, "synthetic-epoch-1"); err != nil || id != first.ID {
		t.Fatalf("retention removed the kept cycle: %q %v", id, err)
	}
	if _, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, false); err != nil {
		t.Fatal(err)
	}
	if err := f.s.trimCycles(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE cycle_id='`+first.ID+`'`); n != 0 {
		t.Fatalf("an old kept cycle stayed after its Enable ended: %d", n)
	}
}
