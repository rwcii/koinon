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
func TestSignedMaintainerMCP(t *testing.T) {
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
	envelope := func(op string, args map[string]any) []byte {
		n++
		data, err := signed.Sign(map[string]any{"v": signed.Version, "key": keyID, "auth": e.Auth, "target": id.Pin, "op": op, "args": args,
			"request": fmt.Sprintf("mcp-request-%06d", n), "created": time.Now().Unix()}, private)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

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
