package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// TestCodexHookMergePreservesConfiguration: setup adds its SessionStart group to the Codex
// hooks file and keeps every other hook, member order and the file mode; a repeat changes
// nothing; a group for another koinon binary is replaced; removal takes out only its own
// group; a file that is not in Codex's format is refused and left as it is.
func TestCodexHookMergePreservesConfiguration(t *testing.T) {
	cli, _, _ := syntheticCLI(t, "codex")
	root := t.TempDir()
	path := filepath.Join(root, "hooks.json")
	original := `{
  "comment": "kept",
  "hooks": {
    "PreToolUse": [{"matcher": "shell", "hooks": [{"type": "command", "command": "./check.sh"}]}],
    "SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "./greet.sh"}]},
      {"hooks": [{"type": "command", "command": "'/old/koinon' hook codex-session-start", "timeout": 10}]}]
  }
}`
	os.WriteFile(path, []byte(original), 0640)
	o := Options{Family: "codex", CLI: cli, Binary: "/opt/koinon/koinon", CodexHome: root}
	report, err := Run(context.Background(), o)
	if err != nil || !strings.Contains(report.Note, "trust") {
		t.Fatalf("setup: %+v %v", report, err)
	}
	data, _ := os.ReadFile(path)
	var config struct {
		Comment string
		Hooks   map[string][]struct {
			Matcher string
			Hooks   []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	if err := json.Unmarshal(data, &config); err != nil || config.Comment != "kept" || len(config.Hooks["PreToolUse"]) != 1 {
		t.Fatalf("merged: %s %v", data, err)
	}
	start := config.Hooks["SessionStart"]
	if len(start) != 2 || start[0].Hooks[0].Command != "./greet.sh" || start[1].Hooks[0].Command != `'/opt/koinon/koinon' hook codex-session-start` ||
		start[1].Hooks[0].Timeout != 10 {
		t.Fatalf("SessionStart: %s", data)
	}
	if bytes.Index(data, []byte("comment")) > bytes.Index(data, []byte(`"hooks"`)) || bytes.Index(data, []byte("PreToolUse")) > bytes.Index(data, []byte("SessionStart")) {
		t.Fatalf("order changed: %s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
		t.Fatalf("mode: %v", info.Mode())
	}
	again, err := Run(context.Background(), o)
	if after, _ := os.ReadFile(path); err != nil || len(again.Changed) != 0 || !bytes.Equal(after, data) {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if _, err := Remove(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	removed, _ := os.ReadFile(path)
	if strings.Contains(string(removed), "codex-session-start") || !strings.Contains(string(removed), "./greet.sh") || !strings.Contains(string(removed), "./check.sh") {
		t.Fatalf("removal: %s", removed)
	}
	// A file without other hooks becomes an empty object again.
	empty := filepath.Join(t.TempDir(), "codex")
	o.CodexHome = empty
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(empty, "hooks.json")); strings.TrimSpace(string(data)) != "{\n}" {
		t.Fatalf("emptied file: %q", data)
	}
	for _, broken := range []string{`{"broken": `, `{"hooks": []}`, `{"hooks": {"SessionStart": {}}}`} {
		os.WriteFile(path, []byte(broken), 0600)
		o.CodexHome = root
		if _, err := Run(context.Background(), o); err == nil {
			t.Fatalf("%s accepted", broken)
		}
		if data, _ := os.ReadFile(path); string(data) != broken {
			t.Fatalf("invalid file changed: %s", data)
		}
	}
}

// clearFixture is a daemon with a Git repository and a cleared Codex thread that holds the
// repository's participant. This test process stands in for the Codex host: it is the
// launch's host process and the parent of a hook that the test runs.
type clearFixture struct {
	root, repo, address string
	secret              string
}

func newClearFixture(t *testing.T) clearFixture {
	t.Helper()
	f := clearFixture{root: filepath.Join(t.TempDir(), "state"), repo: t.TempDir()}
	if out, err := exec.Command("git", "init", "-q", f.repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	f.repo, _ = filepath.EvalSymlinks(f.repo)
	d, err := core.Start(core.Config{StateDir: f.root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f.address = d.Addresses()[0]
	if f.secret, err = core.ReadSecret(f.root); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), f.address, f.secret, "/v1/sessions/register", core.Registration{Family: "codex", ID: "synthetic-cleared",
		Repository: f.repo, Directory: f.repo, LaunchID: f.launch(t), Ancestors: []int{os.Getpid()}, ToolCall: true}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f clearFixture) launch(t *testing.T) string {
	t.Helper()
	id, err := core.CreateLaunch(context.Background(), f.address, f.secret, core.LaunchTarget{Family: "codex", Directory: f.repo, CLI: "/synthetic/cli", HostPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f clearFixture) env(launch string) map[string]string {
	return map[string]string{"KOINON_STATE_DIR": f.root, "KOINON_DAEMON_ADDRESS": f.address, "KOINON_LAUNCH_ID": launch}
}

func (f clearFixture) input(id, source string) string {
	return `{"session_id":"` + id + `","transcript_path":"/synthetic/rollout.jsonl","cwd":"` + f.repo +
		`","hook_event_name":"SessionStart","model":"synthetic","permission_mode":"default","source":"` + source + `"}`
}

func (f clearFixture) session(t *testing.T, id string) (core.Session, bool) {
	t.Helper()
	data, err := core.Call(context.Background(), f.address, f.secret, "/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Sessions []core.Session }
	json.Unmarshal(data, &list)
	for _, s := range list.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return core.Session{}, false
}

// wantMoved checks that the thread took the participant from the cleared thread.
func (f clearFixture) wantMoved(t *testing.T, id string) {
	t.Helper()
	got, found := f.session(t, id)
	if !found || !got.HoldsAddress || got.Succession == nil || got.Succession.Reason != "host_cleared" {
		t.Fatalf("cleared thread: %+v %+v", got, got.Succession)
	}
	if former, _ := f.session(t, "synthetic-cleared"); former.State != "retired" {
		t.Fatalf("former holder: %+v", former)
	}
}

// TestCodexSessionStartMovesTheAddress: the hook registers the thread that a Codex host
// shows after /clear, and it takes the participant from the cleared thread at once, within
// the guard. Another source, or a hook outside a launch, registers nothing. The input has
// the fields that Codex CLI 0.162.0 gave in the #275 probe.
func TestCodexSessionStartMovesTheAddress(t *testing.T) {
	f := newClearFixture(t)
	launch := f.launch(t)
	env := func(values map[string]string) HookEnv {
		return HookEnv{Getenv: func(k string) string { return values[k] }, Now: time.Now, ParentPID: os.Getpid()}
	}
	CodexSessionStart(strings.NewReader(f.input("synthetic-startup", "startup")), env(f.env(launch)))
	unlaunched := f.env(launch)
	delete(unlaunched, "KOINON_LAUNCH_ID")
	CodexSessionStart(strings.NewReader(f.input("synthetic-unlaunched", "clear")), env(unlaunched))
	for _, id := range []string{"synthetic-startup", "synthetic-unlaunched"} {
		if _, found := f.session(t, id); found {
			t.Fatalf("%s was registered", id)
		}
	}
	CodexSessionStart(strings.NewReader(f.input("synthetic-new", "clear")), env(f.env(launch)))
	f.wantMoved(t, "synthetic-new")
}

// TestCodexSessionStartBinary: Codex runs the hook as a command. The built binary, started
// by its host, reads the input, finds its host among its ancestors from the process table,
// moves the address, and writes nothing to stdout or stderr.
func TestCodexSessionStartBinary(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	binary := filepath.Join(t.TempDir(), "koinon")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/koinon")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	f := newClearFixture(t)
	cmd := exec.Command(binary, "hook", "codex-session-start")
	cmd.Stdin = strings.NewReader(f.input("synthetic-binary", "clear"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range f.env(f.launch(t)) {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("hook: %v stdout %q stderr %q", err, stdout.String(), stderr.String())
	}
	f.wantMoved(t, "synthetic-binary")
}
