package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/launcher"
)

// syntheticCLI writes an agent CLI that keeps MCP entries in a state file, one
// "NAME COMMAND ARGS" line each, prints them in the family's real output format, and logs
// every invocation; it reads nothing from the user's real agent configuration.
func syntheticCLI(t *testing.T, family string) (cli, state, log string) {
	t.Helper()
	dir := t.TempDir()
	cli, state, log = filepath.Join(dir, family), filepath.Join(dir, "entries"), filepath.Join(dir, "calls")
	formats := map[string]string{
		"codex":    `printf '{"name":"%s","enabled":true,"transport":{"type":"stdio","command":"%s","args":["%s"]}}\n' "$n" "$c" "$a"`,
		"claude":   `printf '%s:\n  Scope: User config\n  Type: stdio\n  Command: %s\n  Args: %s\n' "$n" "$c" "$a"`,
		"agy":      `printf '%s  stdio  enabled  %s %s\n' "$n" "$c" "$a"`,
		"opencode": `printf '\033[0m●  ✓ %s \033[90mconnected\n│      \033[90m%s %s\n│\n' "$n" "$c" "$a"`,
		"grok":     `printf '%s{"command":"%s","args":["%s"],"enabled":true,"name":"%s","scope":"user"}' "$sep" "$c" "$a" "$n"; sep=,`,
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + log + `'
touch '` + state + `'
show() { while read -r n c a; do [ -z "$1" ] || [ "$n" = "$1" ] || continue; ` + formats[family] + `; done < '` + state + `'; }
case "$1 $2" in
"mcp get") command grep -q "^$3 " '` + state + `' && show "$3" && exit 0; echo "No MCP server named $3"; exit 1 ;;
"mcp list") [ "` + family + `" = agy ] && echo "NAME    TYPE   STATUS   COMMAND/URL"
  [ "` + family + `" = grok ] && { printf '['; show ""; printf ']\n'; exit 0; }; show ""; exit 0 ;;
"--version ") echo "grok 1.0.49 (synthetic) [stable]"; exit 0 ;;
"mcp remove") for name; do :; done; command grep -v "^$name " '` + state + `' > '` + state + `.new'; mv '` + state + `.new' '` + state + `'; exit 0 ;;
"mcp add") shift 2; while [ "$1" != "--" ]; do name=$1; shift; done; shift
  command grep -v "^$name " '` + state + `' > '` + state + `.new'; mv '` + state + `.new' '` + state + `'
  printf '%s %s\n' "$name" "$*" >> '` + state + `'; exit 0 ;;
esac
exit 2
`
	if family == "opencode" {
		script = openCodeScript(log, state, 2)
	}
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return cli, state, log
}

// openCodeScript is a synthetic OpenCode of the given major version (#692): the script logs
// the call and runs TestSyntheticOpenCode in this test binary, which acts as the real one.
func openCodeScript(log, state string, major int) string {
	return `#!/bin/sh
printf '%s\n' "$*" >> '` + log + `'
KOINON_SYNTHETIC_OPENCODE=` + fmt.Sprint(major) + ` KOINON_SYNTHETIC_STATE='` + state + `' \
KOINON_SYNTHETIC_XDG=$XDG_CONFIG_HOME KOINON_SYNTHETIC_PWD=$PWD exec '` + os.Args[0] + `' -test.run='^TestSyntheticOpenCode$' -- "$@"
`
}

// TestSyntheticOpenCode is the synthetic OpenCode CLI, run only by openCodeScript. Its `mcp add`
// merges the entry into the configuration file as the real one does: OpenCode 1 always writes
// mcp.NAME into $XDG_CONFIG_HOME/opencode/opencode.json; OpenCode 2 writes mcp.servers.NAME
// there with --global, and into the working directory's opencode.json without it. Its
// `mcp list` prints no command, as OpenCode 2.3.0 does. Each add is also kept in the state
// file, one "NAME COMMAND ARGS" line each.
func TestSyntheticOpenCode(t *testing.T) {
	major := os.Getenv("KOINON_SYNTHETIC_OPENCODE")
	if major == "" {
		t.Skip("run by openCodeScript only")
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	state := os.Getenv("KOINON_SYNTHETIC_STATE")
	lines, _ := os.ReadFile(state)
	switch {
	case slices.Equal(args, []string{"mcp", "add", "--help"}):
		fmt.Println("  --url string  URL for a remote MCP server")
		if major == "2" {
			fmt.Println("  --global      Write to the global config instead of the project config")
		}
	case slices.Equal(args, []string{"mcp", "list"}):
		for _, line := range strings.Fields(string(lines)) {
			if !strings.HasPrefix(line, "/") && line != "mcp" {
				fmt.Printf("✓ %s  connected\n", line)
			}
		}
	case len(args) > 2 && args[0] == "mcp" && args[1] == "add":
		// TestMain replaces the environment and the directory; the script kept the caller's.
		dir := os.Getenv("KOINON_SYNTHETIC_PWD")
		dash := slices.Index(args, "--")
		global := slices.Contains(args[:dash], "--global")
		name := args[dash-1]
		if major == "1" || global {
			dir = filepath.Join(os.Getenv("KOINON_SYNTHETIC_XDG"), "opencode")
		}
		path := filepath.Join(dir, "opencode.json")
		config := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(data, &config); err != nil {
				os.Exit(3)
			}
		}
		mcp, _ := config["mcp"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
		}
		servers := mcp
		if major == "2" {
			if servers, _ = mcp["servers"].(map[string]any); servers == nil {
				servers = map[string]any{}
			}
			mcp["servers"] = servers
		}
		servers[name] = map[string]any{"type": "local", "command": args[dash+1:]}
		config["mcp"] = mcp
		data, _ := json.MarshalIndent(config, "", "  ")
		os.MkdirAll(dir, 0700)
		if os.WriteFile(path, data, 0600) != nil {
			os.Exit(3)
		}
		var kept []string
		for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
			if line != "" && !strings.HasPrefix(line, name+" ") {
				kept = append(kept, line)
			}
		}
		kept = append(kept, name+" "+strings.Join(args[dash+1:], " "))
		os.WriteFile(state, []byte(strings.Join(kept, "\n")+"\n"), 0600)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

// xdgOpenCode makes base the XDG configuration directory, where OpenCode keeps its global
// configuration, and gives that configuration's directory.
func xdgOpenCode(t *testing.T, base string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", base)
	return filepath.Join(base, "opencode")
}

func calls(t *testing.T, log string) []string {
	data, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestSetupEachFamilyAndRepeat(t *testing.T) {
	binary := "/opt/koinon/bin/koinon"
	for _, family := range []string{"claude", "codex", "agy", "opencode", "grok"} {
		t.Run(family, func(t *testing.T) {
			cli, state, log := syntheticCLI(t, family)
			o := Options{Family: family, CLI: cli, Binary: binary, StateDir: filepath.Join(t.TempDir(), "state"),
				ClaudeConfig: filepath.Join(t.TempDir(), "claude"), AgyRoot: filepath.Join(t.TempDir(), "agy"), Opencode: xdgOpenCode(t, t.TempDir())}
			report, err := Run(context.Background(), o)
			if err != nil || len(report.Changed) == 0 || report.CLI != cli {
				t.Fatalf("first run: %+v %v", report, err)
			}
			entry, _ := os.ReadFile(state)
			if string(entry) != "koinon "+binary+" mcp\n" {
				t.Fatalf("entry: %q", entry)
			}
			added := calls(t, log)
			last := added[len(added)-1]
			if family == "grok" {
				// The version check follows the add; no remove precedes it without an entry.
				last = added[len(added)-2]
				if strings.Contains(strings.Join(added, "\n"), "mcp remove") {
					t.Fatalf("grok removed an absent entry: %v", added)
				}
			}
			if !strings.HasPrefix(last, "mcp add") || !strings.HasSuffix(last, "koinon -- "+binary+" mcp") {
				t.Fatalf("add command: %v", added)
			}
			if (family == "claude" || family == "grok") && !strings.Contains(last, "--scope user") {
				t.Fatalf("%s scope: %s", family, last)
			}
			if family == "grok" && !strings.Contains(report.Note, "Grok Build 1.0.49 is installed; the Koinon evidence covers 1.0.50") ||
				family == "grok" && !strings.Contains(report.Note, "MCPTool(koinon__*)") {
				t.Fatalf("grok note: %q", report.Note)
			}
			if path, err := launcher.ConfiguredCLI(o.StateDir, family, ""); err != nil || path != cli {
				t.Fatalf("setup did not record CLI: %q %v", path, err)
			}
			again, err := Run(context.Background(), o)
			if err != nil || len(again.Changed) != 0 || len(again.Unchanged) != len(report.Changed) {
				t.Fatalf("repeat changed something: %+v %v", again, err)
			}
			if after := calls(t, log); strings.HasPrefix(after[len(after)-1], "mcp add") && after[len(after)-1] != "mcp add --help" {
				t.Fatalf("repeat ran add: %v", after)
			}
		})
	}
}

func TestSetupReplacesOnlyItsOwnEntry(t *testing.T) {
	cli, state, log := syntheticCLI(t, "codex")
	os.WriteFile(state, []byte("koinon /old/koinon mcp\n"), 0600)
	if _, err := Run(context.Background(), Options{Family: "codex", CLI: cli, Binary: "/new/koinon"}); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if len(got) != 3 || got[1] != "mcp remove koinon" || got[2] != "mcp add koinon -- /new/koinon mcp" {
		t.Fatalf("calls: %v", got)
	}
}

func TestSetupRecordsDiscoveredCLI(t *testing.T) {
	cli, _, _ := syntheticCLI(t, "codex")
	t.Setenv("PATH", filepath.Dir(cli)+string(filepath.ListSeparator)+os.Getenv("PATH"))
	state := filepath.Join(t.TempDir(), "state")
	report, err := Run(context.Background(), Options{Family: "codex", Binary: "/opt/koinon/bin/koinon", StateDir: state})
	if err != nil || report.CLI != cli {
		t.Fatalf("resolved CLI: %+v %v", report, err)
	}
	if path, err := launcher.ConfiguredCLI(state, "codex", ""); err != nil || path != cli {
		t.Fatalf("discovered path not recorded: %q %v", path, err)
	}
}

func TestSetupIdentifiesItsOwnEntry(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode", "grok"} {
		for _, entries := range []string{"other /opt/k/koinon mcp\n", "koinon /opt/k/koinon status\n"} {
			cli, state, _ := syntheticCLI(t, family)
			os.WriteFile(state, []byte(entries), 0600)
			report, err := Run(context.Background(), Options{Family: family, CLI: cli, Binary: "/opt/k/koinon", AgyRoot: t.TempDir(), Opencode: xdgOpenCode(t, t.TempDir())})
			data, _ := os.ReadFile(state)
			if err != nil || len(report.Changed) == 0 || !strings.Contains(string(data), "koinon /opt/k/koinon mcp\n") {
				t.Fatalf("%s with %q: %+v %v %q", family, entries, report, err, data)
			}
			if strings.HasPrefix(entries, "other") && !strings.Contains(string(data), "other /opt/k/koinon mcp") {
				t.Fatalf("%s removed another entry: %q", family, data)
			}
		}
	}
}

// Output captured from the real CLIs (Codex 0.160.0, Claude Code 2.1.290, agy 1.3.0) against
// scratch configuration directories. OpenCode is read from its files (#692).
func TestConfiguredMatchesRealOutput(t *testing.T) {
	const binary = "/opt/synthetic/koinon"
	codex := `{"name":"koinon","enabled":true,"disabled_reason":null,"transport":{"type":"stdio","command":"/opt/synthetic/koinon","args":["mcp"],"env":null,"env_vars":[],"cwd":null},"enabled_tools":null}`
	claude := "koinon:\n  Scope: User config (available in all your projects)\n  Status: ✘ Failed to connect\n  Type: stdio\n  Command: /opt/synthetic/koinon\n  Args: mcp\n  Environment:\n\nTo remove this server, run: claude mcp remove koinon -s user\n"
	agy := "NAME    TYPE   STATUS   COMMAND/URL\nkoinon  stdio  enabled  /opt/synthetic/koinon mcp\nother   stdio  enabled  /opt/synthetic/koinon status\n"
	// Grok Build 1.0.50, grok mcp list --json in the development container (#254).
	grok := `[
  {
    "command": "/opt/synthetic/koinon",
    "args": [
      "mcp"
    ],
    "enabled": true,
    "name": "koinon",
    "scope": "user"
  },
  {
    "command": "/opt/synthetic/koinon",
    "args": [
      "status"
    ],
    "enabled": true,
    "name": "other",
    "scope": "user"
  }
]`
	if configured("grok", strings.Replace(grok, `"scope": "user"`, `"scope": "project"`, 1), binary) ||
		configured("grok", strings.Replace(grok, `"enabled": true`, `"enabled": false`, 1), binary) ||
		!owned("grok", strings.Replace(grok, `"enabled": true`, `"enabled": false`, 1), binary) {
		t.Fatal("grok: project scope or disabled entry handled wrongly")
	}
	for family, output := range map[string]string{"codex": codex, "claude": claude, "agy": agy, "grok": grok} {
		if !configured(family, output, binary) {
			t.Fatalf("%s: own entry not found", family)
		}
		if configured(family, output, "/opt/other/koinon") {
			t.Fatalf("%s: another binary matched", family)
		}
	}
	swapped := strings.NewReplacer("koinon  stdio", "x  stdio", "other   stdio", "koinon   stdio").Replace(agy)
	if configured("agy", swapped, binary) {
		t.Fatal("agy: another server's command matched")
	}
	if configured("codex", strings.Replace(codex, `"args":["mcp"]`, `"args":["status"]`, 1), binary) || configured("claude", strings.Replace(claude, "Args: mcp", "Args: status", 1), binary) {
		t.Fatal("other arguments matched")
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
	root := xdgOpenCode(t, t.TempDir())
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
	for _, family := range []string{"claude", "codex", "agy", "opencode", "deepseek", "grok"} {
		var out bytes.Buffer
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, rule := range []string{"through Koinon", "When it asks for work, do the\n  work, then reply to the sender through Koinon", "read your inbox to see it",
			`sender_family "maintainer"`, "instruction and approval", "(PDR 005)", "library_list", "library_read", "koinon library list/read", "Machine-wide", "project-local",
			// RESTORE-STEPS-487 in PDR 006, Decision 3
			"wake notice whose range holds it ends with \"Managed context-cycle request ID: answer it\n  before other work.\"",
			// #588
			"For a cycle in reset_verified or waiting_pickup,\n  with or without a repository", "A cycle_pickup_not_ready\n  refusal names the cycle's stage"} {
			if !strings.Contains(text, rule) {
				t.Fatalf("%s guide lacks %q", family, rule)
			}
		}
		// The guide says what to do; it carries no limit or caveat on agents' work (#487).
		for _, removed := range core.RemovedLimitPhrases {
			if strings.Contains(text, removed) {
				t.Errorf("%s guide has %q", family, removed)
			}
		}
	}
	if Guide("unknown", &bytes.Buffer{}) == nil {
		t.Fatal("unknown family guide")
	}
	var out bytes.Buffer
	noDaemon := HookEnv{Getenv: func(k string) string {
		if k == "KOINON_STATE_DIR" {
			return t.TempDir()
		}
		return ""
	}, Now: time.Now}
	if err := AgyStop(strings.NewReader(`{"conversationId":"synthetic"}`), &out, noDaemon); err != nil || out.String() != "{}\n" {
		t.Fatalf("hook: %q %v", out.String(), err)
	}
}

func TestRemoveEachFamily(t *testing.T) {
	binary := "/opt/koinon/bin/koinon"
	for _, family := range []string{"claude", "codex", "agy", "opencode", "grok"} {
		t.Run(family, func(t *testing.T) {
			cli, state, _ := syntheticCLI(t, family)
			dir := t.TempDir()
			o := Options{Family: family, CLI: cli, Binary: binary, AgyRoot: filepath.Join(dir, "agy"), Opencode: xdgOpenCode(t, dir),
				StateDir: filepath.Join(dir, "state"), ClaudeConfig: filepath.Join(dir, "claude")}
			os.MkdirAll(o.StateDir, 0700)
			if family == "agy" {
				os.MkdirAll(o.AgyRoot, 0700)
				os.WriteFile(filepath.Join(o.AgyRoot, "hooks.json"), []byte(`{"other": {"Stop": []}}`), 0600)
			}
			if _, err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			if family == "opencode" {
				// opencode mcp add writes its configuration file; it has no remove command.
				os.WriteFile(filepath.Join(o.Opencode, "opencode.json"), []byte(`{"$schema": "https://opencode.ai/config.json",
  "mcp": {"other": {"type": "local", "command": ["x"]}, "koinon": {"type": "local", "command": ["`+binary+`", "mcp"]}}, "model": "m"}`), 0600)
			}
			report, err := Remove(context.Background(), o)
			if err != nil || len(report.Changed) == 0 {
				t.Fatalf("remove: %+v %v", report, err)
			}
			if entries, _ := os.ReadFile(state); family != "opencode" && strings.Contains(string(entries), "koinon ") {
				t.Fatalf("entry left: %q", entries)
			}
			switch family {
			case "claude":
				if report.StatusLine == nil {
					t.Fatal("status line not removed")
				}
			case "agy":
				hooks, _ := os.ReadFile(filepath.Join(o.AgyRoot, "hooks.json"))
				if strings.Contains(string(hooks), "agy-stop") || !strings.Contains(string(hooks), `"other"`) {
					t.Fatalf("hooks: %s", hooks)
				}
			case "opencode":
				config, _ := os.ReadFile(filepath.Join(o.Opencode, "opencode.json"))
				var parsed map[string]any
				if json.Unmarshal(config, &parsed) != nil || parsed["model"] != "m" || parsed["$schema"] == nil {
					t.Fatalf("config damaged: %s", config)
				}
				if servers := parsed["mcp"].(map[string]any); servers["koinon"] != nil || servers["other"] == nil {
					t.Fatalf("mcp servers: %s", config)
				}
				if _, err := os.Stat(filepath.Join(o.Opencode, "plugins", "koinon-identity.js")); !os.IsNotExist(err) {
					t.Fatalf("plugin left: %v", err)
				}
			}
			again, err := Remove(context.Background(), o)
			if err != nil || len(again.Changed) != 0 {
				t.Fatalf("repeat removed something: %+v %v", again, err)
			}
		})
	}
}

func TestRemoveLeavesOtherEntries(t *testing.T) {
	cli, state, _ := syntheticCLI(t, "codex")
	os.WriteFile(state, []byte("koinon /other/koinon mcp\n"), 0600)
	report, err := Remove(context.Background(), Options{Family: "codex", CLI: cli, Binary: "/opt/koinon/bin/koinon"})
	if err != nil || len(report.Changed) != 0 {
		t.Fatalf("remove: %+v %v", report, err)
	}
	if entries, _ := os.ReadFile(state); string(entries) != "koinon /other/koinon mcp\n" {
		t.Fatalf("another binary's entry removed: %q", entries)
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugins", "koinon-identity.js")
	os.MkdirAll(filepath.Dir(plugin), 0700)
	os.WriteFile(plugin, []byte("// edited"), 0600)
	os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte("// comment\n{}"), 0600)
	report, err = Remove(context.Background(), Options{Family: "opencode", Binary: "/opt/koinon/bin/koinon", Opencode: dir})
	if err != nil || len(report.Changed) != 0 {
		t.Fatalf("opencode remove: %+v %v", report, err)
	}
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("edited plugin removed: %v", err)
	}
}

// TestRemoveDisabledEntries (review F7): a disabled entry that still runs this binary is
// removed; a disabled entry of another binary stays.
func TestRemoveDisabledEntries(t *testing.T) {
	binary := "/opt/koinon/bin/koinon"
	for _, c := range []struct {
		family, output string
		removed        bool
	}{
		{"codex", `{"name":"koinon","enabled":false,"transport":{"type":"stdio","command":"` + binary + `","args":["mcp"]}}`, true},
		{"codex", `{"name":"koinon","enabled":false,"transport":{"type":"stdio","command":"/other/koinon","args":["mcp"]}}`, false},
		{"agy", "NAME    TYPE   STATUS   COMMAND/URL\nkoinon  stdio  disabled  " + binary + " mcp", true},
	} {
		dir := t.TempDir()
		log := filepath.Join(dir, "calls")
		cli := filepath.Join(dir, c.family)
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncase \"$2\" in get|list) cat <<'OUT'\n" + c.output + "\nOUT\n;; esac\nexit 0\n"
		os.WriteFile(cli, []byte(script), 0700)
		report, err := Remove(context.Background(), Options{Family: c.family, CLI: cli, Binary: binary, AgyRoot: filepath.Join(dir, "agy-config")})
		removed := strings.Contains(strings.Join(calls(t, log), "\n"), "mcp remove")
		if err != nil || removed != c.removed || (len(report.Changed) > 0) != c.removed {
			t.Fatalf("%s %q: removed %v report %+v %v", c.family, c.output, removed, report, err)
		}
	}
}

// Adopt records the CLI of a family whose koinon entry runs this binary, after reading the
// entry only; another binary's entry, no entry, no CLI or a usable record change nothing
// (#273).
func TestAdoptRecordsOnlyOwnConfiguredFamily(t *testing.T) {
	binary := "/opt/koinon/bin/koinon"
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		t.Run(family, func(t *testing.T) {
			cli, entries, log := syntheticCLI(t, family)
			os.WriteFile(entries, []byte("koinon "+binary+" mcp\n"), 0600)
			if family == "opencode" {
				root := xdgOpenCode(t, t.TempDir())
				os.MkdirAll(root, 0700)
				os.WriteFile(filepath.Join(root, "opencode.json"), []byte(`{"mcp":{"servers":{"koinon":{"type":"local","command":["`+binary+`","mcp"]}}}}`), 0600)
			}
			state := filepath.Join(t.TempDir(), "state")
			t.Setenv("PATH", filepath.Dir(cli)+string(filepath.ListSeparator)+os.Getenv("PATH"))
			report, adopted, err := Adopt(context.Background(), Options{Family: family, Binary: binary, StateDir: state})
			if err != nil || !adopted || report.CLI != cli || len(report.Changed) != 1 {
				t.Fatalf("adopt: %+v %v %v", report, adopted, err)
			}
			if path, err := launcher.ConfiguredCLI(state, family, ""); err != nil || path != cli {
				t.Fatalf("not recorded: %q %v", path, err)
			}
			if got := calls(t, log); len(got) != 1 || !strings.HasPrefix(got[0], "mcp get") && got[0] != "mcp list" && got[0] != "mcp add --help" {
				t.Fatalf("adopt did more than read the entry: %v", got)
			}
			if data, _ := os.ReadFile(entries); string(data) != "koinon "+binary+" mcp\n" {
				t.Fatalf("entries changed: %q", data)
			}
			if _, adopted, err := Adopt(context.Background(), Options{Family: family, Binary: binary, StateDir: state}); err != nil || adopted {
				t.Fatalf("usable record adopted again: %v %v", adopted, err)
			}
			if len(calls(t, log)) != 1 {
				t.Fatal("a usable record ran the CLI")
			}
		})
	}
	cli, entries, _ := syntheticCLI(t, "codex")
	t.Setenv("PATH", filepath.Dir(cli)+string(filepath.ListSeparator)+os.Getenv("PATH"))
	for name, content := range map[string]string{"other binary": "koinon /old/koinon mcp\n", "no entry": ""} {
		os.WriteFile(entries, []byte(content), 0600)
		state := filepath.Join(t.TempDir(), "state")
		if _, adopted, err := Adopt(context.Background(), Options{Family: "codex", Binary: binary, StateDir: state}); err != nil || adopted {
			t.Fatalf("%s adopted: %v %v", name, adopted, err)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatalf("%s wrote state: %v", name, err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, adopted, err := Adopt(context.Background(), Options{Family: "agy", Binary: binary, StateDir: filepath.Join(t.TempDir(), "state")}); err != nil || adopted {
		t.Fatalf("no CLI adopted: %v %v", adopted, err)
	}
}

// The guide's Team operations steps name the fact fields that give their project data, and
// scope the idle-prompt condition to command and text input, so an authorized first-run or
// permission answer stays available (#453, PDR 002 Decision 2).
func TestGuideTeamOperations(t *testing.T) {
	for family := range guideFamily {
		var out strings.Builder
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{
			"## Team operations",
			`DIR and ROLE come from the spin-up facts "Start directory" and "Roles"`,
			`the "Handoff path" fact of .koinon/skills/handoff.md`,
			"Send a command or a line of text at an empty, idle prompt",
			"Answer a first-run or permission dialog as in First-run prompts (PDR 002, Decision 2)",
			"/koinon-handoff", "$koinon-pickup",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s guide lacks %q", family, want)
			}
		}
		if strings.Contains(text, "Type into a pane only at an empty, idle prompt") || strings.Contains(text, "own _handoff directory") {
			t.Fatalf("%s guide keeps the unscoped condition or the fixed handoff path", family)
		}
	}
}

// syntheticOpenCode writes a synthetic OpenCode CLI of the given major version (#692).
func syntheticOpenCode(t *testing.T, major int) (cli, log string) {
	t.Helper()
	dir := t.TempDir()
	cli, log = filepath.Join(dir, "opencode"), filepath.Join(dir, "calls")
	if err := os.WriteFile(cli, []byte(openCodeScript(log, filepath.Join(dir, "entries"), major)), 0700); err != nil {
		t.Fatal(err)
	}
	return cli, log
}

// Setup writes the koinon server into OpenCode's global configuration in the shape of the
// installed major version, never into the working directory, and Inspect then finds it, so
// the install plan is current after one setup (#692).
func TestOpenCodeSetupEachVersion(t *testing.T) {
	binary := "/opt/my koinon/koinon"
	for _, major := range []int{1, 2} {
		t.Run(fmt.Sprint(major), func(t *testing.T) {
			cli, log := syntheticOpenCode(t, major)
			root := xdgOpenCode(t, t.TempDir())
			work := t.TempDir()
			t.Chdir(work)
			o := Options{Family: "opencode", CLI: cli, Binary: binary, StateDir: filepath.Join(t.TempDir(), "state")}
			if in := Inspect(context.Background(), o); in.Error != "" || in.Configured {
				t.Fatalf("before setup: %+v", in)
			}
			report, err := Run(context.Background(), o)
			if err != nil || !slices.Contains(report.Changed, "mcp server koinon") {
				t.Fatalf("setup: %+v %v", report, err)
			}
			if entries, _ := os.ReadDir(work); len(entries) != 0 {
				t.Fatalf("setup wrote into the working directory: %v", entries)
			}
			var config map[string]map[string]any
			data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatalf("config: %s %v", data, err)
			}
			servers := config["mcp"]
			if major == 2 {
				servers, _ = config["mcp"]["servers"].(map[string]any)
			}
			if entry, _ := servers["koinon"].(map[string]any); fmt.Sprint(entry["command"]) != "["+binary+" mcp]" {
				t.Fatalf("version %d entry: %s", major, data)
			}
			if in := Inspect(context.Background(), o); in.Error != "" || !in.Configured {
				t.Fatalf("after setup: %+v", in)
			}
			again, err := Run(context.Background(), o)
			if err != nil || !slices.Contains(again.Unchanged, "mcp server koinon") {
				t.Fatalf("repeat: %+v %v", again, err)
			}
			adds := 0
			for _, call := range calls(t, log) {
				if strings.HasPrefix(call, "mcp add") && call != "mcp add --help" {
					adds++
					if (major == 2) != strings.Contains(call, "--global") {
						t.Fatalf("version %d add: %s", major, call)
					}
				}
			}
			if adds != 1 {
				t.Fatalf("adds: %v", calls(t, log))
			}
		})
	}
}

// The check reads OpenCode's global files in its merge order, with JSONC comments and
// trailing commas, and counts only the entry in the installed version's shape (#692).
func TestOpenCodeConfigured(t *testing.T) {
	const binary = "/opt/koinon/koinon"
	v1 := `{"$schema": "https://opencode.ai/config.json", "mcp": {"koinon": {"type": "local", "command": ["` + binary + `", "mcp"]}}}`
	v2 := `{"$schema": "https://opencode.ai/config.json", "mcp": {"servers": {"koinon": {"type": "local", "command": ["` + binary + `", "mcp"]}}}}`
	for _, c := range []struct {
		name   string
		global bool
		files  map[string]string
		want   bool
	}{
		{"v1 entry", false, map[string]string{"opencode.json": v1}, true},
		{"v2 entry", true, map[string]string{"opencode.json": v2}, true},
		{"v1 entry under v2", true, map[string]string{"opencode.json": v1}, false},
		{"v2 entry under v1", false, map[string]string{"opencode.json": v2}, false},
		{"v1 disabled", false, map[string]string{"opencode.json": strings.Replace(v1, `"type"`, `"enabled": false, "type"`, 1)}, false},
		{"v2 disabled", true, map[string]string{"opencode.json": strings.Replace(v2, `"type"`, `"disabled": true, "type"`, 1)}, false},
		{"other binary", true, map[string]string{"opencode.json": strings.Replace(v2, binary, "/old/koinon", 1)}, false},
		{"other server", true, map[string]string{"opencode.json": strings.Replace(v2, `"koinon"`, `"other"`, 1)}, false},
		{"jsonc only", true, map[string]string{"opencode.jsonc": "// Koinon\n{\"mcp\": {\"servers\": {\"koinon\": {\"type\": \"local\", /* x */ \"command\": [\"" + binary + "\", \"mcp\",],},},},}\n"}, true},
		{"jsonc disables json entry", true, map[string]string{"opencode.json": v2,
			"opencode.jsonc": "{\"mcp\": {\"servers\": {\"koinon\": {\"disabled\": true,},},},} // off\n"}, false},
		{"json changes config.json command", true, map[string]string{"config.json": v2,
			"opencode.json": strings.Replace(v2, binary, "/old/koinon", 1)}, false},
		{"jsonc repairs json entry", true, map[string]string{"opencode.json": strings.Replace(v2, binary, "/old/koinon", 1),
			"opencode.jsonc": v2}, true},
	} {
		root := t.TempDir()
		for name, content := range c.files {
			os.WriteFile(filepath.Join(root, name), []byte(content), 0600)
		}
		if got, err := openCodeConfigured(root, c.global, binary, true); err != nil || got != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "opencode.json"), []byte(`{"mcp": `), 0600)
	if _, err := openCodeConfigured(root, true, binary, true); err == nil {
		t.Fatal("unreadable configuration accepted")
	}
}

// A later file that disables the entry makes setup fail after the add, never report a change;
// a CLI whose help fails is an error, not OpenCode 1 (#692).
func TestOpenCodeSetupFailures(t *testing.T) {
	cli, _ := syntheticOpenCode(t, 2)
	root := xdgOpenCode(t, t.TempDir())
	os.MkdirAll(root, 0700)
	os.WriteFile(filepath.Join(root, "opencode.jsonc"), []byte(`{"mcp": {"servers": {"koinon": {"disabled": true}}}}`), 0600)
	o := Options{Family: "opencode", CLI: cli, Binary: "/opt/koinon/koinon", StateDir: filepath.Join(t.TempDir(), "state")}
	if report, err := Run(context.Background(), o); err == nil || len(report.Changed) != 0 || !strings.Contains(err.Error(), "later file") {
		t.Fatalf("masked entry: %+v %v", report, err)
	}
	broken := filepath.Join(t.TempDir(), "opencode")
	os.WriteFile(broken, []byte("#!/bin/sh\necho broken >&2\nexit 1\n"), 0700)
	o.CLI = broken
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "--help") {
		t.Fatalf("failed help: %v", err)
	}
	if in := Inspect(context.Background(), o); in.Error == "" {
		t.Fatalf("failed help inspected: %+v", in)
	}
}

// Removal takes out either version's koinon entry, enabled or not, and leaves other servers
// and settings (#692).
func TestRemoveOpenCodeEachVersion(t *testing.T) {
	const binary = "/opt/koinon/koinon"
	for name, c := range map[string]struct{ before, left string }{
		"v2 with other": {`{"model":"m","mcp":{"servers":{"other":{"type":"local","command":["x"]},"koinon":{"type":"local","disabled":true,"command":["` + binary + `","mcp"]}}}}`,
			`{"mcp":{"servers":{"other":{"command":["x"],"type":"local"}}},"model":"m"}`},
		"v2 alone":     {`{"model":"m","mcp":{"servers":{"koinon":{"type":"local","command":["` + binary + `","mcp"]}}}}`, `{"model":"m"}`},
		"v1 disabled":  {`{"model":"m","mcp":{"koinon":{"type":"local","enabled":false,"command":["` + binary + `","mcp"]}}}`, `{"model":"m"}`},
		"other binary": {`{"mcp":{"servers":{"koinon":{"type":"local","command":["/old/koinon","mcp"]}}}}`, `{"mcp":{"servers":{"koinon":{"command":["/old/koinon","mcp"],"type":"local"}}}}`},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(c.before), 0600)
		if _, err := Remove(context.Background(), Options{Family: "opencode", Binary: binary, Opencode: dir}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "opencode.json"))
		if !sameJSON(data, []byte(c.left)) {
			t.Errorf("%s: left %s", name, data)
		}
	}
}
