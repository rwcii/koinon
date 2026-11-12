package core

import (
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pairing tests use temporary daemon state and a synthetic Tailscale adapter.
// They exercise the real dashboard router; no provider account or device is used.
func pairFixture(t *testing.T) (*Daemon, string, *actionClient, http.Handler, string) {
	t.Helper()
	d, root, _ := onboardingFixture(t)
	local := newActionClient(t, d, root)
	if err := d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	config, err := ReadRemoteConfig(root)
	if err != nil || config == nil {
		t.Fatal(config, err)
	}
	return d, root, local, d.remoteDashboardHandler(config.Mapping.Origin), config.Mapping.Origin
}

func pairRequest(h http.Handler, method, origin, path, from string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if from != "" {
		r.Header.Set("Origin", from)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDashboardPairTicketIssuanceProtection(t *testing.T) {
	d, _, local, _, _ := pairFixture(t)
	for _, tc := range []struct {
		name, cookie, origin, csrf string
		want                       int
	}{
		{"anonymous", "", "same", local.csrf, 401},
		{"missing csrf", local.cookie, "same", "", 403},
		{"wrong csrf", local.cookie, "same", strings.Repeat("0", 64), 403},
		{"missing origin", local.cookie, "", local.csrf, 403},
		{"foreign origin", local.cookie, "https://evil.example", local.csrf, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change := func(r *http.Request) {
				if tc.origin == "same" {
					local.sameOrigin(r)
				} else if tc.origin != "" {
					r.Header.Set("Origin", tc.origin)
				}
			}
			got := dashboardDo(t, d, "POST", "/dashboard/remote/ticket", tc.cookie, change, url.Values{"csrf": {tc.csrf}})
			if got.status != tc.want {
				t.Fatalf("status=%d, want %d", got.status, tc.want)
			}
			if got.header.Get("X-Koinon-Ticket-Expires") != "" {
				t.Fatal("refused issuance returned ticket metadata")
			}
		})
	}
	if got := dashboardDo(t, d, "GET", "/dashboard/remote/ticket", local.cookie, nil, nil); got.status != 405 {
		t.Fatal("GET issued ticket", got.status)
	}
	started := time.Now()
	got := dashboardDo(t, d, "POST", "/dashboard/remote/ticket", local.cookie, local.sameOrigin, url.Values{"csrf": {local.csrf}, "origin": {"https://evil.example"}})
	if got.status != 200 || got.header.Get("Content-Type") != "image/png" || got.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("issue=%d headers=%v", got.status, got.header)
	}
	if _, err := png.Decode(strings.NewReader(got.body)); err != nil {
		t.Fatal("issuance not PNG", err)
	}
	expires, err := time.Parse(time.RFC3339, got.header.Get("X-Koinon-Ticket-Expires"))
	if err != nil || expires.Before(started.Add(115*time.Second)) || expires.After(time.Now().Add(125*time.Second)) {
		t.Fatal("unexpected ticket expiry", got.header.Get("X-Koinon-Ticket-Expires"), err)
	}
	if got.header.Get("X-Koinon-Ticket-Lifetime") != "120" {
		t.Fatal("ticket lifetime", got.header.Get("X-Koinon-Ticket-Lifetime"))
	}
	for _, credential := range []string{local.cookie, testSecret, local.csrf} {
		if strings.Contains(got.body, credential) {
			t.Fatal("QR contains an existing session credential")
		}
	}
}

func TestDashboardPairOrdinaryURLAndRemoteHeader(t *testing.T) {
	d, _, local, h, origin := pairFixture(t)
	password := pairRequest(h, "GET", origin, "/dashboard/login", "", nil, nil)
	if password.Code != 200 || !strings.Contains(password.Body.String(), `type="password"`) || !strings.Contains(password.Body.String(), `action="/dashboard/login"`) {
		t.Fatalf("password fallback missing: %d %s", password.Code, password.Body.String())
	}
	for _, method := range []string{"GET", "POST"} {
		legacy := pairRequest(h, method, origin, "/dashboard/login?token="+strings.Repeat("a", 64), origin, url.Values{"secret": {testSecret}}, nil)
		if legacy.Code != 403 {
			t.Fatal("legacy local link accepted remotely", method, legacy.Code)
		}
	}
	id, err := d.passwordLogin(context.Background(), testSecret, "remote:"+origin)
	if err != nil {
		t.Fatal(err)
	}
	remotePage := pairRequest(h, "GET", origin, "/dashboard/sessions", "", nil, &http.Cookie{Name: "koinon_remote_dashboard", Value: id})
	if remotePage.Code != 200 || strings.Contains(remotePage.Body.String(), `class="remote-link"`) || strings.Contains(remotePage.Body.String(), ">Go remote</a>") {
		t.Fatalf("remote header still offers Go remote: %d", remotePage.Code)
	}
	localPage := dashboardDo(t, d, "GET", "/dashboard/sessions", local.cookie, nil, nil)
	if localPage.status != 200 || !strings.Contains(localPage.body, ">Go remote</a>") {
		t.Fatal("local header lost Go remote", localPage.status)
	}
	// The surface marker must come from trusted request context, not query/header data.
	spoofed := dashboardDo(t, d, "GET", "/dashboard/sessions?remote=1", local.cookie, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Host", strings.TrimPrefix(origin, "https://"))
		r.Header.Set("X-Forwarded-Proto", "https")
	}, nil)
	if spoofed.status != 200 || !strings.Contains(spoofed.body, ">Go remote</a>") {
		t.Fatal("untrusted request hid local control")
	}
}

func pairMint(t *testing.T, d *Daemon, issuer string) string {
	t.Helper()
	token, _, _, err := d.issueRemoteTicket(context.Background(), issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tokenHash(token); !ok {
		t.Fatal("ticket is not a256-bit token")
	}
	return token
}

func TestDashboardPairPreviewThenSingleUseSecureSession(t *testing.T) {
	d, _, local, h, origin := pairFixture(t)
	ticket := pairMint(t, d, local.cookie)
	for _, method := range []string{"GET", "HEAD"} {
		preview := pairRequest(h, method, origin, "/dashboard/pair?ticket="+ticket, "", nil, nil)
		if preview.Code != 200 || preview.Header().Get("Set-Cookie") != "" || preview.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s preview consumed or cached ticket: %d %v", method, preview.Code, preview.Header())
		}
		if strings.Contains(preview.Body.String(), ticket) {
			t.Fatal("preview reflected token into HTML")
		}
	}
	form := url.Values{"ticket": {ticket}, "return": {"https://evil.example/steal"}}
	redeemed := pairRequest(h, "POST", origin, "/dashboard/pair?return=/dashboard/actions/remote-disable", origin, form, nil)
	if redeemed.Code != 303 || redeemed.Header().Get("Location") != "/dashboard/sessions" {
		t.Fatalf("redemption: %d %s", redeemed.Code, redeemed.Body.String())
	}
	cookies := redeemed.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing remote session", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "koinon_remote_dashboard" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/dashboard/" || cookie.Domain != "" || cookie.Value == ticket {
		t.Fatal("invalid paired session cookie", cookie)
	}
	remotePage := pairRequest(h, "GET", origin, "/dashboard/sessions", "", nil, cookie)
	if remotePage.Code != 200 {
		t.Fatal("paired remote session not usable", remotePage.Code)
	}
	if copied := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie.Value, nil, nil); copied.status != 401 {
		t.Fatal("paired remote cookie authorized local surface", copied.status)
	}
	replay := pairRequest(h, "POST", origin, "/dashboard/pair", origin, url.Values{"ticket": {ticket}}, nil)
	if replay.Code != 401 || replay.Header().Get("Set-Cookie") != "" {
		t.Fatal("ticket replay authorized", replay.Code)
	}
	for _, record := range auditAll(t, d.store) {
		if strings.Contains(record.Target, ticket) || strings.Contains(record.Target, cookie.Value) {
			t.Fatal("audit persisted credential")
		}
	}
}

func TestDashboardPairWrongOriginHostAndSurfaceCannotConsume(t *testing.T) {
	d, _, local, h, origin := pairFixture(t)
	ticket := pairMint(t, d, local.cookie)
	form := url.Values{"ticket": {ticket}}
	for _, from := range []string{"", "null", "https://evil.example"} {
		got := pairRequest(h, "POST", origin, "/dashboard/pair", from, form, nil)
		if got.Code != 403 || got.Header().Get("Set-Cookie") != "" {
			t.Fatal("cross-origin redemption accepted", from, got.Code)
		}
	}
	wrongHost := pairRequest(h, "POST", "https://evil.example", "/dashboard/pair", origin, form, nil)
	if wrongHost.Code != 403 {
		t.Fatal("foreign host accepted", wrongHost.Code)
	}
	otherOrigin := "https://another.example.ts.net"
	wrongSurface := pairRequest(d.remoteDashboardHandler(otherOrigin), "POST", otherOrigin, "/dashboard/pair", otherOrigin, form, nil)
	if wrongSurface.Code != 401 || wrongSurface.Header().Get("Set-Cookie") != "" {
		t.Fatal("ticket crossed configured origin", wrongSurface.Code)
	}
	localRedeem := dashboardDo(t, d, "POST", "/dashboard/pair", "", local.sameOrigin, form)
	if localRedeem.status != 404 || localRedeem.header.Get("Set-Cookie") != "" {
		t.Fatal("remote ticket redeemed locally", localRedeem.status)
	}
	for _, method := range []string{"GET", "HEAD"} {
		localPreview := dashboardDo(t, d, method, "/dashboard/pair?ticket="+ticket, "", nil, nil)
		if localPreview.status != 404 {
			t.Fatal("pair landing exposed locally", method, localPreview.status)
		}
	}
	// Refused attempts on another origin/surface cannot burn the legitimate code.
	valid := pairRequest(h, "POST", origin, "/dashboard/pair", origin, form, nil)
	if valid.Code != 303 {
		t.Fatal("refused attempt consumed original ticket", valid.Code)
	}
}

func TestDashboardPairMalformedAndExpiredCodes(t *testing.T) {
	d, _, local, h, origin := pairFixture(t)
	for _, form := range []url.Values{
		{}, {"ticket": {"not-a-ticket"}}, {"ticket": {strings.Repeat("0", 64)}}, {"ticket": {"a", "b"}},
	} {
		got := pairRequest(h, "POST", origin, "/dashboard/pair", origin, form, nil)
		if (got.Code != 400 && got.Code != 401) || got.Header().Get("Set-Cookie") != "" {
			t.Fatal("malformed code accepted", got.Code)
		}
	}
	ticket := pairMint(t, d, local.cookie)
	d.auth.mu.Lock()
	previousClock := d.auth.now
	future := previousClock().Add(2*time.Minute + time.Second)
	d.auth.now = func() time.Time { return future }
	d.auth.mu.Unlock()
	defer func() { d.auth.mu.Lock(); d.auth.now = previousClock; d.auth.mu.Unlock() }()
	got := pairRequest(h, "POST", origin, "/dashboard/pair", origin, url.Values{"ticket": {ticket}}, nil)
	if got.Code != 401 || got.Header().Get("Set-Cookie") != "" {
		t.Fatal("expired code accepted", got.Code)
	}
}

func TestDashboardPairConcurrentRedemptionCreatesOneSession(t *testing.T) {
	d, _, local, h, origin := pairFixture(t)
	ticket := pairMint(t, d, local.cookie)
	d.auth.mu.Lock()
	before := len(d.auth.sessions)
	d.auth.mu.Unlock()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 12)
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			responses <- pairRequest(h, "POST", origin, "/dashboard/pair", origin, url.Values{"ticket": {ticket}}, nil)
		}()
	}
	close(start)
	group.Wait()
	close(responses)
	accepted := 0
	for got := range responses {
		if got.Code == 303 {
			accepted++
			if len(got.Result().Cookies()) != 1 {
				t.Fatal("accepted redemption missing cookie")
			}
		} else if got.Code != 401 || got.Header().Get("Set-Cookie") != "" {
			t.Fatal("unexpected losing redemption", got.Code)
		}
	}
	d.auth.mu.Lock()
	after := len(d.auth.sessions)
	d.auth.mu.Unlock()
	if accepted != 1 || after != before+1 {
		t.Fatalf("accepted=%d sessions before=%d after=%d", accepted, before, after)
	}
}

func TestDashboardPairRevokedByDisableOrSecretChange(t *testing.T) {
	for _, action := range []string{"disable", "secret change"} {
		t.Run(action, func(t *testing.T) {
			d, _, local, h, origin := pairFixture(t)
			ticket := pairMint(t, d, local.cookie)
			if action == "disable" {
				if err := d.remoteDisable(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := d.changeSecret(context.Background(), "set", testSecret+"-changed"); err != nil {
					t.Fatal(err)
				}
			}
			got := pairRequest(h, "POST", origin, "/dashboard/pair", origin, url.Values{"ticket": {ticket}}, nil)
			if got.Code != 401 || got.Header().Get("Set-Cookie") != "" {
				t.Fatal("revoked code accepted", got.Code)
			}
			if action == "disable" {
				refused := dashboardDo(t, d, "POST", "/dashboard/remote/ticket", local.cookie, local.sameOrigin, url.Values{"csrf": {local.csrf}})
				if refused.status != 409 {
					t.Fatal("issued ticket after disabling remote access", refused.status)
				}
			}
		})
	}
}

// A remote session cannot issue a sign-in code: a chain of codes would renew it past
// its seven-day limit and fill the login table without the secret.
func TestDashboardPairRemoteSessionCannotIssue(t *testing.T) {
	d, _, _, h, origin := pairFixture(t)
	id, err := d.passwordLogin(context.Background(), testSecret, "remote:"+origin)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "koinon_remote_dashboard", Value: id}
	got := pairRequest(h, "POST", origin, "/dashboard/remote/ticket", origin, url.Values{"csrf": {d.auth.csrf(id)}}, cookie)
	if got.Code != http.StatusConflict || got.Header().Get("X-Koinon-Ticket-Expires") != "" {
		t.Fatalf("remote session issued a ticket: %d %v", got.Code, got.Header())
	}
	if _, _, err := d.auth.newRemoteTicket(origin, id); err != ErrUnavailable {
		t.Fatal("remote issuer accepted", err)
	}
	page := pairRequest(h, "GET", origin, "/dashboard/remote/status?fragment=1", "", nil, cookie)
	if page.Code != 200 || strings.Contains(page.Body.String(), "data-remote-ticket") || !strings.Contains(page.Body.String(), "only on this computer") {
		t.Fatalf("remote surface offers a sign-in code: %d", page.Code)
	}
}
