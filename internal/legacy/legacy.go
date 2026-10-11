// Package legacy reads a Python-era Koinon installation for the import and the upgrade
// (sprint chunk 11): its install.json, the admission marker that makes its own commands
// refuse, the services it runs and the kernel locks its writers hold. It never runs
// Python code; the upgrade runs the installed uninstall.py separately.
package legacy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// Install is a Python installation's install.json, exactly as read.
type Install struct {
	Prefix string
	Raw    []byte
	Config map[string]any
}

// DefaultPrefix is the Python runtime's default installation prefix.
func DefaultPrefix() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "koinon"), nil
}

// DefaultStateRoot is the Python runtime's default state root.
func DefaultStateRoot() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "koinon"), nil
}

// ReadInstall reads prefix/install.json with the Python runtime's file checks: a
// regular file owned by this user and not writable by others. Absent is nil, nil.
func ReadInstall(prefix string) (*Install, error) {
	path := filepath.Join(prefix, "install.json")
	f, err := platform.OpenOwned(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invalid_install_configuration: %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("invalid_install_configuration: %s is writable by others", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, err
	}
	// Numbers stay exact, so a rewrite under the marker never changes another field.
	var config map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&config); err != nil || config == nil {
		return nil, fmt.Errorf("invalid_install_configuration: %s is not a JSON object", path)
	}
	return &Install{Prefix: prefix, Raw: raw, Config: config}, nil
}

// State is the installation lifecycle state; absent means installed.
func (i *Install) State() string {
	if s, ok := i.Config["installation_state"].(string); ok {
		return s
	}
	return "installed"
}

// StateRoot is the saved state root, or the default.
func (i *Install) StateRoot() (string, error) {
	if s, ok := i.Config["state_root"].(string); ok && filepath.IsAbs(s) {
		return s, nil
	}
	return DefaultStateRoot()
}

// Operation is the operation an upgrading marker names, or "".
func (i *Install) Operation() string {
	if i.State() != "upgrading" {
		return ""
	}
	marker, _ := i.Config["upgrade"].(map[string]any)
	operation, _ := marker["operation"].(string)
	return operation
}

// MemoryRepositories maps each saved memory selection's key to its Git common directory.
func (i *Install) MemoryRepositories() map[string]string {
	result := map[string]string{}
	services, _ := i.Config["memory_services"].(map[string]any)
	repos, _ := services["repositories"].(map[string]any)
	for key, value := range repos {
		record, _ := value.(map[string]any)
		if common, ok := record["common_directory"].(string); ok && filepath.IsAbs(common) {
			result[key] = common
		}
	}
	return result
}

var planDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// lockInstall takes the Python runtime's permanent installation lock, waiting as long
// as the Python runtime itself waits.
func lockInstall(prefix string) (*os.File, error) {
	path := filepath.Join(prefix, ".install.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("configuration_busy: the Python installation lock is held")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// update replaces install.json under the installation lock after check accepts the
// current file, preserving every other field in the Python runtime's format.
func update(prefix string, check func(*Install) error, change func(map[string]any) ([]byte, error)) error {
	lock, err := lockInstall(prefix)
	if err != nil {
		return err
	}
	defer platform.Unlock(lock)
	current, err := ReadInstall(prefix)
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("python_install_missing: install.json disappeared")
	}
	if err := check(current); err != nil {
		return err
	}
	data, err := change(current.Config)
	if err != nil {
		return err
	}
	return platform.WriteAtomic(filepath.Join(prefix, "install.json"), data, 0600)
}

func pythonJSON(config map[string]any) ([]byte, error) {
	// Sorted keys and a final newline, as the Python runtime writes it; the Python
	// runtime parses the file, so separator spacing does not matter.
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(config); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Digest is the SHA-256 of the configuration apart from any upgrade marker, so an
// attempt can bind itself to the exact configuration it inspected.
func (i *Install) Digest() (string, error) {
	data, err := i.Without()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// PublishMarker sets the baseline's upgrading marker, which makes the Python runtime's
// install, ensure, uninstall, upgrade and memory-service start refuse. Under the
// installation lock, the configuration must still be the one the caller inspected
// (expected, from Digest), and installed or already carrying this same marker.
func PublishMarker(prefix, operation, plan, expected string) error {
	if !filepath.IsAbs(operation) || !planDigest.MatchString(plan) {
		return errors.New("invalid upgrade marker")
	}
	return update(prefix, func(i *Install) error {
		digest, err := i.Digest()
		if err != nil {
			return err
		}
		if digest != expected {
			return errors.New("source_changed: install.json changed after it was inspected; nothing was changed")
		}
		if i.State() == "upgrading" && i.Operation() == operation {
			return nil
		}
		if i.State() != "installed" {
			return fmt.Errorf("python_install_busy: the Python installation is %s", i.State())
		}
		return nil
	}, func(config map[string]any) ([]byte, error) {
		config["installation_state"] = "upgrading"
		config["upgrade"] = map[string]any{"version": 1, "operation": operation, "plan": plan}
		return pythonJSON(config)
	})
}

// ReplaceWithRemoving changes this operation's upgrading marker to the removing state,
// in which the baseline still refuses install, ensure and memory-service start, and its
// uninstall.py continues.
func ReplaceWithRemoving(prefix, operation string) error {
	return update(prefix, func(i *Install) error {
		if i.State() == "removing" {
			return nil
		}
		if i.Operation() != operation {
			return errors.New("python_install_changed: the upgrade marker belongs to another operation")
		}
		return nil
	}, func(config map[string]any) ([]byte, error) {
		config["installation_state"] = "removing"
		delete(config, "upgrade")
		return pythonJSON(config)
	})
}

// RestoreRaw writes back the exact install.json bytes from before the marker, while
// the file still carries this operation's marker.
func RestoreRaw(prefix, operation string, raw []byte) error {
	return update(prefix, func(i *Install) error {
		if i.Operation() != operation {
			return errors.New("python_install_changed: the upgrade marker belongs to another operation")
		}
		return nil
	}, func(map[string]any) ([]byte, error) { return raw, nil })
}

// Without returns the configuration bytes with any marker of operation removed, so a
// resume compares the installation apart from its marker.
func (i *Install) Without() ([]byte, error) {
	config := map[string]any{}
	for k, v := range i.Config {
		config[k] = v
	}
	if i.State() == "upgrading" {
		delete(config, "installation_state")
		delete(config, "upgrade")
	}
	return pythonJSON(config)
}

// Component is one writer of Python-era state: a session or legacy inbox directory, or
// a memory store directory, with the kernel locks its processes hold while they run.
type Component struct {
	Kind  string   `json:"kind"`
	Home  string   `json:"home"`
	Locks []string `json:"locks"`
}

// The locks the baseline's own upgrade capture takes for each kind of writer.
var componentLocks = map[string][]string{
	"session": {"lifecycle.lock", "registration.lock", "supervisor.lock", "notifier.lock"},
	"memory":  {"manager.lock", "supervisor.lock", "start.lock"},
	"legacy":  {"notifier.lock"},
}

// Components lists every writer directory under a Python state root.
func Components(stateRoot string) ([]Component, error) {
	var result []Component
	for _, kind := range []string{"sessions", "memory"} {
		entries, err := os.ReadDir(filepath.Join(stateRoot, kind))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := map[string]string{"sessions": "session", "memory": "memory"}[kind]
			result = append(result, Component{Kind: name, Home: filepath.Join(stateRoot, kind, e.Name()), Locks: componentLocks[name]})
		}
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, "inbox.sqlite3")); err == nil {
		result = append(result, Component{Kind: "legacy", Home: stateRoot, Locks: componentLocks["legacy"]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Home < result[j].Home })
	return result, nil
}

// Guard holds the writer locks of a set of components.
type Guard struct{ files []*os.File }

// HoldWriters takes every component's writer locks without waiting. A held lock means
// a Python process still runs, and the result is python_running. A lock file that does
// not exist is created, so a writer that starts later contends on the same file.
func HoldWriters(components []Component) (*Guard, error) {
	g := &Guard{}
	for _, c := range components {
		for _, name := range c.Locks {
			path := filepath.Join(c.Home, name)
			f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
			if err != nil {
				g.Release()
				return nil, err
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				f.Close()
				g.Release()
				return nil, fmt.Errorf("python_running: a Python process holds %s", path)
			}
			g.files = append(g.files, f)
		}
	}
	return g, nil
}

// Release drops every held lock.
func (g *Guard) Release() {
	if g == nil {
		return
	}
	for _, f := range g.files {
		platform.Unlock(f)
	}
	g.files = nil
}

// fallbackSockets is the directory where the Python runtime put a control socket whose
// direct path was too long for an AF_UNIX address.
var fallbackSockets = "/tmp/cc-socks"

// resolve resolves symbolic links as Python's Path.resolve does: a missing final
// part is kept as written.
func resolve(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	if parent := filepath.Dir(path); parent != path {
		return filepath.Join(resolve(parent), filepath.Base(path))
	}
	return path
}

// endpoints lists the control socket paths that the Python runtime's session stop
// checks for one bridge or notifier directory: the direct socket under the written and
// the resolved directory, when short enough to bind, and the fallback socket.
func endpoints(root string) []string {
	resolved := resolve(root)
	var paths []string
	for _, dir := range []string{resolved, root} {
		path := filepath.Join(dir, "control.sock")
		if len(path) < platform.UnixPathBytes() && (len(paths) == 0 || paths[0] != path) {
			paths = append(paths, path)
		}
	}
	sum := sha256.Sum256([]byte(resolved))
	return append(paths, filepath.Join(fallbackSockets, hex.EncodeToString(sum[:])[:16]+"-control.sock"))
}

// deadSocket reports whether path is a socket of this user that has no live owner. A
// refused connection alone is no proof: a socket that is bound but not yet listening, and
// on macOS a full listener, refuse too. The kernel's socket table, read after the refusal,
// lists every socket that a process still holds. A missing path is not dead; a socket
// that accepts a connection or that the table lists is python_running.
func deadSocket(path string) (bool, error) {
	unverified := func(why string) error {
		return fmt.Errorf("python_endpoint_unverified: cannot prove that nothing listens on %s (%s); stop its owner, verify that it is gone, remove the file and run the upgrade again", path, why)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, unverified(err.Error())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSocket == 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, unverified("not a socket of this user")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		conn.Close()
		return false, fmt.Errorf("python_running: a Python process listens on %s", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return false, unverified(err.Error())
	}
	bound, err := platform.BoundUnixPaths()
	if err != nil {
		return false, unverified("cannot read the socket table: " + err.Error())
	}
	target := resolve(path)
	for _, owned := range bound {
		if owned == path || resolve(owned) == target {
			return false, fmt.Errorf("python_running: a process still holds %s", path)
		}
	}
	return true, nil
}

// ClearDeadEndpoints removes the control sockets that Python sessions left when their
// processes crashed or were killed. The installed uninstall.py treats any remaining
// endpoint as a running session and stops. A file is removed only while this process
// holds the session's writer locks, which its running supervisor and notifier hold, and
// only when it is a socket of this user that refuses a connection and that no process
// holds. It returns the removed paths, also when it stops at a path that it cannot
// prove dead.
func ClearDeadEndpoints(stateRoot string) ([]string, error) {
	components, err := Components(stateRoot)
	if err != nil {
		return nil, err
	}
	var sessions []Component
	for _, c := range components {
		if c.Kind == "session" {
			sessions = append(sessions, c)
		}
	}
	guard, err := HoldWriters(sessions)
	if err != nil {
		return nil, err
	}
	defer guard.Release()
	var removed []string
	for _, c := range sessions {
		for _, root := range []string{c.Home, filepath.Join(c.Home, "notifier")} {
			for _, path := range endpoints(root) {
				dead, err := deadSocket(path)
				if err != nil {
					return removed, err
				}
				if !dead {
					continue
				}
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return removed, err
				}
				removed = append(removed, path)
			}
		}
	}
	return removed, nil
}

// Units lists the services a Python installation runs: the memory services of its
// saved selections, the session supervisors of its native-service records, and the
// legacy bridge and notifier pair when their unit files carry the Python marker.
func Units(ctx context.Context, i *Install, services platform.Services) ([]platform.Unit, error) {
	var units []platform.Unit
	memory, _ := i.Config["memory_services"].(map[string]any)
	repos, _ := memory["repositories"].(map[string]any)
	keys := make([]string, 0, len(repos))
	for key := range repos {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record, _ := repos[key].(map[string]any)
		backend, _ := record["backend"].(string)
		artifact, _ := record["artifact"].(string)
		switch backend {
		case "systemd":
			units = append(units, platform.Unit{Name: "koinon-memory-" + key + ".service"})
		case "launchd":
			units = append(units, platform.Unit{Name: "io.github.rwcii.koinon.memory." + key, Artifact: artifact})
		}
	}
	root, err := i.StateRoot()
	if err != nil {
		return nil, err
	}
	sessions, _ := filepath.Glob(filepath.Join(root, "sessions", "*", "native-service.json"))
	sort.Strings(sessions)
	for _, path := range sessions {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var record struct {
			Backend  string `json:"backend"`
			Artifact string `json:"artifact"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("invalid native session record %s", path)
		}
		name := filepath.Base(record.Artifact)
		switch record.Backend {
		case "systemd":
			units = append(units, platform.Unit{Name: name})
		case "launchd":
			units = append(units, platform.Unit{Name: strings.TrimSuffix(name, ".plist"), Artifact: record.Artifact})
		}
	}
	unitDir, _ := i.Config["unit_dir"].(string)
	if unitDir != "" {
		for _, name := range []string{"koinon-bridge.service", "koinon-notify.service", "codex-peer-bridge.service", "codex-peer-notify.service"} {
			data, err := os.ReadFile(filepath.Join(unitDir, name))
			if err == nil && bytes.Contains(data, []byte("# Managed by koinon")) {
				units = append(units, platform.Unit{Name: name})
			}
		}
	}
	return units, nil
}
