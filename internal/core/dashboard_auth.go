package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dashboard login (sprint chunk 08). A one-time link from the bearer API, or the maintainer
// secret (PDR 009), starts a browser session; the daemon keeps only hashes of links and session IDs, in memory, so a restart
// ends every login. The CSRF token is derived from the session ID with a per-start key,
// so it needs no storage of its own.

const (
	dashboardCookie = "koinon_dashboard"
	dashboardCSRF   = "X-Koinon-CSRF"
	linkLifetime    = 60 * time.Second
	// A login lasts through a working day and the night after it without a request, and
	// at most a week (#435).
	sessionIdle       = 24 * time.Hour
	sessionAbsolute   = 7 * 24 * time.Hour
	maxLoginLinks     = 8
	maxDashboardLogin = 16
)

// dashboardSession is one login. client is "dashboard" for a browser login and "mcp:NAME"
// for a maintainer token (PDR 009, Decision 3): a cookie opens only a browser login, a token
// only its own client's, and each maintainer action takes its via from client.
type dashboardSession struct {
	created, used time.Time
	client        string
}

type dashboardAuth struct {
	mu       sync.Mutex
	now      func() time.Time
	csrfKey  []byte
	links    map[[32]byte]time.Time
	sessions map[[32]byte]*dashboardSession
	// gate orders the maintainer secret's checks and changes (PDR 009).
	gate secretGate
}

func newDashboardAuth(now func() time.Time) (*dashboardAuth, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &dashboardAuth{now: now, csrfKey: key, links: map[[32]byte]time.Time{}, sessions: map[[32]byte]*dashboardSession{},
		gate: secretGate{mu: make(chanMutex, 1)}}, nil
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

// tokenHash accepts only the 64-hex form that randomToken produces.
func tokenHash(token string) ([32]byte, bool) {
	if len(token) != 64 {
		return [32]byte{}, false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

// link issues a one-time login token. At most maxLoginLinks stay outstanding; a new link
// drops the one that expires first.
func (a *dashboardAuth) link() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	hash, _ := tokenHash(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for h, expires := range a.links {
		if !now.Before(expires) {
			delete(a.links, h)
		}
	}
	for len(a.links) >= maxLoginLinks {
		var oldest [32]byte
		var first time.Time
		for h, expires := range a.links {
			if first.IsZero() || expires.Before(first) {
				oldest, first = h, expires
			}
		}
		delete(a.links, oldest)
	}
	a.links[hash] = now.Add(linkLifetime)
	return token, nil
}

// login consumes a link token and starts a session. A used, expired or unknown token
// starts nothing.
func (a *dashboardAuth) login(token string) (string, bool, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return "", false, nil
	}
	a.mu.Lock()
	expires, found := a.links[hash]
	delete(a.links, hash)
	now := a.now()
	a.mu.Unlock()
	if !found || !now.Before(expires) {
		return "", false, nil
	}
	id, err := a.start("dashboard")
	return id, err == nil, err
}

// start begins a login session for client, after a link or the maintainer secret, and
// returns its ID. Browser logins and maintainer tokens share the limits: at most
// maxDashboardLogin stay, and a new one drops the oldest.
func (a *dashboardAuth) start(client string) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	sessionHash, _ := tokenHash(id)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	for len(a.sessions) >= maxDashboardLogin {
		var oldest [32]byte
		var first time.Time
		for h, s := range a.sessions {
			if first.IsZero() || s.created.Before(first) {
				oldest, first = h, s.created
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sessionHash] = &dashboardSession{created: now, used: now, client: client}
	return id, nil
}

// endAll ends every login and maintainer token, when the maintainer secret changes.
func (a *dashboardAuth) endAll() {
	a.mu.Lock()
	clear(a.sessions)
	a.mu.Unlock()
}

func (a *dashboardAuth) prune(now time.Time) {
	for h, s := range a.sessions {
		if !now.Before(s.used.Add(sessionIdle)) || !now.Before(s.created.Add(sessionAbsolute)) {
			delete(a.sessions, h)
		}
	}
}

// cookieName is the login cookie of the port in the request's Host. Browsers do not separate
// cookies by port, so each board opened under one loopback IP, such as a local daemon and a
// tunnel to another computer's daemon, keeps its own login (#454).
func cookieName(r *http.Request) string {
	_, port, _ := net.SplitHostPort(r.Host)
	return dashboardCookie + "_" + port
}

// session checks the request's cookie and records its use. It returns the session ID.
func (a *dashboardAuth) session(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(cookieName(r))
	if err != nil {
		return "", false
	}
	hash, ok := tokenHash(cookie.Value)
	if !ok {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	s, found := a.sessions[hash]
	if !found || s.client != "dashboard" {
		return "", false
	}
	s.used = now
	return cookie.Value, true
}

// token checks a maintainer token and records its use. It returns the client that the token
// was issued to, "mcp:NAME"; a browser login's ID is no token.
func (a *dashboardAuth) token(id string) (string, bool) {
	hash, ok := tokenHash(id)
	if !ok {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	s, found := a.sessions[hash]
	if !found || !strings.HasPrefix(s.client, "mcp:") {
		return "", false
	}
	s.used = now
	return s.client, true
}

// live reports whether a maintainer token is still in force, without recording a use: a
// waiting changes request checks it while it waits.
func (a *dashboardAuth) live(id string) bool {
	hash, ok := tokenHash(id)
	if !ok {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune(a.now())
	s, found := a.sessions[hash]
	return found && strings.HasPrefix(s.client, "mcp:")
}

func (a *dashboardAuth) logout(id string) {
	if hash, ok := tokenHash(id); ok {
		a.mu.Lock()
		delete(a.sessions, hash)
		a.mu.Unlock()
	}
}

func (a *dashboardAuth) csrf(id string) string {
	mac := hmac.New(sha256.New, a.csrfKey)
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *dashboardAuth) validCSRF(id, token string) bool {
	return subtle.ConstantTimeCompare([]byte(a.csrf(id)), []byte(token)) == 1
}

// dashboardHost reports whether the request names a literal loopback IP with a port, such as
// 127.0.0.1:47671 or [::1]:47671. A name such as localhost is refused, which defeats DNS
// rebinding (ADR 001, Decision 4). The port need not be the listener's, so an SSH tunnel on
// another local port works (#407); a rebinding page arrives under a name, never a literal IP.
func dashboardHost(r *http.Request) bool {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0 && validAddress(r.Host)
}

// dashboardHeaders apply to every dashboard response, refusals included.
func dashboardHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// same-origin, not no-referrer: under no-referrer a browser sends "Origin: null" with every
	// POST, also to this origin, and the origin check refuses every action form.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}
