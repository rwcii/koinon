package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type remoteOriginKey struct{}

// The signed routes on Remote, at the same paths as on the loopback API.
const (
	remoteSignedSession = "/v1/maintainer/signed/session"
	remoteSignedAction  = "/v1/maintainer/signed"
)

func remoteSignedPath(path string) bool {
	return path == remoteSignedSession || path == remoteSignedAction
}

func remoteOrigin(r *http.Request) string {
	origin, _ := r.Context().Value(remoteOriginKey{}).(string)
	return origin
}

// dashboardReturn accepts only a read-only dashboard screen. Login never replays a POST,
// accepts a foreign URL, or opens a one-time local login link.
func dashboardReturn(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.ContainsAny(value, "\\\r\n") || !strings.HasPrefix(u.Path, "/dashboard/") {
		return ""
	}
	view := strings.TrimPrefix(u.Path, "/dashboard/")
	if view != "" && !containsDashboardView(view) {
		return ""
	}
	q := u.Query()
	q.Del("fragment")
	q.Del("return")
	q.Del("token")
	u.RawQuery = q.Encode()
	return u.String()
}

func containsDashboardView(view string) bool {
	for _, v := range dashboardViews {
		if v == view {
			return true
		}
	}
	return false
}

// remoteDashboardHandler mounts only the browser surface and the two signed routes (#693
// chunk 3; decision.md Decision 5.2). Serve preserves the external Host for HTTP backends
// (Tailscale v1.102.3 serve.go); forwarding headers confer no trust. Setup pins a single HTTPS
// authority, never the agent listener or a browser-supplied one.
func (d *Daemon) remoteDashboardHandler(origin string) http.Handler {
	u, err := url.Parse(origin)
	valid := err == nil && u.Scheme == "https" && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.User == nil && u.Fragment == ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardHeaders(w)
		if !valid || r.Host != u.Host {
			http.Error(w, "foreign host", http.StatusForbidden)
			return
		}
		if remoteSignedPath(r.URL.Path) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			// The signed routes take only signed envelopes and the signed session's token as
			// the bearer; no cookie, secret or dashboard session counts here.
			r = r.WithContext(context.WithValue(r.Context(), signedTransport{}, true))
			if r.URL.Path == remoteSignedSession {
				d.signedSessionRoute(w, r)
			} else {
				token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				if !bearer {
					token = ""
				}
				d.signedActionRoute(w, r, token)
			}
			return
		}
		if r.URL.Path == "/" && (r.Method == "GET" || r.Method == "HEAD") {
			http.Redirect(w, r, "/dashboard/", http.StatusSeeOther)
			return
		}
		if r.URL.Path != "/dashboard" && !strings.HasPrefix(r.URL.Path, "/dashboard/") {
			http.NotFound(w, r)
			return
		}
		set, err := d.store.maintainerSecretSet(r.Context())
		if err != nil || !set {
			http.Error(w, "maintainer secret required; set it on the host", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/dashboard/login" && r.URL.Query().Has("token") {
			http.Error(w, "remote login requires the maintainer secret", http.StatusForbidden)
			return
		}
		d.dashboard.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, origin)))
	})
}
