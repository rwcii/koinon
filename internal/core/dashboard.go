package core

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
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

var dashboardViews = []string{"sessions", "messages", "memory", "work", "health", "audit"}

type dashboardPage struct {
	View   string
	Title  string
	CSRF   string
	Views  []string
	Data   any
	Now    time.Time
	Notice string
}

func dashboardTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"namingReason": NamingReason,
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
		"list": func(values ...string) []string { return values },
		// anyWork reports whether a page of stores lists at least one work item.
		"anyWork": func(stores []StoreSummary) bool {
			for _, s := range stores {
				if len(s.Work) > 0 {
					return true
				}
			}
			return false
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
		if page.CSRF != "" {
			page.Notice = notice(r.URL.Query().Get("notice"))
		}
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
	// authedLimit requires a live login session and, for a state-changing request, its
	// CSRF token; it reads a form body of at most limit bytes.
	authedLimit := func(limit int64, next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, ok := d.auth.session(r)
			if !ok {
				refuse(w, r, http.StatusUnauthorized, "login")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
				parsed := r.ParseForm() == nil
				token := r.Header.Get(dashboardCSRF)
				if token == "" && parsed {
					token = r.PostForm.Get("csrf")
				}
				if !d.auth.validCSRF(id, token) {
					refuse(w, r, http.StatusForbidden, "csrf")
					return
				}
				if !parsed {
					refuse(w, r, http.StatusBadRequest, "request")
					return
				}
			}
			next(w, r, id)
		}
	}
	authed := func(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return authedLimit(actionFormLimit, next)
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
	d.dashboardActions(mux, authedLimit)
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
	Work        []SessionWork
	Observed    map[string]SessionView
	Participant *Participant
}

type sessionsData struct {
	Rows   []sessionRow
	Search string
	Counts map[string]int64
	Sort   dashboardSort
	Next   string
}

type messagesData struct {
	Recipient    string
	RecipientKey *Key
	Peers        []peerOption
	AllPeers     bool
	Messages     []MessageRecord
	Sort         dashboardSort
	Next         string
}

type auditData struct {
	Records []AuditRecord
	Sort    dashboardSort
	Next    string
}

type storesData struct {
	Stores []StoreSummary
	Sort   dashboardSort
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
	Wake      map[string]any
	Retention RetentionStatus
}

// dashboardData reads one view's state. Every query is bounded and read-only.
func (d *Daemon) dashboardData(r *http.Request, view string) (any, error) {
	ctx, q, path := r.Context(), r.URL.Query(), "/dashboard/"+view
	switch view {
	case "sessions":
		order, err := parseSort(&sessionSort, path, q, "q")
		if err != nil {
			return nil, err
		}
		sessions, next, err := d.store.dashboardSessions(ctx, order, q.Get("q"))
		if err != nil {
			return nil, err
		}
		claims, err := d.store.sessionClaims(ctx)
		if err != nil {
			return nil, err
		}
		participants, _, err := d.store.Participants(ctx)
		if err != nil {
			return nil, err
		}
		byAddress := make(map[string]*Participant, len(participants))
		for i := range participants {
			byAddress[participants[i].Address] = &participants[i]
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
		counts, err := d.store.Counts(ctx)
		if err != nil {
			return nil, err
		}
		result := sessionsData{Rows: make([]sessionRow, 0, len(sessions)), Search: q.Get("q"), Counts: counts, Sort: order, Next: next}
		for _, s := range sessions {
			result.Rows = append(result.Rows, sessionRow{Session: s, Work: claims[s.Family+":"+s.ID], Observed: d.store.sessionObservations(pull, s), Participant: byAddress[s.Address]})
		}
		return result, nil
	case "messages":
		order, err := parseSort(&messageSort, path, q, "to", "peers")
		if err != nil {
			return nil, err
		}
		result := messagesData{Recipient: q.Get("to"), AllPeers: q.Get("peers") == "all", Sort: order}
		if result.Peers, err = d.store.dashboardPeerOptions(ctx, result.AllPeers); err != nil {
			return nil, err
		}
		var recipient *Key
		if result.Recipient != "" {
			if recipient, err = d.store.sessionByName(ctx, result.Recipient); err != nil {
				return result, nil
			}
			result.RecipientKey = recipient
		}
		result.Messages, result.Next, err = d.store.dashboardMessages(ctx, recipient, order)
		return result, err
	case "audit":
		order, err := parseSort(&auditSort, path, q)
		if err != nil {
			return nil, err
		}
		records, next, err := d.store.dashboardAudit(ctx, order)
		return auditData{Records: records, Sort: order, Next: next}, err
	case "memory":
		// Sort links keep the selected store and its filters.
		order, err := parseSort(&storeSort, path, q, "store", "q", "type", "all")
		if err != nil {
			return nil, err
		}
		stores, next, err := d.store.dashboardMemory(ctx, order)
		if err != nil {
			return nil, err
		}
		selected, err := d.selectedEntries(ctx, q)
		return memoryData{storesData: storesData{Stores: stores, Sort: order, Next: next}, Selected: selected}, err
	case "work":
		// Stores page by repository; the sort orders the work rows in each store.
		after := q.Get("after")
		q.Del("after")
		// Sort links keep the lifecycle filter and the open item.
		order, err := parseSort(&workSort, path, q, "lifecycle", "store", "item")
		if err != nil {
			return nil, err
		}
		lifecycle := q.Get("lifecycle")
		if !workLifecycles[lifecycle] {
			return nil, ErrInvalid
		}
		stores, next, err := d.store.dashboardWork(ctx, after, lifecycle, order)
		if err != nil {
			return nil, err
		}
		data := workData{storesData: storesData{Stores: stores, Sort: order, Next: next}, Lifecycle: lifecycle}
		if data.Open, err = d.openItem(ctx, q); err != nil {
			return nil, err
		}
		if data.Key, err = formKey(); err != nil {
			return nil, err
		}
		// The deadline leaves a minute of the idempotency horizon for the submission.
		data.Deadline = int64(d.store.clock() + d.store.limits().idemTTL - 60)
		all, err := d.store.storeList(ctx, "", -1)
		for _, item := range all {
			data.Repositories = append(data.Repositories, item.Repository)
		}
		return data, err
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
		wake, err := d.store.WakeHealth(ctx)
		if err != nil {
			return nil, err
		}
		retention, err := d.store.RetentionStatus(ctx)
		if err != nil {
			return nil, err
		}
		return healthData{Started: d.started, Revision: buildRevision(), Schema: schemaVersion, Listeners: d.Addresses(),
			Sessions: counts, Storage: storage, Sweep: sweep, Wake: wake, Retention: retention}, nil
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
