package upgrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rwcii/koinon/internal/importer"
	"github.com/rwcii/koinon/internal/install"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
)

type fixture struct {
	t          *testing.T
	prefix     string // Python prefix
	state      string // Python state root
	goState    string
	active     map[string]bool
	stops      []string
	starts     []string
	installs   []install.Options
	uninstalls int
	keys       map[string]string // a, b, c → store key
	paths      map[string]string // a, b, c → repository path
	codex      string            // the Codex session's inbox
	// activeFailures makes that many next unit observations fail.
	activeFailures int
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newFixture builds a Python installation around the committed fixture tree: an
// install.json naming the copied state root, the baseline's marker contract and
// uninstall.py, and one systemd session supervisor that is running.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	tree := filepath.Join(dir, "python")
	copyTree(t, "../importer/testdata/python-state", tree)
	f := &fixture{t: t, prefix: filepath.Join(tree, "prefix"), state: filepath.Join(tree, "state"), active: map[string]bool{},
		keys: map[string]string{}, paths: map[string]string{}}
	var m struct {
		Repositories map[string]struct{ Path, Key string } `json:"repositories"`
		Sessions     map[string]struct{ Directory string }  `json:"sessions"`
	}
	data, _ := os.ReadFile(filepath.Join(tree, "fixture.json"))
	json.Unmarshal(data, &m)
	for name, r := range m.Repositories {
		f.keys[name], f.paths[name] = r.Key, r.Path
	}
	home := filepath.Join(f.state, "sessions", m.Sessions["codex"].Directory)
	f.codex = filepath.Join(home, "inbox.sqlite3")
	var config map[string]any
	data, _ = os.ReadFile(filepath.Join(f.prefix, "install.json"))
	json.Unmarshal(data, &config)
	config["state_root"] = f.state
	data, _ = json.Marshal(config)
	os.WriteFile(filepath.Join(f.prefix, "install.json"), data, 0600)
	os.MkdirAll(filepath.Join(f.prefix, "koinon"), 0700)
	os.WriteFile(filepath.Join(f.prefix, "koinon", "runtime_names.py"), []byte("raise NameConflict('installation_upgrading', ())\n"), 0600)
	os.MkdirAll(filepath.Join(f.prefix, "scripts"), 0700)
	os.WriteFile(filepath.Join(f.prefix, "scripts", "uninstall.py"), []byte("# synthetic\n"), 0600)
	unit := "koinon-session-" + filepath.Base(home)[:16] + ".service"
	record, _ := json.Marshal(map[string]string{"backend": "systemd", "artifact": filepath.Join(home, "native-service", unit)})
	os.WriteFile(filepath.Join(home, "native-service.json"), record, 0600)
	f.active[unit] = true
	f.goState = filepath.Join(dir, "go")
	return f
}

func (f *fixture) services() platform.Services {
	return platform.Services{Backend: "systemd", Home: f.t.TempDir(), UID: 501, Run: func(_ context.Context, argv ...string) ([]byte, error) {
		if len(argv) < 4 || argv[0] != "systemctl" {
			return nil, nil
		}
		unit := argv[3]
		switch argv[2] {
		case "is-active":
			if f.activeFailures > 0 {
				f.activeFailures--
				return []byte("garbled"), errors.New("exit 4")
			}
			if f.active[unit] {
				return []byte("active"), nil
			}
			return []byte("inactive"), errors.New("exit 3")
		case "stop":
			f.stops = append(f.stops, unit)
			f.active[unit] = false
		case "start":
			f.starts = append(f.starts, unit)
			f.active[unit] = true
		}
		return nil, nil
	}}
}

func (f *fixture) options(overrides map[string]string) Options {
	return Options{PythonPrefix: f.prefix, GoPrefix: filepath.Join(f.t.TempDir(), "go-prefix"), StateDir: f.goState,
		Agents: []string{"codex"}, Repositories: overrides, Services: f.services(),
		Install: func(_ context.Context, o install.Options) (install.Report, error) {
			f.installs = append(f.installs, o)
			return install.Report{OK: true, Service: "running"}, nil
		},
		Uninstall: func(_ context.Context, _, prefix string) ([]byte, error) {
			// The baseline's uninstall.py continues only from the removing state.
			i, err := legacy.ReadInstall(prefix)
			if err != nil || i == nil || i.State() != "removing" {
				return []byte("not removing"), errors.New("exit 1")
			}
			f.uninstalls++
			return nil, os.Remove(filepath.Join(prefix, "install.json"))
		}}
}

func (f *fixture) good() map[string]string { return map[string]string{f.keys["b"]: f.paths["b"]} }

func (f *fixture) messages(t *testing.T) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.goState, "state.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT body FROM messages ORDER BY recipient_id,seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var b string
		rows.Scan(&b)
		bodies = append(bodies, b)
	}
	return bodies
}

func TestUpgradeCompletes(t *testing.T) {
	f := newFixture(t)
	result, err := Run(context.Background(), f.options(f.good()))
	if err != nil || result.Phase != phaseComplete || !result.OK {
		t.Fatalf("upgrade %+v %v", result, err)
	}
	if len(f.stops) != 1 || len(f.starts) != 0 || f.uninstalls != 1 || len(f.installs) != 1 || !f.installs[0].Upgrade {
		t.Fatalf("actions: stops %v starts %v uninstalls %d installs %+v", f.stops, f.starts, f.uninstalls, f.installs)
	}
	if result.Import == nil || len(result.Import.Sources) != 6 || len(f.messages(t)) != 5 {
		t.Fatalf("import %+v messages %v", result.Import, f.messages(t))
	}
	again, err := Run(context.Background(), f.options(f.good()))
	if err != nil || again.Phase != phaseComplete || len(f.installs) != 1 {
		t.Fatalf("rerun %+v %v", again, err)
	}
}

// TestFailureBeforeSwapRestoresPython: source A stages, then source B fails; the attempt
// ends, Python gets its configuration and services back, A receives a new message, and
// the next upgrade is a fresh attempt that carries the new message.
func TestFailureBeforeSwapRestoresPython(t *testing.T) {
	f := newFixture(t)
	original, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	// Store b resolves to store a's repository, so its import conflicts after a's.
	_, err := Run(context.Background(), f.options(map[string]string{f.keys["b"]: f.paths["a"]}))
	if err == nil || !strings.Contains(err.Error(), "source_conflict") {
		t.Fatalf("conflicting upgrade: %v", err)
	}
	restored, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	if string(restored) != string(original) || len(f.starts) != 1 || !f.active[f.stops[0]] {
		t.Fatalf("python not restored: %s starts %v", restored, f.starts)
	}
	if staged, _ := filepath.Glob(filepath.Join(f.goState, "state.sqlite3.import-*")); len(staged) != 0 {
		t.Fatalf("staging left: %v", staged)
	}
	j, _ := Status(f.goState)
	if j == nil || j.Phase != phaseEnded || !strings.Contains(j.Error, "source_conflict") {
		t.Fatalf("journal %+v", j)
	}
	db, err := sql.Open("sqlite", "file:"+f.codex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO inbox(received,pid,frame) VALUES (1.0,1,'{"type":"user","from":"uds:/tmp/x.sock","message":{"role":"user","content":"after the restore"}}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	result, err := Run(context.Background(), f.options(f.good()))
	if err != nil || result.Phase != phaseComplete || result.Attempt == j.Attempt {
		t.Fatalf("fresh attempt %+v %v", result, err)
	}
	found := false
	for _, b := range f.messages(t) {
		found = found || b == "after the restore"
	}
	if !found {
		t.Fatalf("the message received after the restore was lost: %v", f.messages(t))
	}
}

func TestCrashAtEveryActionResumes(t *testing.T) {
	probe := newFixture(t)
	points := []string{}
	o := probe.options(probe.good())
	o.Hook = func(point string) error { points = append(points, point); return nil }
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			f := newFixture(t)
			o := f.options(f.good())
			o.Hook = func(p string) error {
				if p == point {
					return ErrCrash
				}
				return nil
			}
			if _, err := Run(context.Background(), o); !errors.Is(err, ErrCrash) {
				t.Fatalf("no crash at %s: %v", point, err)
			}
			result, err := Run(context.Background(), f.options(f.good()))
			if err != nil || result.Phase != phaseComplete {
				t.Fatalf("resume after %s: %+v %v", point, result, err)
			}
			if len(f.starts) != 0 || f.uninstalls != 1 || len(f.installs) < 1 || len(f.messages(t)) != 5 {
				t.Fatalf("after %s: starts %v uninstalls %d installs %d messages %d", point, f.starts, f.uninstalls, len(f.installs), len(f.messages(t)))
			}
		})
	}
}

func TestResumeRefusesAChangedInstallation(t *testing.T) {
	f := newFixture(t)
	o := f.options(f.good())
	o.Hook = func(p string) error {
		if p == "after:marker" {
			return ErrCrash
		}
		return nil
	}
	if _, err := Run(context.Background(), o); !errors.Is(err, ErrCrash) {
		t.Fatal(err)
	}
	var config map[string]any
	data, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	json.Unmarshal(data, &config)
	config["unit_dir"] = "/elsewhere"
	data, _ = json.Marshal(config)
	os.WriteFile(filepath.Join(f.prefix, "install.json"), data, 0600)
	if _, err := Run(context.Background(), f.options(f.good())); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("changed installation: %v", err)
	}
	if len(f.stops) != 0 {
		t.Fatalf("a refused resume stopped services: %v", f.stops)
	}
}

func TestUpgradeRefusals(t *testing.T) {
	f := newFixture(t)
	os.Remove(filepath.Join(f.prefix, "koinon", "runtime_names.py"))
	if _, err := Run(context.Background(), f.options(f.good())); err == nil || !strings.Contains(err.Error(), "unsupported_baseline") {
		t.Fatalf("baseline: %v", err)
	}
	f = newFixture(t)
	root, _ := platform.PrivateDir(f.goState)
	lock, _ := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
	syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	if _, err := Run(context.Background(), f.options(f.good())); err == nil || !strings.Contains(err.Error(), "daemon_running") {
		t.Fatalf("daemon running: %v", err)
	}
	lock.Close()
	os.Remove(filepath.Join(f.prefix, "install.json"))
	if _, err := Run(context.Background(), f.options(f.good())); err == nil || !strings.Contains(err.Error(), "python_install_missing") {
		t.Fatalf("missing python: %v", err)
	}
}

func TestStandaloneImport(t *testing.T) {
	f := newFixture(t)
	original, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	o := ImportOptions{PythonPrefix: f.prefix, StateDir: f.goState, Repositories: f.good(), Services: f.services()}
	if _, err := Import(context.Background(), o); err == nil || !strings.Contains(err.Error(), "python_running") {
		t.Fatalf("running python: %v", err)
	}
	for unit := range f.active {
		f.active[unit] = false
	}
	r, err := Import(context.Background(), o)
	if err != nil || r.Result != "imported" || len(f.stops) != 0 {
		t.Fatalf("import %+v %v", r, err)
	}
	if after, _ := os.ReadFile(filepath.Join(f.prefix, "install.json")); string(after) != string(original) {
		t.Fatalf("install.json not restored: %s", after)
	}
	o.Verify = true
	if r, err := Import(context.Background(), o); err != nil || r.Result != "verified" {
		t.Fatalf("verify %+v %v", r, err)
	}
	o.Verify = false
	if r, err := Import(context.Background(), o); err != nil || r.Result != "unchanged" {
		t.Fatalf("repeat %+v %v", r, err)
	}
}

// TestMarkerRefusesAChangedConfiguration (review F1): install.json changes between the
// preflight and the marker; the marker is refused and nothing is stopped or imported.
func TestMarkerRefusesAChangedConfiguration(t *testing.T) {
	f := newFixture(t)
	o := f.options(f.good())
	o.Hook = func(p string) error {
		if p == "before:marker" {
			var config map[string]any
			data, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
			json.Unmarshal(data, &config)
			config["state_root"] = filepath.Join(t.TempDir(), "newer-root")
			data, _ = json.Marshal(config)
			os.WriteFile(filepath.Join(f.prefix, "install.json"), data, 0600)
		}
		return nil
	}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("changed configuration: %v", err)
	}
	if len(f.stops) != 0 || f.uninstalls != 0 {
		t.Fatalf("acted on a changed configuration: stops %v uninstalls %d", f.stops, f.uninstalls)
	}
	if i, _ := legacy.ReadInstall(f.prefix); i == nil || i.State() != "installed" {
		t.Fatalf("installation left excluded: %+v", i)
	}
}

// TestInventoryFrozenUnderTheMarker (review F1): a service record that appears after the
// preflight is found under the marker; the attempt ends and Python is restored.
func TestInventoryFrozenUnderTheMarker(t *testing.T) {
	f := newFixture(t)
	original, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	o := f.options(f.good())
	o.Hook = func(p string) error {
		if p == "after:marker" {
			home := filepath.Join(f.state, "sessions", strings.Repeat("e", 64))
			os.MkdirAll(home, 0700)
			record, _ := json.Marshal(map[string]string{"backend": "systemd", "artifact": filepath.Join(home, "native-service", "koinon-session-eeeeeeeeeeeeeeee.service")})
			os.WriteFile(filepath.Join(home, "native-service.json"), record, 0600)
		}
		return nil
	}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("changed inventory: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(f.prefix, "install.json")); string(after) != string(original) || len(f.stops) != 0 {
		t.Fatalf("not restored: %s stops %v", after, f.stops)
	}
}

// TestCrashedStopIsRestored (review F2): a crash after a stop, before its completion was
// saved, then a failed observation on resume; the restore starts the stopped unit.
func TestCrashedStopIsRestored(t *testing.T) {
	f := newFixture(t)
	o := f.options(f.good())
	unit := ""
	for name := range f.active {
		unit = name
	}
	o.Hook = func(p string) error {
		if p == "after:stop:"+unit {
			return ErrCrash
		}
		return nil
	}
	if _, err := Run(context.Background(), o); !errors.Is(err, ErrCrash) {
		t.Fatal(err)
	}
	f.activeFailures = 1
	if _, err := Run(context.Background(), f.options(f.good())); err == nil {
		t.Fatal("a failed observation did not fail the attempt")
	}
	if !f.active[unit] || len(f.starts) != 1 {
		t.Fatalf("the stopped unit was not restarted: active %v starts %v", f.active, f.starts)
	}
}

// TestFailureAfterTheSwapResumes (review F3): an error after the rename keeps the import
// and the exclusion; Python is not restored, and the next run completes.
func TestFailureAfterTheSwapResumes(t *testing.T) {
	f := newFixture(t)
	o := f.options(f.good())
	o.Hook = func(p string) error {
		if p == "after:swap" {
			return errors.New("injected completion failure")
		}
		return nil
	}
	result, err := Run(context.Background(), o)
	if err == nil || result.Phase != phaseSwapped || result.Resume == "" || len(f.starts) != 0 {
		t.Fatalf("failure after the swap: %+v %v starts %v", result, err, f.starts)
	}
	if result, err := Run(context.Background(), f.options(f.good())); err != nil || result.Phase != phaseComplete || len(f.starts) != 0 {
		t.Fatalf("resume %+v %v", result, err)
	}
}

// TestRenameWithoutJournalResumes (review F3): the rename happened but the journal still
// says the swap is pending; the target decides, and Python is not restored.
func TestRenameWithoutJournalResumes(t *testing.T) {
	f := newFixture(t)
	o := f.options(f.good())
	o.Hook = func(p string) error {
		if p == "before:swap" {
			return ErrCrash
		}
		return nil
	}
	result, err := Run(context.Background(), o)
	if !errors.Is(err, ErrCrash) {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(f.goState)
	if err := importer.Swap(importer.Paths{Root: root, Attempt: result.Attempt}); err != nil {
		t.Fatal(err)
	}
	if result, err := Run(context.Background(), f.options(f.good())); err != nil || result.Phase != phaseComplete || len(f.starts) != 0 {
		t.Fatalf("resume after an unjournaled rename: %+v %v starts %v", result, err, f.starts)
	}
	if len(f.messages(t)) != 5 {
		t.Fatalf("messages %v", f.messages(t))
	}
}

// TestInterruptedImportRecovers (review F4): a standalone import killed after its marker
// leaves a record; the next run restores install.json and imports.
func TestInterruptedImportRecovers(t *testing.T) {
	f := newFixture(t)
	for unit := range f.active {
		f.active[unit] = false
	}
	original, _ := os.ReadFile(filepath.Join(f.prefix, "install.json"))
	o := ImportOptions{PythonPrefix: f.prefix, StateDir: f.goState, Repositories: f.good(), Services: f.services(),
		Hook: func(p string) error { return ErrCrash }}
	if _, err := Import(context.Background(), o); !errors.Is(err, ErrCrash) {
		t.Fatal(err)
	}
	if i, _ := legacy.ReadInstall(f.prefix); i == nil || i.State() != "upgrading" {
		t.Fatalf("the interruption left no marker: %+v", i)
	}
	o.Hook = nil
	r, err := Import(context.Background(), o)
	if err != nil || r.Result != "imported" {
		t.Fatalf("recovered import %+v %v", r, err)
	}
	if after, _ := os.ReadFile(filepath.Join(f.prefix, "install.json")); string(after) != string(original) {
		t.Fatalf("install.json not restored: %s", after)
	}
	if _, err := os.Stat(filepath.Join(f.goState, "import", "journal.json")); !os.IsNotExist(err) {
		t.Fatalf("import record left: %v", err)
	}
}
