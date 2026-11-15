package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/signed"
)

// The signed routes on Remote (#693 chunk 3, criteria C3-1 to C3-8). All names, addresses and
// keys are synthetic.

// remoteCall sends one request to d's Remote listener, as Tailscale Serve forwards it: with the
// pinned Host of d's origin. It returns the status and the raw body.
func remoteCall(ctx context.Context, d *Daemon, method, path string, header map[string]string, body []byte) (int, []byte, error) {
	d.remote.mu.Lock()
	origin := d.remote.config.Mapping.Origin
	var addr string
	if d.remote.listener != nil {
		addr = d.remote.listener.Addr().String()
	}
	d.remote.mu.Unlock()
	u, err := url.Parse(origin)
	if err != nil {
		return 0, nil, err
	}
	if addr == "" {
		return 0, nil, net.ErrClosed
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	r.Host = u.Host
	for k, v := range header {
		r.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func mustRemote(t *testing.T, d *Daemon, method, path string, header map[string]string, body []byte) (int, []byte) {
	t.Helper()
	status, data, err := remoteCall(context.Background(), d, method, path, header, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, data
}

var jsonType = map[string]string{"Content-Type": "application/json"}

func bearerOf(token string) map[string]string {
	return map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token}
}

// remoteSession opens a signed session for c over d's Remote and returns its token.
func remoteSession(t *testing.T, d *Daemon, c *signedClient) string {
	t.Helper()
	status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed/session", jsonType,
		c.envelope("session", map[string]any{"client": "voice"}, c.request(), time.Now().Unix(), nil))
	reply := decoded(t, string(body))
	token, _ := reply["token"].(string)
	if status != 200 || token == "" || reply["client"] != "key:voice" || reply["idle_seconds"] != float64(86400) || reply["absolute_seconds"] != float64(604800) {
		t.Fatalf("remote session: %d %s", status, body)
	}
	return token
}

// loopbackRaw posts to d's loopback signed route and returns the status and the raw body.
func loopbackRaw(t *testing.T, d *Daemon, bearer, token string, body []byte, contentType string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest("POST", "http://"+d.Addresses()[0]+"/v1/maintainer/signed", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Content-Type", contentType)
	r.Header.Set(MaintainerTokenHeader, token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, data
}

// C3-1, C3-2, C3-3, C3-8: Remote serves the two signed routes and nothing else outside the
// dashboard; the action route takes only a signed session's token as its bearer.
func TestSignedRemoteSurface(t *testing.T) {
	h := newLinkHost(t, &linkNet{}, "alpha", "100.64.0.1")
	d := h.d
	ctx := context.Background()
	join(t, d.store, "codex", "synthetic-remote-surface", "")
	c := newSignedClient(t, d, 40)

	// The dashboard's redirect stays; other paths and methods do not reach the API.
	if status, _ := mustRemote(t, d, "GET", "/", nil, nil); status != http.StatusSeeOther {
		t.Fatalf("GET /: %d", status)
	}
	for _, path := range []string{"/v1/maintainer/signed/", "/v1/maintainer/signed/sessionx", "/v1/maintainer/signedx", "/v1/status",
		"/v1/send", "/v1/maintainer/session", "/v1/maintainer/keys", "/v1/maintainer/secret", "/v1/dashboard/links", "/v1/maintainer/signed/session/"} {
		if status, _ := mustRemote(t, d, "POST", path, jsonType, []byte("{}")); status != http.StatusNotFound {
			t.Fatalf("POST %s: %d", path, status)
		}
	}
	for _, path := range []string{"/v1/maintainer/signed", "/v1/maintainer/signed/session"} {
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if status, _ := mustRemote(t, d, method, path, jsonType, nil); status != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: %d", method, path, status)
			}
		}
	}

	token := remoteSession(t, d, c)
	var audit string
	d.store.db.QueryRow(`SELECT target FROM audit WHERE action='maintainer_login' ORDER BY rowid DESC LIMIT 1`).Scan(&audit)
	if !strings.HasSuffix(audit, " key "+c.key+" via remote") || strings.Contains(audit, token) {
		t.Fatalf("login audit: %q", audit)
	}
	read := func() []byte { return c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil) }
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(token), read()); status != 200 || decoded(t, string(body))["ok"] != true {
		t.Fatalf("action: %d %s", status, body)
	}
	// A browser's Origin is no refusal; the envelope and the token are the authority.
	withOrigin := bearerOf(token)
	withOrigin["Origin"] = "https://alpha.example-tailnet.ts.net"
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", withOrigin, read()); status != 200 {
		t.Fatalf("with Origin: %d %s", status, body)
	}

	// Only the signed token, as the bearer, opens the action route.
	secret := linkBearer(t, h)
	secretToken := newMaintainerToken(t, d, secret, "remote-test")
	cookie := map[string]string{"Content-Type": "application/json", "Cookie": "koinon_dashboard=" + token}
	for name, header := range map[string]map[string]string{
		"no token":           jsonType,
		"token header":       {"Content-Type": "application/json", MaintainerTokenHeader: token},
		"daemon secret":      bearerOf(secret),
		"secret token":       bearerOf(secretToken),
		"token as cookie":    cookie,
		"unknown token":      bearerOf(strings.Repeat("ab", 32)),
		"no Bearer prefix":   {"Content-Type": "application/json", "Authorization": token},
		"bad type, no token": {"Content-Type": "text/plain"},
	} {
		status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", header, read())
		if status != http.StatusConflict || decoded(t, string(body))["code"] != "maintainer_token_expired" {
			t.Fatalf("%s: %d %s", name, status, body)
		}
	}
	// A signed token opens no dashboard page.
	if status, _ := mustRemote(t, d, "GET", "/dashboard/sessions", map[string]string{"Authorization": "Bearer " + token}, nil); status == 200 {
		t.Fatal("a signed token opened a dashboard page")
	}

	// Content type and size: the loopback route's answers, byte for byte.
	loopToken, _ := newSignedSessionToken(t, d, secret, c)
	big := []byte(`{"pad":"` + strings.Repeat("a", signed.MaxEnvelopeBytes) + `"}`)
	for name, tc := range map[string]struct {
		contentType string
		body        []byte
	}{
		"text/plain": {"text/plain", read()},
		"too large":  {"application/json", big},
		"not json":   {"application/json", []byte("not json")},
	} {
		rs, rb := mustRemote(t, d, "POST", "/v1/maintainer/signed", map[string]string{"Content-Type": tc.contentType, "Authorization": "Bearer " + token}, tc.body)
		ls, lb := loopbackRaw(t, d, secret, loopToken, tc.body, tc.contentType)
		if rs != ls || !bytes.Equal(rb, lb) || rs == 200 {
			t.Fatalf("%s: Remote %d %s, loopback %d %s", name, rs, rb, ls, lb)
		}
	}
	// The same envelope gives the same bytes on both mounts.
	same := read()
	rs, rb := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(token), same)
	ls, lb := loopbackRaw(t, d, secret, loopToken, same, "application/json")
	if rs != 200 || rs != ls || !bytes.Equal(rb, lb) {
		t.Fatalf("parity: Remote %d %s, loopback %d %s", rs, rb, ls, lb)
	}

	// The routes have no secret gate: with the secret cleared, the dashboard refuses and the
	// signed routes still answer.
	if _, err := d.changeSecret(ctx, "clear", ""); err != nil {
		t.Fatal(err)
	}
	if status, _ := mustRemote(t, d, "GET", "/dashboard/", nil, nil); status != http.StatusServiceUnavailable {
		t.Fatalf("dashboard without a secret: %d", status)
	}
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(token), read()); status != 200 {
		t.Fatalf("action without a secret: %d %s", status, body)
	}
	remoteSession(t, d, c)
}

// C3-4, C3-5: through A's Remote, an action signed by B's key for B runs on B, with B's own
// answer; A's session uses A's own key. A changes wait near 30 seconds, over a link that is
// slow to connect, answers through the Remote listener past its 10-second timeouts, while a
// 20-second one and a fast call on the same session answer too.
func TestSignedRemoteForward(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, b.d.store, "codex", "synthetic-remote-forward", "")
	mustCreate(t, b.d.store, Key{"codex", agent.ID}, "RF-1", DecisionFields{Title: ptr("Synthetic remote"), Kind: ptr("decision"), Recipients: &[]string{agent.Name}})
	node := newSignedClient(t, a.d, 41)
	token := remoteSession(t, a.d, node)
	c := newSignedClient(t, b.d, 42)
	now := time.Now().Unix()
	for _, args := range []struct {
		op   string
		args map[string]any
	}{
		{"inbox", nil}, {"board_list", nil}, {"decision_get", map[string]any{"reference": "RF-1"}}, {"decision_get", map[string]any{"reference": "RF-404"}},
	} {
		data := c.envelope(args.op, args.args, c.request(), now, nil)
		rs, rb := mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
		ls, lb := answerOf(t, b.d, ctx, data)
		if rs != ls || string(bytes.TrimSuffix(rb, []byte("\n"))) != lb {
			t.Fatalf("%s: Remote of A %d %s, B %d %s", args.op, rs, rb, ls, lb)
		}
	}
	snapshot, revision := snapshotOf(t, c, "RF-1")
	status, body := mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), c.envelope("decision_verdict",
		map[string]any{"reference": "RF-1", "revision": revision, "snapshot": snapshot, "verdict": "approved"}, c.request(), now, nil))
	if status != 200 || rowCount(t, b.d, `SELECT COUNT(*) FROM decision_verdicts WHERE signer_key=?`, c.key) != 1 {
		t.Fatalf("verdict: %d %s", status, body)
	}

	_, versions := mustForwardVersions(t, a.d, token, c)
	slow := func(dial func(context.Context, string) (net.Conn, error)) func(context.Context, string) (net.Conn, error) {
		return func(ctx context.Context, address string) (net.Conn, error) {
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return dial(ctx, address)
		}
	}
	a.d.link.mu.Lock()
	a.d.link.dial = slow(a.d.link.dial)
	a.d.link.mu.Unlock()
	var wg sync.WaitGroup
	results := make([]string, 2)
	for i, wait := range []int64{29, 20} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			status, body, err := remoteCall(ctx, a.d, "POST", "/v1/maintainer/signed", bearerOf(token),
				c.envelope("changes", map[string]any{"versions": versions, "wait_seconds": wait}, c.request(), time.Now().Unix(), nil))
			elapsed := time.Since(started)
			var reply map[string]any
			json.Unmarshal(body, &reply)
			result, _ := reply["result"].(map[string]any)
			if err != nil || status != 200 || result == nil || len(result["changed"].([]any)) != 0 || elapsed < time.Duration(wait)*time.Second {
				results[i] = "failed"
				t.Errorf("wait %d: %d %s %v after %v", wait, status, body, err, elapsed)
				return
			}
			results[i] = "quiet"
		}()
	}
	// A fast call of the same session while both wait.
	time.Sleep(time.Second)
	started := time.Now()
	if status, body := mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), node.envelope("inbox", nil, node.request(), time.Now().Unix(), nil)); status != 200 || time.Since(started) > 3*time.Second {
		t.Fatalf("fast call during the waits: %d %s after %v", status, body, time.Since(started))
	}
	wg.Wait()
	if results[0] != "quiet" || results[1] != "quiet" {
		t.Fatalf("waits: %v", results)
	}
}

// mustForwardVersions reads B's changes versions through A's Remote.
func mustForwardVersions(t *testing.T, a *Daemon, token string, c *signedClient) (int, map[string]any) {
	t.Helper()
	status, body := mustRemote(t, a, "POST", "/v1/maintainer/signed", bearerOf(token), c.envelope("changes", nil, c.request(), time.Now().Unix(), nil))
	result, _ := decoded(t, string(body))["result"].(map[string]any)
	versions, _ := result["versions"].(map[string]any)
	if status != 200 || versions == nil {
		t.Fatalf("changes: %d %s", status, body)
	}
	return status, versions
}

// C3-7: a session has at most MaxSignedCalls actions in flight; one more is refused at once,
// before its envelope runs, and another session goes on.
func TestSignedRemoteCallLimit(t *testing.T) {
	h := newLinkHost(t, &linkNet{}, "alpha", "100.64.0.1")
	d := h.d
	agent := join(t, d.store, "codex", "synthetic-remote-limit", "")
	c := newSignedClient(t, d, 43)
	first := remoteSession(t, d, c)
	second := remoteSession(t, d, c)
	_, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(first), c.envelope("changes", nil, c.request(), time.Now().Unix(), nil))
	versions := decoded(t, string(body))["result"].(map[string]any)["versions"]
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < MaxSignedCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remoteCall(ctx, d, "POST", "/v1/maintainer/signed", bearerOf(first),
				c.envelope("changes", map[string]any{"versions": versions, "wait_seconds": int64(30)}, c.request(), time.Now().Unix(), nil))
		}()
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		d.signedCalls.mu.Lock()
		n := d.signedCalls.n[first]
		d.signedCalls.mu.Unlock()
		if n == MaxSignedCalls {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls in flight: %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	request := c.request()
	status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(first),
		c.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic call over the limit"}, request, time.Now().Unix(), nil))
	if reply := decoded(t, string(body)); status != http.StatusTooManyRequests || reply["code"] != "too_many_calls" || reply["details"].(map[string]any)["limit"] != float64(MaxSignedCalls) {
		t.Fatalf("over the limit: %d %s", status, body)
	}
	if n := rowCount(t, d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic call over the limit'`); n != 0 {
		t.Fatalf("messages: %d", n)
	}
	if outcome := c.must("outcome", map[string]any{"request": request, "created": time.Now().Unix()}); outcome["state"] != "unknown_request" {
		t.Fatalf("outcome: %v", outcome)
	}
	// An unknown token takes no place and is refused as such.
	if status, _ := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(strings.Repeat("cd", 32)), []byte("{}")); status != http.StatusConflict {
		t.Fatalf("unknown token: %d", status)
	}
	// The other session reads and writes.
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(second), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)); status != 200 {
		t.Fatalf("second session read: %d %s", status, body)
	}
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(second),
		c.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic second session send"}, c.request(), time.Now().Unix(), nil)); status != 200 {
		t.Fatalf("second session write: %d %s", status, body)
	}
	// Ended calls give their places back.
	cancel()
	wg.Wait()
	deadline = time.Now().Add(10 * time.Second)
	for {
		d.signedCalls.mu.Lock()
		n, held := d.signedCalls.n[first]
		d.signedCalls.mu.Unlock()
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("places still held: %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(first), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)); status != 200 {
		t.Fatalf("after the waits: %d %s", status, body)
	}
}

// C3-6: a Remote disable does not wait for signed waits: they end with the surface, also a
// forwarded one, and a write held for the store does not commit later. Enable opens a new
// surface where the session goes on.
func TestSignedRemoteDisableEndsRequests(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	agent := join(t, a.d.store, "codex", "synthetic-remote-disable", "")
	node := newSignedClient(t, a.d, 44)
	token := remoteSession(t, a.d, node)
	c := newSignedClient(t, b.d, 45)
	local := node.must("changes", nil)["versions"]
	_, forwarded := mustForwardVersions(t, a.d, token, c)

	// A write that waits for A's store.
	a.d.store.storage.mu.Lock()
	held := true
	defer func() {
		if held {
			a.d.store.storage.mu.Unlock()
		}
	}()
	request := node.request()
	done := make(chan string, 3)
	call := func(name string, data []byte) {
		status, body, err := remoteCall(ctx, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), data)
		done <- name + ": " + strings.TrimSpace(string(body)) + " " + http.StatusText(status) + " " + func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}()
	}
	go call("local wait", node.envelope("changes", map[string]any{"versions": local, "wait_seconds": int64(30)}, node.request(), time.Now().Unix(), nil))
	go call("forwarded wait", c.envelope("changes", map[string]any{"versions": forwarded, "wait_seconds": int64(30)}, c.request(), time.Now().Unix(), nil))
	go call("held write", node.envelope("send", map[string]any{"to": agent.Name, "body": "a synthetic held write"}, request, time.Now().Unix(), nil))
	time.Sleep(time.Second)

	disabled := make(chan error, 1)
	started := time.Now()
	go func() { disabled <- a.d.remoteDisable(ctx) }()
	for i := 0; i < 3; i++ {
		select {
		case result := <-done:
			t.Logf("%s after %v", result, time.Since(started))
		case <-time.After(5 * time.Second):
			t.Fatalf("a request did not end with the surface (%d ended)", i)
		}
	}
	// The disable's own store writes wait for the held store; release it now.
	a.d.store.storage.mu.Unlock()
	held = false
	select {
	case err := <-disabled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("disable did not return")
	}
	if err := a.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := rowCount(t, a.d, `SELECT COUNT(*) FROM messages WHERE body='a synthetic held write'`); n != 0 {
		t.Fatalf("the held write committed: %d", n)
	}
	if outcome := node.must("outcome", map[string]any{"request": request, "created": time.Now().Unix()}); outcome["state"] != "unknown_request" {
		t.Fatalf("outcome: %v", outcome)
	}
	if status, body := mustRemote(t, a.d, "POST", "/v1/maintainer/signed", bearerOf(token), node.envelope("inbox", nil, node.request(), time.Now().Unix(), nil)); status != 200 {
		t.Fatalf("after enable: %d %s", status, body)
	}
}

// C3-F1: a check of the authorization that could not run, such as a request that Remote's
// close cancelled, ends no session; only a removed or replaced authorization ends the key's.
func TestSignedSessionsSurviveCancelledChecks(t *testing.T) {
	h := newLinkHost(t, &linkNet{}, "alpha", "100.64.0.1")
	d := h.d
	ctx := context.Background()
	c := newSignedClient(t, d, 46)
	first, second := remoteSession(t, d, c), remoteSession(t, d, c)
	key, ok := d.auth.signedToken(first)
	if !ok {
		t.Fatal("no session")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	// The action route with a cancelled request.
	r := httptest.NewRequest("POST", "/v1/maintainer/signed", bytes.NewReader(c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil))).WithContext(cancelled)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.signedActionRoute(w, r, first)
	if w.Code == 200 || strings.Contains(w.Body.String(), "maintainer_token_expired") {
		t.Fatalf("cancelled check: %d %s", w.Code, w.Body)
	}
	// The check after a login, cancelled: only the new session ends.
	third, err := d.auth.startSigned(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.checkNewSession(cancelled, key, third); err == nil || authorizationEnded(err) {
		t.Fatalf("cancelled login check: %v", err)
	}
	if _, ok := d.auth.signedToken(third); ok {
		t.Fatal("the new session outlived its failed check")
	}
	for _, token := range []string{first, second} {
		if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(token), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)); status != 200 {
			t.Fatalf("after cancelled checks: %d %s", status, body)
		}
	}

	// The integrated case: the store's one connection is held while the action's check
	// waits for it, Remote turns off and on, and both sessions go on.
	conn, err := d.store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan int, 1)
	go func() {
		status, _, _ := remoteCall(ctx, d, "POST", "/v1/maintainer/signed", bearerOf(first), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil))
		ended <- status
	}()
	time.Sleep(500 * time.Millisecond)
	disabled := make(chan error, 1)
	go func() { disabled <- d.remoteDisable(ctx) }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting check did not end with the surface")
	}
	conn.Close()
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if status, body := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(token), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)); status != 200 {
			t.Fatalf("after Remote off and on: %d %s", status, body)
		}
	}

	// A removed authorization still ends every session of the key.
	if _, err := d.store.RemoveMaintainerKey(ctx, c.key); err != nil {
		t.Fatal(err)
	}
	if status, _ := mustRemote(t, d, "POST", "/v1/maintainer/signed", bearerOf(first), c.envelope("inbox", nil, c.request(), time.Now().Unix(), nil)); status != http.StatusConflict {
		t.Fatalf("after removal: %d", status)
	}
	if _, ok := d.auth.signedToken(second); ok {
		t.Fatal("the key's other session outlived its removal")
	}
}
