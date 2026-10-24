//go:build linux

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/library"
	"golang.org/x/sys/unix"
)

// The facts read never blocks: a FIFO swapped in after the regular-file check is refused on
// the opened object (#468).
func TestHandoffFactsReadDoesNotBlockOnSwappedFIFO(t *testing.T) {
	dir := t.TempDir()
	facts := filepath.Join(dir, handoffFacts)
	if err := os.MkdirAll(filepath.Dir(facts), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(facts, []byte("## Version\n1\n## Skill\nhandoff\n## Sources\n- AGENTS.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	swap := filepath.Join(dir, "swap")
	if err := unix.Mkfifo(swap, 0600); err != nil {
		t.Fatal(err)
	}
	// Hold a writer on the FIFO so a mistaken blocking read cannot finish until released.
	fd, err := unix.Open(swap, unix.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if fd >= 0 {
			unix.Close(fd)
		}
	}()
	root, err := library.OpenReadRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	catalog, err := library.Embedded("review")
	if err != nil {
		t.Fatal(err)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				_ = unix.Renameat2(unix.AT_FDCWD, facts, unix.AT_FDCWD, swap, unix.RENAME_EXCHANGE)
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	for i := 0; i < 2000; i++ {
		done := make(chan struct{})
		go func() {
			_, _ = handoffDirectory(root, catalog, "claude")
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
			unix.Close(fd)
			fd = -1
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("facts reader did not return after FIFO writer was released")
			}
			t.Fatalf("facts read blocked on a FIFO swapped after the regular-file stat (attempt %d)", i)
		}
	}
}
