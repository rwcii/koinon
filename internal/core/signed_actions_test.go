package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/signed"
)

// Signed maintainer actions on the node's own pin (#693 chunk 1; decision.md Decisions 2, 3,
// 4 and 7). Keys, sessions, items and rooms are synthetic.

// signedClient signs envelopes for one authorization on a test daemon.
type signedClient struct {
	t       *testing.T
	d       *Daemon
	pin     string
	private ed25519.PrivateKey
	key     string
	auth    string
	n       atomic.Int64
}

// newSignedClient enrolls the seeded key on d and returns its client.
func newSignedClient(t *testing.T, d *Daemon, seed byte) *signedClient {
	t.Helper()
	id, err := d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	en, private := enrollment(t, seed, id.Pin, time.Now().Unix())
	if _, err := d.store.AddMaintainerKey(context.Background(), en, "test"); err != nil {
		t.Fatal(err)
	}
	return &signedClient{t: t, d: d, pin: id.Pin, private: private, key: en.KeyID, auth: en.Authorization}
}

// envelope signs op with args under request at created; fields overrides any envelope field.
func (c *signedClient) envelope(op string, args map[string]any, request string, created int64, fields map[string]any) []byte {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	e := map[string]any{"v": signed.Version, "key": c.key, "auth": c.auth, "target": c.pin, "op": op, "args": args,
		"request": request, "created": created}
	for k, v := range fields {
		e[k] = v
	}
	data, err := signed.Sign(e, c.private)
	if err != nil {
		c.t.Fatal(err)
	}
	return data
}

// request gives a new request ID.
func (c *signedClient) request() string { return fmt.Sprintf("test-request-%06d", c.n.Add(1)) }

// do signs and runs op now under a new request ID.
func (c *signedClient) do(op string, args map[string]any) (any, error) {
	return c.d.SignedAction(context.Background(), c.envelope(op, args, c.request(), time.Now().Unix(), nil))
}

// must runs op and returns its result as JSON values.
func (c *signedClient) must(op string, args map[string]any) map[string]any {
	c.t.Helper()
	result, err := c.do(op, args)
	if err != nil {
		c.t.Fatalf("%s %v: %v", op, args, err)
	}
	return jsonMap(c.t, result)
}

func asJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustSigned(t *testing.T, c *signedClient, op string, args map[string]any) any {
	t.Helper()
	result, err := c.do(op, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	return result
}

func jsonMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// wantCode checks a refusal's code and, for signature_refused, its reason.
func wantCode(t *testing.T, name string, err error, code, reason string) {
	t.Helper()
	if got := errorCode(err); err == nil || got != code {
		t.Fatalf("%s: %v, want %s", name, err, code)
	}
	if reason != "" {
		var work WorkRefusal
		if !errors.As(err, &work) || work.Details["reason"] != reason {
			t.Fatalf("%s: %v, want reason %s", name, err, reason)
		}
	}
}

// snapshotOf is the digest that a client computes from decision_get.
func snapshotOf(t *testing.T, c *signedClient, reference string) (string, int64) {
	t.Helper()
	item := c.must("decision_get", map[string]any{"reference": reference})
	commands := []string{}
	for _, v := range item["commands"].([]any) {
		commands = append(commands, v.(string))
	}
	revision := int64(item["revision"].(float64))
	return signed.SnapshotDigest(reference, revision, item["kind"].(string), item["title"].(string), item["where"].(string),
		item["context"].(string), item["recommendation"].(string), item["agent_text"].(string), commands), revision
}

// Decision 3.3 items 1 and 2, and item 4 for a read: each check refuses with its reason.
func TestSignedActionChecks(t *testing.T) {
	d, _ := startTestDaemon(t)
	c := newSignedClient(t, d, 1)
	now := time.Now().Unix()
	run := func(data []byte) error {
		_, err := d.SignedAction(context.Background(), data)
		return err
	}
	if inbox := c.must("inbox", nil); inbox["last_seq"] != float64(0) {
		t.Fatalf("inbox: %v", inbox)
	}
	good := c.envelope("inbox", map[string]any{"limit": int64(10)}, c.request(), now, nil)
	wantCode(t, "changed argument", run([]byte(strings.Replace(string(good), `"limit":10`, `"limit":11`, 1))), "signature_refused", "signature")
	wantCode(t, "unknown field", run([]byte(strings.Replace(string(good), `"v":`, `"x":1,"v":`, 1))), "signature_refused", "version")
	wantCode(t, "other pin", run(c.envelope("inbox", nil, c.request(), now, map[string]any{"target": strings.Repeat("cd", 32)})), "invalid_request", "")
	wantCode(t, "old read", run(c.envelope("inbox", nil, c.request(), now-signedWindow-5, nil)), "signature_refused", "time")
	wantCode(t, "future read", run(c.envelope("inbox", nil, c.request(), now+signedWindow+5, nil)), "signature_refused", "time")
	wantCode(t, "unknown op", run(c.envelope("room_create", nil, c.request(), now, nil)), "invalid_request", "")
	wantCode(t, "unknown argument", run(c.envelope("inbox", map[string]any{"cursor": "x"}, c.request(), now, nil)), "invalid_request", "")
	wantCode(t, "wrong argument type", run(c.envelope("inbox", map[string]any{"after": "1"}, c.request(), now, nil)), "invalid_request", "")

	// An unknown key, and a known key under another authorization.
	stranger := newSignedClient(t, startTestDaemonFor(t), 2)
	stranger.pin = c.pin
	wantCode(t, "unknown key", run(stranger.envelope("inbox", nil, stranger.request(), now, nil)), "signature_refused", "key_unknown")
	old := c.auth
	en, _ := enrollment(t, 1, c.pin, now+1)
	if _, err := d.store.AddMaintainerKey(context.Background(), en, "test"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "authorization changed", run(c.envelope("inbox", nil, c.request(), now, nil)), "signature_refused", "authorization_changed")
	c.auth = en.Authorization
	c.must("inbox", nil)
	if _, err := d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "removed key", run(c.envelope("inbox", nil, c.request(), now, nil)), "signature_refused", "key_removed")
	c.auth = old
	wantCode(t, "removed key, old authorization", run(c.envelope("inbox", nil, c.request(), now, nil)), "signature_refused", "key_removed")
}

// startTestDaemonFor is another test daemon, for a key that the first one never added.
func startTestDaemonFor(t *testing.T) *Daemon {
	d, _ := startTestDaemon(t)
	return d
}

// Decision 4: a write runs once per (authorization, request); the same envelope answers the
// first result, also after a restart; another envelope with that request is refused; outcome
// reads the receipt; the verdict records its signer (Decision 7).
func TestSignedWriteOnce(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-signed-a", namedRepo(t, "signedrepo"))
	aKey := Key{"codex", a.ID}
	mustCreate(t, s, aKey, "S-1", DecisionFields{Title: ptr("Ship it"), Kind: ptr("decision"), Context: ptr("- one"),
		Commands: &[]string{"koinon version"}, Recipients: &[]string{a.Name}})
	c := newSignedClient(t, d, 3)
	snapshot, revision := snapshotOf(t, c, "S-1")
	now := time.Now().Unix()

	// Refusals of the operation's own checks are receipts too.
	stale := c.envelope("decision_verdict", map[string]any{"reference": "S-1", "revision": revision + 1, "snapshot": snapshot,
		"verdict": "approved", "note": ""}, "stale-request-0001", now, nil)
	_, err := d.SignedAction(context.Background(), stale)
	wantCode(t, "stale", err, "stale_revision", "")
	outcome := c.must("outcome", map[string]any{"request": "stale-request-0001", "created": now})
	if outcome["state"] != "refused" || outcome["code"] != "stale_revision" || outcome["details"].(map[string]any)["current_revision"] != float64(revision) {
		t.Fatalf("stale outcome: %v", outcome)
	}
	wrong := c.envelope("decision_verdict", map[string]any{"reference": "S-1", "revision": revision, "snapshot": strings.Repeat("0", 64),
		"verdict": "approved"}, "snapshot-request-01", now, nil)
	_, err = d.SignedAction(context.Background(), wrong)
	wantCode(t, "snapshot", err, "snapshot_changed", "")

	verdict := c.envelope("decision_verdict", map[string]any{"reference": "S-1", "revision": revision, "snapshot": snapshot,
		"verdict": "approved", "note": "Go."}, "verdict-request-001", now, nil)
	first, err := d.SignedAction(context.Background(), verdict)
	if err != nil {
		t.Fatal(err)
	}
	v := jsonMap(t, first)
	if v["verdict"] != "approved" || v["via"] != "key:voice" || v["note"] != "Go." || v["snapshot"].(map[string]any)["title"] != "Ship it" ||
		len(v["deliveries"].([]any)) == 0 {
		t.Fatalf("verdict: %v", v)
	}
	// The repeat answers the whole first answer (review K3-F1).
	again, err := d.SignedAction(context.Background(), verdict)
	if err != nil || string(asJSON(t, again)) != string(asJSON(t, first)) {
		t.Fatalf("repeat: %s %v, first %s", asJSON(t, again), err, asJSON(t, first))
	}
	conflict := c.envelope("decision_verdict", map[string]any{"reference": "S-1", "revision": revision, "snapshot": snapshot,
		"verdict": "declined"}, "verdict-request-001", now, nil)
	_, err = d.SignedAction(context.Background(), conflict)
	wantCode(t, "conflict", err, "request_conflict", "")
	var user, key, client, via string
	if err := s.db.QueryRow(`SELECT signer_user,signer_key,signer_client,via FROM decision_verdicts`).Scan(&user, &key, &client, &via); err != nil {
		t.Fatal(err)
	}
	if user != "maintainer" || key != c.key || client != "voice" || via != "key:voice" {
		t.Fatalf("signer: %s %s %s %s", user, key, client, via)
	}
	var audit string
	if err := s.db.QueryRow(`SELECT target FROM audit WHERE action='decision-approve' AND result='accepted'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(audit, " by maintainer (voice) key "+c.key) {
		t.Fatalf("audit: %s", audit)
	}

	// A lost answer and a restart: the same envelope answers the first result once more.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c.d = d
	after, err := d.SignedAction(context.Background(), verdict)
	if err != nil || string(asJSON(t, after)) != string(asJSON(t, first)) {
		t.Fatalf("after restart: %s %v", asJSON(t, after), err)
	}
	var verdicts int
	d.store.db.QueryRow(`SELECT COUNT(*) FROM decision_verdicts`).Scan(&verdicts)
	if verdicts != 1 {
		t.Fatalf("verdicts: %d", verdicts)
	}
	outcome = c.must("outcome", map[string]any{"request": "verdict-request-001", "created": now})
	if outcome["state"] != "recorded" || fmt.Sprint(outcome["result"]) != fmt.Sprint(v) {
		t.Fatalf("outcome: %v", outcome)
	}
	unknown := c.must("outcome", map[string]any{"request": "never-sent-request-1", "created": now})
	if unknown["state"] != "unknown_request" || unknown["now"].(float64) < float64(now) {
		t.Fatalf("unknown outcome: %v", unknown)
	}
}

// The other writes run once too: ack, send and room_post.
func TestSignedWritesRunOnce(t *testing.T) {
	d, _ := startTestDaemon(t)
	s, ctx := d.store, context.Background()
	a := join(t, s, "codex", "synthetic-signed-b", namedRepo(t, "signedrepob"))
	room, err := s.CreateRoom(ctx, "signed-room-key", "signed-room")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoomMember(ctx, room, a.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Key{"codex", a.ID}, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	c := newSignedClient(t, d, 4)
	now := time.Now().Unix()
	for _, w := range []struct {
		op   string
		args map[string]any
	}{
		{"send", map[string]any{"to": a.Name, "body": "a synthetic instruction"}},
		{"room_post", map[string]any{"room": room, "body": "a synthetic post"}},
		{"ack", map[string]any{"through": int64(1)}},
	} {
		request := c.request()
		data := c.envelope(w.op, w.args, request, now, nil)
		first, err := d.SignedAction(ctx, data)
		if err != nil {
			t.Fatalf("%s: %v", w.op, err)
		}
		// The repeat, also while writes are blocked, and outcome give the first answer whole
		// (review K3-F1, K3-F2).
		s.storage.mu.Lock()
		s.storage.blocked = "synthetic write admission block"
		s.storage.mu.Unlock()
		again, err := d.SignedAction(ctx, data)
		_, blocked := c.do(w.op, w.args)
		s.storage.mu.Lock()
		s.storage.blocked = ""
		s.storage.mu.Unlock()
		if err != nil || string(asJSON(t, first)) != string(asJSON(t, again)) {
			t.Fatalf("%s repeat: %s %v, first %s", w.op, asJSON(t, again), err, asJSON(t, first))
		}
		if !errors.Is(blocked, ErrStorageBlocked) {
			t.Fatalf("%s: a new write while blocked: %v", w.op, blocked)
		}
		outcome := c.must("outcome", map[string]any{"request": request, "created": now})
		if outcome["state"] != "recorded" || fmt.Sprint(outcome["result"]) != fmt.Sprint(jsonMap(t, first)) {
			t.Fatalf("%s outcome: %v, first %s", w.op, outcome, asJSON(t, first))
		}
	}
	if acked := jsonMap(t, mustSigned(t, c, "ack", map[string]any{"through": int64(1)})); acked["acked_through"] != float64(1) {
		t.Fatalf("ack answer: %v", acked)
	}
	var messages, posts int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body='a synthetic instruction'`).Scan(&messages)
	s.db.QueryRow(`SELECT COUNT(*) FROM room_posts WHERE body='a synthetic post'`).Scan(&posts)
	if messages != 1 || posts != 1 {
		t.Fatalf("messages %d posts %d", messages, posts)
	}
	var via string
	s.db.QueryRow(`SELECT ack_via FROM messages WHERE body='a synthetic report'`).Scan(&via)
	if via != "key:voice" {
		t.Fatalf("ack via: %q", via)
	}
	var audits int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE result='accepted' AND target LIKE '% by maintainer (voice) key %'`).Scan(&audits)
	if audits != 4 { // send, room_post, ack and the second ack
		t.Fatalf("audits: %d", audits)
	}
}

// Decision 3.3 item 3: a write's time check runs in its transaction, after the receipt lookup.
// An old write does not run and outcome then reports it unknown; a recorded write answers its
// receipt after its window.
func TestSignedWriteTime(t *testing.T) {
	d, _ := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-signed-c", "")
	c := newSignedClient(t, d, 5)
	now := time.Now().Unix()
	old := c.envelope("send", map[string]any{"to": a.Name, "body": "too late"}, "old-write-request-1", now-signedWindow-2, nil)
	_, err := d.SignedAction(context.Background(), old)
	wantCode(t, "old write", err, "signature_refused", "time")
	if o := c.must("outcome", map[string]any{"request": "old-write-request-1", "created": now - signedWindow - 2}); o["state"] != "unknown_request" {
		t.Fatalf("outcome: %v", o)
	}
	// Recorded near the end of its window, answered from its receipt after it.
	edge := c.envelope("send", map[string]any{"to": a.Name, "body": "just in time"}, "edge-write-request-1", time.Now().Unix()-signedWindow+1, nil)
	first, err := d.SignedAction(context.Background(), edge)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	again, err := d.SignedAction(context.Background(), edge)
	if err != nil || fmt.Sprint(jsonMap(t, first)) != fmt.Sprint(jsonMap(t, again)) {
		t.Fatalf("after the window: %v %v", again, err)
	}
	var messages int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body IN ('too late','just in time')`).Scan(&messages)
	if messages != 1 {
		t.Fatalf("messages: %d", messages)
	}
}

// A removal that commits after the envelope's checks and before its write's transaction
// refuses the write in that transaction (Decision 3.3; #693 review risk 5).
func TestSignedRemovalBeforeWrite(t *testing.T) {
	d, _ := startTestDaemon(t)
	c := newSignedClient(t, d, 6)
	key, err := currentAuthorization(context.Background(), d.store.db, c.key)
	if err != nil {
		t.Fatal(err)
	}
	a := &signedAction{key: key, op: "ack", request: "removal-request-01", digest: strings.Repeat("0", 64), created: time.Now().Unix()}
	ctx := context.WithValue(context.Background(), signedContextKey{}, a)
	if _, err := d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	_, err = d.store.ack(ctx, maintainerKey, 0, true)
	wantCode(t, "removed while waiting", err, "signature_refused", "key_removed")
	var receipts int
	d.store.db.QueryRow(`SELECT COUNT(*) FROM signed_receipts`).Scan(&receipts)
	if receipts != 0 {
		t.Fatalf("receipts: %d", receipts)
	}
}

// Decision 4.3: outcome reads in a transaction, so it waits for a write that holds the store
// and then sees its receipt.
func TestSignedOutcomeWaitsForWrite(t *testing.T) {
	d, _ := startTestDaemon(t)
	c := newSignedClient(t, d, 7)
	now := time.Now().Unix()
	tx, err := d.store.begin(context.Background(), ordinary)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]any, 1)
	go func() {
		result, err := d.SignedAction(context.Background(), c.envelope("outcome", map[string]any{"request": "held-write-request", "created": now}, c.request(), now, nil))
		if err != nil {
			t.Error(err)
		}
		done <- jsonMap(t, result)
	}()
	select {
	case o := <-done:
		t.Fatalf("outcome did not wait: %v", o)
	case <-time.After(300 * time.Millisecond):
	}
	ms := time.Now().UnixMilli()
	if _, err := tx.Exec(`INSERT INTO signed_receipts(authorization,request,op,digest,state,code,result,created_at,expires_at)
		VALUES(?,?,'ack',?,'recorded','','{"acked_through":0}',?,?)`, c.auth, "held-write-request", strings.Repeat("0", 64), ms, ms+signedReceiptWindow.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o["state"] != "recorded" {
		t.Fatalf("outcome: %v", o)
	}
}

// Decision 4.3: after the key is added again, an outcome under the original authorization is
// refused (authorization_changed); the new authorization finds no receipt of the old one.
func TestSignedOutcomeAfterReenroll(t *testing.T) {
	d, _ := startTestDaemon(t)
	a := join(t, d.store, "codex", "synthetic-signed-d", "")
	c := newSignedClient(t, d, 8)
	now := time.Now().Unix()
	if _, err := d.SignedAction(context.Background(), c.envelope("send", map[string]any{"to": a.Name, "body": "before"}, "reenroll-request-01", now, nil)); err != nil {
		t.Fatal(err)
	}
	old := c.auth
	en, _ := enrollment(t, 8, c.pin, now+1)
	if _, err := d.store.AddMaintainerKey(context.Background(), en, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := d.SignedAction(context.Background(), c.envelope("outcome", map[string]any{"request": "reenroll-request-01", "created": now}, c.request(), now, nil))
	wantCode(t, "original authorization", err, "signature_refused", "authorization_changed")
	c.auth = en.Authorization
	if o := c.must("outcome", map[string]any{"request": "reenroll-request-01", "created": now}); o["state"] != "unknown_request" {
		t.Fatalf("new authorization: %v", o)
	}
	c.auth = old
}

// An outcome checks its authorization inside its lookup transaction: a key added again after
// the envelope's checks refuses the old authorization's outcome (review K3-F3).
func TestSignedOutcomeAuthorizationInTransaction(t *testing.T) {
	d, _ := startTestDaemon(t)
	a := join(t, d.store, "codex", "synthetic-signed-e", "")
	c := newSignedClient(t, d, 9)
	now := time.Now().Unix()
	if _, err := d.SignedAction(context.Background(), c.envelope("send", map[string]any{"to": a.Name, "body": "x"}, "race-request-00001", now, nil)); err != nil {
		t.Fatal(err)
	}
	key, err := currentAuthorization(context.Background(), d.store.db, c.key)
	if err != nil {
		t.Fatal(err)
	}
	en, _ := enrollment(t, 9, c.pin, now+1)
	if _, err := d.store.AddMaintainerKey(context.Background(), en, "test"); err != nil {
		t.Fatal(err)
	}
	_, err = d.store.signedOutcome(context.Background(), key, "race-request-00001")
	wantCode(t, "outcome after the checks", err, "signature_refused", "authorization_changed")
	if _, err := d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	_, err = d.store.signedOutcome(context.Background(), key, "race-request-00001")
	wantCode(t, "outcome after removal", err, "signature_refused", "key_removed")
}

// postEnvelope posts raw envelope bytes to a signed route with the daemon's bearer and, when
// set, a session token.
func postEnvelope(t *testing.T, d *Daemon, bearer, path, token string, body []byte) (int, map[string]any) {
	t.Helper()
	r, err := http.NewRequest("POST", "http://"+d.Addresses()[0]+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set(MaintainerTokenHeader, token)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var reply map[string]any
	json.NewDecoder(response.Body).Decode(&reply)
	return response.StatusCode, reply
}

// The signed session routes (Decision 5, Answer shapes): the session answers flat as the
// secret's session route does; the action route checks the token before it reads the
// envelope; a secret change leaves a signed session, and the key's removal ends it.
func TestSignedSessionRoutes(t *testing.T) {
	d, root, bearer := maintainerDaemon(t)
	c := newSignedClient(t, d, 10)
	now := time.Now().Unix()
	status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed/session", "", c.envelope("session", map[string]any{"client": "voice"}, c.request(), now, nil))
	token, _ := reply["token"].(string)
	if status != 200 || len(token) != 64 || reply["client"] != "key:voice" || reply["idle_seconds"] != float64(86400) || reply["absolute_seconds"] != float64(604800) {
		t.Fatalf("session: %d %v", status, reply)
	}
	for name, body := range map[string][]byte{
		"read as session": c.envelope("inbox", nil, c.request(), now, nil),
		"old session":     c.envelope("session", map[string]any{"client": "voice"}, c.request(), now-signedWindow-5, nil),
		"bad client":      c.envelope("session", map[string]any{"client": "Voice!"}, c.request(), now, nil),
	} {
		if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed/session", "", body); status == 200 {
			t.Fatalf("%s: %v", name, reply)
		}
	}
	// The token is checked first: an unknown token is refused whatever the body is.
	if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", strings.Repeat("0", 64), []byte("not json")); status != 409 || reply["code"] != "maintainer_token_expired" {
		t.Fatalf("unknown token: %d %v", status, reply)
	}
	// A secret token is no signed token, and a signed token is no secret token.
	secretToken := newMaintainerToken(t, d, bearer, "test-client")
	if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", secretToken, c.envelope("inbox", nil, c.request(), now, nil)); status != 409 || reply["code"] != "maintainer_token_expired" {
		t.Fatalf("secret token on the signed route: %d %v", status, reply)
	}
	refusedMaintainer(t, d, bearer, token, "inbox", nil, "maintainer_token_expired")
	if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", token, c.envelope("inbox", nil, c.request(), now, nil)); status != 200 || reply["ok"] != true {
		t.Fatalf("signed inbox: %d %v", status, reply)
	}
	if _, err := d.changeSecret(context.Background(), "generate", ""); err != nil {
		t.Fatal(err)
	}
	if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", token, c.envelope("inbox", nil, c.request(), now, nil)); status != 200 {
		t.Fatalf("after a secret change: %d %v", status, reply)
	}
	if _, err := d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	if status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", token, c.envelope("inbox", nil, c.request(), now, nil)); status != 409 || reply["code"] != "maintainer_token_expired" {
		t.Fatalf("after removal: %d %v", status, reply)
	}
	_ = root
}

// computers and changes on the node (Decisions 2.4, 6.2, 6.3): the node and its pair with its
// connection state; changes answers at once without versions, waits with them, and names the
// inbox, a room or computers when they move. Two in-process daemons with real TLS.
func TestSignedComputersAndChanges(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	pairHosts(t, b, a)
	c := newSignedClient(t, a.d, 12)
	bID, err := b.d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for {
		view = c.must("computers", nil)
		computers := view["computers"].([]any)
		if len(computers) == 1 && computers[0].(map[string]any)["state"] == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("computers: %v", view)
		}
		time.Sleep(200 * time.Millisecond)
	}
	node := view["node"].(map[string]any)
	beta := view["computers"].([]any)[0].(map[string]any)
	if node["pin"] != c.pin || node["name"] != "alpha" || beta["pin"] != bID.Pin || beta["name"] != "beta" || beta["since"].(float64) <= 0 {
		t.Fatalf("computers: %v", view)
	}

	first := c.must("changes", nil)
	versions := first["versions"].(map[string]any)
	for _, topic := range []string{"inbox", "decisions", "computers"} {
		if v, _ := versions[topic].(string); len(v) != 20 {
			t.Fatalf("versions: %v", first)
		}
	}
	if len(first["changed"].([]any)) != 0 {
		t.Fatalf("first changes: %v", first)
	}
	started := time.Now()
	if quiet := c.must("changes", map[string]any{"versions": versions, "wait_seconds": int64(1)}); len(quiet["changed"].([]any)) != 0 || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("quiet wait: %v", quiet)
	}
	wait := func(change func()) []any {
		done := make(chan map[string]any, 1)
		go func() {
			result, err := c.do("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)})
			if err != nil {
				t.Error(err)
			}
			done <- jsonMap(t, result)
		}()
		time.Sleep(300 * time.Millisecond)
		change()
		select {
		case answer := <-done:
			versions = answer["versions"].(map[string]any)
			return answer["changed"].([]any)
		case <-time.After(15 * time.Second):
			t.Fatal("the wait did not answer the change")
		}
		return nil
	}
	agent := join(t, a.d.store, "codex", "synthetic-signed-f", "")
	if changed := wait(func() {
		if _, err := a.d.store.Send(context.Background(), Key{"codex", agent.ID}, "maintainer", "a synthetic report"); err != nil {
			t.Error(err)
		}
	}); fmt.Sprint(changed) != "[inbox]" {
		t.Fatalf("inbox change: %v", changed)
	}
	if changed := wait(func() {
		if err := a.d.linkRename("beta", "gamma"); err != nil {
			t.Error(err)
		}
	}); fmt.Sprint(changed) != "[computers]" {
		t.Fatalf("computers change: %v", changed)
	}
	if renamed := c.must("computers", nil)["computers"].([]any)[0].(map[string]any); renamed["name"] != "gamma" {
		t.Fatalf("renamed: %v", renamed)
	}
	// The computers version is stored: a daemon that starts again from the same store gives
	// the same version for the same view (review K6-F1).
	a.d.computers.mu.Lock()
	a.d.computers.loaded, a.d.computers.saved = false, computersSaved{}
	a.d.computers.mu.Unlock()
	if same := c.must("changes", map[string]any{"versions": versions, "wait_seconds": int64(1)}); len(same["changed"].([]any)) != 0 ||
		same["versions"].(map[string]any)["computers"] != versions["computers"] {
		t.Fatalf("after a restart of the topic: %v, held %v", same, versions)
	}
	for name, args := range map[string]map[string]any{
		"long wait":       {"wait_seconds": int64(31)},
		"short version":   {"versions": map[string]any{"inbox": "1"}},
		"versions string": {"versions": "inbox"},
	} {
		if _, err := c.do("changes", args); errorCode(err) != "invalid_request" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A removal ends a waiting changes request.
	done := make(chan error, 1)
	go func() {
		_, err := c.do("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := a.d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		wantCode(t, "removed during a wait", err, "signature_refused", "key_removed")
	case <-time.After(10 * time.Second):
		t.Fatal("the wait outlived the key")
	}
}

// A signed verdict shows its signer: the answer, the maintainer's history and the dashboard
// say USER (CLIENT), also after the key's removal; an agent's history does not (Decision 7).
func TestSignedVerdictIdentity(t *testing.T) {
	d, root, _ := maintainerDaemon(t)
	s, ctx := d.store, context.Background()
	a := join(t, s, "codex", "synthetic-signed-g", "")
	aKey := Key{"codex", a.ID}
	mustCreate(t, s, aKey, "ID-1", DecisionFields{Title: ptr("Synthetic identity"), Kind: ptr("decision"), Recipients: &[]string{a.Name}})
	c := newSignedClient(t, d, 13)
	snapshot, revision := snapshotOf(t, c, "ID-1")
	v := c.must("decision_verdict", map[string]any{"reference": "ID-1", "revision": revision, "snapshot": snapshot, "verdict": "approved"})
	signer, _ := v["signer"].(map[string]any)
	if signer["user"] != "maintainer" || signer["client"] != "voice" || signer["key_id"] != c.key || v["via"] != "key:voice" {
		t.Fatalf("answer: %v", v)
	}
	if _, err := s.RemoveMaintainerKey(ctx, c.key); err != nil {
		t.Fatal(err)
	}
	page, err := s.maintainerHistory(ctx, "ID-1", 0, 0)
	if err != nil || len(page.Verdicts) != 1 || page.Verdicts[0].Signer == nil || *page.Verdicts[0].Signer != (VerdictSigner{"maintainer", "voice", c.key}) ||
		page.Verdicts[0].Via != "key:voice" {
		t.Fatalf("history: %+v %v", page, err)
	}
	agent, err := s.DecisionHistory(ctx, aKey, "ID-1", 0, 0, false)
	if err != nil || len(agent.Verdicts) != 1 || agent.Verdicts[0].Signer != nil || agent.Verdicts[0].Via != "" {
		t.Fatalf("agent history: %+v %v", agent, err)
	}
	client := newActionClient(t, d, root)
	view := dashboardDo(t, d, "GET", "/dashboard/decisions?ref=ID-1", client.cookie, nil, nil)
	if view.status != 200 || !strings.Contains(view.body, ", by maintainer (voice), via key:voice") {
		t.Fatalf("board: %d", view.status)
	}
}

// A signed read's answer fits 1 MiB as the route writes it: an inbox page of bodies that JSON
// escapes sixfold reads fewer messages and keeps its continuation (review K5-F1).
func TestSignedAnswerBound(t *testing.T) {
	d, _ := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-signed-h", "")
	body := strings.Repeat("\x01", maxBody)
	for i := 0; i < 3; i++ {
		if _, err := s.Send(context.Background(), Key{"codex", a.ID}, "maintainer", body); err != nil {
			t.Fatal(err)
		}
	}
	c := newSignedClient(t, d, 14)
	first, err := c.do("inbox", map[string]any{"limit": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	page := first.(Inbox)
	if size := answerBytes(first); size > maxSignedAnswer || !page.More || len(page.Messages) == 0 || len(page.Messages) >= 3 {
		t.Fatalf("first page: %d bytes, %d messages, more %v", size, len(page.Messages), page.More)
	}
	seen := len(page.Messages)
	for page.More {
		next, err := c.do("inbox", map[string]any{"after": page.Messages[len(page.Messages)-1].Seq, "limit": int64(100)})
		if err != nil {
			t.Fatal(err)
		}
		if answerBytes(next) > maxSignedAnswer {
			t.Fatalf("page: %d bytes", answerBytes(next))
		}
		page = next.(Inbox)
		seen += len(page.Messages)
	}
	if seen != 3 {
		t.Fatalf("messages read: %d", seen)
	}
}

// A session's login checks the authorization in its own transaction: a removal that commits
// after the envelope's checks refuses the login and writes no accepted record (review K5-F2).
func TestSignedLoginAfterRemoval(t *testing.T) {
	d, _ := startTestDaemon(t)
	c := newSignedClient(t, d, 15)
	key, err := currentAuthorization(context.Background(), d.store.db, c.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.RemoveMaintainerKey(context.Background(), c.key); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "login after removal", d.store.signedLogin(context.Background(), key, time.Now().Unix()), "signature_refused", "key_removed")
	wantCode(t, "old login", d.store.signedLogin(context.Background(), key, time.Now().Unix()-signedWindow-5), "signature_refused", "key_removed")
	var logins int
	d.store.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action='maintainer_login'`).Scan(&logins)
	if logins != 0 {
		t.Fatalf("logins: %d", logins)
	}
}

// room_list pages with more and next_after, and a refusal's text is bounded, so that a refusal
// that names a long field or operation fits an answer too (review K5-F1 follow-up).
func TestSignedRoomPagesAndRefusalBound(t *testing.T) {
	d, root, bearer := maintainerDaemon(t)
	for i := 0; i < 3; i++ {
		if _, err := d.store.CreateRoom(context.Background(), fmt.Sprintf("room-key-%d", i), fmt.Sprintf("room-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	c := newSignedClient(t, d, 16)
	first := c.must("room_list", map[string]any{"limit": int64(2)})
	if len(first["rooms"].([]any)) != 2 || first["more"] != true || first["next_after"] != float64(2) {
		t.Fatalf("first page: %v", first)
	}
	last := c.must("room_list", map[string]any{"after": int64(2), "limit": int64(2)})
	if len(last["rooms"].([]any)) != 1 || last["more"] != false || last["next_after"] != nil {
		t.Fatalf("last page: %v", last)
	}
	now := time.Now().Unix()
	long := c.envelope(strings.Repeat("<", 200000), nil, c.request(), now, nil)
	_, err := d.SignedAction(context.Background(), long)
	var refusal Refusal
	if !errors.As(err, &refusal) || refusal.Code != "invalid_request" || len(refusal.Message) > MaxRefusalMessage {
		t.Fatalf("long op: %v", err)
	}
	session, _ := newSignedSessionToken(t, d, bearer, c)
	status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed", session, long)
	if data, _ := json.Marshal(reply); status != 400 || len(data) > maxSignedAnswer || reply["code"] != "invalid_request" {
		t.Fatalf("route: %d %d bytes", status, len(data))
	}
	_ = root
}

// newSignedSessionToken opens a signed session for c over the route.
func newSignedSessionToken(t *testing.T, d *Daemon, bearer string, c *signedClient) (string, map[string]any) {
	t.Helper()
	status, reply := postEnvelope(t, d, bearer, "/v1/maintainer/signed/session", "", c.envelope("session", map[string]any{"client": "voice"}, c.request(), time.Now().Unix(), nil))
	token, _ := reply["token"].(string)
	if status != 200 || token == "" {
		t.Fatalf("session: %d %v", status, reply)
	}
	return token, reply
}
