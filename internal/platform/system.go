package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// System mode (#283, macOS): the daemon runs as a LaunchDaemon in the system domain under the
// installing user, so it survives the end of a desktop login. Every launchctl call goes through
// sudo: the person who runs `koinon install --system` approves each privileged step, and the
// calls also work from SSH and tmux, where the user's own gui/<uid> calls can fail.

// SystemDir holds the system plist; Services.SystemDir replaces it in tests.
const SystemDir = "/Library/LaunchDaemons"

// SystemArtifact is the path of the daemon's LaunchDaemon plist.
func (s Services) SystemArtifact() string {
	dir := s.SystemDir
	if dir == "" {
		dir = SystemDir
	}
	return filepath.Join(dir, DaemonLabel+".plist")
}

func (s Services) sudo(ctx context.Context, argv ...string) ([]byte, error) {
	return s.Run(ctx, append([]string{"sudo"}, argv...)...)
}

func (s Services) agentTarget() string  { return s.domain() + "/" + DaemonLabel }
func (s Services) systemTarget() string { return "system/" + DaemonLabel }

// SystemPlist is what the system plist says. Program and User are read only from a plist
// without the marker, to check a hand-written LaunchDaemon (criterion 7).
type SystemPlist struct {
	Present bool
	Marked  bool
	Program []string
	User    string
}

// ReadSystem reads the system plist. Its presence records the intent to run in system mode;
// whether the service works is verified separately.
func (s Services) ReadSystem(ctx context.Context) (SystemPlist, error) {
	path := s.SystemArtifact()
	present, marked, err := Marked(path)
	if err != nil || !present || marked {
		return SystemPlist{Present: present, Marked: marked}, err
	}
	out, err := s.Run(ctx, "plutil", "-convert", "json", "-o", "-", path)
	if err != nil {
		return SystemPlist{Present: true}, fmt.Errorf("system_plist_unreadable: plutil cannot read %s: %s", path, strings.TrimSpace(string(out)))
	}
	var fields struct {
		ProgramArguments []string `json:"ProgramArguments"`
		UserName         string   `json:"UserName"`
	}
	if err := json.Unmarshal(out, &fields); err != nil {
		return SystemPlist{Present: true}, fmt.Errorf("system_plist_unreadable: %s: %v", path, err)
	}
	return SystemPlist{Present: true, Program: fields.ProgramArguments, User: fields.UserName}, nil
}

// CheckHandWritten accepts an unmarked system plist only when it runs binary as `serve
// --state-dir state` (with an optional trailing --lock-wait) as this user. Otherwise the
// fixed error names the field that differs.
func (s Services) CheckHandWritten(p SystemPlist, binary, state string) error {
	path := s.SystemArtifact()
	args := p.Program
	switch {
	case len(args) == 0 || args[0] != binary:
		return fmt.Errorf("system_plist_mismatch: %s runs another executable than %s (ProgramArguments)", path, binary)
	case len(args) < 4 || args[1] != "serve" || args[2] != "--state-dir":
		return fmt.Errorf("system_plist_mismatch: %s does not run serve --state-dir (ProgramArguments)", path)
	case args[3] != state:
		return fmt.Errorf("system_plist_mismatch: %s serves another state directory than %s (state directory)", path, state)
	case len(args) != 4 && (len(args) != 6 || args[4] != "--lock-wait"):
		return fmt.Errorf("system_plist_mismatch: %s has other arguments after the state directory (ProgramArguments)", path)
	case p.User != s.User:
		return fmt.Errorf("system_plist_mismatch: %s runs as %q, not as %q (UserName)", path, p.User, s.User)
	}
	return nil
}

// RenderSystemDaemon is the LaunchDaemon: the agent's arguments and log paths, run as the
// installing user with its HOME, started at boot and restarted on any exit.
func (s Services) RenderSystemDaemon(binary, state string) []byte {
	return s.renderPlist(binary, state, `	<key>UserName</key>
	<string>`+xmlEscape(s.User)+`</string>
	<key>GroupName</key>
	<string>`+xmlEscape(s.Group)+`</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>`+xmlEscape(s.Home)+`</string>
	</dict>
`, `	<true/>
`)
}

// SystemInstall is one system-mode install: the plist that ReadSystem found, and StatusPID,
// which returns the process ID of the daemon that answers on the daemon address.
type SystemInstall struct {
	Binary, State string
	Plist         SystemPlist
	StatusPID     func(context.Context) (int, error)
	// Wait bounds the wait for the system daemon to answer; zero is 90 seconds (the lock
	// wait of a daemon that starts while the former one stops, plus the throttle).
	Wait time.Duration
}

// stepError is a failed step of the transition, with the rollback's result.
func stepError(step string, out []byte, err error) error {
	detail := strings.TrimSpace(string(out))
	if detail == "" && err != nil {
		detail = err.Error()
	}
	return fmt.Errorf("system_step_failed: %s: %s", step, detail)
}

// InstallSystem moves the daemon to the system domain, or reconciles an existing system-mode
// installation. A first transition stops and disables the user agent, installs, starts and
// verifies the LaunchDaemon, and then removes the owned agent plist; a failure restores the
// agent to its recorded state. A later run repeats the agent stop, rewrites only an owned plist
// whose content differs, bootstraps a service that is not loaded, restarts it and verifies it.
// It never writes, enables or bootstraps the user agent.
func (s Services) InstallSystem(ctx context.Context, in SystemInstall) error {
	if in.Wait == 0 {
		in.Wait = 90 * time.Second
	}
	path := s.SystemArtifact()
	if err := s.logDir(); err != nil {
		return err
	}
	var want []byte
	if !in.Plist.Present || in.Plist.Marked {
		want = s.RenderSystemDaemon(in.Binary, in.State)
	}
	current, _ := os.ReadFile(path)
	var staged string
	if want != nil && !bytes.Equal(current, want) {
		dir, err := os.MkdirTemp("", "koinon-system-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		staged = filepath.Join(dir, DaemonLabel+".plist")
		if err := os.WriteFile(staged, want, 0644); err != nil {
			return err
		}
		if out, err := s.Run(ctx, "plutil", "-lint", staged); err != nil {
			return stepError("lint", out, err)
		}
	}
	if in.Plist.Present {
		return s.reconcileSystem(ctx, in, staged)
	}
	agent, err := s.observeAgent(ctx)
	if err != nil {
		return err
	}
	started := false
	fail := func(err error) error {
		if rollback := s.rollbackSystem(ctx, agent, started); rollback != nil {
			return fmt.Errorf("%w; %w", err, rollback)
		}
		return fmt.Errorf("%w; rolled back: the user agent is as it was and no system plist is installed", err)
	}
	if err := s.stopAgent(ctx); err != nil {
		return fail(err)
	}
	started = true
	for _, step := range []struct {
		name string
		argv []string
	}{
		{"install_plist", []string{"install", "-o", "root", "-g", "wheel", "-m", "0644", staged, path}},
		{"bootstrap", []string{"launchctl", "bootstrap", "system", path}},
		{"kickstart", []string{"launchctl", "kickstart", "-k", s.systemTarget()}},
	} {
		if out, err := s.sudo(ctx, step.argv...); err != nil {
			return fail(stepError(step.name, out, err))
		}
	}
	if err := s.verifyOwner(ctx, in); err != nil {
		return fail(err)
	}
	return s.removeAgentPlist()
}

func (s Services) reconcileSystem(ctx context.Context, in SystemInstall, staged string) error {
	path := s.SystemArtifact()
	if err := s.stopAgent(ctx); err != nil {
		return err
	}
	loaded, _, err := s.systemService(ctx)
	if err != nil {
		return err
	}
	if staged != "" {
		// launchd keeps the loaded plist; a changed one takes effect after a new bootstrap.
		if loaded {
			if out, err := s.sudo(ctx, "launchctl", "bootout", s.systemTarget()); err != nil && !launchdAbsent(out, err) {
				return stepError("bootout_system", out, err)
			}
			if err := s.waitSystemStopped(ctx); err != nil {
				return err
			}
			loaded = false
		}
		if out, err := s.sudo(ctx, "install", "-o", "root", "-g", "wheel", "-m", "0644", staged, path); err != nil {
			return stepError("install_plist", out, err)
		}
	}
	if !loaded {
		if out, err := s.sudo(ctx, "launchctl", "bootstrap", "system", path); err != nil {
			return stepError("bootstrap", out, err)
		}
	}
	if out, err := s.sudo(ctx, "launchctl", "kickstart", "-k", s.systemTarget()); err != nil {
		return stepError("kickstart", out, err)
	}
	if err := s.verifyOwner(ctx, in); err != nil {
		return err
	}
	return s.removeAgentPlist()
}

// agentState is the user agent before a transition.
type agentState struct{ loaded, disabled bool }

func (s Services) observeAgent(ctx context.Context) (agentState, error) {
	var a agentState
	out, err := s.sudo(ctx, "launchctl", "print", s.agentTarget())
	switch {
	case err == nil:
		a.loaded = true
	case !launchdAbsent(out, err):
		return a, stepError("observe_agent", out, err)
	}
	disabled, err := s.agentDisabled(ctx)
	if err != nil {
		return a, err
	}
	a.disabled = disabled
	return a, nil
}

func (s Services) agentDisabled(ctx context.Context) (bool, error) {
	out, err := s.sudo(ctx, "launchctl", "print-disabled", s.domain())
	if err != nil {
		return false, stepError("print_disabled", out, err)
	}
	return regexp.MustCompile(`"` + regexp.QuoteMeta(DaemonLabel) + `"\s*=>\s*(disabled|true)`).Match(out), nil
}

// stopAgent disables the user agent and boots it out, then verifies both.
func (s Services) stopAgent(ctx context.Context) error {
	if out, err := s.sudo(ctx, "launchctl", "disable", s.agentTarget()); err != nil {
		return stepError("disable_agent", out, err)
	}
	if out, err := s.sudo(ctx, "launchctl", "bootout", s.agentTarget()); err != nil && !launchdAbsent(out, err) && !notLoaded(out) {
		return stepError("bootout_agent", out, err)
	}
	disabled, err := s.agentDisabled(ctx)
	if err != nil {
		return err
	}
	if !disabled {
		return stepError("verify_agent", []byte("print-disabled does not list the agent as disabled"), nil)
	}
	return s.waitAbsent(ctx, s.agentTarget(), "verify_agent")
}

// notLoaded recognizes bootout's answer for a service that is not loaded.
func notLoaded(out []byte) bool {
	text := string(out)
	return strings.Contains(text, "No such process") || strings.Contains(text, "Could not find specified service")
}

func (s Services) waitAbsent(ctx context.Context, target, step string) error {
	wait := s.StopWait
	if wait == 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		out, err := s.sudo(ctx, "launchctl", "print", target)
		switch {
		case err != nil && launchdAbsent(out, err):
			return nil
		case err != nil:
			return stepError(step, out, err)
		case time.Now().After(deadline):
			return stepError(step, []byte(target+" is still loaded"), nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s Services) waitSystemStopped(ctx context.Context) error {
	return s.waitAbsent(ctx, s.systemTarget(), "bootout_system")
}

var launchdPID = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)

// systemService reports whether the system service is loaded, and its process ID.
func (s Services) systemService(ctx context.Context) (bool, int, error) {
	out, err := s.sudo(ctx, "launchctl", "print", s.systemTarget())
	switch {
	case err == nil:
		pid := 0
		if m := launchdPID.FindSubmatch(out); m != nil {
			pid, _ = strconv.Atoi(string(m[1]))
		}
		return true, pid, nil
	case launchdAbsent(out, err):
		return false, 0, nil
	}
	return false, 0, stepError("print_system", out, err)
}

// verifyOwner waits until the daemon that answers is the system service's process. A daemon
// that answers with another process ID is the former agent.
func (s Services) verifyOwner(ctx context.Context, in SystemInstall) error {
	deadline := time.Now().Add(in.Wait)
	var detail string
	for {
		_, pid, err := s.systemService(ctx)
		if err != nil {
			return err
		}
		answered, statusErr := in.StatusPID(ctx)
		switch {
		case statusErr == nil && pid != 0 && answered == pid:
			return nil
		case statusErr != nil:
			detail = "the daemon does not answer: " + statusErr.Error()
		default:
			detail = fmt.Sprintf("the answering daemon is process %d, the system service is process %d", answered, pid)
		}
		if time.Now().After(deadline) {
			return stepError("verify_owner", []byte(detail), nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// rollbackSystem removes a started system service and restores the agent's recorded state.
// A failed command stops the rollback and reports the commands that finish it by hand.
func (s Services) rollbackSystem(ctx context.Context, agent agentState, started bool) error {
	var steps [][]string
	if started {
		steps = append(steps, []string{"launchctl", "bootout", s.systemTarget()}, []string{"rm", "-f", s.SystemArtifact()})
	}
	// launchd refuses to bootstrap a disabled service, so an agent that was loaded while
	// disabled is enabled for its bootstrap and disabled again.
	if !agent.disabled || agent.loaded {
		steps = append(steps, []string{"launchctl", "enable", s.agentTarget()})
	}
	if agent.loaded {
		steps = append(steps, []string{"launchctl", "bootstrap", s.domain(), s.DaemonArtifact()})
	}
	if agent.loaded && agent.disabled {
		steps = append(steps, []string{"launchctl", "disable", s.agentTarget()})
	}
	for i, argv := range steps {
		out, err := s.sudo(ctx, argv...)
		if err == nil || (argv[1] == "bootout" && (launchdAbsent(out, err) || notLoaded(out))) {
			continue
		}
		var manual []string
		for _, rest := range steps[i:] {
			manual = append(manual, "sudo "+shellJoin(rest))
		}
		return fmt.Errorf("rollback_failed: %s; finish it by hand: %s", strings.TrimSpace(string(out)), strings.Join(manual, "; "))
	}
	return nil
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_/.:@%+=,-]+$`)

// shellJoin quotes each word that a shell would split or expand.
func shellJoin(argv []string) string {
	words := make([]string, len(argv))
	for i, word := range argv {
		words[i] = word
		if !plainWord.MatchString(word) {
			words[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
		}
	}
	return strings.Join(words, " ")
}

// removeAgentPlist removes the user agent's plist when it carries the marker.
func (s Services) removeAgentPlist() error {
	path := s.DaemonArtifact()
	present, marked, err := Marked(path)
	if err != nil || !present || !marked {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveSystem boots out and removes an owned system plist. An unowned one is refused with
// the commands that remove it. It returns whether anything was removed.
func (s Services) RemoveSystem(ctx context.Context) (bool, error) {
	path := s.SystemArtifact()
	present, marked, err := Marked(path)
	if err != nil || !present {
		return false, err
	}
	if !marked {
		return false, fmt.Errorf("service_artifact_unowned: %s exists without the Koinon marker; remove it with: sudo launchctl bootout %s; sudo rm %s",
			path, s.systemTarget(), path)
	}
	if out, err := s.sudo(ctx, "launchctl", "bootout", s.systemTarget()); err != nil && !launchdAbsent(out, err) && !notLoaded(out) {
		return false, fmt.Errorf("service_stop_failed: launchctl bootout %s: %s", s.systemTarget(), strings.TrimSpace(string(out)))
	}
	if err := s.waitSystemStopped(ctx); err != nil {
		return false, fmt.Errorf("service_stop_failed: %w", err)
	}
	if out, err := s.sudo(ctx, "rm", "-f", path); err != nil {
		return false, fmt.Errorf("service_remove_failed: sudo rm %s: %s", path, strings.TrimSpace(string(out)))
	}
	return true, nil
}
