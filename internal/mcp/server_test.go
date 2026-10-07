package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

type harness struct {
	t      *testing.T
	root   string
	d      *core.Daemon
	s      *server
	out    *bytes.Buffer
	env    map[string]string
	parent string
	nextID int
}

func newHarness(t *testing.T, client string) *harness {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	h := &harness{t: t, root: root, d: d, out: &bytes.Buffer{}, env: map[string]string{}, parent: "/opt/agent/bin/codex"}
	directory := t.TempDir()
	h.s = &server{c: Config{StateDir: root, Address: d.Addresses()[0], Directory: directory, ParentPID: 4242,
		Getenv:  func(k string) string { return h.env[k] },
		Command: func(int) (string, []string, error) { return h.parent, []string{h.parent}, nil },
		Now:     time.Now}, sessions: map[core.Key]registered{}}
	h.s.out = json.NewEncoder(h.out)
	if client != "" {
		h.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": client, "version": "1"}, "capabilities": map[string]any{}})
	}
	return h
}

func (h *harness) request(method string, params any) map[string]any {
	h.t.Helper()
	h.nextID++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": h.nextID, "method": method, "params": params})
	h.out.Reset()
	h.s.handle(context.Background(), line)
	var reply map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &reply); err != nil {
		h.t.Fatalf("%s: %q: %v", method, h.out.String(), err)
	}
	return reply
}

// tool calls one tool and returns its decoded JSON text and whether it is an error.
func (h *harness) tool(name string, args map[string]any, meta map[string]any) (map[string]any, bool) {
	h.t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	if meta != nil {
		params["_meta"] = meta
	}
	reply := h.request("tools/call", params)
	r, ok := reply["result"].(map[string]any)
	if !ok {
		h.t.Fatalf("%s: %v", name, reply)
	}
	var value map[string]any
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		h.t.Fatal(err)
	}
	return value, r["isError"] == true
}

func (h *harness) sessions() []core.Session {
	h.t.Helper()
	secret, _ := core.ReadSecret(h.root)
	data, err := core.Call(context.Background(), h.d.Addresses()[0], secret, "/v1/sessions", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	var reply struct {
		Sessions []core.Session `json:"sessions"`
	}
	json.Unmarshal(data, &reply)
	return reply.Sessions
}

func TestProtocol(t *testing.T) {
	h := newHarness(t, "")
	reply := h.request("initialize", map[string]any{"protocolVersion": "2024-11-05", "clientInfo": map[string]any{"name": "synthetic"}})
	if r := reply["result"].(map[string]any); r["protocolVersion"] != "2024-11-05" || r["serverInfo"].(map[string]any)["name"] != "koinon" {
		t.Fatalf("initialize: %v", reply)
	}
	reply = h.request("initialize", map[string]any{"protocolVersion": "1999-01-01", "clientInfo": map[string]any{"name": "synthetic"}})
	if reply["result"].(map[string]any)["protocolVersion"] != protocolVersions[0] {
		t.Fatalf("version fallback: %v", reply)
	}
	tools := h.request("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		tool := tool.(map[string]any)
		names = append(names, tool["name"].(string))
		for field := range tool["inputSchema"].(map[string]any)["properties"].(map[string]any) {
			for _, forbidden := range callerFields {
				if field == forbidden {
					t.Fatalf("caller field in schema: %v", tool)
				}
			}
		}
	}
	if strings.Join(names, ",") != "peers,send,inbox,ack,delivery" {
		t.Fatalf("tools: %v", names)
	}
	if reply := h.request("ping", nil); reply["result"] == nil {
		t.Fatalf("ping: %v", reply)
	}
	if reply := h.request("resources/list", nil); reply["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", reply)
	}
	h.out.Reset()
	h.s.handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	h.s.handle(context.Background(), []byte(`not json`))
	if !strings.Contains(h.out.String(), "-32700") || strings.Count(h.out.String(), "\n") != 1 {
		t.Fatalf("notification or parse error: %q", h.out.String())
	}
}

func TestServeReadsLinesAndBoundsThem(t *testing.T) {
	root := t.TempDir()
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"x","params":"` + strings.Repeat("a", maxLine) + `"}` + "\n" + `{"jsonrpc":"2.0","id":3,"method":"ping"}`
	var out bytes.Buffer
	err := Serve(context.Background(), Config{StateDir: root, Address: "127.0.0.1:1", Getenv: os.Getenv, Now: time.Now}, strings.NewReader(input), &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if err != nil || len(lines) != 3 || !strings.Contains(lines[1], "-32700") || !strings.Contains(lines[2], `"id":3`) {
		t.Fatalf("lines: %v %q", err, out.String())
	}
}

func TestCodexThreadsAreSeparateSessions(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	a, b := map[string]any{"threadId": "synthetic-thread-a"}, map[string]any{"threadId": "synthetic-thread-b"}
	listing, isError := h.tool("peers", map[string]any{}, a)
	if isError {
		t.Fatal(listing)
	}
	h.tool("peers", map[string]any{}, b)
	names := map[string]string{}
	for _, s := range h.sessions() {
		names[s.ID] = s.Name
		var target map[string]any
		json.Unmarshal(s.WakeTarget, &target)
		if s.Family != "codex" || target["cli"] != "/opt/agent/bin/codex" {
			t.Fatalf("codex session: %+v", s)
		}
	}
	if len(names) != 2 {
		t.Fatalf("sessions: %v", names)
	}
	sent, isError := h.tool("send", map[string]any{"to": names["synthetic-thread-b"], "body": "synthetic hello"}, a)
	if isError {
		t.Fatal(sent)
	}
	inbox, _ := h.tool("inbox", map[string]any{}, b)
	messages := inbox["inbox"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["body"] != "synthetic hello" {
		t.Fatalf("inbox: %v", inbox)
	}
	if other, _ := h.tool("inbox", map[string]any{}, a); len(other["inbox"].(map[string]any)["messages"].([]any)) != 0 {
		t.Fatalf("inbox isolation: %v", other)
	}
	if acked, isError := h.tool("ack", map[string]any{"through": 1}, b); isError || acked["acked_through"] != float64(1) {
		t.Fatalf("ack: %v", acked)
	}
	id := sent["message"].(map[string]any)["id"]
	if outcome, isError := h.tool("delivery", map[string]any{"message_id": id}, a); isError || outcome["message"].(map[string]any)["acknowledged"] != true {
		t.Fatalf("delivery: %v", outcome)
	}
	if outcome, isError := h.tool("delivery", map[string]any{"message_id": id}, b); !isError || outcome["code"] != "message_not_found" {
		t.Fatalf("other sender's outcome: %v", outcome)
	}
}

func TestIdentitySources(t *testing.T) {
	codexMeta := map[string]any{"threadId": "synthetic-thread"}
	for _, c := range []struct {
		name, client, parent string
		env                  map[string]string
		meta                 map[string]any
		args                 map[string]any
		family, id           string
	}{
		{"claude trusted", "claude-code", "/opt/claude/versions/2.1.290", map[string]string{"CLAUDE_CODE_SESSION_ID": "synthetic-claude"}, nil, nil, "claude", "synthetic-claude"},
		{"claude npm", "claude-code", "/usr/bin/node", map[string]string{"CLAUDE_CODE_SESSION_ID": "synthetic-claude"}, nil, nil, "", ""},
		{"claude wrong parent", "claude-code", "/usr/bin/bash", map[string]string{"CLAUDE_CODE_SESSION_ID": "synthetic-claude"}, nil, nil, "", ""},
		{"claude env under another client", "synthetic-client", "/opt/claude/versions/2.1.290", map[string]string{"CLAUDE_CODE_SESSION_ID": "synthetic-claude"}, nil, nil, "", ""},
		{"claude no id", "claude-code", "/opt/claude/bin/claude", nil, nil, nil, "", ""},
		{"codex", "codex-mcp-client", "/opt/agent/bin/codex", nil, codexMeta, nil, "codex", "synthetic-thread"},
		{"codex meta from another client", "synthetic-client", "/opt/agent/bin/codex", nil, codexMeta, nil, "", ""},
		{"agy", "antigravity", "/opt/agent/bin/agy", nil, map[string]any{"antigravity.google/conversation_id": "synthetic-conversation"}, nil, "agy", "synthetic-conversation"},
		{"opencode plugin", "opencode", "/opt/agent/bin/opencode", nil, map[string]any{"progressToken": 2}, map[string]any{"koinon_session": "ses_synthetic"}, "opencode", "ses_synthetic"},
		{"opencode without plugin", "opencode", "/opt/agent/bin/opencode", nil, nil, nil, "", ""},
		{"plugin field from codex", "codex-mcp-client", "/opt/agent/bin/codex", nil, codexMeta, map[string]any{"koinon_session": "ses_synthetic"}, "", ""},
		{"caller in arguments", "codex-mcp-client", "/opt/agent/bin/codex", nil, codexMeta, map[string]any{"caller": map[string]any{"family": "codex", "id": "other"}}, "", ""},
		{"id in arguments", "opencode", "/opt/agent/bin/opencode", nil, nil, map[string]any{"koinon_session": "ses_synthetic", "id": "other"}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.parent, h.env = c.parent, c.env
			if c.name == "claude npm" {
				h.s.c.Command = func(int) (string, []string, error) {
					return "/usr/bin/node", []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, nil
				}
				c.family, c.id = "claude", "synthetic-claude"
			}
			h.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": c.client}})
			args := map[string]any{}
			for k, v := range c.args {
				args[k] = v
			}
			reply, isError := h.tool("peers", args, c.meta)
			if c.family == "" {
				if !isError || reply["code"] != "identity_unavailable" || len(h.sessions()) != 0 {
					t.Fatalf("accepted: %v %v", reply, h.sessions())
				}
				return
			}
			sessions := h.sessions()
			if isError || len(sessions) != 1 || sessions[0].Family != c.family || sessions[0].ID != c.id {
				t.Fatalf("identity: %v %+v", reply, sessions)
			}
			if c.family == "claude" && string(sessions[0].WakeTarget) != `{"claude_pid":4242}` {
				t.Fatalf("claude wake target: %s", sessions[0].WakeTarget)
			}
		})
	}
}

func TestOpenCodeSessionsOnOneServer(t *testing.T) {
	h := newHarness(t, "opencode")
	for _, id := range []string{"ses_a", "ses_b"} {
		if reply, isError := h.tool("peers", map[string]any{"koinon_session": id}, nil); isError {
			t.Fatal(reply)
		}
	}
	names := map[string]string{}
	for _, s := range h.sessions() {
		names[s.ID] = s.Name
	}
	if _, isError := h.tool("send", map[string]any{"koinon_session": "ses_a", "to": names["ses_b"], "body": "synthetic"}, nil); isError || len(names) != 2 {
		t.Fatalf("opencode sessions: %v", names)
	}
	inbox, _ := h.tool("inbox", map[string]any{"koinon_session": "ses_b"}, nil)
	if messages := inbox["inbox"].(map[string]any)["messages"].([]any); len(messages) != 1 {
		t.Fatalf("attributed inbox: %v", inbox)
	}
}

func TestArgumentsAndLaunchBinding(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	meta := map[string]any{"threadId": "synthetic-thread"}
	for _, args := range []map[string]any{{"to": "x"}, {"to": "x", "body": "y", "extra": 1}, {"through": "one"}} {
		name := "send"
		if args["through"] != nil {
			name = "ack"
		}
		if reply, isError := h.tool(name, args, meta); !isError || reply["code"] != "invalid_arguments" {
			t.Fatalf("arguments %v: %v", args, reply)
		}
	}
	if reply, isError := h.tool("ack", map[string]any{}, meta); !isError || reply["code"] != "invalid_arguments" {
		t.Fatalf("missing through: %v", reply)
	}
	if reply, isError := h.tool("unknown", map[string]any{}, meta); !isError || reply["code"] != "unknown_tool" {
		t.Fatalf("unknown tool: %v", reply)
	}
	if reply, isError := h.tool("send", map[string]any{"to": "codex-none-00", "body": "x"}, meta); !isError || reply["code"] != "peer_not_found" {
		t.Fatalf("daemon code: %v", reply)
	}
	// A launched agent's server binds the session to the launcher's private target.
	secret, _ := core.ReadSecret(h.root)
	cli := filepath.Join(t.TempDir(), "codex")
	id, err := core.CreateLaunch(context.Background(), h.d.Addresses()[0], secret, core.LaunchTarget{Family: "codex", Directory: h.s.c.Directory, CLI: cli, HostPID: 77})
	if err != nil {
		t.Fatal(err)
	}
	h.env["KOINON_LAUNCH_ID"] = id
	h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-launched"})
	for _, s := range h.sessions() {
		if s.ID == "synthetic-launched" && !strings.Contains(string(s.WakeTarget), id) {
			t.Fatalf("launch not bound: %s", s.WakeTarget)
		}
	}
}

func TestDaemonRestartAndRenewal(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	a, b := map[string]any{"threadId": "synthetic-a"}, map[string]any{"threadId": "synthetic-b"}
	h.tool("peers", map[string]any{}, b)
	recipient := h.sessions()[0].Name
	if _, isError := h.tool("send", map[string]any{"to": recipient, "body": "before restart"}, a); isError {
		t.Fatal("send")
	}
	address := h.d.Addresses()[0]
	if err := h.d.Close(); err != nil {
		t.Fatal(err)
	}
	if reply, isError := h.tool("inbox", map[string]any{}, b); !isError || reply["code"] != "daemon_unavailable" {
		t.Fatalf("daemon down: %v", reply)
	}
	restarted, err := core.Start(core.Config{StateDir: h.root, Listen: []string{address}})
	if err != nil {
		t.Fatal(err)
	}
	h.d = restarted
	t.Cleanup(func() { restarted.Close() })
	inbox, isError := h.tool("inbox", map[string]any{}, b)
	if messages := inbox["inbox"].(map[string]any)["messages"].([]any); isError || len(messages) != 1 {
		t.Fatalf("after restart: %v", inbox)
	}
	// Renewal keeps the latest session; a retired record is registered again by the next call.
	before := h.s.sessions[core.Key{Family: "codex", ID: "synthetic-b"}].revision
	h.s.renew(context.Background())
	after := h.s.sessions[core.Key{Family: "codex", ID: "synthetic-b"}].revision
	if after != before+1 {
		t.Fatalf("renew: %d -> %d", before, after)
	}
	secret, _ := core.ReadSecret(h.root)
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/retire", core.Mutation{Family: "codex", ID: "synthetic-b", IfRevision: after}); err != nil {
		t.Fatal(err)
	}
	if inbox, isError := h.tool("inbox", map[string]any{}, b); isError {
		t.Fatalf("re-registration: %v", inbox)
	}
	h.s.renew(context.Background())
	for _, s := range h.sessions() {
		if s.ID == "synthetic-b" && s.State != "active" {
			t.Fatalf("state: %+v", s)
		}
	}
}

func TestCommandBinary(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	// The real binary speaks MCP on stdio and keeps stdout for protocol messages only.
	binary := filepath.Join(t.TempDir(), "koinon")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/koinon")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(binary, "mcp", "--state-dir", filepath.Join(t.TempDir(), "none"), "--address", "127.0.0.1:1")
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"codex-mcp-client"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peers","arguments":{},"_meta":{"threadId":"synthetic"}}}
`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		t.Fatalf("exit: %v %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "daemon_unavailable") || stderr.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}
