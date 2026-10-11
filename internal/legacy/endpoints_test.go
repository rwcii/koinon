package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// shortDir is a temporary directory short enough for AF_UNIX paths on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// deadSocketAt leaves a socket file that nothing listens on, as a killed process does.
func deadSocketAt(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0700)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
}

// sessionsUnder builds a Python state root with two 16-hex session directories and the
// fallback socket directory, and returns the root and the two session homes.
func sessionsUnder(t *testing.T) (string, string, string) {
	t.Helper()
	dir := shortDir(t)
	saved := fallbackSockets
	fallbackSockets = filepath.Join(dir, "f")
	t.Cleanup(func() { fallbackSockets = saved })
	os.MkdirAll(fallbackSockets, 0700)
	state := filepath.Join(dir, "s")
	a, b := filepath.Join(state, "sessions", strings.Repeat("a", 16)), filepath.Join(state, "sessions", strings.Repeat("b", 16))
	for _, home := range []string{a, b, filepath.Join(state, "memory", "m")} {
		os.MkdirAll(home, 0700)
	}
	return state, a, b
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestClearDeadEndpoints: the direct and notifier sockets of one session and the
// fallback socket of another are removed when nothing listens on them; a memory
// directory is not a session and keeps its file; a second run finds nothing.
func TestClearDeadEndpoints(t *testing.T) {
	state, a, b := sessionsUnder(t)
	resolved, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(resolved))
	fallback := filepath.Join(fallbackSockets, hex.EncodeToString(sum[:])[:16]+"-control.sock")
	direct, notifier, memory := filepath.Join(a, "control.sock"), filepath.Join(a, "notifier", "control.sock"),
		filepath.Join(state, "memory", "m", "control.sock")
	for _, path := range []string{direct, notifier, fallback, memory} {
		deadSocketAt(t, path)
	}
	removed, err := ClearDeadEndpoints(state)
	if err != nil || len(removed) != 3 {
		t.Fatalf("removed %v: %v", removed, err)
	}
	for _, path := range []string{direct, notifier, fallback} {
		if exists(path) {
			t.Fatalf("%s remains", path)
		}
		found := false
		for _, r := range removed {
			found = found || filepath.Base(filepath.Dir(r)) == filepath.Base(filepath.Dir(path)) && filepath.Base(r) == filepath.Base(path)
		}
		if !found {
			t.Fatalf("%s is not reported in %v", path, removed)
		}
	}
	if !exists(memory) {
		t.Fatal("the memory directory's socket was removed")
	}
	if again, err := ClearDeadEndpoints(state); err != nil || len(again) != 0 {
		t.Fatalf("second run %v: %v", again, err)
	}
}

// TestClearDeadEndpointsRefuses: a socket that accepts a connection, a file that is not
// a socket and a held writer lock each stop the clearing, name the cause and keep the files.
func TestClearDeadEndpointsRefuses(t *testing.T) {
	t.Run("listening", func(t *testing.T) {
		state, a, b := sessionsUnder(t)
		dead := filepath.Join(b, "control.sock")
		deadSocketAt(t, dead)
		live := filepath.Join(a, "notifier", "control.sock")
		os.MkdirAll(filepath.Dir(live), 0700)
		l, err := net.Listen("unix", live)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		removed, err := ClearDeadEndpoints(state)
		if err == nil || !strings.HasPrefix(err.Error(), "python_running:") || !strings.Contains(err.Error(), "notifier") || len(removed) != 0 {
			t.Fatalf("removed %v: %v", removed, err)
		}
		if !exists(live) || !exists(dead) {
			t.Fatal("a file was removed")
		}
	})
	t.Run("bound, not listening", func(t *testing.T) {
		// A bridge binds before it listens, and refuses connections in between.
		state, a, _ := sessionsUnder(t)
		path := filepath.Join(a, "control.sock")
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
		if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
			t.Fatal(err)
		}
		removed, err := ClearDeadEndpoints(state)
		if err == nil || !strings.HasPrefix(err.Error(), "python_running:") || !strings.Contains(err.Error(), path) || len(removed) != 0 {
			t.Fatalf("removed %v: %v", removed, err)
		}
		if !exists(path) {
			t.Fatal("a bound socket was removed")
		}
	})
	t.Run("line break in the path", func(t *testing.T) {
		// The kernel table splits such an address over two rows, so it proves nothing.
		dir := shortDir(t)
		state := filepath.Join(dir, "s\nt")
		path := filepath.Join(state, "sessions", strings.Repeat("a", 16), "control.sock")
		os.MkdirAll(filepath.Dir(path), 0700)
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
		if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
			t.Fatal(err)
		}
		removed, err := ClearDeadEndpoints(state)
		if err == nil || !strings.HasPrefix(err.Error(), "python_endpoint_unverified:") || len(removed) != 0 {
			t.Fatalf("removed %v: %v", removed, err)
		}
		if !exists(path) {
			t.Fatal("a bound socket was removed")
		}
	})
	t.Run("not a socket", func(t *testing.T) {
		state, a, _ := sessionsUnder(t)
		path := filepath.Join(a, "control.sock")
		os.WriteFile(path, nil, 0600)
		removed, err := ClearDeadEndpoints(state)
		if err == nil || !strings.HasPrefix(err.Error(), "python_endpoint_unverified:") || !strings.Contains(err.Error(), path) || len(removed) != 0 {
			t.Fatalf("removed %v: %v", removed, err)
		}
		if !exists(path) {
			t.Fatal("the file was removed")
		}
	})
	t.Run("held lock", func(t *testing.T) {
		state, a, b := sessionsUnder(t)
		dead := filepath.Join(a, "control.sock")
		deadSocketAt(t, dead)
		f, err := os.OpenFile(filepath.Join(b, "supervisor.lock"), os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		removed, err := ClearDeadEndpoints(state)
		if err == nil || !strings.HasPrefix(err.Error(), "python_running:") || len(removed) != 0 {
			t.Fatalf("removed %v: %v", removed, err)
		}
		if !exists(dead) {
			t.Fatal("the socket was removed while a writer held its lock")
		}
	})
}

// TestEndpointsMatchThePythonRuntime: the direct socket is checked under the written and
// the resolved directory while it fits an AF_UNIX address, and the fallback always.
func TestEndpointsMatchThePythonRuntime(t *testing.T) {
	dir := shortDir(t)
	short := filepath.Join(dir, "s")
	paths := endpoints(short)
	if !slices.Contains(paths, filepath.Join(short, "control.sock")) || !strings.HasSuffix(paths[len(paths)-1], "-control.sock") {
		t.Fatalf("short %v", paths)
	}
	long := filepath.Join(dir, strings.Repeat("x", 120))
	if paths := endpoints(long); len(paths) != 1 || filepath.Dir(paths[0]) != fallbackSockets {
		t.Fatalf("long %v", paths)
	}
}
