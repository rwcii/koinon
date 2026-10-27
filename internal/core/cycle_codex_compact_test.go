package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The Codex compaction input (#509) against the synthetic terminal. Nothing here starts Codex
// or reaches a real tmux server.

// The compaction's Probe needs the platform, the thread's rollout and the pane, but neither
// the pickup skill nor the participant address.
func TestCodexCompactProbe(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	target.Holds = false
	if got := (codexCycle{}).CompactProbe(ctx, target); got != (CycleProbe{Entry: "/compact"}) {
		t.Fatalf("probe: %+v", got)
	}
	if got := (codexCycle{}).Probe(ctx, target); got.Reason == "" {
		t.Fatalf("the handoff probe passed without the address and the skill: %+v", got)
	}
	target.SessionID = "thread-z"
	if got := (codexCycle{}).CompactProbe(ctx, target); got.Reason != "native_registry_unavailable" {
		t.Fatalf("no rollout: %+v", got)
	}
}

// /compact is typed only into an empty composer of an ended turn, read back and submitted
// with one Enter; the empty composer is the receipt. A draft or a running turn gets no key, and
// a check that fails before the Enter erases the typed command and reports it unsubmitted.
func TestCodexCompact(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	submitted, received, err := (codexCycle{}).Compact(ctx, target, term.fence)
	if !submitted || !received || err != nil || !slices.Equal(term.sent(), []string{"-l /compact", "Enter"}) {
		t.Fatalf("compact: %v %v %v %q", submitted, received, err, term.sent())
	}

	term = newCodexTerminal(t)
	target = term.target(t)
	term.input = "maintainer draft"
	if submitted, _, err := (codexCycle{}).Compact(ctx, target, term.fence); submitted || !errors.Is(err, errCodexNotReady) || len(term.sent()) != 0 || term.input != "maintainer draft" {
		t.Fatalf("over a draft: %v %v %q %q", submitted, err, term.sent(), term.input)
	}
	term.input = ""
	term.rollout("thread-a", "0.164.0", "task_started")
	if submitted, _, err := (codexCycle{}).Compact(ctx, target, term.fence); submitted || err == nil || len(term.sent()) != 0 {
		t.Fatalf("running turn: %v %v %q", submitted, err, term.sent())
	}

	// The fence before the Enter refuses: the command is erased and nothing is submitted.
	term = newCodexTerminal(t)
	target = term.target(t)
	term.failAt = 2
	submitted, received, err = (codexCycle{}).Compact(ctx, target, term.fence)
	keys := term.sent()
	if submitted || received || !errors.Is(err, ErrCycleChanged) || len(keys) != 2 || keys[0] != "-l /compact" ||
		!strings.HasPrefix(keys[1], "BSpace") || term.input != "" {
		t.Fatalf("fenced Enter: %v %v %v %q %q", submitted, received, err, keys, term.input)
	}
}
