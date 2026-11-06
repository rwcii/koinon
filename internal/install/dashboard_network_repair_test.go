package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

func TestInstallDashboardNetworkRepairInstructions(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		for _, unsafe := range []bool{false, true} {
			e := newEnv(t, "systemd")
			// The repair must retain a custom state directory and quote its argument safely.
			state := e.o.StateDir + " with space"
			if err := os.Rename(e.o.StateDir, state); err != nil {
				t.Fatal(err)
			}
			e.o.StateDir, e.o.Upgrade, e.o.NoStart = state, upgrade, true
			path := filepath.Join(state, "dashboard-address.json")
			if err := os.WriteFile(path, []byte("{bad"), 0600); err != nil {
				t.Fatal(err)
			}
			if unsafe {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Install(context.Background(), e.o)
			if err == nil {
				t.Fatal("invalid saved setting was ignored")
			}
			for _, want := range []string{path, "koinon install --state-dir " + core.ShellWord(state) + " --dashboard-address HOST:PORT", "--dashboard-address none"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("upgrade%v unsafe%v: missing %q in %v", upgrade, unsafe, want, err)
				}
			}
			e.o.DashboardAddress = "none"
			_, repair := Install(context.Background(), e.o)
			if unsafe {
				info, _ := os.Stat(path)
				if repair == nil || info == nil || info.Mode().Perm() != 0644 {
					t.Fatalf("unsafe setting overwritten: %v %v", info, repair)
				}
			} else if repair != nil {
				t.Fatalf("explicit repair failed: %v", repair)
			}
		}
	}
}
