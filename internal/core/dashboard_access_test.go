package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var accessAddress = DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}

// accessFixture is a daemon with the maintainer secret, a synthetic Tailscale and a running
// network dashboard on a loopback socket.
func accessFixture(t *testing.T) (*Daemon, string, *onboardingTailscale) {
	t.Helper()
	d, root, fake := onboardingFixture(t)
	if err := WriteDashboardAddress(root, &accessAddress); err != nil {
		t.Fatal(err)
	}
	d.startDashboardNetwork(networkTestInterfaces, func(_, _ string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") })
	if s := d.dashboardNetworkStatus(context.Background()); s == nil || s.Code != "running" {
		t.Fatalf("network: %+v", s)
	}
	return d, root, fake
}

// networkClosed waits until a new connection to the network listener is refused.
func networkClosed(t *testing.T, addr string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return
		}
		c.Close()
	}
	t.Fatal("the network listener still accepts connections")
}

// #665: the dashboard access switch enables remote access, saves the local choice, removes
// the network address and closes the network listener; the panel names each mode.
func TestDashboardAccessSwitch(t *testing.T) {
	d, root, _ := accessFixture(t)
	client := newActionClient(t, d, root)
	panel := func() string {
		t.Helper()
		page := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1", client.cookie, nil, nil)
		if page.status != 200 {
			t.Fatalf("panel %d", page.status)
		}
		return page.body
	}
	if body := panel(); !strings.Contains(body, "and on the network address <code>https://board.example.test:8443/dashboard/</code>") || !strings.Contains(body, ">Use this computer and the tailnet only</button>") || !strings.Contains(body, "closes the network address at once") {
		t.Fatalf("network mode: %s", body)
	}
	addr := d.networkListener.Addr().String()
	before := len(auditAll(t, d.store))
	if code := client.do("dashboard-access", url.Values{"mode": {"tailnet"}}); code != "dashboard_access_tailnet" {
		t.Fatal(code)
	}
	expectAudit(t, d.store, before, "dashboard-access", "accepted", "")
	if a, err := ReadDashboardAddress(root); a != nil || err != nil {
		t.Fatalf("address kept: %+v %v", a, err)
	}
	if local, err := ReadDashboardLocal(root); !local || err != nil {
		t.Fatalf("local choice: %v %v", local, err)
	}
	if c, _ := ReadRemoteConfig(root); c == nil || !c.Enabled || !c.Owned {
		t.Fatalf("remote: %+v", c)
	}
	networkClosed(t, addr)
	if s := d.dashboardNetworkStatus(context.Background()); s != nil {
		t.Fatalf("network status after the switch: %+v", s)
	}
	if body := panel(); !strings.Contains(body, "and on your tailnet at <code>https://host.example.ts.net/dashboard/</code>") || strings.Contains(body, "network address") || strings.Contains(body, "Use this computer and the tailnet only") {
		t.Fatalf("tailnet mode: %s", body)
	}
	// A restart opens no network listener.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if s := restarted.dashboardNetworkStatus(context.Background()); s != nil {
		t.Fatalf("restart opened the network dashboard: %+v", s)
	}
}

// A failed remote enable changes no dashboard setting and keeps the network listener; a
// failed write keeps it open and says so. The form needs its mode and CSRF token.
func TestDashboardAccessFailures(t *testing.T) {
	d, root, fake := accessFixture(t)
	client := newActionClient(t, d, root)
	if r := dashboardDo(t, d, "POST", "/dashboard/actions/dashboard-access", client.cookie, client.sameOrigin, url.Values{"mode": {"tailnet"}}); r.status == http.StatusSeeOther {
		t.Fatal("accepted without the CSRF token")
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/actions/dashboard-access", client.cookie, client.sameOrigin, url.Values{"csrf": {client.csrf}, "mode": {"lan"}}); r.status != http.StatusBadRequest {
		t.Fatalf("another mode: %d", r.status)
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/actions/dashboard-access", "", client.sameOrigin, url.Values{"csrf": {client.csrf}, "mode": {"tailnet"}}); r.status == http.StatusSeeOther {
		t.Fatal("accepted without a login")
	}
	fake.base.mu.Lock()
	fake.base.noSelf = true
	fake.base.mu.Unlock()
	if code := client.do("dashboard-access", url.Values{"mode": {"tailnet"}}); code != "remote_setup_required" {
		t.Fatal(code)
	}
	if a, _ := ReadDashboardAddress(root); a == nil || *a != accessAddress {
		t.Fatalf("address after a failed enable: %+v", a)
	}
	if local, _ := ReadDashboardLocal(root); local {
		t.Fatal("local choice saved after a failed enable")
	}
	if s := d.dashboardNetworkStatus(context.Background()); s == nil || s.Code != "running" {
		t.Fatalf("network after a failed enable: %+v", s)
	}
	fake.base.mu.Lock()
	fake.base.noSelf = false
	fake.base.mu.Unlock()
	// The address file cannot be removed: remote access is on, the network address stays.
	path := filepath.Join(root, "dashboard-address.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := client.do("dashboard-access", url.Values{"mode": {"tailnet"}}); code != "dashboard_access_partial" {
		t.Fatal(code)
	}
	if s := d.dashboardNetworkStatus(context.Background()); s == nil || s.Code != "running" {
		t.Fatalf("network after a partial switch: %+v", s)
	}
	if c, _ := ReadRemoteConfig(root); c == nil || !c.Enabled {
		t.Fatalf("remote after a partial switch: %+v", c)
	}
}

// On the network surface the switch answers with a page that links the tailnet URL; the
// response arrives, and the listener then refuses new connections. Status reads during the
// switch do not race with it.
func TestDashboardAccessFromTheNetworkSurface(t *testing.T) {
	d, root, _ := accessFixture(t)
	certificate, _, err := dashboardCertificate(root, accessAddress)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(certificate.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: accessAddress.Host}}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	addr := d.networkListener.Addr().String()
	origin := "https://" + accessAddress.Address()
	call := func(method, path string, form url.Values, cookie *http.Cookie) (*http.Response, string) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r, _ := http.NewRequest(method, "https://"+addr+path, body)
		r.Host = accessAddress.Address()
		r.Header.Set("Origin", origin)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response, string(data)
	}
	login, _ := call("POST", "/dashboard/login", url.Values{"secret": {testSecret}}, nil)
	if login.StatusCode != 303 || len(login.Cookies()) != 1 {
		t.Fatalf("login %d", login.StatusCode)
	}
	cookie := login.Cookies()[0]
	_, page := call("GET", "/dashboard/settings", nil, cookie)
	match := csrfField.FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("no CSRF")
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					d.dashboardNetworkStatus(context.Background())
				}
			}
		}()
	}
	response, body := call("POST", "/dashboard/actions/dashboard-access", url.Values{"csrf": {match[1]}, "mode": {"tailnet"}}, cookie)
	close(stop)
	wg.Wait()
	if response.StatusCode != 200 || !strings.Contains(body, `<a href="https://host.example.ts.net/dashboard/">`) || !strings.Contains(body, "This network address is closed.") {
		t.Fatalf("network surface answer: %d %s", response.StatusCode, body)
	}
	client.CloseIdleConnections()
	networkClosed(t, addr)
}
