package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/signed"
)

// koinon mcp --maintainer --signed (#693; Decision 5.1 and 5.3 of the signed maintainer
// contract) over real stdio pipes: a session envelope on the first line, then the tool signed
// with envelopes as objects and as strings, at the size bounds. Keys and sessions are
// synthetic.
// signedFixture is a daemon with one agent session and one enrolled client key.
type signedFixture struct {
	root, address, bearer, name, keyID, pin string
	envelope                                func(op string, args map[string]any) []byte
	// envelope2 signs a version 2 envelope (#696).
	envelope2 func(op string, args map[string]any) []byte
}

func newSignedFixture(t *testing.T) signedFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", false)
	address := d.Addresses()[0]
	bearer, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := core.Key{Family: "deepseek", ID: "synthetic-signed-agent"}
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

	id, err := link.LoadIdentity(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))
	public := private.Public().(ed25519.PublicKey)
	keyID := signed.KeyID(public)
	enroll, err := signed.Sign(map[string]any{"v": signed.Version, "key": keyID, "target": id.Pin, "op": "enroll", "created": time.Now().Unix(),
		"args": map[string]any{"user": "maintainer", "client": "koinon-voice", "public_key": base64.StdEncoding.EncodeToString(public)}}, private)
	if err != nil {
		t.Fatal(err)
	}
	statement := filepath.Join(t.TempDir(), "statement.json")
	os.WriteFile(statement, enroll, 0o600)
	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "key", "add", "--statement", statement, "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var e struct {
		Auth string `json:"authorization"`
	}
	keys, _ := core.Call(ctx, address, bearer, "/v1/maintainer/keys", map[string]any{"action": "list"})
	var keyList struct {
		Result struct {
			Keys []struct {
				Auth string `json:"authorization"`
			} `json:"keys"`
		} `json:"result"`
	}
	json.Unmarshal(keys, &keyList)
	e.Auth = keyList.Result.Keys[0].Auth
	n := 0
	sign := func(v, op string, args map[string]any) []byte {
		n++
		data, err := signed.Sign(map[string]any{"v": v, "key": keyID, "auth": e.Auth, "target": id.Pin, "op": op, "args": args,
			"request": fmt.Sprintf("mcp-request-%06d", n), "created": time.Now().Unix()}, private)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	var mu sync.Mutex
	locked := func(v string) func(op string, args map[string]any) []byte {
		return func(op string, args map[string]any) []byte {
			mu.Lock()
			defer mu.Unlock()
			return sign(v, op, args)
		}
	}
	return signedFixture{root: root, address: address, bearer: bearer, name: name, keyID: keyID, pin: id.Pin,
		envelope: locked(signed.Version), envelope2: locked(signed.Version2)}
}

func TestSignedMaintainerMCP(t *testing.T) {
	f := newSignedFixture(t)
	root, address, name, keyID, envelope, ctx := f.root, f.address, f.name, f.keyID, f.envelope, context.Background()
	var out bytes.Buffer
	// Refusals end the command before any MCP output.
	for _, first := range []string{"\n", "not an envelope\n", string(envelope("inbox", map[string]any{})) + "\n"} {
		err := run(ctx, []string{"mcp", "--maintainer", "--signed", "--state-dir", root, "--address", address}, strings.NewReader(first), &out)
		if err == nil {
			t.Fatalf("session %q accepted", first)
		}
	}
	if err := run(ctx, []string{"mcp", "--maintainer", "--signed", "--client", "x", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err == nil {
		t.Fatal("--signed with --client accepted")
	}

	p := startMaintainerMCP(t, []string{"--maintainer", "--signed", "--state-dir", root, "--address", address},
		string(envelope("session", map[string]any{"client": "koinon-voice"}))+"\r\n")
	initialized := p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "koinon-voice"}})
	if !strings.Contains(initialized["instructions"].(string), "signed maintainer session") || initialized["capabilities"].(map[string]any)["resources"] != nil {
		t.Fatalf("initialize: %v", initialized)
	}
	tools := p.request("tools/list", map[string]any{})["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "signed" {
		t.Fatalf("tools: %v", tools)
	}
	p.id++
	p.in.WriteString(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"resources/list","params":{}}`+"\n", p.id))
	if line, _ := p.next(); !strings.Contains(line, "-32601") {
		t.Fatalf("resources/list: %s", line)
	}

	// An envelope as an object and as a string.
	if inbox, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(envelope("inbox", map[string]any{}))}); isError || inbox["ok"] != true {
		t.Fatalf("inbox: %v", inbox)
	}
	if peersAnswer, isError := p.tool("signed", map[string]any{"envelope": string(envelope("peers", map[string]any{}))}); isError || peersAnswer["ok"] != true {
		t.Fatalf("peers: %v", peersAnswer)
	}
	// The largest body that send takes, all control characters: JSON escapes it sixfold, and
	// the envelope, the MCP line and the daemon request still carry it.
	body := strings.Repeat("\x01", 64*1024)
	large := envelope("send", map[string]any{"to": name, "body": body})
	if len(large) < 6*64*1024 || len(large) > signed.MaxEnvelopeBytes {
		t.Fatalf("large envelope: %d bytes", len(large))
	}
	if sent, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(large)}); isError || sent["ok"] != true {
		t.Fatalf("large send: %v", sent)
	}
	// A refusal comes back as the target's answer.
	bad := bytes.Replace(envelope("inbox", map[string]any{"limit": int64(5)}), []byte(`"limit":5`), []byte(`"limit":6`), 1)
	if refused, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(bad)}); !isError || refused["code"] != "signature_refused" ||
		refused["details"].(map[string]any)["reason"] != "signature" {
		t.Fatalf("bad signature: %v", refused)
	}
	if refused, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(`"\ud800"`)}); !isError || refused["code"] != "invalid_arguments" {
		t.Fatalf("invalid string: %v", refused)
	}

	// Removing the key ends the session: the next call answers maintainer_token_expired and
	// the server exits.
	if err := run(ctx, []string{"maintainer", "key", "remove", keyID, "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if expired, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(envelope("inbox", map[string]any{}))}); !isError || expired["code"] != "maintainer_token_expired" {
		t.Fatalf("after removal: %v", expired)
	}
	var refused core.RefusedError
	if err := p.end(); !errors.As(err, &refused) || refused.Code != mcp.ErrTokenExpired.Code {
		t.Fatalf("end: %v", err)
	}
}

// A version 2 session starts the local signed MCP unsigned, as its bootstrap; each answer of
// the signed tool to a version 2 envelope is the target's signed object, a refusal too, and not
// an error of the tool (#696; contract.md, sections 2.5 and 3.6).
func TestSignedMCPVersion2(t *testing.T) {
	f := newSignedFixture(t)
	p := startMaintainerMCP(t, []string{"--maintainer", "--signed", "--state-dir", f.root, "--address", f.address},
		string(f.envelope2("session", map[string]any{"client": "koinon-voice"}))+"\n")
	p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "koinon-voice"}})
	call := func(name string, data []byte, code string) {
		t.Helper()
		e, err := signed.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		result := p.request("tools/call", map[string]any{"name": "signed", "arguments": map[string]any{"envelope": json.RawMessage(data)}})
		content, _ := result["content"].([]any)
		if len(content) != 1 || result["isError"] == true {
			t.Fatalf("%s: %v", name, result)
		}
		text := content[0].(map[string]any)["text"].(string)
		v, err := signed.VerifyAnswer([]byte(text), signed.Expect{Request: e.Digest(), Target: f.pin, Node: f.pin})
		if err != nil || v.Signer != signed.SignerTarget || v.Code != code {
			t.Fatalf("%s: %+v %v: %s", name, v, err, text)
		}
	}
	// Each operation of a version 2 envelope that this fixture can run, on the node's own pin.
	sent := f.envelope2("send", map[string]any{"to": f.name, "body": "a synthetic \"v2\" send \x01 é"})
	call("send", sent, "")
	e, _ := signed.Parse(sent)
	var created int64
	json.Unmarshal(bytes.Split(bytes.SplitN(sent, []byte(`"created":`), 2)[1], []byte(","))[0], &created)
	for _, op := range []struct {
		op   string
		args map[string]any
		code string
	}{
		{"board_list", map[string]any{}, ""},
		{"decision_get", map[string]any{"reference": "MCP-404"}, "not_found"},
		{"decision_history", map[string]any{"reference": "MCP-404"}, "not_found"},
		{"inbox", map[string]any{}, ""},
		{"room_list", map[string]any{}, ""},
		{"room_read", map[string]any{"room": "synthetic-no-room"}, "invalid_request"},
		{"peers", map[string]any{}, ""},
		{"peer_status", map[string]any{"peer": f.name}, ""},
		{"computers", map[string]any{}, ""},
		{"changes", map[string]any{}, ""},
		{"outcome", map[string]any{"request": e.Request, "created": created}, ""},
		{"ack", map[string]any{"through": int64(0)}, ""},
		{"room_post", map[string]any{"room": "synthetic-no-room", "body": "x"}, "invalid_request"},
		{"send", map[string]any{"to": "synthetic-nobody", "body": "x"}, "peer_not_found"},
	} {
		call(op.op, f.envelope2(op.op, op.args), op.code)
	}
	bad := bytes.Replace(f.envelope2("inbox", map[string]any{"limit": int64(5)}), []byte(`"limit":5`), []byte(`"limit":6`), 1)
	call("bad signature", bad, "signature_refused")
	// A version 1 envelope in the same session keeps its unsigned answer.
	if inbox, isError := p.tool("signed", map[string]any{"envelope": json.RawMessage(f.envelope("inbox", map[string]any{}))}); isError || inbox["ok"] != true {
		t.Fatalf("v1 inbox: %v", inbox)
	}
}

// writeCall writes one tools/call of the signed tool with its own id, without reading.
func (p *mcpProcess) writeCall(id int, envelope []byte) {
	p.t.Helper()
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "signed", "arguments": map[string]any{"envelope": json.RawMessage(envelope)}}})
	if _, err := p.in.Write(append(data, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

// replies reads n replies of tools/call by id, with each one's decoded answer.
func (p *mcpProcess) replies(n int) map[int]map[string]any {
	p.t.Helper()
	got := map[int]map[string]any{}
	for len(got) < n {
		line, ok := p.next()
		if !ok {
			p.t.Fatalf("output ended after %d replies", len(got))
		}
		var reply struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &reply) != nil || len(reply.Result.Content) != 1 {
			p.t.Fatalf("reply: %s", line)
		}
		var answer map[string]any
		json.Unmarshal([]byte(reply.Result.Content[0].Text), &answer)
		got[reply.ID] = answer
	}
	return got
}

// The signed form runs its calls at once (#693 chunk 2, C2-10): a changes wait does not hold
// another call; a call beyond the bound is refused at once; an expiry lets the calls in flight
// answer before the server exits.
func TestSignedMCPConcurrentCalls(t *testing.T) {
	f := newSignedFixture(t)
	ctx := context.Background()
	p := startMaintainerMCP(t, []string{"--maintainer", "--signed", "--state-dir", f.root, "--address", f.address},
		string(f.envelope("session", map[string]any{"client": "koinon-voice"}))+"\n")
	p.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "koinon-voice"}})
	first, _ := p.tool("signed", map[string]any{"envelope": json.RawMessage(f.envelope("changes", map[string]any{}))})
	versions := first["result"].(map[string]any)["versions"].(map[string]any)

	// A 20-second wait is open while an inbox read answers.
	p.writeCall(1000, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)}))
	time.Sleep(300 * time.Millisecond)
	started := time.Now()
	p.writeCall(1001, f.envelope("inbox", map[string]any{}))
	if got := p.replies(1); got[1001]["ok"] != true || time.Since(started) > 2*time.Second {
		t.Fatalf("inbox behind the wait: %v after %s", got, time.Since(started))
	}
	agent := core.Key{Family: "deepseek", ID: "synthetic-signed-agent"}
	if _, err := core.Call(ctx, f.address, f.bearer, "/v1/messages/send", map[string]any{"caller": agent, "to": "maintainer", "body": "a change"}); err != nil {
		t.Fatal(err)
	}
	if got := p.replies(1); fmt.Sprint(got[1000]["result"].(map[string]any)["changed"]) != "[inbox]" {
		t.Fatalf("wait: %v", got)
	}
	versions = nil
	again, _ := p.tool("signed", map[string]any{"envelope": json.RawMessage(f.envelope("changes", map[string]any{}))})
	versions = again["result"].(map[string]any)["versions"].(map[string]any)

	// With a wait open for each target that a node can have (itself and link.MaxPairs pairs), a
	// read and a write still answer at once.
	for i := 0; i < 65; i++ {
		p.writeCall(1100+i, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(5)}))
	}
	time.Sleep(500 * time.Millisecond)
	started = time.Now()
	p.writeCall(1200, f.envelope("inbox", map[string]any{}))
	p.writeCall(1201, f.envelope("ack", map[string]any{"through": int64(0)}))
	if fast := p.replies(2); fast[1200]["ok"] != true || fast[1201]["ok"] != true || time.Since(started) > 2*time.Second {
		t.Fatalf("calls beside 65 waits: %v after %s", fast, time.Since(started))
	}
	if waits := p.replies(65); len(waits) != 65 {
		t.Fatalf("waits: %d", len(waits))
	}

	// The bound: maxSignedCalls waits are open; one more call is refused at once.
	const limit = 128
	for i := 0; i < limit; i++ {
		p.writeCall(2000+i, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(5)}))
	}
	time.Sleep(500 * time.Millisecond)
	started = time.Now()
	rejected := f.envelope("send", map[string]any{"to": f.name, "body": "never sent"})
	p.writeCall(3000, rejected)
	got := p.replies(1)
	if got[3000]["code"] != "too_many_calls" || got[3000]["details"].(map[string]any)["limit"] != float64(limit) || time.Since(started) > 2*time.Second {
		t.Fatalf("beyond the bound: %v", got)
	}
	for id, answer := range p.replies(limit) {
		if id < 2000 || id >= 2000+limit || answer["ok"] != true {
			t.Fatalf("wait %d: %v", id, answer)
		}
	}
	// The refused write reached nothing: its request has no receipt.
	var sent struct {
		Request string `json:"request"`
		Created int64  `json:"created"`
	}
	json.Unmarshal(rejected, &sent)
	if outcome, _ := p.tool("signed", map[string]any{"envelope": json.RawMessage(f.envelope("outcome", map[string]any{"request": sent.Request, "created": sent.Created}))}); outcome["result"].(map[string]any)["state"] != "unknown_request" {
		t.Fatalf("refused write: %v", outcome)
	}

	// Expiry with a wait in flight: both calls answer, then the server exits.
	p.writeCall(4000, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)}))
	time.Sleep(300 * time.Millisecond)
	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "key", "remove", f.keyID, "--state-dir", f.root, "--address", f.address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	p.writeCall(4001, f.envelope("inbox", map[string]any{}))
	final := p.replies(2)
	if final[4001]["code"] != "maintainer_token_expired" || final[4000]["ok"] != false {
		t.Fatalf("expiry: %v", final)
	}
	var refused core.RefusedError
	if err := p.end(); !errors.As(err, &refused) || refused.Code != "maintainer_token_expired" {
		t.Fatalf("end: %v", err)
	}
}

// The end of the input lets an open wait answer before the server returns; a cancelled run
// ends its calls at once and writes nothing after it returns (C2-10).
func TestSignedMCPEndAndCancel(t *testing.T) {
	f := newSignedFixture(t)
	first := string(f.envelope("session", map[string]any{"client": "koinon-voice"})) + "\n"
	call := func(id int, envelope []byte) string {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "signed", "arguments": map[string]any{"envelope": json.RawMessage(envelope)}}})
		return string(data) + "\n"
	}
	versions := map[string]any{"inbox": "00000000000000000001", "decisions": "00000000000000000001", "computers": "00000000000000000001"}

	// EOF with a 3-second wait open: the reply is written, then the run returns nil.
	var out bytes.Buffer
	input := first + call(1, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(3)}))
	started := time.Now()
	if err := run(context.Background(), []string{"mcp", "--maintainer", "--signed", "--state-dir", f.root, "--address", f.address}, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id":1`) || time.Since(started) < 2*time.Second {
		t.Fatalf("EOF: %q after %s", out.String(), time.Since(started))
	}

	// Cancel with a 20-second wait open: the run returns within a few seconds and writes
	// nothing after it returns.
	inR, inW, _ := os.Pipe()
	after := &afterWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"mcp", "--maintainer", "--signed", "--state-dir", f.root, "--address", f.address}, inR, after)
		after.close()
	}()
	inW.WriteString(first + call(2, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)})))
	time.Sleep(500 * time.Millisecond)
	cancel()
	started = time.Now()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled run did not end")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("cancel took %s", time.Since(started))
	}
	inW.Close()
	inR.Close()
	time.Sleep(500 * time.Millisecond)
	if after.late() {
		t.Fatal("the run wrote after it returned")
	}
}

// afterWriter records whether anything was written after close.
type afterWriter struct {
	mu     sync.Mutex
	closed bool
	wrote  bool
}

func (w *afterWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wrote = w.wrote || w.closed
	return len(p), nil
}

func (w *afterWriter) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

func (w *afterWriter) late() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wrote
}

// A call that reads maintainer_token_expired after the input ended makes the run's result
// maintainer_token_expired, not nil (review C2-10-F1).
func TestSignedMCPExpiryAfterEOF(t *testing.T) {
	f := newSignedFixture(t)
	inR, inW, _ := os.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"mcp", "--maintainer", "--signed", "--state-dir", f.root, "--address", f.address}, inR, &out)
	}()
	inW.WriteString(string(f.envelope("session", map[string]any{"client": "koinon-voice"})) + "\n")
	time.Sleep(time.Second)
	var removed bytes.Buffer
	if err := run(context.Background(), []string{"maintainer", "key", "remove", f.keyID, "--state-dir", f.root, "--address", f.address}, strings.NewReader(""), &removed); err != nil {
		t.Fatal(err)
	}
	call, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "signed", "arguments": map[string]any{"envelope": json.RawMessage(f.envelope("inbox", map[string]any{}))}}})
	inW.Write(append(call, '\n'))
	inW.Close()
	var refused core.RefusedError
	select {
	case err := <-done:
		if !errors.As(err, &refused) || refused.Code != "maintainer_token_expired" {
			t.Fatalf("result: %v, output %q", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the run did not end")
	}
	if !strings.Contains(out.String(), "maintainer_token_expired") {
		t.Fatalf("output: %q", out.String())
	}
	inR.Close()
}
