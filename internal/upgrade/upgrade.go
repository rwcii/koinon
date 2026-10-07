// Package upgrade moves a Python-era installation to the Go runtime (sprint chunk 11,
// docs/INSTALL.md "Upgrade from the Python runtime"). One attempt at a time is recorded
// in a journal under the Go state. Every external action records its intent first, and a
// resume observes the action again instead of trusting the journal. A failure before the
// import is swapped into place ends the attempt and restores the Python runtime; a
// failure after it keeps the verified import and is resumed.
package upgrade

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/importer"
	"github.com/rwcii/koinon/internal/install"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
)

// Phases, in order. ended and complete are final.
const (
	phasePreflight = "preflight"
	phaseExcluded  = "excluded"
	phaseStopped   = "stopped"
	phaseSwapped   = "swapped"
	phaseRemoved   = "python_removed"
	phaseComplete  = "complete"
	phaseEnded     = "ended"
)

// Options select the upgrade. Zero values take the documented defaults.
type Options struct {
	PythonPrefix string
	GoPrefix     string
	StateDir     string
	Agents       []string
	Repositories map[string]string
	Python       string // interpreter that runs the installed uninstall.py
	Services     platform.Services
	Install      func(context.Context, install.Options) (install.Report, error)
	Uninstall    func(ctx context.Context, python, prefix string) ([]byte, error)
	// Hook is called before and after every external action, as "before:NAME" and
	// "after:NAME"; tests return ErrCrash to stop as a killed process would.
	Hook func(point string) error
}

// ErrCrash stops a run without any recovery, as a killed process stops.
var ErrCrash = errors.New("simulated crash")

// Journal is one attempt's durable record.
type Journal struct {
	Version       int               `json:"version"`
	Attempt       string            `json:"attempt"`
	Operation     string            `json:"operation"`
	Plan          string            `json:"plan"`
	PythonPrefix  string            `json:"python_prefix"`
	StateRoot     string            `json:"state_root"`
	GoPrefix      string            `json:"go_prefix"`
	Agents        []string          `json:"agents"`
	Repositories  map[string]string `json:"repositories"`
	InstallDigest string            `json:"install_digest"`
	Raw           []byte            `json:"install_raw"`
	Units         []platform.Unit   `json:"units"`
	Stopped       []string          `json:"stopped"`
	Phase         string            `json:"phase"`
	Intent        string            `json:"intent"`
	Error         string            `json:"error,omitempty"`
	Report        *importer.Report  `json:"import,omitempty"`
}

// Result is the JSON report of an upgrade run.
type Result struct {
	OK      bool             `json:"ok"`
	Phase   string           `json:"phase"`
	Attempt string           `json:"attempt"`
	Import  *importer.Report `json:"import,omitempty"`
	Install *install.Report  `json:"install,omitempty"`
	Resume  string           `json:"resume,omitempty"`
}

func (o *Options) defaults() error {
	var err error
	if o.PythonPrefix == "" {
		if o.PythonPrefix, err = legacy.DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.GoPrefix == "" {
		if o.GoPrefix, err = install.DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.StateDir == "" {
		if o.StateDir, err = platform.DefaultStateDir(); err != nil {
			return err
		}
	}
	if o.Services.Run == nil {
		if o.Services, err = platform.NewServices(); err != nil {
			return err
		}
	}
	if o.Install == nil {
		o.Install = install.Install
	}
	if o.Uninstall == nil {
		o.Uninstall = runUninstall
	}
	if o.Hook == nil {
		o.Hook = func(string) error { return nil }
	}
	if o.Python == "" {
		if o.Python, err = exec.LookPath("python3"); err != nil {
			o.Python = "python3"
		}
	}
	for _, path := range []string{o.PythonPrefix, o.GoPrefix, o.StateDir} {
		if !filepath.IsAbs(path) {
			return errors.New("upgrade paths must be absolute")
		}
	}
	return nil
}

// runUninstall runs the Python installation's own uninstall.py, which removes its
// services and managed guidance and keeps its state tree.
func runUninstall(ctx context.Context, python, prefix string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, python, filepath.Join(prefix, "scripts", "uninstall.py"), "--prefix", prefix)
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PYTHON") {
			env = append(env, entry)
		}
	}
	cmd.Env = env
	return cmd.CombinedOutput()
}

type run struct {
	o    Options
	dir  string
	j    *Journal
	lock *os.File
}

func (r *run) path() string { return filepath.Join(r.dir, "journal.json") }

func (r *run) save() error {
	data, err := json.MarshalIndent(r.j, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteAtomic(r.path(), append(data, '\n'), 0600)
}

// act records an intent, calls the hooks around the action and clears the intent.
func (r *run) act(name string, action func() error) error {
	r.j.Intent = name
	if err := r.save(); err != nil {
		return err
	}
	if err := r.o.Hook("before:" + name); err != nil {
		return err
	}
	if err := action(); err != nil {
		return err
	}
	if err := r.o.Hook("after:" + name); err != nil {
		return err
	}
	r.j.Intent = ""
	return r.save()
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func digest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// baseline checks that the installed Python runtime honours the upgrade marker and has
// its own uninstall.py: the contract of the main release this upgrade supports.
func baseline(prefix string) error {
	names, err := os.ReadFile(filepath.Join(prefix, "koinon", "runtime_names.py"))
	if err != nil || !bytes.Contains(names, []byte("installation_upgrading")) {
		return errors.New("unsupported_baseline: the installed Python runtime does not honour the upgrade marker; upgrade it to the main release first")
	}
	if _, err := os.Stat(filepath.Join(prefix, "scripts", "uninstall.py")); err != nil {
		return errors.New("unsupported_baseline: the installed Python runtime has no scripts/uninstall.py")
	}
	return nil
}

func (r *run) paths() importer.Paths {
	return importer.Paths{Root: r.o.StateDir, Attempt: r.j.Attempt}
}

// targetEmpty reports whether the Go state database is absent or empty.
func targetEmpty(ctx context.Context, root string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(root, "state.sqlite3")); errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	s, err := core.OpenImportStore(root, "state.sqlite3")
	if err != nil {
		return false, err
	}
	empty, err := s.Empty(ctx)
	return empty, errors.Join(err, s.Close())
}

// Run performs or resumes the upgrade.
func Run(ctx context.Context, o Options) (Result, error) {
	if err := o.defaults(); err != nil {
		return Result{}, err
	}
	root, err := platform.PrivateDir(o.StateDir)
	if err != nil {
		return Result{}, err
	}
	o.StateDir = root
	r := &run{o: o, dir: filepath.Join(root, "upgrade")}
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return Result{}, err
	}
	if r.lock, err = platform.Lock(filepath.Join(r.dir, "upgrade.lock")); err != nil {
		return Result{}, errors.New("upgrade_busy: another upgrade runs")
	}
	defer platform.Unlock(r.lock)
	data, err := os.ReadFile(r.path())
	switch {
	case err == nil:
		r.j = &Journal{}
		if err := json.Unmarshal(data, r.j); err != nil {
			return Result{}, fmt.Errorf("upgrade_journal_invalid: %w", err)
		}
		if r.j.Phase == phaseEnded {
			r.j = nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return Result{}, err
	}
	if r.j != nil && r.j.Phase == phaseComplete {
		return Result{OK: true, Phase: phaseComplete, Attempt: r.j.Attempt, Import: r.j.Report}, nil
	}
	if r.j == nil {
		if err := r.begin(ctx); err != nil {
			return Result{Phase: phasePreflight}, err
		}
	} else if err := r.recheck(ctx); err != nil {
		return r.result(), err
	}
	result, err := r.advance(ctx)
	if err == nil || errors.Is(err, ErrCrash) {
		return result, err
	}
	if r.j.Phase == phaseStopped {
		// The swap is decided by the files, not by the phase: a failure after the rename,
		// before its phase was saved, keeps the import and resumes.
		if empty, terr := targetEmpty(ctx, r.o.StateDir); terr != nil || !empty {
			r.j.Phase, r.j.Intent = phaseSwapped, ""
			r.save()
			result = r.result()
			result.Resume = "koinon upgrade --from-python"
			return result, fmt.Errorf("%w; the import is in place and kept: rerun koinon upgrade --from-python to resume", err)
		}
	}
	if r.j.Phase == phasePreflight || r.j.Phase == phaseExcluded || r.j.Phase == phaseStopped {
		// Before the swap: end the attempt and give the Python runtime back.
		if rerr := r.end(ctx, err); rerr != nil {
			return r.result(), fmt.Errorf("%w; restoring the Python runtime also failed: %v", err, rerr)
		}
		return r.result(), err
	}
	result.Resume = "koinon upgrade --from-python"
	return result, fmt.Errorf("%w; the import is verified and kept: rerun koinon upgrade --from-python to resume", err)
}

func (r *run) result() Result {
	res := Result{Phase: r.j.Phase, Attempt: r.j.Attempt, Import: r.j.Report}
	res.OK = r.j.Phase == phaseComplete
	return res
}

// begin runs the preflight and records a new attempt.
func (r *run) begin(ctx context.Context) error {
	o := r.o
	i, err := legacy.ReadInstall(o.PythonPrefix)
	if err != nil {
		return err
	}
	if i == nil {
		return fmt.Errorf("python_install_missing: no Python-era installation at %s", o.PythonPrefix)
	}
	if i.State() != "installed" {
		return fmt.Errorf("python_install_busy: the Python installation is %s; finish that operation first", i.State())
	}
	if err := baseline(o.PythonPrefix); err != nil {
		return err
	}
	lock, err := platform.Lock(filepath.Join(o.StateDir, "daemon.lock"))
	if err != nil {
		return errors.New("daemon_running: a Go daemon runs on this state directory; stop it first")
	}
	empty, err := targetEmpty(ctx, o.StateDir)
	platform.Unlock(lock)
	if err != nil {
		return err
	}
	if !empty {
		return errors.New("target_not_empty: the Go state database already holds records")
	}
	stateRoot, err := i.StateRoot()
	if err != nil {
		return err
	}
	units, err := legacy.Units(ctx, i, o.Services)
	if err != nil {
		return err
	}
	installDigest, err := i.Digest()
	if err != nil {
		return err
	}
	attempt := randomID()
	r.j = &Journal{Version: 1, Attempt: attempt, Operation: filepath.Join(r.dir, attempt), PythonPrefix: o.PythonPrefix,
		StateRoot: stateRoot, GoPrefix: o.GoPrefix, Agents: o.Agents, Repositories: o.Repositories,
		InstallDigest: installDigest, Raw: i.Raw, Units: units, Stopped: []string{}, Phase: phasePreflight}
	r.j.Plan = digest(map[string]any{"python_prefix": o.PythonPrefix, "state_root": stateRoot, "go_prefix": o.GoPrefix,
		"agents": o.Agents, "install": r.j.InstallDigest, "units": units})
	if err := os.MkdirAll(r.j.Operation, 0700); err != nil {
		return err
	}
	return r.save()
}

// recheck refuses a resume when the Python installation or its services changed.
func (r *run) recheck(ctx context.Context) error {
	if r.j.Phase == phaseSwapped || r.j.Phase == phaseRemoved {
		return nil // the Python installation is being removed on purpose
	}
	i, err := legacy.ReadInstall(r.j.PythonPrefix)
	if err != nil || i == nil {
		return fmt.Errorf("source_changed: the Python installation cannot be read again: %v", err)
	}
	return r.sameSource(ctx, i)
}

// sameSource requires the configuration, state root and service inventory that this
// attempt inspected.
func (r *run) sameSource(ctx context.Context, i *legacy.Install) error {
	installDigest, err := i.Digest()
	if err != nil {
		return err
	}
	units, err := legacy.Units(ctx, i, r.o.Services)
	if err != nil {
		return err
	}
	root, err := i.StateRoot()
	if err != nil {
		return err
	}
	if installDigest != r.j.InstallDigest || root != r.j.StateRoot || digest(units) != digest(r.j.Units) {
		return errors.New("source_changed: the Python installation or its services changed since this attempt began")
	}
	return nil
}

func (r *run) advance(ctx context.Context) (Result, error) {
	o, j := r.o, r.j
	for {
		switch j.Phase {
		case phasePreflight:
			if err := r.act("marker", func() error {
				return legacy.PublishMarker(j.PythonPrefix, j.Operation, j.Plan, j.InstallDigest)
			}); err != nil {
				return r.result(), err
			}
			j.Phase = phaseExcluded
		case phaseExcluded:
			// Under the marker nothing can change the selection; the inventory is frozen
			// here and must equal the one the attempt inspected.
			i, err := legacy.ReadInstall(j.PythonPrefix)
			if err != nil || i == nil || i.Operation() != j.Operation {
				return r.result(), fmt.Errorf("source_changed: the Python installation lost this attempt's marker: %v", err)
			}
			if err := r.sameSource(ctx, i); err != nil {
				return r.result(), err
			}
			for _, u := range j.Units {
				active, err := o.Services.Active(ctx, u)
				if err != nil {
					return r.result(), err
				}
				if !active {
					continue
				}
				// The unit is this attempt's to restart from the moment its stop may begin.
				owned := false
				for _, name := range j.Stopped {
					owned = owned || name == u.Name
				}
				if !owned {
					j.Stopped = append(j.Stopped, u.Name)
				}
				if err := r.act("stop:"+u.Name, func() error { return o.Services.Stop(ctx, u) }); err != nil {
					return r.result(), err
				}
			}
			for _, u := range j.Units {
				if active, err := o.Services.Active(ctx, u); err != nil || active {
					return r.result(), fmt.Errorf("python_running: %s is still running after its stop", u.Name)
				}
			}
			j.Phase = phaseStopped
		case phaseStopped:
			report, err := r.importState(ctx)
			if err != nil {
				return r.result(), err
			}
			j.Report = report
			j.Phase = phaseSwapped
		case phaseSwapped:
			if err := r.act("removing", func() error {
				// After a completed uninstall, install.json is gone and nothing remains to mark.
				if i, err := legacy.ReadInstall(j.PythonPrefix); err != nil || i == nil {
					return err
				}
				return legacy.ReplaceWithRemoving(j.PythonPrefix, j.Operation)
			}); err != nil {
				return r.result(), err
			}
			if err := r.act("uninstall", func() error {
				i, err := legacy.ReadInstall(j.PythonPrefix)
				if err != nil {
					return err
				}
				if i == nil {
					return nil // a previous run's uninstall completed
				}
				if out, err := o.Uninstall(ctx, o.Python, j.PythonPrefix); err != nil {
					return fmt.Errorf("python_uninstall_failed: %v: %s", err, strings.TrimSpace(string(out)))
				}
				return nil
			}); err != nil {
				return r.result(), err
			}
			j.Phase = phaseRemoved
		case phaseRemoved:
			var report install.Report
			if err := r.act("install", func() error {
				var err error
				report, err = o.Install(ctx, install.Options{Prefix: j.GoPrefix, StateDir: o.StateDir, PythonPrefix: j.PythonPrefix,
					Agents: j.Agents, Upgrade: true, Services: o.Services})
				return err
			}); err != nil {
				return r.result(), err
			}
			j.Phase = phaseComplete
			if err := r.save(); err != nil {
				return r.result(), err
			}
			res := r.result()
			res.Install = &report
			return res, nil
		default:
			return r.result(), fmt.Errorf("upgrade_journal_invalid: unknown phase %q", j.Phase)
		}
		if err := r.save(); err != nil {
			return r.result(), err
		}
	}
}

// importState captures, stages, verifies and swaps the import while holding the Go
// daemon lock. The swap is the attempt's point of no return.
func (r *run) importState(ctx context.Context) (*importer.Report, error) {
	o, j := r.o, r.j
	lock, err := platform.Lock(filepath.Join(o.StateDir, "daemon.lock"))
	if err != nil {
		return nil, errors.New("daemon_running: a Go daemon runs on this state directory; stop it first")
	}
	defer platform.Unlock(lock)
	p := r.paths()
	// The preflight found the target empty and this attempt holds the daemon lock, so a
	// non-empty target is this attempt's swapped import, whatever the journal says.
	empty, err := targetEmpty(ctx, o.StateDir)
	if err != nil {
		return nil, err
	}
	if !empty {
		j.Intent = ""
		return j.Report, nil
	}
	i, err := legacy.ReadInstall(j.PythonPrefix)
	if err != nil {
		return nil, err
	}
	prepared, err := importer.CaptureAll(ctx, j.StateRoot, i, j.Repositories, p)
	if err != nil {
		return nil, err
	}
	report, err := importer.Stage(ctx, prepared, p)
	if err != nil {
		return &report, err
	}
	if report.Result != "staged" {
		return &report, fmt.Errorf("target_not_empty: the target changed during the attempt (%s)", report.Result)
	}
	report.Result = "imported"
	j.Report = &report
	j.Intent = "swap"
	if err := r.save(); err != nil {
		return &report, err
	}
	if err := o.Hook("before:swap"); err != nil {
		return &report, err
	}
	if err := importer.Swap(p); err != nil {
		return &report, err
	}
	// The outcome is saved before anything else can fail.
	j.Phase, j.Intent = phaseSwapped, ""
	if err := r.save(); err != nil {
		return &report, err
	}
	return &report, o.Hook("after:swap")
}

// end ends an attempt that failed before its swap: the staging files go, install.json
// gets its original bytes back, and the services this attempt stopped start again.
func (r *run) end(ctx context.Context, cause error) error {
	j := r.j
	var errs []error
	errs = append(errs, r.paths().Discard())
	if i, err := legacy.ReadInstall(j.PythonPrefix); err == nil && i != nil && i.Operation() == j.Operation {
		errs = append(errs, legacy.RestoreRaw(j.PythonPrefix, j.Operation, j.Raw))
	}
	for _, name := range j.Stopped {
		for _, u := range j.Units {
			if u.Name == name {
				errs = append(errs, r.o.Services.Start(ctx, u))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		j.Error = cause.Error()
		r.save()
		return err
	}
	j.Phase, j.Intent, j.Error = phaseEnded, "", cause.Error()
	os.RemoveAll(j.Operation)
	return r.save()
}

// Status reads the current journal without changing anything.
func Status(stateDir string) (*Journal, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "upgrade", "journal.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j := &Journal{}
	if err := json.Unmarshal(data, j); err != nil {
		return nil, err
	}
	j.Raw = nil
	return j, nil
}
