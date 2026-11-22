package install

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

type remoteCall struct {
	state, address string
	port           int
}

// tailnet records the install's remote enable and answers with url or err.
func tailnet(e *env, url string, err error) *[]remoteCall {
	var calls []remoteCall
	e.o.RemoteEnable = func(_ context.Context, state, address string, port int) (string, error) {
		calls = append(calls, remoteCall{state, address, port})
		return url, err
	}
	return &calls
}

// #665: the answer "this computer and your tailnet" saves the local choice and no network
// address, asks the Serve HTTPS port, sets the secret when none is set, and enables remote
// access for this installation after the start.
func TestInstallDashboardTailnet(t *testing.T) {
	for _, tc := range []struct {
		answers []string
		port    int
	}{{[]string{"2", ""}, 443}, {[]string{"2", "8443"}, 8443}, {[]string{"2", "0", "x", "08443", "65536", "10000"}, 10000}} {
		e := newEnv(t, "systemd")
		d := newNetworkDaemon(t, e)
		url := "https://host.example.ts.net/dashboard/"
		calls := tailnet(e, url, nil)
		s, told := questions(e, append(tc.answers, "")...)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK || r.DashboardNetwork != nil {
			t.Fatalf("%v: install %+v %v", tc.answers, r, err)
		}
		if a, local := saved(t, e); a != nil || !local {
			t.Fatalf("%v: saved %+v %v", tc.answers, a, local)
		}
		if !reflect.DeepEqual(*calls, []remoteCall{{e.o.StateDir, "127.0.0.1:" + strconv.Itoa(r.Port), tc.port}}) || len(d.starts) != 1 || !reflect.DeepEqual(d.actions, []string{"set synthetic typed secret"}) {
			t.Fatalf("%v: calls %+v starts %v actions %q", tc.answers, *calls, d.starts, d.actions)
		}
		if r.Remote == nil || r.Remote.URL != url || r.Remote.HTTPSPort != tc.port || r.Remote.Error != "" {
			t.Fatalf("%v: remote %+v", tc.answers, r.Remote)
		}
		out := told.String()
		if !strings.Contains(out, "2. On this computer and your tailnet") || !strings.Contains(out, "The remote dashboard needs the maintainer secret") || !strings.Contains(out, "on your tailnet at "+url) {
			t.Fatalf("%v: terminal %s", tc.answers, out)
		}
		if len(tc.answers) > 2 && strings.Count(out, "Type a port from 1 to 65535.") != 4 {
			t.Fatalf("port re-ask: %s %q", out, s.prompts)
		}
		report, _ := json.Marshal(r)
		if strings.Contains(string(report), "synthetic") {
			t.Fatalf("a secret is in the report: %s", report)
		}
	}
}

// A secret already set is not offered again; refusals, a declined secret and --no-start keep
// the install OK and name the step for this installation; cancellation ends the install.
func TestInstallDashboardTailnetOutcomes(t *testing.T) {
	e := newEnv(t, "systemd")
	d := newNetworkDaemon(t, e)
	d.secretSet = true
	calls := tailnet(e, "https://host.example.ts.net:8443/dashboard/", nil)
	_, told := questions(e, "2", "8443")
	if r, err := Install(context.Background(), e.o); err != nil || r.Remote == nil || r.Remote.URL == "" || len(d.actions) != 0 || len(*calls) != 1 || strings.Contains(told.String(), "needs the maintainer secret") {
		t.Fatalf("secret set: %+v %v actions %q", r.Remote, err, d.actions)
	}

	e = newEnv(t, "systemd")
	d = newNetworkDaemon(t, e)
	d.secretSet = true
	tailnet(e, "", core.RefusedError{Code: "tailscale_operator_required", Message: "Tailscale refused this account."})
	_, told = questions(e, "2", "8443")
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK || r.Remote == nil || r.Remote.Error != "tailscale_operator_required: Tailscale refused this account." || !strings.Contains(r.Remote.Step, "remote enable --https-port 8443 --state-dir ") || r.Remote.URL != "" {
		t.Fatalf("refusal: %+v %v", r.Remote, err)
	}
	if !strings.Contains(told.String(), "Remote access is not enabled: tailscale_operator_required") {
		t.Fatalf("terminal %s", told.String())
	}
	if a, local := saved(t, e); a != nil || !local {
		t.Fatalf("refusal saved %+v %v", a, local)
	}

	e = newEnv(t, "systemd")
	newNetworkDaemon(t, e)
	calls = tailnet(e, "unused", nil)
	questions(e, "2", "", "3")
	if r, err := Install(context.Background(), e.o); err != nil || r.Remote == nil || r.Remote.Error != "remote_secret_required" || len(*calls) != 0 {
		t.Fatalf("declined secret: %+v %v %v", r.Remote, err, *calls)
	}

	e = newEnv(t, "systemd")
	e.o.NoStart = true
	calls = tailnet(e, "unused", nil)
	_, told = questions(e, "2", "")
	if r, err := Install(context.Background(), e.o); err != nil || r.Remote == nil || r.Remote.Error != "daemon_not_started" || !strings.Contains(r.Remote.Step, "Start the daemon, then run ") || len(*calls) != 0 || strings.Contains(told.String(), "needs the maintainer secret") {
		t.Fatalf("no start: %+v %v %v", r.Remote, err, *calls)
	}

	// The end of input at the port question saves nothing and enables nothing.
	e = newEnv(t, "systemd")
	calls = tailnet(e, "unused", nil)
	questions(e, "2")
	if r, err := Install(context.Background(), e.o); err != nil || r.Remote != nil || len(*calls) != 0 {
		t.Fatalf("ended: %+v %v", r.Remote, err)
	}
	if a, local := saved(t, e); a != nil || local {
		t.Fatalf("ended saved %+v %v", a, local)
	}

	e = newEnv(t, "systemd")
	d = newNetworkDaemon(t, e)
	d.secretSet = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.o.RemoteEnable = func(context.Context, string, string, int) (string, error) {
		cancel()
		return "", ctx.Err()
	}
	questions(e, "2", "")
	if _, err := Install(ctx, e.o); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

// Review of #667: the remote URL is reported only while remote access is on and ready; a
// disabled, unready or failing status gives its reason instead.
func TestRemoteURLNeedsReadyRemoteAccess(t *testing.T) {
	ready := core.RemoteStatus{Configured: true, Listener: "ready", Serve: "ready", Phone: "unverified", URL: "https://host.example.ts.net:8443/dashboard/"}
	for name, tc := range map[string]struct {
		status core.RemoteStatus
		reason string
	}{
		"ready":    {ready, ""},
		"disabled": {core.RemoteStatus{Listener: "off", Serve: "off", URL: ready.URL}, "remote_not_ready: configured false, listener off, Serve off"},
		"missing":  {core.RemoteStatus{Configured: true, Listener: "ready", Serve: "missing", URL: ready.URL, Error: "Serve mapping absent; repeat koinon remote enable"}, "remote_not_ready: Serve mapping absent; repeat koinon remote enable"},
		"conflict": {core.RemoteStatus{Configured: true, Listener: "ready", Serve: "ready", URL: ready.URL, Error: "tailscale_serve_conflict: synthetic"}, "remote_not_ready: tailscale_serve_conflict: synthetic"},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tc.status)
		}))
		url, err := RemoteURL(context.Background(), strings.TrimPrefix(s.URL, "http://"), "synthetic-bearer")
		s.Close()
		if tc.reason == "" && (err != nil || url != ready.URL) || tc.reason != "" && (err == nil || err.Error() != tc.reason || url != "") {
			t.Fatalf("%s: %q %v", name, url, err)
		}
	}
}

// Review of #667: both recovery commands name this installation's state directory and daemon
// address, so neither a default nor KOINON_STATE_DIR or KOINON_DAEMON_ADDRESS reaches another
// daemon.
func TestTailnetRecoveryNamesThisInstallation(t *testing.T) {
	t.Setenv("KOINON_DAEMON_ADDRESS", "127.0.0.1:1")
	e := newEnv(t, "systemd")
	// The environment names this installation's state directory and a stale address.
	t.Setenv("KOINON_STATE_DIR", e.o.StateDir)
	newNetworkDaemon(t, e)
	questions(e, "2", "8443", "3")
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Remote == nil || r.Remote.Error != "remote_secret_required" {
		t.Fatalf("install: %v %+v", err, r.Remote)
	}
	address := "127.0.0.1:" + strconv.Itoa(r.Port)
	tail := " --state-dir " + setup.ShellWord(e.o.StateDir) + " --address " + setup.ShellWord(address)
	if !strings.Contains(r.Remote.Step, "maintainer secret set"+tail+",") || !strings.HasSuffix(r.Remote.Step, "remote enable --https-port 8443"+tail+".") {
		t.Fatalf("step %q", r.Remote.Step)
	}
}
