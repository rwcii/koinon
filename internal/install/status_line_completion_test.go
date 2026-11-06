package install

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/setup"
)

// Install completes the status line of each configured claude, agy and grok family that it
// does not set up; a failed completion is reported and the install goes on (#631).
func TestInstallCompletesStatusLines(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"claude"}
	var asked []string
	e.o.CompleteStatusLine = func(_ context.Context, o setup.Options) (setup.Report, bool, error) {
		if o.Binary != Binary(e.o.Prefix) || o.StateDir != e.o.StateDir || o.AgyRoot != e.o.AgyRoot || o.GrokHome != e.o.GrokHome || o.ClaudeConfig != e.o.ClaudeConfig {
			t.Fatalf("completion options %+v", o)
		}
		asked = append(asked, o.Family)
		switch o.Family {
		case "agy":
			return setup.Report{OK: true, Family: "agy", Changed: []string{"agy status line in /synthetic/settings.json"},
				StatusLine: &setup.StatusLineResult{Action: "set_up", Outcome: "set_up", SettingsFile: "/synthetic/settings.json"}}, true, nil
		case "grok":
			return setup.Report{OK: true, Family: "grok"}, true, errors.New("status line not completed: settings_invalid")
		}
		return setup.Report{OK: true, Family: o.Family}, false, nil
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK {
		t.Fatalf("install %+v %v", r, err)
	}
	if want := []string{"agy", "grok"}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("completion asked for %q, want %q (claude is set up by this install)", asked, want)
	}
	var agy, grok *setup.Report
	for i := range r.Agents {
		switch r.Agents[i].Family {
		case "agy":
			agy = &r.Agents[i]
		case "grok":
			grok = &r.Agents[i]
		}
	}
	if agy == nil || agy.StatusLine == nil || agy.StatusLine.Outcome != "set_up" || !slices.Contains(agy.Changed, "agy status line in /synthetic/settings.json") {
		t.Fatalf("agy report %+v", agy)
	}
	if grok == nil || grok.OK || !strings.Contains(grok.Note, "settings_invalid") {
		t.Fatalf("grok report %+v", grok)
	}
}

// The plan names setup for a configured family whose status line is needed, and nothing for
// one that is current, declined, changed or skipped (#631).
func TestPlanNamesStatusLineCompletion(t *testing.T) {
	e := planEnv(t, "systemd")
	b := e.o.Source
	states := map[string]string{"claude": setup.StatusLineCurrent, "agy": setup.StatusLineNeeded, "grok": setup.StatusLineDeclined}
	e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection {
		if o.AgyRoot != e.o.AgyRoot || o.GrokHome != e.o.GrokHome {
			t.Fatalf("inspection options %+v", o)
		}
		in := setup.Inspection{Family: o.Family}
		if state, ok := states[o.Family]; ok {
			in.CLI, in.Configured, in.StatusLine = "/usr/bin/"+o.Family, true, state
		}
		return in
	}
	if steps, want := plan(t, e), []string{b + " setup agy --binary " + Binary(e.o.Prefix)}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("plan %q, want %q", steps, want)
	}
	for _, state := range []string{setup.StatusLineCurrent, setup.StatusLineDeclined, setup.StatusLineChanged, setup.StatusLineSkipped, ""} {
		states["agy"] = state
		if steps := plan(t, e); len(steps) != 0 {
			t.Fatalf("state %q: plan %q", state, steps)
		}
	}
	// A status line that cannot be read is an unfinished check: an error, never current.
	inspect := e.o.Inspect
	e.o.Inspect = func(ctx context.Context, o setup.Options) setup.Inspection {
		in := inspect(ctx, o)
		if o.Family == "agy" {
			in.StatusLine, in.StatusLineError = "", "settings_invalid: the Antigravity settings are not valid JSON; not changed (/synthetic/settings.json)"
		}
		return in
	}
	steps, err := Plan(context.Background(), e.o)
	if err == nil || !strings.Contains(err.Error(), "agent_unreadable: agy status line: settings_invalid") || len(steps) != 0 {
		t.Fatalf("unreadable status line: %q %v", steps, err)
	}
}
