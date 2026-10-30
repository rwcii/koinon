package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// brewKeg makes <brew>/Cellar/koinon/<version>/bin/koinon with its INSTALL_RECEIPT.json, points
// <brew>/opt/koinon at it, and returns the keg's binary, as `brew upgrade` leaves them.
func brewKeg(t *testing.T, brew, version string) string {
	t.Helper()
	keg := filepath.Join(brew, "Cellar", "koinon", version)
	if err := os.MkdirAll(filepath.Join(keg, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keg, "bin", "koinon"), []byte("koinon "+version), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	pointOpt(t, brew, filepath.Join("..", "Cellar", "koinon", version))
	return filepath.Join(keg, "bin", "koinon")
}

func pointOpt(t *testing.T, brew, target string) {
	t.Helper()
	link := filepath.Join(brew, "opt", "koinon")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// brewRoot is a temporary Homebrew prefix with its links resolved: on macOS the temporary
// directory is under a link (/var to /private/var).
func brewRoot(t *testing.T) string {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(tmp, "brew")
}

func runningAs(t *testing.T, path string) {
	t.Helper()
	saved := executable
	executable = func() (string, error) { return path, nil }
	t.Cleanup(func() { executable = saved })
}

// Without --binary, setup records the formula's opt path for a binary in a Homebrew keg, and
// this executable otherwise (#577).
func TestDefaultBinaryUsesTheHomebrewOptPath(t *testing.T) {
	brew := brewRoot(t)
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	old := brewKeg(t, brew, "0.8.0")
	check := func(name, running, want string) {
		t.Helper()
		runningAs(t, running)
		if got, err := defaultBinary(); err != nil || got != want {
			t.Fatalf("%s: %q %v, want %q", name, got, err, want)
		}
	}
	check("keg", old, opt)
	current := brewKeg(t, brew, "0.9.0")
	check("new version", current, opt)
	// opt points at another version of the same formula, as during an upgrade.
	check("other version of the formula", old, opt)

	plain := filepath.Join(t.TempDir(), "koinon")
	os.WriteFile(plain, []byte("koinon"), 0755)
	check("not in a keg", plain, plain)

	os.Remove(filepath.Join(brew, "Cellar", "koinon", "0.8.0", "INSTALL_RECEIPT.json"))
	check("no receipt", old, old)

	foreign := filepath.Join(brew, "Cellar", "other", "1.0")
	os.MkdirAll(filepath.Join(foreign, "bin"), 0755)
	os.WriteFile(filepath.Join(foreign, "bin", "koinon"), []byte("other"), 0755)
	pointOpt(t, brew, filepath.Join("..", "Cellar", "other", "1.0"))
	check("opt of another formula", current, current)

	os.Remove(filepath.Join(brew, "opt", "koinon"))
	check("no opt link", current, current)

	saved := executable
	executable = func() (string, error) { return "", errors.New("no executable") }
	defer func() { executable = saved }()
	if _, err := defaultBinary(); err == nil {
		t.Fatal("an executable error was not returned")
	}
}

func claudeStatusCommand(t *testing.T, config string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	command, _ := settings.StatusLine["command"].(string)
	return command
}

// Claude setup, its permission rules and removal take the opt path by default; the entries
// still run after a brew upgrade deletes the old keg, and --binary is used as given (#577).
func TestSetupClaudeRecordsTheHomebrewOptPath(t *testing.T) {
	ctx := context.Background()
	brew := brewRoot(t)
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	old := brewKeg(t, brew, "0.8.0")
	runningAs(t, old)
	cli, entries, _ := syntheticCLI(t, "claude")
	config, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
	original := `{"type":"command","command":"my-line","padding":2}`
	os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"statusLine":`+original+`}`), 0644)
	o := Options{Family: "claude", CLI: cli, ClaudeConfig: config, StateDir: state}

	report, err := Run(ctx, o)
	if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("setup: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(entries); strings.TrimSpace(string(data)) != "koinon "+opt+" mcp" {
		t.Fatalf("MCP entry %q", data)
	}
	if command := claudeStatusCommand(t, config); !strings.HasPrefix(command, "'"+opt+"' hook claude-status") || strings.Contains(command, "Cellar") {
		t.Fatalf("status line %q", command)
	}
	missing, err := Permissions(o, false)
	if err != nil || missing.Outcome != "missing" || !slices.Contains(missing.Missing, "Bash("+ShellWord(opt)+" status)") {
		t.Fatalf("missing rules %+v %v", missing, err)
	}
	if data, _ := os.ReadFile(filepath.Join(config, "settings.json")); strings.Contains(string(data), "Bash(") {
		t.Fatal("a report without add changed the rules")
	}
	added, err := Permissions(o, true)
	if err != nil || added.Outcome != "added" || slices.ContainsFunc(added.Added, func(rule string) bool { return strings.Contains(rule, "Cellar") }) {
		t.Fatalf("added rules %+v %v", added, err)
	}

	// brew upgrade: opt moves to the new keg and the cleanup deletes the old one.
	current := brewKeg(t, brew, "0.9.0")
	if err := os.RemoveAll(filepath.Join(brew, "Cellar", "koinon", "0.8.0")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(opt); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the configured path after the upgrade: %v", err)
	}
	runningAs(t, current)
	report, err = Run(ctx, o)
	if err != nil || report.StatusLine.Outcome != "unchanged" || slices.Contains(report.Changed, "mcp server koinon") {
		t.Fatalf("setup after the upgrade: %+v %v", report, err)
	}

	report, err = Remove(ctx, o)
	if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(entries); strings.TrimSpace(string(data)) != "" {
		t.Fatalf("MCP entry after removal %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(config, "settings.json")); !strings.Contains(string(data), `"my-line"`) || strings.Contains(string(data), "hook claude-status") {
		t.Fatalf("settings after removal %s", data)
	}

	// --binary is used as given, even a keg path.
	o.Binary = current
	if report, err = Run(ctx, o); err != nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("setup with --binary: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(entries); strings.TrimSpace(string(data)) != "koinon "+current+" mcp" {
		t.Fatalf("MCP entry with --binary %q", data)
	}
}

// Setup again repairs an unedited Koinon entry that names a Cellar path, keeping the saved
// original; a wrapper that the person edited stays as it is (#577).
func TestSetupClaudeRepairsACellarPath(t *testing.T) {
	ctx := context.Background()
	brew := brewRoot(t)
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	keg := brewKeg(t, brew, "0.8.0")
	cli, entries, _ := syntheticCLI(t, "claude")
	config, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
	settings := filepath.Join(config, "settings.json")
	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"my-line","padding":2}}`), 0644)
	// The configuration that an earlier setup wrote from the keg binary.
	o := Options{Family: "claude", CLI: cli, Binary: keg, ClaudeConfig: config, StateDir: state}
	if _, err := Run(ctx, o); err != nil {
		t.Fatal(err)
	}

	runningAs(t, keg)
	o.Binary = ""
	report, err := Run(ctx, o)
	if err != nil || report.StatusLine.Outcome != "set_up" || !slices.Contains(report.Changed, "mcp server koinon") {
		t.Fatalf("repair: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(entries); strings.TrimSpace(string(data)) != "koinon "+opt+" mcp" {
		t.Fatalf("MCP entry %q", data)
	}
	data, _ := os.ReadFile(settings)
	var repaired struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	json.Unmarshal(data, &repaired)
	if command, _ := repaired.StatusLine["command"].(string); !strings.HasPrefix(command, "'"+opt+"' hook claude-status") || !strings.Contains(command, "my-line") ||
		repaired.StatusLine["padding"] != float64(2) || repaired.StatusLine["refreshInterval"] != float64(30) {
		t.Fatalf("repaired status line %v", repaired.StatusLine)
	}
	if report, err = Remove(ctx, o); err != nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove after the repair: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(settings); !strings.Contains(string(data), `"command": "my-line"`) && !strings.Contains(string(data), `"command":"my-line"`) {
		t.Fatalf("restored settings %s", data)
	}

	// An edited wrapper with the Cellar path is kept and reported.
	o.Binary = keg
	if _, err := Run(ctx, o); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(settings)
	edited := strings.Replace(string(data), "hook claude-status", "hook claude-status --mine", 1)
	os.WriteFile(settings, []byte(edited), 0644)
	o.Binary = ""
	if report, err = Run(ctx, o); err != nil || report.StatusLine.Outcome != "changed" || report.StatusLine.Reason != "statusline_changed" {
		t.Fatalf("edited wrapper: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(settings); string(data) != edited {
		t.Fatalf("edited wrapper changed: %s", data)
	}
}
