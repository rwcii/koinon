package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// busyCompactor is the synthetic compaction adapter with the read-only busy check (#758).
type busyCompactor struct {
	*fakeCompactor
	busy string
}

func (b *busyCompactor) CompactBusy(context.Context, CycleTarget) string {
	b.record("busy check")
	return b.busy
}

// A waiting compaction whose busy check gives a code keeps waiting with that code and runs no
// Inspect; "" runs Inspect, which alone admits the input. The wait deadline still ends it.
func TestCompactionBusyCheckDefersInspect(t *testing.T) {
	f := newCompactFixture(t)
	busy := &busyCompactor{fakeCompactor: f.compactor, busy: "input_not_empty"}
	cycleAdapters = map[string]CycleAdapter{"codex": busy}
	f.now(t, 800)
	f.step(t)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactWaiting || f.compactor.count("inspect") != 0 || f.compactor.count("busy check") == 0 {
		t.Fatalf("busy pane: %+v %v", v, f.compactor.calls)
	}
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCancelled || v.Reason != "compact_waiting_timeout.input_not_empty" || f.compactor.count("enter") != 0 {
		t.Fatalf("wait timeout: %+v %v", v, f.compactor.calls)
	}

	f.now(t, 800)
	busy.busy = ""
	f.adapter.pane = PaneState{Seen: "turn_running"}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactWaiting || f.compactor.count("inspect") != 1 || f.compactor.count("enter") != 0 {
		t.Fatalf("Inspect busy: %+v %v", v, f.compactor.calls)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleCompactSent || f.compactor.count("inspect") != 2 {
		t.Fatalf("idle: %+v %v", v, f.compactor.calls)
	}
}

// busyTerminal is the synthetic Codex terminal with counted process-table reads and settable
// tmux answers for the busy check.
type busyTerminal struct {
	*codexTerminal
	groups  int
	inMode  string
	modeErr error
	capture error
	working bool
}

func (b *busyTerminal) target(t *testing.T) CycleTarget {
	target := b.codexTerminal.target(t)
	read := target.tools.processGroups
	target.tools.processGroups = func() (map[int]platform.ProcessGroup, error) {
		b.groups++
		return read()
	}
	target.tools.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		switch {
		case args[0] == "display-message" && args[len(args)-1] == "#{pane_in_mode}":
			return b.inMode, b.modeErr
		case args[0] == "capture-pane" && b.capture != nil:
			return "", b.capture
		case args[0] == "capture-pane" && b.working:
			data, err := os.ReadFile(filepath.Join("testdata", "codex-screens", "cycle-working.ansi"))
			return string(data), err
		}
		return b.codexTerminal.tmux(ctx, socket, args...)
	}
	return target
}

// The Codex busy check reads no whole process table, sends no key and calls no fence. It gives
// a code only for a proven pane, out of copy mode, whose screen shows no dialog and a draft, a
// running turn or no composer; every other case gives "" and leaves the decision to Inspect,
// whose full proof reads the table.
func TestCodexCompactBusy(t *testing.T) {
	ctx := context.Background()
	term := &busyTerminal{codexTerminal: newCodexTerminal(t), inMode: "0\n"}
	target := term.target(t)
	check := func(want string) {
		t.Helper()
		if got := (codexCycle{}).CompactBusy(ctx, target); got != want {
			t.Fatalf("busy check %q, want %q", got, want)
		}
		if term.groups != 0 || len(term.sent()) != 0 || term.fences != 0 {
			t.Fatalf("%d table reads, keys %q, %d fences", term.groups, term.sent(), term.fences)
		}
	}
	term.input = "draft"
	check("input_not_empty")
	term.input, term.working = "", true
	check("turn_running")
	term.working = false
	check("")
	term.dialog, term.options = []string{"  Would you like to run the following command?", "› 1. Yes, proceed (y)", "  2. No (esc)", "  Press enter to confirm or esc to cancel"}, []string{"1", "2"}
	check("")
	term.dialog, term.options = nil, nil

	// A draft in copy mode, behind a failed mode or capture read, or in a pane whose host
	// changed, goes to Inspect.
	term.input = "draft"
	term.inMode = "1\n"
	check("")
	term.inMode, term.modeErr = "0\n", errors.New("synthetic tmux failure")
	check("")
	term.modeErr, term.capture = nil, errors.New("synthetic tmux failure")
	check("")
	term.capture = nil
	moved := target
	moved.tools.processStart = func(int) (int64, error) { return 99, nil }
	if got := (codexCycle{}).CompactBusy(ctx, moved); got != "" {
		t.Fatalf("changed host: %q", got)
	}
	check("input_not_empty")

	// The idle pane: the busy check gives "", and Inspect proves the foreground with one
	// table read before it reports the pane idle.
	term.input = ""
	check("")
	if pane, err := (codexCycle{}).Inspect(ctx, target, term.fence); err != nil || !pane.Idle || term.groups != 1 {
		t.Fatalf("Inspect: %+v %v after %d table reads", pane, err, term.groups)
	}
}
