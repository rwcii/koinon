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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	LinkDir  string // the directory of the koinon link; default ~/.local/bin
	Path     string // the search path that the report resolves koinon on; default $PATH
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
	Link         string         `json:"link"`
	LinkResult   string         `json:"link_result"`
	OnPath       string         `json:"on_path,omitempty"`
	PathStep     string         `json:"path_step,omitempty"`
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
	if o.LinkDir == "" {
		o.LinkDir = filepath.Join(o.Services.Home, ".local", "bin")
	}
	if o.Path == "" {
		o.Path = os.Getenv("PATH")
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
	for _, path := range []string{o.Prefix, o.StateDir, o.PythonPrefix, o.Source, o.LinkDir} {
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

// linkMarker records, in the prefix, the link that the install created.
func linkMarker(prefix string) string { return filepath.Join(prefix, "link") }

// link puts a koinon link to binary in dir, so that koinon on PATH runs the installed
// copy. Another file or link there is never replaced: the result is then "occupied".
func link(binary, dir, marker string) (string, string, error) {
	path := filepath.Join(dir, "koinon")
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0755); err != nil {
			return path, "", err
		}
		// The marker comes first, so a link is never left unrecorded.
		if err := writeMarker(marker, path); err != nil {
			return path, "", err
		}
		if err := os.Symlink(binary, path); err != nil {
			os.Remove(marker)
			return path, "", err
		}
		return path, "created", nil
	case err != nil:
		return path, "", err
	case info.Mode()&fs.ModeSymlink != 0:
		if target, err := os.Readlink(path); err == nil && target == binary {
			return path, "unchanged", nil
		}
	}
	return path, "occupied", nil
}

// readMarker returns the link that the marker records, or "" without a marker. A marker
// that is not a regular file of this user (a symlink included), or that names no koinon
// link, is refused, so an unrelated file there is never read as one or overwritten.
func readMarker(marker string) (string, error) {
	invalid := errors.New("link_marker_invalid: " + marker + " is not a koinon link marker")
	f, err := platform.OpenOwned(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", invalid
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	recorded := strings.TrimSpace(string(data))
	if err != nil || len(data) > 4096 || !filepath.IsAbs(recorded) || filepath.Base(recorded) != "koinon" {
		return "", invalid
	}
	return recorded, nil
}

// writeMarker records path in the marker. It replaces only a valid marker, by renaming a
// new file over it, which never follows a symlink.
func writeMarker(marker, path string) error {
	if _, err := readMarker(marker); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(marker), ".link-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(path + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), marker)
}

// searchDirs resolves the search path's directories as a shell in the current directory
// would: an empty entry is the current directory, and a relative entry is under it.
func searchDirs(path string) []string {
	wd, _ := os.Getwd()
	var dirs []string
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			if wd == "" {
				continue
			}
			dir = filepath.Join(wd, dir)
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	return dirs
}

// lookPath returns the koinon that the search path runs, or "".
func lookPath(path string) string {
	for _, dir := range searchDirs(path) {
		candidate := filepath.Join(dir, "koinon")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate
		}
	}
	return ""
}

func sameFile(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// pathStep names the step that makes koinon on PATH run the installed binary, or "" when
// it already does. The install never edits shell startup files.
func pathStep(r Report, dir, path string) string {
	if r.OnPath != "" && sameFile(r.OnPath, r.Binary) {
		return ""
	}
	listed := false
	for _, entry := range searchDirs(path) {
		listed = listed || entry == filepath.Clean(dir)
	}
	switch {
	case r.LinkResult == "occupied":
		return fmt.Sprintf("%s is not a link to the installed binary; replace it with ln -sf %s %s, or run %s", r.Link, r.Binary, r.Link, r.Binary)
	case !listed:
		return fmt.Sprintf("add %s to PATH in your shell's startup file: export PATH=\"%s:$PATH\"", dir, dir)
	case r.OnPath != "":
		return fmt.Sprintf("PATH runs %s first; put %s before its directory in PATH, or run %s", r.OnPath, dir, r.Binary)
	}
	return "run " + r.Binary
}

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
	if r.Link, r.LinkResult, err = link(r.Binary, o.LinkDir, linkMarker(o.Prefix)); err != nil {
		return r, err
	}
	r.OnPath = lookPath(o.Path)
	r.PathStep = pathStep(r, o.LinkDir, o.Path)
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
// binary and the link that the install created while it still points at the binary. It
// keeps the state directory. A repeated uninstall changes nothing.
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
	if r.Link, r.LinkResult, err = unlink(r.Binary, o.LinkDir, linkMarker(o.Prefix)); err != nil {
		return r, err
	}
	// Empty directories of the prefix go too; anything else in them stays.
	os.Remove(filepath.Dir(r.Binary))
	os.Remove(o.Prefix)
	return r, nil
}

// unlink removes the link that the marker records while it still points at binary, then
// the marker. A link the install did not create, or one that now points elsewhere, is
// kept.
func unlink(binary, dir, marker string) (string, string, error) {
	path := filepath.Join(dir, "koinon")
	recorded, err := readMarker(marker)
	switch {
	case err != nil:
		return path, "", err
	case recorded != "":
		path = recorded
		if target, err := os.Readlink(path); err == nil && target == binary {
			if err := os.Remove(path); err != nil {
				return path, "", err
			}
			if err := os.Remove(marker); err != nil {
				return path, "", err
			}
			return path, "removed", nil
		}
		if err := os.Remove(marker); err != nil {
			return path, "", err
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return path, "kept", nil
	}
	return path, "absent", nil
}
