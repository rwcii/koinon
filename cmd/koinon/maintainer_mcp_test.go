package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// Without --address, the process finds the daemon at its recorded port for each call: after
// a restart on another port it reaches the new daemon, whose answer ends the token, and exits,
// in place of answering daemon_unavailable for ever (#549).
func TestMaintainerMCPFollowsRecordedPort(t *testing.T) {
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	record := func(address string) {
		t.Helper()
		_, port, _ := net.SplitHostPort(address)
		n, _ := strconv.Atoi(port)
		if err := core.RecordPort(root, n); err != nil {
			t.Fatal(err)
		}
	}
	record(d.Addresses()[0])
	p := startMaintainerMCP(t, []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin", "--state-dir", root}, mcpSecret+"\n")
	p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	if reply, isError := p.tool("peers", map[string]any{}); isError {
		t.Fatalf("peers: %v", reply)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	moved := maintainerMCPDaemon(t, root, "127.0.0.1:0", false)
	record(moved.Addresses()[0])
	if reply, isError := p.tool("board_list", map[string]any{}); !isError || reply["code"] != "maintainer_token_expired" {
		t.Fatalf("after the move: %v", reply)
	}
	var refused core.RefusedError
	if err := p.end(); !errors.As(err, &refused) || refused.Code != "maintainer_token_expired" {
		t.Fatalf("exit: %v", err)
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

// call sends one request and returns its reply's result or error, keeping each notice that
// arrives before it for await.
func (p *mcpProcess) call(method string, params any, pending *[]string) map[string]any {
	p.t.Helper()
	p.id++
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": p.id, "method": method, "params": params})
	if _, err := p.in.Write(append(data, '\n')); err != nil {
		p.t.Fatal(err)
	}
	for {
		line, ok := p.next()
		var reply map[string]any
		if !ok || json.Unmarshal([]byte(line), &reply) != nil {
			p.t.Fatalf("%s: %q", method, line)
		}
		if reply["method"] == "notifications/resources/updated" {
			*pending = append(*pending, noticeURI(p.t, reply))
			continue
		}
		if reply["id"] != float64(p.id) {
			p.t.Fatalf("%s: %q", method, line)
		}
		return reply
	}
}

// noticeURI checks that a notice carries only its URI and returns it.
func noticeURI(t *testing.T, notice map[string]any) string {
	t.Helper()
	params, _ := notice["params"].(map[string]any)
	uri, _ := params["uri"].(string)
	if len(notice) != 3 || notice["jsonrpc"] != "2.0" || len(params) != 1 || uri == "" {
		t.Fatalf("notice: %v", notice)
	}
	return uri
}

// await reads notices until each of uris has one, and fails on a notice for another URI.
func (p *mcpProcess) await(pending *[]string, uris ...string) {
	p.t.Helper()
	want := map[string]bool{}
	for _, uri := range uris {
		want[uri] = true
	}
	take := func(uri string) {
		if !want[uri] {
			p.t.Fatalf("a notice for %s, want %v", uri, uris)
		}
		delete(want, uri)
	}
	for _, uri := range *pending {
		take(uri)
	}
	*pending = nil
	for len(want) > 0 {
		select {
		case line, ok := <-p.lines:
			var notice map[string]any
			if !ok || json.Unmarshal([]byte(line), &notice) != nil || notice["method"] != "notifications/resources/updated" {
				p.t.Fatalf("waiting for notices %v: %q", uris, line)
			}
			take(noticeURI(p.t, notice))
		case <-time.After(10 * time.Second):
			p.t.Fatalf("no notice for %v", want)
		}
	}
}

// quiet waits past a few polls and checks that no notice came.
func (p *mcpProcess) quiet(pending *[]string) {
	p.t.Helper()
	time.Sleep(1500 * time.Millisecond)
	if reply := p.call("ping", map[string]any{}, pending); reply["result"] == nil || len(*pending) != 0 {
		p.t.Fatalf("notices %v", *pending)
	}
}

// noticedTool calls one tool as tool does, keeping each notice that arrives before its reply.
func (p *mcpProcess) noticedTool(pending *[]string, name string, arguments map[string]any) (map[string]any, bool) {
	p.t.Helper()
	result, _ := p.call("tools/call", map[string]any{"name": name, "arguments": arguments}, pending)["result"].(map[string]any)
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

// Change notices over real stdio (PDR 009, Decision 3): a notice with only the URI for each
// change of a subscribed resource, none for an unsubscribed room or after an unsubscribe, a
// clean new subscription, a deleted room, and the end of the token while the input is idle.
func TestMaintainerMCPChangeNotices(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	bearer, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := core.Key{Family: "deepseek", ID: "synthetic-notice-agent"}
	if _, err := core.Call(ctx, address, bearer, "/v1/sessions/register", core.Registration{Family: agent.Family, ID: agent.ID, Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	agentCall := func(path string, body map[string]any) {
		t.Helper()
		body["caller"] = agent
		if _, err := core.Call(ctx, address, bearer, path, body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	p := startMaintainerMCP(t, []string{"--maintainer", "--client", "koinon-voice", "--secret-stdin", "--state-dir", root, "--address", address}, mcpSecret+"\n")
	var pending []string
	initialized := p.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}, &pending)
	if resources, _ := initialized["result"].(map[string]any)["capabilities"].(map[string]any)["resources"].(map[string]any); resources["subscribe"] != true {
		t.Fatalf("initialize: %v", initialized)
	}
	room := func(name string) string {
		created, isError := p.noticedTool(&pending, "room_create", map[string]any{"name": name, "key": name})
		if isError {
			t.Fatalf("room_create: %v", created)
		}
		return created["result"].(map[string]any)["room_id"].(string)
	}
	inbox, decisions := "koinon://maintainer/inbox", "koinon://maintainer/decisions"
	roomA, roomB := "koinon://maintainer/rooms/"+room("synthetic-a"), "koinon://maintainer/rooms/"+room("synthetic-b")
	listed := p.call("resources/list", map[string]any{}, &pending)["result"].(map[string]any)["resources"].([]any)
	var uris []string
	for _, r := range listed {
		uris = append(uris, r.(map[string]any)["uri"].(string))
	}
	slices.Sort(uris)
	if want := []string{decisions, inbox, roomA, roomB}; !slices.Equal(uris, sortedCopy(want)) {
		t.Fatalf("resources: %v", uris)
	}
	for _, uri := range []string{inbox, decisions, roomA} {
		if reply := p.call("resources/subscribe", map[string]any{"uri": uri}, &pending); reply["result"] == nil {
			t.Fatalf("subscribe %s: %v", uri, reply)
		}
	}
	// Each subscription sends one notice after its reply, so the client reads again.
	p.await(&pending, inbox, decisions, roomA)
	post := func(uri, key string) {
		t.Helper()
		reply, isError := p.noticedTool(&pending, "room_post", map[string]any{"room_id": strings.TrimPrefix(uri, "koinon://maintainer/rooms/"), "body": "a synthetic post",
			"key": key, "deadline": float64(time.Now().Unix() + 600)})
		if isError {
			t.Fatalf("room_post: %v", reply)
		}
	}
	// A new message, its acknowledgement, a new waiting item, its edit, a verdict and a close.
	agentCall("/v1/messages/send", map[string]any{"to": "maintainer", "body": "a synthetic report"})
	p.await(&pending, inbox)
	read := func(uri string) string {
		t.Helper()
		reply := p.call("resources/read", map[string]any{"uri": uri}, &pending)
		contents, _ := reply["result"].(map[string]any)["contents"].([]any)
		if len(contents) != 1 || contents[0].(map[string]any)["mimeType"] != "application/json" || contents[0].(map[string]any)["uri"] != uri {
			t.Fatalf("read %s: %v", uri, reply)
		}
		return contents[0].(map[string]any)["text"].(string)
	}
	toolText := func(name string, arguments map[string]any) string {
		t.Helper()
		result := p.call("tools/call", map[string]any{"name": name, "arguments": arguments}, &pending)["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	if text := read(inbox); text != toolText("inbox", map[string]any{}) || !strings.Contains(text, "a synthetic report") {
		t.Fatalf("inbox resource: %s", text)
	}
	if reply, isError := p.noticedTool(&pending, "ack", map[string]any{"through": 1}); isError {
		t.Fatalf("ack: %v", reply)
	}
	p.await(&pending, inbox)
	agentCall("/v1/decisions/create", map[string]any{"reference": "N-1", "key": "n1", "deadline": float64(time.Now().Unix() + 600),
		"title": "Synthetic decision", "kind": "decision"})
	p.await(&pending, decisions)
	if text := read(decisions); text != toolText("board_list", map[string]any{"filter": "waiting"}) || !strings.Contains(text, `"reference":"N-1"`) {
		t.Fatalf("decisions resource: %s", text)
	}
	agentCall("/v1/decisions/edit", map[string]any{"reference": "N-1", "if_revision": 1, "title": "Synthetic decision, edited"})
	p.await(&pending, decisions)
	if reply, isError := p.noticedTool(&pending, "decision_verdict", map[string]any{"reference": "N-1", "revision": 2, "verdict": "approved"}); isError {
		t.Fatalf("verdict: %v", reply)
	}
	p.await(&pending, decisions)
	agentCall("/v1/decisions/close", map[string]any{"reference": "N-1", "if_revision": 2, "key": "c1", "deadline": float64(time.Now().Unix() + 600)})
	p.await(&pending, decisions)
	// A post in the subscribed room; none for the other room.
	post(roomA, "p1")
	p.await(&pending, roomA)
	if text := read(roomA); text != toolText("room_read", map[string]any{"room_id": strings.TrimPrefix(roomA, "koinon://maintainer/rooms/")}) {
		t.Fatalf("room resource: %s", text)
	}
	post(roomB, "p2")
	p.quiet(&pending)
	// After the last unsubscribe nothing is noticed; a new subscription starts cleanly.
	for _, uri := range []string{roomA, inbox, decisions} {
		p.call("resources/unsubscribe", map[string]any{"uri": uri}, &pending)
	}
	post(roomA, "p3")
	agentCall("/v1/messages/send", map[string]any{"to": "maintainer", "body": "another synthetic report"})
	p.quiet(&pending)
	p.call("resources/subscribe", map[string]any{"uri": roomA}, &pending)
	p.await(&pending, roomA)
	post(roomA, "p4")
	p.await(&pending, roomA)
	// A deleted room is a change; reading it then gives the room's refusal.
	if _, err := core.Call(ctx, address, bearer, "/v1/rooms/delete", map[string]any{"room_id": strings.TrimPrefix(roomA, "koinon://maintainer/rooms/")}); err != nil {
		t.Fatal(err)
	}
	p.await(&pending, roomA)
	failure, _ := p.call("resources/read", map[string]any{"uri": roomA}, &pending)["error"].(map[string]any)
	if data, _ := failure["data"].(map[string]any); data["code"] != "room_not_found" {
		t.Fatalf("read a deleted room: %v", failure)
	}
	// A secret change ends the token during the poll: the process exits with the code while
	// its input stays open and idle.
	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("another synthetic secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	var refused core.RefusedError
	if err := p.end(); !errors.As(err, &refused) || refused.Code != "maintainer_token_expired" {
		t.Fatalf("exit: %v", err)
	}
	if line, ok := p.next(); ok {
		t.Fatalf("output after the exit: %q", line)
	}
}

func sortedCopy(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return values
}

// TestMaintainerMCPSubscribeAfterRead checks that a change between a client's read and its
// subscription is noticed: the subscription's own notice asks the client to read again.
func TestMaintainerMCPSubscribeAfterRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	bearer, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := core.Key{Family: "deepseek", ID: "synthetic-read-agent"}
	if _, err := core.Call(ctx, address, bearer, "/v1/sessions/register", core.Registration{Family: agent.Family, ID: agent.ID, Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	p := startMaintainerMCP(t, []string{"--maintainer", "--client", "synthetic-read", "--secret-stdin", "--state-dir", root, "--address", address}, mcpSecret+"\n")
	var pending []string
	p.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}, &pending)
	uri := "koinon://maintainer/inbox"
	if reply := p.call("resources/read", map[string]any{"uri": uri}, &pending); reply["result"] == nil {
		t.Fatalf("read: %v", reply)
	}
	// A change commits after the client's read and before its subscription.
	if _, err := core.Call(ctx, address, bearer, "/v1/messages/send", map[string]any{"caller": agent, "to": "maintainer", "body": "synthetic change between read and subscribe"}); err != nil {
		t.Fatal(err)
	}
	if reply := p.call("resources/subscribe", map[string]any{"uri": uri}, &pending); reply["result"] == nil {
		t.Fatalf("subscribe: %v", reply)
	}
	p.await(&pending, uri)
}
