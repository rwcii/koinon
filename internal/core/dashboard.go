package core

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// The dashboard (sprint chunk 08) is served on the daemon's loopback listeners under
// /dashboard/. Pages are rendered on the server; html/template escapes every value, so
// message bodies and other agent-supplied text stay data. The /v1/ API keeps its bearer
// check and its refusal of browser origins; neither credential authorizes the other path.

//go:embed web
var webFiles embed.FS

var dashboardViews = []string{"sessions", "messages", "memory", "work", "health"}

type dashboardPage struct {
	View  string
	Title string
	CSRF  string
	Views []string
	Data  any
	Now   time.Time
}

func dashboardTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"ms": func(v int64) string {
			if v == 0 {
				return "—"
			}
			return time.UnixMilli(v).UTC().Format("2006-01-02 15:04:05Z")
		},
		"secs": func(v any) string {
			var f float64
			switch x := v.(type) {
			case float64:
				f = x
			case *float64:
				if x == nil {
					return "—"
				}
				f = *x
			default:
				return "—"
			}
			return time.Unix(0, int64(f*1e9)).UTC().Format("2006-01-02 15:04:05Z")
		},
		"pct": func(used, limit int64) string {
			if limit <= 0 {
				return "—"
			}
			return strconv.FormatFloat(100*float64(used)/float64(limit), 'f', 1, 64) + "%"
		},
		"tokens": func(v *ObservedValue) string {
			if v.UsedTokens == nil || v.LimitTokens == nil {
				return "no token usage"
			}
			text := strconv.FormatInt(*v.UsedTokens, 10) + " of " + strconv.FormatInt(*v.LimitTokens, 10) + " tokens"
			if *v.LimitTokens > 0 {
				text += " (" + strconv.FormatFloat(100*float64(*v.UsedTokens)/float64(*v.LimitTokens), 'f', 0, 64) + "%)"
			}
			return text
		},
		"query": func(pairs ...string) template.URL {
			v := url.Values{}
			for i := 0; i+1 < len(pairs); i += 2 {
				if pairs[i+1] != "" {
					v.Set(pairs[i], pairs[i+1])
				}
			}
			return template.URL("?" + v.Encode())
		},
	}
	layout, err := template.New("layout.html").Funcs(funcs).ParseFS(webFiles, "web/layout.html")
	if err != nil {
		return nil, err
	}
	result := map[string]*template.Template{}
	for _, view := range append([]string{"login"}, dashboardViews...) {
		t, err := template.Must(layout.Clone()).ParseFS(webFiles, "web/"+view+".html")
		if err != nil {
			return nil, err
		}
		result[view] = t
	}
	return result, nil
}

func (d *Daemon) dashboardHandler() (http.Handler, error) {
	pages, err := dashboardTemplates()
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(webFiles, "web/static")
	if err != nil {
		return nil, err
	}
	render := func(w http.ResponseWriter, r *http.Request, status int, page dashboardPage) {
		var out bytes.Buffer
		name := "page"
		if r.URL.Query().Get("fragment") == "1" {
			name = "list"
		}
		page.Views, page.Now = dashboardViews, d.store.now()
		if err := pages[page.View].ExecuteTemplate(&out, name, page); err != nil {
			http.Error(w, "dashboard render failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		w.Write(out.Bytes())
	}
	refuse := func(w http.ResponseWriter, r *http.Request, status int, reason string) {
		render(w, r, status, dashboardPage{View: "login", Title: "Not signed in", Data: reason})
	}
	// authed requires a live login session and, for a state-changing request, its CSRF token.
	authed := func(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, ok := d.auth.session(r)
			if !ok {
				refuse(w, r, http.StatusUnauthorized, "login")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				token := r.Header.Get(dashboardCSRF)
				if token == "" {
					r.Body = http.MaxBytesReader(w, r.Body, 4096)
					if r.ParseForm() == nil {
						token = r.PostForm.Get("csrf")
					}
				}
				if !d.auth.validCSRF(id, token) {
					refuse(w, r, http.StatusForbidden, "csrf")
					return
				}
			}
			next(w, r, id)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /dashboard/static/", http.StripPrefix("/dashboard/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /dashboard/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			// A HEAD request, such as a link preview, never uses the token.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id, ok, err := d.auth.login(r.URL.Query().Get("token"))
		if err != nil {
			http.Error(w, "login failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			refuse(w, r, http.StatusUnauthorized, "link")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dashboardCookie, Value: id, Path: "/dashboard/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/dashboard/sessions", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /dashboard/logout", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		d.auth.logout(id)
		http.SetCookie(w, &http.Cookie{Name: dashboardCookie, Value: "", Path: "/dashboard/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		refuse(w, r, http.StatusOK, "logout")
	}))
	mux.HandleFunc("GET /dashboard/{$}", authed(func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Redirect(w, r, "/dashboard/sessions", http.StatusSeeOther)
	}))
	for _, view := range dashboardViews {
		mux.HandleFunc("GET /dashboard/"+view, authed(func(w http.ResponseWriter, r *http.Request, id string) {
			data, err := d.dashboardData(r, view)
			if err != nil {
				refuse(w, r, http.StatusBadRequest, "request")
				return
			}
			render(w, r, http.StatusOK, dashboardPage{View: view, Title: strings.ToUpper(view[:1]) + view[1:], CSRF: d.auth.csrf(id), Data: data})
		}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardHeaders(w)
		origin := r.Header.Get("Origin")
		switch {
		case !dashboardHost(r):
			http.Error(w, "foreign host", http.StatusForbidden)
		case origin != "" && origin != "http://"+r.Host:
			http.Error(w, "foreign origin", http.StatusForbidden)
		case r.Method != http.MethodGet && r.Method != http.MethodHead && origin == "":
			http.Error(w, "missing origin", http.StatusForbidden)
		default:
			mux.ServeHTTP(w, r)
		}
	}), nil
}

type sessionRow struct {
	Session
	Work     []SessionWork
	Observed map[string]SessionView
}

type sessionsData struct {
	Rows []sessionRow
	Next string
}

type messagesData struct {
	Recipient string
	Messages  []MessageRecord
	Next      int64
}

type storesData struct {
	Stores []StoreSummary
	Next   string
}

type healthData struct {
	Started   time.Time
	Revision  string
	Schema    int
	Listeners []string
	Sessions  map[string]int64
	Storage   StorageStatus
	Sweep     map[string]any
}

// dashboardData reads one view's state. Every query is bounded and read-only.
func (d *Daemon) dashboardData(r *http.Request, view string) (any, error) {
	ctx, q := r.Context(), r.URL.Query()
	switch view {
	case "sessions":
		var after *Key
		if v := q.Get("after"); v != "" {
			family, id, found := strings.Cut(v, ":")
			if !found || !validKey(family, id) {
				return nil, ErrInvalid
			}
			after = &Key{family, id}
		}
		sessions, next, err := d.store.dashboardSessions(ctx, after)
		if err != nil {
			return nil, err
		}
		claims, err := d.store.sessionClaims(ctx)
		if err != nil {
			return nil, err
		}
		// Observations of ended sessions are dropped here too, whatever page is shown.
		active, err := d.store.activeKeys(ctx)
		if err != nil {
			return nil, err
		}
		d.store.forget(active)
		// Pulled observations share one short budget, so unreachable agents cannot stall the page.
		pull, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		result := sessionsData{Rows: make([]sessionRow, 0, len(sessions))}
		if next != nil {
			result.Next = next.Family + ":" + next.ID
		}
		for _, s := range sessions {
			result.Rows = append(result.Rows, sessionRow{Session: s, Work: claims[s.Family+":"+s.ID], Observed: d.store.sessionObservations(pull, s)})
		}
		return result, nil
	case "messages":
		var before int64
		if v := q.Get("before"); v != "" {
			var err error
			if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 1 {
				return nil, ErrInvalid
			}
		}
		result := messagesData{Recipient: q.Get("to")}
		var recipient *Key
		if result.Recipient != "" {
			var err error
			if recipient, err = d.store.sessionByName(ctx, result.Recipient); err != nil {
				return result, nil
			}
		}
		var err error
		result.Messages, result.Next, err = d.store.dashboardMessages(ctx, recipient, before)
		return result, err
	case "memory", "work":
		stores, next, err := d.store.dashboardStores(ctx, q.Get("after"), view == "work")
		return storesData{Stores: stores, Next: next}, err
	default:
		counts, err := d.store.Counts(ctx)
		if err != nil {
			return nil, err
		}
		storage, err := d.store.StorageStatus(ctx)
		if err != nil {
			return nil, err
		}
		sweep, err := d.store.workMaintenanceStatus(ctx, "")
		if err != nil {
			return nil, err
		}
		return healthData{Started: d.started, Revision: buildRevision(), Schema: schemaVersion, Listeners: d.Addresses(),
			Sessions: counts, Storage: storage, Sweep: sweep}, nil
	}
}

// buildRevision is the VCS revision the binary was built from, when the build recorded it.
func buildRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "unknown"
}
