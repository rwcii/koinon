package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The recorded tmux (#418): koinon mcp reports the absolute tmux of the agent's own terminal
// with its pane, and the daemon runs it after checking it, whatever its own PATH holds.

// launchdPath is the PATH of a macOS LaunchDaemon, which holds no Homebrew directory.
const launchdPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// fakeTmux installs a tmux under dir as Homebrew does: bin/tmux is a link to the program in
// cellar/. The program records each run in marker, then runs real, or prints its arguments
// when real is "". mode is set after the write, so the umask does not change it.
func fakeTmux(t *testing.T, dir, real string, mode os.FileMode) (link, marker string) {
	t.Helper()
	cellar, bin := filepath.Join(dir, "cellar", "tmux", "bin"), filepath.Join(dir, "bin")
	for _, d := range []string{cellar, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker = filepath.Join(dir, "runs")
	script := "#!/bin/sh\nprintf 'run\\n' >> '" + marker + "'\n"
	if real != "" {
		script += "exec '" + real + "' \"$@\"\n"
	} else {
		script += "printf 'synthetic tmux %s\\n' \"$*\"\n"
	}
	program := filepath.Join(cellar, "tmux")
	if err := os.WriteFile(program, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(program, mode); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(bin, "tmux")
	if err := os.Symlink(filepath.Join("..", "cellar", "tmux", "bin", "tmux"), link); err != nil {
		t.Fatal(err)
	}
	return link, marker
}

// unsafeTmux is a fake tmux under dir that its group can write.
func unsafeTmux(t *testing.T, dir, real string) string {
	t.Helper()
	sub, err := os.MkdirTemp(dir, "unsafe-")
	if err != nil {
		t.Fatal(err)
	}
	link, _ := fakeTmux(t, sub, real, 0o775)
	return link
}

func runs(t *testing.T, marker string) int {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(data), "run\n")
}

// A pane's tmux must be absolute and clean; a registration with another is invalid.
func TestTmuxPaneExecutableIsChecked(t *testing.T) {
	for path, want := range map[string]bool{
		"":                              true,
		"/opt/homebrew/bin/tmux":        true,
		"tmux":                          false,
		"bin/tmux":                      false,
		"/opt/homebrew/../bin/tmux":     false,
		"/opt/homebrew/bin/":            false,
		"/opt/homebrew/bin/tmux\n":      false,
		"/opt/homebrew/bin/tmux\x7f":    false,
		"/" + strings.Repeat("a", 4096): false,
	} {
		if got := (&TmuxPane{Socket: "/tmp/tmux-1000/default", Pane: "%1", Executable: path}).valid(); got != want {
			t.Errorf("%q: valid %v, want %v", path, got, want)
		}
	}
	s, _ := testStore(t)
	_, err := s.Register(context.Background(), Registration{Family: "codex", ID: "synthetic-a", Directory: t.TempDir(), TTLSeconds: 60,
		Tmux: &TmuxPane{Socket: "/tmp/tmux-1000/default", Pane: "%1", Executable: "tmux"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative tmux registered: %v", err)
	}
}

// The daemon runs the recorded tmux under the minimal launchd PATH. Without one, a tmux that
// is not on PATH, and a recorded tmux that fails the check, give tmux_unavailable and never
// run; a tmux that ran and failed does not.
func TestRunTmuxRunsTheRecordedExecutable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	link, marker := fakeTmux(t, filepath.Join(dir, "homebrew"), "", 0o755)
	t.Setenv("PATH", launchdPath)
	out, err := runTmux(ctx, link, "/synthetic/socket", "list-panes")
	if err != nil || out != "synthetic tmux -S /synthetic/socket list-panes\n" || runs(t, marker) != 1 {
		t.Fatalf("recorded tmux: %q %v, runs %d", out, err, runs(t, marker))
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", empty)
	if _, err := runTmux(ctx, "", "/synthetic/socket", "list-panes"); !errors.Is(err, errTmuxUnavailable) || err.Error() != "tmux_unavailable" {
		t.Fatalf("no tmux on PATH: %v", err)
	}
	notRegular := filepath.Join(dir, "directory", "tmux")
	if err := os.MkdirAll(notRegular, 0o755); err != nil {
		t.Fatal(err)
	}
	refused := map[string]string{
		"relative":           "homebrew/bin/tmux",
		"unclean":            filepath.Join(dir, "homebrew", "bin") + "/../bin/tmux",
		"missing":            filepath.Join(dir, "missing", "tmux"),
		"not a regular file": notRegular,
	}
	markers := map[string]string{}
	for name, mode := range map[string]os.FileMode{"group writable": 0o775, "world writable": 0o757, "not executable": 0o644} {
		refused[name], markers[name] = fakeTmux(t, filepath.Join(dir, strings.ReplaceAll(name, " ", "-")), "", mode)
	}
	for name, path := range refused {
		if _, err := runTmux(ctx, path, "/synthetic/socket", "list-panes"); !errors.Is(err, errTmuxUnavailable) || err.Error() != "tmux_unavailable" {
			t.Errorf("%s: %v", name, err)
		}
		if markers[name] != "" && runs(t, markers[name]) != 0 {
			t.Errorf("%s: the refused tmux ran", name)
		}
	}
	failing := filepath.Join(dir, "failing")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if _, err := runTmux(ctx, failing, "/synthetic/socket", "list-panes"); errors.Is(err, errTmuxUnavailable) || !errors.As(err, &exit) {
		t.Fatalf("a tmux that ran and failed: %v", err)
	}
}

// A launched Codex participant under the minimal launchd PATH (#418). With no tmux to run, the
// message stays queued as tmux_unavailable and nothing reaches the pane; a recorded tmux that
// fails the check is refused the same way; once the registration records the agent's tmux,
// outside that PATH, the idle participant gets its content-free notice through it.
func TestCodexWakeRunsTheRecordedTmux(t *testing.T) {
	f := newCodexFixture(t)
	ctx := context.Background()
	p := f.pane("idle", "")
	if out, err := exec.Command("git", "init", "-q", f.dir).CombinedOutput(); err != nil {
		t.Fatalf("repository: %v %s", err, out)
	}
	clock := time.Now()
	f.s.now = func() time.Time { return clock }
	launch, err := f.s.CreateLaunch(ctx, LaunchTarget{Family: "codex", Directory: f.dir, CLI: "/synthetic/codex", HostPID: p.host})
	if err != nil {
		t.Fatal(err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	register := func(executable string) Session {
		t.Helper()
		receiver, err := f.s.Register(ctx, Registration{Family: "codex", ID: id, Repository: f.dir, Directory: f.dir, TTLSeconds: 3600,
			LaunchID: launch, Ancestors: []int{p.host}, Tmux: &TmuxPane{Socket: f.socket, Pane: p.id, Executable: executable}})
		if err != nil || !receiver.HoldsAddress {
			t.Fatalf("launched receiver: %v %+v", err, receiver)
		}
		var recorded string
		if err := f.s.db.QueryRow(`SELECT tmux_path FROM sessions WHERE family='codex' AND id=?`, id).Scan(&recorded); err != nil || recorded != executable {
			t.Fatalf("recorded tmux %q, want %q: %v", recorded, executable, err)
		}
		return receiver
	}
	// The daemon runs Git to register a session; only the wakes run under the changed PATH.
	path := os.Getenv("PATH")
	receiver := register("")
	sender, err := f.s.Register(ctx, Registration{Family: "deepseek", ID: "synthetic-sender", Directory: f.dir, TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Address, "synthetic payload")
	if err != nil {
		t.Fatal(err)
	}
	f.s.wake.send = f.s.providerWake
	// wake observes the receiver idle and runs one scheduler step.
	wake := func() wakeRow {
		t.Helper()
		if err := f.s.Observe(ctx, Observation{Caller: Key{"codex", id},
			Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: "idle"}}); err != nil {
			t.Fatal(err)
		}
		step(t, f.s)
		return messageRow(t, f.s, message.ID)
	}
	// No tmux on the daemon's PATH, and none recorded: the message stays queued.
	empty := filepath.Join(f.dir, "empty-path")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", empty)
	if got := wake(); got.state != "waiting" || got.wake != "tmux_unavailable" || got.notices != 0 {
		t.Fatalf("without tmux: %+v", got)
	}
	f.untouched(p)
	// A recorded tmux that its group can write is refused.
	t.Setenv("PATH", path)
	register(unsafeTmux(t, f.dir, f.tmuxPath))
	t.Setenv("PATH", empty)
	clock = clock.Add(wakeBackoffMax + time.Millisecond)
	if got := wake(); got.state != "waiting" || got.wake != "tmux_unavailable" || got.notices != 0 {
		t.Fatalf("unsafe tmux: %+v", got)
	}
	f.untouched(p)
	// The registration records the agent's tmux, outside the minimal launchd PATH.
	t.Setenv("PATH", path)
	link, marker := fakeTmux(t, filepath.Join(f.dir, "homebrew"), f.tmuxPath, 0o755)
	register(link)
	t.Setenv("PATH", launchdPath)
	clock = clock.Add(wakeBackoffMax + time.Millisecond)
	if got := wake(); got.state != "notified" || got.notices != 1 {
		t.Fatalf("recorded tmux: %+v\n%s", got, f.run("capture-pane", "-p", "-t", p.id))
	}
	received := f.received(p)
	if !strings.HasPrefix(received, `Koinon inbox "codex":"participant:`) || strings.Contains(received, "synthetic payload") || runs(t, marker) == 0 {
		t.Fatalf("notice %q through the recorded tmux, runs %d", received, runs(t, marker))
	}
}

// A schema 27 store gains the recorded tmux column without changing its sessions or their
// host records; a schema 27 writer refuses the upgraded store.
func TestSchemaTwentyEightKeepsSessions(t *testing.T) {
	s, root := testStore(t)
	join(t, s, "codex", "synthetic-a", "")
	join(t, s, "claude", "synthetic-b", "")
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=7,tmux_socket='/synthetic/tmux/default',tmux_pane='%3'
		WHERE family='codex'`); err != nil {
		t.Fatal(err)
	}
	hosts := func(store *Store) string {
		t.Helper()
		var text string
		if err := store.db.QueryRow(`SELECT group_concat(family||id||host_pid||host_start||tmux_socket||tmux_pane||revision, '|')
			FROM (SELECT * FROM sessions ORDER BY family,id)`).Scan(&text); err != nil {
			t.Fatal(err)
		}
		return text
	}
	before, rows := hosts(s), rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaThirtyTwo+undoSchemaThirtyOne+undoSchemaThirty+undoSchemaTwentyNine+undoSchemaTwentyEight+`PRAGMA user_version=27`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 27); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || hosts(upgraded) != before || rowsOf(t, upgraded.db) != rows ||
		count(t, upgraded, `SELECT COUNT(*) FROM sessions WHERE tmux_path!=''`) != 0 {
		t.Fatal("schema 28 changed a session")
	}
	if err := checkCatalog(upgraded.db, 27); err == nil {
		t.Fatal("a schema 27 writer would accept the schema 28 store")
	}
}
