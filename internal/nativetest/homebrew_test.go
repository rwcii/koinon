//go:build native

package nativetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHomebrewUpgrade installs from a Homebrew keg layout and upgrades as brew upgrade does:
// a new keg, and the opt link moved to it. Without another koinon command, the service
// manager runs the new release, and the installed path reports it (#397).
func TestHomebrewUpgrade(t *testing.T) {
	dir := tempDir(t)
	brew := filepath.Join(dir, "brew")
	build := func(version string) string {
		t.Helper()
		keg := filepath.Join(brew, "Cellar", "koinon", strings.TrimPrefix(version, "v"))
		path := filepath.Join(keg, "bin", "koinon")
		cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", path, "../../cmd/koinon")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", version, err, out)
		}
		if err := os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		return keg
	}
	// link points name at target by renaming a new link over it, as brew links a keg.
	link := func(target, name string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(name), 0755)
		if err := os.Symlink(target, name+".new"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(name+".new", name); err != nil {
			t.Fatal(err)
		}
	}
	old, next := build("v9.9.8"), build("v9.9.9")
	link(old, filepath.Join(brew, "opt", "koinon"))
	link(filepath.Join(old, "bin", "koinon"), filepath.Join(brew, "bin", "koinon"))

	prefix, state := filepath.Join(dir, "prefix space"), filepath.Join(dir, "state")
	result, err := koinonAt(t, filepath.Join(brew, "bin", "koinon"), "install", "--prefix", prefix, "--state-dir", state)
	if err != nil {
		t.Fatal(err)
	}
	defer koinon(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	installed := filepath.Join(prefix, "bin", "koinon")
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	if target, err := os.Readlink(installed); err != nil || target != opt || text(result, "binary_result") != "linked" {
		t.Fatalf("installed binary %q %v: %v", target, err, result)
	}
	var before daemonHealth
	waitFor(t, "the first release's daemon", func() bool {
		before = health(t, state)
		return before.PID != 0 && before.Version == "v9.9.8"
	})

	// brew upgrade: the new keg is linked, and nothing else runs.
	link(next, filepath.Join(brew, "opt", "koinon"))
	link(filepath.Join(next, "bin", "koinon"), filepath.Join(brew, "bin", "koinon"))
	var after daemonHealth
	waitFor(t, "the upgraded daemon", func() bool {
		after = health(t, state)
		return after.PID != 0 && after.PID != before.PID && after.Version == "v9.9.9"
	})
	if after.Schema < before.Schema {
		t.Fatalf("schema %d after %d", after.Schema, before.Schema)
	}
	if got, err := koinonAt(t, installed, "version"); err != nil || text(got, "version") != "v9.9.9" {
		t.Fatalf("installed version %v %v", got, err)
	}
	if got, err := koinonAt(t, installed, "status", "--state-dir", state); err != nil || number(got, "schema") != int64(after.Schema) {
		t.Fatalf("status %v %v", got, err)
	}
}
