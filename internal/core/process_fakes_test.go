package core

import "github.com/rwcii/koinon/internal/platform"

// parentIn reads one process's parent from a synthetic table; a process that the table does
// not hold is gone.
func parentIn(parents map[int]int) func(int) (int, error) {
	return func(pid int) (int, error) {
		parent, ok := parents[pid]
		if !ok {
			return 0, platform.ErrProcessGone
		}
		return parent, nil
	}
}

// withParents is a synthetic ProcessGroups table whose records also hold the parents, as one
// read of the real table gives them (#758).
func withParents(groups map[int]platform.ProcessGroup, parents map[int]int) map[int]platform.ProcessGroup {
	table := make(map[int]platform.ProcessGroup, len(groups))
	for pid, g := range groups {
		g.Parent = parents[pid]
		table[pid] = g
	}
	return table
}
