package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Generic controller rules that the Codex adapter needs (#196, chunk 04): the epoch is proven
// again right before the pickup input, a dialog during the pickup wait is shown, and the
// report may arrive during it. Succession gives the address to the thread that a cycle's reset
// proved, and to no other candidate.

// cycleToReset runs the fixture's cycle to reset_verified with epoch 2 proven.
func cycleToReset(t *testing.T, f *cycleFixture) (CycleView, string) {
	t.Helper()
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleResetVerified {
		t.Fatalf("reset: %+v", v)
	}
	return *v, name
}

// Another epoch, or none, right before the pickup ends the cycle without the pickup input.
func TestContextCycleEpochRecheckBeforePickup(t *testing.T) {
	for _, epoch := range []string{"synthetic-epoch-3", ""} {
		f := newCycleFixture(t)
		cycleToReset(t, f)
		f.adapter.epoch = epoch
		f.step(t)
		if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "epoch_changed" || f.adapter.count("pickup") != 0 {
			t.Fatalf("epoch %q: %+v %v", epoch, v, f.adapter.calls)
		}
	}
	// The same epoch enters the pickup.
	f := newCycleFixture(t)
	cycleToReset(t, f)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleWaitingPickup || f.adapter.count("pickup") != 1 {
		t.Fatalf("same epoch: %+v", v)
	}
}

// A dialog in waiting_pickup moves the cycle to waiting_prompt from waiting_pickup. The wait
// goes on when the dialog is gone or the maintainer chose an option, for the pickup's next
// dialog or its report (#520), and the report completes the cycle during the wait.
func TestContextCyclePromptInPickupWait(t *testing.T) {
	ctx := context.Background()
	waiting := func(t *testing.T) (*cycleFixture, CycleView, string) {
		f := newCycleFixture(t)
		_, name := cycleToReset(t, f)
		f.step(t)
		// The pickup wait only reads the dialog: no Inspect, which may send a key.
		inspected := f.adapter.count("inspect")
		f.step(t)
		if f.adapter.count("inspect") != inspected || f.adapter.count("dialog") == 0 {
			t.Fatalf("pickup wait calls: %v", f.adapter.calls)
		}
		f.adapter.pane = PaneState{Prompt: "native_dialog"}
		f.adapter.dialog = CycleDialog{Lines: []string{"synthetic approval"}, Options: []string{"1", "2"}, Digest: "synthetic-digest"}
		f.step(t)
		v := *f.status(t, f.k)
		if v.State != cycleWaitingPrompt || v.PromptFrom != cycleWaitingPickup || v.Reason != "native_dialog" {
			t.Fatalf("dialog in the pickup wait: %+v", v)
		}
		return f, v, name
	}
	f, v, _ := waiting(t)
	f.adapter.pane = PaneState{Idle: true}
	f.adapter.dialog = CycleDialog{}
	inspected := f.adapter.count("inspect")
	f.step(t)
	if now := f.status(t, f.k); now.State != cycleWaitingPrompt || now.Revision != v.Revision || now.Reason != "native_dialog" ||
		f.adapter.count("pickup") != 1 || f.adapter.count("inspect") != inspected {
		t.Fatalf("resolved in the pane: %+v %v", now, f.adapter.calls)
	}
	f, v, _ = waiting(t)
	if err := f.s.AnswerCycleDialog(ctx, v.ID, v.Revision, "synthetic-digest", "1"); err != nil {
		t.Fatal(err)
	}
	if now := f.status(t, f.k); now.State != cycleWaitingPrompt || now.PromptFrom != cycleWaitingPickup || now.Reason != "prompt_answered" ||
		now.StageDeadline != v.StageDeadline || f.adapter.count("choose 1") != 1 {
		t.Fatalf("answered on the dashboard: %+v", now)
	}
	// The report names a revision that the successor read while the cycle awaited it: the
	// waiting_pickup before the dialog, or the dialog's own. After the completion, the same
	// report is a retry.
	for _, read := range []int64{0, 1} {
		f, v, name := waiting(t)
		read = v.Revision - read
		done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, read, name, "resumed", "synthetic restored work")
		if err != nil || done.State != cycleComplete {
			t.Fatalf("report at revision %d during the dialog wait: %v %+v", read, err, done)
		}
		if again, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, read, name, "resumed", "synthetic restored work"); err != nil || again.State != cycleComplete {
			t.Fatalf("retry: %v %+v", err, again)
		}
	}
	// The approved call is the report: it completes the cycle before the answer records the
	// return to the wait, and the dashboard answer still succeeds.
	f, v, name := waiting(t)
	var report error
	f.adapter.afterChoose = func() {
		_, report = f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision-1, name, "resumed", "synthetic")
	}
	if err := f.s.AnswerCycleDialog(ctx, v.ID, v.Revision, "synthetic-digest", "1"); err != nil || report != nil {
		t.Fatalf("answer that approved the report: %v, report %v", err, report)
	}
	if got := f.status(t, f.k); got.State != cycleComplete {
		t.Fatalf("after the approved report: %+v", got)
	}
	// #389: the approved report read the cycle at prompt_intent, and the answer recorded the
	// return to the wait before the report's write. The report completes the cycle, and
	// the dashboard answer still succeeds.
	f, v, name = waiting(t)
	f.adapter.afterChoose = func() {
		f.s.pickupWrite = func() {
			f.s.pickupWrite = nil
			now := f.status(t, f.k)
			if now.State != cyclePromptIntent {
				t.Fatalf("report read: %+v", now)
			}
			if _, err := f.s.advanceCycle(ctx, now.ID, now.Revision, cyclePromptIntent, cycleWaitingPrompt, "prompt_answered", nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		_, report = f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision-1, name, "resumed", "synthetic")
	}
	if err := f.s.AnswerCycleDialog(ctx, v.ID, v.Revision, "synthetic-digest", "1"); err != nil || report != nil {
		t.Fatalf("answer recorded before the approved report's write: %v, report %v", err, report)
	}
	if got := f.status(t, f.k); got.State != cycleComplete || got.Reason != "resumed" {
		t.Fatalf("after the late write: %+v", got)
	}
	// A cycle that ended between the report's read and its write refuses the report.
	f, v, name = waiting(t)
	f.s.pickupWrite = func() {
		f.s.pickupWrite = nil
		if _, err := f.s.endCycle(ctx, v.ID, v.Revision, cycleCancelled, "disabled"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision-1, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("report after the cycle ended: %v", err)
	}
	if got := f.status(t, f.k); got.State != cycleCancelled {
		t.Fatalf("ended cycle: %+v", got)
	}
	// The approved report can also arrive after the wait went on, after a dashboard answer or
	// the native session's own: the revision it read still names it. The revisions of the
	// pickup input and of the stages before the reset do not (#588).
	for _, answer := range []string{"dashboard", "pane"} {
		f, v, name = waiting(t)
		read := v.Revision - 1 // the waiting_pickup that the successor read
		if answer == "dashboard" {
			if err := f.s.AnswerCycleDialog(ctx, v.ID, v.Revision, "synthetic-digest", "1"); err != nil {
				t.Fatal(err)
			}
		} else {
			f.adapter.pane = PaneState{Idle: true}
			f.adapter.dialog = CycleDialog{}
			f.step(t)
		}
		if now := f.status(t, f.k); now.State != cycleWaitingPrompt || now.PromptFrom != cycleWaitingPickup || now.Revision == read {
			t.Fatalf("%s: returned %+v", answer, now)
		}
		for _, stale := range []int64{read - 1, read - 3} { // pickup_intent, clear_received
			if _, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, stale, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
				t.Fatalf("%s: revision %d: %v", answer, stale, err)
			}
		}
		if done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, read, name, "resumed", "synthetic"); err != nil || done.State != cycleComplete {
			t.Fatalf("%s: delayed approved report: %v %+v", answer, err, done)
		}
	}
	// A dialog wait that began before the reset is no pickup wait.
	f = newCycleFixture(t)
	f.adapter.pane = PaneState{Prompt: "native_dialog"}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v = *f.status(t, f.k)
	name = f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	f.s.ContextCycleReady(ctx, f.k, "synthetic-epoch-1", v.ID, 1, name)
	f.step(t)
	if v = *f.status(t, f.k); v.State != cycleWaitingPrompt || v.PromptFrom != cycleReady {
		t.Fatalf("before the clear: %+v", v)
	}
	// The refusal names the stage and says that the reset is not verified (#588).
	if _, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic"); errorCode(err) != ErrCyclePickupNotReady.Code ||
		!strings.Contains(err.Error(), "the cycle is at waiting_prompt (from ready); the reset is not verified yet;") {
		t.Fatalf("report before the reset: %v", err)
	}
}

// resetLink is one succession case: a holder thread-1 in host 100 and pane %1, an unfinished
// cycle of its address in the given stage that proved successor, and a candidate's arrival.
type resetLink struct {
	state, promptFrom, successor, requester, address string
	enabled                                          bool
	enableRevision                                   int64
	stageLeft, cycleLeft                             time.Duration
	finished                                         bool
	noCycle                                          bool
	candidate                                        arrival
	fenced, restarted                                bool
}

func runResetLink(t *testing.T, c resetLink) Session {
	t.Helper()
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 8)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	advance(5 * time.Second) // inside the holder guard
	now := s.now().UnixMilli()
	if c.requester == "" {
		c.requester = "thread-1"
	}
	if c.address == "" {
		c.address = held.Address
	}
	if !c.noCycle {
		finished, outcome := int64(0), ""
		if c.finished {
			finished, outcome = now, cycleComplete
		}
		if _, err := s.db.Exec(`INSERT INTO context_cycles(cycle_id,credit,held,revision,state,prompt_from,family,session_id,peer,address,
			host_pid,host_start,tmux_socket,tmux_pane,enable_revision,epoch,mode,threshold,used_tokens,limit_tokens,source_at,confirmed_at,
			message_seq,created_at,updated_at,stage_deadline,cycle_deadline,successor_epoch,outcome,events,finished_at)
			VALUES('synthetic-cycle',0,1,5,?,?,'codex',?,?,?,100,7,'/synthetic/tmux/default','%1',1,'thread-1','handoff_cycle',75,900,1000,?,?,1,?,?,?,?,?,?,5,?)`,
			c.state, c.promptFrom, c.requester, held.Name, c.address, now, now, now, now, now+c.stageLeft.Milliseconds(), now+c.cycleLeft.Milliseconds(),
			c.successor, outcome, finished); err != nil {
			t.Fatal(err)
		}
		enabled := 0
		if c.enabled {
			enabled = 1
		}
		if _, err := s.db.Exec(`INSERT INTO context_enables(family,session_id,enabled,revision,session_revision,address,host_pid,host_start,
			tmux_socket,tmux_pane,updated_at) VALUES('codex','thread-1',?,?,1,?,100,7,'/synthetic/tmux/default','%1',?)`,
			enabled, c.enableRevision, held.Address, now); err != nil {
			t.Fatal(err)
		}
	}
	if c.fenced {
		if _, err := s.db.Exec(`INSERT INTO participant_fences(address,family,session_id,reason,at) VALUES(?,?,?,?,?)`,
			held.Address, "codex", c.candidate.id, "synthetic", now); err != nil {
			t.Fatal(err)
		}
	}
	if c.restarted {
		s.calls.mu.Lock()
		s.calls.since = now // a daemon start inside the guard
		s.calls.mu.Unlock()
	}
	c.candidate.family, c.candidate.repo, c.candidate.toolCall = "codex", repo, true
	return arrive(t, s, c.candidate)
}

// The cycle's proven successor takes the address with cycle_reset inside the holder guard;
// every other candidate keeps the ordinary rules.
func TestSuccessionCycleReset(t *testing.T) {
	proven := resetLink{state: cycleResetVerified, successor: "thread-2", enabled: true, enableRevision: 1, stageLeft: time.Minute,
		cycleLeft: 10 * time.Minute, candidate: arrival{id: "thread-2", pid: 100, pane: "%1"}}
	for name, mutate := range map[string]func(*resetLink){
		"reset_verified":               func(*resetLink) {},
		"pickup_intent":                func(c *resetLink) { c.state = cyclePickupIntent },
		"waiting_pickup":               func(c *resetLink) { c.state = cycleWaitingPickup },
		"dialog from the pickup wait":  func(c *resetLink) { c.state, c.promptFrom = cycleWaitingPrompt, cycleWaitingPickup },
		"dialog intent from reset":     func(c *resetLink) { c.state, c.promptFrom = cyclePromptIntent, cycleResetVerified },
		"after a restart inside guard": func(c *resetLink) { c.restarted = true },
	} {
		c := proven
		mutate(&c)
		got := runResetLink(t, c)
		if !got.HoldsAddress {
			t.Fatalf("%s: %+v", name, got.Succession)
		}
		wantSuccession(t, got, "succeeded", "cycle_reset", got.Succession.Holder)
	}
	for name, c := range map[string]struct {
		mutate func(*resetLink)
		reason string
	}{
		"another thread":           {func(c *resetLink) { c.candidate.id = "thread-3" }, "holder_active"},
		"another pane":             {func(c *resetLink) { c.candidate.pane = "%2" }, "holder_active"},
		"another host":             {func(c *resetLink) { c.candidate.pid = 200 }, "host_running"},
		"no cycle":                 {func(c *resetLink) { c.noCycle = true }, "holder_active"},
		"finished cycle":           {func(c *resetLink) { c.finished, c.state = true, cycleComplete }, "holder_active"},
		"stage deadline passed":    {func(c *resetLink) { c.stageLeft = -time.Second }, "holder_active"},
		"cycle deadline passed":    {func(c *resetLink) { c.cycleLeft = -time.Second }, "holder_active"},
		"Enable off":               {func(c *resetLink) { c.enabled = false }, "holder_active"},
		"Enable changed":           {func(c *resetLink) { c.enableRevision = 2 }, "holder_active"},
		"another address":          {func(c *resetLink) { c.address = "codex-other" }, "holder_active"},
		"another requester":        {func(c *resetLink) { c.requester = "thread-9" }, "holder_active"},
		"ready":                    {func(c *resetLink) { c.state = cycleReady }, "holder_active"},
		"clear_intent":             {func(c *resetLink) { c.state = cycleClearIntent }, "holder_active"},
		"clear_received":           {func(c *resetLink) { c.state = cycleClearReceived }, "holder_active"},
		"waiting_ready":            {func(c *resetLink) { c.state = cycleWaitingReady }, "holder_active"},
		"dialog from before reset": {func(c *resetLink) { c.state, c.promptFrom = cycleWaitingPrompt, cycleReady }, "holder_active"},
		"no proven successor":      {func(c *resetLink) { c.successor = "" }, "holder_active"},
		"fenced candidate":         {func(c *resetLink) { c.fenced = true }, "fenced"},
		"subagent":                 {func(c *resetLink) { c.candidate.subagent = true }, "subagent"},
	} {
		link := proven
		c.mutate(&link)
		got := runResetLink(t, link)
		if got.HoldsAddress || got.Succession == nil || got.Succession.Reason != c.reason {
			t.Fatalf("%s: holds %v, %+v", name, got.HoldsAddress, got.Succession)
		}
	}
}
