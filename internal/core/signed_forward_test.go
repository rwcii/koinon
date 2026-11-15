package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/signed"
)

// Signed actions for another computer (#693 chunk 2, criteria C2-1 to C2-8). All names,
// addresses and keys are synthetic.

// viaNode signs with c's key for c's computer, and runs the envelopes on node.
func viaNode(c *signedClient, node *Daemon) *signedClient {
	f := &signedClient{t: c.t, d: node, pin: c.pin, private: c.private, key: c.key, auth: c.auth}
	f.n.Store(500000)
	return f
}

// answerOf runs data on d and returns the answer as d's signed route writes it.
func answerOf(t *testing.T, d *Daemon, ctx context.Context, data []byte) (int, string) {
	t.Helper()
	result, err := d.SignedAction(ctx, data)
	if answer, ok := result.(forwardedAnswer); ok && err == nil {
		return answer.status, string(answer.body)
	}
	w := &answerWriter{header: http.Header{}}
	maintainerRead(w, result, err)
	return w.status, string(trimNewline(w.body))
}

// decoded is a route answer's JSON object.
func decoded(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("answer %q: %v", body, err)
	}
	return m
}

// forwardMust runs op through f's node and returns the target's result.
func forwardMust(t *testing.T, f *signedClient, op string, args map[string]any) map[string]any {
	t.Helper()
	status, body := answerOf(t, f.d, context.Background(), f.envelope(op, args, f.request(), time.Now().Unix(), nil))
	m := decoded(t, body)
	result, _ := m["result"].(map[string]any)
	if status != 200 || m["ok"] != true || result == nil {
		t.Fatalf("%s through the node: %d %s", op, status, body)
	}
	return result
}

// wantAnswer checks an answer's status and code.
func wantAnswer(t *testing.T, name string, status int, body string, wantStatus int, code string) map[string]any {
	t.Helper()
	m := decoded(t, body)
	if status != wantStatus || m["code"] != code {
		t.Fatalf("%s: %d %s, want %d %s", name, status, body, wantStatus, code)
	}
	return m
}

func rowCount(t *testing.T, d *Daemon, query string, args ...any) int {
	t.Helper()
	var n int
	if err := d.store.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// C2-1, C2-2, C2-4, C2-5: A forwards an envelope for B's pin unchanged, and B's answer comes
// back unchanged; B runs it with its own key and records its signer; A holds no key and runs
// nothing.
func TestSignedForwardRunsOnTarget(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	s := b.d.store
	agent := join(t, s, "codex", "synthetic-forward-a", "")
	aKey := Key{"codex", agent.ID}
	room, err := s.CreateRoom(ctx, "forward-room-key", "forward-room")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoomMember(ctx, room, agent.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, aKey, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, aKey, "FW-1", DecisionFields{Title: ptr("Synthetic forward"), Kind: ptr("decision"), Recipients: &[]string{agent.Name}})
	c := newSignedClient(t, b.d, 21)
	f := viaNode(c, a.d)
	if n := rowCount(t, a.d, `SELECT COUNT(*) FROM maintainer_authorizations`); n != 0 {
		t.Fatalf("keys on the node: %d", n)
	}
	now := time.Now().Unix()

	// The same read, through A and on B: the same status and the same bytes.
	for _, op := range []struct {
		op   string
		args map[string]any
	}{
		{"inbox", nil},
		{"board_list", nil},
		{"decision_get", map[string]any{"reference": "FW-1"}},
		{"room_list", nil},
		{"room_read", map[string]any{"room": room}},
		{"decision_get", map[string]any{"reference": "FW-404"}},
	} {
		data := c.envelope(op.op, op.args, c.request(), now, nil)
		fs, fb := answerOf(t, a.d, ctx, data)
		ls, lb := answerOf(t, b.d, ctx, data)
		if fs != ls || fb != lb {
			t.Fatalf("%s: through A %d %s, on B %d %s", op.op, fs, fb, ls, lb)
		}
	}

	// The writes run on B, once, with B's key as the signer.
	snapshot, revision := snapshotOf(t, c, "FW-1")
	v := forwardMust(t, f, "decision_verdict", map[string]any{"reference": "FW-1", "revision": revision, "snapshot": snapshot, "verdict": "approved"})
	if signer, _ := v["signer"].(map[string]any); signer["key_id"] != c.key || signer["client"] != "voice" || v["via"] != "key:voice" {
		t.Fatalf("verdict: %v", v)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM decision_verdicts WHERE signer_key=?`, c.key); n != 1 {
		t.Fatalf("verdicts on B: %d", n)
	}
	if n := rowCount(t, a.d, `SELECT COUNT(*) FROM decision_verdicts`); n != 0 {
		t.Fatalf("verdicts on A: %d", n)
	}
	history := forwardMust(t, f, "decision_history", map[string]any{"reference": "FW-1"})
	verdicts, _ := history["verdicts"].([]any)
	if len(verdicts) != 1 || verdicts[0].(map[string]any)["signer"].(map[string]any)["key_id"] != c.key {
		t.Fatalf("history: %v", history)
	}
	forwardMust(t, f, "send", map[string]any{"to": agent.Name, "body": "a synthetic instruction"})
	forwardMust(t, f, "room_post", map[string]any{"room": room, "body": "a synthetic post"})
	if acked := forwardMust(t, f, "ack", map[string]any{"through": int64(1)}); acked["acked_through"] != float64(1) {
		t.Fatalf("ack: %v", acked)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic instruction'`); n != 1 {
		t.Fatalf("messages on B: %d", n)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM room_posts WHERE body='a synthetic post'`); n != 1 {
		t.Fatalf("posts on B: %d", n)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic report' AND ack_via='key:voice'`); n != 1 {
		t.Fatalf("ack on B: %d", n)
	}
	if n := rowCount(t, a.d, `SELECT COUNT(*) FROM signed_receipts`); n != 0 {
		t.Fatalf("receipts on A: %d", n)
	}

	// The node's own operations are not forwarded; B refuses computers as forwarded too.
	status, body := answerOf(t, a.d, ctx, f.envelope("session", map[string]any{"client": "voice"}, f.request(), now, nil))
	wantAnswer(t, "session for B", status, body, 400, "invalid_request")
	status, body = answerOf(t, a.d, ctx, f.envelope("computers", nil, f.request(), now, nil))
	wantAnswer(t, "computers for B", status, body, 400, "invalid_request")
	status, body = answerOf(t, b.d, context.WithValue(ctx, forwardedKey{}, pinOf(t, b)), c.envelope("computers", nil, c.request(), now, nil))
	wantAnswer(t, "forwarded computers on B", status, body, 400, "invalid_request")

	// An envelope for a third pin: A has no such pair, and B runs only its own pin.
	other := map[string]any{"target": strings.Repeat("cd", 32)}
	status, body = answerOf(t, a.d, ctx, c.envelope("inbox", nil, c.request(), now, other))
	if m := wantAnswer(t, "third pin on A", status, body, 409, "signature_refused"); m["details"].(map[string]any)["reason"] != "target" {
		t.Fatalf("third pin on A: %s", body)
	}
	_, err = b.d.SignedAction(context.WithValue(ctx, forwardedKey{}, pinOf(t, b)), c.envelope("inbox", nil, c.request(), now, other))
	wantCode(t, "third pin forwarded to B", err, "signature_refused", "target")
	// The target comes before the node's own operations (review C2-F3).
	for _, op := range []string{"session", "computers"} {
		status, body = answerOf(t, a.d, ctx, c.envelope(op, map[string]any{}, c.request(), now, other))
		if m := wantAnswer(t, op+" for a third pin", status, body, 409, "signature_refused"); m["details"].(map[string]any)["reason"] != "target" {
			t.Fatalf("%s for a third pin: %s", op, body)
		}
	}

	// A forwarded changes has no computers topic; B's own has one.
	fc := forwardMust(t, f, "changes", nil)
	if _, ok := fc["versions"].(map[string]any)["computers"]; ok {
		t.Fatalf("forwarded changes: %v", fc)
	}
	if _, ok := c.must("changes", nil)["versions"].(map[string]any)["computers"]; !ok {
		t.Fatal("B's own changes has no computers topic")
	}
}

// C2-5: peers and peer_status for another computer give the link directory's fields only:
// no session ID, directory, process or credential.
func TestSignedForwardPeersRemoteSafe(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	repo := linkRepo(t, "forward-sentinel-repo")
	agent := joinAs(t, b.d.store, "codex", "synthetic-forward-sentinel-id", repo, "")
	c := newSignedClient(t, b.d, 22)
	f := viaNode(c, a.d)
	peers := forwardMust(t, f, "peers", nil)
	status := forwardMust(t, f, "peer_status", map[string]any{"peer": agent.Name})
	for name, v := range map[string]any{"peers": peers, "peer_status": status} {
		data, _ := json.Marshal(v)
		text := string(data)
		for _, private := range []string{agent.ID, repo, "pid", "host", "secret", "token", "directory"} {
			if strings.Contains(text, private) {
				t.Fatalf("%s holds %q: %s", name, private, text)
			}
		}
		if !strings.Contains(text, agent.Name) {
			t.Fatalf("%s has no %s: %s", name, agent.Name, text)
		}
	}
	// B's own answer holds the repository path that the forwarded one must not.
	if local, _ := json.Marshal(c.must("peers", nil)); !strings.Contains(string(local), repo) {
		t.Fatalf("the sentinel is not in B's own peers answer: %s", local)
	}
}

// C2-4, C2-6: an answer lost after the write leaves the outcome unknown; outcome then reads the
// recorded write, and the same envelope again answers it without a second write.
func TestSignedForwardLostReply(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, b.d.store, "codex", "synthetic-forward-lost", "")
	c := newSignedClient(t, b.d, 23)
	f := viaNode(c, a.d)
	now := time.Now().Unix()
	request := f.request()
	data := f.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic lost reply"}, request, now, nil)
	a.d.link.mu.Lock()
	a.d.link.hook = func(point string) error {
		if point == "maintainer-after-write" {
			return errors.New("synthetic lost reply")
		}
		return nil
	}
	a.d.link.mu.Unlock()
	status, body := answerOf(t, a.d, ctx, data)
	m := wantAnswer(t, "lost reply", status, body, 409, "outcome_unknown")
	if d := m["details"].(map[string]any); d["request"] != request || d["target"] != c.pin {
		t.Fatalf("lost reply details: %s", body)
	}
	a.d.link.mu.Lock()
	a.d.link.hook = nil
	a.d.link.mu.Unlock()
	// The write ran on B: the forwarded outcome reads it.
	deadline := time.Now().Add(10 * time.Second)
	var outcome map[string]any
	for {
		outcome = forwardMust(t, f, "outcome", map[string]any{"request": request, "created": now})
		if outcome["state"] == "recorded" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if outcome["state"] != "recorded" {
		t.Fatalf("outcome: %v", outcome)
	}
	again, againBody := answerOf(t, a.d, ctx, data)
	if again != 200 || decoded(t, againBody)["ok"] != true {
		t.Fatalf("resend: %d %s", again, againBody)
	}
	if got := asJSON(t, decoded(t, againBody)["result"]); string(got) != string(asJSON(t, outcome["result"])) {
		t.Fatalf("resend %s, outcome %v", got, outcome["result"])
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic lost reply'`); n != 1 {
		t.Fatalf("messages on B: %d", n)
	}
}

// C2-4: a target that the node cannot reach is computer_offline, with the last connected time,
// and nothing runs; a node with Remote off says why.
func TestSignedForwardOffline(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, b.d.store, "codex", "synthetic-forward-offline", "")
	c := newSignedClient(t, b.d, 24)
	f := viaNode(c, a.d)
	now := time.Now().Unix()
	n.mu.Lock()
	delete(n.hosts, b.ip+":47672")
	n.mu.Unlock()
	// A later failed ping keeps the time of the last connected one.
	p, _ := pairWith(t, a, b)
	a.d.observe(a.d.openGen(), p.ID, "offline", "synthetic")
	request := f.request()
	status, body := answerOf(t, a.d, ctx, f.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic offline send"}, request, now, nil))
	m := wantAnswer(t, "unreachable", status, body, 409, "computer_offline")
	details := m["details"].(map[string]any)
	if seen, _ := details["last_seen"].(float64); seen <= 0 || details["target"] != c.pin {
		t.Fatalf("unreachable details: %s", body)
	}
	if outcome := c.must("outcome", map[string]any{"request": request, "created": now}); outcome["state"] != "unknown_request" {
		t.Fatalf("outcome on B: %v", outcome)
	}
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	status, body = answerOf(t, a.d, ctx, f.envelope("inbox", nil, f.request(), now, nil))
	m = wantAnswer(t, "Remote off", status, body, 409, "computer_offline")
	if m["details"].(map[string]any)["reason"] != ErrLinkRemoteOff.Code {
		t.Fatalf("Remote off details: %s", body)
	}
}

// C2-7: B runs a forwarded write only while the pair and the link generation stand.
func TestSignedForwardTargetChecksPair(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	agent := join(t, b.d.store, "codex", "synthetic-forward-pair", "")
	c := newSignedClient(t, b.d, 25)
	frame, err := link.NewFrame("maintainer", maintainerFrame{Envelope: base64Of(c.envelope("send",
		map[string]any{"to": agent.Name, "body": "a synthetic unpaired send"}, c.request(), time.Now().Unix(), nil))})
	if err != nil {
		t.Fatal(err)
	}
	gen := b.d.openGen()
	if _, _, err := b.d.answerMaintainer(strings.Repeat("ef", 32), frame, gen); !errors.Is(err, ErrLinkNoPair) {
		t.Fatalf("unknown pin: %v", err)
	}
	if _, _, err := b.d.answerMaintainer(pinOf(t, a), frame, gen+1); !errors.Is(err, ErrLinkRemoteOff) {
		t.Fatalf("old generation: %v", err)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic unpaired send'`); n != 0 {
		t.Fatalf("messages: %d", n)
	}
	// The generation closes, or the pair goes, while the write waits for the link lock.
	hook := func(change func()) {
		b.d.link.mu.Lock()
		b.d.link.hook = func(point string) error {
			if point == "maintainer-before-write" {
				change()
			}
			return nil
		}
		b.d.link.mu.Unlock()
	}
	hook(func() {
		b.d.link.mu.Lock()
		b.d.link.gen++
		b.d.link.mu.Unlock()
	})
	_, _, err = b.d.answerMaintainer(pinOf(t, a), frame, gen)
	b.d.link.mu.Lock()
	b.d.link.gen--
	b.d.link.hook = nil
	b.d.link.mu.Unlock()
	if !errors.Is(err, ErrLinkRemoteOff) {
		t.Fatalf("generation closed during the write: %v", err)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic unpaired send'`); n != 0 {
		t.Fatalf("messages: %d", n)
	}
	if _, _, err := b.d.answerMaintainer(pinOf(t, a), frame, gen); err != nil {
		t.Fatalf("paired: %v", err)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic unpaired send'`); n != 1 {
		t.Fatalf("messages: %d", n)
	}
	second, err := link.NewFrame("maintainer", maintainerFrame{Envelope: base64Of(c.envelope("send",
		map[string]any{"to": agent.Name, "body": "a synthetic second send"}, c.request(), time.Now().Unix(), nil))})
	if err != nil {
		t.Fatal(err)
	}
	hook(func() {
		b.d.link.mu.Lock()
		s, err := link.ReadState(b.root)
		if err == nil {
			s.Pairs = nil
			err = link.WriteState(b.root, s)
		}
		b.d.link.mu.Unlock()
		if err != nil {
			t.Error(err)
		}
	})
	if _, _, err := b.d.answerMaintainer(pinOf(t, a), second, gen); !errors.Is(err, ErrLinkNoPair) {
		t.Fatalf("unpaired during the write: %v", err)
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic second send'`); n != 0 {
		t.Fatalf("messages: %d", n)
	}
}

// C2-3: a forwarded answer at the target's bound fits a maintainer-ok frame; one byte more
// would not, so the target pages it first.
func TestSignedForwardAnswerFitsFrame(t *testing.T) {
	limit := signedAnswerLimit(context.WithValue(context.Background(), forwardedKey{}, "pin"))
	frameOf := func(body int) int {
		answer := json.RawMessage(`"` + strings.Repeat("a", body-2) + `"`)
		data, err := json.Marshal(link.Frame{Op: "maintainer-ok", Body: mustJSON(maintainerReply{Status: 599, Answer: answer})})
		if err != nil {
			t.Fatal(err)
		}
		return len(data)
	}
	// respond's answer of limit bytes ends in a newline, which the frame does not carry.
	if n := frameOf(limit - 1); n != link.MaxMaintainerFrameBytes {
		t.Fatalf("frame at the bound: %d bytes", n)
	}
	if signedAnswerLimit(context.Background()) != maxSignedAnswer {
		t.Fatal("the node's own bound changed")
	}
}

// C2-3, C2-8: a 512 KiB envelope of characters that JSON escapes reaches the target, which
// answers its own refusal; a large inbox reads over the link in pages that fit a frame.
func TestSignedForwardSizes(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	s := b.d.store
	agent := join(t, s, "codex", "synthetic-forward-size", "")
	c := newSignedClient(t, b.d, 26)
	f := viaNode(c, a.d)
	now := time.Now().Unix()
	small := len(f.envelope("inbox", map[string]any{"pad": ""}, "size-request-000001", now, nil))
	pad := strings.Repeat("<", signed.MaxEnvelopeBytes-small)
	big := f.envelope("inbox", map[string]any{"pad": pad}, "size-request-000001", now, nil)
	if len(big) != signed.MaxEnvelopeBytes {
		t.Fatalf("envelope: %d bytes", len(big))
	}
	status, body := answerOf(t, a.d, ctx, big)
	wantAnswer(t, "512 KiB envelope", status, body, 400, "invalid_request")

	text := strings.Repeat("\x01", maxBody)
	for i := 0; i < 3; i++ {
		if _, err := s.Send(ctx, Key{"codex", agent.ID}, "maintainer", text); err != nil {
			t.Fatal(err)
		}
	}
	seen, after := 0, int64(0)
	for {
		status, body := answerOf(t, a.d, ctx, f.envelope("inbox", map[string]any{"after": after, "limit": int64(100)}, f.request(), time.Now().Unix(), nil))
		if status != 200 || len(body) > link.MaxMaintainerFrameBytes {
			t.Fatalf("page: %d, %d bytes", status, len(body))
		}
		var page struct {
			Result Inbox `json:"result"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Result.Messages) == 0 {
			t.Fatalf("empty page: %d bytes", len(body))
		}
		seen += len(page.Result.Messages)
		after = page.Result.Messages[len(page.Result.Messages)-1].Seq
		if !page.Result.More {
			break
		}
	}
	if seen != 3 {
		t.Fatalf("messages read: %d", seen)
	}
}

// C2-4: a changes wait with nothing changed holds over the link and answers its quiet result.
func TestSignedForwardQuietWait(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	c := newSignedClient(t, b.d, 27)
	f := viaNode(c, a.d)
	versions := forwardMust(t, f, "changes", nil)["versions"]
	started := time.Now()
	quiet := forwardMust(t, f, "changes", map[string]any{"versions": versions, "wait_seconds": int64(20)})
	if elapsed := time.Since(started); len(quiet["changed"].([]any)) != 0 || elapsed < 19*time.Second {
		t.Fatalf("quiet wait after %v: %v", elapsed, quiet)
	}
	// A request that ends ends its forwarded wait (review C2-F4).
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started = time.Now()
	status, body := answerOf(t, a.d, ctx, f.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(20)}, f.request(), time.Now().Unix(), nil))
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled wait answered after %v: %d %s", elapsed, status, body)
	}
	wantAnswer(t, "cancelled wait", status, body, 409, "outcome_unknown")
}

// C2-F2: a maintainer-ok reply that is not a route's answer leaves the outcome unknown.
func TestSignedForwardReplyShape(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		ok   bool
	}{
		"success":        {`{"status":200,"answer":{"ok":true,"result":{"a":1}}}`, true},
		"refusal":        {`{"status":409,"answer":{"ok":false,"code":"decided","error":"x"}}`, true},
		"null answer":    {`{"status":200,"answer":null}`, false},
		"array answer":   {`{"status":200,"answer":[]}`, false},
		"string answer":  {`{"status":200,"answer":"ok"}`, false},
		"no ok":          {`{"status":200,"answer":{"result":{}}}`, false},
		"ok not boolean": {`{"status":200,"answer":{"ok":"yes"}}`, false},
		"ok and refusal": {`{"status":409,"answer":{"ok":true,"result":{}}}`, false},
		"not ok and 200": {`{"status":200,"answer":{"ok":false,"code":"x"}}`, false},
		"no status":      {`{"answer":{"ok":true,"result":{}}}`, false},
		"string status":  {`{"status":"200","answer":{"ok":true,"result":{}}}`, false},
		"no result":      {`{"status":200,"answer":{"ok":true}}`, false},
		"no code":        {`{"status":409,"answer":{"ok":false}}`, false},
		"number code":    {`{"status":409,"answer":{"ok":false,"code":123}}`, false},
		"empty code":     {`{"status":409,"answer":{"ok":false,"code":""}}`, false},
		"code, no error": {`{"status":500,"answer":{"ok":false,"code":"storage_error"}}`, true},
		"extra fields":   {`{"status":200,"answer":{"ok":true,"result":[1],"more":1}}`, true},
		"status 600":     {`{"status":600,"answer":{"ok":false,"code":"x"}}`, false},
	} {
		answer, ok := validReply(link.Frame{Op: "maintainer-ok", Body: json.RawMessage(c.body)})
		if ok != c.ok {
			t.Fatalf("%s: %v", name, ok)
		}
		if ok && !strings.Contains(c.body, string(answer.body)) {
			t.Fatalf("%s: bytes changed: %s", name, answer.body)
		}
	}
	if _, ok := validReply(link.Frame{Op: "error", Body: json.RawMessage(`{"status":200,"answer":{"ok":true}}`)}); ok {
		t.Fatal("another operation")
	}
}

// C2-F1: an unpair that completes while the node connects admits no action: the node sends
// nothing and B, which still trusts A, writes nothing.
func TestSignedForwardUnpairDuringConnect(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, b.d.store, "codex", "synthetic-forward-unpair", "")
	c := newSignedClient(t, b.d, 30)
	f := viaNode(c, a.d)
	a.d.link.mu.Lock()
	dial := a.d.link.dial
	stage := 0
	a.d.link.dial = func(ctx context.Context, address string) (net.Conn, error) {
		switch stage {
		case 0:
			stage = 1
			if err := a.d.linkUnpair(ctx, "beta"); err != nil {
				t.Error(err)
			}
			stage = 2
		case 1:
			// The unpair's notice does not reach B.
			return nil, errors.New("synthetic: notice lost")
		}
		return dial(ctx, address)
	}
	a.d.link.mu.Unlock()
	status, body := answerOf(t, a.d, ctx, f.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic send after unpair"}, f.request(), time.Now().Unix(), nil))
	if m := wantAnswer(t, "unpaired while connecting", status, body, 409, "signature_refused"); m["details"].(map[string]any)["reason"] != "target" {
		t.Fatalf("unpaired while connecting: %s", body)
	}
	if stage != 2 {
		t.Fatalf("stage %d", stage)
	}
	if _, ok := pairWith(t, b, a); !ok {
		t.Fatal("B no longer has the pair: the test needs B to trust A")
	}
	if n := rowCount(t, b.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic send after unpair'`); n != 0 {
		t.Fatalf("messages on B: %d", n)
	}
}

// C2-8: the whole path: a client opens a session on A with A's own key, and sends an envelope
// signed by B's key for B over A's signed route; the answer is B's own.
func TestSignedForwardRoute(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	join(t, b.d.store, "codex", "synthetic-forward-route", "")
	bearer := linkBearer(t, a)
	node := newSignedClient(t, a.d, 28)
	session, _ := newSignedSessionToken(t, a.d, bearer, node)
	c := newSignedClient(t, b.d, 29)
	data := c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)
	status, reply := postEnvelope(t, a.d, bearer, "/v1/maintainer/signed", session, data)
	ls, lb := answerOf(t, b.d, context.Background(), data)
	if got, _ := json.Marshal(reply); status != ls || string(got) != string(asJSON(t, decoded(t, lb))) {
		t.Fatalf("route: %d %s, on B %d %s", status, got, ls, lb)
	}
}

func base64Of(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// #693 Acceptance 6: a computer that is off answers computer_offline at once. Its Serve port
// can take the TCP connection with no TLS answer behind it; the connect bound covers every
// address together. A computer that the node last saw offline, or that answers slowly within
// the bound, still runs the action.
func TestSignedForwardOfflineAtOnce(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	c := newSignedClient(t, b.d, 47)
	f := viaNode(c, a.d)
	p, _ := pairWith(t, a, b)
	setDial := func(dial func(context.Context, string) (net.Conn, error)) {
		a.d.link.mu.Lock()
		a.d.link.dial = dial
		a.d.link.mu.Unlock()
	}
	// The computer was last seen offline and is back: the action runs.
	a.d.observe(a.d.openGen(), p.ID, "offline", "synthetic")
	status, body := answerOf(t, a.d, context.Background(), f.envelope("board_list", nil, f.request(), time.Now().Unix(), nil))
	if status != 200 {
		t.Fatalf("back after offline: %d %s", status, body)
	}
	// A handshake that comes late but within the bound runs the action.
	setDial(func(ctx context.Context, address string) (net.Conn, error) {
		select {
		case <-time.After(forwardConnect / 2):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return n.dial(ctx, address)
	})
	status, body = answerOf(t, a.d, context.Background(), f.envelope("board_list", nil, f.request(), time.Now().Unix(), nil))
	if status != 200 {
		t.Fatalf("slow handshake: %d %s", status, body)
	}
	// Two saved addresses; each takes the TCP connection and never answers the handshake.
	a.d.link.mu.Lock()
	s, err := link.ReadState(a.root)
	if err == nil {
		i := slices.IndexFunc(s.Pairs, func(q link.Pair) bool { return q.ID == p.ID })
		s.Pairs[i].IPs = []string{b.ip, "100.64.0.9"}
		err = link.WriteState(a.root, s)
	}
	a.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var tried []string
	setDial(func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		tried = append(tried, address)
		mu.Unlock()
		client, server := net.Pipe()
		go func() { defer server.Close(); io.Copy(io.Discard, server) }()
		return client, nil
	})
	for _, observed := range []string{"connected", "offline"} {
		a.d.observe(a.d.openGen(), p.ID, observed, "synthetic")
		mu.Lock()
		tried = nil
		mu.Unlock()
		request, now := f.request(), time.Now().Unix()
		started := time.Now()
		status, body := answerOf(t, a.d, context.Background(), f.envelope("send",
			map[string]any{"to": "nobody", "body": "a synthetic silent send"}, request, now, nil))
		elapsed := time.Since(started)
		wantAnswer(t, "silent TLS, "+observed, status, body, 409, "computer_offline")
		if elapsed > 4*time.Second {
			t.Fatalf("observed %s: computer_offline after %s", observed, elapsed)
		}
		mu.Lock()
		if len(tried) == 0 || tried[0] != b.ip+":47672" {
			t.Errorf("observed %s: dials %v", observed, tried)
		}
		mu.Unlock()
		if outcome := c.must("outcome", map[string]any{"request": request, "created": now}); outcome["state"] != "unknown_request" {
			t.Fatalf("outcome on B: %v", outcome)
		}
	}
	// As live (#693, 2026-11-15): the first address refuses at once and the second takes no
	// TCP connection at all.
	setDial(func(ctx context.Context, address string) (net.Conn, error) {
		if address == b.ip+":47672" {
			return nil, errors.New("synthetic: connection refused")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	started := time.Now()
	status, body = answerOf(t, a.d, context.Background(), f.envelope("board_list", nil, f.request(), time.Now().Unix(), nil))
	wantAnswer(t, "refused, then silent", status, body, 409, "computer_offline")
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("refused, then silent: computer_offline after %s", elapsed)
	}
	// A caller's shorter deadline still ends the connect.
	ctx, cancel := context.WithTimeout(context.Background(), forwardConnect/6)
	defer cancel()
	started = time.Now()
	status, body = answerOf(t, a.d, ctx, f.envelope("board_list", nil, f.request(), time.Now().Unix(), nil))
	if elapsed := time.Since(started); elapsed > forwardConnect/2 {
		t.Fatalf("cancelled connect answered after %s: %d %s", elapsed, status, body)
	}
}
