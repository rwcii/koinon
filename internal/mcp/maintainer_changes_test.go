package mcp

import (
	"bufio"
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

// The change notices of the maintainer mode (PDR 009, Decision 3) against a synthetic daemon,
// over real pipes: the poll's start and end, the notices and the end of the token.

// fakeMaintainerDaemon answers the maintainer routes. changes answers a request without a
// cursor at once; poll answers one with a cursor.
type fakeMaintainerDaemon struct {
	mu      sync.Mutex
	version int
	polls   int
	poll    func(w http.ResponseWriter, r *http.Request)
	tool    func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeMaintainerDaemon) cursor() string {
	return fmt.Sprintf(`{"koinon://maintainer/inbox":"%020d","koinon://maintainer/decisions":"%020d"}`, f.version, 1)
}

// startFakeMaintainer starts the synthetic daemon and returns the server's config. A real
// daemon starts once for its secret.
func startFakeMaintainer(t *testing.T, f *fakeMaintainerDaemon) MaintainerConfig {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	f.version = 1
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/maintainer/changes" {
			f.tool(w, r)
			return
		}
		var q struct {
			Cursor map[string]string `json:"cursor"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		if q.Cursor == nil {
			f.mu.Lock()
			fmt.Fprintf(w, `{"ok":true,"result":{"cursor":%s,"changed":[]}}`, f.cursor())
			f.mu.Unlock()
			return
		}
		f.mu.Lock()
		f.polls++
		f.mu.Unlock()
		f.poll(w, r)
	}))
	t.Cleanup(daemon.Close)
	return MaintainerConfig{StateDir: root, Address: strings.TrimPrefix(daemon.URL, "http://"), Token: strings.Repeat("a", 64), Client: "synthetic"}
}

// maintainerPipes is ServeMaintainer on real pipes.
type maintainerPipes struct {
	t     *testing.T
	in    *os.File
	lines chan map[string]any
	done  chan error
	id    int
}

func serveMaintainerPipes(t *testing.T, c MaintainerConfig) *maintainerPipes {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &maintainerPipes{t: t, in: inW, lines: make(chan map[string]any, 256), done: make(chan error, 1)}
	go func() {
		err := ServeMaintainer(context.Background(), c, inR, outW)
		outW.Close()
		p.done <- err
	}()
	go func() {
		defer close(p.lines)
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			var line map[string]any
			json.Unmarshal(scanner.Bytes(), &line)
			p.lines <- line
		}
		outR.Close()
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Error("the server did not end")
		}
		inR.Close()
	})
	return p
}

// send writes one request and returns its ID.
func (p *maintainerPipes) send(method string, params any) int {
	p.t.Helper()
	p.id++
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": p.id, "method": method, "params": params})
	if _, err := p.in.Write(append(data, '\n')); err != nil {
		p.t.Fatal(err)
	}
	return p.id
}

// next reads one line; ok is false at the end of the output or after wait.
func (p *maintainerPipes) next(wait time.Duration) (map[string]any, bool) {
	select {
	case line, ok := <-p.lines:
		return line, ok
	case <-time.After(wait):
		return nil, false
	}
}

// reply reads lines up to the reply to id and returns it with the notices before it.
func (p *maintainerPipes) reply(id int) (map[string]any, []map[string]any) {
	p.t.Helper()
	var notices []map[string]any
	for {
		line, ok := p.next(10 * time.Second)
		if !ok {
			p.t.Fatalf("no reply to %d", id)
		}
		if line["id"] == float64(id) {
			return line, notices
		}
		notices = append(notices, line)
	}
}

// checkNotice fails unless line is a resource notice that carries only the URI.
func checkNotice(t *testing.T, line map[string]any, uri string) {
	t.Helper()
	params, _ := line["params"].(map[string]any)
	if line["method"] != "notifications/resources/updated" || len(params) != 1 || params["uri"] != uri || len(line) != 3 {
		t.Fatalf("notice: %v", line)
	}
}

// The last unsubscribe ends the poll before its reply: no poll starts and no notice is
// written after it, also while every poll returns a change. A new subscription starts a new
// poll. A poll sent before the reply can reach the daemon after it, so the count is taken
// after the quiet second and must then stay.
func TestMaintainerUnsubscribeEndsThePoll(t *testing.T) {
	f := &fakeMaintainerDaemon{}
	f.poll = func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		f.version++
		fmt.Fprintf(w, `{"ok":true,"result":{"cursor":%s,"changed":["koinon://maintainer/inbox"]}}`, f.cursor())
		f.mu.Unlock()
	}
	p := serveMaintainerPipes(t, startFakeMaintainer(t, f))
	polls := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.polls
	}
	for round := range 2 {
		if reply, _ := p.reply(p.send("resources/subscribe", map[string]any{"uri": inboxURI})); reply["result"] == nil {
			t.Fatalf("subscribe: %v", reply)
		}
		for range 3 {
			line, ok := p.next(5 * time.Second)
			if !ok {
				t.Fatalf("round %d: no notice", round)
			}
			checkNotice(t, line, inboxURI)
		}
		reply, notices := p.reply(p.send("resources/unsubscribe", map[string]any{"uri": inboxURI}))
		if reply["result"] == nil {
			t.Fatalf("unsubscribe: %v", reply)
		}
		for _, notice := range notices {
			checkNotice(t, notice, inboxURI)
		}
		if line, ok := p.next(time.Second); ok {
			t.Fatalf("output after the unsubscribe reply: %v", line)
		}
		after := polls()
		time.Sleep(500 * time.Millisecond)
		if polls() != after {
			t.Fatalf("polls after the unsubscribe reply: %d then %d", after, polls())
		}
	}
	// The end of the input ends a running poll before the server returns.
	p.reply(p.send("resources/subscribe", map[string]any{"uri": inboxURI}))
	p.in.Close()
	select {
	case err := <-p.done:
		p.done <- err
		if err != nil {
			t.Fatalf("end of input: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not end with its input")
	}
	time.Sleep(500 * time.Millisecond)
	after := polls()
	time.Sleep(500 * time.Millisecond)
	if polls() != after {
		t.Fatal("a poll after the server ended")
	}
}

// A token that ends during the poll ends the server, also while the input is open and idle;
// a tool call in progress gets its reply first.
func TestMaintainerPollExpiryAnswersThePendingCall(t *testing.T) {
	f := &fakeMaintainerDaemon{}
	arrived, refused := make(chan struct{}), make(chan struct{})
	f.poll = func(w http.ResponseWriter, r *http.Request) {
		<-arrived
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"ok":false,"code":"maintainer_token_expired","error":"the maintainer token has ended"}`)
		close(refused)
	}
	f.tool = func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-refused
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, `{"ok":true,"result":{"peers":[],"truncated":false}}`)
	}
	p := serveMaintainerPipes(t, startFakeMaintainer(t, f))
	p.reply(p.send("resources/subscribe", map[string]any{"uri": decisionsURI}))
	line, ok := p.next(5 * time.Second)
	if !ok {
		t.Fatal("no notice after the subscribe reply")
	}
	checkNotice(t, line, decisionsURI)
	reply, notices := p.reply(p.send("tools/call", map[string]any{"name": "peers", "arguments": map[string]any{}}))
	result, _ := reply["result"].(map[string]any)
	if len(notices) != 0 || result == nil || result["isError"] != false {
		t.Fatalf("the pending call: %v %v", reply, notices)
	}
	select {
	case err := <-p.done:
		p.done <- err
		var refusedErr core.RefusedError
		if !errors.As(err, &refusedErr) || refusedErr.Code != "maintainer_token_expired" {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not end with the token")
	}
	if line, ok := p.next(time.Second); ok {
		t.Fatalf("output after the exit: %v", line)
	}
}

// The maintainer mode advertises subscribable resources and refuses an unknown URI with a
// JSON-RPC error that carries the code; the agent mode has no resources.
func TestMaintainerResourceRefusals(t *testing.T) {
	f := &fakeMaintainerDaemon{tool: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"ok":false,"code":"room_not_found","error":"no room has that ID"}`)
	}}
	p := serveMaintainerPipes(t, startFakeMaintainer(t, f))
	initialized, _ := p.reply(p.send("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}))
	capabilities := initialized["result"].(map[string]any)["capabilities"].(map[string]any)
	if resources, _ := capabilities["resources"].(map[string]any); resources["subscribe"] != true {
		t.Fatalf("capabilities: %v", capabilities)
	}
	room := roomURI + strings.Repeat("0", 32)
	for _, c := range []struct {
		method, uri, code string
		rpc               float64
	}{
		{"resources/read", "koinon://maintainer/work", "unknown_resource", -32002},
		{"resources/subscribe", roomURI + strings.Repeat("A", 32), "unknown_resource", -32002},
		{"resources/unsubscribe", "file:///etc/passwd", "unknown_resource", -32002},
		{"resources/read", room, "room_not_found", -32002},
		{"resources/subscribe", room, "room_not_found", -32002},
	} {
		reply, _ := p.reply(p.send(c.method, map[string]any{"uri": c.uri}))
		failure, _ := reply["error"].(map[string]any)
		data, _ := failure["data"].(map[string]any)
		if failure["code"] != c.rpc || data["code"] != c.code {
			t.Fatalf("%s %s: %v", c.method, c.uri, reply)
		}
	}
	h := newHarness(t, "")
	agent := h.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "codex", "version": "1"}, "capabilities": map[string]any{}})
	if _, ok := agent["result"].(map[string]any)["capabilities"].(map[string]any)["resources"]; ok {
		t.Fatalf("the agent mode has resources: %v", agent)
	}
	if list := h.request("resources/list", map[string]any{}); list["error"] == nil {
		t.Fatalf("agent resources/list: %v", list)
	}
}
