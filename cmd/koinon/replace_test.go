package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// No test here passes a program that would continue the session: replace would then run it
// in place of the test.

func script(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// moveLink points link at target in one rename, as brew upgrade moves the opt link.
func moveLink(t *testing.T, link, target string) {
	t.Helper()
	os.Remove(link + ".new")
	if err := os.Symlink(target, link+".new"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(link+".new", link); err != nil {
		t.Fatal(err)
	}
}

// koinon mcp watches the path it was started by, made absolute, never the file the link
// resolved to at the start: a moved link is a change.
func TestMCPReplacementWatchesTheLaunchPath(t *testing.T) {
	dir := t.TempDir()
	old := script(t, filepath.Join(dir, "old", "koinon"), "exit 0")
	upgraded := script(t, filepath.Join(dir, "new", "koinon"), "exit 0")
	link := filepath.Join(dir, "bin", "koinon")
	os.Mkdir(filepath.Dir(link), 0700)
	previous := launchPath
	defer func() { launchPath = previous }()
	t.Setenv("PATH", filepath.Dir(link)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(dir)
	for _, launch := range []string{"koinon", "./bin/koinon", "bin/koinon", link} {
		moveLink(t, link, old)
		launchPath = func() string { return launch }
		changed, replace := mcpReplacement()
		if changed == nil || replace == nil {
			t.Fatalf("%s: no replacement", launch)
		}
		if changed() {
			t.Fatalf("%s: changed at the start", launch)
		}
		moveLink(t, link, upgraded)
		if !changed() {
			t.Fatalf("%s: the moved link was not seen", launch)
		}
	}
	for _, launch := range []string{"no-such-koinon-command", filepath.Join(dir, "missing")} {
		launchPath = func() string { return launch }
		if changed, replace := mcpReplacement(); changed != nil || replace != nil {
			t.Fatalf("%s: a replacement without a file", launch)
		}
	}
}

// A file that does not continue a running session is tried once and keeps this program; the
// next change is tried again.
func TestMCPReplacementTriesEachFileOnce(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "bin", "koinon")
	os.Mkdir(filepath.Dir(link), 0700)
	moveLink(t, link, script(t, filepath.Join(dir, "old", "koinon"), "exit 0"))
	previous := launchPath
	defer func() { launchPath = previous }()
	launchPath = func() string { return link }
	changed, replace := mcpReplacement()
	moveLink(t, link, script(t, filepath.Join(dir, "older", "koinon"), `printf '{"version":"v0.3.3"}\n'`))
	if !changed() {
		t.Fatal("the change was not seen")
	}
	if err := replace([]byte(`{}`)); err == nil || !strings.Contains(err.Error(), `"v0.3.3" does not continue a running session`) {
		t.Fatalf("replace: %v", err)
	}
	if changed() {
		t.Fatal("the same file was tried again")
	}
	moveLink(t, link, script(t, filepath.Join(dir, "broken", "koinon"), "exit 3"))
	if !changed() {
		t.Fatal("the next change was not seen")
	}
	if err := replace([]byte(`{}`)); err == nil || !strings.Contains(err.Error(), "version command failed") {
		t.Fatalf("replace: %v", err)
	}
}

// Only a program whose version command reports the same resume state version continues.
func TestResumesNeedsTheSameStateVersion(t *testing.T) {
	previous := resumeProbe
	resumeProbe = 300 * time.Millisecond
	defer func() { resumeProbe = previous }()
	dir := t.TempDir()
	if err := resumes(script(t, filepath.Join(dir, "current"), `printf '{"version":"v9","mcp_resume":"1"}\n'`)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"older":     `printf '{"version":"v0.3.3"}\n'`,
		"newer":     `printf '{"version":"v9","mcp_resume":"2"}\n'`,
		"malformed": `printf 'not json\n'`,
		"failing":   `exit 3`,
		"slow":      `exec sleep 5`,
	} {
		started := time.Now()
		if err := resumes(script(t, filepath.Join(dir, name), body)); err == nil {
			t.Fatalf("%s: continues", name)
		}
		if time.Since(started) > 3*time.Second {
			t.Fatalf("%s: the version command was not bounded", name)
		}
	}
}
