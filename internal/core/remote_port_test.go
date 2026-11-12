package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

const portOrigin = "https://host.example.ts.net:8443"

func remoteWrites(f *syntheticTailscale) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func serveRoot(f *syntheticTailscale, key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.config.Web[key].Handlers["/"]
	return h.Proxy, ok
}

// #665: enable on 8443 writes Serve's HTTPS port 8443, and the URL, the policy hint, a
// repeated enable, an orderly shutdown, a restart and disable all keep that port.
func TestRemotePortLifecycle(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx, 8443); err != nil {
		t.Fatal(err)
	}
	c, err := ReadRemoteConfig(root)
	if err != nil || c == nil || c.Mapping.Origin != portOrigin || !c.Enabled || !c.Owned {
		t.Fatalf("record: %+v %v", c, err)
	}
	if w := remoteWrites(fake); !reflect.DeepEqual(w, []string{"--https=8443 " + c.Mapping.Target}) {
		t.Fatalf("writes %v", w)
	}
	s := d.remoteStatus(ctx)
	if s.URL != portOrigin+"/dashboard/" || s.Serve != "ready" || s.Listener != "ready" || !strings.Contains(s.PolicyHint, "TCP 8443") || strings.Contains(s.PolicyHint, "TCP 443 ") {
		t.Fatalf("status: %+v", s)
	}
	// A repeated enable without a port keeps 8443 and writes nothing.
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if again, _ := ReadRemoteConfig(root); *again != *c || len(remoteWrites(fake)) != 1 {
		t.Fatalf("repeat: %+v %v", again, remoteWrites(fake))
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := serveRoot(fake, "host.example.ts.net:8443"); ok {
		t.Fatal("orderly shutdown kept the 8443 mapping")
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: fake, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.restoreRemote(ctx)
	if s := restarted.remoteStatus(ctx); s.URL != portOrigin+"/dashboard/" || s.Serve != "ready" || s.Listener != "ready" {
		t.Fatalf("restart: %+v", s)
	}
	if proxy, ok := serveRoot(fake, "host.example.ts.net:8443"); !ok || proxy != c.Mapping.Target {
		t.Fatalf("restart mapping %q %v", proxy, ok)
	}
	if err := restarted.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	off, _ := ReadRemoteConfig(root)
	if _, ok := serveRoot(fake, "host.example.ts.net:8443"); ok || off.Enabled || off.Owned || off.Mapping.Origin != portOrigin {
		t.Fatalf("disable: %+v %v", off, ok)
	}
}

// A new port on an owned mapping first takes the disable steps: the logins end and the old
// mapping is removed before the new one is written. A failed removal keeps the old record,
// disabled and owned, and writes no new mapping.
func TestRemotePortChange(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	first, _ := ReadRemoteConfig(root)
	if first.Mapping.Origin != "https://host.example.ts.net" {
		t.Fatalf("first: %+v", first)
	}
	id, _ := d.auth.start("remote:" + first.Mapping.Origin)
	if err := d.remoteEnable(ctx, 8443); err != nil {
		t.Fatal(err)
	}
	moved, _ := ReadRemoteConfig(root)
	want := []string{"--https=443 " + first.Mapping.Target, "--https=443 off", "--https=8443 " + moved.Mapping.Target}
	if w := remoteWrites(fake); !reflect.DeepEqual(w, want) || moved.Mapping.Origin != portOrigin || !moved.Enabled || !moved.Owned {
		t.Fatalf("writes %v, record %+v", w, moved)
	}
	if _, ok := serveRoot(fake, "host.example.ts.net:443"); ok {
		t.Fatal("old 443 mapping stayed")
	}
	if hash, _ := tokenHash(id); d.auth.sessions[hash] != nil {
		t.Fatal("old remote login stayed")
	}
	// An explicit 443 moves back and writes the origin without a port.
	if err := d.remoteEnable(ctx, 443); err != nil {
		t.Fatal(err)
	}
	if back, _ := ReadRemoteConfig(root); back.Mapping.Origin != "https://host.example.ts.net" || !back.Owned {
		t.Fatalf("back: %+v", back)
	}

	d, root, fake = remoteFixture(t)
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	first, _ = ReadRemoteConfig(root)
	fake.mu.Lock()
	fake.fail = true
	fake.mu.Unlock()
	if err := d.remoteEnable(ctx, 8443); err == nil {
		t.Fatal("enable succeeded through a failed removal")
	}
	kept, _ := ReadRemoteConfig(root)
	if kept.Mapping != first.Mapping || kept.Enabled || !kept.Owned || len(remoteWrites(fake)) != 1 {
		t.Fatalf("after failed removal: %+v %v", kept, remoteWrites(fake))
	}
	fake.mu.Lock()
	fake.fail = false
	fake.mu.Unlock()
	if err := d.remoteEnable(ctx, 8443); err != nil {
		t.Fatal(err)
	}
	moved, _ = ReadRemoteConfig(root)
	if w := remoteWrites(fake); !reflect.DeepEqual(w, []string{"--https=443 " + first.Mapping.Target, "--https=443 off", "--https=8443 " + moved.Mapping.Target}) {
		t.Fatalf("retry writes %v", w)
	}
}

// The API takes an optional https_port; an invalid one is refused before any change.
func TestRemoteEnableRoutePort(t *testing.T) {
	d, root, fake := remoteFixture(t)
	for _, body := range []string{`{"https_port":65536}`, `{"https_port":-1}`, `{"https_port":"8443"}`, `{"https_port":8443,"other":1}`} {
		response := request(t, d, "/v1/remote/enable", body, d.secret)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var refused struct {
			Code string `json:"code"`
		}
		json.Unmarshal(data, &refused)
		if response.StatusCode != 400 || refused.Code != "invalid_request" {
			t.Fatalf("%s: %d %s", body, response.StatusCode, data)
		}
	}
	if c, _ := ReadRemoteConfig(root); c != nil || len(remoteWrites(fake)) != 0 {
		t.Fatalf("a refused request changed state: %+v %v", c, remoteWrites(fake))
	}
	for _, tc := range []struct{ body, origin string }{{`{}`, "https://host.example.ts.net"}, {`{"https_port":8443}`, portOrigin}, {`{"https_port":0}`, portOrigin}} {
		response := request(t, d, "/v1/remote/enable", tc.body, d.secret)
		response.Body.Close()
		if c, _ := ReadRemoteConfig(root); response.StatusCode != 200 || c == nil || c.Mapping.Origin != tc.origin {
			t.Fatalf("%s: %d %+v", tc.body, response.StatusCode, c)
		}
	}
	if response := request(t, d, "/v1/remote/disable", `{"https_port":8443}`, d.secret); response.StatusCode != 400 {
		t.Fatalf("disable took a port: %d", response.StatusCode)
	}
}

// On 8443 the remote surface accepts only Host and Origin with that port: login, a write
// and ticket redemption work there, and the origin without the port is refused.
func TestRemotePortBoundary(t *testing.T) {
	d, root, _ := onboardingFixture(t)
	local := newActionClient(t, d, root)
	if err := d.remoteEnable(context.Background(), 8443); err != nil {
		t.Fatal(err)
	}
	h := d.remoteDashboardHandler(portOrigin)
	call := func(method, path, host, from string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:10001"+path, strings.NewReader(form.Encode()))
		r.Host = host
		if from != "" {
			r.Header.Set("Origin", from)
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := url.Values{"secret": {testSecret}}
	for _, tc := range []struct{ host, from string }{
		{"host.example.ts.net", portOrigin},
		{"host.example.ts.net:443", portOrigin},
		{"host.example.ts.net:8443", "https://host.example.ts.net"},
		{"host.example.ts.net:8443", "https://host.example.ts.net:443"},
		{"host.example.ts.net:8444", portOrigin},
	} {
		if got := call("POST", "/dashboard/login", tc.host, tc.from, login, nil); got.Code != 403 || got.Header().Get("Set-Cookie") != "" {
			t.Fatalf("%+v: %d", tc, got.Code)
		}
	}
	w := call("POST", "/dashboard/login", "host.example.ts.net:8443", portOrigin, login, nil)
	if w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	page := call("GET", "/dashboard/sessions", "host.example.ts.net:8443", "", nil, cookie)
	match := csrfField.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(match) != 2 {
		t.Fatalf("page: %d", page.Code)
	}
	if got := call("POST", "/dashboard/logout", "host.example.ts.net:8443", "https://host.example.ts.net", url.Values{"csrf": {match[1]}}, cookie); got.Code != 403 {
		t.Fatalf("write from the origin without the port: %d", got.Code)
	}
	if got := call("POST", "/dashboard/logout", "host.example.ts.net:8443", portOrigin, url.Values{"csrf": {match[1]}}, cookie); got.Code != 200 {
		t.Fatalf("write: %d", got.Code)
	}
	// The one-time ticket names the origin with its port, and redeems only there.
	token, _, origin, err := d.issueRemoteTicket(context.Background(), local.cookie)
	if err != nil || origin != portOrigin {
		t.Fatalf("ticket origin %q %v", origin, err)
	}
	form := url.Values{"ticket": {token}}
	if got := pairRequest(h, "POST", portOrigin, "/dashboard/pair", "https://host.example.ts.net", form, nil); got.Code != 403 {
		t.Fatalf("ticket from the origin without the port: %d", got.Code)
	}
	if got := pairRequest(h, "POST", portOrigin, "/dashboard/pair", portOrigin, form, nil); got.Code != 303 {
		t.Fatalf("ticket: %d %s", got.Code, got.Body.String())
	}
}

// The dashboard setup shows the URL with the port, and its QR encodes exactly that URL.
func TestRemotePortDashboardURLAndQR(t *testing.T) {
	d, root, _ := onboardingFixture(t)
	client := newActionClient(t, d, root)
	if err := d.remoteEnable(context.Background(), 8443); err != nil {
		t.Fatal(err)
	}
	link := portOrigin + "/dashboard/"
	page := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, link) || !strings.Contains(page.body, "TCP 8443") {
		t.Fatalf("setup: %d %s", page.status, page.body)
	}
	qr := dashboardDo(t, d, "GET", "/dashboard/remote/qr", client.cookie, nil, nil)
	want, err := qrcode.Encode(link, qrcode.Medium, 256)
	if err != nil || qr.status != 200 || !bytes.Equal([]byte(qr.body), want) {
		t.Fatalf("QR: %d %v", qr.status, err)
	}
	// The dashboard's Enable keeps the saved port.
	if code := client.do("remote-enable", url.Values{}); code != "remote_enabled" {
		t.Fatal(code)
	}
	if c, _ := ReadRemoteConfig(root); c.Mapping.Origin != portOrigin {
		t.Fatalf("dashboard enable moved the port: %+v", c)
	}
}
