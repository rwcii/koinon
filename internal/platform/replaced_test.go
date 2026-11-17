package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
)

const oneLinkRefusal = "state file must be a regular file with one link"

// replacingOpener opens the real path and then, while it holds the opened inode, runs after
// (for example a replacement of the path); it keeps each file it returned so that a test can
// check that openPrivate closed the ones it refused.
type replacingOpener struct {
	t      *testing.T
	opened []*os.File
	after  func(call int)
}

func (o *replacingOpener) open(path string, flags int) (*os.File, error) {
	f, err := openNoFollow(path, flags)
	if err != nil {
		return nil, err
	}
	o.opened = append(o.opened, f)
	if o.after != nil {
		o.after(len(o.opened))
	}
	return f, nil
}

// closedExcept checks that every opened file except keep is closed.
func (o *replacingOpener) closedExcept(keep *os.File) {
	o.t.Helper()
	for i, f := range o.opened {
		if f == keep {
			continue
		}
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			o.t.Fatalf("open %d was not closed: %v", i+1, err)
		}
	}
}

func privateFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteAtomic(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func replace(t *testing.T, path, body string) {
	t.Helper()
	if err := WriteAtomic(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// A read whose file is replaced between its open and its check reads the replacement (#727).
func TestOpenPrivateReadsTheReplacement(t *testing.T) {
	path := privateFile(t, "old")
	o := &replacingOpener{t: t, after: func(call int) {
		if call == 1 {
			replace(t, path, "new")
		}
	}}
	f, err := openPrivate(path, os.O_RDONLY, o.open)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if data, err := io.ReadAll(f); err != nil || string(data) != "new" {
		t.Fatalf("read %q, %v; want the replacement", data, err)
	}
	if len(o.opened) != 2 {
		t.Fatalf("%d opens, want 2", len(o.opened))
	}
	o.closedExcept(f)
}

// A file replaced at each open is refused after replacedOpens opens, all closed.
func TestOpenPrivateStopsAfterItsOpens(t *testing.T) {
	path := privateFile(t, "0")
	o := &replacingOpener{t: t, after: func(int) { replace(t, path, "next") }}
	f, err := openPrivate(path, os.O_RDONLY, o.open)
	if f != nil || err == nil || err.Error() != oneLinkRefusal {
		t.Fatalf("got %v, %v; want the one-link refusal", f, err)
	}
	if len(o.opened) != replacedOpens {
		t.Fatalf("%d opens, want %d", len(o.opened), replacedOpens)
	}
	o.closedExcept(nil)
}

// A path removed after the open is missing at the next open.
func TestOpenPrivateReplacedThenRemoved(t *testing.T) {
	path := privateFile(t, "old")
	o := &replacingOpener{t: t, after: func(call int) {
		if call == 1 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}}
	f, err := openPrivate(path, os.O_RDONLY, o.open)
	if f != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, %v; want not exist", f, err)
	}
	if len(o.opened) != 1 {
		t.Fatalf("%d opens, want 1", len(o.opened))
	}
	o.closedExcept(nil)
}

// The next open checks its inode as any open does: a symbolic link or a file with a wrong mode
// at the path is refused, with no further open.
func TestOpenPrivateChecksTheReplacement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		put   func(t *testing.T, path string)
		opens int
		check func(error) bool
	}{
		{"symbolic link", func(t *testing.T, path string) {
			target := privateFile(t, "elsewhere")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, 1, func(err error) bool { return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) }},
		{"mode 0644", func(t *testing.T, path string) {
			if err := WriteAtomic(path, []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
		}, 2, func(err error) bool { return err != nil && err.Error() == "unsafe state ownership or permissions" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := privateFile(t, "old")
			o := &replacingOpener{t: t, after: func(call int) {
				if call == 1 {
					tc.put(t, path)
				}
			}}
			f, err := openPrivate(path, os.O_RDONLY, o.open)
			if f != nil || !tc.check(err) {
				t.Fatalf("got %v, %v", f, err)
			}
			if len(o.opened) != tc.opens {
				t.Fatalf("%d successful opens, want %d", len(o.opened), tc.opens)
			}
			o.closedExcept(nil)
		})
	}
}

// A replaced inode that fails another check, and a file with two links, are refused at once.
func TestOpenPrivateRefusesOtherInodesAtOnce(t *testing.T) {
	t.Run("replaced inode with mode 0644", func(t *testing.T) {
		path := privateFile(t, "old")
		o := &replacingOpener{t: t, after: func(call int) {
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			replace(t, path, "new")
		}}
		if f, err := openPrivate(path, os.O_RDONLY, o.open); f != nil || err == nil {
			t.Fatalf("got %v, %v", f, err)
		}
		if len(o.opened) != 1 {
			t.Fatalf("%d opens, want 1", len(o.opened))
		}
		o.closedExcept(nil)
	})
	t.Run("two links", func(t *testing.T) {
		path := privateFile(t, "old")
		if err := os.Link(path, path+".link"); err != nil {
			t.Fatal(err)
		}
		o := &replacingOpener{t: t}
		if f, err := openPrivate(path, os.O_RDONLY, o.open); f != nil || err == nil || err.Error() != oneLinkRefusal {
			t.Fatalf("got %v, %v", f, err)
		}
		if len(o.opened) != 1 {
			t.Fatalf("%d opens, want 1", len(o.opened))
		}
		o.closedExcept(nil)
	})
}

// A writable or creating open of a replaced inode is refused with no second open, so that no
// write goes to an orphan.
func TestOpenPrivateWritesDoNotReopen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags int
	}{
		{"write only", os.O_WRONLY},
		{"read write", os.O_RDWR},
		{"read write append", os.O_RDWR | os.O_APPEND},
		{"read only create", os.O_RDONLY | os.O_CREATE},
		{"read only truncate", os.O_RDONLY | os.O_TRUNC},
		{"read only exclusive create", os.O_RDONLY | os.O_CREATE | os.O_EXCL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := privateFile(t, "old")
			if tc.flags&os.O_EXCL != 0 {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			o := &replacingOpener{t: t, after: func(int) { replace(t, path, "new") }}
			f, err := openPrivate(path, tc.flags, o.open)
			if f != nil || err == nil || err.Error() != oneLinkRefusal {
				t.Fatalf("got %v, %v; want the one-link refusal", f, err)
			}
			if len(o.opened) != 1 {
				t.Fatalf("%d opens, want 1", len(o.opened))
			}
			o.closedExcept(nil)
		})
	}
}

// Supporting coverage with real scheduling: reads during repeated atomic replacements of the
// same file are not refused. Only replacedOpens replacements in a row, each between one read's
// open and its check, exhaust a read; a replacement syncs the file and its directory, so it
// takes far longer than an open and a check.
func TestOpenPrivateDuringAtomicReplacements(t *testing.T) {
	path := privateFile(t, "{}\n")
	var stop atomic.Bool
	done := make(chan error, 1)
	go func() {
		var err error
		for !stop.Load() && err == nil {
			err = WriteAtomic(path, []byte("{}\n"), 0600)
		}
		done <- err
	}()
	var readErr error
	for i := 0; i < 5000 && readErr == nil; i++ {
		var f *os.File
		if f, readErr = OpenPrivate(path, os.O_RDONLY); readErr == nil {
			f.Close()
		}
	}
	stop.Store(true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatalf("a read during replacements was refused: %v", readErr)
	}
}
