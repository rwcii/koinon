package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
