package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRemoteDashboardBoundary(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	const origin = "https://host.example.ts.net"
	h := d.remoteDashboardHandler(origin)
	call := func(method, path, host, from string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:10001"+path, strings.NewReader(form.Encode()))
		r.Host = host
		if from != "" {
			r.Header.Set("Origin", from)
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// These never determine the accepted host or origin.
		r.Header.Set("X-Forwarded-Host", "host.example.ts.net")
		r.Header.Set("X-Forwarded-Proto", "https")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := url.Values{"secret": {testSecret}, "return": {"/dashboard/decisions?state=waiting"}}
	for _, tc := range []struct {
		path, host, from string
		code             int
	}{
		{"/v1/status", "host.example.ts.net", "", 404},
		{"/v1/dashboard/links", "host.example.ts.net", "", 404},
		{"/dashboard/login?token=abc", "host.example.ts.net", "", 403},
		{"/dashboard/login", "127.0.0.1:10001", origin, 403},
		{"/dashboard/login", "evil.example", origin, 403},
		{"/dashboard/login", "host.example.ts.net", "https://evil.example", 403},
		{"/dashboard/login", "host.example.ts.net", "", 403},
		{"/dashboard/login", "host.example.ts.net", "null", 403},
	} {
		if got := call("POST", tc.path, tc.host, tc.from, login, nil); got.Code != tc.code {
			t.Fatalf("%+v: %d %s", tc, got.Code, got.Body.String())
		}
	}
	if w := call("GET", "/", "host.example.ts.net", "", nil, nil); w.Code != 303 || w.Header().Get("Location") != "/dashboard/" {
		t.Fatal("root", w.Code, w.Header())
	}
	w := call("POST", "/dashboard/login", "host.example.ts.net", origin, login, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/dashboard/decisions?state=waiting" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "koinon_remote_dashboard" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/dashboard/" {
		t.Fatal(cookie)
	}
	page := call("GET", "/dashboard/sessions", "host.example.ts.net", "", nil, cookie)
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	match := csrfField.FindStringSubmatch(page.Body.String())
	if len(match) != 2 {
		t.Fatal("no CSRF")
	}
	if got := call("POST", "/dashboard/logout", "host.example.ts.net", origin, nil, cookie); got.Code != 403 {
		t.Fatal("no csrf", got.Code)
	}
	// A copied remote ID cannot authorize the local listener, even under its cookie name.
	local := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie.Value, nil, nil)
	if local.status != 401 {
		t.Fatal("remote session accepted locally", local.status)
	}
	id, err := d.auth.start("dashboard")
	if err != nil {
		t.Fatal(err)
	}
	copied := *cookie
	copied.Value = id
	if got := call("GET", "/dashboard/sessions", "host.example.ts.net", "", nil, &copied); got.Code != 401 {
		t.Fatal("local session accepted remotely", got.Code)
	}
	// The session is bound to its configured external origin, not just its cookie name.
	r := httptest.NewRequest("GET", "http://else.example.ts.net/dashboard/sessions", nil)
	r.Host = "else.example.ts.net"
	r.AddCookie(cookie)
	other := httptest.NewRecorder()
	d.remoteDashboardHandler("https://else.example.ts.net").ServeHTTP(other, r)
	if other.Code != 401 {
		t.Fatal("origin crossover", other.Code)
	}
	if got := call("POST", "/dashboard/logout", "host.example.ts.net", origin, url.Values{"csrf": {match[1]}}, cookie); got.Code != 200 {
		t.Fatal("logout", got.Code)
	}
	if got := call("GET", "/dashboard/sessions", "host.example.ts.net", "", nil, cookie); got.Code != 401 {
		t.Fatal("logout retained login", got.Code)
	}
}

func TestRemoteSessionInvalidation(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	id, err := d.auth.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://host.example.ts.net/dashboard/sessions", nil)
	r = r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, "https://host.example.ts.net"))
	r.AddCookie(&http.Cookie{Name: cookieName(r), Value: id})
	if _, ok := d.auth.session(r); !ok {
		t.Fatal("not authenticated")
	}
	d.auth.endRemote()
	if _, ok := d.auth.session(r); ok {
		t.Fatal("disable retained session")
	}
	// The refused page names the revocation for the browser's draft cleanup (#673 review M8).
	page := httptest.NewRecorder()
	d.dashboard.ServeHTTP(page, r)
	if page.Code != http.StatusUnauthorized || !strings.Contains(page.Body.String(), `data-auth-reason="remote_disabled"`) {
		t.Fatalf("refused page: %d, no data-auth-reason in %.300s", page.Code, page.Body.String())
	}
	id, _ = d.auth.start(dashboardClient(r))
	r.Header.Del("Cookie")
	r.AddCookie(&http.Cookie{Name: cookieName(r), Value: id})
	d.auth.now = func() time.Time { return d.store.now().Add(remoteSessionIdle + time.Second) }
	if _, ok := d.auth.session(r); ok {
		t.Fatal("idle login accepted")
	}
}

func TestDashboardSafeReturn(t *testing.T) {
	for _, v := range []string{"https://evil.example/dashboard/sessions", "//evil.example/dashboard/sessions", "/dashboard/login?token=x", "/v1/status", "/dashboard/logout", "/dashboard/../v1/status", "/dashboard/sessions#x", "/dashboard/sessions\\evil"} {
		if got := dashboardReturn(v); got != "" {
			t.Errorf("unsafe %q -> %q", v, got)
		}
	}
	if got := dashboardReturn("/dashboard/decisions?state=waiting&fragment=1"); got != "/dashboard/decisions?state=waiting" {
		t.Fatal(got)
	}
}

func TestRemoteActionsShareStateAndAttribution(t *testing.T) {
	d, root, _ := remoteFixture(t)
	local := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-remote-recipient", "")
	key := Key{"claude", peer.ID}
	fields := decisionFields("Synthetic remote verdict", "decision")
	fields.Recipients = &[]string{peer.Name}
	mustCreate(t, d.store, key, "REMOTE-1", fields)
	const origin = "https://host.example.ts.net"
	h := d.remoteDashboardHandler(origin)
	id, err := d.passwordLogin(context.Background(), testSecret, "remote:"+origin)
	if err != nil {
		t.Fatal(err)
	}
	post := func(action string, form url.Values) string {
		t.Helper()
		form.Set("csrf", d.auth.csrf(id))
		r := httptest.NewRequest("POST", origin+"/dashboard/actions/"+action, strings.NewReader(form.Encode()))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "koinon_remote_dashboard", Value: id})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 303 {
			t.Fatal(action, w.Code, w.Body.String())
		}
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return location.Query().Get("notice")
	}
	if got := post("send", url.Values{"to": {peer.Name}, "body": {"synthetic remote instruction"}}); got != "sent" {
		t.Fatal(got)
	}
	inbox, err := d.store.ReadInbox(context.Background(), key, 0, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].SenderFamily != "maintainer" || inbox.Messages[0].Body != "synthetic remote instruction" {
		t.Fatal(inbox, err)
	}
	if got := post("decision-approve", url.Values{"reference": {"REMOTE-1"}, "revision": {"1"}, "note": {"Synthetic approval"}}); got != "decision_approved" {
		t.Fatal(got)
	}
	decision, err := d.store.DecisionGet(context.Background(), key, "REMOTE-1")
	if err != nil || decision.Verdict == nil || decision.Verdict.Verdict != "approved" {
		t.Fatal(decision, err)
	}
	var via string
	if err := d.store.db.QueryRow(`SELECT via FROM decision_verdicts WHERE id=?`, decision.Verdict.ID).Scan(&via); err != nil || via != "dashboard" {
		t.Fatal("verdict attribution", via, err)
	}
	if got := local.do("decision-decline", url.Values{"reference": {"REMOTE-1"}, "revision": {"1"}}); got != "decided" {
		t.Fatal("concurrent local verdict", got)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/decisions?ref=REMOTE-1", local.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "Synthetic approval") {
		t.Fatal("local browser did not see remote verdict", page.status)
	}
	if _, err := d.changeSecret(context.Background(), "set", testSecret+"-changed"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", origin+"/dashboard/sessions", nil)
	r.AddCookie(&http.Cookie{Name: "koinon_remote_dashboard", Value: id})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("secret change retained remote login", w.Code)
	}
}
