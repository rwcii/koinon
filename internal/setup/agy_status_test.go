package setup

import (
	"bytes"
	"context"
	"encoding/json"
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

// The agy status-line hook reports the conversation's model and context use and passes the
// user's command through; it never invents a reading (#512).
func TestAgyStatusHook(t *testing.T) {
	env, reports := reportingDaemon(t)
	var mu sync.Mutex
	var witnesses []core.AgyWitness
	witnessed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report core.AgyWitness
		if r.URL.Path == "/v1/cycles/agy-witness" && json.NewDecoder(r.Body).Decode(&report) == nil {
			mu.Lock()
			witnesses = append(witnesses, report)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(witnessed.Close)
	input := `{"version":"1.4.6","conversation_id":"synthetic-agy","session_id":"synthetic-agy","model":{"id":"gemini-synthetic"},
		"agent_state":"idle","cwd":"/private","transcript_path":"/private/t","email":"private@example.invalid",
		"context_window":{"total_input_tokens":13744,"total_output_tokens":89,"context_window_size":1048576,"current_usage":{"input_tokens":9990}}}`
	var out, errOut bytes.Buffer
	if status := AgyStatus([]string{"--command", "cat; printf ' two'; exit 4"}, strings.NewReader(input), &out, &errOut, env); status != 4 {
		t.Fatalf("status %d: %s", status, errOut.String())
	}
	if out.String() != input+" two" {
		t.Fatalf("output: %q", out.String())
	}
	got := reports()
	if len(got) != 1 || got[0].Caller != (core.Key{Family: "agy", ID: "synthetic-agy"}) || got[0].Model.ID != "gemini-synthetic" ||
		got[0].Model.Source != "agy_statusline" || got[0].Context.Source != "agy_statusline" || got[0].Context.Epoch != "synthetic-agy" ||
		*got[0].Context.LimitTokens != 1048576 || *got[0].Context.UsedTokens != 13744 || !*got[0].Context.UsageAvailable ||
		got[0].Activity == nil || got[0].Activity.State != "idle" || got[0].Activity.Source != "agy_statusline" {
		t.Fatalf("report: %+v", got)
	}
	if data, _ := json.Marshal(got); strings.Contains(string(data), "private") {
		t.Fatal("status input content reported")
	}
	// The hook also gives the conversation with itself as the reporter (#512).
	address := strings.TrimPrefix(witnessed.URL, "http://")
	state := env.Getenv("KOINON_STATE_DIR")
	witnessEnv := HookEnv{Getenv: func(k string) string {
		return map[string]string{"KOINON_STATE_DIR": state, "KOINON_DAEMON_ADDRESS": address}[k]
	}, Now: time.Now}
	if status := AgyStatus(nil, strings.NewReader(input), &out, &errOut, witnessEnv); status != 0 {
		t.Fatalf("witness run: %d", status)
	}
	mu.Lock()
	if len(witnesses) != 1 || witnesses[0].Conversation != "synthetic-agy" || witnesses[0].Reporter.PID != os.Getpid() || witnesses[0].Reporter.Start <= 0 {
		t.Fatalf("witness: %+v", witnesses)
	}
	mu.Unlock()
	// agent_state: idle, or busy while a turn or a tool runs; any other state is unknown.
	for state, want := range map[string]string{"idle": "idle", "working": "busy", "tool_use": "busy", "authenticating": "", "": "", "other": ""} {
		o, _, _ := agyStatus([]byte(`{"conversation_id":"c","agent_state":"`+state+`"}`), 1)
		if want == "" && o.Activity != nil || want != "" && (o.Activity == nil || o.Activity.State != want) {
			t.Fatalf("agent_state %q: %+v", state, o.Activity)
		}
	}
	// Without a command it prints nothing: agy's stacked default line stands alone.
	out.Reset()
	if status := AgyStatus(nil, strings.NewReader(input), &out, &errOut, env); status != 0 || out.Len() != 0 {
		t.Fatalf("default line: %d %q", status, out.String())
	}
	if status := AgyStatus([]string{"--wrong"}, strings.NewReader(input), &out, &errOut, env); status != 2 {
		t.Fatalf("bad arguments: %d", status)
	}
	// Before the first turn both IDs are empty: no report.
	before := len(reports())
	if status := AgyStatus(nil, strings.NewReader(`{"conversation_id":"","session_id":"","context_window":{"total_input_tokens":0,"context_window_size":1048576}}`),
		&out, &errOut, env); status != 0 || len(reports()) != before {
		t.Fatal("input without a conversation reported")
	}
	if _, _, ok := agyStatus([]byte(`{"session_id":"s","model":{"id":"m"}}`), 1); ok {
		t.Fatal("session_id taken as the caller")
	}
	// Counts that are absent, null or invalid, and a limit that is not positive, are unknown.
	for _, window := range []string{
		`{}`,
		`{"total_input_tokens":null,"context_window_size":1048576}`,
		`{"total_input_tokens":5,"context_window_size":null}`,
		`{"total_input_tokens":5}`,
		`{"total_input_tokens":-1,"context_window_size":1048576}`,
		`{"total_input_tokens":1.5,"context_window_size":1048576}`,
		`{"total_input_tokens":"5","context_window_size":1048576}`,
		`{"total_input_tokens":9007199254740992,"context_window_size":1048576}`,
		`{"total_input_tokens":0,"context_window_size":0}`,
		`{"total_input_tokens":5,"context_window_size":-1}`,
	} {
		o, _, ok := agyStatus([]byte(`{"conversation_id":"c","model":{"id":"m"},"context_window":`+window+`}`), 1)
		if !ok || o.Model == nil || o.Context == nil || o.Context.LimitTokens != nil || o.Context.UsedTokens != nil || *o.Context.UsageAvailable {
			t.Fatalf("%s: %+v", window, o.Context)
		}
	}
	// A new conversation after /clear reports zero used of a positive limit.
	o, _, ok := agyStatus([]byte(`{"conversation_id":"after-clear","context_window":{"total_input_tokens":0,"context_window_size":1048576}}`), 1)
	if !ok || o.Model != nil || *o.Context.UsedTokens != 0 || *o.Context.LimitTokens != 1048576 || o.Context.Epoch != "after-clear" {
		t.Fatalf("zero used: %+v", o.Context)
	}
	// A model that is not an object with an ID is unknown; the context still reports.
	o, _, ok = agyStatus([]byte(`{"conversation_id":"c","model":"gemini","context_window":{"total_input_tokens":1,"context_window_size":2}}`), 1)
	if !ok || o.Model != nil || *o.Context.UsedTokens != 1 {
		t.Fatalf("model string: %+v", o)
	}
	if _, _, ok := agyStatus([]byte(`{"conversation_id":"c","model":{"id":"`+strings.Repeat("m", hookInputMax)+`"}}`), 1); ok {
		t.Fatal("oversized input parsed")
	}
}

func readJSON(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Claude and agy each wrap their own status line with their own record, and each removal
// restores only its own (#512).
func TestAgyStatusLineBesideClaude(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	binary := "/opt/k o/koinon"
	claudeCLI, _, _ := syntheticCLI(t, "claude")
	agyCLI, _, _ := syntheticCLI(t, "agy")
	claude := Options{Family: "claude", CLI: claudeCLI, Binary: binary, StateDir: state, ClaudeConfig: filepath.Join(dir, "claude")}
	agy := Options{Family: "agy", CLI: agyCLI, Binary: binary, StateDir: state, AgyRoot: filepath.Join(dir, "gemini", "config")}
	agySettings := filepath.Join(dir, "gemini", "antigravity-cli", "settings.json")
	for _, d := range []string{claude.ClaudeConfig, agy.AgyRoot, filepath.Dir(agySettings)} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	agyBefore := `{"permissions":{"allow":["command(koinon)"]},"trustedWorkspaces":["/w"]}`
	if err := os.WriteFile(agySettings, []byte(agyBefore), 0600); err != nil {
		t.Fatal(err)
	}
	claudeOriginal := `{"type":"command","command":"line.sh","padding":1}`
	if err := os.WriteFile(filepath.Join(claude.ClaudeConfig, "settings.json"), []byte(`{"statusLine":`+claudeOriginal+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		want := map[bool]string{false: "set_up", true: "unchanged"}[round > 0]
		for _, o := range []Options{agy, claude} {
			report, err := Run(context.Background(), o)
			if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != want {
				t.Fatalf("round %d %s: %+v %v", round, o.Family, report.StatusLine, err)
			}
		}
	}
	settings := readJSON(t, agySettings)
	wantEntry := `{"type":"command","command":"'/opt/k o/koinon' hook agy-status","stack_with_default":true}`
	if !equal(settings["statusLine"], json.RawMessage(wantEntry)) || !equal(settings["trustedWorkspaces"], json.RawMessage(`["/w"]`)) {
		t.Fatalf("agy settings: %s", settings)
	}
	claudeEntry := readJSON(t, filepath.Join(claude.ClaudeConfig, "settings.json"))["statusLine"]
	if !strings.Contains(string(claudeEntry), "hook claude-status --command") {
		t.Fatalf("claude entry: %s", claudeEntry)
	}
	for _, name := range []string{"agy-statusline.json", "claude-statusline.json"} {
		if _, err := os.Stat(filepath.Join(state, name)); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	// Removing agy's leaves Claude's in place, and the reverse.
	agy.RemoveStatusLine = true
	report, err := Run(context.Background(), agy)
	if err != nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("agy removal: %+v %v", report.StatusLine, err)
	}
	if _, found := readJSON(t, agySettings)["statusLine"]; found {
		t.Fatal("agy statusLine left after removal")
	}
	if got := readJSON(t, filepath.Join(claude.ClaudeConfig, "settings.json"))["statusLine"]; !equal(got, claudeEntry) {
		t.Fatalf("claude entry changed by agy removal: %s", got)
	}
	claude.RemoveStatusLine = true
	if report, err := Run(context.Background(), claude); err != nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("claude removal: %+v %v", report.StatusLine, err)
	}
	if got := readJSON(t, filepath.Join(claude.ClaudeConfig, "settings.json"))["statusLine"]; !equal(got, json.RawMessage(claudeOriginal)) {
		t.Fatalf("claude original: %s", got)
	}
}

// A user's agy entry is wrapped with its other members kept, and Remove restores it.
func TestAgyStatusLineWrapsTheUserEntry(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "antigravity-cli", "settings.json")
	for _, d := range []string{filepath.Dir(settings), filepath.Join(dir, "config")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	original := `{"type":"command","command":"sh '/u/line.sh'","stack_with_default":false,"padding":0}`
	if err := os.WriteFile(settings, []byte(`{"statusLine":`+original+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	cli, _, _ := syntheticCLI(t, "agy")
	o := Options{Family: "agy", CLI: cli, Binary: "/opt/koinon", StateDir: filepath.Join(dir, "state"), AgyRoot: filepath.Join(dir, "config"), AgySettings: settings}
	if report, err := Run(context.Background(), o); err != nil || report.StatusLine.Outcome != "set_up" {
		t.Fatalf("setup: %+v %v", report.StatusLine, err)
	}
	want := `{"stack_with_default":false,"padding":0,"type":"command","command":"'/opt/koinon' hook agy-status --command 'sh '\"'\"'/u/line.sh'\"'\"''"}`
	if got := readJSON(t, settings)["statusLine"]; !equal(got, json.RawMessage(want)) {
		t.Fatalf("wrapper: %s", got)
	}
	report, err := Remove(context.Background(), o)
	if err != nil || report.StatusLine == nil || report.StatusLine.Outcome != "restored" {
		t.Fatalf("remove: %+v %v", report.StatusLine, err)
	}
	if got := readJSON(t, settings)["statusLine"]; !equal(got, json.RawMessage(original)) {
		t.Fatalf("restored: %s", got)
	}
	// Without the Antigravity CLI directory, set-up is skipped.
	o.AgySettings = filepath.Join(dir, "absent", "settings.json")
	if report, err := Run(context.Background(), o); err != nil || report.StatusLine.Outcome != "skipped" || report.StatusLine.Reason != "agy_config_missing" {
		t.Fatalf("missing: %+v %v", report.StatusLine, err)
	}
}

// The settings file is beside the agy root also when the root ends in a slash or a dot, for
// setup and for the permission rules; AgySettings comes first.
func TestAgySettingsFileBesideTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "gemini", "config")
	settings := filepath.Join(dir, "gemini", "antigravity-cli", "settings.json")
	for _, d := range []string{root, filepath.Dir(settings)} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["mcp(koinon/*)"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cli, _, _ := syntheticCLI(t, "agy")
	for i, spelling := range []string{root, root + "/", root + "/.", root + "//"} {
		o := Options{Family: "agy", AgyRoot: spelling}
		if got := agySettingsFile(o); got != settings {
			t.Fatalf("%q: %s", spelling, got)
		}
		if result, err := Permissions(o, false); err != nil || result.SettingsFile != settings {
			t.Fatalf("permissions %q: %+v %v", spelling, result, err)
		}
		o.CLI, o.Binary, o.StateDir = cli, "/opt/koinon", filepath.Join(dir, "state")
		want := map[bool]string{true: "set_up", false: "unchanged"}[i == 0]
		if report, err := Run(context.Background(), o); err != nil || report.StatusLine.Outcome != want || report.StatusLine.SettingsFile != settings {
			t.Fatalf("setup %q: %+v %v", spelling, report.StatusLine, err)
		}
	}
	if got := agySettingsFile(Options{AgyRoot: root + "/", AgySettings: "/elsewhere/settings.json"}); got != "/elsewhere/settings.json" {
		t.Fatalf("AgySettings precedence: %s", got)
	}
}
