package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The maintainer secret (PDR 009, Decisions 1 and 2): the stored key, the CLI route, the
// password login and its wait.

const testSecret = "correct horse battery staple"

// TestMaintainerSecretKey checks the production cost and PBKDF2-HMAC-SHA256 against
// published test vectors; the other tests use a lower cost.
func TestMaintainerSecretKey(t *testing.T) {
	for _, c := range []struct {
		iterations int
		want       string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	} {
		key, err := deriveSecret("password", []byte("salt"), c.iterations)
		if err != nil || hex.EncodeToString(key) != c.want {
			t.Fatalf("PBKDF2 with %d iterations: %x %v", c.iterations, key, err)
		}
	}
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if secretIterations != 600000 || s.iterations() != 600000 {
		t.Fatalf("production cost %d", s.iterations())
	}
	if err := s.setMaintainerSecret(context.Background(), testSecret, "set"); err != nil {
		t.Fatal(err)
	}
	var algorithm string
	var iterations, salt, key int
	if err := s.db.QueryRow(`SELECT algorithm,iterations,length(salt),length(derived_key) FROM maintainer_secret`).Scan(&algorithm, &iterations, &salt, &key); err != nil ||
		algorithm != "pbkdf2-sha256" || iterations != 600000 || salt != 16 || key != 32 {
		t.Fatalf("stored key: %s %d %d %d %v", algorithm, iterations, salt, key, err)
	}
	if ok, err := s.matchMaintainerSecret(context.Background(), testSecret); !ok || err != nil {
		t.Fatalf("match: %v %v", ok, err)
	}
}

func TestValidMaintainerSecret(t *testing.T) {
	for secret, want := range map[string]bool{
		"":                             false,
		"elevenchars":                  false,
		"twelve chars":                 true,
		"  spaced  out  ":              true,
		"ünïcödé-ÿ€ß":                  false, // 11 characters
		"ünïcödé-ÿ€ßx":                 true,
		strings.Repeat("a", 1024):      true,
		strings.Repeat("a", 1025):      false,
		strings.Repeat("é", 512):       true, // 1024 bytes
		strings.Repeat("é", 512) + "a": false,
		"twelve chars\xff":             false,
	} {
		if ValidMaintainerSecret(secret) != want {
			t.Errorf("ValidMaintainerSecret(%q) = %v", secret, !want)
		}
	}
	generated, err := GenerateMaintainerSecret()
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(generated) || !ValidMaintainerSecret(generated) {
		t.Fatalf("generated %q %v", generated, err)
	}
}

func secretCall(t *testing.T, d *Daemon, bearer, body string) (int, map[string]any) {
	t.Helper()
	response := request(t, d, "/v1/maintainer/secret", body, bearer)
	defer response.Body.Close()
	var reply map[string]any
	json.NewDecoder(response.Body).Decode(&reply)
	return response.StatusCode, reply
}

// storeBytes is the store's files as they are on disk.
func storeBytes(t *testing.T, root string) string {
	t.Helper()
	var all strings.Builder
	for _, name := range []string{"state.sqlite3", "state.sqlite3-wal"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		all.Write(data)
	}
	return all.String()
}

func auditRows(t *testing.T, d *Daemon) string {
	t.Helper()
	rows, err := d.store.db.Query(`SELECT action||' '||target||' '||result||' '||reason FROM audit ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		rows.Scan(&line)
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, "\n")
}

func TestMaintainerSecretRoute(t *testing.T) {
	d, root := startTestDaemon(t)
	d.store.secretCost = 1000
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	// The route takes the bearer secret only: no browser origin, and a dashboard login
	// alone is no credential for it.
	cookie := dashboardLogin(t, d, bearer)
	browser := dashboardDo(t, d, "POST", "/v1/maintainer/secret", cookie, func(r *http.Request) {
		r.Header.Set("Origin", "http://"+r.Host)
		r.Header.Set("Content-Type", "application/json")
	}, nil)
	cookieOnly := dashboardDo(t, d, "POST", "/v1/maintainer/secret", cookie, func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
	}, nil)
	if browser.status != 403 || cookieOnly.status != 401 {
		t.Fatalf("browser %d, cookie only %d", browser.status, cookieOnly.status)
	}
	for _, body := range []string{`{"action":"set","secret":"short"}`, `{"action":"set","secret":"` + strings.Repeat("a", 1025) + `"}`,
		`{"action":"generate","secret":"` + testSecret + `"}`, `{"action":"rotate"}`, `{"action":"set","secret":"` + testSecret + `","extra":1}`} {
		if status, reply := secretCall(t, d, bearer, body); status != 400 || reply["code"] != "invalid_request" {
			t.Fatalf("%s: %d %v", body, status, reply)
		}
	}
	if status, reply := secretCall(t, d, bearer, `{"action":"clear"}`); status != 409 || reply["code"] != "maintainer_secret_unset" {
		t.Fatalf("clear without a secret: %d %v", status, reply)
	}
	if count(t, d.store, `SELECT COUNT(*) FROM maintainer_secret`) != 0 {
		t.Fatal("a refused change stored a key")
	}
	if status, reply := secretCall(t, d, bearer, `{"action":"set","secret":"`+testSecret+`"}`); status != 200 || reply["secret"] != nil || reply["logins_ended"] != true {
		t.Fatalf("set: %d %v", status, reply)
	}
	// Setting the secret ended the earlier login.
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil); r.status != 401 {
		t.Fatalf("login after set: %d", r.status)
	}
	if ok, _ := d.store.matchMaintainerSecret(context.Background(), testSecret); !ok {
		t.Fatal("set secret does not match")
	}
	cookie = dashboardLogin(t, d, bearer)
	status, reply := secretCall(t, d, bearer, `{"action":"generate"}`)
	generated, _ := reply["secret"].(string)
	if status != 200 || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(generated) {
		t.Fatalf("generate: %d %v", status, reply)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil); r.status != 401 {
		t.Fatalf("login after generate: %d", r.status)
	}
	if ok, _ := d.store.matchMaintainerSecret(context.Background(), testSecret); ok {
		t.Fatal("the replaced secret still matches")
	}
	if ok, _ := d.store.matchMaintainerSecret(context.Background(), generated); !ok {
		t.Fatal("generated secret does not match")
	}
	cookie = dashboardLogin(t, d, bearer)
	if status, reply := secretCall(t, d, bearer, `{"action":"clear"}`); status != 200 || reply["logins_ended"] != true {
		t.Fatalf("clear: %d %v", status, reply)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil); r.status != 401 {
		t.Fatalf("login after clear: %d", r.status)
	}
	if want := "maintainer_secret set by cli accepted\nmaintainer_secret generate by cli accepted\nmaintainer_secret clear by cli accepted"; !strings.Contains(auditRows(t, d), want) {
		t.Fatalf("audit:\n%s", auditRows(t, d))
	}
	d.store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if files := storeBytes(t, root); strings.Contains(files, testSecret) || strings.Contains(files, generated) {
		t.Fatal("the store holds a secret")
	}
}

// passwordLogin posts the login form with an Origin of the request's own Host.
func passwordForm(t *testing.T, d *Daemon, secret string, change func(*http.Request)) dashboardResponse {
	t.Helper()
	return dashboardDo(t, d, "POST", "/dashboard/login", "", func(r *http.Request) {
		r.Header.Set("Origin", "http://"+r.Host)
		if change != nil {
			change(r)
		}
	}, url.Values{"secret": {secret}})
}

func TestDashboardPasswordLogin(t *testing.T) {
	d, root := startTestDaemon(t)
	d.store.secretCost = 1000
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	// Without a secret the page says how to set one; the form is not shown, and a post is
	// refused.
	page := dashboardDo(t, d, "GET", "/dashboard/login", "", nil, nil)
	if page.status != 200 || strings.Contains(page.body, `name="secret"`) || !strings.Contains(page.body, "koinon maintainer secret set") {
		t.Fatalf("login page without a secret: %d %s", page.status, page.body)
	}
	if r := passwordForm(t, d, testSecret, nil); r.status != 401 || !strings.Contains(r.body, "No maintainer secret is set") {
		t.Fatalf("post without a secret: %d", r.status)
	}
	if status, _ := secretCall(t, d, bearer, `{"action":"set","secret":"`+testSecret+`"}`); status != 200 {
		t.Fatal("set")
	}
	page = dashboardDo(t, d, "GET", "/dashboard/login", "", nil, nil)
	if page.status != 200 || !strings.Contains(page.body, `<input type="password" name="secret" autocomplete="current-password"`) ||
		!strings.Contains(page.body, `action="/dashboard/login"`) {
		t.Fatalf("login form: %d %s", page.status, page.body)
	}
	// The request checks apply before the secret is read.
	for name, change := range map[string]func(*http.Request){
		"missing origin": func(r *http.Request) { r.Header.Del("Origin") },
		"foreign origin": func(r *http.Request) { r.Header.Set("Origin", "http://example.test") },
		"null origin":    func(r *http.Request) { r.Header.Set("Origin", "null") },
		"foreign host": func(r *http.Request) {
			_, port, _ := strings.Cut(r.Host, ":")
			r.Host = "localhost:" + port
			r.Header.Set("Origin", "http://"+r.Host)
		},
	} {
		if r := passwordForm(t, d, testSecret, change); r.status != 403 || r.header.Get("Set-Cookie") != "" {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	wrong := "wrong secret " + strings.Repeat("z", 20)
	if r := passwordForm(t, d, wrong, nil); r.status != 401 || r.header.Get("Set-Cookie") != "" ||
		!strings.Contains(r.body, "not correct") || strings.Contains(r.body, wrong) || strings.Contains(r.body, "zzzz") {
		t.Fatalf("wrong secret: %d %s", r.status, r.body)
	}
	big := dashboardDo(t, d, "POST", "/dashboard/login", "", func(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) },
		url.Values{"secret": {strings.Repeat("a", loginFormLimit)}})
	if big.status != 400 {
		t.Fatalf("oversized form: %d", big.status)
	}
	login := passwordForm(t, d, testSecret, nil)
	if login.status != http.StatusSeeOther || login.header.Get("Location") != "/dashboard/sessions" {
		t.Fatalf("login: %d %v", login.status, login.header)
	}
	cookies := (&http.Response{Header: login.header}).Cookies()
	_, port, _ := strings.Cut(d.Addresses()[0], ":")
	if len(cookies) != 1 || cookies[0].Name != dashboardCookie+"_"+port || cookies[0].MaxAge != int(sessionAbsolute.Seconds()) ||
		!cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/dashboard/" {
		t.Fatalf("cookie: %+v", cookies)
	}
	board := dashboardDo(t, d, "GET", "/dashboard/settings", cookies[0].Value, nil, nil)
	if board.status != 200 {
		t.Fatalf("board: %d", board.status)
	}
	// An action still needs its CSRF token.
	if r := dashboardDo(t, d, "POST", "/dashboard/logout", cookies[0].Value, func(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }, url.Values{}); r.status != 403 {
		t.Fatalf("action without CSRF: %d", r.status)
	}
	audit := auditRows(t, d)
	if !strings.Contains(audit, "maintainer_login dashboard refused secret_invalid") || !strings.Contains(audit, "maintainer_login dashboard accepted") ||
		strings.Contains(audit, testSecret) || strings.Contains(audit, wrong) {
		t.Fatalf("audit:\n%s", audit)
	}
	d.store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if files := storeBytes(t, root); strings.Contains(files, testSecret) || strings.Contains(files, wrong) {
		t.Fatal("the store holds a presented secret")
	}
}

// secretDaemon is a daemon's store and login state without listeners, on a set clock.
func secretDaemon(t *testing.T) (*Daemon, func(time.Duration)) {
	t.Helper()
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.secretCost = 1000
	var mu sync.Mutex
	clock := time.Unix(1_800_000_000, 0)
	auth, err := newDashboardAuth(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setMaintainerSecret(context.Background(), testSecret, "set"); err != nil {
		t.Fatal(err)
	}
	return &Daemon{store: s, auth: auth}, func(step time.Duration) {
		mu.Lock()
		clock = clock.Add(step)
		mu.Unlock()
	}
}

func TestMaintainerSecretBackoff(t *testing.T) {
	d, advance := secretDaemon(t)
	ctx := context.Background()
	fail := func() error {
		_, err := d.passwordLogin(ctx, "not the secret at all", "dashboard")
		return err
	}
	for i := 1; i <= 4; i++ {
		if err := fail(); !errors.Is(err, ErrSecretInvalid) {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	// The fifth failure in a row starts the wait; each later checked failure doubles it.
	for _, wait := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		if err := fail(); !errors.Is(err, ErrSecretInvalid) {
			t.Fatalf("checked failure: %v", err)
		}
		before := d.auth.gate.retry
		advance(wait*time.Second - time.Millisecond)
		// An attempt during the wait computes no key and leaves the wait as it is, also with
		// the correct secret.
		for _, secret := range []string{"not the secret at all", testSecret} {
			var backoff SecretBackoff
			if _, err := d.passwordLogin(ctx, secret, "dashboard"); !errors.As(err, &backoff) || !backoff.RetryAfter.Equal(before) {
				t.Fatalf("during a %ds wait: %v", wait, err)
			}
		}
		if d.auth.gate.retry != before {
			t.Fatal("a refused attempt moved the wait")
		}
		advance(time.Millisecond)
	}
	// The one-time link still works during a wait.
	fail()
	link, err := d.auth.link()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.auth.login(link); !ok || err != nil {
		t.Fatalf("link during the wait: %v %v", ok, err)
	}
	advance(secretMaxWait)
	// A success resets the count: the next four failures have no wait.
	if id, err := d.passwordLogin(ctx, testSecret, "dashboard"); err != nil || id == "" {
		t.Fatalf("login: %v", err)
	}
	for i := 1; i <= 4; i++ {
		if err := fail(); !errors.Is(err, ErrSecretInvalid) {
			t.Fatalf("failure %d after a success: %v", i, err)
		}
	}
	if !d.auth.gate.retry.IsZero() {
		t.Fatal("a wait after four failures")
	}
}

// Concurrent attempts are checked one at a time, so none gets past the count.
func TestMaintainerSecretConcurrentAttempts(t *testing.T) {
	d, _ := secretDaemon(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked, waited := 0, 0
	for range 20 {
		wg.Go(func() {
			_, err := d.passwordLogin(context.Background(), "not the secret at all", "dashboard")
			var backoff SecretBackoff
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrSecretInvalid):
				checked++
			case errors.As(err, &backoff):
				waited++
			default:
				t.Errorf("attempt: %v", err)
			}
		})
	}
	wg.Wait()
	if checked != 5 || waited != 15 {
		t.Fatalf("checked %d, waited %d", checked, waited)
	}
}

// A check against the key that a change replaces never leaves a login after the change.
func TestMaintainerSecretChangeEndsOverlappingLogin(t *testing.T) {
	d, _ := secretDaemon(t)
	ctx := context.Background()
	for i := range 20 {
		var wg sync.WaitGroup
		wg.Go(func() { d.passwordLogin(ctx, testSecret, "dashboard") })
		action := map[bool]string{true: "set", false: "clear"}[i%2 == 0]
		wg.Go(func() {
			secret := ""
			if action == "set" {
				secret = "another secret entirely"
			}
			if _, err := d.changeSecret(ctx, action, secret); err != nil {
				t.Error(err)
			}
		})
		wg.Wait()
		d.auth.mu.Lock()
		left := len(d.auth.sessions)
		d.auth.mu.Unlock()
		if left != 0 {
			t.Fatalf("run %d: %d logins after the %s", i, left, action)
		}
		if err := d.store.setMaintainerSecret(ctx, testSecret, "set"); err != nil {
			t.Fatal(err)
		}
	}
}

// A login or change whose audit record cannot be written does not happen.
func TestMaintainerSecretAuditFailure(t *testing.T) {
	d, _ := secretDaemon(t)
	ctx := context.Background()
	if _, err := d.store.db.Exec(`CREATE TRIGGER refuse_audit BEFORE INSERT ON audit WHEN NEW.result='accepted'
		BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if id, err := d.passwordLogin(ctx, testSecret, "dashboard"); err == nil || id != "" {
		t.Fatalf("login without its record: %q %v", id, err)
	}
	link, _ := d.auth.link()
	d.auth.login(link)
	if _, err := d.changeSecret(ctx, "set", "another secret entirely"); err == nil {
		t.Fatal("a change without its record")
	}
	if _, err := d.changeSecret(ctx, "clear", ""); err == nil {
		t.Fatal("a clear without its record")
	}
	if ok, _ := d.store.matchMaintainerSecret(ctx, testSecret); !ok {
		t.Fatal("a failed change replaced the key")
	}
	d.auth.mu.Lock()
	left := len(d.auth.sessions)
	d.auth.mu.Unlock()
	if left != 1 {
		t.Fatalf("a failed change ended logins: %d", left)
	}
}

func TestSchemaTwentyNineKeepsState(t *testing.T) {
	root := t.TempDir()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	join(t, s, "codex", "synthetic-a", "")
	rows := rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaThirtyTwo+undoSchemaThirtyOne+undoSchemaThirty+undoSchemaTwentyNine+`PRAGMA user_version=28`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 28); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != rows ||
		count(t, upgraded, `SELECT COUNT(*) FROM maintainer_secret`) != 0 {
		t.Fatal("schema 29 changed retained state")
	}
	if err := checkCatalog(upgraded.db, 28); err == nil {
		t.Fatal("a schema 28 writer would accept the schema 29 store")
	}
}

func TestReadSecretLine(t *testing.T) {
	long := strings.Repeat("a", MaintainerSecretMaxBytes)
	for _, c := range []struct {
		input, want, rest string
		fails             bool
	}{
		{"twelve chars\nrest", "twelve chars", "rest", false},
		{"twelve chars\r\nrest", "twelve chars", "rest", false},
		{"twelve chars\r\r\n", "twelve chars\r", "", false},
		{"  tabs\tand spaces  \n", "  tabs\tand spaces  ", "", false},
		{"ünïcödé\n", "ünïcödé", "", false},
		{"at end of input", "at end of input", "", false},
		{"\n", "", "", false},
		{"", "", "", true},
		{long + "\n", long, "", false},
		{long + "\r\n", long, "", false},
		{long + "a\n", "", "", true},
		{long + "a", "", "", true},
		{long + "\r", "", "", true},
	} {
		r := strings.NewReader(c.input)
		got, err := ReadSecretLine(r)
		if c.fails != (err != nil) || got != c.want {
			t.Errorf("%q: %q %v", c.input, got, err)
			continue
		}
		if rest, _ := io.ReadAll(r); !c.fails && string(rest) != c.rest {
			t.Errorf("%q: left %q", c.input, rest)
		}
	}
	// The read stops at the bound: a long line is refused after at most one byte past it.
	r := strings.NewReader(strings.Repeat("a", 10*MaintainerSecretMaxBytes))
	if _, err := ReadSecretLine(r); err == nil || r.Len() < 8*MaintainerSecretMaxBytes {
		t.Fatalf("read %d bytes of a long line: %v", 10*MaintainerSecretMaxBytes-r.Len(), err)
	}
}
