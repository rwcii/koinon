package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

func TestCodexArgsRunOnItsOwn(t *testing.T) {
	variables := `mcp_servers.koinon.env_vars=["KOINON_LAUNCH_ID","KOINON_STATE_DIR","KOINON_DAEMON_ADDRESS","TMUX","TMUX_PANE","CODEX_HOME"]`
	want := []string{"--no-daemon", "-c", `shell_environment_policy.set.KOINON_CODEX_HOST="42"`, "-c", variables, "--model", "x"}
	if got := codexArgs("42", []string{"--model", "x"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("codex args: %q", got)
	}
	// A user's own --no-daemon is not repeated.
	if got := codexArgs("42", []string{"--no-daemon"}); !reflect.DeepEqual(got, append(want[1:5], "--no-daemon")) {
		t.Fatalf("repeated --no-daemon: %q", got)
	}
}

func TestParseBackground(t *testing.T) {
	o, err := Parse("claude", []string{"--bg", "--directory", "/synthetic", "--", "--bg"})
	if err != nil || !o.Background || o.Directory != "/synthetic" || !reflect.DeepEqual(o.Args, []string{"--bg"}) {
		t.Fatalf("parse: %+v %v", o, err)
	}
}

func TestJobID(t *testing.T) {
	for output, want := range map[string]string{
		"Started background session 0123abcd\n":                           "0123abcd",
		"\x1b[1m0123abcd\x1b[0m":                                          "0123abcd",
		"session 0123abcd-1111-4222-8333-444455556666 (attach: 0123abcd)": "0123abcd-1111-4222-8333-444455556666",
		"":                      "",
		"no job":                "",
		"0123abcd and 9876fedc": "",
		"0123ABCD":              "",
		"0123abcd-1111-4222-8333-444455556666 9876fedc-1111-4222-8333-444455556666": "",
	} {
		if got := jobID(output); got != want {
			t.Errorf("%q: %q, want %q", output, got, want)
		}
	}
}

// koinon claude --bg starts the job without launch variables in its environment, passes
// them only in a private --settings file, and records the job that claude --bg reports.
// A start that reports no job retires the launch and removes the file.
func TestClaudeBackgroundJob(t *testing.T) {
	root, address, _, directory := fixture(t)
	state := filepath.Join(root, "state")
	claude := filepath.Join(root, "claude")
	args, env := filepath.Join(root, "args"), filepath.Join(root, "env")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_ARGS\"\nenv > \"$FAKE_ENV\"\nprintf '%s' \"$FAKE_OUTPUT\"\n"
	if err := os.WriteFile(claude, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ARGS", args)
	t.Setenv("FAKE_ENV", env)
	t.Setenv("KOINON_LAUNCH_ID", "synthetic-inherited-launch")
	run := func(output string, extra ...string) (string, error) {
		t.Setenv("FAKE_OUTPUT", output)
		var out strings.Builder
		err := Run(context.Background(), Options{Family: "claude", CLI: claude, Directory: directory, StateDir: state, Address: address,
			Background: true, Args: append([]string{"--model", "synthetic"}, extra...)}, &out)
		return out.String(), err
	}
	out, err := run("Started background session 0123abcd\n")
	if err != nil || out != "Started background session 0123abcd\n" {
		t.Fatalf("background start: %q %v", out, err)
	}
	data, _ := os.ReadFile(args)
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(got) != 5 || got[0] != "--bg" || got[1] != "--model" || got[2] != "synthetic" || got[3] != "--settings" {
		t.Fatalf("claude arguments: %q", got)
	}
	settings := got[4]
	// The settings path is canonical: a macOS temporary directory is under a symbolic link.
	launches, _ := filepath.EvalSymlinks(filepath.Join(state, "launches"))
	info, err := os.Stat(settings)
	if err != nil || info.Mode().Perm() != 0600 || filepath.Dir(settings) != launches {
		t.Fatalf("settings file: %v %v", info, err)
	}
	var file struct {
		Env map[string]string `json:"env"`
	}
	data, _ = os.ReadFile(settings)
	if json.Unmarshal(data, &file) != nil || len(file.Env["KOINON_LAUNCH_ID"]) != 64 || file.Env["KOINON_STATE_DIR"] != state || file.Env["KOINON_DAEMON_ADDRESS"] != address {
		t.Fatalf("settings: %s", data)
	}
	if data, _ := os.ReadFile(env); strings.Contains(string(data), "KOINON_") {
		t.Fatalf("claude --bg inherited launch variables:\n%s", data)
	}
	// The recorded job admits its own session, and only it.
	secret, err := core.ReadSecret(state)
	if err != nil {
		t.Fatal(err)
	}
	register := func(id, launch string) error {
		_, err := core.Call(context.Background(), address, secret, "/v1/sessions/register", core.Registration{Family: "claude", ID: id,
			Directory: directory, LaunchID: launch, Ancestors: []int{900}, WakeTarget: json.RawMessage(`{"claude_pid":900}`)})
		return err
	}
	if err := register("0123abcd-1111-4222-8333-444455556666", file.Env["KOINON_LAUNCH_ID"]); err != nil {
		t.Fatalf("job session: %v", err)
	}
	var refusal core.RefusedError
	if err := register("9876fedc-1111-4222-8333-444455556666", file.Env["KOINON_LAUNCH_ID"]); !errors.As(err, &refusal) || refusal.Code != "not_launched" {
		t.Fatalf("another job admitted: %v", err)
	}
	// No job reported: the launch is retired and the settings file removed.
	before, _ := os.ReadDir(filepath.Join(state, "launches"))
	if _, err := run("something went wrong\n"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("start without a job: %v", err)
	}
	after, _ := os.ReadDir(filepath.Join(state, "launches"))
	data, _ = os.ReadFile(args)
	failed := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(after) != len(before) || len(failed) != 5 {
		t.Fatalf("settings file left: %d -> %d", len(before), len(after))
	}
	retired := strings.TrimSuffix(filepath.Base(failed[4]), ".json")
	if err := register("0123abcd-1111-4222-8333-444455556666", retired); !errors.As(err, &refusal) || refusal.Code != "not_launched" {
		t.Fatalf("retired launch admitted: %v", err)
	}
	// Koinon sets --settings and --bg itself.
	for _, arg := range []string{"--settings", "--settings=x", "--bg"} {
		if _, err := run("0123abcd", arg); err == nil {
			t.Fatalf("user %s accepted", arg)
		}
	}
	if err := Run(context.Background(), Options{Family: "codex", CLI: claude, Directory: directory, StateDir: state, Address: address, Background: true}, &strings.Builder{}); err == nil {
		t.Fatal("background Codex accepted")
	}
}
