package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Directory check with real synthetic repositories (#236): a recorded submodule and a
// linked worktree of the start repository are accepted; any other nested repository is not.

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

func TestDirectorySubmodulesAndWorktrees(t *testing.T) {
	base := t.TempDir()
	library := syntheticRepo(t, filepath.Join(base, "library"))
	nestedLibrary := syntheticRepo(t, filepath.Join(base, "nested-library"))
	gitRun(t, library, "submodule", "add", "-q", nestedLibrary, "deep")
	gitRun(t, library, "commit", "-q", "-m", "deep submodule")
	root := syntheticRepo(t, filepath.Join(base, "root"))
	gitRun(t, root, "submodule", "add", "-q", library, "vendor/library")
	gitRun(t, root, "submodule", "update", "-q", "--init", "--recursive")
	gitRun(t, root, "commit", "-q", "-m", "submodule")
	gitRun(t, root, "worktree", "add", "-q", "-b", "synthetic", filepath.Join(root, ".worktrees", "synthetic"))
	for _, gitFile := range []string{"vendor/library/.git", "vendor/library/deep/.git", ".worktrees/synthetic/.git"} {
		if info, err := os.Lstat(filepath.Join(root, gitFile)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s is not a .git file: %v", gitFile, err)
		}
	}
	// A recorded submodule (also one inside it) and a nested linked worktree are accepted,
	// from the top level and from a subdirectory.
	for _, start := range []string{root, filepath.Join(root, "vendor")} {
		if _, err := CheckDirectory(start); err != nil {
			t.Fatalf("%s: %v", start, err)
		}
	}
	refused := func(label string) {
		t.Helper()
		if _, err := CheckDirectory(root); err == nil || !strings.Contains(err.Error(), "holds another repository") {
			t.Fatalf("%s accepted: %v", label, err)
		}
	}
	// A .git file at a path the repository does not record, even into its modules/.
	unrecorded := filepath.Join(root, "unrecorded")
	os.Mkdir(unrecorded, 0700)
	os.WriteFile(filepath.Join(unrecorded, ".git"), []byte("gitdir: "+filepath.Join(root, ".git", "modules", "vendor", "library")+"\n"), 0600)
	refused("unrecorded .git file")
	os.RemoveAll(unrecorded)
	// A worktree of another repository.
	gitRun(t, library, "worktree", "add", "-q", "-b", "elsewhere", filepath.Join(root, "foreign-worktree"))
	refused("worktree of another repository")
	gitRun(t, library, "worktree", "remove", "--force", filepath.Join(root, "foreign-worktree"))
	// A repository with its Git directory elsewhere.
	gitRun(t, root, "init", "-q", "--separate-git-dir="+filepath.Join(base, "separate.git"), "separate")
	refused("separate Git directory")
	os.RemoveAll(filepath.Join(root, "separate"))
	// A nested .git directory, and a nested .git symlink.
	syntheticRepo(t, filepath.Join(root, "inner"))
	refused("nested .git directory")
	os.RemoveAll(filepath.Join(root, "inner"))
	os.Mkdir(filepath.Join(root, "linked"), 0700)
	os.Symlink(filepath.Join(root, ".worktrees", "synthetic", ".git"), filepath.Join(root, "linked", ".git"))
	refused("nested .git symlink")
	os.RemoveAll(filepath.Join(root, "linked"))
	// Without the exceptions in place the start directory is accepted again.
	if _, err := CheckDirectory(root); err != nil {
		t.Fatal(err)
	}
}

// TestDirectoryExactGitlink: only the exact recorded path counts. An unrecorded directory
// whose name is a pathspec pattern, or one above a recorded gitlink, is refused even when its
// .git file points at the retained submodule directory.
func TestDirectoryExactGitlink(t *testing.T) {
	for _, c := range []struct{ candidate, recorded string }{{"record*", "recorded"}, {"bucket", "bucket/recorded"}} {
		t.Run(c.candidate, func(t *testing.T) {
			base := t.TempDir()
			library := syntheticRepo(t, filepath.Join(base, "library"))
			root := syntheticRepo(t, filepath.Join(base, "root"))
			gitRun(t, root, "submodule", "add", "-q", library, c.recorded)
			gitRun(t, root, "commit", "-q", "-m", "submodule")
			gitRun(t, root, "submodule", "deinit", "-q", "-f", "--", c.recorded)
			dir := filepath.Join(root, c.candidate)
			os.MkdirAll(dir, 0700)
			gitDir := filepath.Join(root, ".git", "modules", filepath.FromSlash(c.recorded))
			os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0600)
			if _, err := CheckDirectory(root); err == nil {
				t.Fatalf("unrecorded .git at %q accepted", c.candidate)
			}
		})
	}
}

// TestDirectorySubmoduleInLinkedWorktree: Git keeps an initialized submodule of a linked
// worktree under that worktree's own Git directory; it is accepted, in a worktree outside
// the start directory and in one nested inside it.
func TestDirectorySubmoduleInLinkedWorktree(t *testing.T) {
	base := t.TempDir()
	library := syntheticRepo(t, filepath.Join(base, "library"))
	root := syntheticRepo(t, filepath.Join(base, "root"))
	gitRun(t, root, "submodule", "add", "-q", library, "module")
	gitRun(t, root, "commit", "-q", "-m", "submodule")
	for _, worktree := range []string{filepath.Join(base, "linked"), filepath.Join(root, ".worktrees", "nested")} {
		gitRun(t, root, "worktree", "add", "-q", "-b", filepath.Base(worktree), worktree)
		gitRun(t, worktree, "submodule", "update", "-q", "--init")
		data, err := os.ReadFile(filepath.Join(worktree, "module", ".git"))
		if err != nil || !strings.Contains(string(data), filepath.Join("worktrees", filepath.Base(worktree), "modules")) {
			t.Fatalf("%s: submodule not under the worktree's Git directory: %q %v", worktree, data, err)
		}
		if _, err := CheckDirectory(worktree); err != nil {
			t.Fatalf("%s: %v", worktree, err)
		}
	}
	if _, err := CheckDirectory(root); err != nil {
		t.Fatalf("start with a nested worktree and its submodule: %v", err)
	}
}
