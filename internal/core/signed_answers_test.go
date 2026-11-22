package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/signed"
)

// Signed answers to version 2 envelopes (#696; docs/sprints/2026-11-22-signed-answers/
// contract.md, sections 2 to 4). All names, addresses and keys are synthetic.

// v2 signs op as a version 2 envelope under a new request ID.
func (c *signedClient) v2(op string, args map[string]any) []byte {
	return c.envelope(op, args, c.request(), time.Now().Unix(), map[string]any{"v": signed.Version2})
}

// replyOf runs data on d and gives the status and body as d's routes and the link write them.
func replyOf(t *testing.T, d *Daemon, data []byte) (int, []byte) {
	t.Helper()
	result, err := d.SignedAction(context.Background(), data)
	return d.signedReply(data, result, err)
}

// linkPinOf gives d's link pin.
func linkPinOf(t *testing.T, d *Daemon) string {
	t.Helper()
	id, err := d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Pin
}

// expectFor is what a client knows when it sent data through the node with pin node.
func expectFor(t *testing.T, data []byte, node string) signed.Expect {
	t.Helper()
	e, err := signed.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return signed.Expect{Request: e.Digest(), Target: e.Target, Node: node}
}

// verifiedAs checks an answer as a client does, and that the transport's status is the signed
// status, the signer is signer and the code is code ("" for a success).
func verifiedAs(t *testing.T, name string, x signed.Expect, status int, body []byte, signer, code string) signed.Verified {
	t.Helper()
	v, err := signed.VerifyAnswer(body, x)
	if err != nil {
		t.Fatalf("%s: %v: %d %s", name, err, status, body)
	}
	if status >= 0 && v.Status != status || v.Signer != signer || v.Code != code || v.OK != (code == "") {
		t.Fatalf("%s: %+v, transport %d, want %s %q", name, v, status, signer, code)
	}
	return v
}

// transport sends one envelope to a node and gives the status and body that the client gets; a
// status below 0 means that the transport gives the client no status of its own.
type transport struct {
	name string
	send func(data []byte) (int, []byte)
}

// nodeTransports are the ways a client reaches node: the node's own handler, its Serve route,
// and the daemon client that the signed MCP tool uses on its loopback route.
func nodeTransports(t *testing.T, node *linkHost) []transport {
	t.Helper()
	token := remoteSession(t, node.d, newSignedClient(t, node.d, 72))
	bearer := linkBearer(t, node)
	session, _ := newSignedSessionToken(t, node.d, bearer, newSignedClient(t, node.d, 73))
	return []transport{
		{"handler", func(data []byte) (int, []byte) { return replyOf(t, node.d, data) }},
		{"Serve route", func(data []byte) (int, []byte) {
			return mustRemote(t, node.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
		}},
		{"MCP daemon client", func(data []byte) (int, []byte) {
			raw, err := CallEnvelope(context.Background(), node.d.Addresses()[0], bearer, "/v1/maintainer/signed", data, session, SignedCallTimeout)
			if err != nil {
				t.Fatalf("daemon client: %v", err)
			}
			return -1, raw
		}},
	}
}

// Each operation of a version 2 envelope, on the node's own pin and through the node for a
// paired pin, on each transport, answers an object that the target signed and the client
// verifies; the inner answer is the version 1 answer.
func TestSignedAnswersEachOperation(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	nodePin := linkPinOf(t, a.d)
	transports := nodeTransports(t, a)
	setup := func(h *linkHost, suffix string) (Session, string) {
		s := h.d.store
		agent := join(t, s, "codex", "synthetic-answer-"+suffix, "")
		key := Key{"codex", agent.ID}
		room, err := s.CreateRoom(ctx, "answer-room-"+suffix, "answer-room-"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRoomMember(ctx, room, agent.Name); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Send(ctx, key, "maintainer", "a synthetic report"); err != nil {
			t.Fatal(err)
		}
		for i := range transports {
			mustCreate(t, s, key, fmt.Sprintf("AN-%s-%d", strings.ToUpper(suffix), i), DecisionFields{Title: ptr("Synthetic answer"), Kind: ptr("decision"), Recipients: &[]string{agent.Name}})
		}
		return agent, room
	}
	ownAgent, ownRoom := setup(a, "a")
	pairedAgent, pairedRoom := setup(b, "b")
	own := newSignedClient(t, a.d, 60)
	pairedLocal := newSignedClient(t, b.d, 61)
	paired := viaNode(pairedLocal, a.d)
	for i, tr := range transports {
		for _, run := range []struct {
			name  string
			c     *signedClient
			agent Session
			room  string
			ref   string
			local *signedClient
		}{{"own pin", own, ownAgent, ownRoom, fmt.Sprintf("AN-A-%d", i), own}, {"paired pin", paired, pairedAgent, pairedRoom, fmt.Sprintf("AN-B-%d", i), pairedLocal}} {
			name := tr.name + ", " + run.name
			now := time.Now().Unix()
			sent := run.c.envelope("send", map[string]any{"to": run.agent.Name, "body": "a synthetic v2 send"}, run.c.request(), now,
				map[string]any{"v": signed.Version2})
			status, body := tr.send(sent)
			verifiedAs(t, name+" send", expectFor(t, sent, nodePin), status, body, signed.SignerTarget, "")
			sentRequest, _ := signed.Parse(sent)
			ops := []struct {
				op   string
				args map[string]any
				code string
			}{
				{"board_list", nil, ""},
				{"decision_get", map[string]any{"reference": run.ref}, ""},
				{"decision_get", map[string]any{"reference": "AN-404"}, "not_found"},
				{"decision_history", map[string]any{"reference": run.ref}, ""},
				{"inbox", nil, ""},
				{"room_list", nil, ""},
				{"room_read", map[string]any{"room": run.room}, ""},
				{"peers", nil, ""},
				{"peer_status", map[string]any{"peer": run.agent.Name}, ""},
				{"changes", nil, ""},
				{"outcome", map[string]any{"request": sentRequest.Request, "created": now}, ""},
				{"ack", map[string]any{"through": int64(1)}, ""},
				{"room_post", map[string]any{"room": run.room, "body": "a synthetic v2 post"}, ""},
				{"send", map[string]any{"to": "synthetic-nobody", "body": "x"}, "peer_not_found"},
			}
			if run.c == own {
				ops = append(ops, struct {
					op   string
					args map[string]any
					code string
				}{"computers", nil, ""})
			}
			for _, op := range ops {
				data := run.c.v2(op.op, op.args)
				status, body := tr.send(data)
				v := verifiedAs(t, name+" "+op.op, expectFor(t, data, nodePin), status, body, signed.SignerTarget, op.code)
				if op.code == "" && !bytes.Contains(v.Text, []byte(`"result":`)) {
					t.Fatalf("%s %s: no result: %s", name, op.op, v.Text)
				}
			}
			snapshot, revision := snapshotOf(t, run.local, run.ref)
			data := run.c.v2("decision_verdict", map[string]any{"reference": run.ref, "revision": revision, "snapshot": snapshot, "verdict": "approved"})
			status, body = tr.send(data)
			verifiedAs(t, name+" decision_verdict", expectFor(t, data, nodePin), status, body, signed.SignerTarget, "")
			// A refusal of the envelope itself is signed: a changed argument fails its signature.
			bad := bytes.Replace(run.c.v2("inbox", map[string]any{"limit": int64(5)}), []byte(`"limit":5`), []byte(`"limit":6`), 1)
			status, body = tr.send(bad)
			v := verifiedAs(t, name+" bad signature", expectFor(t, bad, nodePin), status, body, signed.SignerTarget, "signature_refused")
			if !bytes.Contains(v.Text, []byte(`"reason":"signature"`)) {
				t.Fatalf("%s bad signature: %s", name, v.Text)
			}
		}
	}
	// The inner answer is the version 1 answer of the same read.
	v1 := paired.envelope("decision_get", map[string]any{"reference": "AN-B-0"}, paired.request(), time.Now().Unix(), nil)
	_, v1Body := replyOf(t, a.d, v1)
	data := paired.v2("decision_get", map[string]any{"reference": "AN-B-0"})
	_, body := replyOf(t, a.d, data)
	v := verifiedAs(t, "v2 decision_get", expectFor(t, data, nodePin), 200, body, signed.SignerTarget, "")
	if string(v.Text) != string(v1Body) {
		t.Fatalf("v2 inner %s, v1 %s", v.Text, v1Body)
	}
	if _, err := signed.ParseAnswer(v1Body); err == nil {
		t.Fatal("a v1 answer is signed")
	}
}

// The node signs its own claims for a version 2 envelope, and the client verifies them as the
// node's (contract.md, section 4).
func TestSignedAnswersNodeClaims(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	agent := join(t, b.d.store, "codex", "synthetic-answer-claims", "")
	c := newSignedClient(t, b.d, 62)
	f := viaNode(c, a.d)
	nodePin := linkPinOf(t, a.d)

	// invalid_request: a node operation signed for a paired computer's pin.
	for _, op := range []string{"computers", "session"} {
		data := f.v2(op, map[string]any{})
		if op == "session" {
			data = f.v2(op, map[string]any{"client": "voice"})
		}
		status, body := replyOf(t, a.d, data)
		verifiedAs(t, op+" for a paired pin", expectFor(t, data, nodePin), status, body, signed.SignerNode, "invalid_request")
	}
	// signature_refused, reason target: a pin that is neither the node nor a paired computer.
	other := viaNode(c, a.d)
	other.pin = strings.Repeat("ab", 32)
	data := other.v2("inbox", nil)
	status, body := replyOf(t, a.d, data)
	v := verifiedAs(t, "unknown pin", expectFor(t, data, nodePin), status, body, signed.SignerNode, "signature_refused")
	if !bytes.Contains(v.Text, []byte(`"reason":"target"`)) {
		t.Fatalf("unknown pin: %s", v.Text)
	}
	// outcome_unknown: the answer did not come after the envelope was sent.
	a.d.link.mu.Lock()
	a.d.link.hook = func(point string) error {
		if point == "maintainer-after-write" {
			return errors.New("synthetic lost reply")
		}
		return nil
	}
	a.d.link.mu.Unlock()
	data = f.v2("send", map[string]any{"to": agent.Name, "body": "a synthetic lost v2 send"})
	status, body = replyOf(t, a.d, data)
	verifiedAs(t, "lost reply", expectFor(t, data, nodePin), status, body, signed.SignerNode, "outcome_unknown")
	a.d.link.mu.Lock()
	a.d.link.hook = nil
	a.d.link.mu.Unlock()
	// computer_offline: the node cannot reach the target.
	n.mu.Lock()
	delete(n.hosts, b.ip+":47672")
	n.mu.Unlock()
	data = f.v2("send", map[string]any{"to": agent.Name, "body": "a synthetic offline v2 send"})
	status, body = replyOf(t, a.d, data)
	verifiedAs(t, "offline", expectFor(t, data, nodePin), status, body, signed.SignerNode, "computer_offline")
	// A node claim does not verify as the target's answer, nor for another node.
	if _, err := signed.VerifyAnswer(body, signed.Expect{Request: expectFor(t, data, nodePin).Request, Target: c.pin, Node: c.pin}); err == nil {
		t.Fatal("a node claim verified for another node pin")
	}
	// too_large: the node's own refusal of a read that does not fit is signed by the target role
	// on its own pin, and by the node for a paired pin.
	for name, cl := range map[string]*signedClient{"own": newSignedClient(t, a.d, 63), "paired": f} {
		data := cl.v2("inbox", nil)
		status, body := a.d.signedReply(data, nil, ErrSignedTooLarge)
		signer := signed.SignerTarget
		if name == "paired" {
			signer = signed.SignerNode
		}
		verifiedAs(t, name+" too_large", expectFor(t, data, nodePin), status, body, signer, "too_large")
	}
}

// The answers that come before a request is bound stay unsigned: an envelope that does not parse
// as version 2, and a version 1 envelope.
func TestSignedAnswersUnsigned(t *testing.T) {
	d := startTestDaemonFor(t)
	c := newSignedClient(t, d, 64)
	for name, data := range map[string][]byte{
		"v1":         c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil),
		"not parsed": []byte(`{"v":"koinon-maintainer-2"}`),
		"not JSON":   []byte(`not json`),
	} {
		_, body := replyOf(t, d, data)
		if _, err := signed.ParseAnswer(body); err == nil {
			t.Fatalf("%s: signed: %s", name, body)
		}
	}
}

// A target answer whose text has quotes, backslashes, control characters and non-ASCII text
// goes through the link and the node and still verifies, on the node's routes too.
func TestSignedAnswersEscapedText(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, b.d.store, "codex", "synthetic-answer-escape", "")
	text := "\"quoted\" \\back\\slash\\ \x01\x1f\t\n <tag> & é ж 漢 \U0001F600  "
	if _, err := b.d.store.Send(ctx, Key{"codex", agent.ID}, "maintainer", text); err != nil {
		t.Fatal(err)
	}
	c := newSignedClient(t, b.d, 65)
	f := viaNode(c, a.d)
	nodePin := linkPinOf(t, a.d)
	check := func(name string, data []byte, status int, body []byte) {
		t.Helper()
		v := verifiedAs(t, name, expectFor(t, data, nodePin), status, body, signed.SignerTarget, "")
		var page struct {
			Result Inbox `json:"result"`
		}
		if err := json.Unmarshal(v.Text, &page); err != nil || len(page.Result.Messages) != 1 || page.Result.Messages[0].Body != text {
			t.Fatalf("%s: %v %s", name, err, v.Text)
		}
	}
	data := f.v2("inbox", nil)
	status, body := replyOf(t, a.d, data)
	check("node", data, status, body)

	// The loopback route of the node.
	bearer := linkBearer(t, a)
	session, _ := newSignedSessionToken(t, a.d, bearer, newSignedClient(t, a.d, 66))
	data = f.v2("inbox", nil)
	status, body = loopbackRaw(t, a.d, bearer, session, data, "application/json")
	check("loopback route", data, status, body)

	// The Serve route of the node.
	token := remoteSession(t, a.d, newSignedClient(t, a.d, 67))
	data = f.v2("inbox", nil)
	status, body = mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
	check("Serve route", data, status, body)

	// A refusal on the Serve route is the signed object with its status.
	data = f.v2("decision_get", map[string]any{"reference": "AN-404"})
	status, body = mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
	verifiedAs(t, "Serve refusal", expectFor(t, data, nodePin), status, body, signed.SignerTarget, "not_found")

	// The daemon client that the signed MCP tool uses passes a signed refusal on whole.
	data = f.v2("decision_get", map[string]any{"reference": "AN-404"})
	raw, err := CallEnvelope(ctx, a.d.Addresses()[0], bearer, "/v1/maintainer/signed", data, session, SignedCallTimeout)
	if err != nil {
		t.Fatalf("signed refusal over the daemon client: %v", err)
	}
	verifiedAs(t, "daemon client refusal", expectFor(t, data, nodePin), 409, raw, signed.SignerTarget, "not_found")
}

// A version 2 session on the Serve route answers the flat token object signed by the node as
// its target; the loopback session route answers it unsigned (contract.md, section 2.5).
func TestSignedAnswersSession(t *testing.T) {
	h := newLinkHost(t, &linkNet{}, "alpha", "100.64.0.1")
	d := h.d
	c := newSignedClient(t, d, 68)
	data := c.v2("session", map[string]any{"client": "voice"})
	status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed/session", jsonType, data)
	v := verifiedAs(t, "Serve session", expectFor(t, data, linkPinOf(t, d)), status, body, signed.SignerTarget, "")
	var session struct {
		Token  string `json:"token"`
		Client string `json:"client"`
	}
	if err := json.Unmarshal(v.Text, &session); err != nil || session.Token == "" || session.Client != "key:voice" {
		t.Fatalf("Serve session: %s", v.Text)
	}
	// The token of the signed answer opens the action route.
	inbox := c.v2("inbox", nil)
	status, body = mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(session.Token), inbox)
	verifiedAs(t, "Serve inbox", expectFor(t, inbox, linkPinOf(t, d)), status, body, signed.SignerTarget, "")
	// A refused session on the Serve route is signed too.
	stale := c.envelope("session", map[string]any{"client": "voice"}, c.request(), time.Now().Unix()-3600, map[string]any{"v": signed.Version2})
	status, body = mustRemote(t, d, "POST", "/v1/maintainer/signed/session", jsonType, stale)
	verifiedAs(t, "Serve stale session", expectFor(t, stale, linkPinOf(t, d)), status, body, signed.SignerTarget, "signature_refused")

	bearer := linkBearer(t, h)
	status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed/session", "", c.v2("session", map[string]any{"client": "voice"}))
	if token, _ := reply["token"].(string); status != 200 || token == "" || reply["v"] != nil {
		t.Fatalf("loopback session: %d %v", status, reply)
	}
}

// A version 1 and a version 2 envelope with the same request ID conflict; a version 2 outcome
// reads the receipt of a version 1 request.
func TestSignedAnswersVersionsShareReceipts(t *testing.T) {
	d := startTestDaemonFor(t)
	agent := join(t, d.store, "codex", "synthetic-answer-receipt", "")
	c := newSignedClient(t, d, 69)
	pin := linkPinOf(t, d)
	now := time.Now().Unix()
	request := c.request()
	args := map[string]any{"to": agent.Name, "body": "a synthetic v1 send"}
	if _, err := d.SignedAction(context.Background(), c.envelope("send", args, request, now, nil)); err != nil {
		t.Fatal(err)
	}
	data := c.envelope("send", args, request, now, map[string]any{"v": signed.Version2})
	status, body := replyOf(t, d, data)
	verifiedAs(t, "same request as v2", expectFor(t, data, pin), status, body, signed.SignerTarget, "request_conflict")
	data = c.v2("outcome", map[string]any{"request": request, "created": now})
	status, body = replyOf(t, d, data)
	v := verifiedAs(t, "v2 outcome of v1", expectFor(t, data, pin), status, body, signed.SignerTarget, "")
	if !bytes.Contains(v.Text, []byte(`"state":"recorded"`)) {
		t.Fatalf("v2 outcome of v1: %s", v.Text)
	}
	// And the reverse: a v2 request, then the same ID as v1.
	request = c.request()
	if status, body := replyOf(t, d, c.envelope("send", args, request, now, map[string]any{"v": signed.Version2})); status != 200 {
		t.Fatalf("v2 send: %d %s", status, body)
	}
	_, err := d.SignedAction(context.Background(), c.envelope("send", args, request, now, nil))
	wantCode(t, "same request as v1", err, "request_conflict", "")
}

// The node's link check (validReply): for a version 2 envelope it takes the target's signed
// answer with the same status, and an earlier target's unsigned version refusal unchanged; it
// takes nothing else.
func TestSignedAnswersLinkCheck(t *testing.T) {
	target := newSignedClient(t, startTestDaemonFor(t), 70)
	id, err := target.d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	request := strings.Repeat("ab", 32)
	signedBody := func(signer string, status int, answer string) json.RawMessage {
		a, err := signed.SignAnswer(signer, id.Certificate.PrivateKey.(ed25519.PrivateKey), request, status, []byte(answer))
		if err != nil {
			t.Fatal(err)
		}
		return mustJSON(a)
	}
	frame := func(status int, answer json.RawMessage) link.Frame {
		f, err := link.NewFrame("maintainer-ok", maintainerReply{Status: status, Answer: answer})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	earlier := json.RawMessage(`{"ok":false,"code":"signature_refused","error":"x","details":{"reason":"version"}}`)
	for name, c := range map[string]struct {
		f  link.Frame
		ok bool
	}{
		"target success":        {frame(200, signedBody(signed.SignerTarget, 200, `{"ok":true,"result":{}}`)), true},
		"target refusal":        {frame(404, signedBody(signed.SignerTarget, 404, `{"ok":false,"code":"not_found"}`)), true},
		"status differs":        {frame(200, signedBody(signed.SignerTarget, 404, `{"ok":false,"code":"not_found"}`)), false},
		"node signed":           {frame(409, signedBody(signed.SignerNode, 409, `{"ok":false,"code":"computer_offline"}`)), false},
		"earlier version":       {frame(400, earlier), true},
		"unsigned success":      {frame(200, json.RawMessage(`{"ok":true,"result":{}}`)), false},
		"unsigned other reason": {frame(400, json.RawMessage(`{"ok":false,"code":"signature_refused","details":{"reason":"signature"}}`)), false},
	} {
		answer, ok := validReply(c.f, signed.Version2)
		if ok != c.ok {
			t.Fatalf("%s: %v", name, ok)
		}
		var reply maintainerReply
		c.f.Decode(&reply)
		if ok && string(answer.body) != string(reply.Answer) {
			t.Fatalf("%s: bytes changed: %s", name, answer.body)
		}
	}
	// A v1 envelope takes the v1 form only.
	if _, ok := validReply(frame(200, signedBody(signed.SignerTarget, 200, `{"ok":true,"result":{}}`)), signed.Version); ok {
		t.Fatal("v1 took a signed answer")
	}
	// The node passes the earlier target's refusal on unchanged and unsigned.
	data := target.v2("inbox", nil)
	status, body := target.d.signedReply(data, forwardedAnswer{status: 400, body: earlier}, nil)
	if status != 400 || string(body) != string(earlier) {
		t.Fatalf("earlier target through the node: %d %s", status, body)
	}
}

// Sizes: a read near the link limit, of text that JSON escapes, reads in pages whose signed
// answers fit the frame; the largest room post result fits with room to spare.
func TestSignedAnswersSizes(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	s := b.d.store
	agent := join(t, s, "codex", "synthetic-answer-size", "")
	c := newSignedClient(t, b.d, 71)
	f := viaNode(c, a.d)
	nodePin := linkPinOf(t, a.d)
	text := strings.Repeat("\x01", maxBody)
	for i := 0; i < 3; i++ {
		if _, err := s.Send(ctx, Key{"codex", agent.ID}, "maintainer", text); err != nil {
			t.Fatal(err)
		}
	}
	seen, after, pages := 0, int64(0), 0
	for {
		data := f.v2("inbox", map[string]any{"after": after, "limit": int64(100)})
		status, body := replyOf(t, a.d, data)
		v := verifiedAs(t, "page", expectFor(t, data, nodePin), status, body, signed.SignerTarget, "")
		encoded, err := json.Marshal(link.Frame{Op: "maintainer-ok", Body: mustJSON(maintainerReply{Status: status, Answer: body})})
		if err != nil || len(encoded) > link.MaxMaintainerFrameBytes {
			t.Fatalf("page frame: %d bytes, %v", len(encoded), err)
		}
		var page struct {
			Result Inbox `json:"result"`
		}
		if err := json.Unmarshal(v.Text, &page); err != nil || len(page.Result.Messages) == 0 {
			t.Fatalf("page: %v, %d bytes", err, len(body))
		}
		pages++
		seen += len(page.Result.Messages)
		after = page.Result.Messages[len(page.Result.Messages)-1].Seq
		if !page.Result.More {
			break
		}
	}
	if seen != 3 || pages < 2 {
		t.Fatalf("messages read: %d in %d pages", seen, pages)
	}

	// The largest room post result: 32 members with the longest labels, each escaped to its
	// longest JSON spelling.
	recipients := make([]RoomRecipient, maxRoomMembers)
	for i := range recipients {
		recipients[i] = RoomRecipient{Name: strings.Repeat("\x01", 256), State: "not_sent", Reason: "no_liveness_evidence"}
	}
	post := maintainerPost{RoomID: strings.Repeat("f", 64), Seq: 1 << 62, Queued: maxRoomMembers, Skipped: maxRoomMembers, Duplicate: true, Recipients: &recipients}
	limit := signedAnswerLimit(context.WithValue(ctx, forwardedKey{}, "pin"))
	if size := signedAnswerSize(signed.Version2, post); size > limit/4 {
		t.Fatalf("largest room post answer: %d bytes, limit %d", size, limit)
	}
}

// A session signed for a paired computer's pin is the node's invalid_request on the session
// routes, signed by the node on the Serve route for version 2; an unknown pin is the node's
// target refusal.
func TestSignedAnswersSessionForPairedPin(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	nodePin := linkPinOf(t, a.d)
	f := viaNode(newSignedClient(t, b.d, 74), a.d)
	unknown := viaNode(f, a.d)
	unknown.pin = strings.Repeat("ab", 32)
	session := map[string]any{"client": "voice"}
	data := f.v2("session", session)
	status, body := mustRemote(t, a.d, "POST", "/v1/maintainer/signed/session", jsonType, data)
	verifiedAs(t, "Serve v2 paired", expectFor(t, data, nodePin), status, body, signed.SignerNode, "invalid_request")
	data = unknown.v2("session", session)
	status, body = mustRemote(t, a.d, "POST", "/v1/maintainer/signed/session", jsonType, data)
	v := verifiedAs(t, "Serve v2 unknown", expectFor(t, data, nodePin), status, body, signed.SignerNode, "signature_refused")
	if !bytes.Contains(v.Text, []byte(`"reason":"target"`)) {
		t.Fatalf("Serve v2 unknown: %s", v.Text)
	}
	status, body = mustRemote(t, a.d, "POST", "/v1/maintainer/signed/session", jsonType, f.envelope("session", session, f.request(), time.Now().Unix(), nil))
	wantAnswer(t, "Serve v1 paired", status, string(bytes.TrimSpace(body)), 400, "invalid_request")
	bearer := linkBearer(t, a)
	status, reply := postEnvelope(t, a.d, bearer, "/v1/maintainer/signed/session", "", f.v2("session", session))
	if status != 400 || reply["code"] != "invalid_request" {
		t.Fatalf("loopback v2 paired: %d %v", status, reply)
	}
}

// A target of an earlier release refuses a version 2 envelope with its unsigned version
// refusal; a node of this release passes it on unchanged on each transport, and the client
// does not verify it.
func TestSignedAnswersEarlierTarget(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	f := viaNode(newSignedClient(t, b.d, 75), a.d)
	b.d.link.mu.Lock()
	b.d.link.hook = func(point string) error {
		if point == "maintainer-earlier-release" {
			return errors.New("synthetic earlier release")
		}
		return nil
	}
	b.d.link.mu.Unlock()
	nodePin := linkPinOf(t, a.d)
	token := remoteSession(t, a.d, newSignedClient(t, a.d, 76))
	for name, send := range map[string]func([]byte) (int, []byte){
		"handler": func(data []byte) (int, []byte) { return replyOf(t, a.d, data) },
		"Serve route": func(data []byte) (int, []byte) {
			return mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
		},
	} {
		data := f.v2("inbox", nil)
		status, body := send(data)
		m := wantAnswer(t, name, status, string(bytes.TrimSpace(body)), 409, "signature_refused")
		if m["details"].(map[string]any)["reason"] != "version" {
			t.Fatalf("%s: %s", name, body)
		}
		if _, err := signed.VerifyAnswer(body, expectFor(t, data, nodePin)); !errors.Is(err, signed.ErrNotVerified) {
			t.Fatalf("%s: verified: %v", name, err)
		}
	}
	// The daemon client of the signed MCP tool gives it as the refusal that it is.
	bearer := linkBearer(t, a)
	session, _ := newSignedSessionToken(t, a.d, bearer, newSignedClient(t, a.d, 77))
	_, err := CallEnvelope(context.Background(), a.d.Addresses()[0], bearer, "/v1/maintainer/signed", f.v2("inbox", nil), session, SignedCallTimeout)
	var refused RefusedError
	if !errors.As(err, &refused) || refused.Code != "signature_refused" {
		t.Fatalf("daemon client: %v", err)
	}
	// A version 1 envelope still runs on that target.
	if status, body := replyOf(t, a.d, f.envelope("inbox", nil, f.request(), time.Now().Unix(), nil)); status != 200 {
		t.Fatalf("v1 on the earlier target: %d %s", status, body)
	}
}
