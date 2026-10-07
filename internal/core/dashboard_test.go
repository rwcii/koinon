package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Dashboard tests: a real daemon on ephemeral loopback ports, temporary state and
// synthetic sessions; no browser.

type dashboardResponse struct {
	status int
	body   string
	header http.Header
}

func dashboardDo(t *testing.T, d *Daemon, method, path, cookie string, change func(*http.Request), form url.Values) dashboardResponse {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, err := http.NewRequest(method, "http://"+d.Addresses()[0]+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: dashboardCookie, Value: cookie})
	}
	if change != nil {
		change(r)
	}
	client := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return dashboardResponse{response.StatusCode, string(data), response.Header}
}

func dashboardLink(t *testing.T, d *Daemon, secret string) string {
	t.Helper()
	response := request(t, d, "/v1/dashboard/links", "{}", secret)
	var reply struct {
		Path      string `json:"path"`
		ExpiresIn int    `json:"expires_in"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&reply) != nil || reply.ExpiresIn != 60 ||
		!regexp.MustCompile(`^/dashboard/login\?token=[0-9a-f]{64}$`).MatchString(reply.Path) {
		t.Fatalf("link: %d %+v", response.StatusCode, reply)
	}
	return reply.Path
}

// dashboardLogin follows a fresh link and returns the session cookie.
func dashboardLogin(t *testing.T, d *Daemon, secret string) string {
	t.Helper()
	response := dashboardDo(t, d, "GET", dashboardLink(t, d, secret), "", nil, nil)
	if response.status != http.StatusSeeOther || response.header.Get("Location") != "/dashboard/sessions" {
		t.Fatalf("login: %d %v", response.status, response.header)
	}
	cookies := (&http.Response{Header: response.header}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != dashboardCookie {
		t.Fatalf("cookies: %v", cookies)
	}
	return cookies[0].Value
}

var csrfField = regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)

func TestDashboardLoginAndRequestProtection(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	// Without a login every page is refused and says how to log in; static files load.
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", "", nil, nil); r.status != 401 || !strings.Contains(r.body, "koinon dashboard") {
		t.Fatalf("no session: %d", r.status)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/static/app.js", "", nil, nil); r.status != 200 {
		t.Fatalf("static: %d", r.status)
	}
	// A link works once; a HEAD request, such as a link preview, does not use it.
	link := dashboardLink(t, d, secret)
	if r := dashboardDo(t, d, "HEAD", link, "", nil, nil); r.status != http.StatusMethodNotAllowed || r.header.Get("Set-Cookie") != "" {
		t.Fatalf("HEAD login: %d", r.status)
	}
	first := dashboardDo(t, d, "GET", link, "", nil, nil)
	if first.status != http.StatusSeeOther {
		t.Fatalf("first use: %d", first.status)
	}
	setCookie := first.header.Get("Set-Cookie")
	for _, attribute := range []string{"Path=/dashboard/", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(setCookie, attribute) {
			t.Fatalf("cookie lacks %s: %s", attribute, setCookie)
		}
	}
	if strings.Contains(setCookie, "Domain") {
		t.Fatalf("cookie has a domain: %s", setCookie)
	}
	if r := dashboardDo(t, d, "GET", link, "", nil, nil); r.status != 401 || r.header.Get("Set-Cookie") != "" {
		t.Fatalf("second use: %d", r.status)
	}
	for _, bad := range []string{"", "token=", "token=" + strings.Repeat("0", 64), "token=xyz"} {
		if r := dashboardDo(t, d, "GET", "/dashboard/login?"+bad, "", nil, nil); r.status != 401 {
			t.Fatalf("bad link %q: %d", bad, r.status)
		}
	}
	cookie := (&http.Response{Header: first.header}).Cookies()[0].Value
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil)
	if page.status != 200 {
		t.Fatalf("sessions: %d %s", page.status, page.body)
	}
	for name, want := range map[string]string{
		"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"X-Frame-Options":         "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"Cache-Control": "no-store", "Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := page.header.Get(name); got != want {
			t.Fatalf("%s = %q", name, got)
		}
	}
	match := csrfField.FindStringSubmatch(page.body)
	if match == nil {
		t.Fatal("page has no CSRF field")
	}
	token := match[1]
	if strings.Contains(page.body, secret) || strings.Contains(page.body, cookie) {
		t.Fatal("page carries the bearer secret or the session cookie")
	}
	// Host and Origin checks apply to every request, logged in or not.
	_, port, _ := strings.Cut(d.Addresses()[0], ":")
	for _, host := range []string{"localhost:" + port, "127.0.0.1:1", "attacker.example", "[::1]:" + port} {
		r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, func(r *http.Request) { r.Host = host }, nil)
		if r.status != 403 || r.header.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("host %s: %d", host, r.status)
		}
	}
	for _, origin := range []string{"http://attacker.example", "http://localhost:" + port, "https://" + d.Addresses()[0], "null"} {
		if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, func(r *http.Request) { r.Header.Set("Origin", origin) }, nil); r.status != 403 {
			t.Fatalf("origin %s: %d", origin, r.status)
		}
	}
	same := func(r *http.Request) { r.Header.Set("Origin", "http://"+d.Addresses()[0]) }
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, same, nil); r.status != 200 {
		t.Fatalf("own origin: %d", r.status)
	}
	// The other listener answers under its own address.
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, func(r *http.Request) {
		r.URL.Host, r.Host = d.Addresses()[1], d.Addresses()[1]
	}, nil); r.status != 200 {
		t.Fatalf("IPv6 listener: %d", r.status)
	}
	// A state-changing request needs a matching Origin and the CSRF token.
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookie, nil, url.Values{"csrf": {token}}); r.status != 403 {
		t.Fatalf("logout without origin: %d", r.status)
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookie, same, url.Values{}); r.status != 403 {
		t.Fatalf("logout without token: %d", r.status)
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookie, same, url.Values{"csrf": {strings.Repeat("a", 64)}}); r.status != 403 {
		t.Fatalf("logout with a wrong token: %d", r.status)
	}
	other := dashboardLogin(t, d, secret)
	otherToken := csrfField.FindStringSubmatch(dashboardDo(t, d, "GET", "/dashboard/health", other, nil, nil).body)[1]
	if otherToken == token {
		t.Fatal("two sessions share a CSRF token")
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookie, same, url.Values{"csrf": {otherToken}}); r.status != 403 {
		t.Fatalf("logout with another session's token: %d", r.status)
	}
	// The cookie never authorizes the API, and the bearer never authorizes the dashboard.
	if r := dashboardDo(t, d, "GET", "/v1/status", cookie, nil, nil); r.status != 401 {
		t.Fatalf("cookie on the API: %d", r.status)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+secret) }, nil); r.status != 401 {
		t.Fatalf("bearer on the dashboard: %d", r.status)
	}
	if r := dashboardDo(t, d, "GET", "/v1/status", "", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("Origin", "http://"+d.Addresses()[0])
	}, nil); r.status != 403 {
		t.Fatalf("API accepted a browser origin: %d", r.status)
	}
	// The positive flow: the page's token logs out, by form field or header, and the cookie
	// is refused afterwards.
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookie, same, url.Values{"csrf": {token}}); r.status != 200 || !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %v", r.status, r.header)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil); r.status != 401 {
		t.Fatalf("cookie after logout: %d", r.status)
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", other, func(r *http.Request) { same(r); r.Header.Set(dashboardCSRF, otherToken) }, nil); r.status != 200 {
		t.Fatalf("logout by header: %d", r.status)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/health", other, nil, nil); r.status != 401 {
		t.Fatalf("second cookie after logout: %d", r.status)
	}
	// A restart ends every login.
	third := dashboardLogin(t, d, secret)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0", "[::1]:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if r := dashboardDo(t, restarted, "GET", "/dashboard/sessions", third, nil, nil); r.status != 401 {
		t.Fatalf("cookie after restart: %d", r.status)
	}
}

func TestDashboardLinkAndSessionLifetimes(t *testing.T) {
	clock := time.Unix(1_800_000_000, 0)
	a, err := newDashboardAuth(func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	withCookie := func(id string) *http.Request {
		r, _ := http.NewRequest("GET", "http://127.0.0.1:1/dashboard/sessions", nil)
		r.AddCookie(&http.Cookie{Name: dashboardCookie, Value: id})
		return r
	}
	login := func() string {
		t.Helper()
		token, err := a.link()
		if err != nil {
			t.Fatal(err)
		}
		id, ok, err := a.login(token)
		if err != nil || !ok {
			t.Fatalf("login: %v %v", ok, err)
		}
		return id
	}
	// A link expires at 60 seconds.
	token, _ := a.link()
	clock = clock.Add(59 * time.Second)
	fresh, _ := a.link()
	clock = clock.Add(time.Second)
	if _, ok, _ := a.login(token); ok {
		t.Fatal("expired link accepted")
	}
	if _, ok, _ := a.login(fresh); !ok {
		t.Fatal("fresh link refused")
	}
	// At most eight links stay outstanding; the oldest is dropped.
	var links []string
	for range maxLoginLinks + 1 {
		token, _ := a.link()
		links = append(links, token)
		clock = clock.Add(time.Millisecond)
	}
	if _, ok, _ := a.login(links[0]); ok {
		t.Fatal("dropped link accepted")
	}
	if _, ok, _ := a.login(links[1]); !ok {
		t.Fatal("retained link refused")
	}
	// Idle expiry at 30 minutes; use keeps a session alive until 12 hours.
	idle := login()
	clock = clock.Add(sessionIdle)
	if _, ok := a.session(withCookie(idle)); ok {
		t.Fatal("idle session accepted")
	}
	busy := login()
	for elapsed := time.Duration(0); elapsed < sessionAbsolute-sessionIdle; elapsed += 20 * time.Minute {
		clock = clock.Add(20 * time.Minute)
		if _, ok := a.session(withCookie(busy)); !ok {
			t.Fatalf("active session refused after %v", elapsed)
		}
	}
	clock = clock.Add(sessionIdle - time.Minute)
	if _, ok := a.session(withCookie(busy)); ok {
		t.Fatal("session beyond 12 hours accepted")
	}
	// At most sixteen sessions; a new login ends the oldest.
	var ids []string
	for range maxDashboardLogin + 1 {
		ids = append(ids, login())
		clock = clock.Add(time.Millisecond)
	}
	if _, ok := a.session(withCookie(ids[0])); ok {
		t.Fatal("oldest session survived the cap")
	}
	for _, id := range ids[1:] {
		if _, ok := a.session(withCookie(id)); !ok {
			t.Fatal("newer session ended by the cap")
		}
	}
	// The CSRF token belongs to its session and changes with the per-start key.
	other, _ := newDashboardAuth(func() time.Time { return clock })
	if a.csrf(ids[1]) == a.csrf(ids[2]) || a.csrf(ids[1]) == other.csrf(ids[1]) || !a.validCSRF(ids[1], a.csrf(ids[1])) || a.validCSRF(ids[1], "") {
		t.Fatal("CSRF token is not bound to its session and start")
	}
}

func TestDashboardViewsRenderSyntheticState(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	repo := namedRepo(t, "viewrepo")
	a := join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	retired := join(t, s, "agy", "synthetic-gone", "")
	if _, err := s.Mutate(ctx, Mutation{Family: "agy", ID: "synthetic-gone", IfRevision: retired.Revision}, true); err != nil {
		t.Fatal(err)
	}
	// A session whose working directory differs from its repository.
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, Registration{Family: "claude", ID: "synthetic-sub", Directory: sub, TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, Observation{Caller: Key{"claude", "synthetic-sub"}, Terminal: &ObservedValue{Source: "tmux_env",
		At: s.now().UnixMilli(), Socket: "/tmp/tmux-synthetic/default", Pane: "%5", Session: "synthetic-work"}}); err != nil {
		t.Fatal(err)
	}
	hostile := `<script>alert("x")</script> <a href="javascript:alert(1)">link</a> 'quote' & more`
	for i, body := range []string{"first message", hostile, "third message"} {
		if _, err := s.Send(ctx, Key{"codex", "synthetic-a"}, b.Name, body); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if err := s.SetDelivery(ctx, 1, "failed", "synthetic_unreachable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(ctx, Key{"claude", "synthetic-b"}, 1); err != nil {
		t.Fatal(err)
	}
	m := MemoryCaller{Repository: "/synthetic/view/.git", Family: "codex", Name: a.Name, Consumer: "codex:synthetic-a"}
	open := create(t, s, m, "Synthetic open item")
	held := create(t, s, m, "Synthetic held item <b>bold</b>")
	mustStart(t, s, m, held, map[string]any{"resources": [][]string{{"path", "internal/core"}, {"exact", "db:schema"}}})
	cookie := dashboardLogin(t, d, secret)
	page := func(path string) string {
		t.Helper()
		r := dashboardDo(t, d, "GET", path, cookie, nil, nil)
		if r.status != 200 || r.header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		return r.body
	}
	contains := func(body string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in:\n%s", want, body)
			}
		}
	}
	sessions := page("/dashboard/sessions")
	contains(sessions, a.Name, b.Name, retired.Name, "state-retired", repo, "Synthetic held item &lt;b&gt;bold&lt;/b&gt;", held,
		"directory "+sub, "synthetic-work <code>%5</code>", "tmux_env, ", "unknown: terminal_unverified")
	if strings.Contains(sessions, "More sessions") {
		t.Fatal("a single page links to more")
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions?after=nofamily", cookie, nil, nil); r.status != 400 {
		t.Fatalf("bad session cursor: %d", r.status)
	}
	if strings.Contains(sessions, "<b>bold</b>") {
		t.Fatal("work title not escaped")
	}
	messages := page("/dashboard/messages")
	contains(messages, "first message", "third message", "failed", "synthetic_unreachable", b.Name,
		"&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", "&lt;a href=&#34;javascript:alert(1)&#34;&gt;", "&#39;quote&#39; &amp; more")
	if strings.Contains(messages, "<script>alert") || strings.Contains(messages, `href="javascript:`) {
		t.Fatal("message body not escaped")
	}
	if got := strings.Count(messages, "<td>yes</td>"); got != 1 {
		t.Fatalf("acknowledged messages: %d", got)
	}
	contains(page("/dashboard/messages?to="+b.Name), "first message")
	if body := page("/dashboard/messages?to=" + a.Name); strings.Contains(body, "first message") {
		t.Fatal("recipient filter ignored")
	}
	if body := page("/dashboard/messages?to=nobody"); !strings.Contains(body, "No messages.") {
		t.Fatal("unknown recipient listed messages")
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/messages?before=x", cookie, nil, nil); r.status != 400 {
		t.Fatalf("bad cursor: %d", r.status)
	}
	contains(page("/dashboard/memory"), "/synthetic/view/.git", "overdue, ")
	work := page("/dashboard/work")
	contains(work, open, held, "codex:synthetic-a", "internal/core", "db:schema", "valid until", "open", "active")
	health := page("/dashboard/health")
	contains(health, "running since", "schema 5", d.Addresses()[0], "1 retired")
	// A fragment is the list alone, for the in-place refresh.
	fragment := page("/dashboard/work?fragment=1")
	if strings.Contains(fragment, "<html") || !strings.Contains(fragment, held) {
		t.Fatalf("fragment: %s", fragment)
	}
	// The first page links to the sessions view.
	if r := dashboardDo(t, d, "GET", "/dashboard/", cookie, nil, nil); r.status != http.StatusSeeOther || r.header.Get("Location") != "/dashboard/sessions" {
		t.Fatalf("root: %d", r.status)
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/sessions", cookie, nil, nil); r.status == 200 {
		t.Fatal("POST to a view accepted")
	}
	// More sessions than one page: the page links to the next one.
	for i := range dashboardSessionPage {
		join(t, s, "codex", fmt.Sprintf("synthetic-many-%03d", i), "")
	}
	first := page("/dashboard/sessions")
	link := regexp.MustCompile(`href="/dashboard/sessions\?after=([^"]+)"`).FindStringSubmatch(first)
	if link == nil {
		t.Fatal("no link to the next page")
	}
	cursor, _ := url.QueryUnescape(strings.ReplaceAll(link[1], "&amp;", "&"))
	if second := page("/dashboard/sessions?after=" + url.QueryEscape(cursor)); !strings.Contains(second, "<tr") {
		t.Fatal("next page empty")
	}
}

func TestDashboardMessagePagingAcrossInboxes(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	repo := namedRepo(t, "pagerepo")
	sender := join(t, s, "codex", "synthetic-sender", repo)
	var names []string
	for _, id := range []string{"synthetic-x", "synthetic-y", "synthetic-z"} {
		names = append(names, join(t, s, "claude", id, repo).Name)
	}
	// Every inbox gets sequences 1..N at one and the same time, so neither the sequence nor
	// the time orders the listing.
	total := 0
	for round := range 47 {
		for i, name := range names {
			if round%(i+1) != 0 {
				continue
			}
			if _, err := s.Send(ctx, Key{"codex", "synthetic-sender"}, name, strings.Repeat("m", 10)); err != nil {
				t.Fatal(err)
			}
			total++
		}
	}
	_ = sender
	seen := map[int64]bool{}
	previous, before, pages := int64(1<<62), int64(0), 0
	for {
		page, next, err := s.dashboardMessages(ctx, nil, before)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page) > dashboardMessagePage {
			t.Fatalf("page of %d", len(page))
		}
		for _, m := range page {
			if seen[m.ID] || m.ID >= previous {
				t.Fatalf("duplicate or out of order: %d after %d", m.ID, previous)
			}
			seen[m.ID], previous = true, m.ID
		}
		if next == 0 {
			break
		}
		if next != page[len(page)-1].ID {
			t.Fatalf("cursor %d is not the last listed ID", next)
		}
		before = next
	}
	if len(seen) != total || pages < 2 {
		t.Fatalf("listed %d of %d in %d pages", len(seen), total, pages)
	}
	// One recipient pages through its own inbox only.
	k, err := s.sessionByName(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	count, before := 0, int64(0)
	for {
		page, next, err := s.dashboardMessages(ctx, k, before)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if m.RecipientID != "synthetic-x" {
				t.Fatalf("filter leaked %s", m.RecipientID)
			}
		}
		count += len(page)
		if next == 0 {
			break
		}
		before = next
	}
	if count != 47 {
		t.Fatalf("recipient listed %d of 47", count)
	}
	// Bodies bound a page at about one MiB, but a page always holds one message.
	big := strings.Repeat("b", maxBody)
	for range 20 {
		if _, err := s.Send(ctx, Key{"codex", "synthetic-sender"}, names[1], big); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := s.dashboardMessages(ctx, nil, 0)
	if err != nil || next == 0 || len(page) == 0 {
		t.Fatalf("large page: %d %d %v", len(page), next, err)
	}
	size := 0
	for _, m := range page {
		size += len(m.Body)
	}
	if size > 1<<20 && len(page) > 1 {
		t.Fatalf("page of %d bytes", size)
	}
	if _, _, err := s.dashboardMessages(ctx, nil, -1); err != ErrInvalid {
		t.Fatalf("negative cursor: %v", err)
	}
}
