package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// countedWake is the Codex pane wake with counted process-table reads and keys, and a capture
// that can show another screen in place of the pane's own for the next captures.
type countedWake struct {
	mu              sync.Mutex
	groups, keys    int
	fake            string // the screen that the next fakes captures show
	fakes, captures int
}

func (c *countedWake) install(f *codexFixture) {
	groups, tmux := f.s.processGroups, f.s.tmux
	f.s.processGroups = func() (map[int]platform.ProcessGroup, error) {
		c.mu.Lock()
		c.groups++
		c.mu.Unlock()
		return groups()
	}
	f.s.tmux = func(ctx context.Context, executable, socket string, args ...string) (string, error) {
		c.mu.Lock()
		switch args[0] {
		case "send-keys":
			c.keys++
		case "capture-pane":
			c.captures++
			if c.fakes > 0 {
				c.fakes--
				c.mu.Unlock()
				return c.fake, nil
			}
		}
		c.mu.Unlock()
		return tmux(ctx, executable, socket, args...)
	}
}

func (c *countedWake) counts() (groups, keys int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.groups, c.keys
}

func (c *countedWake) show(screen string, captures int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fake, c.fakes = screen, captures
}

func (c *countedWake) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.groups, c.keys, c.captures = 0, 0, 0
}

// A busy Codex pane waits with no process-table read and no key (#758): a running turn or a
// draft, also in a pane whose foreground the full proof would refuse. An idle screen takes the
// full path, which proves the foreground before it types and again before Enter.
func TestCodexWakeBusyPaneReadsNoTable(t *testing.T) {
	f := newCodexFixture(t)
	idleScreen := f.run("capture-pane", "-p", "-e", "-J", "-t", f.pane("idle", "").id)
	working := f.pane("working", "")
	workingScreen := f.run("capture-pane", "-p", "-e", "-J", "-t", working.id)
	if codexIdle(workingScreen) || !codexIdle(idleScreen) {
		t.Fatal("fixture screens")
	}
	c := &countedWake{}
	c.install(f)
	for name, command := range map[string][2]string{
		"working":                        {"working", "sh -c 'PROGRAM; exit'"},
		"draft":                          {"draft", "sh -c 'PROGRAM; exit'"},
		"working, child in foreground":   {"working", "sh -c 'set -m; PROGRAM; exit'"},
		"draft, child in the foreground": {"draft", "sh -c 'set -m; PROGRAM; exit'"},
	} {
		t.Run(name, func(t *testing.T) {
			c.reset()
			p := f.pane(command[0], command[1])
			host := p.shell
			if !strings.Contains(command[1], "set -m") {
				host = f.child(p.shell)
			}
			session := f.session(host, p.id, "idle")
			if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != waiting("receiver_busy") {
				t.Fatalf("wake: %v", got)
			}
			if groups, keys := c.counts(); groups != 0 || keys != 0 {
				t.Fatalf("%d table reads, %d keys", groups, keys)
			}
			f.untouched(p)
		})
	}

	// A screen that looked idle to the first read and is busy to the full path's own read waits
	// after the full proof.
	c.reset()
	p := f.pane("working", "sh -c 'PROGRAM; exit'")
	session := f.session(f.child(p.shell), p.id, "idle")
	c.show(idleScreen, 1)
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != waiting("receiver_busy") {
		t.Fatalf("idle, then busy: %v", got)
	}
	if groups, keys := c.counts(); groups != 1 || keys != 0 {
		t.Fatalf("idle, then busy: %d table reads, %d keys", groups, keys)
	}
	f.untouched(p)

	// A busy result is not kept: the same pane, idle at the next wake, gets the notice after
	// the full proof before it types and again before Enter.
	c.reset()
	p = f.pane("idle", "sh -c 'PROGRAM; exit'")
	session = f.session(f.child(p.shell), p.id, "idle")
	c.show(workingScreen, 1)
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != waiting("receiver_busy") {
		t.Fatalf("busy: %v", got)
	}
	if groups, keys := c.counts(); groups != 0 || keys != 0 {
		t.Fatalf("busy: %d table reads, %d keys", groups, keys)
	}
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != accepted() {
		t.Fatalf("then idle: %v", got)
	}
	if groups, keys := c.counts(); groups != 2 || keys != 2 {
		t.Fatalf("then idle: %d table reads, %d keys, want 2 and 2", groups, keys)
	}
	if got := f.received(p); got != codexTestNotice+"\n" {
		t.Fatalf("received %q", got)
	}
}
