package install

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/setup"
)

// At a terminal, install reviews the families, asks to set up each installed family that is
// not set up, and offers each set-up family's missing permission rules (#449).
func TestInstallAsksAtTerminal(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"opencode"}
	installed := map[string]bool{"claude": true, "codex": true, "agy": true, "opencode": true}
	e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection {
		in := setup.Inspection{Family: o.Family}
		if installed[o.Family] {
			in.CLI, in.Configured = "/bin/"+o.Family, o.Family == "claude"
		}
		if o.Family == "agy" {
			in.Permissions = &setup.PermissionsResult{Family: "agy", Outcome: "unreadable", Reason: "settings_invalid"}
		}
		return in
	}
	var adds []string
	e.o.Permissions = func(o setup.Options, add bool) (setup.PermissionsResult, error) {
		result := setup.PermissionsResult{Family: o.Family, SettingsFile: "/cfg/" + o.Family, Outcome: "missing", Missing: []string{o.Family + "-rule"}}
		if o.Family == "opencode" {
			result.Outcome, result.Missing = "present", nil
		}
		if add {
			adds = append(adds, o.Family)
			result.Outcome, result.Added, result.Missing = "added", result.Missing, nil
		}
		return result, nil
	}
	var questions []string
	var told bytes.Buffer
	answers := map[string]bool{"Set up Koinon for codex?": true, "Set up Koinon for agy?": false,
		"Add them to /cfg/claude?": true, "Add them to /cfg/codex?": false}
	e.o.Ask = func(q string) (bool, error) {
		if !strings.Contains(told.String(), "permission rules unreadable") {
			t.Fatal("asked before the review showed the permission state")
		}
		questions = append(questions, q)
		return answers[q], nil
	}
	e.o.Tell = &told
	r, err := Install(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Set up Koinon for codex?", "Set up Koinon for agy?", "Add them to /cfg/claude?", "Add them to /cfg/codex?"}
	if !slices.Equal(questions, want) {
		t.Fatalf("questions %q", questions)
	}
	if len(e.setups) != 2 || !strings.HasPrefix(e.setups[0], "opencode ") || !strings.HasPrefix(e.setups[1], "codex ") {
		t.Fatalf("setups %q", e.setups)
	}
	for _, adopt := range e.adopts {
		if strings.HasPrefix(adopt, "codex ") || strings.HasPrefix(adopt, "opencode ") {
			t.Fatalf("adopted a family that was set up: %q", e.adopts)
		}
	}
	if !slices.Equal(adds, []string{"claude"}) {
		t.Fatalf("added rules for %q", adds)
	}
	outcomes := map[string]string{}
	for _, p := range r.Permissions {
		outcomes[p.Family] = p.Outcome
	}
	if len(r.Review) != 5 || len(outcomes) != 3 || outcomes["claude"] != "added" || outcomes["codex"] != "missing" || outcomes["opencode"] != "present" {
		t.Fatalf("review %d, permissions %v", len(r.Review), outcomes)
	}
	for _, line := range []string{"claude    set up (/bin/claude)", "agy       not set up (/bin/agy); permission rules unreadable (settings_invalid)", "codex     not set up (/bin/codex)", "grok      not installed", "  claude-rule"} {
		if !strings.Contains(told.String(), line) {
			t.Fatalf("review omits %q:\n%s", line, told.String())
		}
	}
}

// Without a terminal, install asks nothing, reviews nothing and changes no permission rule.
func TestInstallWithoutTerminalAsksNothing(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"claude"}
	e.o.Inspect = func(context.Context, setup.Options) setup.Inspection {
		t.Fatal("reviewed without a terminal")
		return setup.Inspection{}
	}
	e.o.Permissions = func(setup.Options, bool) (setup.PermissionsResult, error) {
		t.Fatal("permission rules read without a terminal")
		return setup.PermissionsResult{}, nil
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Review != nil || r.Permissions != nil || len(e.setups) != 1 {
		t.Fatalf("%+v %v %q", r, err, e.setups)
	}
}
