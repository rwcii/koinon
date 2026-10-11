package launcher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

func testExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecordCLIPreservesPathsAndConcurrentSetup(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	paths := map[string]string{}
	for _, family := range []string{"codex", "agy", "opencode"} {
		paths[family] = testExecutable(t, family)
	}
	var wg sync.WaitGroup
	for family, cli := range paths {
		wg.Go(func() {
			if changed, err := RecordCLI(context.Background(), state, family, cli); err != nil || !changed {
				t.Errorf("record %s: %v %v", family, changed, err)
			}
		})
	}
	wg.Wait()
	// Launch-time reads do not depend on PATH, and explicit overrides still win.
	t.Setenv("PATH", t.TempDir())
	for family, cli := range paths {
		if path, err := ConfiguredCLI(state, family, ""); err != nil || path != cli {
			t.Fatalf("configured %s: %q %v", family, path, err)
		}
		if changed, err := RecordCLI(context.Background(), state, family, cli); err != nil || changed {
			t.Fatalf("repeat %s: %v %v", family, changed, err)
		}
	}
	before, _ := os.ReadFile(filepath.Join(state, "launchers.json"))
	override := testExecutable(t, "override")
	if path, err := ConfiguredCLI(state, "codex", override); err != nil || path != override {
		t.Fatalf("override: %q %v", path, err)
	}
	after, _ := os.ReadFile(filepath.Join(state, "launchers.json"))
	if string(before) != string(after) {
		t.Fatal("launch override changed recorded configuration")
	}
	if changed, err := RecordCLI(context.Background(), state, "codex", override); err != nil || !changed {
		t.Fatalf("update: %v %v", changed, err)
	}
	if path, err := ConfiguredCLI(state, "agy", ""); err != nil || path != paths["agy"] {
		t.Fatal("updating Codex lost another family's entry")
	}
	info, err := os.Stat(filepath.Join(state, "launchers.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
}

func TestRecordCLIRefusesUnsafeConfiguration(t *testing.T) {
	cli := testExecutable(t, "codex")
	for _, fixture := range []string{"public", "symlink", "hardlink", "invalid", "null", "oversized"} {
		t.Run(fixture, func(t *testing.T) {
			state, err := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "launchers.json")
			data := []byte(`{"agy":"/opt/synthetic/agy"}`)
			mode := os.FileMode(0600)
			switch fixture {
			case "public":
				mode = 0644
			case "invalid":
				data = []byte("{")
			case "null":
				data = []byte("null")
			case "oversized":
				data = make([]byte, 16385)
			}
			if err := os.WriteFile(path, data, mode); err != nil {
				t.Fatal(err)
			}
			if fixture == "symlink" || fixture == "hardlink" {
				other := filepath.Join(state, "retained")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if fixture == "symlink" {
					err = os.Symlink(other, path)
				} else {
					err = os.Link(other, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if changed, err := RecordCLI(context.Background(), state, "codex", cli); err == nil || changed {
				t.Fatalf("unsafe configuration accepted: %v %v", changed, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(data) {
				t.Fatal("refused configuration was changed")
			}
		})
	}
}

func TestRecordedPathLaunchAndOverride(t *testing.T) {
	root, address, cli, directory := fixture(t)
	state := filepath.Join(root, "state")
	if _, err := RecordCLI(context.Background(), state, "codex", cli); err != nil {
		t.Fatal(err)
	}
	// The stored executable really launches; no --cli is provided.
	t.Setenv("TMUX", "")
	t.Setenv("PATH", t.TempDir())
	result := filepath.Join(root, "configured-result")
	args := []string{"codex", "--state-dir", state, "--address", address, "--directory", directory,
		"--", "--result", result, "--family", "codex", "literal ' $() ` ; argument"}
	if out, err := exec.Command(launcherBinary, args...).CombinedOutput(); err != nil {
		t.Fatalf("recorded launch: %v %s", err, out)
	}
	checkReport(t, readReport(t, result), "codex", directory)
	// A broken configured path does not prevent an explicit valid override.
	data, _ := json.Marshal(map[string]string{"codex": filepath.Join(root, "missing")})
	if err := os.WriteFile(filepath.Join(state, "launchers.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	result = filepath.Join(root, "override-result")
	args = arguments(root, address, cli, directory, "codex", result)
	if out, err := exec.Command(launcherBinary, args...).CombinedOutput(); err != nil {
		t.Fatalf("override launch: %v %s", err, out)
	}
	checkReport(t, readReport(t, result), "codex", directory)
}

// A family without a recorded CLI, an empty entry and a relative entry each name their own
// cause; the first two name setup for this state directory and --cli (#272).
func TestMissingCLIEntry(t *testing.T) {
	state := filepath.Join(t.TempDir(), "custom state")
	remedy := "; run koinon setup agy --state-dir '" + state + "' to record it, or start with --cli ABS_PATH"
	if _, err := ConfiguredCLI(state, "agy", ""); err == nil || err.Error() != "no agy CLI is configured in launchers.json"+remedy {
		t.Fatalf("no file: %v", err)
	}
	if _, err := platform.PrivateDir(state); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"codex": testExecutable(t, "codex"), "agy": "", "opencode": "bin/opencode"})
	if err := os.WriteFile(filepath.Join(state, "launchers.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for family, want := range map[string]string{
		"claude":   "no claude CLI is configured in launchers.json; run koinon setup claude --state-dir '" + state + "' to record it, or start with --cli ABS_PATH",
		"agy":      "the agy CLI entry in launchers.json is empty" + remedy,
		"opencode": "configured CLI path must be absolute",
	} {
		if _, err := ConfiguredCLI(state, family, ""); err == nil || err.Error() != want {
			t.Errorf("%s: %v", family, err)
		}
	}
	// The default state directory needs no --state-dir in the setup command.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	standard, err := platform.DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfiguredCLI(standard, "agy", ""); err == nil ||
		err.Error() != "no agy CLI is configured in launchers.json; run koinon setup agy to record it, or start with --cli ABS_PATH" {
		t.Fatalf("default state: %v", err)
	}
}
