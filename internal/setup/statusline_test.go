package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

func statusLineFixture(t *testing.T, binary string) (statusLine, string) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	return statusLine{binary: binary, state: state, settings: filepath.Join(config, "settings.json")}, config
}

func mustOutcome(t *testing.T, result StatusLineResult, err error, outcome string) {
	t.Helper()
	if err != nil || result.Outcome != outcome {
		t.Fatalf("want %s: %+v %v", outcome, result, err)
	}
}

func settingsEntry(t *testing.T, l statusLine) (json.RawMessage, []string) {
	t.Helper()
	_, fields, err := l.read()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range fields {
		keys = append(keys, f.key)
	}
	return lookup(fields, "statusLine"), keys
}

func TestStatusLineSetUpAndRemove(t *testing.T) {
	binary := "/opt/k o/it's/koinon"
	l, _ := statusLineFixture(t, binary)
	original := `{"type":"command","command":"bash $HOME/.claude/line.sh 'quoted arg' \"two\"","padding":2}`
	before := `{"model":"opus","statusLine":` + original + `,"permissions":{"allow":["Bash(ls:*)"]},"zeta":"é"}`
	if err := os.WriteFile(l.settings, []byte(before), 0640); err != nil {
		t.Fatal(err)
	}
	result, err := l.setUp()
	mustOutcome(t, result, err, "set_up")
	entry, keys := settingsEntry(t, l)
	if strings.Join(keys, ",") != "model,statusLine,permissions,zeta" {
		t.Fatalf("key order: %v", keys)
	}
	var wrapped struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Padding int    `json:"padding"`
	}
	json.Unmarshal(entry, &wrapped)
	words, err := splitWords(wrapped.Command)
	if err != nil || wrapped.Type != "command" || wrapped.Padding != 2 || len(words) != 5 || words[0] != binary ||
		words[1] != "hook" || words[2] != "claude-status" || words[3] != "--command" || words[4] != `bash $HOME/.claude/line.sh 'quoted arg' "two"` {
		t.Fatalf("wrapper: %s %q %v", entry, words, err)
	}
	info, _ := os.Stat(l.settings)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: %v", info.Mode())
	}
	data, _ := os.ReadFile(l.settings)
	if !bytes.Contains(data, []byte(`"zeta": "é"`)) || !bytes.HasSuffix(data, []byte("}\n")) {
		t.Fatalf("other settings changed: %s", data)
	}
	// Repeating set-up changes nothing and never wraps twice.
	result, err = l.setUp()
	mustOutcome(t, result, err, "unchanged")
	if again, _ := settingsEntry(t, l); !equal(again, entry) {
		t.Fatal("repeat changed the entry")
	}
	// Removal restores the original entry exactly.
	result, err = l.remove()
	mustOutcome(t, result, err, "restored")
	if restored, _ := settingsEntry(t, l); !equal(restored, json.RawMessage(original)) {
		t.Fatalf("restored: %s", restored)
	}
	// After removal the record says declined; another removal finds nothing set up.
	result, err = l.remove()
	mustOutcome(t, result, err, "not_set_up")
}

func TestStatusLineWithoutAnEntryAndAfterUserChanges(t *testing.T) {
	l, config := statusLineFixture(t, "/opt/koinon")
	// No settings file: set-up creates one with only the entry; removal deletes the entry.
	result, err := l.setUp()
	mustOutcome(t, result, err, "set_up")
	entry, _ := settingsEntry(t, l)
	if command := entryCommand(entry); command != "'/opt/koinon' hook claude-status" {
		t.Fatalf("bare wrapper: %q", command)
	}
	info, _ := os.Stat(l.settings)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("new file mode: %v", info.Mode())
	}
	result, err = l.remove()
	mustOutcome(t, result, err, "restored")
	if entry, keys := settingsEntry(t, l); entry != nil || len(keys) != 0 {
		t.Fatalf("entry left: %s %v", entry, keys)
	}
	// The user replaces the entry after set-up: setting up again wraps the new entry, and
	// removal restores it.
	if _, err := l.setUp(); err != nil {
		t.Fatal(err)
	}
	mine := `{"type":"command","command":"my-line"}`
	os.WriteFile(l.settings, []byte(`{"statusLine":`+mine+`}`), 0600)
	if _, err := l.setUp(); err != nil {
		t.Fatal(err)
	}
	if entry, _ := settingsEntry(t, l); !strings.Contains(entryCommand(entry), "--command 'my-line'") {
		t.Fatalf("rewrapped: %s", entry)
	}
	result, err = l.remove()
	mustOutcome(t, result, err, "restored")
	if entry, _ := settingsEntry(t, l); !equal(entry, json.RawMessage(mine)) {
		t.Fatalf("restored user entry: %s", entry)
	}
	// An edited wrapper is left alone by set-up and by removal.
	if _, err := l.setUp(); err != nil {
		t.Fatal(err)
	}
	edited := `{"type":"command","command":"'/opt/koinon' hook claude-status --command 'my-line' # edited"}`
	os.WriteFile(l.settings, []byte(`{"statusLine":`+edited+`}`), 0600)
	result, err = l.setUp()
	mustOutcome(t, result, err, "changed")
	result, err = l.remove()
	mustOutcome(t, result, err, "changed")
	if entry, _ := settingsEntry(t, l); !equal(entry, json.RawMessage(edited)) {
		t.Fatal("edited wrapper changed")
	}
	// A wrapper without a record is unwrapped, never wrapped again.
	os.Remove(filepath.Join(l.state, statusLineRecord))
	wrapper := l.wrapperEntry(json.RawMessage(mine))
	os.WriteFile(l.settings, []byte(`{"statusLine":`+string(wrapper)+`}`), 0600)
	result, err = l.setUp()
	mustOutcome(t, result, err, "unchanged")
	if entry, _ := settingsEntry(t, l); strings.Count(entryCommand(entry), "claude-status") != 1 {
		t.Fatalf("double wrap: %s", entry)
	}
	if _, err := l.remove(); err != nil {
		t.Fatal(err)
	}
	if entry, _ := settingsEntry(t, l); !equal(entry, json.RawMessage(mine)) {
		t.Fatalf("recovered original: %s", entry)
	}
	// An interrupted set-up (record pending, settings unchanged) finishes on the next run.
	l.save(statusRecord{State: "pending", Original: json.RawMessage(mine), Wrapper: wrapper})
	result, err = l.setUp()
	mustOutcome(t, result, err, "set_up")
	// Without a Claude configuration directory, set-up is skipped.
	missing := l
	missing.settings = filepath.Join(config, "absent", "settings.json")
	result, err = missing.setUp()
	mustOutcome(t, result, err, "skipped")
}

func TestStatusLineRefusals(t *testing.T) {
	l, _ := statusLineFixture(t, "/opt/koinon")
	for name, c := range map[string]struct{ content, code string }{
		"invalid":    {`{"statusLine":`, "settings_invalid"},
		"not object": {`[1,2]`, "settings_invalid"},
		"trailing":   {`{} {}`, "settings_invalid"},
	} {
		os.WriteFile(l.settings, []byte(c.content), 0600)
		var refusal SettingsError
		if _, err := l.setUp(); !errors.As(err, &refusal) || refusal.Code != c.code {
			t.Fatalf("%s: %v", name, err)
		}
		if data, _ := os.ReadFile(l.settings); string(data) != c.content {
			t.Fatalf("%s: file changed", name)
		}
	}
	os.Remove(l.settings)
	target := filepath.Join(t.TempDir(), "real.json")
	os.WriteFile(target, []byte(`{}`), 0600)
	os.Symlink(target, l.settings)
	var refusal SettingsError
	if _, err := l.setUp(); !errors.As(err, &refusal) || refusal.Code != "settings_symlink" {
		t.Fatalf("symlink: %v", err)
	}
	// A file that changed since it was read is not replaced.
	os.Remove(l.settings)
	os.WriteFile(l.settings, []byte(`{"a":1}`), 0600)
	raw, fields, err := l.read()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(l.settings, []byte(`{"a":2}`), 0600)
	if err := l.write(raw, replace(fields, "statusLine", json.RawMessage(`{"type":"command","command":"x"}`))); !errors.As(err, &refusal) || refusal.Code != "settings_conflict" {
		t.Fatalf("conflict: %v", err)
	}
	if data, _ := os.ReadFile(l.settings); string(data) != `{"a":2}` {
		t.Fatalf("conflicting change lost: %s", data)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(l.settings), ".settings.json.koinon")); !os.IsNotExist(err) {
		t.Fatal("temporary file left")
	}
}

func TestSplitWords(t *testing.T) {
	for _, value := range []string{"plain", "it's", `a "b" \c $d`, "", "  spaced  ", `'"'"'`} {
		words, err := splitWords(quote(value) + " next")
		if err != nil || len(words) != 2 || words[0] != value || words[1] != "next" {
			t.Fatalf("%q: %q %v", value, words, err)
		}
	}
	if words, err := splitWords(`a\ b "c \"d\"" e`); err != nil || strings.Join(words, "|") != `a b|c "d"|e` {
		t.Fatalf("escapes: %q %v", words, err)
	}
	for _, bad := range []string{`'open`, `"open`} {
		if _, err := splitWords(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// reportingDaemon is a synthetic daemon that records observation reports.
func reportingDaemon(t *testing.T) (HookEnv, func() []core.Observation) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	var mu sync.Mutex
	var reports []core.Observation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var o core.Observation
		if r.URL.Path == "/v1/sessions/observe" && json.NewDecoder(r.Body).Decode(&o) == nil {
			mu.Lock()
			reports = append(reports, o)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	env := map[string]string{"KOINON_STATE_DIR": root, "KOINON_DAEMON_ADDRESS": strings.TrimPrefix(server.URL, "http://")}
	return HookEnv{Getenv: func(k string) string { return env[k] }, Now: time.Now}, func() []core.Observation {
		mu.Lock()
		defer mu.Unlock()
		return append([]core.Observation{}, reports...)
	}
}

func TestClaudeStatusHook(t *testing.T) {
	env, reports := reportingDaemon(t)
	input := `{"session_id":"synthetic-claude","transcript_path":"/private/t.jsonl","model":{"id":"claude-synthetic","display_name":"Synthetic"},
		"context_window":{"context_window_size":200000,"total_input_tokens":42000,"current_usage":{"input_tokens":1}},"cwd":"/private"}`
	var out, errOut bytes.Buffer
	// The user's command gets the same input, and its output and status pass through.
	if status := ClaudeStatus([]string{"--command", "cat; echo; printf 'line two'; exit 3"}, strings.NewReader(input), &out, &errOut, env); status != 3 {
		t.Fatalf("status %d: %s", status, errOut.String())
	}
	if out.String() != input+"\nline two" {
		t.Fatalf("output: %q", out.String())
	}
	got := reports()
	if len(got) != 1 || got[0].Caller != (core.Key{Family: "claude", ID: "synthetic-claude"}) || got[0].Model.ID != "claude-synthetic" ||
		*got[0].Context.LimitTokens != 200000 || *got[0].Context.UsedTokens != 42000 || !*got[0].Context.UsageAvailable || got[0].Activity != nil {
		t.Fatalf("report: %+v", got)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "private") {
		t.Fatal("status input content reported")
	}
	// Without a command it prints the model's display name.
	out.Reset()
	if status := ClaudeStatus(nil, strings.NewReader(input), &out, &errOut, env); status != 0 || out.String() != "Synthetic\n" {
		t.Fatalf("default line: %d %q", status, out.String())
	}
	// A command ended by a signal reports it as the shell does.
	if status := ClaudeStatus([]string{"--command", "kill -TERM $$"}, strings.NewReader(input), &out, &errOut, env); status != 143 {
		t.Fatalf("signalled: %d", status)
	}
	if status := ClaudeStatus([]string{"--wrong"}, strings.NewReader(input), &out, &errOut, env); status != 2 {
		t.Fatalf("bad arguments: %d", status)
	}
	// Input without a session, or without usage numbers, reports accordingly.
	if _, _, ok := claudeStatus([]byte(`{"model":{"id":"m"}}`), 1); ok {
		t.Fatal("input without a session reported")
	}
	o, _, ok := claudeStatus([]byte(`{"session_id":"s","context_window":{"current_usage":null}}`), 1)
	if !ok || o.Model != nil || o.Context == nil || o.Context.LimitTokens != nil || *o.Context.UsageAvailable {
		t.Fatalf("no usage: %+v", o)
	}
	if _, _, ok := claudeStatus([]byte(`{"session_id":"s","model":{"id":"`+strings.Repeat("m", hookInputMax)+`"}}`), 1); ok {
		t.Fatal("oversized input parsed")
	}
	// Without a reachable daemon the user's command still runs.
	out.Reset()
	none := HookEnv{Getenv: func(k string) string { return map[string]string{"KOINON_STATE_DIR": t.TempDir()}[k] }, Now: time.Now}
	if status := ClaudeStatus([]string{"--command", "printf ok"}, strings.NewReader(input), &out, &errOut, none); status != 0 || out.String() != "ok" {
		t.Fatalf("without daemon: %d %q", status, out.String())
	}
}

func TestAgyStopReportsIdle(t *testing.T) {
	env, reports := reportingDaemon(t)
	var out bytes.Buffer
	input := `{"conversationId":"synthetic-agy","modelName":"Gemini Synthetic Flash","transcriptPath":"/private","executionNum":0}`
	if err := AgyStop(strings.NewReader(input), &out, env); err != nil || out.String() != "{}\n" {
		t.Fatalf("stop: %v %q", err, out.String())
	}
	got := reports()
	if len(got) != 1 || got[0].Caller != (core.Key{Family: "agy", ID: "synthetic-agy"}) || got[0].Activity.State != "idle" ||
		got[0].Activity.Source != "agy_hook" || got[0].Model.ID != "Gemini Synthetic Flash" {
		t.Fatalf("report: %+v", got)
	}
	if err := AgyStop(strings.NewReader(`{}`), &out, env); err != nil || len(reports()) != 1 {
		t.Fatal("input without a conversation reported")
	}
}

func TestSetupClaudeConfiguresTheStatusLine(t *testing.T) {
	cli, _, _ := syntheticCLI(t, "claude")
	config, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
	o := Options{Family: "claude", CLI: cli, Binary: "/opt/koinon", ClaudeConfig: config, StateDir: state}
	report, err := Run(context.Background(), o)
	if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("setup: %+v %v", report, err)
	}
	o.RemoveStatusLine = true
	report, err = Run(context.Background(), o)
	if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove: %+v %v", report, err)
	}
	codex, _, _ := syntheticCLI(t, "codex")
	if _, err := Run(context.Background(), Options{Family: "codex", CLI: codex, Binary: "/opt/koinon", RemoveStatusLine: true}); err == nil {
		t.Fatal("status line removal for codex accepted")
	}
}
