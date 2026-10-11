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
	adopts   []string
	// adopted lists the families whose fake earlier setup Adopt finds.
	adopted map[string]bool
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
		Path:     filepath.Join(dir, "empty-path"),
		Services: platform.Services{Backend: backend, Home: filepath.Join(dir, "home"), UID: 501, Run: e.r.run},
		Setup: func(_ context.Context, o setup.Options) (setup.Report, error) {
			e.setups = append(e.setups, o.Family+" "+o.Binary)
			return setup.Report{OK: true, Family: o.Family}, nil
		},
		Adopt: func(_ context.Context, o setup.Options) (setup.Report, bool, error) {
			e.adopts = append(e.adopts, o.Family+" "+o.Binary+" "+o.StateDir)
			return setup.Report{OK: true, Family: o.Family, Changed: []string{o.Family + " CLI in launchers.json"}}, e.adopted[o.Family], nil
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

func TestInstallPathLink(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	home := e.o.Services.Home
	linkDir := filepath.Join(home, ".local", "bin")
	link := filepath.Join(linkDir, "koinon")

	// The link directory is not on PATH: the link is made and the report names the step.
	r, err := Install(ctx, e.o)
	if err != nil || r.Link != link || r.LinkResult != "created" || r.OnPath != "" ||
		r.PathStep != `add `+linkDir+` to PATH in your shell's startup file: export PATH="`+linkDir+`:$PATH"` {
		t.Fatalf("not on PATH %+v %v", r, err)
	}
	if target, err := os.Readlink(link); err != nil || target != r.Binary {
		t.Fatalf("link %q %v", target, err)
	}

	// On PATH, koinon runs the installed binary and no step is needed.
	e.o.Path = "relative:" + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "unchanged" || r.OnPath != link || r.PathStep != "" {
		t.Fatalf("on PATH %+v %v", r, err)
	}

	// Another koinon earlier on PATH, such as the Homebrew copy, is named.
	brew := filepath.Join(home, "brew", "bin")
	os.MkdirAll(brew, 0755)
	os.WriteFile(filepath.Join(brew, "koinon"), []byte("other"), 0755)
	e.o.Path = brew + string(filepath.ListSeparator) + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(brew, "koinon") ||
		r.PathStep != "PATH runs "+r.OnPath+" first; put "+linkDir+" before its directory in PATH, or run "+r.Binary {
		t.Fatalf("other on PATH %+v %v", r, err)
	}

	// Uninstall removes the link it made; a repeat finds nothing.
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "removed" {
		t.Fatalf("uninstall %+v %v", u, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link left: %v", err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "absent" {
		t.Fatalf("repeat uninstall %+v %v", u, err)
	}

	// A link the install did not make is used but never removed.
	os.Symlink(Binary(e.o.Prefix), link)
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "unchanged" {
		t.Fatalf("existing link %+v %v", r, err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("existing link uninstall %+v %v", u, err)
	}
	os.Remove(link)

	// A made link that now points elsewhere is kept.
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "created" {
		t.Fatalf("relink %+v %v", r, err)
	}
	os.Remove(link)
	os.Symlink(filepath.Join(brew, "koinon"), link)
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("retargeted uninstall %+v %v", u, err)
	}
	if target, _ := os.Readlink(link); target != filepath.Join(brew, "koinon") {
		t.Fatalf("retargeted link changed: %q", target)
	}
	os.Remove(link)

	// Another file at the link's path is never replaced.
	os.WriteFile(link, []byte("mine"), 0755)
	e.o.Path = linkDir
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "occupied" || r.OnPath != link ||
		r.PathStep != link+" is not a link to the installed binary; replace it with ln -sf "+r.Binary+" "+link+", or run "+r.Binary {
		t.Fatalf("occupied %+v %v", r, err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("occupied uninstall %+v %v", u, err)
	}
	if data, _ := os.ReadFile(link); string(data) != "mine" {
		t.Fatalf("occupied file changed: %q", data)
	}
	if _, err := os.Stat(e.o.Prefix); !os.IsNotExist(err) {
		t.Fatalf("prefix left: %v", err)
	}
}

func TestInstallLinkMarkerRefusesUnrelatedFile(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t, "systemd")
			retained := filepath.Join(t.TempDir(), "retained")
			os.WriteFile(retained, []byte("unrelated"), 0600)
			marker := filepath.Join(e.o.Prefix, "link")
			os.MkdirAll(e.o.Prefix, 0755)
			if kind == "symlink" {
				os.Symlink(retained, marker)
			} else {
				os.WriteFile(marker, []byte("unrelated"), 0600)
			}
			// Install and uninstall refuse the marker and change neither file.
			if _, err := Install(ctx, e.o); err == nil || !strings.Contains(err.Error(), "link_marker_invalid") {
				t.Fatalf("install: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(e.o.Services.Home, ".local", "bin", "koinon")); !os.IsNotExist(err) {
				t.Fatalf("link made: %v", err)
			}
			if _, err := Uninstall(ctx, e.o); err == nil || !strings.Contains(err.Error(), "link_marker_invalid") {
				t.Fatalf("uninstall: %v", err)
			}
			if data, _ := os.ReadFile(retained); string(data) != "unrelated" {
				t.Fatalf("retained file changed: %q", data)
			}
			if kind == "file" {
				if data, _ := os.ReadFile(marker); string(data) != "unrelated" {
					t.Fatalf("marker file changed: %q", data)
				}
			}
		})
	}
}

func TestInstallReportsRelativePathEntries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	linkDir := filepath.Join(e.o.Services.Home, ".local", "bin")
	wd := t.TempDir()
	t.Chdir(wd)
	os.MkdirAll(filepath.Join(wd, "rel"), 0755)
	os.WriteFile(filepath.Join(wd, "rel", "koinon"), []byte("relative"), 0755)
	os.WriteFile(filepath.Join(wd, "koinon"), []byte("current"), 0755)
	sep := string(filepath.ListSeparator)
	// A relative entry before the link directory runs first, as in a shell.
	e.o.Path = "rel" + sep + linkDir
	r, err := Install(ctx, e.o)
	if err != nil || r.OnPath != filepath.Join(wd, "rel", "koinon") ||
		r.PathStep != "PATH runs "+r.OnPath+" first; put "+linkDir+" before its directory in PATH, or run "+r.Binary {
		t.Fatalf("relative entry %+v %v", r, err)
	}
	// An empty entry is the current directory.
	e.o.Path = sep + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(wd, "koinon") {
		t.Fatalf("empty entry %+v %v", r, err)
	}
	// After the link directory, neither matters.
	e.o.Path = linkDir + sep + "rel" + sep
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(linkDir, "koinon") || r.PathStep != "" {
		t.Fatalf("link first %+v %v", r, err)
	}
}

// An install records the launcher CLI of each other family that an earlier setup configured,
// and reports only those; the named families are set up as before (#273).
func TestInstallAdoptsEarlierSetup(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"codex", "claude"}
	e.adopted = map[string]bool{"agy": true}
	r, err := Install(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.adopts, []string{"agy " + r.Binary + " " + e.o.StateDir, "opencode " + r.Binary + " " + e.o.StateDir}) {
		t.Fatalf("adopt calls %v", e.adopts)
	}
	if len(r.Agents) != 3 || r.Agents[2].Family != "agy" {
		t.Fatalf("agents %+v", r.Agents)
	}
	e = newEnv(t, "launchd")
	e.o.Adopt = func(context.Context, setup.Options) (setup.Report, bool, error) {
		return setup.Report{}, false, errors.New("unsafe launcher configuration")
	}
	if _, err := Install(context.Background(), e.o); err == nil || err.Error() != "record claude launcher: unsafe launcher configuration" {
		t.Fatalf("adopt failure: %v", err)
	}
}
