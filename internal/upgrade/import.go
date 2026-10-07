package upgrade

import (
	"context"
	"encoding/json"
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
	// Hook is called after the marker is published ("after:marker"); tests stop there.
	Hook func(point string) error
}

// importAttempt is a standalone import's durable record. It is written before the
// marker and removed after the original configuration is back, so a later run can give
// back a marker that an interrupted import left behind.
type importAttempt struct {
	Attempt   string `json:"attempt"`
	Operation string `json:"operation"`
	Prefix    string `json:"python_prefix"`
	Raw       []byte `json:"install_raw"`
}

// recoverImport restores what an interrupted standalone import left: its marker, its
// staging database and its captures. An import that was already swapped into place stays.
func recoverImport(root string) error {
	path := filepath.Join(root, "import", "journal.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var a importAttempt
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("import_journal_invalid: %w", err)
	}
	if i, err := legacy.ReadInstall(a.Prefix); err != nil {
		return err
	} else if i != nil && i.Operation() == a.Operation {
		if err := legacy.RestoreRaw(a.Prefix, a.Operation, a.Raw); err != nil {
			return fmt.Errorf("import_recovery_failed: cannot restore install.json after an interrupted import: %w", err)
		}
	}
	if err := (importer.Paths{Root: root, Attempt: a.Attempt}).Discard(); err != nil {
		return err
	}
	return os.Remove(path)
}

// Import runs `koinon import` on its own. It stops nothing: while a Python installation
// exists it holds the upgrade marker, refuses with python_running while any Python
// service runs, and gives install.json its original bytes back afterwards.
func Import(ctx context.Context, o ImportOptions) (report importer.Report, err error) {
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
	if o.Hook == nil {
		o.Hook = func(string) error { return nil }
	}
	if err := recoverImport(root); err != nil {
		return importer.Report{}, err
	}
	if i, err = legacy.ReadInstall(o.PythonPrefix); err != nil {
		return importer.Report{}, err
	}
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
		installDigest, err := i.Digest()
		if err != nil {
			return importer.Report{}, err
		}
		operation := filepath.Join(root, "import", p.Attempt)
		record, _ := json.Marshal(importAttempt{Attempt: p.Attempt, Operation: operation, Prefix: o.PythonPrefix, Raw: i.Raw})
		if err := os.MkdirAll(filepath.Dir(operation), 0700); err != nil {
			return importer.Report{}, err
		}
		if err := platform.WriteAtomic(filepath.Join(root, "import", "journal.json"), record, 0600); err != nil {
			return importer.Report{}, err
		}
		if err := legacy.PublishMarker(o.PythonPrefix, operation, digest(map[string]string{"import": p.Attempt, "from": o.From}), installDigest); err != nil {
			os.Remove(filepath.Join(root, "import", "journal.json"))
			return importer.Report{}, err
		}
		if err := o.Hook("after:marker"); err != nil {
			return importer.Report{}, err
		}
		// The original configuration comes back whatever the import's outcome; a failure to
		// restore it keeps the record for the next run.
		defer func() {
			if rerr := recoverImport(root); rerr != nil && err == nil {
				err = rerr
			}
		}()
	}
	prepared, err := importer.CaptureAll(ctx, o.From, i, o.Repositories, p)
	if err != nil {
		return importer.Report{}, err
	}
	if o.Verify {
		return importer.Verify(ctx, prepared, p)
	}
	report, err = importer.Stage(ctx, prepared, p)
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
