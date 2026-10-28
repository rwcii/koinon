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
	"time"

	"github.com/BurntSushi/toml"
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
				// CLI permissions live separately from the customization/hook root, beside the
				// status line that setup adds (#512).
				file, field = filepath.Join(dir, "antigravity-cli", "settings.json"), "permissions"
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
		if err != nil || result.Outcome != "missing" || len(result.Missing) != len(claudeWant) || result.Note != missingNote {
			t.Fatalf("check: %+v %v", result, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("the check created the configuration directory")
		}
		if result, err = claudeRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != len(claudeWant) {
			t.Fatalf("add: %+v %v", result, err)
		}
		info, err := os.Stat(result.SettingsFile)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("settings file: %v %v", info, err)
		}
		var got struct{ Permissions struct{ Allow []string } }
		if json.Unmarshal(read(t, result.SettingsFile), &got) != nil || !slices.Equal(got.Permissions.Allow, claudeWant) {
			t.Fatalf("allow: %v", got.Permissions.Allow)
		}
	})
	t.Run("merge keeps other settings", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		before := `{"theme":"dark","permissions":{"deny":["Bash(sudo *)"],"allow":["mcp__other__read","Bash(koinon status)","Read(\u002ftmp)"],"defaultMode":"auto"},"statusLine":{"type":"command","command":"x"}}`
		if err := os.WriteFile(path, []byte(before), 0640); err != nil {
			t.Fatal(err)
		}
		result, err := claudeRules(dir, false)
		if err != nil || result.Outcome != "missing" || slices.Contains(result.Missing, "Bash(koinon status)") || len(result.Missing) != len(claudeWant)-1 {
			t.Fatalf("check: %+v %v", result, err)
		}
		if string(read(t, path)) != before {
			t.Fatal("the check changed the settings")
		}
		if result, err = claudeRules(dir, true); err != nil || result.Outcome != "added" || len(result.Added) != len(claudeWant)-1 || result.Missing != nil {
			t.Fatalf("add: %+v %v", result, err)
		}
		var got map[string]any
		if json.Unmarshal(read(t, path), &got) != nil {
			t.Fatal("invalid settings after add")
		}
		allow := []any{"mcp__other__read", "Bash(koinon status)", "Read(/tmp)"}
		for _, rule := range claudeWant {
			if rule != "Bash(koinon status)" {
				allow = append(allow, rule)
			}
		}
		want := map[string]any{"theme": "dark", "statusLine": map[string]any{"type": "command", "command": "x"},
			"permissions": map[string]any{"deny": []any{"Bash(sudo *)"}, "defaultMode": "auto", "allow": allow}}
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
		// A broad rule covers the forms of its own program only: the PATH name or the binary.
		path, binary := claudeWant[1:len(ClaudePermissions)], claudeWant[len(ClaudePermissions):]
		dir := t.TempDir()
		for allow, missing := range map[string][]string{
			`["mcp__koinon","Bash(koinon *)","Bash(/opt/koinon/bin/koinon *)"]`:    nil,
			`["mcp__koinon__*","Bash(koinon:*)","Bash(/opt/koinon/bin/koinon:*)"]`: nil,
			`["mcp__koinon","Bash(koinon *)"]`:                                     binary,
			`["mcp__koinon","Bash(/opt/koinon/bin/koinon *)"]`:                     path,
		} {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":`+allow+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := claudeRules(dir, false)
			if err != nil || !slices.Equal(result.Missing, missing) || (missing == nil) != (result.Outcome == "present") {
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

// claudeWant are the rules that setup recommends for the binary of claudeRules.
var claudeWant = claudePermissions("/opt/koinon/bin/koinon")

func claudeRules(dir string, add bool) (PermissionsResult, error) {
	return Permissions(Options{Family: "claude", ClaudeConfig: dir, Binary: "/opt/koinon/bin/koinon"}, add)
}

// The Claude rules allow the agent-facing commands with and without arguments, for the PATH
// name and for the installed binary, and no command that changes the installation, the
// service, the maintainer's settings or a login (DECISIONS.md#CLI-RULES-550).
func TestClaudeRulesCoverAgentCommands(t *testing.T) {
	if len(ClaudePermissions) != 1+2*len(ClaudeCommands) || ClaudePermissions[0] != "mcp__koinon__*" {
		t.Fatalf("rules: %q", ClaudePermissions)
	}
	for _, command := range []string{"status", "decision", "work", "claim renew", "memory", "room post", "library", "guide", "cycle-pickup"} {
		for _, rule := range []string{"Bash(koinon " + command + ")", "Bash(koinon " + command + " *)",
			"Bash(/opt/koinon/bin/koinon " + command + ")", "Bash(/opt/koinon/bin/koinon " + command + " *)"} {
			if !slices.Contains(claudeWant, rule) {
				t.Errorf("missing %s", rule)
			}
		}
	}
	for _, rule := range claudeWant {
		for _, left := range []string{"install", "uninstall", "upgrade", "serve", "setup", "maintainer", "dashboard",
			"context set", "context enable", "context disable", "context threshold", "sessions", "participant"} {
			if strings.HasPrefix(strings.TrimPrefix(strings.TrimPrefix(rule, "Bash(koinon "), "Bash(/opt/koinon/bin/koinon "), left) {
				t.Errorf("%s allows %s", rule, left)
			}
		}
	}
	if len(claudeWant) != 1+4*len(ClaudeCommands) {
		t.Fatalf("binary rules: %d", len(claudeWant))
	}
	// A path that the shell must quote is quoted, as setup quotes it in commands.
	quoted := claudePermissions("/Users/Synthetic User/koinon's/bin/koinon")
	if want := `Bash('/Users/Synthetic User/koinon'"'"'s/bin/koinon' status *)`; !slices.Contains(quoted, want) {
		t.Fatalf("quoted path: %q", quoted[len(ClaudePermissions):len(ClaudePermissions)+4])
	}
	if got := claudePermissions("koinon"); !slices.Equal(got, ClaudePermissions) {
		t.Fatalf("PATH name only: %q", got)
	}
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
		{"invalid TOML", "[mcp_servers.koinon\n", "", "unreadable", "settings_invalid"},
		{"quoted key present", "[mcp_servers.koinon]\ncommand = \"k\"\n\"default_tools_approval_mode\" = \"approve\"\n", "", "present", ""},
		{"quoted key different", "[mcp_servers.koinon]\n'default_tools_approval_mode' = \"prompt\"\n", "", "different", ""},
		{"dotted key elsewhere", "[mcp_servers]\nkoinon.default_tools_approval_mode = \"approve\"\nkoinon.command = \"k\"\n", "", "present", ""},
		{"no table header", "[mcp_servers]\nkoinon.command = \"k\"\n", "", "missing", "settings_unusual"},
		// A header line inside a string is not the table: the line would land at the top level.
		{"header in a string", "note = \"\"\"\n[mcp_servers.koinon]\nx\n\"\"\"\n[mcp_servers]\nkoinon.command = \"k\"\n", "", "missing", "settings_unusual"},
		{"inline table", "mcp_servers.koinon = { command = \"k\" }\n", "", "missing", "settings_unusual"},
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
			wantCheck, checkReason := c.outcome, c.reason
			if wantCheck == "added" {
				wantCheck = "missing"
			}
			if wantCheck == "missing" {
				// Only the write finds an unusual layout.
				checkReason = ""
			}
			if err != nil || check.Outcome != wantCheck || check.Reason != checkReason || check.SettingsFile != path {
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
				var config map[string]any
				if _, err := toml.Decode(string(data), &config); err != nil {
					t.Fatalf("written file is not TOML: %v", err)
				}
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
		// A different rule leaves the whole file as it is (#449).
		{"bash action", `{"permission":{"bash":"ask"}}`, "different", nil},
		{"different server rule", `{"permission":{"koinon_*":"deny","bash":{}}}`, "different", nil},
		{"different guide rule", `{"permission":{"bash":{` + strings.Replace(guide, "allow", "ask", 1) + `}}}`, "different", nil},
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
	// "No MCP server named koinon" is an answer: the server is not there (#551).
	if in := Inspect(context.Background(), o); in.CLI != cli || in.Configured || in.Error != "" || in.Permissions == nil || in.Permissions.Outcome != "missing" {
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
	// A CLI that fails is not read, which differs from a missing server.
	if in := Inspect(context.Background(), Options{Family: "claude", CLI: filepath.Join(dir, "none"), Binary: o.Binary}); in.Configured || in.Error == "" || in.Permissions == nil {
		t.Fatalf("missing CLI: %+v", in)
	}
	t.Setenv("PATH", dir)
	if in := Inspect(context.Background(), Options{Family: "codex", Binary: o.Binary}); in.CLI != "" || in.Permissions != nil {
		t.Fatalf("no CLI on PATH: %+v", in)
	}
}

// A CLI child that holds the output open does not hold an inspection past its context. Output
// that has not ended after a clean exit, where a child may still write the entry, is an error
// too, not an answer (#551).
func TestInspectBoundsAHeldPipe(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "codex")
	inspect := func(ctx context.Context) (Inspection, time.Duration) {
		start := time.Now()
		in := Inspect(ctx, Options{Family: "codex", CLI: cli, Binary: "/opt/koinon/bin/koinon", StateDir: filepath.Join(dir, "state")})
		return in, time.Since(start)
	}
	os.WriteFile(cli, []byte("#!/bin/sh\nsleep 3 &\nwait\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if in, elapsed := inspect(ctx); elapsed > time.Second || in.Error == "" || in.Configured {
		t.Fatalf("stopped CLI: %+v after %s", in, elapsed)
	}
	os.WriteFile(cli, []byte("#!/bin/sh\n(sleep 1; echo '{\"name\":\"koinon\"}') &\necho starting\n"), 0700)
	if in, elapsed := inspect(context.Background()); elapsed > 900*time.Millisecond || in.Error == "" || in.Configured {
		t.Fatalf("clean exit with a writing child: %+v after %s", in, elapsed)
	}
	// Setup's own runs keep waiting for the whole output.
	if out, err := runCLI(context.Background(), cli); err != nil || !strings.Contains(out, "koinon") {
		t.Fatalf("setup run: %q %v", out, err)
	}
}
