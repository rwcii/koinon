package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type wrongOwner struct{ os.FileInfo }

func (f wrongOwner) Sys() any {
	stat := *f.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid = uint32(os.Geteuid() + 1)
	return &stat
}

func TestPrivateFilesAndLocks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(root, "lock")
	f, err := Lock(lock)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := owned(wrongOwner{info}, false); err == nil {
		t.Fatal("foreign owner accepted")
	}
	if second, err := Lock(lock); err == nil {
		Unlock(second)
		t.Fatal("second owner obtained lock")
	}
	if err := Unlock(f); err != nil {
		t.Fatal(err)
	}
	// A stopped owner leaves a permanent lock file which the next owner reuses.
	f, err = Lock(lock)
	if err != nil {
		t.Fatal(err)
	}
	Unlock(f)
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory"} {
		path := filepath.Join(root, kind)
		switch kind {
		case "symlink":
			err = os.Symlink(lock, path)
		case "hardlink":
			err = os.Link(lock, path)
		case "fifo":
			err = syscall.Mkfifo(path, 0600)
		case "directory":
			err = os.Mkdir(path, 0700)
		}
		if err != nil {
			t.Fatal(err)
		}
		if f, err := OpenPrivate(path, os.O_RDONLY); err == nil {
			f.Close()
			t.Fatalf("unsafe type accepted: %s", kind)
		}
	}
	if _, err := PrivateDir("relative"); err == nil {
		t.Fatal("relative root accepted")
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrivateDir(root); err == nil {
		t.Fatal("unsafe root accepted")
	}
}

func TestLockReleasedAfterProcessDeath(t *testing.T) {
	if os.Getenv("KOINON_SYNTHETIC_LOCK_HELPER") == "1" {
		f, err := Lock(os.Getenv("KOINON_SYNTHETIC_LOCK_PATH"))
		if err != nil {
			os.Exit(2)
		}
		if _, err := f.WriteString("held"); err != nil {
			os.Exit(3)
		}
		// Exit without releasing the lock, as a daemon crash would.
		os.Exit(0)
	}
	root, _ := PrivateDir(filepath.Join(t.TempDir(), "state"))
	path := filepath.Join(root, "lock")
	runLockHelper(t, path)
	f, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer Unlock(f)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "held" {
		t.Fatalf("lock inode lost: %q %v", data, err)
	}
}

func TestDefaultStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	got, err := DefaultStateDir()
	if err != nil || got != filepath.Join(os.Getenv("XDG_STATE_HOME"), "koinon", "go") {
		t.Fatalf("default: %s %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := DefaultStateDir(); err == nil {
		t.Fatal("relative XDG accepted")
	}
	if _, err := OpenPrivate(filepath.Join(t.TempDir(), "missing"), os.O_RDONLY); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestProcessCommand(t *testing.T) {
	executable, args, err := ProcessCommand(os.Getpid())
	if err != nil || len(args) == 0 {
		t.Fatalf("own process: %q %v %v", executable, args, err)
	}
	self, _ := os.Executable()
	if filepath.Base(executable) != filepath.Base(self) {
		t.Fatalf("executable %q, want %q", executable, self)
	}
	if _, _, err := ProcessCommand(1 << 30); err == nil {
		t.Fatal("missing process reported")
	}
}
