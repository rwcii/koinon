package platform

import (
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
