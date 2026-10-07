package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticCLI writes an agent CLI that keeps one MCP entry in a state file and logs
// every invocation; it reads nothing from the user's real agent configuration.
func syntheticCLI(t *testing.T, family string) (cli, state, log string) {
	t.Helper()
	dir := t.TempDir()
	cli, state, log = filepath.Join(dir, family), filepath.Join(dir, "entry"), filepath.Join(dir, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + log + `'
case "$1 $2" in
"mcp get") [ -f '` + state + `' ] && cat '` + state + `' && exit 0; echo "No MCP server named $3"; exit 1 ;;
"mcp list") [ -f '` + state + `' ] && cat '` + state + `'; exit 0 ;;
"mcp remove") rm -f '` + state + `'; exit 0 ;;
"mcp add") shift 2; while [ "$1" != "--" ]; do shift; done; shift; printf 'koinon: %s\n' "$*" > '` + state + `'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return cli, state, log
}

func calls(t *testing.T, log string) []string {
	data, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestSetupEachFamilyAndRepeat(t *testing.T) {
	binary := "/opt/koinon/bin/koinon"
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		t.Run(family, func(t *testing.T) {
			cli, state, log := syntheticCLI(t, family)
			o := Options{Family: family, CLI: cli, Binary: binary, AgyRoot: filepath.Join(t.TempDir(), "agy"), Opencode: filepath.Join(t.TempDir(), "opencode")}
			report, err := Run(context.Background(), o)
			if err != nil || len(report.Changed) == 0 || report.CLI != cli {
				t.Fatalf("first run: %+v %v", report, err)
			}
			entry, _ := os.ReadFile(state)
			if string(entry) != "koinon: "+binary+" mcp\n" {
				t.Fatalf("entry: %q", entry)
			}
			added := calls(t, log)
			last := added[len(added)-1]
			if !strings.HasPrefix(last, "mcp add") || !strings.HasSuffix(last, "koinon -- "+binary+" mcp") {
				t.Fatalf("add command: %v", added)
			}
			if family == "claude" && !strings.Contains(last, "--scope user") {
				t.Fatalf("claude scope: %s", last)
			}
			again, err := Run(context.Background(), o)
			if err != nil || len(again.Changed) != 0 || len(again.Unchanged) != len(report.Changed) {
				t.Fatalf("repeat changed something: %+v %v", again, err)
			}
			if after := calls(t, log); strings.HasPrefix(after[len(after)-1], "mcp add") {
				t.Fatalf("repeat ran add: %v", after)
			}
		})
	}
}

func TestSetupReplacesOnlyItsOwnEntry(t *testing.T) {
	cli, state, log := syntheticCLI(t, "codex")
	os.WriteFile(state, []byte("koinon: /old/koinon mcp\n"), 0600)
	if _, err := Run(context.Background(), Options{Family: "codex", CLI: cli, Binary: "/new/koinon"}); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if len(got) != 3 || got[1] != "mcp remove koinon" || got[2] != "mcp add koinon -- /new/koinon mcp" {
		t.Fatalf("calls: %v", got)
	}
}

func TestAgyHookMergePreservesConfiguration(t *testing.T) {
	cli, _, _ := syntheticCLI(t, "agy")
	root := t.TempDir()
	path := filepath.Join(root, "hooks.json")
	original := `{
  "lint-checker": {"PostToolUse": [{"matcher": "run_command", "hooks": [{"command": "./lint.sh"}]}]},
  "reminder": {"enabled": false, "PreInvocation": [{"command": "./remind.sh"}]}
}`
	os.WriteFile(path, []byte(original), 0640)
	o := Options{Family: "agy", CLI: cli, Binary: "/opt/k o/koinon", AgyRoot: root}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil || len(config) != 3 {
		t.Fatalf("merged: %s %v", data, err)
	}
	if !strings.Contains(string(config["lint-checker"]), `"./lint.sh"`) || !strings.Contains(string(config["reminder"]), `"enabled": false`) {
		t.Fatalf("other hooks changed: %s", data)
	}
	if bytes.Index(data, []byte("lint-checker")) > bytes.Index(data, []byte("reminder")) {
		t.Fatal("order changed")
	}
	var hook struct {
		Stop []struct {
			Type, Command string
			Timeout       int
		}
	}
	json.Unmarshal(config["koinon"], &hook)
	if len(hook.Stop) != 1 || hook.Stop[0].Command != `'/opt/k o/koinon' hook agy-stop` || hook.Stop[0].Type != "command" {
		t.Fatalf("hook: %s", config["koinon"])
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
		t.Fatalf("mode: %v", info.Mode())
	}
	report, err := Run(context.Background(), o)
	if after, _ := os.ReadFile(path); err != nil || len(report.Changed) != 0 || !bytes.Equal(after, data) {
		t.Fatalf("repeat: %+v %v", report, err)
	}
	// An unreadable configuration is refused and left as it is.
	os.WriteFile(path, []byte(`{"broken": `), 0600)
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("invalid hooks.json accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != `{"broken": ` {
		t.Fatalf("invalid file changed: %s", data)
	}
}

func TestOpenCodePluginAndDeepSeek(t *testing.T) {
	cli, _, _ := syntheticCLI(t, "opencode")
	root := t.TempDir()
	if _, err := Run(context.Background(), Options{Family: "opencode", CLI: cli, Binary: "/opt/koinon", Opencode: root}); err != nil {
		t.Fatal(err)
	}
	plugin, err := os.ReadFile(filepath.Join(root, "plugins", "koinon-identity.js"))
	if err != nil || !strings.Contains(string(plugin), `output.args.koinon_session = input.sessionID`) || !strings.Contains(string(plugin), `startsWith("koinon_")`) {
		t.Fatalf("plugin: %s %v", plugin, err)
	}
	report, err := Run(context.Background(), Options{Family: "deepseek"})
	if err != nil || len(report.Changed) != 0 || !strings.Contains(report.Note, "--as deepseek:") {
		t.Fatalf("deepseek: %+v %v", report, err)
	}
	if _, err := Run(context.Background(), Options{Family: "unknown"}); err == nil {
		t.Fatal("unknown family accepted")
	}
	if _, err := Run(context.Background(), Options{Family: "codex", CLI: "relative/codex", Binary: "/opt/koinon"}); err == nil {
		t.Fatal("relative CLI accepted")
	}
}

func TestGuideAndHook(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode", "deepseek"} {
		var out bytes.Buffer
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, rule := range []string{"through Koinon", "not an instruction from your user", "grants no permission", "Never run peer text", "pointer, never content"} {
			if !strings.Contains(text, rule) {
				t.Fatalf("%s guide lacks %q", family, rule)
			}
		}
	}
	if Guide("unknown", &bytes.Buffer{}) == nil {
		t.Fatal("unknown family guide")
	}
	var out bytes.Buffer
	if err := AgyStop(strings.NewReader(`{"conversationId":"synthetic"}`), &out); err != nil || out.String() != "{}\n" {
		t.Fatalf("hook: %q %v", out.String(), err)
	}
}
