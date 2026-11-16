package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
)

// fakeDashboard is a daemon for koinon remote configure (#685): its status, its remote routes
// and a restart through the patched service manager, which opens the saved network address
// as a real start does. No test reaches a real daemon, Serve or service manager.
type fakeDashboard struct {
	state   string
	pid     int
	secret  bool
	network *core.DashboardAddress // what runs
	remote  bool
	port    int
	calls   []string
	fail    map[string]bool // remote_disable, remote_enable, restart, open
}

func (f *fakeDashboard) writeRemote(t *testing.T) {
	t.Helper()
	port := f.port
	if port == 0 {
		port = remote.DefaultHTTPSPort
	}
	origin := "https://board.example.ts.net"
	if port != remote.DefaultHTTPSPort {
		origin += ":" + strconv.Itoa(port)
	}
	data, err := json.Marshal(core.RemoteConfig{Version: 1, Enabled: f.remote, Owned: f.remote, Mapping: remote.Mapping{Origin: origin, Target: "http://127.0.0.1:47690"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.WriteAtomic(filepath.Join(f.state, "remote-dashboard.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeDashboard) networkStatus() *core.DashboardNetworkStatus {
	if f.network == nil {
		return nil
	}
	s := &core.DashboardNetworkStatus{Address: f.network.Address(), Bind: f.network.Bind(), URL: "https://" + f.network.Address() + "/dashboard/", Code: "running"}
	if !f.secret {
		s.Code = "dashboard_secret_required"
	}
	return s
}

// setupConfigure makes an installed environment whose daemon answers as f says. saved is
// the network address saved for the next start; f.network is what runs now.
func setupConfigure(t *testing.T, f *fakeDashboard, asked bool, saved *core.DashboardAddress) *env {
	t.Helper()
	e := newEnv(t, "systemd")
	f.state = e.o.StateDir
	f.pid = 100
	if f.fail == nil {
		f.fail = map[string]bool{}
	}
	if err := os.MkdirAll(filepath.Dir(Binary(e.o.Prefix)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Binary(e.o.Prefix), []byte("synthetic binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if asked && saved == nil {
		if err := core.WriteDashboardLocal(e.o.StateDir); err != nil {
			t.Fatal(err)
		}
	}
	if saved != nil {
		if err := core.WriteDashboardAddress(e.o.StateDir, saved); err != nil {
			t.Fatal(err)
		}
	}
	if f.remote || f.port != 0 {
		f.writeRemote(t)
	}
	e.o.Address = "127.0.0.1:47681"
	e.o.Wait = 5 * time.Second
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		if f.fail["down"] {
			return nil, errors.New("connection refused")
		}
		return json.Marshal(map[string]any{"pid": f.pid, "maintainer_secret_set": f.secret, "dashboard_network": f.networkStatus()})
	}
	e.o.Call = func(_ context.Context, _, _, path string, _ any) (json.RawMessage, error) {
		switch path {
		case "/v1/remote/status":
			s := core.RemoteStatus{Configured: f.remote, Listener: "off", Serve: "off", Phone: "unverified"}
			if f.remote {
				s.Listener, s.Serve, s.URL = "ready", "ready", fmt.Sprintf("https://board.example.ts.net:%d/dashboard/", f.port)
			}
			return json.Marshal(s)
		case "/v1/remote/disable":
			f.calls = append(f.calls, "remote_disable")
			if f.fail["remote_disable"] {
				return nil, core.RefusedError{Code: "remote_disable_failed"}
			}
			f.remote = false
			f.writeRemote(t)
			return json.RawMessage(`{}`), nil
		}
		return nil, errors.New("unexpected route " + path)
	}
	e.o.RemoteEnable = func(_ context.Context, _, _ string, port int) (string, error) {
		f.calls = append(f.calls, "remote_enable "+strconv.Itoa(port))
		if f.fail["remote_enable"] {
			return "", core.RefusedError{Code: "remote_setup_required"}
		}
		f.remote, f.port = true, port
		f.writeRemote(t)
		return "https://board.example.ts.net/dashboard/", nil
	}
	e.o.ResolveDashboard = func(_ context.Context, address string) (core.DashboardAddress, error) {
		host, port, err := net.SplitHostPort(address)
		n, _ := strconv.Atoi(port)
		if err != nil || net.ParseIP(host) == nil {
			return core.DashboardAddress{}, errors.New("dashboard_address_invalid: not an address of this host")
		}
		return core.DashboardAddress{Host: host, IP: host, Port: n}, nil
	}
	run := e.o.Services.Run
	e.o.Services.Run = func(ctx context.Context, argv ...string) ([]byte, error) {
		if len(argv) == 4 && argv[2] == "restart" {
			f.calls = append(f.calls, "restart")
			if f.fail["restart"] {
				return []byte("refused"), errors.New("exit 1")
			}
			// The new daemon opens the saved network address.
			saved, err := core.ReadDashboardAddress(f.state)
			if err != nil {
				t.Fatal(err)
			}
			f.pid++
			f.network = saved
			if f.fail["open"] && saved != nil {
				f.secret = false
			}
		}
		return run(ctx, argv...)
	}
	return e
}

func stepNames(c DashboardChange) []string {
	names := []string{}
	for _, s := range c.Steps {
		name := s.Step
		if !s.Done {
			name += " FAILED"
		}
		names = append(names, name)
	}
	return names
}

var (
	addressA = &core.DashboardAddress{Host: "192.0.2.10", IP: "192.0.2.10", Port: 8443}
	addressB = &core.DashboardAddress{Host: "192.0.2.11", IP: "192.0.2.11", Port: 9443}
)

// Each change closes the old access before it opens the new one, does only the steps that the
// target needs, and an equal choice that runs changes nothing (#685, C4 and C5).
func TestConfigureDashboardTransitions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       fakeDashboard
		asked   bool
		saved   *core.DashboardAddress
		target  DashboardTarget
		steps   []string
		calls   []string
		choice  string
		port    int
		network *core.DashboardAddress
	}{
		{name: "unanswered local saves the answer", target: DashboardTarget{Choice: DashboardLocal},
			steps: []string{"save_choice"}, calls: []string{}, choice: DashboardLocal},
		{name: "local again", asked: true, target: DashboardTarget{Choice: DashboardLocal},
			steps: []string{}, calls: []string{}, choice: DashboardLocal},
		{name: "1 to 2", f: fakeDashboard{secret: true}, asked: true, target: DashboardTarget{Choice: DashboardTailnet},
			steps: []string{"remote_enable"}, calls: []string{"remote_enable 443"}, choice: DashboardTailnet, port: 443},
		{name: "2 again at the saved port", f: fakeDashboard{secret: true, remote: true, port: 8444}, asked: true, target: DashboardTarget{Choice: DashboardTailnet},
			steps: []string{}, calls: []string{}, choice: DashboardTailnet, port: 8444},
		{name: "2 to another port", f: fakeDashboard{secret: true, remote: true, port: 443}, asked: true, target: DashboardTarget{Choice: DashboardTailnet, HTTPSPort: 8444},
			steps: []string{"remote_enable"}, calls: []string{"remote_enable 8444"}, choice: DashboardTailnet, port: 8444},
		{name: "2 to 1", f: fakeDashboard{secret: true, remote: true, port: 443}, asked: true, target: DashboardTarget{Choice: DashboardLocal},
			steps: []string{"remote_disable"}, calls: []string{"remote_disable"}, choice: DashboardLocal, port: 443},
		{name: "1 to 3", f: fakeDashboard{secret: true}, asked: true, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"},
			steps: []string{"save_network_address", "daemon_restart", "network_address_open"}, calls: []string{"restart"}, choice: DashboardNetwork, network: addressA},
		{name: "3 to 1", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardLocal},
			steps: []string{"save_no_network_address", "daemon_restart"}, calls: []string{"restart"}, choice: DashboardLocal},
		{name: "3 to another address", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.11:9443"},
			steps: []string{"save_network_address", "daemon_restart", "network_address_open"}, calls: []string{"restart"}, choice: DashboardNetwork, network: addressB},
		{name: "3 again at the saved address", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardNetwork},
			steps: []string{}, calls: []string{}, choice: DashboardNetwork, network: addressA},
		{name: "3 saved but not open yet", f: fakeDashboard{secret: true}, saved: addressA, target: DashboardTarget{Choice: DashboardNetwork},
			steps: []string{"daemon_restart", "network_address_open"}, calls: []string{"restart"}, choice: DashboardNetwork, network: addressA},
		{name: "3 to 2", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardTailnet},
			steps: []string{"save_no_network_address", "daemon_restart", "remote_enable"}, calls: []string{"restart", "remote_enable 443"}, choice: DashboardTailnet, port: 443},
		{name: "2 to 3", f: fakeDashboard{secret: true, remote: true, port: 443}, asked: true, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"},
			steps: []string{"remote_disable", "save_network_address", "daemon_restart", "network_address_open"}, calls: []string{"remote_disable", "restart"}, choice: DashboardNetwork, port: 443, network: addressA},
		{name: "both to 3", f: fakeDashboard{secret: true, remote: true, port: 443, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardNetwork},
			steps: []string{"remote_disable"}, calls: []string{"remote_disable"}, choice: DashboardNetwork, port: 443, network: addressA},
		{name: "both to 2", f: fakeDashboard{secret: true, remote: true, port: 443, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardTailnet},
			steps: []string{"save_no_network_address", "daemon_restart"}, calls: []string{"restart"}, choice: DashboardTailnet, port: 443},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			e := setupConfigure(t, &f, tc.asked, tc.saved)
			c, err := ConfigureDashboard(context.Background(), e.o, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if got := stepNames(c); !reflect.DeepEqual(got, tc.steps) || c.Changed != (len(tc.steps) > 0) {
				t.Fatalf("steps %v changed %t, want %v", got, c.Changed, tc.steps)
			}
			if f.calls == nil {
				f.calls = []string{}
			}
			if !reflect.DeepEqual(f.calls, tc.calls) {
				t.Fatalf("calls %v, want %v", f.calls, tc.calls)
			}
			a := c.After
			if a == nil || a.Choice != tc.choice || a.HTTPSPort != tc.port || !reflect.DeepEqual(a.Network, tc.network) || !a.Asked || a.Runtime == nil {
				t.Fatalf("after %+v", a)
			}
			// What runs agrees with what is saved.
			if tc.network != nil && (a.Runtime.Network == nil || a.Runtime.Network.Code != "running" || a.Runtime.Network.Address != tc.network.Address()) ||
				tc.network == nil && a.Runtime.Network != nil || a.Runtime.Remote.Configured != (tc.choice == DashboardTailnet) {
				t.Fatalf("runtime %+v %+v", a.Runtime.Network, a.Runtime.Remote)
			}
			for _, s := range c.Steps {
				want := map[string]bool{"remote_disable": true, "daemon_restart": true}[s.Step] || s.Step == "remote_enable" && tc.f.remote
				if (s.Effect != "") != want {
					t.Fatalf("step %s effect %q", s.Step, s.Effect)
				}
			}
		})
	}
}

// A failed step ends the change: the steps before it stay done, the result names the command
// that finishes the failed step, and no later step runs (#685, C4).
func TestConfigureDashboardStopsAtAFailedStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		f      fakeDashboard
		saved  *core.DashboardAddress
		target DashboardTarget
		steps  []string
		finish string
		choice string
	}{
		{name: "disable", f: fakeDashboard{secret: true, remote: true, fail: map[string]bool{"remote_disable": true}}, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"},
			steps: []string{"remote_disable FAILED"}, finish: "remote disable", choice: DashboardTailnet},
		{name: "restart", f: fakeDashboard{secret: true, fail: map[string]bool{"restart": true}}, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"},
			steps: []string{"save_network_address", "daemon_restart FAILED"}, finish: " install", choice: DashboardNetwork},
		{name: "open", f: fakeDashboard{secret: true, fail: map[string]bool{"open": true}}, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"},
			steps: []string{"save_network_address", "daemon_restart", "network_address_open FAILED"}, finish: " install", choice: DashboardNetwork},
		{name: "enable", f: fakeDashboard{secret: true, network: addressA, fail: map[string]bool{"remote_enable": true}}, saved: addressA, target: DashboardTarget{Choice: DashboardTailnet},
			steps: []string{"save_no_network_address", "daemon_restart", "remote_enable FAILED"}, finish: "remote enable --https-port 443", choice: DashboardLocal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			e := setupConfigure(t, &f, true, tc.saved)
			c, err := ConfigureDashboard(context.Background(), e.o, tc.target)
			if err == nil || !strings.HasPrefix(err.Error(), "dashboard_change_incomplete: ") {
				t.Fatal(err)
			}
			if got := stepNames(c); !reflect.DeepEqual(got, tc.steps) {
				t.Fatalf("steps %v, want %v", got, tc.steps)
			}
			last := c.Steps[len(c.Steps)-1]
			if !strings.Contains(last.Finish, tc.finish) || last.Error == "" || !strings.HasPrefix(last.Finish, Binary(e.o.Prefix)) {
				t.Fatalf("failed step %+v", last)
			}
			if c.After == nil || c.After.Choice != tc.choice {
				t.Fatalf("after %+v", c.After)
			}
		})
	}
}

// Everything that can refuse a change refuses before the first change (#685, C2 and C3).
func TestConfigureDashboardRefusesWithoutEffects(t *testing.T) {
	declined := func(prompt string) (string, error) { return "3", nil }
	ended := func(prompt string) (string, error) { return "", io.EOF }
	for _, tc := range []struct {
		name   string
		f      fakeDashboard
		saved  *core.DashboardAddress
		target DashboardTarget
		line   func(string) (string, error)
		noBin  bool
		code   string
	}{
		{name: "no secret without a terminal", target: DashboardTarget{Choice: DashboardTailnet}, code: "remote_secret_required"},
		{name: "secret declined", target: DashboardTarget{Choice: DashboardTailnet}, line: declined, code: "remote_secret_required"},
		{name: "secret question ended", f: fakeDashboard{remote: true}, target: DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"}, line: ended, code: "remote_secret_required"},
		{name: "no network address", f: fakeDashboard{secret: true}, target: DashboardTarget{Choice: DashboardNetwork}, code: "dashboard_address_required"},
		{name: "unresolved address", f: fakeDashboard{secret: true}, target: DashboardTarget{Choice: DashboardNetwork, Address: "board.example:8443"}, code: "dashboard_address_invalid"},
		{name: "daemon down", f: fakeDashboard{secret: true, fail: map[string]bool{"down": true}}, target: DashboardTarget{Choice: DashboardTailnet}, code: "daemon_unavailable"},
		{name: "no installed binary", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, target: DashboardTarget{Choice: DashboardLocal}, noBin: true, code: "install_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			e := setupConfigure(t, &f, true, tc.saved)
			if tc.noBin {
				os.Remove(Binary(e.o.Prefix))
			}
			e.o.Line = tc.line
			before := stateFiles(t, e.o.StateDir)
			_, err := ConfigureDashboard(context.Background(), e.o, tc.target)
			if err == nil || !strings.HasPrefix(err.Error(), tc.code) {
				t.Fatal(err)
			}
			if len(f.calls) != 0 || len(e.r.calls) != 0 {
				t.Fatalf("calls %v %v", f.calls, e.r.calls)
			}
			if after := stateFiles(t, e.o.StateDir); !reflect.DeepEqual(before, after) {
				t.Fatalf("state changed:\n%v\n%v", before, after)
			}
		})
	}
}

// stateFiles is the content of each file of the state directory.
func stateFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(data)
	}
	return files
}

// The question of koinon remote configure has the current values as its defaults, checks each
// answer as the first install does, and ended input gives no answer (#685, C2).
func TestAskDashboardTargetDefaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       fakeDashboard
		asked   bool
		saved   *core.DashboardAddress
		answers []string
		prompts []string
		target  DashboardTarget
		ok      bool
	}{
		{name: "tailnet keeps its port", f: fakeDashboard{secret: true, remote: true, port: 8444}, asked: true, answers: []string{"", ""},
			prompts: []string{"Choice [2]: ", "Tailscale Serve HTTPS port [8444]: "}, target: DashboardTarget{Choice: DashboardTailnet, HTTPSPort: 8444}, ok: true},
		{name: "network keeps its address", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, answers: []string{"", "", ""},
			prompts: []string{"Choice [3]: ", "Address number, or a fully qualified name of this host [192.0.2.10]: ", "Port [8443]: "},
			target:  DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:8443"}, ok: true},
		{name: "network to another port", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, answers: []string{"", "", "9443"},
			prompts: []string{"Choice [3]: ", "Address number, or a fully qualified name of this host [192.0.2.10]: ", "Port [8443]: "},
			target:  DashboardTarget{Choice: DashboardNetwork, Address: "192.0.2.10:9443"}, ok: true},
		{name: "local to tailnet takes the install default", asked: true, answers: []string{"2", ""},
			prompts: []string{"Choice [1]: ", "Tailscale Serve HTTPS port [443]: "}, target: DashboardTarget{Choice: DashboardTailnet, HTTPSPort: 443}, ok: true},
		{name: "both has no default", f: fakeDashboard{secret: true, remote: true, network: addressA}, saved: addressA, answers: []string{"", "1"},
			prompts: []string{"Choice: ", "Choice: "}, target: DashboardTarget{Choice: DashboardLocal}, ok: true},
		{name: "ended at the choice", f: fakeDashboard{secret: true}, asked: true,
			prompts: []string{"Choice [1]: "}},
		{name: "ended at the port", f: fakeDashboard{secret: true, network: addressA}, saved: addressA, answers: []string{"", ""},
			prompts: []string{"Choice [3]: ", "Address number, or a fully qualified name of this host [192.0.2.10]: ", "Port [8443]: "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			e := setupConfigure(t, &f, tc.asked, tc.saved)
			sc := &script{t: t, answers: tc.answers}
			e.o.Line = sc.line
			e.o.HostAddresses = func() ([]string, error) { return []string{"192.0.2.10", "192.0.2.11"}, nil }
			before := stateFiles(t, e.o.StateDir)
			current, err := ReadDashboardCurrent(context.Background(), e.o)
			if err != nil {
				t.Fatal(err)
			}
			target, ok, err := AskDashboardTarget(context.Background(), e.o, current)
			if err != nil || ok != tc.ok || target != tc.target || !reflect.DeepEqual(sc.prompts, tc.prompts) {
				t.Fatalf("%+v %t %v %q", target, ok, err, sc.prompts)
			}
			if after := stateFiles(t, e.o.StateDir); !reflect.DeepEqual(before, after) || len(f.calls) != 0 {
				t.Fatal("the question changed something", f.calls)
			}
		})
	}
}
