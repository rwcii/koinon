package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/remote"
)

// This opt-in probe changes one previously unused Serve port on the real host. Ordinary
// tests never execute Tailscale. Coordinate the port and account before opting in; the
// synthetic state and secret carry no production content. This does not test a phone.
func TestRealServeForwarding(t *testing.T) {
	if os.Getenv("KOINON_REAL_SERVE") != "1" {
		t.Skip("real Serve probe requires connected Tailscale, HTTPS permission and explicit KOINON_REAL_SERVE=1; see docs/sprints/2026-11-11-remote-dashboard/verification.md")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adapter := remote.Exec{}
	status, err := (remote.Manager{Adapter: adapter}).Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = status.Ready(); err != nil {
		t.Fatal(err)
	}
	before, err := adapter.Run(ctx, "serve", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	host := status.DNSName + ":8443"
	var unused func(remote.ServeConfig) bool
	unused = func(c remote.ServeConfig) bool {
		if _, ok := c.TCP["8443"]; ok {
			return false
		}
		if _, ok := c.Web[host]; ok {
			return false
		}
		if c.AllowFunnel[host] {
			return false
		}
		for _, fg := range c.Foreground {
			if fg != nil && !unused(*fg) {
				return false
			}
		}
		return true
	}
	if !unused(status.Serve) {
		t.Fatal("Serve port 8443 is in use; coordinate another host or free this probe port explicitly; nothing changed")
	}
	d, _ := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, err = d.changeSecret(ctx, "set", testSecret); err != nil {
		t.Fatal(err)
	}
	origin := "https://" + host
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := "http://" + l.Addr().String()
	seen := make(chan [3]string, 32)
	h := d.remoteDashboardHandler(origin)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- [3]string{r.Host, r.Header.Get("Origin"), r.Method}
		h.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(l)
	defer server.Close()
	// Register cleanup before mutation: a command may apply its mapping then lose its reply.
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		data, e := adapter.Run(cleanup, "serve", "status", "--json")
		if e != nil {
			t.Error("cleanup inspect", e)
			return
		}
		var cfg remote.ServeConfig
		if json.Unmarshal(data, &cfg) != nil {
			t.Error("cleanup invalid Serve JSON")
			return
		}
		handler, exists := cfg.Web[host].Handlers["/"]
		if exists {
			if handler.Proxy != target || handler.Path != "" || handler.Text != "" {
				t.Error("probe mapping changed by another owner; inspect manually; not removed")
				return
			}
			// Targeted off applies the current identity. Never remove after a name change.
			current, e := (remote.Manager{Adapter: adapter}).Inspect(cleanup)
			if e != nil || current.DNSName != status.DNSName {
				t.Error("identity changed; inspect probe mapping manually", e)
				return
			}
			if _, e = adapter.Run(cleanup, "serve", "--bg", "--https=8443", "--set-path=/", "off"); e != nil {
				t.Error("probe cleanup", e)
				return
			}
		}
		after, e := adapter.Run(cleanup, "serve", "status", "--json")
		if e != nil {
			t.Error(e)
			return
		}
		var a, b any
		json.Unmarshal(before, &a)
		json.Unmarshal(after, &b)
		if !reflect.DeepEqual(a, b) {
			t.Error("Serve configuration differs after probe; inspect unrelated concurrent edits privately")
		}
	}()
	if _, err = adapter.Run(ctx, "serve", "--bg", "--https=8443", "--set-path=/", target); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, from string, form url.Values) (*http.Response, string) {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, method, origin+path, strings.NewReader(form.Encode()))
		if e != nil {
			t.Fatal(e)
		}
		if from != "" {
			r.Header.Set("Origin", from)
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Forwarded-Host", "forged.example")
		r.Header.Set("X-Forwarded-Proto", "http")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal("real HTTPS request failed (same-host tailnet forwarding may require a second host):", e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		select {
		case got := <-seen:
			if got[0] != host || got[1] != from || got[2] != method {
				t.Fatalf("backend Host/Origin/Method = %q; expected %q", got, [3]string{host, from, method})
			}
		case <-ctx.Done():
			t.Fatal("backend did not observe request")
		}
		return response, string(body)
	}
	r, _ := call("GET", "/", "", nil)
	if r.StatusCode != 303 || r.Header.Get("Location") != "/dashboard/" {
		t.Fatal("root redirect", r.StatusCode, r.Header)
	}
	r, _ = call("POST", "/dashboard/login", origin, url.Values{"secret": {testSecret}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/dashboard/sessions" {
		t.Fatal("login", r.StatusCode)
	}
	cookies := r.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("secure login cookie missing")
	}
	r, body := call("GET", "/dashboard/sessions", "", nil)
	if r.StatusCode != 200 {
		t.Fatal("HTTPS cookie not returned", r.StatusCode)
	}
	csrf := csrfField.FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("no CSRF")
	}
	for _, from := range []string{"", "null", "https://foreign.example"} {
		r, _ = call("POST", "/dashboard/logout", from, url.Values{"csrf": {csrf[1]}})
		if r.StatusCode != 403 {
			t.Fatal("foreign Origin accepted", from, r.StatusCode)
		}
	}
	r, _ = call("POST", "/dashboard/logout", origin, nil)
	if r.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	for _, method := range []string{"GET", "POST"} {
		r, _ = call(method, "/v1/status", origin, nil)
		if r.StatusCode != 404 {
			t.Fatal("agent API exposed", method, r.StatusCode)
		}
	}
	r, _ = call("POST", "/dashboard/logout", origin, url.Values{"csrf": {csrf[1]}})
	if r.StatusCode != 200 {
		t.Fatal("CSRF logout failed", r.StatusCode)
	}
	t.Log(fmt.Sprintf("Real Serve HTTPS forwarding verified: Host and Origin preserved, relative redirects, Secure cookie round trip, origin/CSRF checks, forwarding header isolation and /v1/ refusal. Host OS=%s. Phone access remains unverified.", runtime.GOOS))
}
