package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// Nested repository scan with real synthetic repositories (#252): every nested repository
// except a linked worktree of the start repository is reported with its kind, and none
// refuses the start.

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=synthetic", "-c", "user.email=synthetic@example.com",
		"-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func syntheticRepo(t *testing.T, dir string) string {
	t.Helper()
	gitRun(t, filepath.Dir(dir), "init", "-q", filepath.Base(dir))
	os.WriteFile(filepath.Join(dir, "file"), []byte("synthetic"), 0600)
	gitRun(t, dir, "add", "file")
	gitRun(t, dir, "commit", "-q", "-m", "synthetic")
	return dir
}

func scanned(t *testing.T, dir string) map[string]string {
	t.Helper()
	report := ScanNested(dir)
	if report.Incomplete {
		t.Fatalf("%s: incomplete scan: %+v", dir, report)
	}
	kinds := map[string]string{}
	for _, n := range report.List {
		kinds[filepath.ToSlash(n.Path)] = n.Kind
	}
	return kinds
}

func TestNestedKinds(t *testing.T) {
	base := t.TempDir()
	library := syntheticRepo(t, filepath.Join(base, "library"))
	nestedLibrary := syntheticRepo(t, filepath.Join(base, "nested-library"))
	gitRun(t, library, "submodule", "add", "-q", nestedLibrary, "deep")
	gitRun(t, library, "commit", "-q", "-m", "deep submodule")
	root := syntheticRepo(t, filepath.Join(base, "root"))
	// An absorbed submodule (.git file into modules/), with its own submodule.
	gitRun(t, root, "submodule", "add", "-q", library, "vendor/library")
	gitRun(t, root, "submodule", "update", "-q", "--init", "--recursive")
	// A submodule whose .git is a directory: a clone recorded as a gitlink.
	gitRun(t, root, "clone", "-q", library, "embedded")
	gitRun(t, root, "-c", "advice.addEmbeddedRepo=false", "add", "embedded")
	gitRun(t, root, "commit", "-q", "-m", "submodules")
	if info, err := os.Lstat(filepath.Join(root, "embedded", ".git")); err != nil || !info.IsDir() {
		t.Fatalf("embedded submodule has no .git directory: %v", err)
	}
	// A linked worktree of the start repository, with an initialized submodule.
	gitRun(t, root, "worktree", "add", "-q", "-b", "synthetic", filepath.Join(root, ".worktrees", "synthetic"))
	gitRun(t, filepath.Join(root, ".worktrees", "synthetic"), "submodule", "update", "-q", "--init", "--", "vendor/library")
	// A linked worktree of another repository, a separate Git directory, a separate clone,
	// a .git symlink, and an unrecorded .git file into the start repository's modules/.
	gitRun(t, library, "worktree", "add", "-q", "-b", "elsewhere", filepath.Join(root, "foreign-worktree"))
	gitRun(t, root, "init", "-q", "--separate-git-dir="+filepath.Join(base, "separate.git"), "separate")
	os.Mkdir(filepath.Join(root, "tasks"), 0700)
	syntheticRepo(t, filepath.Join(root, "tasks", "clone"))
	// A repository inside another repository belongs to that one and is not listed.
	syntheticRepo(t, filepath.Join(root, "tasks", "clone", "inner"))
	os.Mkdir(filepath.Join(root, "linked"), 0700)
	os.Symlink(filepath.Join(root, ".worktrees", "synthetic", ".git"), filepath.Join(root, "linked", ".git"))
	os.Mkdir(filepath.Join(root, "unrecorded"), 0700)
	os.WriteFile(filepath.Join(root, "unrecorded", ".git"), []byte("gitdir: "+filepath.Join(root, ".git", "modules", "vendor", "library")+"\n"), 0600)
	// A directory symlink to another repository is not followed.
	os.Symlink(syntheticRepo(t, filepath.Join(base, "outside")), filepath.Join(root, "outside-link"))

	want := map[string]string{
		"vendor/library":                      "submodule",
		"vendor/library/deep":                 "submodule",
		"embedded":                            "submodule",
		".worktrees/synthetic/vendor/library": "submodule",
		"foreign-worktree":                    "worktree",
		"separate":                            "repository",
		"tasks/clone":                         "repository",
		"linked":                              "repository",
		"unrecorded":                          "repository",
	}
	if got := scanned(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("nested kinds:\n got %v\nwant %v", got, want)
	}
	// From a subdirectory, paths are relative to the start directory.
	if got := scanned(t, filepath.Join(root, "vendor")); !reflect.DeepEqual(got, map[string]string{"library": "submodule", "library/deep": "submodule"}) {
		t.Fatalf("subdirectory scan: %v", got)
	}
	// The start repository's own worktree lists the start's other nested repositories, never itself.
	if got := scanned(t, filepath.Join(root, ".worktrees", "synthetic")); !reflect.DeepEqual(got, map[string]string{"vendor/library": "submodule"}) {
		t.Fatalf("worktree scan: %v", got)
	}
	// A directory outside any repository reports every nested repository.
	plain := t.TempDir()
	syntheticRepo(t, filepath.Join(plain, "one"))
	if got := scanned(t, plain); !reflect.DeepEqual(got, map[string]string{"one": "repository"}) {
		t.Fatalf("plain directory scan: %v", got)
	}
}

func TestNestedScanBounds(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0700)
	for i := 0; i <= core.MaxNested; i++ {
		os.MkdirAll(filepath.Join(root, "many", strings.Repeat("r", i+1), ".git"), 0700)
	}
	report := ScanNested(root)
	if !report.Incomplete || len(report.List) != core.MaxNested {
		t.Fatalf("count bound: %d entries, incomplete %v", len(report.List), report.Incomplete)
	}
	os.RemoveAll(filepath.Join(root, "many"))
	long := filepath.Join(root, strings.Repeat("d", 200), strings.Repeat("e", 60))
	os.MkdirAll(filepath.Join(long, ".git"), 0700)
	if report := ScanNested(root); !report.Incomplete || len(report.List) != 0 {
		t.Fatalf("path bound: %+v", report)
	}
	os.RemoveAll(filepath.Join(root, strings.Repeat("d", 200)))
	saved := scanTime
	scanTime = 0
	report = ScanNested(root)
	scanTime = saved
	if !report.Incomplete {
		t.Fatal("time bound not reported")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	locked := filepath.Join(root, "locked")
	os.MkdirAll(filepath.Join(locked, "inner", ".git"), 0700)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0700) })
	if report := ScanNested(root); !report.Incomplete || len(report.List) != 0 {
		t.Fatalf("unreadable subtree: %+v", report)
	}
	os.Chmod(locked, 0700)
	if report := ScanNested(root); report.Incomplete || len(report.List) != 1 {
		t.Fatalf("readable again: %+v", report)
	}
	if notice := NestedNotice(root, ScanNested(root)); !strings.Contains(notice, "1 nested repositories") || !strings.Contains(notice, filepath.Join("locked", "inner")+" (repository)") {
		t.Fatalf("notice: %q", notice)
	}
	if NestedNotice(root, core.NestedReport{}) != "" {
		t.Fatal("notice without nested repositories")
	}
}

// A nested path that a launch record cannot hold is left out and marks the list incomplete,
// so it never refuses the launch (#255 review).
func TestNestedUnrepresentablePath(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0700)
	os.MkdirAll(filepath.Join(root, "line\nbreak", ".git"), 0700)
	os.MkdirAll(filepath.Join(root, "plain", ".git"), 0700)
	report := ScanNested(root)
	if !report.Incomplete || !reflect.DeepEqual(report.List, []core.NestedRepository{{Path: "plain", Kind: "repository"}}) {
		t.Fatalf("unrepresentable path: %+v", report)
	}
}

// One deadline bounds the whole scan, Git queries included: with the production budget and a
// Git that answers each query after 0.8 seconds, the scan ends near the budget and reports the
// list incomplete (#255 review).
func TestNestedScanDeadlineCoversGit(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"+sleep+" 0.8\nexit 1\n"), 0700)
	t.Setenv("PATH", bin)
	root := t.TempDir()
	// One nested entry: the walk ends after it, so only the shared deadline marks it incomplete.
	os.Mkdir(filepath.Join(root, "one"), 0700)
	os.WriteFile(filepath.Join(root, "one", ".git"), []byte("gitdir: synthetic\n"), 0600)
	began := time.Now()
	report := ScanNested(root)
	if elapsed := time.Since(began); elapsed > scanTime+500*time.Millisecond {
		t.Fatalf("scan took %v with a %v budget", elapsed, scanTime)
	}
	if !report.Incomplete {
		t.Fatalf("over-budget scan reported complete: %+v", report)
	}
}
