package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/launcher"
	"github.com/rwcii/koinon/internal/setup"
)

// Bind documentation to the actual registry, including tools appended during init.
func TestPermissionRecommendationsCoverRegisteredTools(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../docs/PERMISSIONS.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| .+ \\|$").FindAllSubmatch(data, -1)
	documented := map[string]int{}
	for _, row := range rows {
		documented[string(row[1])]++
	}
	registered := map[string]bool{}
	for _, tool := range toolList {
		name := tool["name"].(string)
		registered[name] = true
		if documented[name] != 1 {
			t.Errorf("registered tool %s needs exactly one purpose row, got %d", name, documented[name])
		}
	}
	for name := range documented {
		if !registered[name] {
			t.Errorf("documented tool %s is absent from the registry", name)
		}
	}
	for family := range launcher.Families {
		t.Run(family, func(t *testing.T) {
			var guide bytes.Buffer
			if err := setup.Guide(family, &guide); err != nil {
				t.Fatal(err)
			}
			text := guide.String()
			if family == "codex" {
				if !strings.Contains(text, "[mcp_servers.koinon]\n  default_tools_approval_mode = \"approve\"") {
					t.Fatal("server-scoped approval must cover every registered Koinon tool")
				}
				return
			}
			var patterns []string
			if family == "grok" {
				// Grok takes its rule as a launch flag: MCPTool(server__tool).
				rule := regexp.MustCompile(`--allow 'MCPTool\(([^)]*)\)'`).FindStringSubmatch(text)
				if rule == nil || !strings.HasPrefix(rule[1], "koinon__") {
					t.Fatal("missing Grok MCPTool rule scoped to the koinon server")
				}
				patterns = append(patterns, strings.TrimPrefix(rule[1], "koinon__"))
			}
			var example map[string]any
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "  {\"") {
					if err := json.Unmarshal([]byte(line), &example); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if example == nil && family != "grok" {
				t.Fatal("missing copyable permission JSON")
			}
			switch family {
			case "grok":
			case "claude", "agy":
				permissions, ok := example["permissions"].(map[string]any)
				if !ok {
					t.Fatal("missing native permissions object")
				}
				allow, ok := permissions["allow"].([]any)
				if !ok {
					t.Fatal("missing native allow list")
				}
				if family == "claude" {
					// The guide, docs/PERMISSIONS.md and the rules that setup reports agree.
					want, _ := json.Marshal(setup.ClaudePermissions)
					if got, _ := json.Marshal(allow); !bytes.Equal(got, want) {
						t.Fatalf("guide rules %s, setup rules %s", got, want)
					}
					block := regexp.MustCompile("(?s)## Claude Code\n.*?```json\n(.*?)```").FindSubmatch(data)
					var doc struct{ Permissions struct{ Allow []string } }
					if block == nil || json.Unmarshal(block[1], &doc) != nil {
						t.Fatal("docs/PERMISSIONS.md has no Claude Code JSON example")
					}
					if got, _ := json.Marshal(doc.Permissions.Allow); !bytes.Equal(got, want) {
						t.Fatalf("docs/PERMISSIONS.md rules %s, setup rules %s", got, want)
					}
				}
				for _, entry := range allow {
					rule, ok := entry.(string)
					if !ok {
						t.Fatal("permission rule is not a string")
					}
					if family == "claude" && strings.HasPrefix(rule, "mcp__") {
						if !strings.HasPrefix(rule, "mcp__koinon__") {
							t.Fatal("MCP approval extends beyond the koinon server")
						}
						patterns = append(patterns, strings.TrimPrefix(rule, "mcp__koinon__"))
					}
					if family == "agy" && strings.HasPrefix(rule, "mcp(") {
						if !strings.HasPrefix(rule, "mcp(koinon/") || !strings.HasSuffix(rule, ")") {
							t.Fatal("MCP approval extends beyond the koinon server")
						}
						patterns = append(patterns, strings.TrimSuffix(strings.TrimPrefix(rule, "mcp(koinon/"), ")"))
					}
				}
			case "opencode":
				permissions, ok := example["permission"].(map[string]any)
				if !ok {
					t.Fatal("missing native permission object")
				}
				for name, action := range permissions {
					if name == "bash" {
						continue
					}
					if !strings.HasPrefix(name, "koinon_") || action != "allow" {
						t.Fatal("MCP approval extends beyond the koinon server")
					}
					patterns = append(patterns, strings.TrimPrefix(name, "koinon_"))
				}
			default:
				t.Fatal("new launcher family needs a native permission coverage check")
			}
			for name := range registered {
				covered := false
				for _, pattern := range patterns {
					match, err := path.Match(pattern, name)
					if err != nil {
						t.Fatal(err)
					}
					covered = covered || match
				}
				if !covered {
					t.Errorf("registered tool %s omitted from %s recommendation", name, family)
				}
			}
		})
	}
}
