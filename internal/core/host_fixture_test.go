package core

import (
	"os"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// pinSyntheticHost makes s read the synthetic host process pid as running with start; every
// other process ID is read as before. A fixture that records a synthetic host row calls it
// before any goroutine reads s, so that the cycle probe's host check (#637) sees that host
// as running.
func pinSyntheticHost(s *Store, pid int, start int64) {
	read := s.processStart
	s.processStart = func(p int) (int64, error) {
		if p == pid {
			return start, nil
		}
		return read(p)
	}
}

// liveHost is the test process's own process ID and start time: a host that runs for the
// whole test. A fixture on a started daemon records it, because the daemon's goroutines
// already read the store's process reader.
func liveHost(t *testing.T) (int, int64) {
	t.Helper()
	pid := os.Getpid()
	start, err := platform.ProcessStart(pid)
	if err != nil || start <= 0 {
		t.Fatalf("own process start: %d %v", start, err)
	}
	return pid, start
}
