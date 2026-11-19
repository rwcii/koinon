package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

// planDetail is PlanSteps for a test. It checks that Plan gives the same commands, in the same
// order, so the text and the JSON form cannot disagree (#745).
func planDetail(t *testing.T, e *env) PlanResult {
	t.Helper()
	result, err := PlanSteps(context.Background(), e.o)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if result.Steps == nil || result.Current != (len(result.Steps) == 0) {
		t.Fatalf("result %+v", result)
	}
	commands := []string{}
	for _, step := range result.Steps {
		if len(step.Reasons) == 0 {
			t.Fatalf("step without a reason: %+v", step)
		}
		commands = append(commands, step.Command)
	}
	if text := plan(t, e); !reflect.DeepEqual(append([]string{}, text...), commands) {
		t.Fatalf("text plan %q, JSON commands %q", text, commands)
	}
	return result
}

// codes is each step's command with its reason codes and families.
func codes(result PlanResult) []string {
	var got []string
	for _, step := range result.Steps {
		for _, r := range step.Reasons {
			got = append(got, step.Command+" | "+r.Code+" "+r.Family)
		}
	}
	return got
}

// Each step of the plan names its reason (#745), and the commands are those of the text form.
func TestPlanReasons(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		e := planEnv(t, backend)
		b, installed := e.o.Source, Binary(e.o.Prefix)
		result := planDetail(t, e)
		data, err := json.Marshal(result)
		if err != nil || string(data) != `{"current":true,"steps":[]}` {
			t.Fatalf("%s: current plan %s %v", backend, data, err)
		}
		e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
			return json.RawMessage(`{"maintainer_secret_set":false}`), nil
		}
		e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection {
			in := setup.Inspection{Family: o.Family}
			switch o.Family {
			case "codex":
				in.CLI = "/usr/bin/codex"
			case "agy":
				in.CLI, in.Configured, in.StatusLine = "/usr/bin/agy", true, setup.StatusLineNeeded
			}
			return in
		}
		want := []string{
			b + " maintainer secret set | maintainer_secret_unset ",
			b + " setup codex --binary " + installed + " | agent_not_set_up codex",
			b + " setup agy --binary " + installed + " | status_line_needed agy",
		}
		if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: setup reasons %q, want %q", backend, got, want)
		}
		e.r.fail = func(argv []string) bool { return argv[0] == "systemctl" || argv[0] == "launchctl" }
		e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
			return nil, errors.New("connection refused")
		}
		if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, []string{b + " install | no_current_daemon "}) {
			t.Fatalf("%s: no manager, no daemon: %q", backend, got)
		}
		e.r.fail = nil
		os.WriteFile(installed, []byte("older binary"), 0755)
		if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, []string{b + " install | binary_differs "}) {
			t.Fatalf("%s: outdated copy: %q", backend, got)
		}
		os.Remove(e.o.Services.DaemonArtifact())
		if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, []string{b + " install | not_installed "}) {
			t.Fatalf("%s: not installed: %q", backend, got)
		}
		os.MkdirAll(e.o.PythonPrefix, 0700)
		os.WriteFile(filepath.Join(e.o.PythonPrefix, "install.json"), []byte(`{"version":1}`), 0600)
		if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, []string{b + " upgrade --from-python | python_install "}) {
			t.Fatalf("%s: Python-era: %q", backend, got)
		}
		// The given prefix and state directory are quoted in the command, as in the text form.
		e.o.PlanPrefix, e.o.PlanState = true, true
		result = planDetail(t, e)
		if want := b + " upgrade --from-python --prefix " + setup.ShellWord(e.o.Prefix) + " --state-dir " + setup.ShellWord(e.o.StateDir); result.Steps[0].Command != want {
			t.Fatalf("%s: quoted command %q, want %q", backend, result.Steps[0].Command, want)
		}
	}
}

// A Homebrew copy that is not the link to the formula's opt path, and an older macOS system
// copy, name their reasons.
func TestPlanReasonsBinaries(t *testing.T) {
	e := planEnv(t, "systemd")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	brew := filepath.Join(dir, "brew with space")
	e.o.Source = homebrewKeg(t, brew, "0.7.0", "synthetic binary")
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	if got := codes(planDetail(t, e)); !reflect.DeepEqual(got, []string{"'" + opt + "' install | homebrew_link_differs "}) {
		t.Fatalf("Homebrew copy: %q", got)
	}

	s, _ := systemEnv(t)
	s.o.System = true
	if _, err := Install(context.Background(), s.o); err != nil {
		t.Fatal(err)
	}
	s.o.System = false
	s.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"maintainer_secret_set":true}`), nil
	}
	s.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection { return setup.Inspection{Family: o.Family} }
	os.WriteFile(s.o.Source, []byte("newer binary"), 0700)
	if got := codes(planDetail(t, s)); !reflect.DeepEqual(got, []string{s.o.Source + " install --system | binary_differs "}) {
		t.Fatalf("older system copy: %q", got)
	}
}

// Every entry that the install would create or replace is a reason of the one install step,
// across families and kinds; a kept file is none (#745).
func TestPlanReasonsEntries(t *testing.T) {
	e := entriesEnv(t)
	pickup := core.ClaudeSkillPath(e.o.ClaudeConfig, core.PickupSkillName)
	record := core.CodexSkillPath(e.o.Services.Home, "koinon-record")
	command := core.OpenCodeCommandPath(e.o.Opencode, "koinon-check")
	deepseek := core.DeepSeekPickupSkillPath(e.o.DSHHome)
	kept := core.OpenCodeCommandPath(e.o.Opencode, "koinon-resume")
	os.Remove(pickup)
	os.Remove(command)
	for _, path := range []string{record, deepseek} {
		if err := os.WriteFile(path, []byte("an earlier entry\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(kept, []byte("my own command\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := planDetail(t, e)
	if len(result.Steps) != 1 || result.Steps[0].Command != e.o.Source+" install" {
		t.Fatalf("steps %+v", result.Steps)
	}
	var got []PlanReason
	got = append(got, result.Steps[0].Reasons...)
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []PlanReason{
		{Code: "entry_needs_install", Kind: "pickup_skill", Family: "claude", Path: pickup, Result: "created"},
		{Code: "entry_needs_install", Kind: "skill", Family: "codex", Path: record, Result: "replaced"},
		{Code: "entry_needs_install", Kind: "pickup_skill", Family: "deepseek", Path: deepseek, Result: "replaced"},
		{Code: "entry_needs_install", Kind: "opencode_command", Family: "opencode", Path: command, Result: "created"},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons\n%+v\nwant\n%+v", got, want)
	}
}

// A later entry that cannot be read is an unfinished check: no plan in either form.
func TestPlanReasonsUnreadableEntry(t *testing.T) {
	e := entriesEnv(t)
	os.Remove(core.ClaudeSkillPath(e.o.ClaudeConfig, core.PickupSkillName))
	path := core.CodexSkillPath(e.o.Services.Home, "koinon-standards-review")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := PlanSteps(context.Background(), e.o)
	if err == nil || !strings.HasPrefix(err.Error(), "skill_entry_unreadable: ") || !reflect.DeepEqual(result, PlanResult{}) {
		t.Fatalf("%+v %v", result, err)
	}
}
