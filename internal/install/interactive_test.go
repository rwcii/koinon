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

// After the permission question, each family's review and own entries report the rules as they
// are then, not as review and setup read them before the question (#462).
func TestInstallReportsRulesAfterQuestion(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"claude", "codex"}
	missing := func(family string) setup.PermissionsResult {
		return setup.PermissionsResult{Family: family, SettingsFile: "/cfg/" + family, Outcome: "missing", Missing: []string{family + "-rule"},
			Note: "Run koinon install in a terminal to add them, or merge them by hand (docs/PERMISSIONS.md)."}
	}
	e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection {
		in := setup.Inspection{Family: o.Family}
		if o.Family == "claude" || o.Family == "codex" {
			rules := missing(o.Family)
			in.CLI, in.Permissions = "/bin/"+o.Family, &rules
		}
		return in
	}
	e.o.Setup = func(_ context.Context, o setup.Options) (setup.Report, error) {
		rules := missing(o.Family)
		return setup.Report{OK: true, Family: o.Family, Permissions: &rules}, nil
	}
	e.o.Permissions = func(o setup.Options, add bool) (setup.PermissionsResult, error) {
		result := missing(o.Family)
		if add {
			result = setup.PermissionsResult{Family: o.Family, SettingsFile: "/cfg/" + o.Family, Outcome: "added", Added: []string{o.Family + "-rule"}}
		}
		return result, nil
	}
	e.o.Ask = func(q string) (bool, error) { return q == "Add them to /cfg/claude?", nil }
	e.o.Tell = &bytes.Buffer{}
	r, err := Install(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	agents := map[string]setup.Report{}
	for _, a := range r.Agents {
		agents[a.Family] = a
	}
	for _, in := range r.Review {
		if in.Family == "claude" && (in.Permissions == nil || in.Permissions.Outcome != "added" || in.Permissions.Note != "") {
			t.Fatalf("claude review after yes: %+v", in.Permissions)
		}
	}
	claude, codex := agents["claude"], agents["codex"]
	if claude.Permissions == nil || claude.Permissions.Outcome != "added" || claude.Permissions.Missing != nil || claude.Permissions.Note != "" {
		t.Fatalf("claude permissions after yes: %+v", claude.Permissions)
	}
	if !slices.Contains(claude.Changed, "claude permission rules in /cfg/claude") {
		t.Fatalf("claude changed %q", claude.Changed)
	}
	if codex.Permissions == nil || codex.Permissions.Outcome != "missing" || slices.ContainsFunc(codex.Changed, func(c string) bool { return strings.Contains(c, "permission") }) {
		t.Fatalf("codex after no: %+v %q", codex.Permissions, codex.Changed)
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
