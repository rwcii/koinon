package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// koinon install --port records the port in the state directory and waits for the daemon
// there; the service file stays `serve --state-dir`, and later installs, an upgrade among
// them, keep the port (#405).
func TestInstallRecordsPort(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t, backend)
			var waited []string
			e.o.Status = func(_ context.Context, address, _ string) (json.RawMessage, error) {
				waited = append(waited, address)
				return json.RawMessage(`{}`), nil
			}
			// Without --port, nothing is recorded and the port stays 47671.
			r, err := Install(ctx, e.o)
			if err != nil || r.Port != 47671 || waited[len(waited)-1] != "127.0.0.1:47671" {
				t.Fatalf("default %+v %v %v", r, err, waited)
			}
			if _, err := os.Stat(filepath.Join(e.o.StateDir, "port")); !os.IsNotExist(err) {
				t.Fatalf("port recorded without --port: %v", err)
			}
			unchanged, _ := os.ReadFile(r.Artifact)

			e.o.Port = 47670
			r, err = Install(ctx, e.o)
			if err != nil || !r.OK || r.Port != 47670 || waited[len(waited)-1] != "127.0.0.1:47670" {
				t.Fatalf("--port %+v %v %v", r, err, waited)
			}
			if data, _ := os.ReadFile(filepath.Join(e.o.StateDir, "port")); string(data) != "47670\n" {
				t.Fatalf("port file %q", data)
			}
			artifact, _ := os.ReadFile(r.Artifact)
			if !bytes.Equal(artifact, unchanged) || !bytes.Equal(artifact, e.o.Services.RenderDaemon(r.Binary, e.o.StateDir)) ||
				strings.Contains(string(artifact), "47670") || strings.Contains(string(artifact), "--listen") {
				t.Fatalf("service file changed: %s", artifact)
			}
			if !strings.Contains(strings.Join(e.r.calls, "\n"), map[string]string{"systemd": "systemctl --user restart koinon.service",
				"launchd": "launchctl kickstart gui/501/" + "io.github.rwcii.koinon.daemon"}[backend]) {
				t.Fatalf("the service was not restarted: %v", e.r.calls)
			}

			// A later install and an upgrade's install name no port and keep it.
			for _, upgrade := range []bool{false, true} {
				o := e.o
				o.Port, o.Upgrade = 0, upgrade
				if r, err := Install(ctx, o); err != nil || r.Port != 47670 || waited[len(waited)-1] != "127.0.0.1:47670" {
					t.Fatalf("upgrade %v: %+v %v", upgrade, r, err)
				}
			}
			// --port 47671 sets the default again.
			e.o.Port = 47671
			if r, err := Install(ctx, e.o); err != nil || r.Port != 47671 || waited[len(waited)-1] != "127.0.0.1:47671" {
				t.Fatalf("back to the default %+v %v", r, err)
			}
			// --no-start records the port for the service's later start.
			e.o.Port, e.o.NoStart = 47672, true
			if r, err := Install(ctx, e.o); err != nil || r.Service != "staged" || r.Port != 47672 {
				t.Fatalf("no start %+v %v", r, err)
			}
			if port, err := core.RecordedPort(e.o.StateDir); err != nil || port != 47672 {
				t.Fatalf("no start recorded %d %v", port, err)
			}
		})
	}
}

// An invalid port, or a damaged record, refuses the install before the binary is placed.
func TestInstallPortRefusals(t *testing.T) {
	e := newEnv(t, "systemd")
	for _, port := range []int{-1, 65536} {
		e.o.Port = port
		if _, err := Install(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), "invalid_request: ") {
			t.Fatalf("port %d: %v", port, err)
		}
	}
	e.o.Port = 0
	os.WriteFile(filepath.Join(e.o.StateDir, "port"), []byte("http\n"), 0600)
	if _, err := Install(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), "port_invalid: ") {
		t.Fatalf("damaged record: %v", err)
	}
	if _, err := os.Stat(Binary(e.o.Prefix)); !os.IsNotExist(err) {
		t.Fatalf("binary placed: %v", err)
	}
}

// System mode keeps the plist's arguments, which a hand-written LaunchDaemon must match, and
// verifies the daemon at the recorded port (#405).
func TestInstallSystemModeUsesRecordedPort(t *testing.T) {
	e, f := systemEnv(t)
	var waited []string
	e.o.Status = func(ctx context.Context, address, _ string) (json.RawMessage, error) {
		waited = append(waited, address)
		pid, err := f.StatusPID(ctx)
		return json.RawMessage(fmt.Sprintf(`{"pid":%d}`, pid)), err
	}
	e.o.System, e.o.Port = true, 47670
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Service != "system" || r.Port != 47670 {
		t.Fatalf("install %+v %v", r, err)
	}
	plist, _ := os.ReadFile(e.o.Services.SystemArtifact())
	if !bytes.Equal(plist, e.o.Services.RenderSystemDaemon(r.Binary, e.o.StateDir)) {
		t.Fatalf("plist %s", plist)
	}
	for _, address := range waited {
		if address != "127.0.0.1:47670" {
			t.Fatalf("verified at %s", address)
		}
	}
	e.o.System, e.o.Port = false, 0
	if r, err := Install(context.Background(), e.o); err != nil || r.Service != "system" || r.Port != 47670 || waited[len(waited)-1] != "127.0.0.1:47670" {
		t.Fatalf("later install %+v %v", r, err)
	}
}
