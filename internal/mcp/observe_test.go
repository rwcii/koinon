package mcp

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

// observeHarness is an MCP server whose daemon is a synthetic one that records reports.
type observeHarness struct {
	*harness
	mu      sync.Mutex
	reports []core.Observation
}

func newObserveHarness(t *testing.T, client, parent string) *observeHarness {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	d.Close() // Only its secret is needed.
	o := &observeHarness{}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions/observe":
			var report core.Observation
			json.NewDecoder(r.Body).Decode(&report)
			o.mu.Lock()
			o.reports = append(o.reports, report)
			o.mu.Unlock()
			fmt.Fprint(w, `{"ok":true}`)
		case "/v1/sessions/register", "/v1/sessions/renew":
			fmt.Fprint(w, `{"ok":true,"session":{"revision":1}}`)
		default:
			fmt.Fprint(w, `{"ok":true,"peers":[],"truncated":false}`)
		}
	}))
	t.Cleanup(daemon.Close)
	// A launched session: this fake daemon accepts its registration.
	h := &harness{t: t, root: root, out: &bytes.Buffer{}, env: map[string]string{"KOINON_LAUNCH_ID": strings.Repeat("a", 64)}, parent: parent}
	h.s = &server{c: Config{StateDir: root, Address: strings.TrimPrefix(daemon.URL, "http://"), Directory: t.TempDir(), ParentPID: 4242,
		Getenv:      func(k string) string { return h.env[k] },
		Command:     func(int) (string, []string, error) { return h.parent, []string{h.parent}, nil },
		Now:         time.Now,
		TmuxSession: func(context.Context, string, string) (string, error) { return "synthetic-work", nil }}, sessions: map[core.Key]registered{}}
	h.s.out = json.NewEncoder(h.out)
	h.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": client, "version": "1"}, "capabilities": map[string]any{}})
	o.harness = h
	return o
}

// taken returns the reports so far and clears them, waiting briefly for asynchronous ones.
func (o *observeHarness) taken(want int) []core.Observation {
	o.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		o.mu.Lock()
		n := len(o.reports)
		o.mu.Unlock()
		if n >= want || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	result := o.reports
	o.reports = nil
	return result
}

func writeLines(t *testing.T, path string, lines ...any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		text, ok := line.(string)
		if !ok {
			data, _ := json.Marshal(line)
			text = string(data) + "\n"
		}
		f.WriteString(text)
	}
}

func TestCodexModelFromCallMetadata(t *testing.T) {
	o := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread", "x-codex-turn-metadata": map[string]any{"model": "gpt-synthetic", "turn_id": "t"}})
	reports := o.taken(1)
	if len(reports) != 1 || reports[0].Caller != (core.Key{Family: "codex", ID: "synthetic-thread"}) || reports[0].Model == nil ||
		reports[0].Model.ID != "gpt-synthetic" || reports[0].Model.Source != "codex_mcp_meta" || reports[0].Terminal != nil {
		t.Fatalf("object metadata: %+v", reports)
	}
	// A JSON string form is read too; the same model again is not resent.
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread", "x-codex-turn-metadata": `{"model":"gpt-other"}`})
	if reports := o.taken(1); len(reports) != 1 || reports[0].Model.ID != "gpt-other" {
		t.Fatalf("string metadata: %+v", reports)
	}
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread"})
	if reports := o.taken(1); len(reports) != 0 {
		t.Fatalf("call without metadata reported: %+v", reports)
	}
}

func TestCodexRolloutAndTerminalAttribution(t *testing.T) {
	o := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	home := t.TempDir()
	o.env["CODEX_HOME"] = home
	dir := filepath.Join(home, "sessions", "2026", "10", "07")
	os.MkdirAll(dir, 0700)
	rollout := filepath.Join(dir, "rollout-2026-10-07T00-00-00-synthetic-thread-1.jsonl")
	writeLines(t, rollout,
		map[string]any{"timestamp": "2026-10-07T00:00:00.000Z", "type": "session_meta", "payload": map[string]any{"id": "synthetic-thread-1"}},
		map[string]any{"timestamp": "2026-10-07T00:00:01.000Z", "type": "turn_context", "payload": map[string]any{"model": "gpt-rollout", "cwd": "/private"}},
		map[string]any{"timestamp": "2026-10-07T00:00:02.000Z", "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-a"}},
		map[string]any{"timestamp": "2026-10-07T00:00:03.000Z", "type": "response_item", "payload": map[string]any{"type": "message", "content": "private conversation"}},
		map[string]any{"timestamp": "2026-10-07T00:00:04.000Z", "type": "event_msg", "payload": map[string]any{"type": "token_count",
			"info": map[string]any{"model_context_window": 200000, "last_token_usage": map[string]any{"input_tokens": 50000}}}},
		`{"timestamp":"2026-10-07T00:00:05.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a"}}`) // no newline yet
	// Two threads served by one server outside tmux: neither reports a terminal.
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-2"})
	o.s.observeOnce(context.Background())
	reports := o.taken(1)
	var first *core.Observation
	for i := range reports {
		if reports[i].Terminal != nil {
			t.Fatalf("terminal reported outside tmux: %+v", reports[i])
		}
		if reports[i].Caller.ID == "synthetic-thread-1" {
			first = &reports[i]
		}
		if reports[i].Caller.ID == "synthetic-thread-2" {
			t.Fatalf("thread without a rollout reported: %+v", reports[i])
		}
	}
	if first == nil || first.Model == nil || first.Model.ID != "gpt-rollout" || first.Context == nil || *first.Context.UsedTokens != 50000 ||
		*first.Context.LimitTokens != 200000 || first.Activity == nil || first.Activity.State != "busy" {
		t.Fatalf("rollout report: %+v", reports)
	}
	data, _ := json.Marshal(reports)
	if strings.Contains(string(data), "private") {
		t.Fatal("rollout content reported")
	}
	// The completed last record is read once its line ends; unchanged groups are not resent.
	writeLines(t, rollout, "\n")
	o.s.observeOnce(context.Background())
	reports = o.taken(1)
	if len(reports) != 1 || reports[0].Activity == nil || reports[0].Activity.State != "idle" || reports[0].Model != nil || reports[0].Context != nil {
		t.Fatalf("completed turn: %+v", reports)
	}
	// A replaced rollout that names another session is not read.
	os.Remove(rollout)
	writeLines(t, rollout, map[string]any{"timestamp": "2026-10-07T00:00:00.000Z", "type": "session_meta", "payload": map[string]any{"id": "someone-else"}},
		map[string]any{"timestamp": "2026-10-07T00:01:00.000Z", "type": "turn_context", "payload": map[string]any{"model": "gpt-wrong"}})
	o.s.observeOnce(context.Background())
	for _, r := range o.taken(0) {
		if r.Model != nil && r.Model.ID == "gpt-wrong" {
			t.Fatal("foreign rollout read")
		}
	}
	// A launched server's sessions each get the launch's terminal.
	launched := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	launched.env["KOINON_LAUNCH_ID"] = strings.Repeat("a", 64)
	launched.env["TMUX"] = "/tmp/tmux-synthetic/default,1,0"
	launched.env["TMUX_PANE"] = "%9"
	launched.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	launched.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-2"})
	launched.s.observeOnce(context.Background())
	reports = launched.taken(2)
	if len(reports) != 2 {
		t.Fatalf("launched reports: %+v", reports)
	}
	for _, r := range reports {
		if r.Terminal == nil || r.Terminal.Pane != "%9" || r.Terminal.Socket != "/tmp/tmux-synthetic/default" || r.Terminal.Session != "synthetic-work" {
			t.Fatalf("launched terminal: %+v", r)
		}
	}
	launched.s.observeOnce(context.Background())
	if reports := launched.taken(0); len(reports) != 0 {
		t.Fatalf("unchanged terminal resent: %+v", reports)
	}
}

func TestClaudeRegistryActivity(t *testing.T) {
	o := newObserveHarness(t, "claude-code", "/opt/agent/bin/claude")
	config := t.TempDir()
	o.env["CLAUDE_CONFIG_DIR"] = config
	o.env["CLAUDE_CODE_SESSION_ID"] = "synthetic-claude"
	o.env["TMUX"] = "/tmp/tmux-synthetic/default,1,0"
	o.env["TMUX_PANE"] = "%4"
	os.MkdirAll(filepath.Join(config, "sessions"), 0700)
	record := filepath.Join(config, "sessions", "4242.json")
	stamp := time.Now().Add(-time.Second).UnixMilli()
	write := func(value map[string]any) {
		data, _ := json.Marshal(value)
		os.WriteFile(record, data, 0600)
	}
	write(map[string]any{"pid": 4242, "sessionId": "synthetic-claude", "status": "busy", "statusUpdatedAt": stamp, "entrypoint": "cli", "cwd": "/private"})
	o.tool("peers", map[string]any{}, nil)
	o.s.observeOnce(context.Background())
	reports := o.taken(1)
	if len(reports) != 1 || reports[0].Activity == nil || reports[0].Activity.State != "busy" || reports[0].Activity.At != stamp ||
		reports[0].Activity.Source != "claude_registry" || reports[0].Terminal == nil || reports[0].Terminal.Pane != "%4" {
		t.Fatalf("claude report: %+v", reports)
	}
	// Records that are not this interactive session's, or have no valid time, report nothing.
	for _, bad := range []map[string]any{
		{"pid": 4242, "sessionId": "another-session", "status": "idle", "statusUpdatedAt": stamp + 1, "entrypoint": "cli"},
		{"pid": 4242, "sessionId": "synthetic-claude", "status": "idle", "statusUpdatedAt": stamp + 1, "entrypoint": "sdk-ts"},
		{"pid": 4243, "sessionId": "synthetic-claude", "status": "idle", "statusUpdatedAt": stamp + 1, "entrypoint": "cli"},
		{"pid": 4242, "sessionId": "synthetic-claude", "status": "idle", "statusUpdatedAt": time.Now().Add(time.Hour).UnixMilli(), "entrypoint": "cli"},
		{"pid": 4242, "sessionId": "synthetic-claude", "status": "dreaming", "statusUpdatedAt": stamp + 1, "entrypoint": "cli"},
	} {
		write(bad)
		o.s.observeOnce(context.Background())
		for _, r := range o.taken(0) {
			if r.Activity != nil {
				t.Fatalf("record %v reported %+v", bad, r.Activity)
			}
		}
	}
	write(map[string]any{"pid": 4242, "sessionId": "synthetic-claude", "status": "waiting", "statusUpdatedAt": stamp + 2, "entrypoint": "cli"})
	o.s.observeOnce(context.Background())
	if reports := o.taken(1); len(reports) != 1 || reports[0].Activity.State != "waiting" {
		t.Fatalf("waiting: %+v", reports)
	}
	// shell is the prompt with a background shell task running: idle, not busy.
	write(map[string]any{"pid": 4242, "sessionId": "synthetic-claude", "status": "shell", "statusUpdatedAt": stamp + 3, "entrypoint": "cli"})
	o.s.observeOnce(context.Background())
	if reports := o.taken(1); len(reports) != 1 || reports[0].Activity.State != "idle" {
		t.Fatalf("shell: %+v", reports)
	}
	// A symlinked record is not followed.
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	os.Rename(record, target)
	os.Symlink(target, record)
	write(map[string]any{"pid": 4242, "sessionId": "synthetic-claude", "status": "idle", "statusUpdatedAt": stamp + 4, "entrypoint": "cli"})
	o.s.observeOnce(context.Background())
	for _, r := range o.taken(0) {
		if r.Activity != nil {
			t.Fatalf("symlinked record read: %+v", r.Activity)
		}
	}
}

func TestAgyCallIsBusy(t *testing.T) {
	o := newObserveHarness(t, "antigravity", "/opt/agent/bin/agy")
	o.tool("peers", map[string]any{}, map[string]any{"antigravity.google/conversation_id": "synthetic-agy"})
	if reports := o.taken(1); len(reports) != 1 || reports[0].Activity == nil || reports[0].Activity.State != "busy" || reports[0].Activity.Source != "mcp_call" {
		t.Fatalf("agy call: %+v", reports)
	}
}

func TestRolloutLongRecordsAndBatchBound(t *testing.T) {
	o := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	home := t.TempDir()
	o.env["CODEX_HOME"] = home
	dir := filepath.Join(home, "sessions", "2026", "10", "07")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "rollout-synthetic-thread-1.jsonl")
	event := func(at, kind, turn string) map[string]any {
		return map[string]any{"timestamp": "2026-10-07T00:00:" + at + ".000Z", "type": "event_msg", "payload": map[string]any{"type": kind, "turn_id": turn}}
	}
	long := func(n int) string {
		return `{"timestamp":"2026-10-07T00:00:02.500Z","type":"response_item","payload":{"text":"` + strings.Repeat("x", n) + `"}}` + "\n"
	}
	// Normal records, a 70 KiB record, a record over the 1 MiB line limit, then the turn's end.
	writeLines(t, path,
		map[string]any{"timestamp": "2026-10-07T00:00:00.000Z", "type": "session_meta", "payload": map[string]any{"id": "synthetic-thread-1"}},
		event("01", "task_started", "turn-a"), long(70<<10), long(rolloutLine+10), event("03", "task_complete", "turn-a"))
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	size := func() int64 { info, _ := os.Stat(path); return info.Size() }
	reader := func() *rollout { return o.s.obs.rollouts["synthetic-thread-1"] }
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset != size() || r.activity == nil || r.activity.State != "idle" || r.skipping {
		t.Fatalf("after long records: offset %d of %d, %+v", r.offset, size(), r.activity)
	}
	// A later turn is read from the exact offset.
	writeLines(t, path, event("04", "task_started", "turn-b"), long(10))
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset != size() || r.activity.State != "busy" {
		t.Fatalf("next turn: offset %d of %d, %+v", r.offset, size(), r.activity)
	}
	// A record longer than one batch is skipped over several calls, each bounded, without
	// passing the end of the file; an unfinished long record keeps being skipped.
	writeLines(t, path, long(rolloutBatch+rolloutBatch/2))
	before := reader().offset
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset-before > rolloutBatch+rolloutLine+1 || r.offset > size() || !r.skipping {
		t.Fatalf("first batch: moved %d, at %d of %d", r.offset-before, r.offset, size())
	}
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset != size() || r.skipping {
		t.Fatalf("second batch: at %d of %d", r.offset, size())
	}
	writeLines(t, path, `{"timestamp":"2026-10-07T00:00:05.000Z","type":"response_item","payload":{"text":"`+strings.Repeat("y", rolloutLine+10))
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset != size() || !r.skipping {
		t.Fatalf("unfinished long record: at %d of %d", r.offset, size())
	}
	writeLines(t, path, `"}}`+"\n", event("06", "task_complete", "turn-b"))
	o.s.observeOnce(context.Background())
	if r := reader(); r.offset != size() || r.skipping || r.activity.State != "idle" {
		t.Fatalf("after the long record ends: at %d of %d, %+v", r.offset, size(), r.activity)
	}
}

func TestUnchangedValuesAreConfirmedAndUnreadableSourcesAreNot(t *testing.T) {
	o := newObserveHarness(t, "claude-code", "/opt/agent/bin/claude")
	clock := time.Now()
	o.s.c.Now = func() time.Time { return clock }
	config := t.TempDir()
	o.env["CLAUDE_CONFIG_DIR"] = config
	o.env["CLAUDE_CODE_SESSION_ID"] = "synthetic-claude"
	os.MkdirAll(filepath.Join(config, "sessions"), 0700)
	record := filepath.Join(config, "sessions", "4242.json")
	data, _ := json.Marshal(map[string]any{"pid": 4242, "sessionId": "synthetic-claude", "status": "idle",
		"statusUpdatedAt": clock.Add(-time.Second).UnixMilli(), "entrypoint": "cli"})
	os.WriteFile(record, data, 0600)
	o.tool("peers", map[string]any{}, nil)
	o.s.observeOnce(context.Background())
	if reports := o.taken(1); len(reports) != 1 || reports[0].Activity == nil {
		t.Fatalf("first: %+v", reports)
	}
	clock = clock.Add(confirmEvery - time.Second)
	o.s.observeOnce(context.Background())
	if reports := o.taken(0); len(reports) != 0 {
		t.Fatalf("resent within a minute: %+v", reports)
	}
	clock = clock.Add(time.Second)
	o.s.observeOnce(context.Background())
	if reports := o.taken(1); len(reports) != 1 || reports[0].Activity == nil || reports[0].Activity.State != "idle" {
		t.Fatalf("confirmation: %+v", reports)
	}
	// An unreadable source is not confirmed, so the daemon lets its value go stale.
	os.Remove(record)
	clock = clock.Add(2 * confirmEvery)
	o.s.observeOnce(context.Background())
	for _, r := range o.taken(0) {
		if r.Activity != nil {
			t.Fatalf("unreadable source confirmed: %+v", r)
		}
	}
}
