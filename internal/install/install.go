// Package install places the koinon binary, writes and starts the daemon's user service,
// and configures the agents the user names; it also removes them again, keeping the
// state (sprint chunk 11, docs/INSTALL.md).
package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// Families are the agent families setup and removal know.
var Families = []string{"claude", "codex", "agy", "opencode", "deepseek"}

// Options select the installation. Zero values take the documented defaults.
type Options struct {
	Prefix       string   // default $XDG_DATA_HOME/koinon/go
	StateDir     string   // the Go state root; default platform.DefaultStateDir
	PythonPrefix string   // the Python-era prefix that refuses a plain install
	Agents       []string // families to set up, or to remove (default: all for removal)
	NoStart      bool
	// Upgrade is set by `koinon upgrade`, which removed the Python installation itself.
	Upgrade  bool
	Source   string // the binary to install; default this executable
	Address  string // the daemon address to wait for
	Services platform.Services
	Setup    func(context.Context, setup.Options) (setup.Report, error)
	Remove   func(context.Context, setup.Options) (setup.Report, error)
	Status   func(ctx context.Context, address, secret string) (json.RawMessage, error)
	Wait     time.Duration
}

// Report is the JSON result of an install or an uninstall.
type Report struct {
	OK           bool           `json:"ok"`
	Binary       string         `json:"binary"`
	BinaryResult string         `json:"binary_result"`
	Backend      string         `json:"backend"`
	Artifact     string         `json:"artifact"`
	Service      string         `json:"service"`
	StartCommand string         `json:"start_command,omitempty"`
	StateDir     string         `json:"state_dir"`
	Agents       []setup.Report `json:"agents"`
}

// DefaultPrefix is the Go installation prefix.
func DefaultPrefix() (string, error) {
	python, err := legacy.DefaultPrefix()
	if err != nil {
		return "", err
	}
	return filepath.Join(python, "go"), nil
}

func (o *Options) defaults() error {
	var err error
	if o.Prefix == "" {
		if o.Prefix, err = DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.StateDir == "" {
		if o.StateDir, err = platform.DefaultStateDir(); err != nil {
			return err
		}
	}
	if o.PythonPrefix == "" {
		if o.PythonPrefix, err = legacy.DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.Source == "" {
		if o.Source, err = os.Executable(); err != nil {
			return err
		}
	}
	if o.Address == "" {
		o.Address = "127.0.0.1:47671"
	}
	if o.Services.Run == nil {
		if o.Services, err = platform.NewServices(); err != nil {
			return err
		}
	}
	if o.Setup == nil {
		o.Setup = setup.Run
	}
	if o.Remove == nil {
		o.Remove = setup.Remove
	}
	if o.Status == nil {
		o.Status = core.GetStatus
	}
	if o.Wait == 0 {
		o.Wait = 20 * time.Second
	}
	for _, path := range []string{o.Prefix, o.StateDir, o.PythonPrefix, o.Source} {
		if !filepath.IsAbs(path) {
			return errors.New("installation paths must be absolute")
		}
	}
	for _, agent := range o.Agents {
		known := false
		for _, f := range Families {
			known = known || f == agent
		}
		if !known {
			return fmt.Errorf("unknown agent family %q", agent)
		}
	}
	return nil
}

// Binary is the installed binary's path under prefix.
func Binary(prefix string) string { return filepath.Join(prefix, "bin", "koinon") }

func digest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// place copies source to target atomically with mode 0755 and checks the copy's digest.
func place(source, target string) (string, error) {
	want, err := digest(source)
	if err != nil {
		return "", err
	}
	if have, err := digest(target); err == nil && bytes.Equal(have, want) {
		return "unchanged", nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".koinon-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if have, err := digest(tmp.Name()); err != nil || !bytes.Equal(have, want) {
		return "", errors.New("binary_copy_mismatch: the copied binary differs from its source")
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return "replaced", platform.SyncDir(filepath.Dir(target))
}

// Install places the binary, writes and starts the service, waits until the daemon
// answers, and runs setup for each named agent. A Python-era installation refuses it.
func Install(ctx context.Context, o Options) (Report, error) {
	if err := o.defaults(); err != nil {
		return Report{}, err
	}
	r := Report{OK: true, Binary: Binary(o.Prefix), Backend: o.Services.Backend, Artifact: o.Services.DaemonArtifact(),
		StateDir: o.StateDir, Agents: []setup.Report{}}
	if !o.Upgrade {
		i, err := legacy.ReadInstall(o.PythonPrefix)
		if err != nil {
			return r, err
		}
		if i != nil {
			return r, fmt.Errorf("python_install_present: a Python-era installation is at %s; run koinon upgrade --from-python", o.PythonPrefix)
		}
	}
	var err error
	if r.BinaryResult, err = place(o.Source, r.Binary); err != nil {
		return r, err
	}
	if _, err := o.Services.WriteDaemon(r.Binary, o.StateDir); err != nil {
		return r, err
	}
	r.Service = "staged"
	if !o.NoStart {
		switch err := o.Services.StartDaemon(ctx); {
		case errors.Is(err, platform.ErrManualRequired):
			r.Service, r.StartCommand = "manual_required", r.Binary+" serve --state-dir "+o.StateDir
		case err != nil:
			return r, err
		default:
			if err := waitForDaemon(ctx, o); err != nil {
				return r, err
			}
			r.Service = "running"
		}
	}
	for _, agent := range o.Agents {
		report, err := o.Setup(ctx, setup.Options{Family: agent, Binary: r.Binary, StateDir: o.StateDir})
		r.Agents = append(r.Agents, report)
		if err != nil {
			return r, fmt.Errorf("setup %s: %w", agent, err)
		}
	}
	return r, nil
}

// waitForDaemon polls the daemon's status until it answers or the wait ends.
func waitForDaemon(ctx context.Context, o Options) error {
	deadline := time.Now().Add(o.Wait)
	var last error
	for time.Now().Before(deadline) {
		secret, err := core.ReadSecret(o.StateDir)
		if err == nil {
			if _, err = o.Status(ctx, o.Address, secret); err == nil {
				return nil
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("daemon_unavailable: the daemon did not answer at %s: %v", o.Address, last)
}

// Uninstall removes what setup added for each named agent (every family by default),
// stops and removes the service artifact that carries the marker, and removes the
// binary. It keeps the state directory. A repeated uninstall changes nothing.
func Uninstall(ctx context.Context, o Options) (Report, error) {
	if err := o.defaults(); err != nil {
		return Report{}, err
	}
	r := Report{OK: true, Binary: Binary(o.Prefix), Backend: o.Services.Backend, Artifact: o.Services.DaemonArtifact(),
		StateDir: o.StateDir, Agents: []setup.Report{}}
	agents := o.Agents
	if len(agents) == 0 {
		agents = Families
	}
	for _, agent := range agents {
		report, err := o.Remove(ctx, setup.Options{Family: agent, Binary: r.Binary, StateDir: o.StateDir})
		r.Agents = append(r.Agents, report)
		if err != nil {
			return r, fmt.Errorf("remove %s: %w", agent, err)
		}
	}
	removed, err := o.Services.RemoveDaemon(ctx)
	if err != nil {
		return r, err
	}
	r.Service = map[bool]string{true: "removed", false: "absent"}[removed]
	r.BinaryResult = "absent"
	if info, err := os.Lstat(r.Binary); err == nil {
		if !info.Mode().IsRegular() {
			return r, fmt.Errorf("refusing to remove %s: not a regular file", r.Binary)
		}
		if err := os.Remove(r.Binary); err != nil {
			return r, err
		}
		r.BinaryResult = "removed"
	}
	// Empty directories of the prefix go too; anything else in them stays.
	os.Remove(filepath.Dir(r.Binary))
	os.Remove(o.Prefix)
	return r, nil
}
