package core

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/remote"
	qrcode "github.com/skip2/go-qrcode"
)

// The browser controls call the same lifecycle as the CLI. No browser-supplied
// address, port, Serve configuration or provider URL is accepted.
type dashboardRemoteData struct {
	CSRF         string
	Checked      bool
	Secret       bool
	Ready        bool
	Status       RemoteStatus
	Approval     remote.Setup
	SetupError   string
	Prerequisite string
	// Operator is true after Tailscale refused a Serve write from this account
	// on Linux. OperatorCommand is the exact command, or "" when the account name
	// is not a plain name; the diagnostic text then says how to find it (#652).
	Operator        bool
	OperatorCommand string
	// OnRemote is true when the page is served on the remote surface, which
	// cannot issue sign-in codes.
	OnRemote bool
}

func remoteControlRequest(r *http.Request) bool {
	switch r.URL.Path {
	case "/dashboard/settings":
		// The no-JS status redirect renders these same lifecycle diagnostics.
		// It can revoke this surface on an observed identity/Serve conflict.
		return r.URL.Query().Get("remote_status") == "1" || r.URL.Query().Get("remote_setup") == "1"
	case "/dashboard/remote/status", "/dashboard/remote/qr", "/dashboard/remote/ticket", "/dashboard/actions/remote-enable", "/dashboard/actions/remote-disable":
		return true
	}
	return false
}

func (d *Daemon) dashboardRemoteState(ctx context.Context, checked bool, csrf string) dashboardRemoteData {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data := dashboardRemoteData{CSRF: csrf, Checked: checked}
	data.Secret, _ = d.store.maintainerSecretSet(ctx)
	data.Status = d.remoteStatus(ctx)
	if data.Status.Tailscale != nil {
		if err := data.Status.Tailscale.Ready(); err != nil {
			data.Prerequisite = err.Error()
		}
		data.Ready = data.Secret && data.Prerequisite == ""
		// A feature approval can still be needed even after HTTPS certificates are
		// enabled. Query instructions only during explicit setup, never normal polling.
		if checked && (!data.Status.Configured || data.Status.Serve != "ready") && data.Status.Tailscale.BackendState == "Running" && data.Status.Tailscale.Online {
			d.remote.mu.Lock()
			manager := d.remote.manager
			d.remote.mu.Unlock()
			var err error
			data.Approval, err = manager.Setup(ctx)
			if err != nil {
				data.SetupError = err.Error()
			} else if !data.Approval.Complete {
				data.Ready = false
			}
		}
	}
	for _, text := range []string{data.Status.Error, data.SetupError} {
		if strings.HasPrefix(text, remote.OperatorRequired+": ") {
			data.Operator, data.Ready = true, false
			data.OperatorCommand = remote.OperatorCommand()
		}
	}
	return data
}

func (d *Daemon) dashboardRemoteRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc, pages map[string]*template.Template) {
	mux.HandleFunc("GET /dashboard/remote/status", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		if r.URL.Query().Get("fragment") != "1" {
			q := url.Values{"remote_status": {"1"}}
			if r.URL.Query().Get("setup") == "1" {
				q.Set("remote_setup", "1")
			}
			if code := r.URL.Query().Get("notice"); noticeCode.MatchString(code) {
				q.Set("notice", code)
			}
			http.Redirect(w, r, "/dashboard/settings?"+q.Encode()+"#remote-access", http.StatusSeeOther)
			return
		}
		data := d.dashboardRemoteState(r.Context(), r.URL.Query().Get("setup") == "1", d.auth.csrf(id))
		data.OnRemote = remoteOrigin(r) != ""
		data.Checked = data.Checked || r.URL.Query().Get("checked") == "1"
		var out bytes.Buffer
		err := pages["settings"].ExecuteTemplate(&out, "remote", data)
		if err != nil {
			http.Error(w, "remote status unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(out.Bytes())
	}))
	mux.HandleFunc("GET /dashboard/remote/qr", authed(func(w http.ResponseWriter, r *http.Request, _ string) {
		d.remote.mu.Lock()
		var link string
		if c := d.remote.config; c != nil && c.Enabled && remote.ValidOrigin(c.Mapping.Origin) {
			link = c.Mapping.Origin + "/dashboard/"
		}
		d.remote.mu.Unlock()
		if link == "" {
			http.NotFound(w, r)
			return
		}
		png, err := qrcode.Encode(link, qrcode.Medium, 256)
		if err != nil {
			http.Error(w, "QR unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	for _, action := range []string{"enable", "disable"} {
		mux.HandleFunc("POST /dashboard/actions/remote-"+action, authed(func(w http.ResponseWriter, r *http.Request, _ string) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 8*time.Second)
			defer cancel()
			ctx, pending := withAudit(ctx, "remote-"+action, "remote dashboard")
			var err error
			if action == "enable" {
				err = d.remoteEnable(ctx)
			} else {
				err = d.remoteDisable(ctx)
			}
			code := "remote_" + action + "d"
			if err != nil {
				d.store.auditRefusal(ctx, pending, "remote_setup_required")
				code = "remote_setup_required"
				if errors.Is(err, ErrSecretUnset) {
					code = "remote_secret_required"
				}
			} else if d.store.auditAccepted(ctx, "remote-"+action, "remote dashboard") != nil {
				code = "remote_audit_unavailable"
			}
			// PRG: neither a reload nor reconnect can submit the action again. Disable
			// from the remote surface may close this connection before the redirect.
			http.Redirect(w, r, "/dashboard/remote/status?setup=1&notice="+code+"#remote-access", http.StatusSeeOther)
		}))
	}
}
