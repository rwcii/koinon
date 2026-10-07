package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// recorder is a patched service manager: it logs every command and answers with the
// outcome its fail function chooses. No test reaches a real manager.
type recorder struct {
	calls   []string
	fail    func(argv []string) bool
	running bool
}

func (r *recorder) run(_ context.Context, argv ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(argv, " "))
	if r.fail != nil && r.fail(argv) {
		return []byte("refused"), errors.New("exit 1")
	}
	command := strings.Join(argv, " ")
	switch {
	case strings.Contains(command, "restart") || strings.Contains(command, "bootstrap"):
		r.running = true
	case strings.Contains(command, "disable --now") || strings.Contains(command, "bootout"):
		r.running = false
	case strings.HasPrefix(command, "launchctl print gui/501/"):
		if !r.running {
			return []byte("Could not find service"), errors.New("exit status 113")
		}
	case strings.Contains(command, "is-active"):
		if !r.running {
			return []byte("inactive"), errors.New("exit 3")
		}
	}
	return nil, nil
}

type env struct {
	o        Options
	r        *recorder
	setups   []string
	removals []string
}

func newEnv(t *testing.T, backend string) *env {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "koinon-download")
	if err := os.WriteFile(source, []byte("synthetic binary"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := platform.PrivateDir(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "secret"), []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	e := &env{r: &recorder{}}
	e.o = Options{Prefix: filepath.Join(dir, "prefix"), StateDir: state, PythonPrefix: filepath.Join(dir, "python"), Source: source,
		Services: platform.Services{Backend: backend, Home: filepath.Join(dir, "home"), UID: 501, Run: e.r.run},
		Setup: func(_ context.Context, o setup.Options) (setup.Report, error) {
			e.setups = append(e.setups, o.Family+" "+o.Binary)
			return setup.Report{OK: true, Family: o.Family}, nil
		},
		Remove: func(_ context.Context, o setup.Options) (setup.Report, error) {
			e.removals = append(e.removals, o.Family+" "+o.Binary)
			return setup.Report{OK: true, Family: o.Family}, nil
		},
		Status: func(context.Context, string, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil },
	}
	return e
}

func TestInstallAndUninstall(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			e := newEnv(t, backend)
			e.o.Agents = []string{"codex", "claude"}
			r, err := Install(context.Background(), e.o)
			if err != nil || r.Service != "running" || r.BinaryResult != "replaced" {
				t.Fatalf("install %+v %v", r, err)
			}
			data, err := os.ReadFile(r.Binary)
			if err != nil || string(data) != "synthetic binary" {
				t.Fatalf("binary %q %v", data, err)
			}
			if info, _ := os.Stat(r.Binary); info.Mode().Perm() != 0755 {
				t.Fatalf("binary mode %v", info.Mode())
			}
			artifact, _ := os.ReadFile(r.Artifact)
			if !strings.Contains(string(artifact), platform.ServiceMarker) || !strings.Contains(string(artifact), r.Binary) {
				t.Fatalf("artifact %s", artifact)
			}
			want := map[string]string{"systemd": "systemctl --user enable koinon.service", "launchd": "launchctl bootstrap gui/501 " + r.Artifact}[backend]
			if !strings.Contains(strings.Join(e.r.calls, "\n"), want) {
				t.Fatalf("manager calls %v", e.r.calls)
			}
			if len(e.setups) != 2 || e.setups[0] != "codex "+r.Binary {
				t.Fatalf("setup %v", e.setups)
			}
			// A repeated install keeps the binary and artifact and restarts the service.
			again, err := Install(context.Background(), e.o)
			if err != nil || again.BinaryResult != "unchanged" {
				t.Fatalf("repeat %+v %v", again, err)
			}
			u, err := Uninstall(context.Background(), e.o)
			// Uninstall removes setup for the agents given, here codex and claude.
			if err != nil || u.Service != "removed" || u.BinaryResult != "removed" || len(e.removals) != 2 {
				t.Fatalf("uninstall %+v %v %v", u, err, e.removals)
			}
			if _, err := os.Stat(r.Artifact); !os.IsNotExist(err) {
				t.Fatalf("artifact left: %v", err)
			}
			if _, err := os.Stat(e.o.StateDir); err != nil {
				t.Fatalf("state removed: %v", err)
			}
			repeat, err := Uninstall(context.Background(), e.o)
			if err != nil || repeat.Service != "absent" || repeat.BinaryResult != "absent" {
				t.Fatalf("repeat uninstall %+v %v", repeat, err)
			}
		})
	}
}

func TestInstallRefusals(t *testing.T) {
	e := newEnv(t, "systemd")
	// An artifact at the daemon's path without the marker is never replaced.
	os.MkdirAll(filepath.Dir(e.o.Services.DaemonArtifact()), 0755)
	os.WriteFile(e.o.Services.DaemonArtifact(), []byte("[Service]\nExecStart=/usr/bin/other\n"), 0644)
	if _, err := Install(context.Background(), e.o); err == nil || !strings.Contains(err.Error(), "service_artifact_unowned") {
		t.Fatalf("unowned artifact: %v", err)
	}
	if _, err := Uninstall(context.Background(), e.o); err == nil || !strings.Contains(err.Error(), "service_artifact_unowned") {
		t.Fatalf("unowned removal: %v", err)
	}
	os.Remove(e.o.Services.DaemonArtifact())
	// A Python-era installation refuses a plain install.
	os.MkdirAll(e.o.PythonPrefix, 0700)
	os.WriteFile(filepath.Join(e.o.PythonPrefix, "install.json"), []byte(`{"state_root": "/x"}`), 0600)
	if _, err := Install(context.Background(), e.o); err == nil || !strings.Contains(err.Error(), "python_install_present") {
		t.Fatalf("python present: %v", err)
	}
	e.o.Upgrade = true
	// Without a reachable manager the install reports the start command.
	e.r.fail = func(argv []string) bool { return argv[0] == "systemctl" }
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Service != "manual_required" || r.StartCommand != r.Binary+" serve --state-dir "+e.o.StateDir {
		t.Fatalf("manual %+v %v", r, err)
	}
	// --no-start stages without any manager call.
	e.r.calls = nil
	e.o.NoStart = true
	if r, err := Install(context.Background(), e.o); err != nil || r.Service != "staged" || len(e.r.calls) != 0 {
		t.Fatalf("staged %+v %v %v", r, err, e.r.calls)
	}
}
