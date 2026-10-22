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
	"strings"
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
		result, err := claudeRules(dir, false)
		if err != nil || result.Outcome != "missing" || len(result.Missing) != len(ClaudePermissions) || result.Note != missingNote {
			t.Fatalf("check: %+v %v", result, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("the check created the configuration directory")
		}
		if result, err = claudeRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != len(ClaudePermissions) {
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
		before := `{"theme":"dark","permissions":{"deny":["Bash(sudo *)"],"allow":["mcp__other__read","Bash(koinon guide --agent claude)","Read(\u002ftmp)"],"defaultMode":"auto"},"statusLine":{"type":"command","command":"x"}}`
		if err := os.WriteFile(path, []byte(before), 0640); err != nil {
			t.Fatal(err)
		}
		result, err := claudeRules(dir, false)
		if err != nil || result.Outcome != "missing" || slices.Contains(result.Missing, "Bash(koinon guide --agent claude)") || len(result.Missing) != 3 {
			t.Fatalf("check: %+v %v", result, err)
		}
		if string(read(t, path)) != before {
			t.Fatal("the check changed the settings")
		}
		if result, err = claudeRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != 3 || result.Missing != nil {
			t.Fatalf("add: %+v %v", result, err)
		}
		var got map[string]any
		if json.Unmarshal(read(t, path), &got) != nil {
			t.Fatal("invalid settings after add")
		}
		want := map[string]any{"theme": "dark", "statusLine": map[string]any{"type": "command", "command": "x"},
			"permissions": map[string]any{"deny": []any{"Bash(sudo *)"}, "defaultMode": "auto",
				"allow": []any{"mcp__other__read", "Bash(koinon guide --agent claude)", "Read(/tmp)", "mcp__koinon__*", "Bash(koinon context show)", "Bash(koinon status)"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("settings: %v", got)
		}
		if !bytes.Contains(read(t, path), []byte(`"Read(\u002ftmp)"`)) {
			t.Fatal("add rewrote an existing rule")
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("add changed the file mode")
		}
		after := read(t, path)
		if result, err = claudeRules(dir, true); err != nil || result.Outcome != "present" || !bytes.Equal(read(t, path), after) {
			t.Fatalf("repeat: %+v %v", result, err)
		}
	})
	t.Run("broader rules cover", func(t *testing.T) {
		dir := t.TempDir()
		for _, allow := range []string{`["mcp__koinon","Bash(koinon *)"]`, `["mcp__koinon__*","Bash(koinon:*)"]`} {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":`+allow+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if result, err := claudeRules(dir, false); err != nil || result.Outcome != "present" {
				t.Fatalf("%s: %+v %v", allow, result, err)
			}
		}
	})
	t.Run("refusals", func(t *testing.T) {
		for name, content := range map[string]string{
			"settings_invalid": `{"permissions":{"allow":"mcp__koinon__*"}}`,
			"null rule":        `{"permissions":{"allow":["mcp__other__read",null]}}`,
			"number rule":      `{"permissions":{"allow":["mcp__other__read",7]}}`,
			"object rule":      `{"permissions":{"allow":[{"rule":"x"}]}}`,
			"not an object":    `{"permissions":["mcp__koinon__*"]}`,
			"broken":           `{"permissions":`,
		} {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := claudeRules(dir, false)
			if err != nil || result.Outcome != "unreadable" || result.Reason != "settings_invalid" {
				t.Fatalf("%s check: %+v %v", name, result, err)
			}
			var refused SettingsError
			if _, err := claudeRules(dir, true); !errors.As(err, &refused) || refused.Code != "settings_invalid" {
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
		if result, err := claudeRules(dir, false); err != nil || result.Reason != "settings_symlink" {
			t.Fatalf("symlink check: %+v %v", result, err)
		}
		if _, err := claudeRules(dir, true); err == nil || string(read(t, target)) != `{}` {
			t.Fatalf("symlink add: %v", err)
		}
	})
}

func claudeRules(dir string, add bool) (PermissionsResult, error) {
	return Permissions(Options{Family: "claude", ClaudeConfig: dir}, add)
}

// Setup reports each family's rules and changes none of them.
func TestSetupReportsPermissionRules(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode", "grok"} {
		t.Run(family, func(t *testing.T) {
			dir := t.TempDir()
			cli, _, _ := syntheticCLI(t, family)
			o := Options{Family: family, CLI: cli, Binary: "/opt/koinon/bin/koinon",
				StateDir: filepath.Join(dir, "state"), ClaudeConfig: filepath.Join(dir, "claude"),
				CodexHome: filepath.Join(dir, "codex"), AgyRoot: filepath.Join(dir, "agy"),
				AgySettings: filepath.Join(dir, "agy-cli", "settings.json"), Opencode: filepath.Join(dir, "opencode")}
			report, err := Run(context.Background(), o)
			if err != nil || report.Permissions == nil || report.Permissions.Family != family {
				t.Fatalf("%+v %v", report.Permissions, err)
			}
			want := map[string]string{"claude": "missing", "agy": "missing", "opencode": "missing", "grok": "manual",
				// The synthetic codex CLI writes no config.toml.
				"codex": "unreadable"}[family]
			if report.Permissions.Outcome != want {
				t.Fatalf("outcome %+v, want %s", report.Permissions, want)
			}
			for _, path := range []string{o.AgySettings, filepath.Join(o.Opencode, "opencode.json"), filepath.Join(o.CodexHome, "config.toml")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("setup wrote %s", path)
				}
			}
		})
	}
}

func TestCodexApproval(t *testing.T) {
	table := "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"other\"\ndefault_tools_approval_mode = \"prompt\"\n\n[mcp_servers.koinon]\ncommand = \"/opt/k\" # the binary\nargs = [\"mcp\"]\n\n# a note\n[mcp_servers.koinon.env]\nKOINON = \"1\"\n"
	cases := []struct {
		name, before, after, outcome, reason string
	}{
		{"insert after the last key", table, strings.Replace(table, "args = [\"mcp\"]\n", "args = [\"mcp\"]\ndefault_tools_approval_mode = \"approve\"\n", 1), "added", ""},
		{"last table without newline", "[mcp_servers.koinon]\ncommand = \"/opt/k\"", "[mcp_servers.koinon]\ncommand = \"/opt/k\"\ndefault_tools_approval_mode = \"approve\"\n", "added", ""},
		{"quoted header", "[mcp_servers.\"koinon\"]\ncommand = \"k\"\n", "[mcp_servers.\"koinon\"]\ncommand = \"k\"\ndefault_tools_approval_mode = \"approve\"\n", "added", ""},
		{"present", "[mcp_servers.koinon]\ndefault_tools_approval_mode = 'approve' # ok\n", "", "present", ""},
		{"different", "[mcp_servers.koinon]\ndefault_tools_approval_mode = \"prompt\"\n", "", "different", ""},
		{"no table", "[mcp_servers.other]\ncommand = \"x\"\n", "", "unreadable", "settings_no_server"},
		{"absent file", "", "", "unreadable", "settings_no_server"},
		{"two tables", "[mcp_servers.koinon]\n[mcp_servers.koinon]\n", "", "unreadable", "settings_invalid"},
		{"dotted key elsewhere", "[mcp_servers]\nkoinon.default_tools_approval_mode = \"approve\"\n[mcp_servers.koinon]\ncommand = \"k\"\n", "", "unreadable", "settings_unusual"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if c.name != "absent file" {
				if err := os.WriteFile(path, []byte(c.before), 0640); err != nil {
					t.Fatal(err)
				}
			}
			o := Options{Family: "codex", CodexHome: dir}
			check, err := Permissions(o, false)
			wantCheck := c.outcome
			if wantCheck == "added" {
				wantCheck = "missing"
			}
			if err != nil || check.Outcome != wantCheck || check.Reason != c.reason || check.SettingsFile != path {
				t.Fatalf("check: %+v %v", check, err)
			}
			result, err := Permissions(o, true)
			if c.reason != "" {
				var refused SettingsError
				if !errors.As(err, &refused) || refused.Code != c.reason {
					t.Fatalf("add: %+v %v", result, err)
				}
			} else if err != nil || result.Outcome != c.outcome {
				t.Fatalf("add: %+v %v", result, err)
			}
			data, _ := os.ReadFile(path)
			want := c.after
			if want == "" {
				want = c.before
			}
			if string(data) != want {
				t.Fatalf("file:\n%s\nwant:\n%s", data, want)
			}
			if c.outcome == "added" {
				if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
					t.Fatal("add changed the file mode")
				}
				if again, err := Permissions(o, true); err != nil || again.Outcome != "present" {
					t.Fatalf("repeat: %+v %v", again, err)
				}
			}
		})
	}
}

func TestAgyAllowList(t *testing.T) {
	dir := t.TempDir()
	o := Options{Family: "agy", AgySettings: filepath.Join(dir, "antigravity-cli", "settings.json")}
	if err := os.MkdirAll(filepath.Dir(o.AgySettings), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.AgySettings, []byte(`{"permissions":{"allow":["command(ls)","command(koinon)"]},"trustedWorkspaces":["/w"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Permissions(o, false)
	if err != nil || result.Outcome != "missing" || !slices.Equal(result.Missing, []string{"mcp(koinon/*)"}) {
		t.Fatalf("check: %+v %v", result, err)
	}
	if result, err = Permissions(o, true); err != nil || result.Outcome != "added" {
		t.Fatalf("add: %+v %v", result, err)
	}
	var got map[string]any
	data, _ := os.ReadFile(o.AgySettings)
	if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got, map[string]any{
		"permissions":       map[string]any{"allow": []any{"command(ls)", "command(koinon)", "mcp(koinon/*)"}},
		"trustedWorkspaces": []any{"/w"}}) {
		t.Fatalf("settings: %s", data)
	}
	if err := os.WriteFile(o.AgySettings, []byte(`{"permissions":{"allow":[null]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var refused SettingsError
	if _, err := Permissions(o, true); !errors.As(err, &refused) || !strings.Contains(refused.Message, "Antigravity settings") {
		t.Fatalf("refusal: %v", err)
	}
}

func TestOpenCodePermission(t *testing.T) {
	guide := `"koinon guide --agent opencode":"allow"`
	cases := []struct {
		name, before, outcome string
		after                 map[string]any // the permission member after add; nil: file unchanged
	}{
		{"absent file", "", "added", map[string]any{"koinon_*": "allow", "bash": map[string]any{"koinon guide --agent opencode": "allow"}}},
		{"merge", `{"mcp":{},"permission":{"other_*":"ask","bash":{"sudo *":"deny"}}}`, "added",
			map[string]any{"other_*": "ask", "bash": map[string]any{"sudo *": "deny", "koinon guide --agent opencode": "allow"}, "koinon_*": "allow"}},
		{"present", `{"permission":{"koinon_*":"allow","bash":{` + guide + `}}}`, "present", nil},
		{"allow everything", `{"permission":"allow"}`, "present", nil},
		{"ask everything", `{"permission":"ask"}`, "different", nil},
		{"bash action kept", `{"permission":{"bash":"ask"}}`, "added", map[string]any{"bash": "ask", "koinon_*": "allow"}},
		{"different server rule", `{"permission":{"koinon_*":"deny"}}`, "added",
			map[string]any{"koinon_*": "deny", "bash": map[string]any{"koinon guide --agent opencode": "allow"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "opencode.json")
			if c.before != "" {
				if err := os.WriteFile(path, []byte(c.before), 0600); err != nil {
					t.Fatal(err)
				}
			}
			o := Options{Family: "opencode", Opencode: dir}
			result, err := Permissions(o, true)
			if err != nil || result.Outcome != c.outcome {
				t.Fatalf("add: %+v %v", result, err)
			}
			data, _ := os.ReadFile(path)
			if c.after == nil {
				if string(data) != c.before {
					t.Fatalf("file changed: %s", data)
				}
				return
			}
			var got map[string]any
			if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got["permission"], any(c.after)) {
				t.Fatalf("permission: %s", data)
			}
			if again, err := Permissions(o, true); err != nil || (again.Outcome != "present" && again.Outcome != "different") || again.Added != nil {
				t.Fatalf("repeat: %+v %v", again, err)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := Permissions(Options{Family: "opencode", Opencode: dir}, false); err != nil || result.Reason != "settings_jsonc" {
		t.Fatalf("jsonc: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("jsonc check wrote opencode.json")
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "opencode.json"), []byte(`{"permission":{"koinon_*":7}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Permissions(Options{Family: "opencode", Opencode: bad}, true); err == nil {
		t.Fatal("a non-action rule was accepted")
	}
}

func TestInspectReadsOnly(t *testing.T) {
	dir := t.TempDir()
	cli, _, log := syntheticCLI(t, "claude")
	o := Options{Family: "claude", CLI: cli, Binary: "/opt/koinon/bin/koinon", StateDir: filepath.Join(dir, "state"),
		ClaudeConfig: filepath.Join(dir, "claude")}
	if in := Inspect(context.Background(), o); in.CLI != cli || in.Configured || in.Permissions == nil || in.Permissions.Outcome != "missing" {
		t.Fatalf("before setup: %+v", in)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(log)
	if in := Inspect(context.Background(), o); !in.Configured {
		t.Fatalf("after setup: %+v", in)
	}
	after, _ := os.ReadFile(log)
	if calls := strings.TrimPrefix(string(after), string(before)); calls != "mcp get koinon\n" {
		t.Fatalf("inspect ran %q", calls)
	}
	if in := Inspect(context.Background(), Options{Family: "claude", CLI: filepath.Join(dir, "none"), Binary: o.Binary}); in.Configured || in.Permissions == nil {
		t.Fatalf("missing CLI: %+v", in)
	}
	t.Setenv("PATH", dir)
	if in := Inspect(context.Background(), Options{Family: "codex", Binary: o.Binary}); in.CLI != "" || in.Permissions != nil {
		t.Fatalf("no CLI on PATH: %+v", in)
	}
}
