package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type remoteOriginKey struct{}

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

// remoteDashboardHandler mounts only the browser surface. Serve preserves the external
// Host for HTTP backends (Tailscale v1.102.3 serve.go); forwarding headers confer no trust.
// Setup pins a single HTTPS authority, never the agent listener or a browser-supplied one.
func (d *Daemon) remoteDashboardHandler(origin string) http.Handler {
	u, err := url.Parse(origin)
	valid := err == nil && u.Scheme == "https" && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.User == nil && u.Fragment == ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardHeaders(w)
		if !valid || r.Host != u.Host {
			http.Error(w, "foreign host", http.StatusForbidden)
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
