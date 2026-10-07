package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The user service manager: a systemd user unit on Linux, a launchd agent on macOS
// (sprint chunk 11). Both are rendered and driven here on every platform, so tests
// check both; ServiceBackend selects the one the host uses. Every manager call goes
// through Run, which tests replace.

// ServiceMarker identifies an artifact that `koinon install` wrote. An artifact at the
// same path without it is never replaced or removed.
const ServiceMarker = "koinon-go-daemon-v1"

// DaemonUnit and DaemonLabel name the daemon's service.
const (
	DaemonUnit  = "koinon.service"
	DaemonLabel = "io.github.rwcii.koinon.daemon"
)

// ErrManualRequired means no user service manager is reachable.
var ErrManualRequired = errors.New("manual_required")

// ServiceBackend is the manager this platform uses.
func ServiceBackend() string {
	if runtime.GOOS == "darwin" {
		return "launchd"
	}
	return "systemd"
}

// Services drives one user's service manager.
type Services struct {
	Backend string
	Home    string
	UID     int
	// Run runs a manager command and returns its combined output.
	Run func(ctx context.Context, argv ...string) ([]byte, error)
	// StopWait bounds the wait for a stopped unit; zero is 30 seconds.
	StopWait time.Duration
}

// NewServices returns the manager of this host and user.
func NewServices() (Services, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Services{}, err
	}
	return Services{Backend: ServiceBackend(), Home: home, UID: os.Geteuid(), Run: RunCommand}, nil
}

// RunCommand runs argv and returns its combined output.
func RunCommand(ctx context.Context, argv ...string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func (s Services) domain() string { return "gui/" + strconv.Itoa(s.UID) }

// DaemonArtifact is the path of the daemon's unit or agent file.
func (s Services) DaemonArtifact() string {
	if s.Backend == "launchd" {
		return filepath.Join(s.Home, "Library", "LaunchAgents", DaemonLabel+".plist")
	}
	return filepath.Join(s.Home, ".config", "systemd", "user", DaemonUnit)
}

// RenderDaemon is the daemon's unit or agent: binary serves the state directory state.
func (s Services) RenderDaemon(binary, state string) []byte {
	if s.Backend == "launchd" {
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + DaemonLabel + `</string>
	<key>KoinonManaged</key>
	<string>` + ServiceMarker + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(binary) + `</string>
		<string>serve</string>
		<string>--state-dir</string>
		<string>` + xmlEscape(state) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>Umask</key>
	<integer>63</integer>
</dict>
</plist>
`)
		return b.Bytes()
	}
	return []byte("# " + ServiceMarker + "\n[Unit]\nDescription=Koinon daemon\n\n[Service]\nExecStart=" +
		systemdQuote(binary) + " serve --state-dir " + systemdQuote(state) + "\nRestart=on-failure\nRestartSec=5\nUMask=0077\n\n[Install]\nWantedBy=default.target\n")
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// systemdQuote quotes one ExecStart word; a percent sign is doubled so systemd does not
// expand it as a specifier.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Marked reports whether the artifact at path carries ServiceMarker; absent is false
// with a nil error.
func Marked(path string) (present, marked bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return true, false, err
	}
	return true, bytes.Contains(data, []byte(ServiceMarker)), nil
}

// Available reports whether the user's service manager answers.
func (s Services) Available(ctx context.Context) bool {
	if s.Backend == "launchd" {
		_, err := s.Run(ctx, "launchctl", "print", s.domain())
		return err == nil
	}
	_, err := s.Run(ctx, "systemctl", "--user", "show-environment")
	return err == nil
}

// WriteDaemon writes the daemon's artifact for binary. An existing artifact without the
// marker is refused. It returns whether the artifact changed.
func (s Services) WriteDaemon(binary, state string) (bool, error) {
	path := s.DaemonArtifact()
	present, marked, err := Marked(path)
	if err != nil {
		return false, err
	}
	if present && !marked {
		return false, fmt.Errorf("service_artifact_unowned: %s exists without the Koinon marker", path)
	}
	want := s.RenderDaemon(binary, state)
	current, _ := os.ReadFile(path)
	if bytes.Equal(current, want) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	return true, WriteAtomic(path, want, 0644)
}

// StartDaemon enables and (re)starts the daemon from its written artifact, so a replaced
// binary or artifact takes effect. Without a reachable manager it is ErrManualRequired.
func (s Services) StartDaemon(ctx context.Context) error {
	if !s.Available(ctx) {
		return ErrManualRequired
	}
	if s.Backend == "launchd" {
		// The agent is loaded again, so a changed file takes effect; an absent one makes
		// bootout fail harmlessly.
		s.Run(ctx, "launchctl", "bootout", s.domain()+"/"+DaemonLabel)
		if out, err := s.Run(ctx, "launchctl", "bootstrap", s.domain(), s.DaemonArtifact()); err != nil {
			return fmt.Errorf("launchctl bootstrap failed: %s", strings.TrimSpace(string(out)))
		}
		if out, err := s.Run(ctx, "launchctl", "kickstart", s.domain()+"/"+DaemonLabel); err != nil {
			return fmt.Errorf("launchctl kickstart failed: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	for _, argv := range [][]string{{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", DaemonUnit}, {"systemctl", "--user", "restart", DaemonUnit}} {
		if out, err := s.Run(ctx, argv...); err != nil {
			return fmt.Errorf("%s failed: %s", strings.Join(argv, " "), strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// RemoveDaemon stops the daemon and removes its artifact when it carries the marker.
// The artifact stays when the manager refuses the stop or the daemon is still running
// afterwards. An absent artifact is a no-op; it returns whether anything was removed.
func (s Services) RemoveDaemon(ctx context.Context) (bool, error) {
	path := s.DaemonArtifact()
	present, marked, err := Marked(path)
	if err != nil || !present {
		return false, err
	}
	if !marked {
		return false, fmt.Errorf("service_artifact_unowned: %s exists without the Koinon marker", path)
	}
	available := s.Available(ctx)
	if available {
		unit := Unit{Name: DaemonUnit}
		var out []byte
		var stopErr error
		if s.Backend == "launchd" {
			unit = Unit{Name: DaemonLabel}
			// An agent that is not loaded makes bootout fail; the observation below decides.
			out, stopErr = s.Run(ctx, "launchctl", "bootout", s.domain()+"/"+DaemonLabel)
		} else if out, stopErr = s.Run(ctx, "systemctl", "--user", "disable", "--now", DaemonUnit); stopErr != nil {
			return false, fmt.Errorf("service_stop_failed: systemctl disable --now %s: %s", DaemonUnit, strings.TrimSpace(string(out)))
		}
		if err := s.waitStopped(ctx, unit); err != nil {
			if stopErr != nil {
				err = fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
			}
			return false, err
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if s.Backend == "systemd" && available {
		s.Run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return true, nil
}

// waitStopped waits until a unit is observed stopped; launchd unloads asynchronously.
// An unknown observation or a unit still running at the deadline is service_stop_failed.
func (s Services) waitStopped(ctx context.Context, u Unit) error {
	wait := s.StopWait
	if wait == 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		active, err := s.Active(ctx, u)
		if err != nil {
			return fmt.Errorf("service_stop_failed: cannot observe %s: %w", u.Name, err)
		}
		if !active {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service_stop_failed: %s is still running after its stop", u.Name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Unit is another program's service, such as a Python-era one: its systemd unit name,
// or its launchd label with the agent file that loads it again.
type Unit struct {
	Name     string `json:"name"`
	Artifact string `json:"artifact,omitempty"`
}

// Active reports whether a unit is running (systemd) or loaded (launchd).
func (s Services) Active(ctx context.Context, u Unit) (bool, error) {
	if s.Backend == "launchd" {
		_, err := s.Run(ctx, "launchctl", "print", s.domain()+"/"+u.Name)
		return err == nil, nil
	}
	out, err := s.Run(ctx, "systemctl", "--user", "is-active", u.Name)
	state := strings.TrimSpace(string(out))
	switch {
	case err == nil:
		return true, nil
	case state == "inactive" || state == "failed" || state == "unknown":
		return false, nil
	case state == "activating" || state == "deactivating" || state == "reloading":
		return true, nil
	}
	return false, fmt.Errorf("cannot observe %s: %s", u.Name, state)
}

// Stop stops a unit without removing it, and waits until it is observed stopped;
// launchd unloads the agent.
func (s Services) Stop(ctx context.Context, u Unit) error {
	if s.Backend == "launchd" {
		if active, _ := s.Active(ctx, u); !active {
			return nil
		}
		if out, err := s.Run(ctx, "launchctl", "bootout", s.domain()+"/"+u.Name); err != nil {
			return fmt.Errorf("launchctl bootout %s failed: %s", u.Name, strings.TrimSpace(string(out)))
		}
		return s.waitStopped(ctx, u)
	}
	if out, err := s.Run(ctx, "systemctl", "--user", "stop", u.Name); err != nil {
		return fmt.Errorf("systemctl stop %s failed: %s", u.Name, strings.TrimSpace(string(out)))
	}
	return s.waitStopped(ctx, u)
}

// Start starts a unit again; launchd loads its agent file.
func (s Services) Start(ctx context.Context, u Unit) error {
	if s.Backend == "launchd" {
		if active, _ := s.Active(ctx, u); !active {
			if u.Artifact == "" {
				return fmt.Errorf("no agent file to load %s", u.Name)
			}
			if out, err := s.Run(ctx, "launchctl", "bootstrap", s.domain(), u.Artifact); err != nil {
				return fmt.Errorf("launchctl bootstrap %s failed: %s", u.Name, strings.TrimSpace(string(out)))
			}
		}
		return nil
	}
	if out, err := s.Run(ctx, "systemctl", "--user", "start", u.Name); err != nil {
		return fmt.Errorf("systemctl start %s failed: %s", u.Name, strings.TrimSpace(string(out)))
	}
	return nil
}

// WriteAtomic replaces path with data through a synced temporary file in its directory.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}
