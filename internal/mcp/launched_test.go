package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/launcher"
)

// caller is one family's native identity on a synthetic server.
type caller struct {
	family, client, parent string
	env                    map[string]string
	meta, args             map[string]any
}

var callers = []caller{
	{"claude", "claude-code", "/opt/claude/versions/2.1.290", map[string]string{"CLAUDE_CODE_SESSION_ID": "0123abcd-1111-4222-8333-444455556666"}, nil, nil},
	{"codex", "codex-mcp-client", "/opt/agent/bin/codex", nil, map[string]any{"threadId": "synthetic-thread"}, nil},
	{"agy", "antigravity", "/opt/agent/bin/agy", nil, map[string]any{"antigravity.google/conversation_id": "synthetic-conversation"}, nil},
	{"opencode", "opencode", "/opt/agent/bin/opencode", nil, nil, map[string]any{"koinon_session": "ses_synthetic"}},
}

func (c caller) harness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, "")
	h.parent = c.parent
	for k, v := range c.env {
		h.env[k] = v
	}
	h.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": c.client}})
	return h
}

func (c caller) peers(h *harness) (map[string]any, bool) {
	args := map[string]any{}
	for k, v := range c.args {
		args[k] = v
	}
	return h.tool("peers", args, c.meta)
}

// A direct start is islanded: without its own launch, every tool call returns not_launched
// with the family's launcher command, and nothing is registered or listed.
func TestDirectStartIsIslanded(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			for _, launch := range []string{"", strings.Repeat("c", 64)} {
				h.env["KOINON_LAUNCH_ID"] = launch
				reply, isError := c.peers(h)
				if !isError || reply["code"] != "not_launched" || reply["launcher"] != "koinon "+c.family || len(h.sessions()) != 0 {
					t.Fatalf("launch %q: %v %+v", launch, reply, h.sessions())
				}
			}
			// The tools stay listed, so the agent can read the refusal.
			if tools := h.request("tools/list", map[string]any{}); len(tools["result"].(map[string]any)["tools"].([]any)) == 0 {
				t.Fatal("no tools listed")
			}
			h.launch(c.family)
			if reply, isError := c.peers(h); isError || len(h.sessions()) != 1 {
				t.Fatalf("launched: %v %+v", reply, h.sessions())
			}
		})
	}
}

// A background job's first call can come before the launcher records its job ID; the call
// returns launch_pending, and the next call after the record registers it.
func TestBackgroundJobRegistersAfterItsRecord(t *testing.T) {
	c := callers[0]
	h := c.harness(t)
	secret, err := core.ReadSecret(h.root)
	if err != nil {
		t.Fatal(err)
	}
	address := h.d.Addresses()[0]
	id, err := core.CreateLaunch(context.Background(), address, secret, core.LaunchTarget{Family: "claude", Directory: h.s.c.Directory,
		CLI: "/synthetic/claude", HostPID: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	h.env["KOINON_LAUNCH_ID"] = id
	if reply, isError := c.peers(h); !isError || reply["code"] != "launch_pending" || len(h.sessions()) != 0 {
		t.Fatalf("before the record: %v", reply)
	}
	if _, err := core.Call(context.Background(), address, secret, "/v1/launches/job", map[string]string{"launch_id": id, "job_id": "0123abcd"}); err != nil {
		t.Fatal(err)
	}
	if reply, isError := c.peers(h); isError || len(h.sessions()) != 1 {
		t.Fatalf("after the record: %v", reply)
	}
}

// A Codex sub-agent thread (x-codex-turn-metadata.thread_source "subagent", an object or a
// JSON string) registers with its own peer name and never takes the repository's alias.
func TestCodexSubagentThreads(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	h.launch("codex")
	for id, turn := range map[string]any{
		"synthetic-sub-object": map[string]any{"thread_id": "synthetic-sub-object", "thread_source": "subagent"},
		"synthetic-sub-string": `{"thread_id":"synthetic-sub-string","thread_source":"subagent"}`,
		"synthetic-sub-future": map[string]any{"thread_source": "a-future-source"},
	} {
		if reply, isError := h.tool("peers", map[string]any{}, map[string]any{"threadId": id, "x-codex-turn-metadata": turn}); isError {
			t.Fatal(reply)
		}
	}
	h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-user", "x-codex-turn-metadata": map[string]any{"thread_source": "user"}})
	for _, s := range h.sessions() {
		sub := strings.HasPrefix(s.ID, "synthetic-sub-")
		if s.Subagent != sub || sub && s.Alias != "" || !sub && s.Alias == "" {
			t.Fatalf("session %+v", s)
		}
	}
	// The user thread's metadata now says sub-agent: it registers again at once, before the
	// cached registration is due, and gives up the alias.
	h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-user", "x-codex-turn-metadata": map[string]any{"thread_source": "subagent"}})
	for _, s := range h.sessions() {
		if s.ID == "synthetic-user" && (!s.Subagent || s.Alias != "") {
			t.Fatalf("late sub-agent metadata: %+v", s)
		}
	}
}

// A thread first marked as a sub-agent while the daemon cannot register it keeps no cached
// registration: its next call registers it as a sub-agent before the tool runs.
func TestSubagentRegistersAgainAfterAFailure(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	h.launch("codex")
	if reply, isError := h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread"}); isError {
		t.Fatal(reply)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"ok":false,"code":"storage_error"}`))
	}))
	defer failing.Close()
	address := h.s.c.Address
	h.s.c.Address = strings.TrimPrefix(failing.URL, "http://")
	meta := map[string]any{"threadId": "synthetic-thread", "x-codex-turn-metadata": map[string]any{"thread_source": "subagent"}}
	if reply, isError := h.tool("peers", map[string]any{}, meta); !isError {
		t.Fatalf("registration did not fail: %v", reply)
	}
	h.s.c.Address = address
	if reply, isError := h.tool("peers", map[string]any{}, meta); isError {
		t.Fatal(reply)
	}
	if s := h.sessions()[0]; !s.Subagent || s.Alias != "" {
		t.Fatalf("after the failure: %+v", s)
	}
}

// Codex passes an MCP server only the variables that its env_vars lists (#247), so every
// variable that koinon mcp reads is in the launcher's list, except the ones named here.
func TestLauncherPassesEveryMCPVariable(t *testing.T) {
	exempt := map[string]string{
		"CLAUDE_CODE_SESSION_ID": "Claude identity, set by Claude Code",
		"CLAUDE_CONFIG_DIR":      "Claude only; Claude passes its environment",
		"HOME":                   "Codex passes it to every MCP server",
	}
	read := regexp.MustCompile(`(?:Getenv|s\.home)\("([A-Z_]+)"`)
	files, _ := filepath.Glob("*.go")
	files = append(files, filepath.Join("..", "..", "cmd", "koinon", "main.go"))
	found := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range read.FindAllStringSubmatch(string(data), -1) {
			name := m[1]
			found++
			if file != files[len(files)-1] || strings.HasPrefix(name, "KOINON_") {
				if exempt[name] == "" && !slices.Contains(launcher.MCPVariables, name) {
					t.Errorf("%s reads %s, which the Codex launch does not pass", file, name)
				}
			}
		}
	}
	if found < 8 {
		t.Fatalf("found only %d environment reads", found)
	}
}

// A former holder that MCP registers again after a change of holder is refused, with
// stale_holder, when it reads or acknowledges the participant's inbox; the holder reads
// it (participants chunk 03).
func TestMCPParticipantInboxFencing(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	h.launch("codex")
	thread := func(id string) map[string]any { return map[string]any{"threadId": id} }
	h.tool("peers", map[string]any{}, thread("synthetic-a"))
	h.tool("peers", map[string]any{}, thread("synthetic-b"))
	secret, _ := core.ReadSecret(h.root)
	address := h.d.Addresses()[0]
	revision := func(id string) int64 {
		for _, s := range h.sessions() {
			if s.ID == id {
				return s.Revision
			}
		}
		t.Fatalf("no session %s", id)
		return 0
	}
	// A retires; B's renewal makes it the one qualifier, which fences A.
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/retire", core.Mutation{Family: "codex", ID: "synthetic-a", IfRevision: revision("synthetic-a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/renew", core.Mutation{Family: "codex", ID: "synthetic-b", IfRevision: revision("synthetic-b")}); err != nil {
		t.Fatal(err)
	}
	for tool, args := range map[string]map[string]any{"inbox": {"participant_after": 0}, "ack": {"participant_through": 0}} {
		reply, isError := h.tool(tool, args, thread("synthetic-a"))
		if !isError || reply["code"] != "stale_holder" {
			t.Fatalf("%s by the former holder: %v", tool, reply)
		}
	}
	reply, isError := h.tool("inbox", map[string]any{}, thread("synthetic-b"))
	participant, _ := reply["inbox"].(map[string]any)["participant"].(map[string]any)
	if isError || participant == nil || participant["address"] == nil {
		t.Fatalf("holder inbox: %v", reply)
	}
}
