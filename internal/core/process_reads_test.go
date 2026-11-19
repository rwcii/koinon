package core

import (
	"context"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// countingTools are host tools over a synthetic process table that count the reads: each
// one-process parent read, and each whole-table read (#758).
type countingTools struct {
	table          map[int]platform.ProcessGroup
	parent, groups int
}

func (c *countingTools) tools(panePID string) hostTools {
	return hostTools{
		tmux: func(context.Context, string, ...string) (string, error) { return "%4 " + panePID, nil },
		processStart: func(pid int) (int64, error) {
			if _, ok := c.table[pid]; !ok {
				return 0, platform.ErrProcessGone
			}
			return 77, nil
		},
		processParent: func(pid int) (int, error) {
			c.parent++
			g, ok := c.table[pid]
			if !ok {
				return 0, platform.ErrProcessGone
			}
			return g.Parent, nil
		},
		processCommand: func(pid int) (string, []string, error) { return "/bin/sh", nil, nil },
		processGroups: func() (map[int]platform.ProcessGroup, error) {
			c.groups++
			return c.table, nil
		},
	}
}

// A foreground proof reads the whole table one time, and walks the pane ancestry one process
// at a time. T1 still admits a waiting ancestor shell and refuses a child in the foreground.
func TestForegroundProofReadsTheTableOnce(t *testing.T) {
	// The pane's shell 90 waits for the host 100 in the host's foreground group, under the
	// tmux server 80; 500 is an unrelated process of another terminal.
	table := map[int]platform.ProcessGroup{
		80:  {Group: 80, Parent: 1},
		90:  {Group: 100, Terminal: 5, Foreground: 100, Parent: 80},
		100: {Group: 100, Terminal: 5, Foreground: 100, Parent: 90},
		500: {Group: 500, Terminal: 9, Foreground: 500, Parent: 1},
	}
	for name, proof := range map[string]func(context.Context, hostTools, int64, int64, string, string) error{
		"codex": codexForeground, "agy": agyForeground,
	} {
		c := &countingTools{table: table}
		if err := proof(context.Background(), c.tools("90"), 100, 77, "/tmp/synthetic-socket", "%4"); err != nil {
			t.Fatalf("%s: waiting ancestor shell refused: %v", name, err)
		}
		if c.groups != 1 || c.parent != 1 {
			t.Fatalf("%s: %d table reads and %d parent reads, want 1 and 1", name, c.groups, c.parent)
		}
	}
	// A child of the host in the foreground group is refused (the 7638dc8 case).
	child := map[int]platform.ProcessGroup{110: {Group: 100, Terminal: 5, Foreground: 100, Parent: 100}}
	for pid, g := range table {
		child[pid] = g
	}
	c := &countingTools{table: child}
	if err := codexForeground(context.Background(), c.tools("90"), 100, 77, "/tmp/synthetic-socket", "%4"); err == nil {
		t.Fatal("child in the foreground admitted")
	}
	if c.groups != 1 {
		t.Fatalf("%d table reads", c.groups)
	}
}

// The status-line fallback and the agy witness never read the whole process table: they walk
// the reporter's ancestors one process at a time (#758).
func TestAncestryWalksReadNoTable(t *testing.T) {
	s, _ := testStore(t)
	s.processParents = func() (map[int]int, error) {
		t.Fatal("whole process table read")
		return nil, nil
	}
	reads := 0
	read := parentIn(map[int]int{999: 3000, 3000: 4000, 4000: 1})
	s.processParent = func(pid int) (int, error) { reads++; return read(pid) }
	s.processStart = func(pid int) (int64, error) { return 50, nil }
	s.processCommand = func(int) (string, []string, error) { return "/bin/sh", nil, nil }
	_, err := s.claudeObservationCaller(context.Background(), Key{"claude", "synthetic-unknown"}, ObservationReporter{PID: 999, Start: 50})
	if err == nil || reads != 3 {
		t.Fatalf("no host on the walk: %v after %d parent reads, want a refusal after 3", err, reads)
	}
}
