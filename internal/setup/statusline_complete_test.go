package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// agyEmptyEntry is the entry that Antigravity CLI 1.5.1 keeps when no command is set (#631).
const agyEmptyEntry = `{"type":"","command":"","enabled":true}`

// olderAgySetup is an agy setup from before the status-line hook existed: the synthetic CLI
// runs the koinon MCP server and the Stop hook is in place, the settings hold settings, and
// Koinon has no status-line record (#631).
func olderAgySetup(t *testing.T, settings string) Options {
	t.Helper()
	dir := t.TempDir()
	cli, entries, _ := syntheticCLI(t, "agy")
	o := Options{Family: "agy", CLI: cli, Binary: "/opt/koinon", StateDir: filepath.Join(dir, "state"),
		AgyRoot: filepath.Join(dir, "gemini", "config"), AgySettings: filepath.Join(dir, "gemini", "antigravity-cli", "settings.json")}
	for _, d := range []string{o.AgyRoot, filepath.Dir(o.AgySettings)} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(entries, []byte("koinon /opt/koinon mcp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.AgyRoot, "hooks.json"), []byte(`{"koinon":{"Stop":[{"command":"'/opt/koinon' hook agy-stop","timeout":10,"type":"command"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.AgySettings, []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	return o
}

func statusState(t *testing.T, o Options) string {
	t.Helper()
	state, err := StatusLineState(o)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// An earlier entry without a command and without a type takes the wrapper's defaults, so agy
// keeps its default status line beside the hook; removal restores the exact entry (#631).
func TestAgyEmptyEntryStacksWithDefault(t *testing.T) {
	o := olderAgySetup(t, `{"statusLine":`+agyEmptyEntry+`,"theme":"dark"}`)
	if report, err := Run(context.Background(), o); err != nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("setup: %+v %v", report.StatusLine, err)
	}
	want := `{"enabled":true,"type":"command","command":"'/opt/koinon' hook agy-status","stack_with_default":true}`
	if got := readJSON(t, o.AgySettings)["statusLine"]; !equal(got, json.RawMessage(want)) {
		t.Fatalf("wrapper: %s", got)
	}
	if report, err := Remove(context.Background(), o); err != nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove: %+v %v", report.StatusLine, err)
	}
	if got := readJSON(t, o.AgySettings)["statusLine"]; !equal(got, json.RawMessage(agyEmptyEntry)) {
		t.Fatalf("restored: %s", got)
	}
	// A stack_with_default that the empty entry already has is kept, also false.
	o = olderAgySetup(t, `{"statusLine":{"enabled":true,"stack_with_default":false}}`)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	want = `{"enabled":true,"stack_with_default":false,"type":"command","command":"'/opt/koinon' hook agy-status"}`
	if got := readJSON(t, o.AgySettings)["statusLine"]; !equal(got, json.RawMessage(want)) {
		t.Fatalf("kept false: %s", got)
	}
}

// A wrapper that an earlier release wrote without stack_with_default, still unedited, is
// brought current by setup and by completion (#631, this machine's case).
func TestAgyUneditedWrapperBroughtCurrent(t *testing.T) {
	old := `{"enabled":true,"type":"command","command":"'/opt/koinon' hook agy-status"}`
	o := olderAgySetup(t, `{"statusLine":`+old+`}`)
	root, err := filepathPrivate(o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	record := `{"state":"enabled","settings_file":` + quoteJSON(o.AgySettings) + `,"original":` + agyEmptyEntry + `,"wrapper":` + old + `}`
	if err := os.WriteFile(filepath.Join(root, "agy-statusline.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineNeeded {
		t.Fatalf("state %q", got)
	}
	report, ran, err := CompleteStatusLine(context.Background(), o)
	if err != nil || !ran || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("complete: %+v %v %v", report, ran, err)
	}
	if got := readJSON(t, o.AgySettings)["statusLine"]; lookupKey(t, got, "stack_with_default") != "true" {
		t.Fatalf("flag not added: %s", got)
	}
	if got := statusState(t, o); got != StatusLineCurrent {
		t.Fatalf("state after %q", got)
	}
	if _, ran, err := CompleteStatusLine(context.Background(), o); err != nil || ran {
		t.Fatalf("second completion ran: %v %v", ran, err)
	}
	if report, err := Remove(context.Background(), o); err != nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove: %+v %v", report.StatusLine, err)
	}
	if got := readJSON(t, o.AgySettings)["statusLine"]; !equal(got, json.RawMessage(agyEmptyEntry)) {
		t.Fatalf("restored: %s", got)
	}
}

// Completion sets the status line up for an older setup, writes nothing in its state check,
// and leaves a declined record, an edited wrapper, a missing directory and an unconfigured
// family alone; an explicit setup after removal sets it up again (#631).
func TestCompleteStatusLine(t *testing.T) {
	o := olderAgySetup(t, `{"statusLine":`+agyEmptyEntry+`}`)
	before, _ := os.ReadFile(o.AgySettings)
	if got := statusState(t, o); got != StatusLineNeeded {
		t.Fatalf("state %q", got)
	}
	if after, _ := os.ReadFile(o.AgySettings); string(after) != string(before) {
		t.Fatal("the state check wrote the settings")
	}
	if _, err := os.Stat(o.StateDir); !os.IsNotExist(err) {
		t.Fatalf("the state check created the state directory: %v", err)
	}
	report, ran, err := CompleteStatusLine(context.Background(), o)
	if err != nil || !ran || report.StatusLine.Outcome != "set_up" || len(report.Changed) != 1 {
		t.Fatalf("complete: %+v %v %v", report, ran, err)
	}
	if got := readJSON(t, o.AgySettings)["statusLine"]; lookupKey(t, got, "stack_with_default") != "true" {
		t.Fatalf("wrapper: %s", got)
	}
	hooks, _ := os.ReadFile(filepath.Join(o.AgyRoot, "hooks.json"))
	if string(hooks) != `{"koinon":{"Stop":[{"command":"'/opt/koinon' hook agy-stop","timeout":10,"type":"command"}]}}` {
		t.Fatalf("completion changed the hooks: %s", hooks)
	}

	// Removed: completion and its state leave it, an explicit setup sets it up again.
	if _, err := Remove(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineDeclined {
		t.Fatalf("declined state %q", got)
	}
	if _, ran, err := CompleteStatusLine(context.Background(), o); err != nil || ran {
		t.Fatalf("declined completion ran: %v %v", ran, err)
	}
	if report, err := Run(context.Background(), o); err != nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("explicit setup after removal: %+v %v", report.StatusLine, err)
	}

	// Edited: changed, and completion leaves it.
	edited := `{"enabled":true,"type":"command","command":"'/opt/koinon' hook agy-status | tee /tmp/x","stack_with_default":true}`
	settings := `{"statusLine":` + edited + `}`
	if err := os.WriteFile(o.AgySettings, []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineChanged {
		t.Fatalf("edited state %q", got)
	}
	if _, ran, err := CompleteStatusLine(context.Background(), o); err != nil || ran {
		t.Fatalf("edited completion ran: %v %v", ran, err)
	}
	if after, _ := os.ReadFile(o.AgySettings); string(after) != settings {
		t.Fatalf("edited entry changed: %s", after)
	}

	// No Antigravity CLI directory: skipped. Not configured: nothing.
	missing := olderAgySetup(t, `{}`)
	missing.AgySettings = filepath.Join(t.TempDir(), "absent", "settings.json")
	if got := statusState(t, missing); got != StatusLineSkipped {
		t.Fatalf("missing state %q", got)
	}
	other := olderAgySetup(t, `{"statusLine":`+agyEmptyEntry+`}`)
	other.Binary = "/opt/another/koinon"
	if _, ran, err := CompleteStatusLine(context.Background(), other); err != nil || ran {
		t.Fatalf("unconfigured completion ran: %v %v", ran, err)
	}
	// A family without a status line has no state.
	if got := statusState(t, Options{Family: "codex", Binary: "/opt/koinon", StateDir: t.TempDir()}); got != "" {
		t.Fatalf("codex state %q", got)
	}
}

// Claude's status line after its own setup is current, so completion changes nothing.
func TestClaudeStatusLineCurrentAfterSetup(t *testing.T) {
	dir := t.TempDir()
	cli, _, _ := syntheticCLI(t, "claude")
	o := Options{Family: "claude", CLI: cli, Binary: "/opt/koinon", StateDir: filepath.Join(dir, "state"), ClaudeConfig: filepath.Join(dir, "claude")}
	if err := os.MkdirAll(o.ClaudeConfig, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineCurrent {
		t.Fatalf("claude state %q", got)
	}
	if _, ran, err := CompleteStatusLine(context.Background(), o); err != nil || ran {
		t.Fatalf("claude completion ran: %v %v", ran, err)
	}
}

func lookupKey(t *testing.T, entry json.RawMessage, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(entry, &m); err != nil {
		t.Fatal(err)
	}
	return string(m[key])
}

func quoteJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func filepathPrivate(path string) (string, error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	return path, os.Chmod(path, 0700)
}

// Grok's state check writes nothing: needed before its setup, current after it, declined
// after its removal (#631).
func TestGrokStatusLineState(t *testing.T) {
	g, home := grokFixture(t)
	config := filepath.Join(home, "config.toml")
	original := "[ui.status_line]\ntype = \"command\"\ncommand = \"~/bin/line\"\n"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Family: "grok", Binary: "/opt/koinon", StateDir: g.state, GrokHome: home}
	if got := statusState(t, o); got != StatusLineNeeded {
		t.Fatalf("before %q", got)
	}
	if after, _ := os.ReadFile(config); string(after) != original {
		t.Fatalf("the state check wrote the configuration: %s", after)
	}
	if _, err := g.setUp(); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineCurrent {
		t.Fatalf("after setup %q", got)
	}
	if _, err := g.remove(); err != nil {
		t.Fatal(err)
	}
	if got := statusState(t, o); got != StatusLineDeclined {
		t.Fatalf("after removal %q", got)
	}
}
