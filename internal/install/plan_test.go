package install

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/setup"
)

// planEnv is an installed user-mode daemon with a maintainer secret, and agent families that
// are not installed: its plan is empty until a test changes one of them.
func planEnv(t *testing.T, backend string) *env {
	t.Helper()
	e := newEnv(t, backend)
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true,"maintainer_secret_set":true}`), nil
	}
	e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection { return setup.Inspection{Family: o.Family} }
	e.o.StatusWait = 300 * time.Millisecond
	if _, err := place(e.o.Source, Binary(e.o.Prefix)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.Services.WriteDaemon(Binary(e.o.Prefix), e.o.StateDir); err != nil {
		t.Fatal(err)
	}
	return e
}

func plan(t *testing.T, e *env) []string {
	t.Helper()
	steps, err := Plan(context.Background(), e.o)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return steps
}

// koinon install --plan lists only the steps that this installation still needs (#551).
func TestPlanSteps(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		e := planEnv(t, backend)
		if steps := plan(t, e); len(steps) != 0 {
			t.Fatalf("%s: current installation: %q", backend, steps)
		}
		b := e.o.Source
		// No secret, and a family whose CLI is installed without its koinon server.
		e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
			return json.RawMessage(`{"maintainer_secret_set":false}`), nil
		}
		e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection {
			if o.Binary != Binary(e.o.Prefix) {
				t.Fatalf("inspected for %s", o.Binary)
			}
			in := setup.Inspection{Family: o.Family}
			switch o.Family {
			case "codex":
				in.CLI = "/usr/bin/codex"
			case "claude":
				in.CLI, in.Configured = "/usr/bin/claude", true
			}
			return in
		}
		if steps, want := plan(t, e), []string{b + " maintainer secret set", b + " setup codex"}; !reflect.DeepEqual(steps, want) {
			t.Fatalf("%s: plan %q, want %q", backend, steps, want)
		}
		// An installed binary that differs from this one: the install comes alone.
		os.WriteFile(Binary(e.o.Prefix), []byte("older binary"), 0755)
		if steps, want := plan(t, e), []string{b + " install"}; !reflect.DeepEqual(steps, want) {
			t.Fatalf("%s: outdated copy: %q, want %q", backend, steps, want)
		}
		// No daemon installed.
		os.Remove(e.o.Services.DaemonArtifact())
		if steps, want := plan(t, e), []string{b + " install"}; !reflect.DeepEqual(steps, want) {
			t.Fatalf("%s: not installed: %q, want %q", backend, steps, want)
		}
		// A Python-era installation upgrades first.
		os.MkdirAll(e.o.PythonPrefix, 0700)
		os.WriteFile(filepath.Join(e.o.PythonPrefix, "install.json"), []byte(`{"version":1}`), 0600)
		if steps, err := Plan(context.Background(), e.o); err != nil || !reflect.DeepEqual(steps, []string{b + " upgrade --from-python"}) {
			t.Fatalf("%s: Python-era: %q %v", backend, steps, err)
		}
	}
}

// From a Homebrew keg, the plan names the formula's opt path, and the installed binary is
// current only as the link to it.
func TestPlanHomebrew(t *testing.T) {
	e := planEnv(t, "systemd")
	brew := filepath.Join(t.TempDir(), "brew with space")
	e.o.Source = homebrewKeg(t, brew, "0.7.0", "synthetic binary")
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	if steps, want := plan(t, e), []string{"'" + opt + "' install"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("a copy with the same content: %q, want %q", steps, want)
	}
	os.Remove(Binary(e.o.Prefix))
	os.Symlink(opt, Binary(e.o.Prefix))
	if steps := plan(t, e); len(steps) != 0 {
		t.Fatalf("linked: %q", steps)
	}
}

// A macOS system daemon keeps a copy; the step is install --system, which asks sudo itself
// for each privileged step and refuses root, so it has no sudo.
func TestPlanSystemMode(t *testing.T) {
	e, _ := systemEnv(t)
	e.o.System = true
	if _, err := Install(context.Background(), e.o); err != nil {
		t.Fatal(err)
	}
	e.o.System = false
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"maintainer_secret_set":true}`), nil
	}
	e.o.Inspect = func(_ context.Context, o setup.Options) setup.Inspection { return setup.Inspection{Family: o.Family} }
	if steps := plan(t, e); len(steps) != 0 {
		t.Fatalf("current system daemon: %q", steps)
	}
	os.WriteFile(e.o.Source, []byte("newer binary"), 0700)
	if steps, want := plan(t, e), []string{e.o.Source + " install --system"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("older system copy: %q, want %q", steps, want)
	}
}

// A check that does not finish is an error, never a partial or current plan.
func TestPlanIncompleteChecks(t *testing.T) {
	e := planEnv(t, "systemd")
	calls := 0
	// The daemon restarts after brew upgrade: the first answers fail.
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		if calls++; calls < 3 {
			return nil, errors.New("daemon unavailable")
		}
		return json.RawMessage(`{"maintainer_secret_set":false}`), nil
	}
	if steps, want := plan(t, e), []string{e.o.Source + " maintainer secret set"}; !reflect.DeepEqual(steps, want) || calls != 3 {
		t.Fatalf("restart: %q after %d calls", steps, calls)
	}
	for name, c := range map[string]struct {
		status  func(context.Context, string, string) (json.RawMessage, error)
		inspect func(context.Context, setup.Options) setup.Inspection
		code    string
	}{
		"no answer": {status: func(context.Context, string, string) (json.RawMessage, error) {
			return nil, errors.New("daemon unavailable")
		}, code: "daemon_unavailable"},
		// A daemon of an earlier release does not report the field; it is not "unset".
		"older daemon": {status: func(context.Context, string, string) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}, code: "daemon_outdated"},
		"agent error": {inspect: func(_ context.Context, o setup.Options) setup.Inspection {
			return setup.Inspection{Family: o.Family, CLI: "/usr/bin/" + o.Family, Error: "exit status 2"}
		}, code: "agent_unreadable"},
		"timeout": {inspect: func(ctx context.Context, o setup.Options) setup.Inspection {
			<-ctx.Done()
			return setup.Inspection{Family: o.Family, CLI: "/usr/bin/" + o.Family}
		}, code: "plan_timeout"},
	} {
		e := planEnv(t, "systemd")
		e.o.PlanWait = time.Second
		if c.status != nil {
			e.o.Status = c.status
		}
		if c.inspect != nil {
			e.o.Inspect = c.inspect
		}
		if steps, err := Plan(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), c.code+":") || steps != nil {
			t.Fatalf("%s: %q %v", name, steps, err)
		}
	}
	// A partial install: the service file is there, the state is not.
	e = planEnv(t, "systemd")
	os.RemoveAll(e.o.StateDir)
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		t.Fatal("status asked without a secret")
		return nil, nil
	}
	if _, err := Plan(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), "daemon_unavailable:") {
		t.Fatalf("partial install: %v", err)
	}
}

// The plan changes nothing: no file, directory or mode in the state (with the store's log
// files), the prefix, the home or the link directory, and an absent state directory stays
// absent.
func TestPlanChangesNothing(t *testing.T) {
	snapshot := func(root string) map[string]string {
		files := map[string]string{}
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, _ := os.Lstat(path)
			data, _ := os.ReadFile(path)
			files[path] = info.Mode().String() + " " + info.ModTime().String() + " " + string(data)
			return nil
		})
		return files
	}
	e := planEnv(t, "systemd")
	for _, name := range []string{"state.sqlite3", "state.sqlite3-wal", "state.sqlite3-shm"} {
		os.WriteFile(filepath.Join(e.o.StateDir, name), []byte(name), 0600)
	}
	root := filepath.Dir(e.o.StateDir)
	before := snapshot(root)
	plan(t, e)
	if after := snapshot(root); !reflect.DeepEqual(before, after) {
		t.Fatalf("the plan changed files:\nbefore %v\nafter  %v", before, after)
	}
	e = newEnv(t, "systemd")
	os.RemoveAll(e.o.StateDir)
	root = filepath.Dir(e.o.StateDir)
	before = snapshot(root)
	if steps := plan(t, e); !reflect.DeepEqual(steps, []string{e.o.Source + " install"}) {
		t.Fatalf("not installed: %q", steps)
	}
	if after := snapshot(root); !reflect.DeepEqual(before, after) {
		t.Fatalf("the plan changed files:\nbefore %v\nafter  %v", before, after)
	}
}
