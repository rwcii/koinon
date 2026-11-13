package core

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rwcii/koinon/internal/remote"
	qrcode "github.com/skip2/go-qrcode"
)

const remoteTicketLifetime = 2 * time.Minute

// A ticket's credential exists only in its PNG response and scanned URL fragment.
// The daemon retains only its hash, expiry, origin and issuing browser session.
type remoteTicket struct {
	origin  string
	expires time.Time
	issuer  [32]byte
}

// newRemoteTicket issues a short-lived sign-in capability from a live browser
// login on this host. A remote session cannot issue one, so it cannot renew
// itself past its 48-hour limit or fill the login table without the secret.
// Its cap matches the existing eight outstanding local login links.
func (a *dashboardAuth) newRemoteTicket(origin, issuer string) (string, time.Time, error) {
	if !remote.ValidOrigin(origin) {
		return "", time.Time{}, ErrInvalid
	}
	issuerHash, ok := tokenHash(issuer)
	if !ok {
		return "", time.Time{}, ErrInvalid
	}
	token, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	hash, _ := tokenHash(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	session := a.sessions[issuerHash]
	if session == nil || session.client != "dashboard" {
		return "", time.Time{}, ErrUnavailable
	}
	a.pruneRemoteTickets(now)
	for len(a.tickets) >= maxLoginLinks {
		var oldest [32]byte
		var first time.Time
		for h, ticket := range a.tickets {
			if first.IsZero() || ticket.expires.Before(first) {
				oldest, first = h, ticket.expires
			}
		}
		delete(a.tickets, oldest)
	}
	expires := now.Add(remoteTicketLifetime)
	a.tickets[hash] = remoteTicket{origin: origin, expires: expires, issuer: issuerHash}
	return token, expires, nil
}

func (a *dashboardAuth) pruneRemoteTickets(now time.Time) {
	for h, ticket := range a.tickets {
		if !now.Before(ticket.expires) || a.sessions[ticket.issuer] == nil {
			delete(a.tickets, h)
		}
	}
}

// redeemRemoteTicket atomically consumes the ticket and starts a session on the
// pinned remote origin. Disable, logout, secret change and concurrent scans
// cannot re-create a session after its revocation critical section.
func (a *dashboardAuth) redeemRemoteTicket(token, origin string) (string, bool, error) {
	hash, ok := tokenHash(token)
	if !ok || !remote.ValidOrigin(origin) {
		return "", false, nil
	}
	id, err := randomToken()
	if err != nil {
		return "", false, err
	}
	sessionHash, _ := tokenHash(id)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	a.pruneRemoteTickets(now)
	ticket, found := a.tickets[hash]
	if !found || ticket.origin != origin {
		return "", false, nil
	}
	// A full device list keeps the code: after the maintainer forgets a device, the same
	// code can be scanned again until it expires (#673 review L9).
	if err := a.startRemoteLocked(sessionHash, origin); err != nil {
		if !errors.Is(err, ErrRemoteCapacity) {
			delete(a.tickets, hash)
		}
		return "", false, err
	}
	delete(a.tickets, hash)
	return id, true, nil
}

func (a *dashboardAuth) discardRemoteTicket(token string) {
	if hash, ok := tokenHash(token); ok {
		a.mu.Lock()
		delete(a.tickets, hash)
		a.mu.Unlock()
	}
}

func (d *Daemon) dashboardPairRoutes(mux *http.ServeMux,
	authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	signedIn func(http.ResponseWriter, *http.Request, string),
	render func(http.ResponseWriter, *http.Request, int, dashboardPage)) {
	mux.HandleFunc("POST /dashboard/remote/ticket", authed(func(w http.ResponseWriter, r *http.Request, issuer string) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		token, expires, origin, err := d.issueRemoteTicket(ctx, issuer)
		if err != nil {
			http.Error(w, "Remote sign-in code unavailable. Check remote access and try again.", http.StatusConflict)
			return
		}
		image, err := qrcode.Encode(origin+"/dashboard/pair#ticket="+token, qrcode.Medium, 320)
		if err != nil {
			d.auth.discardRemoteTicket(token)
			http.Error(w, "Sign-in code unavailable. Try again.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Koinon-Ticket-Expires", expires.UTC().Format(time.RFC3339))
		w.Header().Set("X-Koinon-Ticket-Lifetime", strconv.Itoa(int(remoteTicketLifetime.Seconds())))
		w.Write(image)
	}))
	mux.HandleFunc("GET /dashboard/pair", func(w http.ResponseWriter, r *http.Request) {
		if remoteOrigin(r) == "" {
			http.NotFound(w, r)
			return
		}
		// The fragment never reaches the server. GET/HEAD only show the landing page;
		// scanners and URL previews cannot consume the credential with a read.
		render(w, r, http.StatusOK, dashboardPage{View: "pair", Title: "Sign in to Koinon"})
	})
	mux.HandleFunc("POST /dashboard/pair", func(w http.ResponseWriter, r *http.Request) {
		origin := remoteOrigin(r)
		if origin == "" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, loginFormLimit)
		if r.ParseForm() != nil || len(r.PostForm["ticket"]) != 1 {
			http.Error(w, "This sign-in code is not valid. Get a new code on your computer.", http.StatusBadRequest)
			return
		}
		// Same-origin request protection is enforced by dashboardHandler before this
		// route. The unguessable one-time ticket is the unauthenticated capability.
		id, ok, err := d.auth.redeemRemoteTicket(r.PostForm.Get("ticket"), origin)
		if err != nil {
			if errors.Is(err, ErrRemoteCapacity) {
				http.Error(w, "Signed-in device limit reached. Forget a device in Remote access settings on the host or an existing signed-in browser, then scan this code again.", http.StatusConflict)
				return
			}
			http.Error(w, "Sign-in unavailable. Get a new code on your computer.", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "This sign-in code expired or was already used. Get a new code on your computer.", http.StatusUnauthorized)
			return
		}
		ctx := withOrigin(r.Context(), "dashboard")
		if err := d.store.auditAccepted(ctx, "maintainer_login", "remote QR"); err != nil {
			d.auth.logout(id)
			http.Error(w, "Sign-in unavailable. Get a new code on your computer.", http.StatusServiceUnavailable)
			return
		}
		// A ticket opens the dashboard; it never repeats a write or takes a redirect
		// destination from the QR, query, or redemption form.
		r.PostForm.Del("return")
		signedIn(w, r, id)
	})
}

func (d *Daemon) issueRemoteTicket(ctx context.Context, issuer string) (token string, expires time.Time, origin string, err error) {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.config == nil || !rt.config.Enabled || rt.server == nil || !remote.ValidOrigin(rt.config.Mapping.Origin) {
		return "", time.Time{}, "", ErrUnavailable
	}
	set, err := d.store.maintainerSecretSet(ctx)
	if err != nil || !set {
		return "", time.Time{}, "", errors.Join(err, ErrSecretUnset)
	}
	origin = rt.config.Mapping.Origin
	token, expires, err = d.auth.newRemoteTicket(origin, issuer)
	if err != nil {
		return "", time.Time{}, "", err
	}
	if err = d.store.auditAccepted(ctx, "remote_pair_ticket", "remote dashboard"); err != nil {
		d.auth.discardRemoteTicket(token)
		return "", time.Time{}, "", err
	}
	return token, expires, origin, nil
}
