package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A FIFO where a subagent directory belongs does not block the count: it is unknown (#467).
func TestClaudeSubagentDirectoryReadDoesNotBlock(t *testing.T) {
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	path := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", term.epoch, "subagents")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := claudeRunningSubagents(term.target(t), time.Now().Add(-time.Minute).UnixMilli())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an unreadable subagent directory must produce unknown")
		}
	case <-time.After(250 * time.Millisecond):
		// Release a mistakenly blocking open so the failing probe leaves no stuck goroutine.
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("subagent directory reader did not return after FIFO release")
		}
		t.Fatal("subagent count blocks opening an unreadable directory instead of returning unknown")
	}
}
