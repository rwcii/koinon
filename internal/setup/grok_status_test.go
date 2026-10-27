package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

const grokSynthetic = "0f0e0d0c-0b0a-4908-8706-050403020100"

func grokEnv(t *testing.T, session string) (HookEnv, func() []core.Observation) {
	t.Helper()
	env, reports := reportingDaemon(t)
	base := env.Getenv
	env.Getenv = func(k string) string {
		if k == "GROK_SESSION_ID" {
			return session
		}
		return base(k)
	}
	return env, reports
}

// The Grok status-line hook reports the session's model and context use and passes the user's
// command through unchanged; it never invents a reading (#515).
func TestGrokStatusHook(t *testing.T) {
	env, reports := grokEnv(t, grokSynthetic)
	input := `{"session_id":"` + grokSynthetic + `","cwd":"/private","model":{"id":"grok-synthetic","display_name":"private"},
		"context_window":{"context_window_size":2000000,"context_tokens":13744,"used_percentage":1},"auto_compact_threshold_percent":85,
		"session_input_tokens":99999,"session_output_tokens":88,"session_usage":{"x":1}}`
	var out, errOut bytes.Buffer
	if status := GrokStatus([]string{"--command", "cat; printf ' two'; exit 4"}, strings.NewReader(input), &out, &errOut, env); status != 4 {
		t.Fatalf("status %d: %s", status, errOut.String())
	}
	if out.String() != input+" two" {
		t.Fatalf("output: %q", out.String())
	}
	got := reports()
	if len(got) != 1 || got[0].Caller != (core.Key{Family: "grok", ID: grokSynthetic}) || got[0].Model.ID != "grok-synthetic" ||
		got[0].Model.Source != "grok_statusline" || got[0].Context.Source != "grok_statusline" || got[0].Context.Epoch != grokSynthetic ||
		*got[0].Context.LimitTokens != 2000000 || *got[0].Context.UsedTokens != 13744 || !*got[0].Context.UsageAvailable || got[0].Activity != nil {
		t.Fatalf("report: %+v", got)
	}
	if data, _ := json.Marshal(got); strings.Contains(string(data), "private") {
		t.Fatal("status input content reported")
	}
	// Without a command it prints nothing and exits 0.
	out.Reset()
	if status := GrokStatus(nil, strings.NewReader(input), &out, &errOut, env); status != 0 || out.Len() != 0 {
		t.Fatalf("no command: %d %q", status, out.String())
	}
	// A wrapped command that a signal ends passes the signal on, as for Claude and agy.
	if status := GrokStatus([]string{"--command", "kill -TERM $$"}, strings.NewReader(input), &out, &errOut, env); status != 128+15 {
		t.Fatalf("signal: %d", status)
	}
	// Koinon's own failures never change the result: no daemon, and invalid input.
	offline := HookEnv{Getenv: func(k string) string {
		return map[string]string{"GROK_SESSION_ID": grokSynthetic, "KOINON_STATE_DIR": t.TempDir()}[k]
	}, Now: env.Now}
	for _, in := range []string{input, "not json", ""} {
		out.Reset()
		if status := GrokStatus([]string{"--command", "cat; exit 3"}, strings.NewReader(in), &out, &errOut, offline); status != 3 || out.String() != in {
			t.Fatalf("offline %q: %d %q", in, status, out.String())
		}
		if status := GrokStatus(nil, strings.NewReader(in), &out, &errOut, offline); status != 0 {
			t.Fatalf("offline without command %q: %d", in, status)
		}
	}
	// An input over 64 KiB gives no observation, and the wrapped command still gets all of it.
	big := `{"session_id":"` + grokSynthetic + `","model":{"id":"m"},"context_window":{"context_window_size":10,"context_tokens":1},"pad":"` +
		strings.Repeat("x", grokStatusMax) + `"}`
	before := len(reports())
	out.Reset()
	if status := GrokStatus([]string{"--command", "wc -c"}, strings.NewReader(big), &out, &errOut, env); status != 0 ||
		strings.TrimSpace(out.String()) != strconv.Itoa(len(big)) {
		t.Fatalf("oversized: %d %q", status, out.String())
	}
	if len(reports()) != before {
		t.Fatal("oversized input reported")
	}
}

// The caller is the input's session_id, the session's MCP identity: Grok Build 1.0.50 gives the
// status-line command no GROK_SESSION_ID. A GROK_SESSION_ID in the environment must name the same
// session (#515).
func TestGrokStatusIdentity(t *testing.T) {
	full := func(id string) string {
		return `{` + id + `"model":{"id":"m"},"context_window":{"context_window_size":10,"context_tokens":1}}`
	}
	for _, c := range []struct {
		name, env, input string
		report           bool
	}{
		{"no env", "", full(`"session_id":"` + grokSynthetic + `",`), true},
		{"same env", grokSynthetic, full(`"session_id":"` + grokSynthetic + `",`), true},
		{"other env", "other-session", full(`"session_id":"` + grokSynthetic + `",`), false},
		{"absent", "", full(""), false},
		{"absent with env", grokSynthetic, full(""), false},
		{"null", "", full(`"session_id":null,`), false},
		{"empty", "", full(`"session_id":"",`), false},
		{"number", "", full(`"session_id":7,`), false},
		{"not an ID", "", full(`"session_id":"bad id",`), false},
	} {
		o, _, ok := grokStatus(c.env)([]byte(c.input), 1)
		if ok != c.report || ok && (o.Caller != (core.Key{Family: "grok", ID: grokSynthetic}) || o.Context.Epoch != grokSynthetic) {
			t.Fatalf("%s: %v %+v", c.name, ok, o)
		}
	}
}

// Counts are JSON integers from 0 to 2^53-1, with a positive limit; anything else gives no
// context, and the model is read on its own (#515).
func TestGrokStatusCounts(t *testing.T) {
	read := func(window string) core.Observation {
		o, _, _ := grokStatus("")([]byte(`{"session_id":"`+grokSynthetic+`","model":{"id":"m"},"context_window":`+window+`}`), 1)
		return o
	}
	for window, want := range map[string]bool{
		`{"context_window_size":10,"context_tokens":0}`:                      true,
		`{"context_window_size":9007199254740991,"context_tokens":1}`:        true,
		`{"context_window_size":9007199254740992,"context_tokens":1}`:        false,
		`{"context_window_size":10,"context_tokens":-1}`:                     false,
		`{"context_window_size":10,"context_tokens":1.5}`:                    false,
		`{"context_window_size":10,"context_tokens":"1"}`:                    false,
		`{"context_window_size":10,"context_tokens":null}`:                   false,
		`{"context_window_size":0,"context_tokens":0}`:                       false,
		`{"context_window_size":10}`:                                         false,
		`{"context_tokens":1}`:                                               false,
		`{"context_window_size":10,"context_tokens":1,"used_percentage":50}`: true,
		`{"used_percentage":50}`:                                             false,
		`null`:                                                               false,
		`[]`:                                                                 false,
	} {
		o := read(window)
		if (o.Context != nil) != want || o.Model == nil || o.Model.ID != "m" {
			t.Fatalf("%s: %+v %+v", window, o.Context, o.Model)
		}
	}
	if o := read(`{"context_window_size":200,"context_tokens":13}`); *o.Context.LimitTokens != 200 || *o.Context.UsedTokens != 13 {
		t.Fatalf("values: %+v", o.Context)
	}
	if o, _, ok := grokStatus("")([]byte(`{"session_id":"`+grokSynthetic+`","context_window":{"context_window_size":10}}`), 1); ok || o.Model != nil {
		t.Fatalf("no reading: %v %+v", ok, o)
	}
}

func grokFixture(t *testing.T) (grokStatusFile, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "grok")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	return grokStatusFile{statusLine{binary: "/opt/koinon", state: state, settings: filepath.Join(home, "config.toml"),
		label: "Grok configuration", hook: "grok-status", record: "grok-statusline.json", missing: "grok_config_missing"}}, home
}

func grokStatusOf(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeTOML(raw)
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	table, _, _ := statusTable(value)
	return table
}

const grokWrapper = "'/opt/koinon' hook grok-status"

// Setup owns only type and command in [ui.status_line]: every other byte stays, and removal
// gives back the file as it was (#515).
func TestGrokStatusLineSetUpAndRemove(t *testing.T) {
	g, _ := grokFixture(t)
	original := `# Grok configuration
model = "grok-synthetic" # trailing comment

[ui]
theme = "dark"

[ui.status_line]
# the user's own line
type = "command"
command = "~/bin/line --short"
refresh_interval = 5

[other]
text = """
[ui.status_line]
type = "inside a string"
"""
"quoted key" = 'v'
`
	if err := os.WriteFile(g.settings, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	data, _ := os.ReadFile(g.settings)
	want := strings.Replace(original, `command = "~/bin/line --short"`, `command = "'/opt/koinon' hook grok-status --command '~/bin/line --short'"`, 1)
	if string(data) != want {
		t.Fatalf("set up:\n%s", data)
	}
	if table := grokStatusOf(t, g.settings); table["refresh_interval"] != int64(5) || table["type"] != "command" {
		t.Fatalf("table: %+v", table)
	}
	result, err = g.setUp()
	mustOutcome(t, result, err, "unchanged")
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != original {
		t.Fatalf("restored:\n%s", data)
	}
	result, err = g.remove()
	mustOutcome(t, result, err, "not_set_up")
	// Set up again after a removal, as a person who changed their mind would.
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != original {
		t.Fatalf("restored again:\n%s", data)
	}
}

// Without a table, setup adds one; removal takes away only what setup added (#515).
func TestGrokStatusLineAddsItsTable(t *testing.T) {
	for name, original := range map[string]string{
		"absent file":   "",
		"other content": "model = \"m\"\n\n[ui]\ntheme = \"dark\"\n",
		"no newline":    "model = \"m\"",
	} {
		t.Run(name, func(t *testing.T) {
			g, _ := grokFixture(t)
			if original != "" {
				os.WriteFile(g.settings, []byte(original), 0600)
			}
			result, err := g.setUp()
			mustOutcome(t, result, err, "set_up")
			table := grokStatusOf(t, g.settings)
			if len(table) != 2 || table["type"] != "command" || table["command"] != grokWrapper {
				t.Fatalf("table: %+v", table)
			}
			result, err = g.remove()
			mustOutcome(t, result, err, "restored")
			data, _ := os.ReadFile(g.settings)
			if want := strings.TrimSuffix(original, "\n"); strings.TrimSuffix(string(data), "\n") != want {
				t.Fatalf("restored: %q", data)
			}
		})
	}
	// A key the person added to setup's table keeps the table at removal.
	g, _ := grokFixture(t)
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	data, _ := os.ReadFile(g.settings)
	os.WriteFile(g.settings, append(data, []byte("refresh_interval = 2\n")...), 0600)
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if table := grokStatusOf(t, g.settings); len(table) != 1 || table["refresh_interval"] != int64(2) {
		t.Fatalf("kept table: %+v", table)
	}
	// A table that was there before keeps its header, even when setup's keys were its only ones.
	g, _ = grokFixture(t)
	os.WriteFile(g.settings, []byte("[ui.status_line]\n"), 0600)
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != "[ui.status_line]\n" {
		t.Fatalf("existing table: %q", data)
	}
}

// The edited wrapper, a change after setup, another config.toml and a missing directory are
// reported and left alone (#515).
func TestGrokStatusLineChanges(t *testing.T) {
	g, home := grokFixture(t)
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	// An edited wrapper is left alone.
	data, _ := os.ReadFile(g.settings)
	edited := strings.Replace(string(data), "grok-status", "grok-status --command 'mine'", 1)
	os.WriteFile(g.settings, []byte(edited), 0600)
	result, err = g.setUp()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
		t.Fatalf("edited wrapper: %+v %v", result, err)
	}
	// After the binary moves, the edited wrapper of the old binary is still Koinon's and is left
	// alone, never wrapped again.
	g.binary = "/new/koinon"
	result, err = g.setUp()
	if err != nil || result.Outcome != "changed" {
		t.Fatalf("edited wrapper of an old binary: %+v %v", result, err)
	}
	if data, _ := os.ReadFile(g.settings); string(data) != edited {
		t.Fatalf("edited wrapper of an old binary changed: %s", data)
	}
	result, err = g.remove()
	if err != nil || result.Outcome != "changed" {
		t.Fatalf("remove after edit: %+v %v", result, err)
	}
	if data, _ := os.ReadFile(g.settings); string(data) != edited {
		t.Fatalf("edited wrapper changed: %s", data)
	}
	// After that removal, setup leaves a wrapper that it cannot unwrap alone (#532).
	unreadable := strings.Replace(edited, "--command 'mine'", "--command 'mine' --extra", 1)
	os.WriteFile(g.settings, []byte(unreadable), 0600)
	result, err = g.setUp()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
		t.Fatalf("setup after removal of an edited wrapper: %+v %v", result, err)
	}
	if data, _ := os.ReadFile(g.settings); string(data) != unreadable {
		t.Fatalf("edited wrapper replaced after removal: %s", data)
	}
	// A shell operator after the quoted command is an edit too, never moved into the command.
	chained := strings.Replace(edited, "--command 'mine'", "--command 'mine';true", 1)
	os.WriteFile(g.settings, []byte(chained), 0600)
	result, err = g.setUp()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
		t.Fatalf("setup after removal of a chained wrapper: %+v %v", result, err)
	}
	if data, _ := os.ReadFile(g.settings); string(data) != chained {
		t.Fatalf("chained wrapper replaced after removal: %s", data)
	}
	// An unedited wrapper follows the binary, and removal still restores the original.
	g, _ = grokFixture(t)
	os.WriteFile(g.settings, []byte("[ui.status_line]\ntype = \"command\"\ncommand = \"old\"\n"), 0600)
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	g.binary = "/new/koinon"
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	if table := grokStatusOf(t, g.settings); table["command"] != "'/new/koinon' hook grok-status --command 'old'" {
		t.Fatalf("moved binary: %+v", table)
	}
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != "[ui.status_line]\ntype = \"command\"\ncommand = \"old\"\n" {
		t.Fatalf("restored after the move: %q", data)
	}
	// Another config.toml while one is set up: reported, and removal acts on the recorded file.
	g, home = grokFixture(t)
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	other := filepath.Join(t.TempDir(), "other")
	os.Mkdir(other, 0700)
	moved := g
	moved.settings = filepath.Join(other, "config.toml")
	result, err = moved.setUp()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_other_file" {
		t.Fatalf("other file: %+v %v", result, err)
	}
	if _, err := os.Stat(moved.settings); !os.IsNotExist(err) {
		t.Fatal("other file written")
	}
	result, err = moved.remove()
	if err != nil || result.Outcome != "restored" || result.SettingsFile != filepath.Join(home, "config.toml") {
		t.Fatalf("remove from the recorded file: %+v %v", result, err)
	}
	// A missing Grok directory is skipped.
	missing := g
	missing.settings = filepath.Join(t.TempDir(), "absent", "config.toml")
	missing.state = t.TempDir()
	result, err = missing.setUp()
	if err != nil || result.Outcome != "skipped" || result.Reason != "grok_config_missing" {
		t.Fatalf("missing: %+v %v", result, err)
	}
}

// An owned line keeps its key spelling, spacing and comment; removal gives back its value as it
// was spelled (#515).
func TestGrokStatusLineKeepsLineSpelling(t *testing.T) {
	g, _ := grokFixture(t)
	original := "[ui.status_line]\n\"type\" = 'command' # why type\ncommand\t=  'old' # why \"command\" = x\nrefresh_interval = 3\n"
	os.WriteFile(g.settings, []byte(original), 0600)
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	data, _ := os.ReadFile(g.settings)
	want := strings.Replace(original, "'old'", `"`+grokWrapper+` --command 'old'"`, 1)
	if string(data) != want {
		t.Fatalf("set up: %q", data)
	}
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != original {
		t.Fatalf("restored: %q", data)
	}
	// A record from before raw values restores the same meaning, with the comments.
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	path := filepath.Join(g.state, g.record)
	var record map[string]any
	saved, _ := os.ReadFile(path)
	if json.Unmarshal(saved, &record) != nil || record["raw_values"] == nil {
		t.Fatalf("record: %s", saved)
	}
	delete(record, "raw_values")
	saved, _ = json.Marshal(record)
	os.WriteFile(path, saved, 0600)
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != strings.Replace(original, "'old'", `"old"`, 1) {
		t.Fatalf("restored without raw values: %q", data)
	}
	// A saved token that does not spell the saved value is not used.
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	saved, _ = os.ReadFile(path)
	json.Unmarshal(saved, &record)
	record["raw_values"] = map[string]string{"command": "'x'", "type": "'command'"}
	saved, _ = json.Marshal(record)
	os.WriteFile(path, saved, 0600)
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != strings.Replace(original, "'old'", `"old"`, 1) {
		t.Fatalf("restored with a wrong token: %q", data)
	}
	// A quoted key that the lexer reads as an owned key gives no token, also after a refusal
	// saved the original as pending.
	unusual := original + "\"command='x'\" = \"unowned\"\n"
	os.WriteFile(g.settings, []byte(unusual), 0600)
	var refusal SettingsError
	if _, err := g.setUp(); !errors.As(err, &refusal) || refusal.Code != "settings_unusual" {
		t.Fatalf("quoted key: %v", err)
	}
	os.WriteFile(g.settings, []byte(original), 0600)
	result, err = g.setUp()
	mustOutcome(t, result, err, "set_up")
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != original {
		t.Fatalf("restored after the refusal: %q", data)
	}
}

func TestValueSpan(t *testing.T) {
	for line, want := range map[string]string{
		"k = \"a\\\"b\" # c\n": `"a\"b"`,
		"k='a\"b'":             `'a"b'`,
		"k = \"a\" # \"x\"\n":  `"a"`,
		"k = \"\"\"a\"\"\"\n":  "",
		"k = '''a'''\n":        "",
		"k = \"open\n":         "",
		"k = 'open\n":          "",
		"k = 1\n":              "",
		"k\n":                  "",
	} {
		from, to, ok := valueSpan(line)
		if ok && line[from:to] != want {
			t.Errorf("%q: %q, want %q", line, line[from:to], want)
		} else if !ok && want != "" {
			t.Errorf("%q: no value, want %q", line, want)
		}
	}
}

// Shapes that the text editor cannot prove are refused unchanged; that is the editor's limit
// (#515).
func TestGrokStatusLineRefusals(t *testing.T) {
	for name, c := range map[string]struct{ text, code string }{
		"invalid":       {"[ui\n", "settings_invalid"},
		"inline":        {"ui = { status_line = { type = \"command\", command = \"x\" } }\n", "settings_unusual"},
		"dotted":        {"[ui]\nstatus_line.type = \"command\"\n", "settings_unusual"},
		"dotted header": {"[ui]\nstatus_line = { refresh_interval = 1 }\n", "settings_unusual"},
		"not a table":   {"ui = 3\n", "settings_unusual"},
		"not a string":  {"[ui.status_line]\ntype = 3\n", "settings_unusual"},
		"multiline":     {"[ui.status_line]\ncommand = \"\"\"\nx\n\"\"\"\n", "settings_unusual"},
		// The lexer reads this quoted key as type; only the check of the meaning after the edit
		// finds that.
		"quoted equals": {"[ui.status_line]\n\"type=x\" = 1\n", "settings_unusual"},
	} {
		t.Run(name, func(t *testing.T) {
			g, _ := grokFixture(t)
			os.WriteFile(g.settings, []byte(c.text), 0600)
			_, err := g.setUp()
			var refusal SettingsError
			if !errors.As(err, &refusal) || refusal.Code != c.code {
				t.Fatalf("want %s: %v", c.code, err)
			}
			if data, _ := os.ReadFile(g.settings); string(data) != c.text {
				t.Fatalf("changed: %q", data)
			}
		})
	}
	// A file that changed since it was read is not replaced.
	g, _ := grokFixture(t)
	os.WriteFile(g.settings, []byte("a = 1\n"), 0600)
	c, err := g.read()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(g.settings, []byte("a = 2\n"), 0600)
	var refusal SettingsError
	if err := g.write(c, g.wrapperEntry(nil), nil, false); !errors.As(err, &refusal) || refusal.Code != "settings_conflict" {
		t.Fatalf("conflict: %v", err)
	}
	if data, _ := os.ReadFile(g.settings); string(data) != "a = 2\n" {
		t.Fatalf("conflicting change lost: %s", data)
	}
}

// Quoted keys and multiline strings that look like headers keep their meaning (#515).
func TestGrokStatusLineLexer(t *testing.T) {
	g, _ := grokFixture(t)
	original := "[ui.\"status_line\"]\n\"type\" = 'command'\ncommand = 'old'\narray = [\n  \"[ui.status_line]\",\n  { type = \"x\" },\n]\nnote = '''\n[other]\ncommand = \"no\"\n'''\n\n[after]\nk = 1\n"
	os.WriteFile(g.settings, []byte(original), 0600)
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	table := grokStatusOf(t, g.settings)
	if table["command"] != grokWrapper+" --command 'old'" || table["type"] != "command" || len(table) != 4 {
		t.Fatalf("table: %+v", table)
	}
	raw, _ := os.ReadFile(g.settings)
	value, _ := decodeTOML(raw)
	if after, _ := value["after"].(map[string]any); after["k"] != int64(1) {
		t.Fatalf("after: %+v", value)
	}
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	before, _ := decodeTOML([]byte(original))
	restored, _ := decodeTOML(current(g.settings))
	if a, b := jsonOf(before), jsonOf(restored); a != b {
		t.Fatalf("restored meaning:\n%s\n%s", a, b)
	}
}

func jsonOf(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
