//go:build native

package nativetest

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	brew := homebrew(filepath.Join(dir, "brew"))
	brew.pour(t, "../..", "v9.9.8", "")
	brew.link(t, "v9.9.8")
	brewUpgrade(t, dir, brew, "v9.9.8", filepath.Join(string(brew), "bin", "koinon"))
}

// TestHomebrewUpgradeFromRelease starts from the current main release's Homebrew layout: its
// keg, built as the release workflow builds it, and the user-mode install that its formula's
// caveat names. brew upgrade alone then moves the running daemon and koinon on the PATH to
// this checkout's build (#404).
func TestHomebrewUpgradeFromRelease(t *testing.T) {
	dir := tempDir(t)
	source := filepath.Join(dir, "release")
	extractCommit(t, goRelease, source)
	brew := homebrew(filepath.Join(dir, "brew"))
	brew.pour(t, source, goReleaseVersion, goRelease)
	brew.link(t, goReleaseVersion)
	brewUpgrade(t, dir, brew, goReleaseVersion, filepath.Join(string(brew), "opt", "koinon", "bin", "koinon"))
}

// homebrew is a Homebrew prefix: the kegs in Cellar, and the opt and bin links to one of them.
type homebrew string

// pour builds the module at source into the keg of version, with the release workflow's
// flags, and writes the keg's INSTALL_RECEIPT.json, as brew pours a bottle.
func (b homebrew) pour(t *testing.T, source, version, commit string) {
	t.Helper()
	keg := filepath.Join(string(b), "Cellar", "koinon", strings.TrimPrefix(version, "v"))
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version+" -X main.commit="+commit,
		"-o", filepath.Join(keg, "bin", "koinon"), "./cmd/koinon")
	build.Dir, build.Env = source, append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, out)
	}
	if err := os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
}

// link points opt/koinon and bin/koinon at the keg of version with relative links, as brew
// links a keg: each new link is renamed over the old one, and nothing else changes.
func (b homebrew) link(t *testing.T, version string) {
	t.Helper()
	keg := filepath.Join("..", "Cellar", "koinon", strings.TrimPrefix(version, "v"))
	for name, target := range map[string]string{"opt": keg, "bin": filepath.Join(keg, "bin", "koinon")} {
		path := filepath.Join(string(b), name, "koinon")
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.Symlink(target, path+".new"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".new", path); err != nil {
			t.Fatal(err)
		}
	}
}

// brewUpgrade installs the linked keg of from in user mode with installer, then upgrades as
// brew upgrade does: a keg of this checkout's build, v9.9.9, and the links moved to it. With
// no other command, the daemon restarts on the new build, keeps or raises its schema, and
// koinon on the PATH reports the new version, as does the daemon in koinon status.
func brewUpgrade(t *testing.T, dir string, brew homebrew, from, installer string) {
	t.Helper()
	const next = "v9.9.9"
	prefix, state := filepath.Join(dir, "prefix space"), filepath.Join(dir, "state")
	result, err := koinonAt(t, installer, "install", "--prefix", prefix, "--state-dir", state)
	if err != nil {
		t.Fatal(err)
	}
	defer koinon(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	installed := filepath.Join(prefix, "bin", "koinon")
	opt := filepath.Join(string(brew), "opt", "koinon", "bin", "koinon")
	if target, err := os.Readlink(installed); err != nil || target != opt || text(result, "binary_result") != "linked" {
		t.Fatalf("installed binary %q %v: %v", target, err, result)
	}
	// The search path puts the install's link (~/.local/bin/koinon) ahead of brew's bin, as on
	// the machine in #404, where it shadowed the brew binary.
	link := text(result, "link")
	if target, err := os.Readlink(link); err != nil || target != installed {
		t.Fatalf("koinon link %q -> %q %v: %v", link, target, err, result)
	}
	path := strings.Join([]string{filepath.Dir(link), filepath.Join(string(brew), "bin"), os.Getenv("PATH")}, string(os.PathListSeparator))
	if got, err := onPath(t, path, "version"); err != nil || text(got, "version") != from {
		t.Fatalf("version on the PATH before the upgrade %v %v", got, err)
	}
	var before daemonHealth
	waitFor(t, "the "+from+" daemon", func() bool {
		before = health(t, state)
		return before.PID != 0 && before.Version == from
	})

	// brew upgrade: the new keg is poured and linked, and nothing else runs.
	brew.pour(t, "../..", next, "")
	brew.link(t, next)
	var after daemonHealth
	waitFor(t, "the upgraded daemon", func() bool {
		after = health(t, state)
		return after.PID != 0 && after.PID != before.PID && after.Version == next
	})
	if after.Schema < before.Schema {
		t.Fatalf("schema %d after %d", after.Schema, before.Schema)
	}
	if got, err := onPath(t, path, "version"); err != nil || text(got, "version") != next {
		t.Fatalf("version on the PATH after the upgrade %v %v", got, err)
	}
	if got, err := onPath(t, path, "status", "--state-dir", state); err != nil || text(got, "version") != next || number(got, "schema") != int64(after.Schema) {
		t.Fatalf("status %v %v", got, err)
	}
}

// onPath runs koinon as a shell finds it on the search path path.
func onPath(t *testing.T, path string, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", `exec koinon "$@"`, "koinon"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var result map[string]any
	json.Unmarshal(stdout.Bytes(), &result)
	if err != nil {
		return result, fmt.Errorf("koinon %s on the PATH: %v\n%s%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return result, nil
}
