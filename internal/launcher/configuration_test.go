package launcher

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// entries reads launchers.json, or nil when it is absent.
func entries(t *testing.T, state string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "launchers.json"))
	if os.IsNotExist(err) {
		return nil
	}
	var config map[string]string
	if err != nil || json.Unmarshal(data, &config) != nil {
		t.Fatalf("launchers.json: %v %q", err, data)
	}
	return config
}

// A launch with no recorded CLI and no --cli records the family's CLI from PATH and reports
// it; a CLI that is not on PATH, an unreadable file and an unusable entry change nothing
// (PDR 003, Decision 5).
func TestLaunchRecordsCLIFromPath(t *testing.T) {
	ctx := context.Background()
	bin := t.TempDir()
	cli := filepath.Join(bin, "codex")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Not on PATH: the shipped refusal, and no file.
	state := filepath.Join(t.TempDir(), "state")
	t.Setenv("PATH", t.TempDir())
	if _, err := launchCLI(ctx, Options{Family: "codex", StateDir: state}, io.Discard); err == nil ||
		!strings.HasPrefix(err.Error(), "no codex CLI is configured in launchers.json; run koinon setup codex") {
		t.Fatalf("not on PATH: %v", err)
	}
	if entries(t, state) != nil {
		t.Fatal("a launch without a CLI on PATH wrote launchers.json")
	}
	// On PATH: recorded, reported, and other entries kept.
	t.Setenv("PATH", bin)
	other := testExecutable(t, "agy")
	if _, err := RecordCLI(ctx, state, "agy", other); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	if path, err := launchCLI(ctx, Options{Family: "codex", StateDir: state}, &report); err != nil || path != cli {
		t.Fatalf("PATH fallback: %q %v", path, err)
	}
	if got := entries(t, state); got["codex"] != cli || got["agy"] != other {
		t.Fatalf("recorded entries: %v", got)
	}
	if report.String() != "koinon: recorded the codex CLI "+cli+" in launchers.json\n" {
		t.Fatalf("report: %q", report.String())
	}
	// Recorded: later launches use it without PATH and report nothing; --cli writes nothing.
	t.Setenv("PATH", t.TempDir())
	report.Reset()
	if path, err := launchCLI(ctx, Options{Family: "codex", StateDir: state}, &report); err != nil || path != cli || report.Len() != 0 {
		t.Fatalf("recorded launch: %q %v %q", path, err, report.String())
	}
	override := testExecutable(t, "codex")
	if path, err := launchCLI(ctx, Options{Family: "codex", StateDir: state, CLI: override}, &report); err != nil || path != override || entries(t, state)["codex"] != cli {
		t.Fatalf("override: %q %v %v", path, err, entries(t, state))
	}
	// An empty entry is filled from PATH.
	t.Setenv("PATH", bin)
	empty := filepath.Join(t.TempDir(), "state")
	if _, err := platform.PrivateDir(empty); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "launchers.json"), []byte(`{"codex":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if path, err := launchCLI(ctx, Options{Family: "codex", StateDir: empty}, io.Discard); err != nil || path != cli || entries(t, empty)["codex"] != cli {
		t.Fatalf("empty entry: %q %v", path, err)
	}
	// An invalid file and a relative entry refuse as before and stay unchanged.
	for content, want := range map[string]string{
		`{`:                     "invalid launcher configuration",
		`{"codex":"bin/codex"}`: "configured CLI path must be absolute",
	} {
		broken := filepath.Join(t.TempDir(), "state")
		if _, err := platform.PrivateDir(broken); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(broken, "launchers.json")
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := launchCLI(ctx, Options{Family: "codex", StateDir: broken}, io.Discard); err == nil || err.Error() != want {
			t.Errorf("%s: %v", content, err)
		}
		if data, _ := os.ReadFile(file); string(data) != content {
			t.Errorf("%s changed to %q", content, data)
		}
	}
}

// Racing first launches keep the first recorded CLI: the launch rechecks the entry under the
// lock and never replaces it, while setup still replaces it.
func TestLaunchKeepsRecordedCLIUnderLock(t *testing.T) {
	ctx := context.Background()
	state := filepath.Join(t.TempDir(), "state")
	paths := make([]string, 8)
	for i := range paths {
		paths[i] = testExecutable(t, "codex")
	}
	var mu sync.Mutex
	var winners []string
	var wg sync.WaitGroup
	for _, cli := range paths {
		wg.Go(func() {
			changed, err := recordCLI(ctx, state, "codex", cli, false)
			if err != nil {
				t.Errorf("record %s: %v", cli, err)
			}
			if changed {
				mu.Lock()
				winners = append(winners, cli)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(winners) != 1 || entries(t, state)["codex"] != winners[0] {
		t.Fatalf("winners %v, entry %v", winners, entries(t, state))
	}
	if changed, err := RecordCLI(ctx, state, "codex", paths[0]); err != nil || entries(t, state)["codex"] != paths[0] || changed == (winners[0] == paths[0]) {
		t.Fatalf("setup replacement: %v %v %v", changed, err, entries(t, state))
	}
}

// The built launcher records the CLI it finds on PATH, reports it and starts that CLI; the
// next launch starts the recorded CLI with no CLI on PATH.
func TestLaunchFromPathStartsAndRecords(t *testing.T) {
	root, address, cli, directory := fixture(t)
	bin := t.TempDir()
	script, err := os.ReadFile(cli)
	if err != nil {
		t.Fatal(err)
	}
	found := filepath.Join(bin, "codex")
	if err := os.WriteFile(found, script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	state := filepath.Join(root, "state")
	for i, path := range []string{bin, t.TempDir()} {
		t.Setenv("PATH", path)
		result := filepath.Join(root, "result-"+string(rune('a'+i)))
		cmd := exec.Command(launcherBinary, "codex", "--state-dir", state, "--address", address, "--directory", directory,
			"--", "--result", result, "--family", "codex", "literal ' $() ` ; argument")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("launch %d: %v %s", i, err, stderr.String())
		}
		checkReport(t, readReport(t, result), "codex", directory)
		notice := "koinon: recorded the codex CLI " + found + " in launchers.json\n"
		if strings.Contains(stderr.String(), notice) != (i == 0) {
			t.Fatalf("launch %d report: %q", i, stderr.String())
		}
		if entries(t, state)["codex"] != found {
			t.Fatalf("launch %d entry: %v", i, entries(t, state))
		}
	}
}
