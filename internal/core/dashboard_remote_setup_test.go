package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/remote"
	qrcode "github.com/skip2/go-qrcode"
)

const onboardingApprovalURL = "https://login.tailscale.com/f/serve?node=Synthetic-Approval_123"

type onboardingTailscale struct {
	base     *syntheticTailscale
	mu       sync.Mutex
	complete bool
	queries  int
}

func (s *onboardingTailscale) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "debug" {
		if !reflect.DeepEqual(args, []string{"debug", "localapi", "POST", "/localapi/v0/query-feature?feature=serve"}) {
			return nil, errors.New("unexpected synthetic setup query")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.queries++
		return json.Marshal(map[string]any{"Complete": s.complete, "URL": onboardingApprovalURL, "ShouldWait": !s.complete, "Text": "synthetic private provider prose"})
	}
	return s.base.Run(ctx, args...)
}

func onboardingFixture(t *testing.T) (*Daemon, string, *onboardingTailscale) {
	t.Helper()
	d, root, base := remoteFixture(t)
	fake := &onboardingTailscale{base: base, complete: true}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: fake}
	d.remote.mu.Unlock()
	return d, root, fake
}

func onboardingMutations(s *onboardingTailscale) int {
	s.base.mu.Lock()
	defer s.base.mu.Unlock()
	return s.base.mutations
}

func TestDashboardRemoteSetupAuthenticationAndWriteProtection(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	client := newActionClient(t, d, root)
	for _, path := range []string{"/dashboard/remote/status?fragment=1&setup=1", "/dashboard/remote/qr"} {
		if got := dashboardDo(t, d, "GET", path, "", nil, nil); got.status != 401 {
			t.Fatalf("anonymous %s: %d", path, got.status)
		}
		if got := dashboardDo(t, d, "GET", path, client.cookie, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, nil); got.status != 403 {
			t.Fatalf("foreign origin %s: %d", path, got.status)
		}
	}
	for _, action := range []string{"enable", "disable"} {
		path := "/dashboard/actions/remote-" + action
		for _, tc := range []struct {
			name, cookie, origin, csrf string
			want                       int
		}{
			{"anonymous", "", "same", client.csrf, 401},
			{"missing csrf", client.cookie, "same", "", 403},
			{"wrong csrf", client.cookie, "same", strings.Repeat("0", 64), 403},
			{"missing origin", client.cookie, "", client.csrf, 403},
			{"foreign origin", client.cookie, "https://evil.example", client.csrf, 403},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				change := func(r *http.Request) {
					if tc.origin == "same" {
						client.sameOrigin(r)
					} else if tc.origin != "" {
						r.Header.Set("Origin", tc.origin)
					}
				}
				got := dashboardDo(t, d, "POST", path, tc.cookie, change, url.Values{"csrf": {tc.csrf}})
				if got.status != tc.want {
					t.Fatalf("got %d, want %d", got.status, tc.want)
				}
			})
		}
		if got := dashboardDo(t, d, "GET", path, client.cookie, nil, nil); got.status != 405 {
			t.Fatalf("GET action: %d", got.status)
		}
	}
	if got := onboardingMutations(fake); got != 0 {
		t.Fatalf("refused requests mutated Serve %d times", got)
	}
	fake.mu.Lock()
	queries := fake.queries
	fake.mu.Unlock()
	if queries != 0 {
		t.Fatalf("refused setup inspected provider %d times", queries)
	}
}

func TestDashboardRemoteSetupReadOnlyApprovalAndReadyStates(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	client := newActionClient(t, d, root)
	fake.base.mu.Lock()
	fake.base.noHTTPS = true
	fake.base.mu.Unlock()
	fake.mu.Lock()
	fake.complete = false
	fake.mu.Unlock()
	// Ordinary settings/polling does not query account approval or change configuration.
	for _, path := range []string{"/dashboard/settings", "/dashboard/remote/status?fragment=1"} {
		got := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		if got.status != 200 {
			t.Fatalf("%s: %d %s", path, got.status, got.body)
		}
		if strings.Contains(got.body, onboardingApprovalURL) {
			t.Fatal("approval queried outside explicit setup")
		}
	}
	fake.mu.Lock()
	queries := fake.queries
	fake.mu.Unlock()
	if queries != 0 {
		t.Fatalf("normal read queried approval %d times", queries)
	}
	approval := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1&setup=1", client.cookie, nil, nil)
	if approval.status != 200 || !strings.Contains(approval.body, onboardingApprovalURL) || !strings.Contains(approval.body, "Open Tailscale approval") {
		t.Fatalf("approval missing: %d %s", approval.status, approval.body)
	}
	if strings.Contains(approval.body, "synthetic private provider prose") || strings.Contains(approval.body, `action="/dashboard/actions/remote-enable"`) {
		t.Fatal("approval leaks provider prose or offers enable before ready")
	}
	if got := dashboardDo(t, d, "GET", "/dashboard/remote/qr", client.cookie, nil, nil); got.status != 404 {
		t.Fatalf("QR while disabled: %d", got.status)
	}
	// Account approval alone is not a Serve mutation and never auto-submits enable.
	fake.base.mu.Lock()
	fake.base.noHTTPS = false
	fake.base.mu.Unlock()
	fake.mu.Lock()
	fake.complete = true
	fake.mu.Unlock()
	ready := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1&setup=1", client.cookie, nil, nil)
	if ready.status != 200 || !strings.Contains(ready.body, `action="/dashboard/actions/remote-enable"`) {
		t.Fatalf("ready state missing explicit action: %d %s", ready.status, ready.body)
	}
	redirect := dashboardDo(t, d, "GET", "/dashboard/remote/status?setup=1", client.cookie, nil, nil)
	location := redirect.header.Get("Location")
	if redirect.status != http.StatusSeeOther || !strings.HasPrefix(location, "/dashboard/settings?") || !strings.Contains(location, "remote_setup=1") || !strings.HasSuffix(location, "#remote-access") {
		t.Fatalf("no-JS setup redirect: %d %s", redirect.status, location)
	}
	full := dashboardDo(t, d, "GET", location, client.cookie, nil, nil)
	if full.status != 200 || !strings.Contains(full.body, "<h1>Settings</h1>") || !strings.Contains(full.body, "Go remote") || !strings.Contains(full.body, `action="/dashboard/actions/remote-enable"`) {
		t.Fatalf("no-JS setup page missing: %d %s", full.status, full.body)
	}
	if got := onboardingMutations(fake); got != 0 {
		t.Fatalf("setup reads changed Serve %d times", got)
	}
}

func TestDashboardRemoteSetupEnableURLOnlyQRAndDisable(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	client := newActionClient(t, d, root)
	fake.base.mu.Lock()
	fake.base.config.Web = map[string]remote.WebConfig{
		"host.example.ts.net:443":  {Handlers: map[string]remote.Handler{"/unrelated/": {Text: "synthetic unrelated service"}}},
		"host.example.ts.net:8443": {Handlers: map[string]remote.Handler{"/": {Proxy: "http://127.0.0.1:54321"}}},
	}
	fake.base.mu.Unlock()
	if code := client.do("remote-enable", url.Values{}); code != "remote_enabled" {
		t.Fatal(code)
	}
	config, err := ReadRemoteConfig(root)
	if err != nil || config == nil || !config.Enabled || !config.Owned {
		t.Fatalf("configuration not persisted: %+v %v", config, err)
	}
	if got := onboardingMutations(fake); got != 1 {
		t.Fatalf("enable mutations=%d", got)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1", client.cookie, nil, nil)
	expectedURL := config.Mapping.Origin + "/dashboard/"
	if page.status != 200 || !strings.Contains(page.body, expectedURL) || !strings.Contains(page.body, "Unverified") {
		t.Fatalf("enabled state: %d %s", page.status, page.body)
	}
	if strings.Contains(page.body, client.cookie) || strings.Contains(page.body, testSecret) || strings.Contains(page.body, "?token=") || strings.Contains(page.body, onboardingApprovalURL) {
		t.Fatal("enabled page leaks credential or obsolete approval link")
	}
	qr := dashboardDo(t, d, "GET", "/dashboard/remote/qr", client.cookie, nil, nil)
	if qr.status != 200 || qr.header.Get("Content-Type") != "image/png" || qr.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("QR response: %d %v", qr.status, qr.header)
	}
	image, err := png.Decode(strings.NewReader(qr.body))
	if err != nil || image.Bounds().Dx() != 256 || image.Bounds().Dy() != 256 {
		t.Fatalf("invalid QR image: %v", err)
	}
	// Byte equality to encoding exactly the stable URL proves there is no token,
	// password or additional payload in this generated QR; no decoder dependency needed.
	expectedPNG, err := qrcode.Encode(expectedURL, qrcode.Medium, 256)
	if err != nil || !bytes.Equal([]byte(qr.body), expectedPNG) {
		t.Fatal("QR differs from URL-only encoding", err)
	}
	if got := onboardingMutations(fake); got != 1 {
		t.Fatalf("URL/QR reads mutated Serve: %d", got)
	}
	if code := client.do("remote-disable", url.Values{}); code != "remote_disabled" {
		t.Fatal(code)
	}
	off, err := ReadRemoteConfig(root)
	if err != nil || off.Enabled || off.Owned {
		t.Fatalf("disable not persisted: %+v %v", off, err)
	}
	if qr := dashboardDo(t, d, "GET", "/dashboard/remote/qr", client.cookie, nil, nil); qr.status != 404 {
		t.Fatalf("QR retained after disable: %d", qr.status)
	}
	fake.base.mu.Lock()
	unrelatedPath := fake.base.config.Web["host.example.ts.net:443"].Handlers["/unrelated/"].Text
	unrelatedPort := fake.base.config.Web["host.example.ts.net:8443"].Handlers["/"].Proxy
	fake.base.mu.Unlock()
	if unrelatedPath != "synthetic unrelated service" || unrelatedPort != "http://127.0.0.1:54321" {
		t.Fatal("dashboard lifecycle changed unrelated Serve configuration")
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/settings", client.cookie, nil, nil); page.status != 200 {
		t.Fatal("local dashboard lost", page.status)
	}
}

func TestDashboardRemoteSetupConflictPreservesForeignMappings(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	client := newActionClient(t, d, root)
	foreign := remote.ServeConfig{TCP: map[string]remote.TCPConfig{"443": {HTTPS: true}}, Web: map[string]remote.WebConfig{"host.example.ts.net:443": {Handlers: map[string]remote.Handler{"/": {Proxy: "http://127.0.0.1:45678"}, "/unrelated/": {Text: "synthetic unrelated service"}}}}}
	fake.base.mu.Lock()
	fake.base.config = foreign
	before, _ := json.Marshal(fake.base.config)
	fake.base.mu.Unlock()
	if got := client.do("remote-enable", url.Values{}); got != "remote_setup_required" {
		t.Fatal(got)
	}
	fake.base.mu.Lock()
	after, _ := json.Marshal(fake.base.config)
	fake.base.mu.Unlock()
	if !bytes.Equal(before, after) || onboardingMutations(fake) != 0 {
		t.Fatal("conflicting enable changed foreign Serve state")
	}
	saved, err := ReadRemoteConfig(root)
	if err != nil || saved != nil {
		t.Fatalf("conflict journaled ownership: %+v %v", saved, err)
	}
}

func TestDashboardRemoteSetupDisableFromOwnListenerCompletes(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	local := newActionClient(t, d, root)
	if err := d.remoteEnable(context.Background()); err != nil {
		t.Fatal(err)
	}
	config, err := ReadRemoteConfig(root)
	if err != nil || config == nil {
		t.Fatal(config, err)
	}
	id, err := d.passwordLogin(context.Background(), testSecret, "remote:"+config.Mapping.Origin)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {d.auth.csrf(id)}}
	r, err := http.NewRequest("POST", config.Mapping.Target+"/dashboard/actions/remote-disable", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = strings.TrimPrefix(config.Mapping.Origin, "https://")
	r.Header.Set("Origin", config.Mapping.Origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "koinon_remote_dashboard", Value: id, Secure: true})
	httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()
	response, requestErr := httpClient.Do(r)
	if response != nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 303 {
			t.Fatalf("disable response %d", response.StatusCode)
		}
	}
	// Server.Close may close the requesting connection before it receives the PRG
	// response. Check persisted state and local availability, not a required redirect.
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, readErr := ReadRemoteConfig(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if current != nil && !current.Enabled && !current.Owned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote disable did not complete; request error: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.remote.mu.Lock()
	stillListening := d.remote.server != nil
	d.remote.mu.Unlock()
	if stillListening {
		t.Fatal("listener retained after disabling itself")
	}
	fake.base.mu.Lock()
	_, mapping := fake.base.config.Web["host.example.ts.net:443"].Handlers["/"]
	fake.base.mu.Unlock()
	if mapping {
		t.Fatal("owned forwarding remained")
	}
	conn, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(config.Mapping.Target, "http://"), 300*time.Millisecond)
	if conn != nil {
		conn.Close()
	}
	if dialErr == nil {
		t.Fatal("disabled listener still accepts connections")
	}
	if got := dashboardDo(t, d, "GET", "/dashboard/settings", local.cookie, nil, nil); got.status != 200 {
		t.Fatal("local dashboard failed after remote disable", got.status)
	}
}

// Reconciliation can close the listener serving this request. Run each case in
// a bounded subprocess: a lock regression must fail without hanging daemon
// cleanup or the rest of the suite.
func TestDashboardRemoteSettingsReconciliationCompletes(t *testing.T) {
	const childVariable = "KOINON_TEST_REMOTE_SETTINGS_RECONCILIATION"
	if scenario := os.Getenv(childVariable); scenario != "" {
		parts := strings.Split(scenario, "/")
		if len(parts) != 2 || (parts[0] != "remote_status" && parts[0] != "remote_setup") || (parts[1] != "identity" && parts[1] != "conflict") {
			t.Fatal("invalid synthetic settings scenario")
		}
		testRemoteSettingsReconciliation(t, parts[0], parts[1])
		return
	}
	for _, query := range []string{"remote_status", "remote_setup"} {
		for _, failure := range []string{"identity", "conflict"} {
			t.Run(query+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDashboardRemoteSettingsReconciliationCompletes$", "-test.timeout=10s")
				command.Env = append(os.Environ(), childVariable+"="+query+"/"+failure)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("remote settings reconciliation failed: %v\n%s", err, output)
				}
			})
		}
	}
}

func testRemoteSettingsReconciliation(t *testing.T, query, failure string) {
	t.Helper()
	d, root, fake := onboardingFixture(t)
	local := newActionClient(t, d, root)
	if err := d.remoteEnable(context.Background()); err != nil {
		t.Fatal(err)
	}
	config, err := ReadRemoteConfig(root)
	if err != nil || config == nil {
		t.Fatal(config, err)
	}
	id, err := d.passwordLogin(context.Background(), testSecret, "remote:"+config.Mapping.Origin)
	if err != nil {
		t.Fatal(err)
	}
	fake.base.mu.Lock()
	if failure == "identity" {
		fake.base.dns = "changed.example.ts.net."
	} else {
		web := fake.base.config.Web["host.example.ts.net:443"]
		web.Handlers["/"] = remote.Handler{Proxy: "http://127.0.0.1:45678"}
		fake.base.config.Web["host.example.ts.net:443"] = web
	}
	foreignBefore, _ := json.Marshal(fake.base.config)
	mutationsBefore := fake.base.mutations
	fake.base.mu.Unlock()
	request, err := http.NewRequest("GET", config.Mapping.Target+"/dashboard/settings?"+query+"=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = strings.TrimPrefix(config.Mapping.Origin, "https://")
	request.AddCookie(&http.Cookie{Name: "koinon_remote_dashboard", Value: id, Secure: true})
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, requestErr := client.Do(request)
	if response != nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("reconciling settings status %d", response.StatusCode)
		}
	}
	// Closing one's own listener may result in EOF before any response. The next
	// bounded operation is the evidence that reconciliation released its locks.
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.remote.mu.Lock()
		listening := d.remote.server != nil
		d.remote.mu.Unlock()
		if !listening {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconciliation left listener active; request error: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1", local.cookie, nil, nil); got.status != http.StatusOK {
		t.Fatal("local status unavailable after remote settings reconciliation", got.status)
	}
	if got := local.do("remote-disable", url.Values{}); got != "remote_setup_required" {
		t.Fatal("expected explicit cleanup conflict with foreign configuration preserved", got)
	}
	fake.base.mu.Lock()
	foreignAfter, _ := json.Marshal(fake.base.config)
	mutationsAfter := fake.base.mutations
	fake.base.mu.Unlock()
	if !bytes.Equal(foreignBefore, foreignAfter) || mutationsBefore != mutationsAfter {
		t.Fatal("status/disable changed conflicting Serve configuration")
	}
	// Repair only this synthetic adapter so the ordinary lifecycle can release its
	// journal and the test daemon can stop; no real Tailscale operation occurs.
	fake.base.mu.Lock()
	fake.base.dns = "host.example.ts.net."
	web := fake.base.config.Web["host.example.ts.net:443"]
	web.Handlers["/"] = remote.Handler{Proxy: config.Mapping.Target}
	fake.base.config.Web["host.example.ts.net:443"] = web
	fake.base.mu.Unlock()
	if got := local.do("remote-disable", url.Values{}); got != "remote_disabled" {
		t.Fatal("local disable did not recover after synthetic repair", got)
	}
	off, err := ReadRemoteConfig(root)
	if err != nil || off == nil || off.Enabled || off.Owned {
		t.Fatalf("cleanup journal not released: %+v %v", off, err)
	}
	if got := dashboardDo(t, d, "GET", "/dashboard/settings", local.cookie, nil, nil); got.status != http.StatusOK {
		t.Fatal("local dashboard lost after reconciliation", got.status)
	}
}
