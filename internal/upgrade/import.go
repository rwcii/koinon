package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rwcii/koinon/internal/importer"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
)

// ImportOptions select a standalone import or verification.
type ImportOptions struct {
	From         string // Python state root; default the installation's, else the default root
	PythonPrefix string
	StateDir     string
	Repositories map[string]string
	Verify       bool
	Services     platform.Services
}

// Import runs `koinon import` on its own. It stops nothing: while a Python installation
// exists it holds the upgrade marker, refuses with python_running while any Python
// service runs, and gives install.json its original bytes back afterwards.
func Import(ctx context.Context, o ImportOptions) (importer.Report, error) {
	var err error
	if o.PythonPrefix == "" {
		if o.PythonPrefix, err = legacy.DefaultPrefix(); err != nil {
			return importer.Report{}, err
		}
	}
	if o.StateDir == "" {
		if o.StateDir, err = platform.DefaultStateDir(); err != nil {
			return importer.Report{}, err
		}
	}
	if o.Services.Run == nil {
		if o.Services, err = platform.NewServices(); err != nil {
			return importer.Report{}, err
		}
	}
	root, err := platform.PrivateDir(o.StateDir)
	if err != nil {
		return importer.Report{}, err
	}
	i, err := legacy.ReadInstall(o.PythonPrefix)
	if err != nil {
		return importer.Report{}, err
	}
	if o.From == "" {
		if i != nil {
			o.From, err = i.StateRoot()
		} else {
			o.From, err = legacy.DefaultStateRoot()
		}
		if err != nil {
			return importer.Report{}, err
		}
	}
	lock, err := platform.Lock(filepath.Join(root, "daemon.lock"))
	if err != nil {
		return importer.Report{}, errors.New("daemon_running: a Go daemon runs on this state directory; stop it first")
	}
	defer platform.Unlock(lock)
	p := importer.Paths{Root: root, Attempt: randomID()}
	defer os.RemoveAll(p.Captures())
	if i != nil {
		if i.State() != "installed" {
			return importer.Report{}, fmt.Errorf("python_install_busy: the Python installation is %s", i.State())
		}
		units, err := legacy.Units(ctx, i, o.Services)
		if err != nil {
			return importer.Report{}, err
		}
		for _, u := range units {
			if active, err := o.Services.Active(ctx, u); err != nil || active {
				return importer.Report{}, fmt.Errorf("python_running: %s runs; stop it, or use koinon upgrade --from-python", u.Name)
			}
		}
		operation := filepath.Join(root, "import-"+p.Attempt)
		if err := legacy.PublishMarker(o.PythonPrefix, operation, digest(map[string]string{"import": p.Attempt, "from": o.From})); err != nil {
			return importer.Report{}, err
		}
		defer legacy.RestoreRaw(o.PythonPrefix, operation, i.Raw)
	}
	prepared, err := importer.CaptureAll(ctx, o.From, i, o.Repositories, p)
	if err != nil {
		return importer.Report{}, err
	}
	if o.Verify {
		return importer.Verify(ctx, prepared, p)
	}
	report, err := importer.Stage(ctx, prepared, p)
	if err != nil || report.Result != "staged" {
		p.Discard()
		return report, err
	}
	if err := importer.Swap(p); err != nil {
		return report, err
	}
	report.Result = "imported"
	return report, nil
}
