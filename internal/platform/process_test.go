package platform

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func runLockHelper(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedAfterProcessDeath$")
	cmd.Env = append(os.Environ(), "KOINON_SYNTHETIC_LOCK_HELPER=1", "KOINON_SYNTHETIC_LOCK_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
}

func TestProcessParentsHoldsThisProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	parents, err := ProcessParents()
	if err != nil {
		t.Fatal(err)
	}
	if parents[os.Getpid()] != os.Getppid() {
		t.Fatalf("parent of this process: got %d, want %d", parents[os.Getpid()], os.Getppid())
	}
	if parents[cmd.Process.Pid] != os.Getpid() {
		t.Fatalf("parent of the child: got %d, want %d", parents[cmd.Process.Pid], os.Getpid())
	}
}

// ProcessStart identifies a process with its ID: the same process reads the same value, a
// child has its own, and an ended process is gone.
func TestProcessStart(t *testing.T) {
	self, err := ProcessStart(os.Getpid())
	if err != nil || self <= 0 {
		t.Fatalf("this process: %d %v", self, err)
	}
	if again, err := ProcessStart(os.Getpid()); err != nil || again != self {
		t.Fatalf("read again: %d %v", again, err)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	child, err := ProcessStart(pid)
	if err != nil || child < self {
		t.Fatalf("child: %d (this process %d) %v", child, self, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if _, err := ProcessStart(pid); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("ended child: %v", err)
	}
	if _, err := ProcessStart(0); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("pid 0: %v", err)
	}
}
