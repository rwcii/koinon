package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// These tests drive rendered service artifacts and the real private configuration functions,
// with a synthetic resolver and patched service managers. They change no host service.
func TestInstallDashboardNetwork(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			e := newEnv(t, backend)
			ip := "192.0.2.10"
			var resolved []string
			e.o.ResolveDashboard = func(_ context.Context, address string) (core.DashboardAddress, error) {
				resolved = append(resolved, address)
				return core.DashboardAddress{Host: "board.example.test", IP: ip, Port: 8443}, nil
			}
			e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
				a, err := core.ReadDashboardAddress(e.o.StateDir)
				if err != nil {
					return nil, err
				}
				if a == nil {
					return json.RawMessage(`{}`), nil
				}
				return json.Marshal(map[string]any{"dashboard_network": core.DashboardNetworkStatus{Address: a.Address(), Bind: a.Bind(), Code: "dashboard_secret_required", Step: "Set the maintainer secret, then run koinon install."}})
			}
			e.o.DashboardAddress = "board.example.test:8443"
			r, err := Install(context.Background(), e.o)
			if err != nil || !r.OK || r.DashboardNetwork == nil || r.DashboardNetwork.Code != "dashboard_secret_required" {
				t.Fatalf("install %+v %v", r, err)
			}
			artifact, err := os.ReadFile(r.Artifact)
			if err != nil || !strings.Contains(string(artifact), e.o.StateDir) || strings.Contains(string(artifact), "board.example.test") {
				t.Fatalf("startup does not read the state directory: %s %v", artifact, err)
			}
			for _, upgrade := range []bool{false, true} {
				e.o.DashboardAddress, e.o.Upgrade = "", upgrade
				r, err := Install(context.Background(), e.o)
				a, readErr := core.ReadDashboardAddress(e.o.StateDir)
				if err != nil || readErr != nil || a == nil || a.Host != "board.example.test" || r.DashboardNetwork == nil {
					t.Fatalf("keep %+v %+v %v %v", r, a, err, readErr)
				}
			}
			ip = "192.0.2.20" // another installation re-resolves a saved FQDN after a DHCP change
			e.o.Upgrade = false
			if _, err := Install(context.Background(), e.o); err != nil {
				t.Fatal(err)
			}
			a, err := core.ReadDashboardAddress(e.o.StateDir)
			if err != nil || a.IP != ip || len(resolved) != 3 {
				t.Fatalf("re-resolve %+v %v %v", a, resolved, err)
			}
			e.o.DashboardAddress = "none"
			if r, err := Install(context.Background(), e.o); err != nil || r.DashboardNetwork != nil {
				t.Fatalf("remove %+v %v", r, err)
			}
			if a, err := core.ReadDashboardAddress(e.o.StateDir); err != nil || a != nil {
				t.Fatalf("removed setting %+v %v", a, err)
			}
		})
	}
}

func TestInstallDashboardNetworkBackgroundRestart(t *testing.T) {
	e := newEnv(t, "systemd")
	e.r.fail = func([]string) bool { return true }
	d := newDaemon(e, "v1")
	ctx := context.Background()
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	old := d.pid
	e.o.ResolveDashboard = func(context.Context, string) (core.DashboardAddress, error) {
		return core.DashboardAddress{Host: "192.0.2.10", IP: "192.0.2.10", Port: 8443}, nil
	}
	e.o.DashboardAddress = "192.0.2.10:8443"
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	if len(d.starts) != 2 || !reflect.DeepEqual(d.stops, []int{old}) {
		t.Fatalf("unchanged binary kept stale configuration: starts%v stops%v", d.starts, d.stops)
	}
	// Keeping the same setting does not restart an unchanged background daemon.
	e.o.DashboardAddress = ""
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	if len(d.starts) != 2 {
		t.Fatalf("unchanged setting restarted: %v", d.starts)
	}
	// A previous failed bind must be retried by install even with the same binary/address.
	failedNetwork := true
	readStatus := e.o.Status
	e.o.Status = func(ctx context.Context, address, secret string) (json.RawMessage, error) {
		data, err := readStatus(ctx, address, secret)
		if err != nil {
			return nil, err
		}
		var status map[string]any
		if err := json.Unmarshal(data, &status); err != nil {
			return nil, err
		}
		if a, _ := core.ReadDashboardAddress(e.o.StateDir); a != nil {
			code := "running"
			if failedNetwork {
				code = "dashboard_address_unavailable"
			}
			status["dashboard_network"] = core.DashboardNetworkStatus{Address: a.Address(), Bind: a.Bind(), Code: code}
		}
		return json.Marshal(status)
	}
	start := e.o.Background
	e.o.Background = func(binary, state, log string) (Started, error) {
		result, err := start(binary, state, log)
		if err == nil {
			failedNetwork = false
		}
		return result, err
	}
	old = d.pid
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	if len(d.starts) != 3 || d.stops[len(d.stops)-1] != old {
		t.Fatalf("failed bind was not retried: %v %v", d.starts, d.stops)
	}
	old = d.pid
	e.o.DashboardAddress = "none"
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	if len(d.starts) != 4 || d.stops[len(d.stops)-1] != old {
		t.Fatalf("removing listener did not restart: %v %v", d.starts, d.stops)
	}
}

func TestInstallDashboardNetworkRefusalBeforeBinary(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.DashboardAddress = "127.0.0.1:8443"
	if _, err := Install(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), "dashboard_address_invalid:") {
		t.Fatalf("loopback accepted: %v", err)
	}
	if _, err := os.Stat(Binary(e.o.Prefix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("placed binary before address validation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.o.StateDir, "dashboard-address.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recorded refused address: %v", err)
	}
}
