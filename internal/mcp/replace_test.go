package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// pipeServer runs Serve on a real pipe, as an agent starts koinon mcp, and collects every
// line it writes.
type pipeServer struct {
	t     *testing.T
	d     *core.Daemon
	root  string
	in    *os.File // the server's end
	write *os.File // the client's end
	lines chan map[string]any
	out   *io.PipeWriter

	mu      sync.Mutex
	stderr  bytes.Buffer
	changed atomic.Bool
	waits   atomic.Int32
}

func newPipeServer(t *testing.T) *pipeServer {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	in, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); write.Close() })
	reader, out := io.Pipe()
	p := &pipeServer{t: t, d: d, root: root, in: in, write: write, lines: make(chan map[string]any, 64), out: out}
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			var line map[string]any
			if json.Unmarshal(scanner.Bytes(), &line) != nil {
				line = map[string]any{"unparsed": scanner.Text()}
			}
			p.lines <- line
		}
	}()
	return p
}

// config is one program's configuration; replace stands for its exec.
func (p *pipeServer) config(version string, replace func([]byte) error) Config {
	return Config{StateDir: p.root, Address: p.d.Addresses()[0], Directory: nonRepositoryDir(p.t), ParentPID: 4242,
		Getenv: func(string) string { return "" },
		Command: func(int) (string, []string, error) {
			return "/opt/agent/bin/codex", []string{"/opt/agent/bin/codex"}, nil
		},
		Now: time.Now, ProcessStart: func(int) (int64, error) { return 0, errors.New("synthetic") },
		After: func(time.Duration) <-chan time.Time {
			p.waits.Add(1)
			return make(chan time.Time)
		},
		Version: version, Changed: func() bool { return p.changed.Swap(false) }, Replace: replace,
		CheckEvery: 10 * time.Millisecond, Stderr: writerFunc(func(b []byte) (int, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.stderr.Write(b)
		})}
}

// serve runs one program until it returns.
func (p *pipeServer) serve(c Config) <-chan error {
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), c, p.in, p.out) }()
	return done
}

func (p *pipeServer) send(text string) {
	p.t.Helper()
	if _, err := p.write.WriteString(text); err != nil {
		p.t.Fatal(err)
	}
}

func request(id int, method string, params any) string {
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return string(line) + "\n"
}

var peersCall = map[string]any{"name": "peers", "arguments": map[string]any{}, "_meta": map[string]any{"threadId": "thread-1"}}

func (p *pipeServer) next() map[string]any {
	p.t.Helper()
	select {
	case line := <-p.lines:
		return line
	case <-time.After(10 * time.Second):
		p.t.Fatal("no line from the server")
		return nil
	}
}

// answer reads the next line, which must answer id, and returns its tool result text.
func (p *pipeServer) answer(id int) map[string]any {
	p.t.Helper()
	line := p.next()
	if line["id"] != float64(id) {
		p.t.Fatalf("want the answer to %d, got %v", id, line)
	}
	result, _ := line["result"].(map[string]any)
	var value map[string]any
	if content, ok := result["content"].([]any); ok {
		json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &value)
	}
	return value
}

func (p *pipeServer) quiet(d time.Duration) {
	p.t.Helper()
	select {
	case line := <-p.lines:
		p.t.Fatalf("unexpected line %v", line)
	case <-time.After(d):
	}
}

func (p *pipeServer) session(key core.Key) core.Session {
	p.t.Helper()
	secret, _ := core.ReadSecret(p.root)
	data, err := core.Call(context.Background(), p.d.Addresses()[0], secret, "/v1/sessions", nil)
	if err != nil {
		p.t.Fatal(err)
	}
	var reply struct {
		Sessions []core.Session `json:"sessions"`
	}
	json.Unmarshal(data, &reply)
	for _, s := range reply.Sessions {
		if s.Family == key.Family && s.ID == key.ID {
			return s
		}
	}
	p.t.Fatalf("no session %v", key)
	return core.Session{}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// The program is replaced only between requests, with no byte read ahead: a partial line and
// a buffered line are answered first, and a request that arrives during the replacement is
// answered once, by the new program, which continues the client's session.
func TestReplaceAtIdlePoint(t *testing.T) {
	p := newPipeServer(t)
	states := make(chan []byte, 1)
	release := make(chan struct{})
	first := p.serve(p.config("v-old", func(state []byte) error {
		states <- state
		<-release // The new program's version command runs.
		return nil
	}))
	p.send(request(1, "initialize", map[string]any{"protocolVersion": "2025-03-26",
		"clientInfo": map[string]any{"name": "codex-mcp-client"}, "capabilities": map[string]any{"elicitation": map[string]any{}}}))
	init := fmt.Sprint(p.next())
	if !strings.Contains(init, "id:1") || !strings.Contains(init, "listChanged:true") || !strings.Contains(init, "version:v-old") {
		t.Fatalf("initialize %s", init)
	}
	p.send(request(2, "tools/call", peersCall))
	if reply := p.answer(2); reply["server_version"] != "v-old" {
		t.Fatalf("peers %v", reply)
	}
	key := core.Key{Family: "codex", ID: "thread-1"}
	before := p.session(key).Revision

	// A partial line holds the replacement back until the line ends.
	third := request(3, "ping", nil)
	p.send(third[:10])
	p.changed.Store(true)
	time.Sleep(100 * time.Millisecond)
	if len(states) != 0 {
		t.Fatal("replaced in the middle of a line")
	}
	// The end of that line and a whole second one arrive together, so the second is buffered.
	p.send(third[10:] + request(4, "tools/call", peersCall))
	p.answer(3)
	if reply := p.answer(4); reply["server_version"] != "v-old" {
		t.Fatalf("peers %v", reply)
	}
	var state []byte
	select {
	case state = <-states:
	case <-time.After(10 * time.Second):
		t.Fatal("not replaced at the idle point")
	}
	// This request arrives while the replacement runs: it stays in the pipe.
	p.send(request(5, "tools/call", peersCall))
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	var r resumeState
	if err := json.Unmarshal(state, &r); err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || r.Protocol != "2025-03-26" || r.Client != "codex-mcp-client" || string(r.Capabilities) != `{"elicitation":{}}` ||
		r.Latest != key || len(r.Sessions) != 1 || r.Sessions[0].Key != key || r.Sessions[0].Revision == 0 {
		t.Fatalf("state %s", state)
	}

	second := p.config("v-new", func([]byte) error { return errors.New("not replaced in this test") })
	second.Resume = state
	done := p.serve(second)
	if line := p.next(); line["method"] != "notifications/tools/list_changed" || line["id"] != nil {
		t.Fatalf("first line of the new program %v", line)
	}
	if reply := p.answer(5); reply["server_version"] != "v-new" {
		t.Fatalf("peers %v", reply)
	}
	// The new program renews the session at once, from the revision it was given.
	deadline := time.Now().Add(10 * time.Second)
	for p.session(key).Revision <= before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := p.session(key).Revision; after <= before {
		t.Fatalf("revision %d after %d", after, before)
	}
	p.quiet(100 * time.Millisecond)
	p.write.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stderr.Len() != 0 {
		t.Fatalf("stderr %q", p.stderr.String())
	}
}

// A replacement that does not happen leaves the program serving, with its loops running
// again and one line on standard error; a later change is tried again.
func TestReplacementFailureKeepsServing(t *testing.T) {
	p := newPipeServer(t)
	var attempts, stopped atomic.Int32
	done := p.serve(p.config("v-old", func([]byte) error {
		if attempts.Add(1) == 1 {
			// The loops are stopped during Replace, so a later renewal wait is a restarted loop.
			stopped.Store(p.waits.Load())
			return errors.New("the new program's version command failed: exit status 1")
		}
		return nil
	}))
	p.send(request(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "codex-mcp-client"}}))
	p.next()
	p.changed.Store(true)
	deadline := time.Now().Add(10 * time.Second)
	for (attempts.Load() != 1 || p.waits.Load() <= stopped.Load()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if attempts.Load() != 1 || p.waits.Load() <= stopped.Load() {
		t.Fatalf("attempts %d, renewal waits %d after the stop", attempts.Load(), p.waits.Load()-stopped.Load())
	}
	p.send(request(2, "tools/call", peersCall))
	if reply := p.answer(2); reply["server_version"] != "v-old" {
		t.Fatalf("peers %v", reply)
	}
	p.mu.Lock()
	if got := p.stderr.String(); got != "koinon mcp: kept the running program: the new program's version command failed: exit status 1\n" {
		t.Fatalf("stderr %q", got)
	}
	p.mu.Unlock()
	p.changed.Store(true)
	select {
	case err := <-done:
		if err != nil || attempts.Load() != 2 {
			t.Fatalf("err %v, attempts %d", err, attempts.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the later change was not tried")
	}
}

// A program is not replaced before the client's initialize has been answered.
func TestNoReplacementBeforeInitialize(t *testing.T) {
	p := newPipeServer(t)
	var attempts atomic.Int32
	done := p.serve(p.config("v-old", func([]byte) error { attempts.Add(1); return nil }))
	p.changed.Store(true)
	time.Sleep(100 * time.Millisecond)
	if attempts.Load() != 0 || !p.changed.Load() {
		t.Fatal("replaced before initialize")
	}
	p.send(request(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-code"}}))
	p.next()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("not replaced after initialize")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts %d", attempts.Load())
	}
}

// A Claude server keeps the host start that the first initialize pinned.
func TestResumeKeepsTheClaudeHost(t *testing.T) {
	p := newPipeServer(t)
	c := p.config("v-new", nil)
	c.Command = func(int) (string, []string, error) { return "/opt/claude/bin/claude", []string{"claude"}, nil }
	c.ProcessStart = func(int) (int64, error) { return 99, nil }
	s := &server{c: c, sessions: map[core.Key]registered{}}
	if err := s.resume([]byte(`{"v":1,"protocol_version":"2025-06-18","client":"claude-code","claude_host_start":42}`)); err != nil {
		t.Fatal(err)
	}
	if !s.claude || s.claudeHostStart != 42 || s.state().ClaudeHostStart != 42 {
		t.Fatalf("claude %v, host start %d", s.claude, s.claudeHostStart)
	}
}

// State that the program cannot read starts it as a new server: no notification, and the
// client's initialize is answered as usual.
func TestUnreadableResumeState(t *testing.T) {
	for _, state := range []string{`{`, `{"v":2,"protocol_version":"2025-06-18"}`, `{"v":1}`} {
		p := newPipeServer(t)
		c := p.config("v-new", nil)
		c.Resume = []byte(state)
		done := p.serve(c)
		p.send(request(1, "ping", nil))
		p.answer(1)
		p.write.Close()
		<-done
		p.mu.Lock()
		if !strings.HasPrefix(p.stderr.String(), "koinon mcp: the replaced program's state was not read: ") {
			t.Fatalf("%s: stderr %q", state, p.stderr.String())
		}
		p.mu.Unlock()
	}
}

// The real binary, started by a link as Homebrew installs it, runs the new release in the
// same process when the link moves, and the client's session goes on without a restart.
func TestReplacedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	dir := t.TempDir()
	for _, version := range []string{"v-test-1", "v-test-2"} {
		buildKoinon(t, filepath.Join(dir, version, "koinon"), version)
	}
	link := filepath.Join(dir, "bin", "koinon")
	os.Mkdir(filepath.Dir(link), 0700)
	if err := os.Symlink(filepath.Join(dir, "v-test-1", "koinon"), link); err != nil {
		t.Fatal(err)
	}
	p := newPipeServer(t)
	stdin, stderr, exited := p.command(link)
	send := func(text string) {
		if _, err := io.WriteString(stdin, text); err != nil {
			t.Fatal(err)
		}
	}
	send(request(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "codex-mcp-client"}}))
	if init := fmt.Sprint(p.next()); !strings.Contains(init, "version:v-test-1") {
		t.Fatalf("initialize %s", init)
	}
	send(request(2, "tools/call", peersCall))
	if reply := p.answer(2); reply["server_version"] != "v-test-1" {
		t.Fatalf("peers %v", reply)
	}
	key := core.Key{Family: "codex", ID: "thread-1"}
	before := p.session(key).Revision

	// brew upgrade moves the link to the new keg.
	moveLink(t, link, filepath.Join(dir, "v-test-2", "koinon"))
	select {
	case line := <-p.lines:
		if line["method"] != "notifications/tools/list_changed" {
			t.Fatalf("line %v", line)
		}
	case err := <-exited:
		t.Fatalf("the process ended: %v %s", err, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("not replaced; stderr %s", stderr.String())
	}
	// The next call goes to the new release, with no second initialize.
	send(request(3, "tools/call", peersCall))
	if reply := p.answer(3); reply["server_version"] != "v-test-2" {
		t.Fatalf("peers %v", reply)
	}
	if after := p.session(key).Revision; after <= before {
		t.Fatalf("revision %d after %d: the session was not renewed", after, before)
	}
	send(request(4, "initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "codex-mcp-client"}}))
	if init := fmt.Sprint(p.next()); !strings.Contains(init, "id:4") || !strings.Contains(init, "version:v-test-2") {
		t.Fatalf("initialize %s", init)
	}
	stdin.Close()
	select {
	case err := <-exited:
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("exit %v, stderr %q", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the process did not end with its input")
	}
	select {
	case line := <-p.lines:
		t.Fatalf("extra line %v", line)
	default:
	}
}

func buildKoinon(t *testing.T, path, version string) {
	t.Helper()
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", path, "../../cmd/koinon")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
}

// command starts the real binary by path on this server's pipes.
func (p *pipeServer) command(path string) (io.WriteCloser, *bytes.Buffer, <-chan error) {
	p.t.Helper()
	cmd := exec.Command(path, "mcp", "--state-dir", p.root, "--address", p.d.Addresses()[0])
	// No terminal, agent or Koinon variable of the test host reaches the server.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + p.t.TempDir()}
	cmd.Dir = nonRepositoryDir(p.t)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	cmd.Stdout = p.out
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() { stdin.Close(); cmd.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return stdin, stderr, exited
}

// moveLink points link at target in one rename, as brew upgrade moves the opt link.
func moveLink(t *testing.T, link, target string) {
	t.Helper()
	os.Remove(link + ".new")
	if err := os.Symlink(target, link+".new"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(link+".new", link); err != nil {
		t.Fatal(err)
	}
}

// The file that the version command checked is the one that runs, even when the link moves
// to another file during the check (#441 review).
func TestReplacementRunsTheProbedFile(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "koinon")
	buildKoinon(t, real, "v-test-a")
	started, gate, unprobed := filepath.Join(dir, "started"), filepath.Join(dir, "gate"), filepath.Join(dir, "unprobed")
	// A's version command waits for the test to move the link; otherwise A is the real build.
	a := filepath.Join(dir, "a", "koinon")
	b := filepath.Join(dir, "b", "koinon")
	for path, body := range map[string]string{
		a: fmt.Sprintf("if [ \"$1\" = version ]; then : > %q; while [ ! -f %q ]; do sleep 0.01; done; printf '{\"version\":\"a\",\"mcp_resume\":\"1\"}\\n'; exit 0; fi\nexec %q \"$@\"", started, gate, real),
		b: fmt.Sprintf("if [ \"$1\" = version ]; then printf '{\"version\":\"b\"}\\n'; exit 0; fi\n: > %q; exit 17", unprobed),
	} {
		os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "bin", "koinon")
	os.Mkdir(filepath.Dir(link), 0700)
	moveLink(t, link, real)
	p := newPipeServer(t)
	stdin, stderr, exited := p.command(link)
	io.WriteString(stdin, request(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "codex-mcp-client"}}))
	p.answer(1)
	moveLink(t, link, a)
	deadline := time.Now().Add(30 * time.Second)
	for _, err := os.Stat(started); err != nil; _, err = os.Stat(started) {
		if time.Now().After(deadline) {
			t.Fatalf("A's version command did not run; stderr %s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	moveLink(t, link, b)
	os.WriteFile(gate, nil, 0600)
	select {
	case line := <-p.lines:
		if line["method"] != "notifications/tools/list_changed" {
			t.Fatalf("line %v", line)
		}
	case err := <-exited:
		_, ran := os.Stat(unprobed)
		t.Fatalf("exited %v, B ran %v, stderr %s", err, ran == nil, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("A was not run; stderr %s", stderr.String())
	}
	io.WriteString(stdin, request(2, "ping", nil))
	p.answer(2)
	if _, err := os.Stat(unprobed); err == nil {
		t.Fatal("B ran without its version command")
	}
}
