package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

// Dashboard login (sprint chunk 08). A one-time link from the bearer API starts a browser
// session; the daemon keeps only hashes of links and session IDs, in memory, so a restart
// ends every login. The CSRF token is derived from the session ID with a per-start key,
// so it needs no storage of its own.

const (
	dashboardCookie   = "koinon_dashboard"
	dashboardCSRF     = "X-Koinon-CSRF"
	linkLifetime      = 60 * time.Second
	sessionIdle       = 30 * time.Minute
	sessionAbsolute   = 12 * time.Hour
	maxLoginLinks     = 8
	maxDashboardLogin = 16
)

type dashboardSession struct {
	created, used time.Time
}

type dashboardAuth struct {
	mu       sync.Mutex
	now      func() time.Time
	csrfKey  []byte
	links    map[[32]byte]time.Time
	sessions map[[32]byte]*dashboardSession
}

func newDashboardAuth(now func() time.Time) (*dashboardAuth, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &dashboardAuth{now: now, csrfKey: key, links: map[[32]byte]time.Time{}, sessions: map[[32]byte]*dashboardSession{}}, nil
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
	id, err := randomToken()
	if err != nil {
		return "", false, err
	}
	sessionHash, _ := tokenHash(id)
	a.mu.Lock()
	defer a.mu.Unlock()
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
	a.sessions[sessionHash] = &dashboardSession{created: now, used: now}
	return id, true, nil
}

func (a *dashboardAuth) prune(now time.Time) {
	for h, s := range a.sessions {
		if !now.Before(s.used.Add(sessionIdle)) || !now.Before(s.created.Add(sessionAbsolute)) {
			delete(a.sessions, h)
		}
	}
}

// session checks the request's cookie and records its use. It returns the session ID.
func (a *dashboardAuth) session(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(dashboardCookie)
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
	if !found {
		return "", false
	}
	s.used = now
	return cookie.Value, true
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

// dashboardHost reports whether the request names the literal address of the listener
// that accepted it. A name such as localhost is refused, which defeats DNS rebinding.
func dashboardHost(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && local != nil && r.Host == local.String() && validAddress(r.Host)
}

// dashboardHeaders apply to every dashboard response, refusals included.
func dashboardHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}
