package core

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// The controller's paths for an adapter that creates the successor session, sends its pickup
// asynchronously and proves the display separately (OpenCode, #513), with a synthetic adapter.

// fakeSessionAdapter is fakeAdapter with the SessionAdapter, AsyncPickupAdapter and
// DisplayAdapter methods. NewEpoch proves the recorded created session while proven holds.
type fakeSessionAdapter struct {
	*fakeAdapter
	created   string
	createErr error
	selected  bool
	proven    bool
	// send blocks SendPickup until it is closed or the worker's context ends; sending is
	// closed when the send starts.
	send, sending chan struct{}
	// beforeSend blocks SendPickup before its fence until it is closed.
	beforeSend chan struct{}
	sendOnce   sync.Once
	sendCtxErr error
	// receipt is PickupReceipt's result; receiptBlock blocks it until closed.
	receipt      bool
	receiptErr   error
	receiptBlock chan struct{}
	shown        bool
	foreign      bool
	displayErr   error
	targets      []CycleTarget
}

func (f *fakeSessionAdapter) Create(ctx context.Context, t CycleTarget, fence CycleFence) (string, error) {
	if err := fence(ctx); err != nil {
		return "", err
	}
	f.record("create")
	return f.created, f.createErr
}

func (f *fakeSessionAdapter) Select(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	if err := fence(ctx); err != nil {
		return false, err
	}
	f.record("select " + t.SuccessorEpoch)
	return f.selected, nil
}

func (f *fakeSessionAdapter) NewEpoch(_ context.Context, t CycleTarget) (string, error) {
	f.record("epoch")
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.proven {
		return "", nil
	}
	return t.SuccessorEpoch, nil
}

func (f *fakeSessionAdapter) SendPickup(ctx context.Context, t CycleTarget, fence CycleFence) error {
	if f.beforeSend != nil {
		<-f.beforeSend
	}
	if err := fence(ctx); err != nil {
		f.record("send refused")
		return err
	}
	f.record("send")
	f.mu.Lock()
	f.targets = append(f.targets, t)
	f.mu.Unlock()
	if f.sending != nil {
		f.sendOnce.Do(func() { close(f.sending) })
	}
	if f.send != nil {
		select {
		case <-f.send:
		case <-ctx.Done():
			f.mu.Lock()
			f.sendCtxErr = ctx.Err()
			f.mu.Unlock()
		}
	}
	return nil
}

func (f *fakeSessionAdapter) PickupReceipt(context.Context, CycleTarget) (bool, error) {
	f.record("receipt")
	if f.receiptBlock != nil {
		<-f.receiptBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.receipt, f.receiptErr
}

func (f *fakeSessionAdapter) PickupDisplay(context.Context, CycleTarget) (bool, bool, error) {
	f.record("display")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown, f.foreign, f.displayErr
}

func (f *fakeSessionAdapter) set(change func(*fakeSessionAdapter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// sessionFixture is a cycleFixture whose adapter creates the successor session.
type sessionFixture struct {
	*cycleFixture
	session *fakeSessionAdapter
	name    string
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	f := newCycleFixture(t)
	a := &fakeSessionAdapter{fakeAdapter: f.adapter, created: "synthetic-created-1", selected: true, proven: true, receipt: true}
	cycleAdapters["claude"] = a
	t.Cleanup(func() { f.s.cycleWork.stop() })
	g := &sessionFixture{cycleFixture: f, session: a}
	return g
}

// ready admits a cycle and reports the requester ready.
func (g *sessionFixture) ready(t *testing.T) *CycleView {
	t.Helper()
	g.observe(t, g.k, 900, "synthetic-epoch-1")
	v := g.status(t, g.k)
	g.name = g.handoff(t, "claude", "2026-10-27-120000.md", "synthetic")
	if _, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", v.ID, 1, g.name); err != nil {
		t.Fatal(err)
	}
	return g.status(t, g.k)
}

// disable turns the requester's Enable off.
func (g *sessionFixture) disable(t *testing.T) {
	t.Helper()
	e, err := g.s.contextEnables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.SetContextEnable(roomCtx("context-enable"), g.cycleFixture.session.Name, g.reload(t, g.k).Revision, e[g.k].Revision, false); err != nil {
		t.Fatal(err)
	}
}

// successor registers the created session in the requester's host and pane, holding its address.
func (g *sessionFixture) successor(t *testing.T) Key {
	t.Helper()
	k := Key{"claude", g.session.created}
	join(t, g.s, "claude", k.ID, g.repo)
	if _, err := fixtureExec(g.s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(g.s, `UPDATE names SET holder_id=? WHERE name=?`, k.ID, g.cycleFixture.session.Address); err != nil {
		t.Fatal(err)
	}
	return k
}

// waitSent waits until the worker's send started.
func waitSent(t *testing.T, a *fakeSessionAdapter) {
	t.Helper()
	select {
	case <-a.sending:
	case <-time.After(5 * time.Second):
		t.Fatal("the pickup was not sent")
	}
}

// waitIdle waits until the cycle's worker returned.
func waitIdle(t *testing.T, s *Store, id string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); s.cycleWork.running(id); {
		if time.Now().After(deadline) {
			t.Fatal("the worker did not return")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func cycleReasons(t *testing.T, s *Store, id string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT stage||':'||reason FROM context_cycle_events WHERE cycle_id=? ORDER BY ordinal`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var e string
		rows.Scan(&e)
		events = append(events, e)
	}
	return events
}

// The whole flow: the created ID is recorded with the create's receipt, the select is
// recorded before it is sent and sent once, the reset is proven, the pickup is sent from a
// worker while other steps and wakes go on, its receipt is read back, the display proof is
// recorded, and the successor's report completes the cycle.
func TestSessionCycleFlow(t *testing.T) {
	g := newSessionFixture(t)
	g.session.send, g.session.sending = make(chan struct{}), make(chan struct{})
	v := g.ready(t)
	g.step(t) // clear intent, create, clear_received with the created ID
	c := g.cycle(t, v.ID)
	if c.State != cycleClearReceived || c.SuccessorEpoch != "synthetic-created-1" || g.session.count("create") != 1 || g.session.count("select") != 0 {
		t.Fatalf("after create: %+v %v", c, g.session.calls)
	}
	g.step(t) // select intent, select
	if c = g.cycle(t, v.ID); c.State != cycleClearReceived || g.session.count("select synthetic-created-1") != 1 {
		t.Fatalf("after select: %+v %v", c, g.session.calls)
	}
	g.step(t) // proof
	if c = g.cycle(t, v.ID); c.State != cycleResetVerified || g.session.count("select") != 1 || g.session.count("create") != 1 {
		t.Fatalf("after proof: %+v %v", c, g.session.calls)
	}
	// The successor's early report, before its receipt, changes nothing.
	successor := g.successor(t)
	g.step(t) // pickup intent; the worker sends
	waitSent(t, g.session)
	if c = g.cycle(t, v.ID); c.State != cyclePickupIntent {
		t.Fatalf("after dispatch: %+v", c)
	}
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, "synthetic-created-1", v.ID, c.Revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("early report: %v", err)
	}
	// While the request is pending, a cycle step returns and the wake lock is free.
	g.session.set(func(a *fakeSessionAdapter) { a.receipt = false })
	done := make(chan struct{})
	go func() { g.s.cycleStep(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cycle step waited for the pending pickup")
	}
	if !g.s.wake.mu.TryLock() {
		t.Fatal("the wake lock is held while the pickup is pending")
	}
	g.s.wake.mu.Unlock()
	if c = g.cycle(t, v.ID); c.State != cyclePickupIntent || g.session.count("send") != 1 {
		t.Fatalf("pending: %+v %v", c, g.session.calls)
	}
	// A receipt read that waits for the server holds no wake lock either.
	g.session.set(func(a *fakeSessionAdapter) { a.receipt, a.receiptBlock = true, make(chan struct{}) })
	reading := make(chan struct{})
	go func() { g.s.cycleStep(context.Background()); close(reading) }()
	for g.session.count("receipt") < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if !g.s.wake.mu.TryLock() {
		t.Fatal("the wake lock is held while the receipt is read")
	}
	g.s.wake.mu.Unlock()
	close(g.session.receiptBlock)
	<-reading
	g.session.set(func(a *fakeSessionAdapter) { a.receiptBlock = nil })
	// The receipt advanced the cycle while the request still runs.
	if c = g.cycle(t, v.ID); c.State != cycleWaitingPickup {
		t.Fatalf("after receipt: %+v", c)
	}
	close(g.session.send)
	waitIdle(t, g.s, v.ID)
	sent := g.session.targets[0]
	if sent.SuccessorEpoch != "synthetic-created-1" || sent.State != cyclePickupIntent || sent.pickupAt == 0 {
		t.Fatalf("send target: %+v", sent)
	}
	// Without the pane's proof the report is refused and may be retried.
	revision := g.cycle(t, v.ID).Revision
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, "synthetic-created-1", v.ID, revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleDisplay) {
		t.Fatalf("report without display: %v", err)
	}
	g.session.set(func(a *fakeSessionAdapter) { a.shown = true })
	g.step(t) // the display proof is recorded once
	g.step(t)
	if events := cycleReasons(t, g.s, v.ID); events[len(events)-1] != cycleWaitingPickup+":"+pickupDisplayedReason ||
		count(t, g.s, `SELECT COUNT(*) FROM context_cycle_events WHERE reason=?`, pickupDisplayedReason) != 1 {
		t.Fatalf("display events: %v", events)
	}
	// The view has scrolled past the tool rows: the recorded proof holds while no other
	// session shows. The report names a revision it read before the proof's event.
	g.session.set(func(a *fakeSessionAdapter) { a.shown = false })
	done2, err := g.s.ContextCyclePickup(context.Background(), successor, "synthetic-created-1", v.ID, revision, g.name, "resumed", "synthetic restored work")
	if err != nil || done2.State != cycleComplete {
		t.Fatalf("report: %v %+v", err, done2)
	}
	// An identical retry stays valid after the pane changed.
	g.session.set(func(a *fakeSessionAdapter) { a.foreign = true })
	if again, err := g.s.ContextCyclePickup(context.Background(), successor, "synthetic-created-1", v.ID, revision, g.name, "resumed", "synthetic restored work"); err != nil || again.State != cycleComplete {
		t.Fatalf("retry: %v %+v", err, again)
	}
	if g.session.count("create") != 1 || g.session.count("select") != 1 || g.session.count("send") != 1 {
		t.Fatalf("input repeated: %v", g.session.calls)
	}
}

// reachWaitingPickup takes a fresh fixture to waiting_pickup with the receipt read back.
func reachWaitingPickup(t *testing.T) (*sessionFixture, *CycleView, Key) {
	t.Helper()
	g := newSessionFixture(t)
	v := g.ready(t)
	for range 4 {
		g.step(t)
	}
	waitIdle(t, g.s, v.ID)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleWaitingPickup {
		t.Fatalf("waiting: %+v %v", c, g.session.calls)
	}
	return g, v, g.successor(t)
}

// A current view that shows another session refuses the report, even after the recorded proof;
// a failed read proves nothing.
func TestSessionCycleDisplayRefusals(t *testing.T) {
	g, v, successor := reachWaitingPickup(t)
	g.session.set(func(a *fakeSessionAdapter) { a.shown = true })
	g.step(t)
	report := func() error {
		_, err := g.s.ContextCyclePickup(context.Background(), successor, "synthetic-created-1", v.ID, g.cycle(t, v.ID).Revision, g.name, "resumed", "synthetic")
		return err
	}
	g.session.set(func(a *fakeSessionAdapter) { a.shown, a.foreign = true, true })
	if err := report(); !errors.Is(err, ErrCycleDisplay) {
		t.Fatalf("foreign view: %v", err)
	}
	g.session.set(func(a *fakeSessionAdapter) { a.foreign, a.displayErr = false, errOpenCodeUnproven })
	if err := report(); !errors.Is(err, ErrCycleDisplay) {
		t.Fatalf("failed read: %v", err)
	}
	if c := g.cycle(t, v.ID); c.State != cycleWaitingPickup {
		t.Fatalf("a refused report changed the cycle: %+v", c)
	}
	g.session.set(func(a *fakeSessionAdapter) { a.displayErr = nil })
	if err := report(); err != nil {
		t.Fatalf("report: %v", err)
	}
}

// Without the display proof, the stage deadline and a Disable end the cycle uncertain; with
// it, the deadline ends it failed as for any adapter.
func TestSessionCyclePendingDisplayOutcome(t *testing.T) {
	g, v, _ := reachWaitingPickup(t)
	g.clock = g.clock.Add(cycleReceiptWait + time.Second)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "waiting_pickup_timeout" {
		t.Fatalf("deadline without display: %+v", c)
	}
	h, w, _ := reachWaitingPickup(t)
	h.disable(t)
	h.step(t)
	if c := h.cycle(t, w.ID); c.State != cycleUncertain {
		t.Fatalf("Disable without display: %+v", c)
	}
	k, x, _ := reachWaitingPickup(t)
	// The proof's same-state event keeps the stage's deadline.
	deadline := k.cycle(t, x.ID).StageDeadline
	k.clock = k.clock.Add(time.Minute)
	k.session.set(func(a *fakeSessionAdapter) { a.shown = true })
	k.step(t)
	if c := k.cycle(t, x.ID); c.StageDeadline != deadline || c.Reason != pickupDisplayedReason {
		t.Fatalf("deadline moved: %d to %d (%s)", deadline, c.StageDeadline, c.Reason)
	}
	k.clock = k.clock.Add(cycleReceiptWait + time.Second)
	k.step(t)
	if c := k.cycle(t, x.ID); c.State != cycleFailed {
		t.Fatalf("deadline with display: %+v", c)
	}
}

// reopen replaces the fixture's store with a new one on the same state, as a restarted daemon.
func (g *sessionFixture) reopen(t *testing.T) {
	t.Helper()
	g.s.cycleWork.stop()
	g.s.db.Close()
	reopened, err := openStore(g.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.cycleWork.stop(); reopened.db.Close() })
	reopened.now = g.s.now
	g.s = reopened
}

// A restart at each boundary proves what was recorded or ends uncertain; no create, select or
// pickup is sent again.
func TestSessionCycleRestartBoundaries(t *testing.T) {
	// A create whose response was lost: no ID is recorded, nothing is created again.
	g := newSessionFixture(t)
	g.session.createErr = errOpenCodeServer
	v := g.ready(t)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != errOpenCodeServer.Error() || c.SuccessorEpoch != "" {
		t.Fatalf("lost create: %+v", c)
	}
	// A restart in clear_intent: no recorded ID proves nothing.
	g = newSessionFixture(t)
	v = g.ready(t)
	if _, err := g.s.advanceCycle(context.Background(), v.ID, v.Revision, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	g.reopen(t)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "reset_unproven" || g.session.count("create") != 0 {
		t.Fatalf("restart in clear_intent: %+v %v", c, g.session.calls)
	}
	// A restart after the select's intent: the select is not sent again; the proof decides.
	g = newSessionFixture(t)
	v = g.ready(t)
	g.session.proven = false
	g.step(t)
	g.step(t)
	g.reopen(t)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleClearReceived || g.session.count("select") != 1 {
		t.Fatalf("restart after select: %+v %v", c, g.session.calls)
	}
	g.session.set(func(a *fakeSessionAdapter) { a.proven = true })
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleResetVerified || g.session.count("select") != 1 {
		t.Fatalf("proof after restart: %+v %v", c, g.session.calls)
	}
	// An unproven reset ends uncertain at its deadline, also after a select that OpenCode
	// accepted but did not apply.
	g = newSessionFixture(t)
	v = g.ready(t)
	g.session.proven = false
	g.step(t)
	g.step(t)
	g.clock = g.clock.Add(cycleReceiptWait + time.Second)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "reset_unproven" || g.session.count("select") != 1 {
		t.Fatalf("unproven reset: %+v %v", c, g.session.calls)
	}
	// A select that the host refused is uncertain.
	g = newSessionFixture(t)
	g.session.selected = false
	v = g.ready(t)
	g.step(t)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "select_unreceived" {
		t.Fatalf("refused select: %+v", c)
	}
	// A restart in pickup_intent: a receipt advances the cycle, none ends it uncertain; the
	// pickup is never sent again.
	for _, received := range []bool{true, false} {
		g = newSessionFixture(t)
		v = g.ready(t)
		g.step(t)
		g.step(t)
		g.step(t)
		c := g.cycle(t, v.ID)
		if _, err := g.s.advanceCycle(context.Background(), v.ID, c.Revision, cycleResetVerified, cyclePickupIntent, "", nil, nil); err != nil {
			t.Fatal(err)
		}
		g.reopen(t)
		g.session.receipt = received
		g.step(t)
		want := map[bool]string{true: cycleWaitingPickup, false: cycleUncertain}[received]
		if c = g.cycle(t, v.ID); c.State != want || g.session.count("send") != 0 {
			t.Fatalf("restart in pickup_intent (receipt %v): %+v %v", received, c, g.session.calls)
		}
		if !received && c.Reason != "pickup_unreceipted" {
			t.Fatalf("reason: %+v", c)
		}
	}
	// A worker that ended without a receipt: uncertain. A failed read is tried again until the
	// stage deadline.
	g = newSessionFixture(t)
	g.session.receipt, g.session.receiptErr = false, errOpenCodeServer
	v = g.ready(t)
	for range 4 {
		g.step(t)
	}
	waitIdle(t, g.s, v.ID)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cyclePickupIntent {
		t.Fatalf("failed read: %+v", c)
	}
	g.session.set(func(a *fakeSessionAdapter) { a.receiptErr = nil })
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "pickup_unreceipted" || g.session.count("send") != 1 {
		t.Fatalf("no receipt: %+v %v", c, g.session.calls)
	}
}

// A changed Enable between the intent and the send stops the send at its fence; the cycle
// then ends uncertain without a receipt.
func TestSessionCycleSendFence(t *testing.T) {
	g := newSessionFixture(t)
	g.session.beforeSend = make(chan struct{})
	g.session.receipt = false
	v := g.ready(t)
	for range 4 {
		g.step(t)
	}
	if c := g.cycle(t, v.ID); c.State != cyclePickupIntent {
		t.Fatalf("dispatch: %+v", c)
	}
	g.disable(t)
	close(g.session.beforeSend)
	waitIdle(t, g.s, v.ID)
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleUncertain || g.session.count("send refused") != 1 || g.session.count("send") != 1 {
		t.Fatalf("fence: %+v %v", c, g.session.calls)
	}
}

// The daemon's stop cancels a pending pickup request and waits for its worker.
func TestSessionCycleWorkerStop(t *testing.T) {
	g := newSessionFixture(t)
	g.session.send, g.session.sending = make(chan struct{}), make(chan struct{})
	v := g.ready(t)
	for range 4 {
		g.step(t)
	}
	waitSent(t, g.session)
	stopped := make(chan struct{})
	go func() { g.s.cycleWork.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the worker")
	}
	g.session.mu.Lock()
	err := g.session.sendCtxErr
	g.session.mu.Unlock()
	if !errors.Is(err, context.Canceled) || g.s.cycleWork.running(v.ID) || g.s.cycleWork.start("another", func(context.Context) {}) {
		t.Fatalf("after stop: %v", err)
	}
	if _, err := os.Stat(g.root); err != nil {
		t.Fatal(err)
	}
}

// At most maxCycleWorkers pickups run at once; a cycle waits in reset_verified, with nothing
// recorded, until one is free.
func TestSessionCycleWorkerBound(t *testing.T) {
	g := newSessionFixture(t)
	v := g.ready(t)
	g.step(t)
	g.step(t)
	g.step(t)
	release := make(chan struct{})
	for i := range maxCycleWorkers {
		if !g.s.cycleWork.start("busy-"+string(rune('a'+i)), func(context.Context) { <-release }) {
			t.Fatal("a free worker did not start")
		}
	}
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cycleResetVerified || g.session.count("send") != 0 {
		t.Fatalf("no free worker: %+v %v", c, g.session.calls)
	}
	close(release)
	for g.s.cycleWork.running("busy-a") || !g.s.cycleWork.free() {
		time.Sleep(5 * time.Millisecond)
	}
	g.step(t)
	if c := g.cycle(t, v.ID); c.State != cyclePickupIntent {
		t.Fatalf("after release: %+v", c)
	}
}
