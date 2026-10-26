package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// koinon mcp --maintainer (PDR 009, Decision 3) over real stdio pipes, as ssh HOST koinon mcp
// --maintainer runs it. The daemon, state, sessions and secrets are synthetic.

const mcpSecret = "a synthetic maintainer secret"

// maintainerMCPDaemon starts a daemon with a low PBKDF2 cost and, when set, the secret.
func maintainerMCPDaemon(t *testing.T, root, listen string, set bool) *core.Daemon {
	t.Helper()
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{listen}, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if set {
		var out bytes.Buffer
		if err := run(context.Background(), []string{"maintainer", "secret", "set", "--state-dir", root, "--address", d.Addresses()[0]},
			strings.NewReader(mcpSecret+"\n"), &out); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// mcpProcess is one koinon mcp --maintainer run with its standard input and output pipes.
type mcpProcess struct {
	t     *testing.T
	in    *os.File
	lines chan string
	done  chan error
	id    int
}

func startMaintainerMCP(t *testing.T, args []string, first string) *mcpProcess {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &mcpProcess{t: t, in: inW, lines: make(chan string, 16), done: make(chan error, 1)}
	go func() {
		err := run(context.Background(), append([]string{"mcp"}, args...), inR, outW)
		outW.Close()
		inR.Close()
		p.done <- err
	}()
	go func() {
		defer close(p.lines)
		reader := bufio.NewReader(outR)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				p.lines <- line
			}
			if err != nil {
				outR.Close()
				return
			}
		}
	}()
	if _, err := inW.WriteString(first); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Error("koinon mcp --maintainer did not end")
		}
	})
	return p
}

// next reads one line of output; ok is false at the end of the output.
func (p *mcpProcess) next() (string, bool) {
	p.t.Helper()
	select {
	case line, ok := <-p.lines:
		return line, ok
	case <-time.After(30 * time.Second):
		p.t.Fatal("no MCP output")
	}
	return "", false
}

// request sends one JSON-RPC request and returns its result.
func (p *mcpProcess) request(method string, params any) map[string]any {
	p.t.Helper()
	p.id++
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": p.id, "method": method, "params": params})
	if _, err := p.in.Write(append(data, '\n')); err != nil {
		p.t.Fatal(err)
	}
	line, ok := p.next()
	var reply struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
	}
	if !ok || json.Unmarshal([]byte(line), &reply) != nil || reply.ID != p.id || reply.Result == nil {
		p.t.Fatalf("%s: %q", method, line)
	}
	return reply.Result
}

// tool calls one tool and returns its decoded text and whether it is an error.
func (p *mcpProcess) tool(name string, arguments map[string]any) (map[string]any, bool) {
	p.t.Helper()
	result := p.request("tools/call", map[string]any{"name": name, "arguments": arguments})
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		p.t.Fatalf("%s: %v", name, result)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &value); err != nil {
		p.t.Fatalf("%s: %v", name, err)
	}
	return value, result["isError"] == true
}

// end waits for the run to end and returns its error.
func (p *mcpProcess) end() error {
	p.t.Helper()
	select {
	case err := <-p.done:
		p.done <- err
		return err
	case <-time.After(10 * time.Second):
		p.t.Fatal("koinon mcp --maintainer did not end")
	}
	return nil
}

func TestMaintainerMCPOverStdio(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	bearer, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := core.Key{Family: "deepseek", ID: "synthetic-mcp-agent"}
	if _, err := core.Call(ctx, address, bearer, "/v1/sessions/register", core.Registration{Family: agent.Family, ID: agent.ID, Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	peers, err := core.Call(ctx, address, bearer, "/v1/peers", map[string]any{"caller": agent})
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Peers []core.Peer `json:"peers"`
	}
	json.Unmarshal(peers, &listed)
	name := listed.Peers[0].Name
	if _, err := core.Call(ctx, address, bearer, "/v1/decisions/create", map[string]any{"caller": agent, "reference": "MCP-1", "key": "k1",
		"deadline": float64(time.Now().Unix() + 600), "title": "Synthetic decision", "kind": "decision", "recipients": []string{name}}); err != nil {
		t.Fatal(err)
	}

	// The secret's line ends with CRLF; the MCP stream follows it on the same input.
	p := startMaintainerMCP(t, []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin", "--state-dir", root, "--address", address}, mcpSecret+"\r\n")
	initialized := p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "koinon-voice"}})
	if initialized["protocolVersion"] != "2025-06-18" || !strings.Contains(initialized["instructions"].(string), "mcp:koinon-voice") {
		t.Fatalf("initialize: %v", initialized)
	}
	p.in.WriteString(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	tools := p.request("tools/list", map[string]any{})["tools"].([]any)
	var names []string
	for _, tool := range tools {
		tool := tool.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["inputSchema"].(map[string]any)["additionalProperties"] != false {
			t.Fatalf("%s schema: %v", tool["name"], tool["inputSchema"])
		}
	}
	slices.Sort(names)
	want := []string{"ack", "board_list", "decision_get", "decision_history", "decision_verdict", "inbox", "peer_status", "peers",
		"room_add", "room_create", "room_list", "room_post", "room_read", "room_remove", "send"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools: %v", names)
	}
	board, isError := p.tool("board_list", map[string]any{})
	items, _ := board["result"].(map[string]any)["items"].([]any)
	if isError || len(items) != 1 || items[0].(map[string]any)["reference"] != "MCP-1" {
		t.Fatalf("board_list: %v", board)
	}
	if reply, isError := p.tool("board_list", map[string]any{"token": "x"}); !isError || reply["code"] != "invalid_arguments" {
		t.Fatalf("a token argument: %v", reply)
	}
	if reply, isError := p.tool("decision_verdict", map[string]any{"reference": "MCP-1", "verdict": "approved"}); !isError || reply["code"] != "invalid_arguments" {
		t.Fatalf("a missing revision: %v", reply)
	}
	if reply, isError := p.tool("decision_verdict", map[string]any{"reference": "MCP-1", "revision": 2, "verdict": "approved"}); !isError || reply["code"] != "stale_revision" {
		t.Fatalf("a stale revision: %v", reply)
	}
	verdict, isError := p.tool("decision_verdict", map[string]any{"reference": "MCP-1", "revision": 1, "verdict": "approved", "note": "By voice."})
	if isError || verdict["result"].(map[string]any)["via"] != "mcp:koinon-voice" {
		t.Fatalf("decision_verdict: %v", verdict)
	}
	if sent, isError := p.tool("send", map[string]any{"to": name, "body": "a synthetic instruction"}); isError || sent["result"].(map[string]any)["recipient"] != name {
		t.Fatalf("send: %v", sent)
	}
	inbox, err := core.Call(ctx, address, bearer, "/v1/inbox/read", map[string]any{"caller": agent})
	if err != nil || !strings.Contains(string(inbox), `"sender_family":"maintainer","sender_name":"maintainer","body":"a synthetic instruction"`) {
		t.Fatalf("agent inbox: %s %v", inbox, err)
	}
	// The maintainer mode is no agent session: peers lists only the agent and the maintainer.
	if peers, err = core.Call(ctx, address, bearer, "/v1/peers", map[string]any{"caller": agent}); err != nil {
		t.Fatal(err)
	}
	listed.Peers = nil
	json.Unmarshal(peers, &listed)
	if len(listed.Peers) != 2 || listed.Peers[0].Name != name || listed.Peers[1].Name != "maintainer" {
		t.Fatalf("peers: %s", peers)
	}
	p.in.Close()
	if err := p.end(); err != nil {
		t.Fatalf("end of input: %v", err)
	}
}

// After a daemon restart the next tool call answers maintainer_token_expired and the process
// exits, so the client starts a new one; the new one works.
func TestMaintainerMCPRestartEndsProcess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	args := []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin", "--state-dir", root, "--address", address}
	p := startMaintainerMCP(t, args, mcpSecret+"\n")
	p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	if reply, isError := p.tool("peers", map[string]any{}); isError {
		t.Fatalf("peers: %v", reply)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	// While no daemon answers, a call says so and the process stays.
	if reply, isError := p.tool("board_list", map[string]any{}); !isError || reply["code"] != "daemon_unavailable" {
		t.Fatalf("without a daemon: %v", reply)
	}
	maintainerMCPDaemon(t, root, address, false)
	reply, isError := p.tool("board_list", map[string]any{})
	if !isError || reply["code"] != "maintainer_token_expired" {
		t.Fatalf("after the restart: %v", reply)
	}
	var refused core.RefusedError
	if err := p.end(); !errors.As(err, &refused) || refused.Code != "maintainer_token_expired" {
		t.Fatalf("exit: %v", err)
	}
	if line, ok := p.next(); ok {
		t.Fatalf("output after the exit: %q", line)
	}
	again := startMaintainerMCP(t, args, mcpSecret+"\n")
	again.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	if reply, isError := again.tool("board_list", map[string]any{}); isError {
		t.Fatalf("a new process: %v", reply)
	}
}

// A start that the daemon or the options refuse ends before any MCP output, with the code.
func TestMaintainerMCPStartRefusals(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", false)
	address := d.Addresses()[0]
	start := func(input string, args ...string) (string, error) {
		var out bytes.Buffer
		err := run(context.Background(), append([]string{"mcp", "--state-dir", root, "--address", address}, args...), strings.NewReader(input), &out)
		return out.String(), err
	}
	code := func(err error) string {
		var refused core.RefusedError
		var usage usageError
		switch {
		case errors.As(err, &refused):
			return refused.Code
		case errors.As(err, &usage):
			return usage.code
		}
		return ""
	}
	maintainer := []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin"}
	if out, err := start(mcpSecret+"\n", maintainer...); out != "" || code(err) != "maintainer_secret_unset" {
		t.Fatalf("no secret set: %q %v", out, err)
	}
	var set bytes.Buffer
	if err := run(context.Background(), []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader(mcpSecret+"\n"), &set); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		input string
		args  []string
	}{
		{mcpSecret + "\n", []string{"--client", "koinon-voice", "--secret-stdin"}},
		{mcpSecret + "\n", []string{"--maintainer", "--secret-stdin"}},
		{mcpSecret + "\n", []string{"--maintainer", "--client", "Koinon_Voice", "--secret-stdin"}},
		{mcpSecret + "\n", []string{"--maintainer", "--client", "koinon-voice"}},
		{mcpSecret + "\n", []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin", "--secret-file", "/nonexistent"}},
		{"", maintainer},
		{strings.Repeat("a", 1025) + "\n", maintainer},
	} {
		if out, err := start(c.input, c.args...); out != "" || code(err) != "invalid_request" || strings.Contains(err.Error(), "aaaa") {
			t.Fatalf("%v: %q %v", c.args, out, err)
		}
	}
	wrong := "a wrong synthetic secret"
	if out, err := start(wrong+"\n", maintainer...); out != "" || code(err) != "maintainer_secret_invalid" || strings.Contains(err.Error(), wrong) {
		t.Fatalf("a wrong secret: %q %v", out, err)
	}
	for range 4 {
		start(wrong+"\n", maintainer...)
	}
	out, err := start(mcpSecret+"\n", maintainer...)
	var refused core.RefusedError
	if out != "" || !errors.As(err, &refused) || refused.Code != "maintainer_secret_backoff" || !strings.Contains(string(refused.Details), `"retry_after_seconds":`) {
		t.Fatalf("during the wait: %q %v", out, err)
	}
}

// --secret-file takes a regular file of this user with mode 0600 or 0400, never a link.
func TestMaintainerMCPSecretFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	dir := t.TempDir()
	file := func(name string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(mcpSecret+"\nignored second line\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	private := file("private", 0600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	refusals := map[string]string{link: "symbolic link", dir: "not a regular file", file("group", 0640): "mode 0640",
		file("other", 0604): "mode 0604", file("exec", 0700): "mode 0700", filepath.Join(dir, "missing"): "cannot read"}
	if os.Geteuid() != 0 {
		// A file of another user: /etc/passwd belongs to root on Linux and macOS.
		refusals["/etc/passwd"] = "owned by another user"
	}
	for path, want := range refusals {
		var out bytes.Buffer
		err := run(context.Background(), []string{"mcp", "--maintainer", "--client", "koinon-voice", "--secret-file", path, "--state-dir", root, "--address", address},
			strings.NewReader(""), &out)
		var usage usageError
		if out.Len() != 0 || !errors.As(err, &usage) || usage.code != "invalid_request" || !strings.Contains(usage.message, want) {
			t.Fatalf("%s: %q %v", path, out.String(), err)
		}
	}
	for _, path := range []string{private, file("readonly", 0400)} {
		p := startMaintainerMCP(t, []string{"--maintainer", "--client", "koinon-file", "--secret-file", path, "--state-dir", root, "--address", address}, "")
		p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
		if reply, isError := p.tool("peers", map[string]any{}); isError {
			t.Fatalf("%s: %v", path, reply)
		}
		p.in.Close()
		if err := p.end(); err != nil {
			t.Fatal(err)
		}
	}
}
