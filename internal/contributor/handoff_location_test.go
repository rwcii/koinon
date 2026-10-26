package contributor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// skillBlock returns the first sh block after heading in a skill, with <agent-id> replaced.
func skillBlock(t *testing.T, skill, heading string) string {
	t.Helper()
	text := read(t, filepath.Join("agents", "skills", skill, "SKILL.md"))
	_, section, ok := strings.Cut(text, heading)
	if !ok {
		t.Fatalf("%s has no %q", skill, heading)
	}
	_, block, ok := strings.Cut(section, "```sh\n")
	if !ok {
		t.Fatalf("%s %q has no sh block", skill, heading)
	}
	block, _, _ = strings.Cut(block, "```")
	return strings.ReplaceAll(block, "<agent-id>", "claude")
}

// runBlock runs a block with bash -e in dir, with Git kept from looking above dir.
func runBlock(t *testing.T, block, dir string) string {
	t.Helper()
	cmd := exec.Command("bash", "-e", "-c", block)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir), "HOME="+t.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("block exited: %v\n%s", err, out)
	}
	return string(out)
}

// The handoff and pickup skills' location blocks run under bash -e in a directory that is
// not a Git checkout, and in a repository (#485).
func TestHandoffLocationBlocksRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	write := skillBlock(t, "handoff", "## 2. Create and check the directory")
	find := skillBlock(t, "pickup", "## 2. Read the progress note, then find the newest handoff")
	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(plain, "_handoff", "claude")
	if out := runBlock(t, write, plain); !strings.Contains(out, "ready: "+dir) || strings.Contains(out, "not ready") {
		t.Fatalf("outside Git: %s", out)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory: %v %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-26-010000.md"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runBlock(t, find, plain); !strings.Contains(out, "newest handoff: "+filepath.Join(dir, "2026-10-26-010000.md")) {
		t.Fatalf("pickup outside Git: %s", out)
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if out := runBlock(t, write, repo); !strings.Contains(out, "ready: "+filepath.Join(repo, "_handoff", "claude")) || strings.Contains(out, "not ready") {
		t.Fatalf("in a repository: %s", out)
	}
	if exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); err != nil || !strings.Contains(string(exclude), "/_handoff/") {
		t.Fatalf("exclude: %q %v", exclude, err)
	}
}
