package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func networkTestInterfaces() ([]net.Addr, error) {
	var addresses []net.Addr
	for _, value := range []string{"192.0.2.20/24", "192.0.2.10/24", "2001:db8::1/64", "127.0.0.1/8"} {
		ip, prefix, _ := net.ParseCIDR(value)
		prefix.IP = ip
		addresses = append(addresses, prefix)
	}
	return addresses, nil
}

func TestDashboardNetworkAddress(t *testing.T) {
	lookup := func(_ context.Context, name string) ([]net.IPAddr, error) {
		if name != "board.example.test" {
			return nil, errors.New("no DNS answer")
		}
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.20")}, {IP: net.ParseIP("198.51.100.1")}, {IP: net.ParseIP("192.0.2.10")}}, nil
	}
	for _, address := range []string{"0.0.0.0:443", "[::]:443", "127.0.0.1:443", "[::1]:443", "localhost:443", "board:443", "bad_name.example:443", "192.0.2.10:0", "192.0.2.10:65536", "198.51.100.1:443", "missing.example:443", "224.0.0.1:443"} {
		if a, err := resolveDashboardAddress(context.Background(), address, networkTestInterfaces, lookup); err == nil {
			t.Fatalf("accepted %s: %+v", address, a)
		}
	}
	a, err := resolveDashboardAddress(context.Background(), "BOARD.EXAMPLE.TEST.:8443", networkTestInterfaces, lookup)
	if err != nil || a != (DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}) {
		t.Fatalf("resolved: %+v %v", a, err)
	}
	for _, address := range []string{"192.0.2.20:8443", "[2001:db8::1]:8443"} {
		if _, err := resolveDashboardAddress(context.Background(), address, networkTestInterfaces, lookup); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "state")
	if err := WriteDashboardAddress(root, &a); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDashboardAddress(root)
	if err != nil || !reflect.DeepEqual(got, &a) {
		t.Fatalf("saved %+v %v", got, err)
	}
	path := filepath.Join(root, "dashboard-address.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDashboardAddress(root); err == nil {
		t.Fatal("read a public configuration")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDashboardAddress(root, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadDashboardAddress(root); err != nil || got != nil {
		t.Fatalf("removed %+v %v", got, err)
	}
}

func TestDashboardNetworkCertificate(t *testing.T) {
	root := t.TempDir()
	a := DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}
	first, fingerprint, err := dashboardCertificate(root, a)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil || cert.VerifyHostname(a.Host) != nil || cert.VerifyHostname(a.IP) != nil || len(fingerprint) != 64 {
		t.Fatalf("SAN/fingerprint: %v %v", cert, err)
	}
	again, same, err := dashboardCertificate(root, a)
	if err != nil || !reflect.DeepEqual(first.Certificate, again.Certificate) || fingerprint != same {
		t.Fatal("certificate changed across reads", err)
	}
	path := filepath.Join(root, "dashboard-tls.pem")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private key mode", info.Mode())
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dashboardCertificate(root, a); err == nil {
		t.Fatal("accepted a public private key")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	a.IP = "192.0.2.20"
	if _, changed, err := dashboardCertificate(root, a); err != nil || changed == fingerprint {
		t.Fatalf("new bind did not get a matching certificate: %s %v", changed, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dashboardCertificate(root, a); err == nil {
		t.Fatal("accepted a symbolic-link private key")
	}
}

// The production listener binds only its pinned address. This fixture redirects that bind to
// an ephemeral loopback socket, with synthetic interface evidence; no host network is exposed.
func TestDashboardNetworkHTTPS(t *testing.T) {
	d, root := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	a := DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}
	if err := WriteDashboardAddress(root, &a); err != nil {
		t.Fatal(err)
	}
	d.startDashboardNetwork(networkTestInterfaces, func(_, bind string) (net.Listener, error) {
		if bind != a.Bind() {
			t.Fatalf("bind %s", bind)
		}
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if d.networkStatus.Code != "running" {
		t.Fatalf("listener: %+v", d.networkStatus)
	}
	plain := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	response, err := plain.Get("http://" + d.networkListener.Addr().String() + "/dashboard/login")
	if err == nil {
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("plaintext accepted: %d", response.StatusCode)
		}
	}
	plain.CloseIdleConnections()
	// Default HTTPS port normalization is required for a browser on port 443: its Origin
	// omits that port. These requests exercise the same handler without binding a low port.
	a443 := a
	a443.Port = 443
	for _, host := range []string{a443.Host, a443.Address(), strings.ToUpper(a443.Host)} {
		r := httptest.NewRequest("POST", "https://"+host+"/dashboard/login", strings.NewReader(url.Values{"secret": {testSecret}}.Encode()))
		r.Header.Set("Origin", "https://"+a443.Host)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		d.networkDashboardHandler(a443).ServeHTTP(w, r)
		if w.Code != 303 {
			t.Fatalf("default HTTPS port %s: %d %s", host, w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != dashboardCookie+"_443" {
			t.Fatalf("default port cookie %+v", cookies)
		}
	}
	certificate, _, err := dashboardCertificate(root, a)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(certificate.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: a.Host}}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	call := func(method, path, host, origin string, form url.Values, cookie *http.Cookie) dashboardResponse {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r, err := http.NewRequest(method, "https://"+d.networkListener.Addr().String()+path, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
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
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return dashboardResponse{response.StatusCode, string(data), response.Header}
	}
	for _, path := range []string{"/v1/status", "/v1/dashboard/links", "/mcp", "/dashboard/../v1/status", "/dashboard/%2e%2e/v1/status"} {
		r := call("GET", path, a.Address(), "", nil, nil)
		if r.status == 200 || strings.Contains(r.body, `"daemon"`) {
			t.Fatalf("agent route %s: %+v", path, r)
		}
	}
	for _, host := range []string{"foreign.example:8443", "board.example.test:443", "127.0.0.1:8443", "localhost:8443"} {
		if r := call("GET", "/dashboard/login", host, "", nil, nil); r.status != 403 {
			t.Fatalf("host %s: %d", host, r.status)
		}
	}
	for _, origin := range []string{"http://" + a.Address(), "https://other.example:8443", "null", ""} {
		if r := call("POST", "/dashboard/login", a.Address(), origin, url.Values{"secret": {testSecret}}, nil); r.status != 403 {
			t.Fatalf("origin %q: %d", origin, r.status)
		}
	}
	if r := call("GET", "/dashboard/login", a.Bind(), "", nil, nil); r.status != 200 || strings.Contains(r.body, "one-time") {
		t.Fatalf("IP login: %+v", r)
	}
	bearer, _ := ReadSecret(root)
	link := dashboardLink(t, d, bearer)
	if r := call("GET", link, a.Address(), "", nil, nil); r.status != 403 || r.header.Get("Set-Cookie") != "" {
		t.Fatalf("network link %+v", r)
	}
	if r := dashboardDo(t, d, "GET", link, "", nil, nil); r.status != 303 {
		t.Fatalf("network consumed loopback link: %+v", r)
	}
	login := call("POST", "/dashboard/login", a.Address(), "https://"+a.Address(), url.Values{"secret": {testSecret}}, nil)
	cookies := (&http.Response{Header: login.header}).Cookies()
	if login.status != 303 || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("login %+v cookies %+v", login, cookies)
	}
	if r := call("GET", "/dashboard/sessions", a.Address(), "", nil, cookies[0]); r.status != 200 {
		t.Fatalf("session %+v", r)
	}
	if r := call("POST", "/dashboard/logout", a.Address(), "https://"+a.Address(), nil, cookies[0]); r.status != 403 {
		t.Fatalf("no CSRF %+v", r)
	}
	if _, err := d.changeSecret(context.Background(), "clear", ""); err != nil {
		t.Fatal(err)
	}
	if r := call("GET", "/dashboard/sessions", a.Address(), "", nil, cookies[0]); r.status != 503 {
		t.Fatalf("cleared secret: %+v", r)
	}
	if status := d.dashboardNetworkStatus(context.Background()); status.Code != "dashboard_secret_required" {
		t.Fatalf("status %+v", status)
	}
	// A full daemon restart reuses the private certificate and keeps the pinned address.
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	fingerprint := d.networkStatus.Fingerprint
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.startDashboardNetwork(networkTestInterfaces, func(_, _ string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") })
	if restarted.networkStatus.Fingerprint != fingerprint || restarted.networkStatus.Bind != a.Bind() {
		t.Fatalf("restart changed identity %+v", restarted.networkStatus)
	}
}

func TestDashboardNetworkUnavailableKeepsLoopback(t *testing.T) {
	d, root := startTestDaemon(t)
	a := DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}
	if err := WriteDashboardAddress(root, &a); err != nil {
		t.Fatal(err)
	}
	called := false
	listen := func(_, _ string) (net.Listener, error) {
		called = true
		return nil, errors.New("synthetic bind failure")
	}
	d.startDashboardNetwork(networkTestInterfaces, listen)
	if called || d.dashboardNetworkStatus(context.Background()).Code != "dashboard_secret_required" {
		t.Fatal("missing-secret bind", called)
	}
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	if status := d.dashboardNetworkStatus(context.Background()); status.Code != "dashboard_restart_required" || !strings.Contains(status.Step, "koinon install") {
		t.Fatalf("new secret %+v", status)
	}
	d.startDashboardNetwork(func() ([]net.Addr, error) { return nil, nil }, listen)
	if called || d.networkStatus.Code != "dashboard_address_unavailable" {
		t.Fatal("missing interface", called, d.networkStatus)
	}
	d.startDashboardNetwork(networkTestInterfaces, listen)
	if !called || d.networkStatus.Code != "dashboard_address_unavailable" {
		t.Fatal("bind failure not reported")
	}
	bearer, _ := ReadSecret(root)
	r := request(t, d, "/v1/status", "", bearer)
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("network failure stopped loopback", r.StatusCode)
	}
}

func TestDashboardNetworkAddressLoss(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	events := make(chan map[string]any, 4)
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, SecretCost: 1000,
		LogEvent: func(event string, fields map[string]any) {
			if event == "dashboard_network_unavailable" {
				events <- fields
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	a := DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}
	if err := WriteDashboardAddress(root, &a); err != nil {
		t.Fatal(err)
	}
	var lost atomic.Bool
	d.startDashboardNetwork(func() ([]net.Addr, error) {
		if lost.Load() {
			return nil, nil
		}
		return networkTestInterfaces()
	}, func(_, _ string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") })
	if status := d.dashboardNetworkStatus(context.Background()); status.Code != "running" {
		t.Fatalf("initial status %+v", status)
	}
	networkAddress := d.networkListener.Addr().String()
	lost.Store(true)
	bearer, _ := ReadSecret(root)
	for i := 0; i < 2; i++ {
		r := request(t, d, "/v1/status", "", bearer)
		var reply struct {
			Network *DashboardNetworkStatus `json:"dashboard_network"`
		}
		err := json.NewDecoder(r.Body).Decode(&reply)
		r.Body.Close()
		if err != nil || r.StatusCode != 200 || reply.Network == nil || reply.Network.Code != "dashboard_address_unavailable" || reply.Network.Bind != a.Bind() || reply.Network.Error == "" {
			t.Fatalf("lost interface: %+v %d %v", reply.Network, r.StatusCode, err)
		}
	}
	select {
	case event := <-events:
		if event["address"] != a.Bind() || event["code"] != "dashboard_address_unavailable" || event["error"] == "" {
			t.Fatalf("loss log %+v", event)
		}
	default:
		t.Fatal("address loss was not logged")
	}
	select {
	case event := <-events:
		t.Fatalf("address loss logged twice: %+v", event)
	default:
	}
	if conn, err := net.DialTimeout("tcp", networkAddress, time.Second); err == nil {
		conn.Close()
		t.Fatal("network listener stayed open after its IP disappeared")
	}
}
