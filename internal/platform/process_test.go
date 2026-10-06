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
