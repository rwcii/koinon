package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// codexHost is a synthetic launched Codex host 100 with child processes: koinon mcp servers
// and other commands, each with its wall-clock start time.
type codexHost struct {
	mu       sync.Mutex
	children map[int]codexChild
	failing  bool
}

type codexChild struct {
	executable string
	args       []string
	started    time.Time
	err        error
}

func (c *codexHost) parents() (map[int]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failing {
		return nil, errors.New("synthetic process table error")
	}
	parents := map[int]int{100: 1, 900: 1}
	for pid := range c.children {
		parents[pid] = 100
	}
	return parents, nil
}

func (c *codexHost) command(pid int) (string, []string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	child, ok := c.children[pid]
	if !ok {
		return "", nil, platform.ErrProcessGone
	}
	return child.executable, child.args, nil
}

func (c *codexHost) startTime(pid int) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	child, ok := c.children[pid]
	if !ok {
		return time.Time{}, platform.ErrProcessGone
	}
	return child.started, child.err
}

func (c *codexHost) add(pid int, child codexChild) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.children[pid] = child
}

func server(started time.Time) codexChild {
	return codexChild{"/opt/koinon/bin/koinon", []string{"koinon", "mcp"}, started, nil}
}

// The queue wake of a launched Codex holder waits while a koinon mcp server under its host
// started after the holder's latest proof; a tool call, a later rollout event and a holder
// change end the wait (#275).
func TestCodexQueueWaitsWhileTheHostMayShowAnotherThread(t *testing.T) {
	s, h, advance := successionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	host := &codexHost{children: map[int]codexChild{}}
	s.processParents, s.processCommand, s.processStartTime = host.parents, host.command, host.startTime
	launch, err := s.CreateLaunch(ctx, LaunchTarget{Family: "codex", Directory: repo, CLI: "/synthetic/codex", HostPID: 100})
	if err != nil {
		t.Fatal(err)
	}
	register := func(id string, clear bool) Session {
		t.Helper()
		got, err := s.Register(ctx, Registration{Family: "codex", ID: id, Repository: repo, Directory: repo, TTLSeconds: 900,
			LaunchID: launch, Ancestors: []int{100}, ToolCall: true, Clear: clear})
		if err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		return got
	}
	sessionNotice := `Koinon inbox "codex":"thread-1" has waiting sequence range 1-1. Read your Koinon inbox.`
	wake := func(id, want string) {
		t.Helper()
		for _, notice := range []string{codexTestNotice, sessionNotice} {
			if got := s.providerWake(ctx, sessionOf(t, s, "codex", id), notice); got != waiting(want) {
				t.Fatalf("%s: %v, want %s", id, got, want)
			}
		}
	}
	// The holder's own server started before its registration; other commands never count,
	// whenever they started.
	host.add(501, server(s.now().Add(-10*time.Second)))
	holder := register("thread-1", false)
	if !holder.HoldsAddress {
		t.Fatalf("not the holder: %+v", holder)
	}
	host.add(601, codexChild{"/bin/sh", []string{"sh", "-c", "synthetic"}, s.now().Add(time.Hour), nil})
	host.add(602, codexChild{"/opt/koinon/bin/koinon", []string{"koinon", "serve"}, s.now().Add(time.Hour), nil})
	wake("thread-1", "cli_unavailable")

	// A /clear or a sub-agent: a new server and no call yet. A replaced binary still counts.
	advance(10 * time.Second)
	host.add(502, codexChild{"/opt/koinon/bin/koinon (deleted)", []string{"/opt/koinon/bin/koinon", "mcp"}, s.now(), nil})
	wake("thread-1", "codex_thread_unproven")
	advance(10 * time.Second)
	s.ToolCall(Key{"codex", "thread-1"})
	wake("thread-1", "cli_unavailable")

	// A start read up to a second before the proof, or at the same time, counts as later: the
	// read can be early by that much.
	for _, before := range []time.Duration{0, 999 * time.Millisecond, time.Second} {
		host.add(503, server(s.now().Add(-before)))
		wake("thread-1", "codex_thread_unproven")
	}
	host.add(503, server(s.now().Add(-time.Second-time.Millisecond)))
	wake("thread-1", "cli_unavailable")

	// A turn of the holder's thread that its rollout records after the start ends the wait,
	// as after a sub-agent start within that turn; an event within the margin does not.
	advance(10 * time.Second)
	host.add(504, server(s.now()))
	observe := func(at int64) {
		t.Helper()
		if err := s.Observe(ctx, Observation{Caller: Key{"codex", "thread-1"},
			Activity: &ObservedValue{Source: "codex_rollout", At: at, State: "idle"}}); err != nil {
			t.Fatal(err)
		}
	}
	observe(s.now().Add(rolloutMargin+startUncertainty).UnixMilli() - 1)
	wake("thread-1", "codex_thread_unproven")
	advance(10 * time.Second)
	observe(s.now().UnixMilli())
	wake("thread-1", "cli_unavailable")

	// Anything unreadable waits: the process table, a server's start, the host itself. A
	// child that ended during the scan does not.
	host.failing = true
	wake("thread-1", "codex_thread_unproven")
	host.failing = false
	host.add(505, codexChild{"/opt/koinon/bin/koinon", []string{"koinon", "mcp"}, time.Time{}, errors.New("synthetic")})
	wake("thread-1", "codex_thread_unproven")
	host.add(505, codexChild{"/opt/koinon/bin/koinon", []string{"koinon", "mcp"}, time.Time{}, platform.ErrProcessGone})
	wake("thread-1", "cli_unavailable")
	h.set(100, 8)
	wake("thread-1", "codex_thread_unproven")
	h.set(100, 7)

	// After a daemon restart no call or event is known: the first registration is the proof.
	s.calls.mu.Lock()
	s.calls.at, s.calls.since = nil, s.now().UnixMilli()
	s.calls.mu.Unlock()
	s.observed.mu.Lock()
	s.observed.byKey = nil
	s.observed.mu.Unlock()
	wake("thread-1", "codex_thread_unproven")
	observe(s.now().UnixMilli())
	wake("thread-1", "cli_unavailable")

	// A new thread's call within the guard is refused: the holder waits, the other thread,
	// which holds no address, keeps the queue.
	advance(10 * time.Second)
	host.add(506, server(s.now()))
	next := register("thread-2", false)
	wantSuccession(t, next, "refused", "holder_active", holder.Name)
	wake("thread-1", "codex_thread_unproven")
	wake("thread-2", "cli_unavailable")

	// The new thread's first turn after /clear takes the address (host_cleared).
	advance(time.Minute)
	host.add(507, server(s.now()))
	advance(10 * time.Second)
	cleared := register("thread-3", true)
	wantSuccession(t, cleared, "succeeded", "host_cleared", holder.Name)
	wake("thread-3", "cli_unavailable")
}
