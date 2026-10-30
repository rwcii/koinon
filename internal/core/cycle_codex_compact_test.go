package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
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

// rolloutMark is the caught-up mark of the terminal's thread-a rollout as it is now, and its
// path.
func (c *codexTerminal) rolloutMark(t *testing.T) (*RolloutEnd, string) {
	t.Helper()
	path := filepath.Join(c.codex, "sessions", "2026", "10", "14", "rollout-2026-10-14T00-00-00-thread-a.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	device, inode, size, err := platform.FileIdentity(f)
	if err != nil {
		t.Fatal(err)
	}
	return &RolloutEnd{Rollout: RolloutFile(device, inode), Size: size, Turn: "turn-1"}, path
}

// With a chosen caught-up mark (#564), /compact is typed only while the rollout, read at the
// first key, is the same file at the same size with its last turn ended. A grown, replaced,
// removed or unreadable rollout gets no key, also when it changes at the fence right before the
// check.
func TestCodexCompactNeedsUnchangedRollout(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	target.RolloutEnd, _ = term.rolloutMark(t)
	if submitted, received, err := (codexCycle{}).Compact(ctx, target, term.fence); !submitted || !received || err != nil ||
		!slices.Equal(term.sent(), []string{"-l /compact", "Enter"}) {
		t.Fatalf("unchanged rollout: %v %v %v %q", submitted, received, err, term.sent())
	}

	appendRecord := func(path string) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(`{"type":"response_item","payload":{}}` + "\n")
		f.Close()
	}
	cases := map[string]func(term *codexTerminal, path string){
		"grown": func(_ *codexTerminal, path string) { appendRecord(path) },
		"grown at the fence": func(term *codexTerminal, path string) {
			term.atFence = func() { term.atFence = nil; appendRecord(path) }
		},
		"replaced": func(_ *codexTerminal, path string) {
			// The same records in another file: only the identity differs.
			data, _ := os.ReadFile(path)
			os.WriteFile(path+".new", data, 0o600)
			os.Rename(path+".new", path)
		},
		"removed":    func(_ *codexTerminal, path string) { os.Remove(path) },
		"unreadable": func(_ *codexTerminal, path string) { os.Chmod(path, 0) },
	}
	for name, change := range cases {
		term := newCodexTerminal(t)
		target := term.target(t)
		var path string
		target.RolloutEnd, path = term.rolloutMark(t)
		change(term, path)
		if f, err := os.Open(path); name == "unreadable" && err == nil {
			f.Close()
			continue // this user reads files without read permission
		}
		if submitted, _, err := (codexCycle{}).Compact(ctx, target, term.fence); submitted || !errors.Is(err, errCodexNotReady) || len(term.sent()) != 0 {
			t.Fatalf("%s: %v %v %q", name, submitted, err, term.sent())
		}
	}
	// The codes name what changed.
	mark, path := newCodexTerminal(t).rolloutMark(t)
	if got := codexRolloutUnchanged(path, *mark); got != "" {
		t.Fatalf("unchanged: %q", got)
	}
	appendRecord(path)
	if got := codexRolloutUnchanged(path, *mark); got != "rollout_changed" {
		t.Fatalf("grown: %q", got)
	}
	if got := codexRolloutUnchanged(path+".missing", *mark); got != "rollout_unreadable" {
		t.Fatalf("missing: %q", got)
	}
	term = newCodexTerminal(t)
	term.rollout("thread-a", "0.162.0", "task_started")
	mark, path = term.rolloutMark(t)
	if got := codexRolloutUnchanged(path, *mark); got != "turn_not_ended" {
		t.Fatalf("running turn: %q", got)
	}
}
