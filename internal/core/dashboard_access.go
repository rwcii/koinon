package core

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"
)

// dashboardAccess is where the dashboard opens besides this computer, for the setup panel's
// Dashboard access section (#665). It names what runs, not only what is configured.
type dashboardAccess struct {
	// Network is the network dashboard's URL when an address is configured, and NetworkOpen
	// whether its listener runs; NetworkCode is its status code otherwise.
	Network     string
	NetworkOpen bool
	NetworkCode string
	// Tailnet is the remote URL when remote access is on, and TailnetReady whether its
	// listener and Serve mapping are ready.
	Tailnet      string
	TailnetReady bool
}

// TailnetOnly is the mode that the switch sets: remote access on and no network address.
func (a dashboardAccess) TailnetOnly() bool { return a.Tailnet != "" && a.Network == "" }

func (d *Daemon) dashboardAccessOf(ctx context.Context, remoteStatus RemoteStatus) dashboardAccess {
	var a dashboardAccess
	if s := d.dashboardNetworkStatus(ctx); s != nil {
		a.Network, a.NetworkOpen, a.NetworkCode = s.URL, s.Code == "running", s.Code
	}
	if remoteStatus.Configured {
		a.Tailnet = remoteStatus.URL
		a.TailnetReady = remoteStatus.Listener == "ready" && remoteStatus.Serve == "ready"
	}
	return a
}

// errAccessPartial reports a switch that enabled remote access but kept the network address.
type errAccessPartial struct{ err error }

func (e errAccessPartial) Error() string {
	return "dashboard_access_partial: remote access is on, but the network address stays configured and open: " + e.err.Error()
}

// dashboardTailnetOnly sets the dashboard to this computer and the tailnet (#665): it enables
// remote access with the saved port, saves the local-only choice, removes the network address
// and then closes the network listener. A failed enable changes no dashboard setting; a failed
// write keeps the network address open. It returns the remote URL.
func (d *Daemon) dashboardTailnetOnly(ctx context.Context) (string, error) {
	d.remote.mu.Lock()
	defer d.remote.mu.Unlock()
	if err := d.remoteEnableLocked(ctx, 0); err != nil {
		return "", err
	}
	url := d.remote.config.Mapping.Origin + "/dashboard/"
	if err := WriteDashboardLocal(d.root); err != nil {
		return url, errAccessPartial{err}
	}
	if err := WriteDashboardAddress(d.root, nil); err != nil {
		return url, errAccessPartial{err}
	}
	d.closeDashboardNetwork()
	return url, nil
}

// closeDashboardNetwork clears the network dashboard and shuts its listener down. The shutdown
// lets requests that are in progress, such as the one that asked for it, finish.
func (d *Daemon) closeDashboardNetwork() {
	d.networkMu.Lock()
	server := d.networkServer
	d.networkServer, d.networkAddress, d.networkStatus = nil, nil, nil
	d.networkMu.Unlock()
	if server == nil {
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if server.Shutdown(ctx) != nil {
			server.Close()
		}
	}()
}

// accessClosedPage answers the switch on the network surface, whose listener closes after it.
var accessClosedPage = template.Must(template.New("closed").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Koinon dashboard access</title></head>
<body><main><h1>Dashboard access changed</h1>
<p>The dashboard now opens on this computer and your tailnet only. This network address is closed.</p>
<p>Open it on your tailnet: <a href="{{.}}">{{.}}</a></p></main></body></html>
`))

func (d *Daemon) dashboardAccessRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	mux.HandleFunc("POST /dashboard/actions/dashboard-access", authed(func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.PostFormValue("mode") != "tailnet" {
			http.Error(w, "invalid_request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 8*time.Second)
		defer cancel()
		ctx, pending := withAudit(ctx, "dashboard-access", "dashboard")
		url, err := d.dashboardTailnetOnly(ctx)
		code := "dashboard_access_tailnet"
		var partial errAccessPartial
		switch {
		case errors.As(err, &partial):
			d.store.auditRefusal(ctx, pending, "dashboard_access_partial")
			code = "dashboard_access_partial"
		case errors.Is(err, ErrSecretUnset):
			d.store.auditRefusal(ctx, pending, "remote_secret_required")
			code = "remote_secret_required"
		case err != nil:
			d.store.auditRefusal(ctx, pending, "remote_setup_required")
			code = "remote_setup_required"
		default:
			if d.store.auditAccepted(ctx, "dashboard-access", "dashboard") != nil {
				code = "remote_audit_unavailable"
			}
		}
		if err == nil && networkDashboard(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Connection", "close")
			if accessClosedPage.Execute(w, url) != nil {
				fmt.Fprintln(w, "Dashboard access changed; open "+url)
			}
			return
		}
		http.Redirect(w, r, "/dashboard/remote/status?setup=1&notice="+code+"#remote-access", http.StatusSeeOther)
	}))
}
