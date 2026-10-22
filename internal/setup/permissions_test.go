package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestSetupPreservesExistingPermissionSettings(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		t.Run(family, func(t *testing.T) {
			dir := t.TempDir()
			cli, _, _ := syntheticCLI(t, family)
			o := Options{Family: family, CLI: cli, Binary: "/opt/koinon/bin/koinon",
				StateDir: filepath.Join(dir, "state"), ClaudeConfig: filepath.Join(dir, "claude"),
				CodexHome: filepath.Join(dir, "codex"), AgyRoot: filepath.Join(dir, "agy"),
				Opencode: filepath.Join(dir, "opencode")}
			var file, field string
			var before []byte
			switch family {
			case "claude":
				file, field = filepath.Join(o.ClaudeConfig, "settings.json"), "permissions"
				before = []byte(`{"permissions":{"allow":["mcp__other__read"],"deny":["Bash(sudo *)"]},"theme":"dark"}`)
			case "codex":
				file = filepath.Join(o.CodexHome, "config.toml")
				before = []byte("[mcp_servers.other]\ncommand = \"other\"\ndefault_tools_approval_mode = \"prompt\"\n")
			case "agy":
				// CLI permissions live separately from the customization/hook root.
				file = filepath.Join(dir, "antigravity-cli", "settings.json")
				before = []byte(`{"permissions":{"allow":["mcp(other/read)"],"deny":["command(sudo)"]}}`)
			case "opencode":
				file, field = filepath.Join(o.Opencode, "opencode.json"), "permission"
				before = []byte(`{"permission":{"other_read":"ask","bash":{"sudo *":"deny"}},"mcp":{}}`)
			}
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, before, 0600); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				if _, err := Run(context.Background(), o); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if field == "" {
					if !bytes.Equal(before, after) {
						t.Fatal("setup rewrote native permission configuration")
					}
					continue
				}
				var original, current map[string]json.RawMessage
				if json.Unmarshal(before, &original) != nil || json.Unmarshal(after, &current) != nil {
					t.Fatal("setup damaged native configuration")
				}
				var want, got any
				if json.Unmarshal(original[field], &want) != nil || json.Unmarshal(current[field], &got) != nil {
					t.Fatal("setup removed permission settings")
				}
				wantJSON, _ := json.Marshal(want)
				gotJSON, _ := json.Marshal(got)
				if !bytes.Equal(wantJSON, gotJSON) {
					t.Fatal("setup changed native permission settings")
				}
			}
		})
	}
}

func TestClaudePermissionRules(t *testing.T) {
	read := func(t *testing.T, path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	t.Run("absent file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "claude")
		result, err := ClaudePermissionRules(dir, false)
		if err != nil || result.Outcome != "missing" || len(result.Missing) != len(ClaudePermissions) || result.Next != addPermissions {
			t.Fatalf("check: %+v %v", result, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("the check created the configuration directory")
		}
		if result, err = ClaudePermissionRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != len(ClaudePermissions) {
			t.Fatalf("add: %+v %v", result, err)
		}
		info, err := os.Stat(result.SettingsFile)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("settings file: %v %v", info, err)
		}
		var got struct{ Permissions struct{ Allow []string } }
		if json.Unmarshal(read(t, result.SettingsFile), &got) != nil || !slices.Equal(got.Permissions.Allow, ClaudePermissions) {
			t.Fatalf("allow: %v", got.Permissions.Allow)
		}
	})
	t.Run("merge keeps other settings", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		before := `{"theme":"dark","permissions":{"deny":["Bash(sudo *)"],"allow":["mcp__other__read","Bash(koinon guide --agent claude)"],"defaultMode":"auto"},"statusLine":{"type":"command","command":"x"}}`
		if err := os.WriteFile(path, []byte(before), 0640); err != nil {
			t.Fatal(err)
		}
		result, err := ClaudePermissionRules(dir, false)
		if err != nil || result.Outcome != "missing" || slices.Contains(result.Missing, "Bash(koinon guide --agent claude)") || len(result.Missing) != 3 {
			t.Fatalf("check: %+v %v", result, err)
		}
		if string(read(t, path)) != before {
			t.Fatal("the check changed the settings")
		}
		if result, err = ClaudePermissionRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != 3 || result.Missing != nil {
			t.Fatalf("add: %+v %v", result, err)
		}
		var got map[string]any
		if json.Unmarshal(read(t, path), &got) != nil {
			t.Fatal("invalid settings after add")
		}
		want := map[string]any{"theme": "dark", "statusLine": map[string]any{"type": "command", "command": "x"},
			"permissions": map[string]any{"deny": []any{"Bash(sudo *)"}, "defaultMode": "auto",
				"allow": []any{"mcp__other__read", "Bash(koinon guide --agent claude)", "mcp__koinon__*", "Bash(koinon context show)", "Bash(koinon status)"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("settings: %v", got)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("add changed the file mode")
		}
		after := read(t, path)
		if result, err = ClaudePermissionRules(dir, true); err != nil || result.Outcome != "present" || !bytes.Equal(read(t, path), after) {
			t.Fatalf("repeat: %+v %v", result, err)
		}
	})
	t.Run("broader rules cover", func(t *testing.T) {
		dir := t.TempDir()
		for _, allow := range []string{`["mcp__koinon","Bash(koinon *)"]`, `["mcp__koinon__*","Bash(koinon:*)"]`} {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":`+allow+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if result, err := ClaudePermissionRules(dir, false); err != nil || result.Outcome != "present" {
				t.Fatalf("%s: %+v %v", allow, result, err)
			}
		}
	})
	t.Run("refusals", func(t *testing.T) {
		for name, content := range map[string]string{
			"settings_invalid": `{"permissions":{"allow":"mcp__koinon__*"}}`,
			"not an object":    `{"permissions":["mcp__koinon__*"]}`,
			"broken":           `{"permissions":`,
		} {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := ClaudePermissionRules(dir, false)
			if err != nil || result.Outcome != "unreadable" || result.Reason != "settings_invalid" {
				t.Fatalf("%s check: %+v %v", name, result, err)
			}
			var refused SettingsError
			if _, err := ClaudePermissionRules(dir, true); !errors.As(err, &refused) || refused.Code != "settings_invalid" {
				t.Fatalf("%s add: %v", name, err)
			}
			if string(read(t, path)) != content {
				t.Fatalf("%s: a refusal changed the settings", name)
			}
		}
		dir := t.TempDir()
		target := filepath.Join(dir, "real.json")
		if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "settings.json")); err != nil {
			t.Fatal(err)
		}
		if result, err := ClaudePermissionRules(dir, false); err != nil || result.Reason != "settings_symlink" {
			t.Fatalf("symlink check: %+v %v", result, err)
		}
		if _, err := ClaudePermissionRules(dir, true); err == nil || string(read(t, target)) != `{}` {
			t.Fatalf("symlink add: %v", err)
		}
	})
}

func TestSetupClaudeReportsAndAddsPermissionRules(t *testing.T) {
	dir := t.TempDir()
	cli, _, log := syntheticCLI(t, "claude")
	o := Options{Family: "claude", CLI: cli, Binary: "/opt/koinon/bin/koinon",
		StateDir: filepath.Join(dir, "state"), ClaudeConfig: filepath.Join(dir, "claude")}
	settings := filepath.Join(o.ClaudeConfig, "settings.json")
	report, err := Run(context.Background(), o)
	if err != nil || report.Permissions == nil || report.Permissions.Outcome != "missing" || report.Permissions.SettingsFile != settings {
		t.Fatalf("setup: %+v %v", report.Permissions, err)
	}
	// The check reads only: no agent CLI call and no settings change.
	before, _ := os.ReadFile(settings)
	calls, _ := os.ReadFile(log)
	o.CheckPermissions, o.CLI = true, "relative"
	if report, err = Run(context.Background(), o); err != nil || report.Permissions.Outcome != "missing" || len(report.Changed) != 0 {
		t.Fatalf("check: %+v %v", report, err)
	}
	if after, _ := os.ReadFile(settings); !bytes.Equal(before, after) {
		t.Fatal("the check changed the settings")
	}
	if after, _ := os.ReadFile(log); !bytes.Equal(calls, after) {
		t.Fatal("the check ran the agent CLI")
	}
	o.CheckPermissions, o.AddPermissions, o.CLI = false, true, cli
	if report, err = Run(context.Background(), o); err != nil || report.Permissions.Outcome != "added" ||
		!slices.Contains(report.Changed, "Claude permission rules in "+settings) {
		t.Fatalf("add: %+v %v", report, err)
	}
	o.AddPermissions = false
	if report, err = Run(context.Background(), o); err != nil || report.Permissions.Outcome != "present" ||
		!slices.Contains(report.Unchanged, "Claude permission rules in "+settings) {
		t.Fatalf("repeat: %+v %v", report, err)
	}
	for _, bad := range []Options{
		{Family: "codex", CheckPermissions: true}, {Family: "codex", AddPermissions: true},
		{Family: "claude", CheckPermissions: true, AddPermissions: true},
		{Family: "claude", CheckPermissions: true, RemoveStatusLine: true},
	} {
		bad.Binary, bad.StateDir, bad.ClaudeConfig = o.Binary, o.StateDir, o.ClaudeConfig
		if _, err := Run(context.Background(), bad); err == nil {
			t.Fatalf("%+v was accepted", bad)
		}
	}
}
