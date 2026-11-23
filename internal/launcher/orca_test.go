package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// orcaWorktrees makes a repository with two linked worktrees, as Orca gives one worktree to
// each pair, and returns the two worktree directories.
func orcaWorktrees(t *testing.T, root string) (string, string) {
	t.Helper()
	repository := filepath.Join(root, "orca-repo")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=synthetic", "-c", "user.email=synthetic@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.Mkdir(repository, 0700); err != nil {
		t.Fatal(err)
	}
	git(repository, "init", "-q")
	git(repository, "commit", "-q", "--allow-empty", "-m", "synthetic")
	first, second := filepath.Join(root, "pair-1"), filepath.Join(root, "pair-2")
	git(repository, "worktree", "add", "-q", "--detach", first)
	git(repository, "worktree", "add", "-q", "--detach", second)
	return first, second
}

// orcaEnvironment sets the variables of a host terminal such as Orca's. They change neither
// the address, nor the tmux session name, nor the registration (#795 chunk 02).
func orcaEnvironment(t *testing.T) {
	t.Setenv("ORCA_TERMINAL_ID", "synthetic-terminal")
	t.Setenv("ORCA_WORKTREE_ID", "synthetic-worktree")
	t.Setenv("TERM_PROGRAM", "synthetic-orca")
	t.Setenv("COLORTERM", "truecolor")
}

func checkPair(t *testing.T, r report, family, directory, role, id string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	address := family + "-orca-repo-" + role
	if r.Directory != canonical || len(r.LaunchID) != 64 || r.Session.ID != id || r.Session.Role != role ||
		r.Session.Address != address || !r.Session.HoldsAddress {
		t.Fatalf("pair %s: want address %s in %s, got %+v", role, address, canonical, r.Session)
	}
}

// Two pairs in two linked worktrees of one repository, each started with --role as an Orca
// terminal command, register distinct addresses with the label of the Git common directory.
// TMUX set by the host terminal keeps each start in its pane.
func TestOrcaPairsInWorktreesInPane(t *testing.T) {
	for _, family := range []string{"claude", "codex"} {
		t.Run(family, func(t *testing.T) {
			root, address, cli, _ := fixture(t)
			first, second := orcaWorktrees(t, root)
			orcaEnvironment(t)
			t.Setenv("TMUX", filepath.Join(root, "unused-socket")+",1,0")
			t.Setenv("PATH", filepath.Join(root, "no-executables")+string(os.PathListSeparator)+os.Getenv("PATH"))
			for i, pair := range []struct{ directory, role string }{{first, "p1-driver"}, {second, "p2-driver"}} {
				result := filepath.Join(root, fmt.Sprintf("result-%d.json", i))
				id := fmt.Sprintf("synthetic-%s-%d", family, i)
				cmd := exec.Command(launcherBinary, family, "--state-dir", filepath.Join(root, "state"), "--address", address, "--cli", cli,
					"--directory", pair.directory, "--role", pair.role, "--", "--result", result, "--family", family, "--id", id, "--repository", "yes")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("launch: %v %s", err, out)
				}
				r := readReport(t, result)
				if r.PID != cmd.Process.Pid {
					t.Fatal("a start in the host pane introduced another process")
				}
				checkPair(t, r, family, pair.directory, pair.role, id)
			}
		})
	}
}

// With TMUX unset, as in a host terminal that is not tmux, each start makes a tmux session
// named for its address with the role, and attaches it.
func TestOrcaPairsInWorktreesNewTmux(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("tmux required in CI")
		}
		t.Skip("tmux not installed")
	}
	for _, family := range []string{"claude", "codex"} {
		t.Run(family, func(t *testing.T) {
			root, address, cli, _ := fixture(t)
			first, second := orcaWorktrees(t, root)
			orcaEnvironment(t)
			// The socket directory is under /tmp: a long TMPDIR would exceed the AF_UNIX path
			// bound on macOS (#426).
			socketDir, err := os.MkdirTemp("/tmp", "kl-tmux-")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(socketDir, "s")
			t.Cleanup(func() { exec.Command(realTmux, "-S", socket, "kill-server").Run(); os.RemoveAll(socketDir) })
			wrapperDir := filepath.Join(root, "tools")
			os.Mkdir(wrapperDir, 0700)
			calls := filepath.Join(root, "calls")
			wrapper := "#!/bin/sh\nprintf '%s ' \"$@\" >> " + quote(calls) + "; echo >> " + quote(calls) +
				"\nif [ \"$1\" = attach-session ]; then exit 0; fi\nexec " + quote(realTmux) + " -S " + quote(socket) + " -f /dev/null \"$@\"\n"
			os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte(wrapper), 0700)
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+filepath.Dir(git))
			t.Setenv("TMUX", "")
			for i, pair := range []struct{ directory, role string }{{first, "p1-driver"}, {second, "p2-driver"}} {
				result := filepath.Join(root, fmt.Sprintf("result-%d.json", i))
				id := fmt.Sprintf("synthetic-%s-%d", family, i)
				out, err := exec.Command(launcherBinary, family, "--state-dir", filepath.Join(root, "state"), "--address", address, "--cli", cli,
					"--directory", pair.directory, "--role", pair.role, "--", "--result", result, "--family", family, "--id", id, "--repository", "yes").CombinedOutput()
				if err != nil {
					t.Fatalf("launch: %v %s", err, out)
				}
				checkPair(t, readReport(t, result), family, pair.directory, pair.role, id)
				data, _ := os.ReadFile(calls)
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				name := family + "-orca-repo-" + pair.role
				if len(lines) < 2 || !strings.Contains(lines[len(lines)-2], "new-session -d -P -F #{session_id} #{pane_id} -s "+name+" ") ||
					!strings.HasPrefix(lines[len(lines)-1], "attach-session -t $") {
					t.Fatalf("did not start and attach %s:\n%s", name, data)
				}
			}
		})
	}
}
