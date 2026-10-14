package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/platform/launchdtest"
)

const label = platform.DaemonLabel

func systemServices(t *testing.T) (platform.Services, *launchdtest.Fake) {
	t.Helper()
	f := launchdtest.New(501, label)
	dir := t.TempDir()
	s := platform.Services{Backend: "launchd", Home: filepath.Join(dir, "home"), UID: 501, User: "alice", Group: "staff",
		SystemDir: filepath.Join(dir, "LaunchDaemons"), Run: f.Run, StopWait: 50 * time.Millisecond}
	if err := os.MkdirAll(s.SystemDir, 0755); err != nil {
		t.Fatal(err)
	}
	// The agent of an earlier plain install.
	if err := os.MkdirAll(filepath.Dir(s.DaemonArtifact()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.DaemonArtifact(), s.RenderDaemon("/p/bin/koinon", "/state"), 0644); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func installSystem(s platform.Services, f *launchdtest.Fake) error {
	p, err := s.ReadSystem(context.Background())
	if err != nil {
		return err
	}
	return s.InstallSystem(context.Background(), platform.SystemInstall{Binary: "/p/bin/koinon", State: "/state", Plist: p,
		StatusPID: f.StatusPID, Wait: 100 * time.Millisecond})
}

var staged = regexp.MustCompile(`\S*/koinon-system-\d+/` + regexp.QuoteMeta(label) + `\.plist`)

func calls(f *launchdtest.Fake, s platform.Services) []string {
	var out []string
	for _, call := range f.Calls {
		call = staged.ReplaceAllString(call, "TMP")
		out = append(out, strings.ReplaceAll(call, s.SystemArtifact(), "SYS"))
	}
	return out
}

// The transition runs the planned steps in order, all launchctl calls through sudo, while
// plain gui/<uid> calls fail with error 125 as over SSH (#283 chunk 02).
func TestSystemTransition(t *testing.T) {
	s, f := systemServices(t)
	if err := installSystem(s, f); err != nil {
		t.Fatal(err)
	}
	agent := "gui/501/" + label
	want := []string{
		"plutil -lint TMP",
		"sudo launchctl print gui/501",
		"sudo launchctl print " + agent,
		"sudo launchctl print-disabled gui/501",
		"sudo launchctl disable " + agent,
		"sudo launchctl bootout " + agent,
		"sudo launchctl print-disabled gui/501",
		"sudo launchctl print " + agent,
		"sudo install -o root -g wheel -m 0644 TMP SYS",
		"sudo launchctl bootstrap system SYS",
		"sudo launchctl kickstart -k system/" + label,
		"sudo launchctl print system/" + label,
	}
	if got := calls(f, s); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f.AgentLoaded || !f.AgentDisabled || !f.SystemLoaded {
		t.Fatalf("state %+v", f)
	}
	if _, err := os.Stat(s.DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatalf("the owned agent plist stayed: %v", err)
	}
	plist, err := os.ReadFile(s.SystemArtifact())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"<key>UserName</key>\n\t<string>alice</string>", "<key>GroupName</key>\n\t<string>staff</string>",
		"<key>HOME</key>\n\t\t<string>" + s.Home + "</string>", "<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>", "<key>ThrottleInterval</key>\n\t<integer>10</integer>",
		"<key>Umask</key>\n\t<integer>63</integer>", "<string>--lock-wait</string>", "<string>/state</string>",
		"<key>StandardErrorPath</key>\n\t<string>" + s.DaemonLog() + "</string>", platform.ServiceMarker,
	} {
		if !strings.Contains(string(plist), key) {
			t.Fatalf("system plist lacks %q:\n%s", key, plist)
		}
	}
	if info, err := os.Stat(filepath.Dir(s.DaemonLog())); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("log directory: %v %v", info, err)
	}
}

// A failure at any step leaves no system plist and the agent as it was recorded.
func TestSystemTransitionRollback(t *testing.T) {
	agent := "gui/501/" + label
	steps := []struct {
		name, fail string
		skip       int
		stale      bool
		down       bool
	}{
		{"disable_agent", "disable " + agent, 0, false, false},
		{"bootout_agent", "bootout " + agent, 0, false, false},
		{"print_disabled", "print-disabled", 1, false, false},
		{"verify_agent", "print " + agent, 1, false, false},
		{"install_plist", "sudo install", 0, false, false},
		{"bootstrap", "bootstrap system", 0, false, false},
		{"kickstart", "kickstart", 0, false, false},
		{"verify_owner", "", 0, false, true},
		{"verify_owner", "", 0, true, false},
	}
	starts := []struct{ loaded, disabled bool }{{true, false}, {false, true}, {false, false}, {true, true}}
	for _, step := range steps {
		for _, start := range starts {
			s, f := systemServices(t)
			f.AgentLoaded, f.AgentDisabled = start.loaded, start.disabled
			f.Fail, f.FailSkip, f.Stale, f.StatusDown = step.fail, step.skip, step.stale, step.down
			err := installSystem(s, f)
			if err == nil || !strings.Contains(err.Error(), "system_step_failed: "+step.name) || !strings.Contains(err.Error(), "rolled back") {
				t.Fatalf("%s %+v: %v", step.name, start, err)
			}
			if _, statErr := os.Stat(s.SystemArtifact()); !os.IsNotExist(statErr) || f.SystemLoaded {
				t.Fatalf("%s %+v: system service left: %v %v", step.name, start, statErr, f.SystemLoaded)
			}
			if f.AgentLoaded != start.loaded || f.AgentDisabled != start.disabled {
				t.Fatalf("%s %+v: agent loaded=%v disabled=%v", step.name, start, f.AgentLoaded, f.AgentDisabled)
			}
			if _, statErr := os.Stat(s.DaemonArtifact()); statErr != nil {
				t.Fatalf("%s: the agent plist was removed: %v", step.name, statErr)
			}
			for _, call := range f.Calls {
				if strings.HasPrefix(call, "launchctl ") {
					t.Fatalf("%s: a launchctl call without sudo: %s", step.name, call)
				}
			}
		}
	}
	// A rollback that fails reports the commands that finish it.
	s, f := systemServices(t)
	f.Fail, f.Rollback = "kickstart", "enable "+agent
	err := installSystem(s, f)
	if err == nil || !strings.Contains(err.Error(), "rollback_failed") ||
		!strings.Contains(err.Error(), "sudo launchctl enable "+agent+"; sudo launchctl bootstrap gui/501 "+s.DaemonArtifact()) {
		t.Fatalf("failed rollback: %v", err)
	}
}

// An install interrupted after the plist was installed completes on the next run; a loaded
// service is not bootstrapped again; a re-enabled agent is stopped again and never written.
func TestSystemReconciliation(t *testing.T) {
	s, f := systemServices(t)
	if err := os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", "/state"), 0644); err != nil {
		t.Fatal(err)
	}
	f.AgentDisabled, f.AgentLoaded = true, false
	if err := installSystem(s, f); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls(f, s), "\n")
	if !strings.Contains(got, "sudo launchctl bootstrap system SYS\nsudo launchctl kickstart -k system/"+label) || strings.Contains(got, "sudo install") {
		t.Fatalf("interrupted install:\n%s", got)
	}
	// Loaded, unchanged, and the agent enabled and loaded again since then.
	f.Calls, f.AgentDisabled, f.AgentLoaded = nil, false, true
	os.WriteFile(s.DaemonArtifact(), s.RenderDaemon("/p/bin/koinon", "/state"), 0644)
	if err := installSystem(s, f); err != nil {
		t.Fatal(err)
	}
	got = strings.Join(calls(f, s), "\n")
	if strings.Contains(got, "bootstrap") || strings.Contains(got, "sudo install") || strings.Contains(got, "enable") ||
		!strings.Contains(got, "sudo launchctl disable gui/501/"+label) || !strings.Contains(got, "sudo launchctl kickstart -k system/"+label) {
		t.Fatalf("reconciliation:\n%s", got)
	}
	if f.AgentLoaded || !f.AgentDisabled {
		t.Fatalf("agent %+v", f)
	}
	// A changed plist takes effect through a new bootstrap.
	f.Calls = nil
	if err := s.InstallSystem(context.Background(), platform.SystemInstall{Binary: "/p/bin/koinon", State: "/other",
		Plist: platform.SystemPlist{Present: true, Marked: true}, StatusPID: f.StatusPID, Wait: 100 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	got = strings.Join(calls(f, s), "\n")
	if !strings.Contains(got, "sudo launchctl bootout system/"+label) || !strings.Contains(got, "sudo install -o root -g wheel -m 0644 TMP SYS\nsudo launchctl bootstrap system SYS") {
		t.Fatalf("changed plist:\n%s", got)
	}
}

// A hand-written LaunchDaemon is system mode when it runs this binary, state and user.
func TestSystemHandWritten(t *testing.T) {
	s, f := systemServices(t)
	if err := os.WriteFile(s.SystemArtifact(), []byte("hand-written"), 0644); err != nil {
		t.Fatal(err)
	}
	f.SystemLoaded = true
	f.PlistJSON = `{"Label":"` + label + `","ProgramArguments":["/p/bin/koinon","serve","--state-dir","/state"],"UserName":"alice","KeepAlive":true}`
	p, err := s.ReadSystem(context.Background())
	if err != nil || !p.Present || p.Marked || s.CheckSystem(p, "/p/bin/koinon", "/state") != nil {
		t.Fatalf("matching plist: %+v %v %v", p, err, s.CheckSystem(p, "/p/bin/koinon", "/state"))
	}
	if err := installSystem(s, f); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(s.SystemArtifact()); string(data) != "hand-written" {
		t.Fatal("the hand-written plist was rewritten")
	}
	if got := strings.Join(calls(f, s), "\n"); strings.Contains(got, "sudo install") || !strings.Contains(got, "kickstart -k system/"+label) {
		t.Fatalf("calls:\n%s", got)
	}
	for field, plist := range map[string]string{
		"ProgramArguments": `{"Label":"` + label + `","ProgramArguments":["/other/koinon","serve","--state-dir","/state"],"UserName":"alice"}`,
		"state directory":  `{"Label":"` + label + `","ProgramArguments":["/p/bin/koinon","serve","--state-dir","/elsewhere"],"UserName":"alice"}`,
		"UserName":         `{"Label":"` + label + `","ProgramArguments":["/p/bin/koinon","serve","--state-dir","/state","--lock-wait","60s"],"UserName":"bob"}`,
		"Label":            `{"Label":"other.service","ProgramArguments":["/p/bin/koinon","serve","--state-dir","/state"],"UserName":"alice"}`,
	} {
		f.PlistJSON = plist
		p, _ := s.ReadSystem(context.Background())
		if err := s.CheckSystem(p, "/p/bin/koinon", "/state"); err == nil ||
			!strings.HasPrefix(err.Error(), "system_plist_mismatch: ") || !strings.Contains(err.Error(), "("+field+")") {
			t.Fatalf("%s: %v", field, err)
		}
	}
}

// Uninstall removes an owned LaunchDaemon through sudo and refuses an unowned one.
func TestRemoveSystem(t *testing.T) {
	s, f := systemServices(t)
	if removed, err := s.RemoveSystem(context.Background()); removed || err != nil {
		t.Fatalf("absent: %v %v", removed, err)
	}
	os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", "/state"), 0644)
	f.SystemLoaded = true
	if removed, err := s.RemoveSystem(context.Background()); !removed || err != nil {
		t.Fatalf("owned: %v %v", removed, err)
	}
	if _, err := os.Stat(s.SystemArtifact()); !os.IsNotExist(err) || f.SystemLoaded {
		t.Fatalf("left: %v %v", err, f.SystemLoaded)
	}
	if got := strings.Join(calls(f, s), "\n"); !strings.Contains(got, "sudo launchctl bootout system/"+label) || !strings.Contains(got, "sudo rm -f SYS") {
		t.Fatalf("calls:\n%s", got)
	}
	os.WriteFile(s.SystemArtifact(), []byte("hand-written"), 0644)
	if removed, err := s.RemoveSystem(context.Background()); removed || err == nil ||
		!strings.Contains(err.Error(), "service_artifact_unowned") || !strings.Contains(err.Error(), "sudo launchctl bootout system/"+label) {
		t.Fatalf("unowned: %v %v", removed, err)
	}
	if _, err := os.Stat(s.SystemArtifact()); err != nil {
		t.Fatal("the unowned plist was removed")
	}
}

// The system plist is global: a marked LaunchDaemon of another user is never taken over or
// removed (#283 chunk 02 review).
func TestSystemOtherUser(t *testing.T) {
	s, f := systemServices(t)
	bob := s
	bob.User = "bob"
	if err := os.WriteFile(s.SystemArtifact(), bob.RenderSystemDaemon("/b/bin/koinon", "/bob-state"), 0644); err != nil {
		t.Fatal(err)
	}
	f.SystemLoaded = true
	p, err := s.ReadSystem(context.Background())
	if err != nil || !p.Marked || p.User != "bob" || p.Label != label {
		t.Fatalf("read %+v %v", p, err)
	}
	if err := s.CheckSystem(p, "/p/bin/koinon", "/state"); err == nil || !strings.Contains(err.Error(), "(UserName)") {
		t.Fatalf("another user's daemon: %v", err)
	}
	f.Calls = nil
	if removed, err := s.RemoveSystem(context.Background()); removed || err == nil || !strings.Contains(err.Error(), "(UserName)") {
		t.Fatalf("removal of another user's daemon: %v %v", removed, err)
	}
	for _, call := range f.Calls {
		if strings.HasPrefix(call, "sudo ") {
			t.Fatalf("%s ran", call)
		}
	}
	if data, _ := os.ReadFile(s.SystemArtifact()); !strings.Contains(string(data), "<string>bob</string>") || !f.SystemLoaded {
		t.Fatal("another user's daemon changed")
	}
	// The own marked plist passes.
	os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", "/state"), 0644)
	if p, _ := s.ReadSystem(context.Background()); s.CheckSystem(p, "/p/bin/koinon", "/state") != nil {
		t.Fatalf("own plist refused: %+v", p)
	}
}

// Without a console login the user has no gui/<uid> domain and launchctl refuses every command
// on it (exit 112, observed on the macOS runner). The transition and later installs proceed,
// remove the owned agent plist and refuse one that Koinon did not write (#283 review).
func TestSystemWithoutGUIDomain(t *testing.T) {
	s, f := systemServices(t)
	f.NoDomain, f.AgentLoaded = true, false
	if err := installSystem(s, f); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls(f, s), "\n")
	if strings.Contains(got, "disable") || strings.Contains(got, "bootout") || !strings.Contains(got, "sudo launchctl bootstrap system SYS") {
		t.Fatalf("calls:\n%s", got)
	}
	if _, err := os.Stat(s.DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatalf("the owned agent plist stayed: %v", err)
	}
	// An upgrade of a running system service after a boot without a desktop login.
	f.Calls = nil
	if err := installSystem(s, f); err != nil {
		t.Fatalf("upgrade without a gui domain: %v", err)
	}
	// After a desktop logout launchctl answers error 125 instead (#344): the same upgrade.
	f.Calls, f.LoggedOut = nil, true
	if err := installSystem(s, f); err != nil {
		t.Fatalf("upgrade after a desktop logout: %v", err)
	}
	if got := strings.Join(calls(f, s), "\n"); strings.Contains(got, "disable") || strings.Contains(got, "bootout") || !strings.Contains(got, "kickstart") {
		t.Fatalf("calls after a logout:\n%s", got)
	}
	// A first transition after a desktop logout skips the agent steps too.
	s5, f5 := systemServices(t)
	f5.NoDomain, f5.LoggedOut, f5.AgentLoaded = true, true, false
	if err := installSystem(s5, f5); err != nil || !f5.SystemLoaded {
		t.Fatalf("transition after a desktop logout: %v", err)
	}
	// A failure rolls back the system service and touches no agent.
	s2, f2 := systemServices(t)
	f2.NoDomain, f2.AgentLoaded, f2.Fail = true, false, "kickstart"
	if err := installSystem(s2, f2); err == nil || !strings.Contains(err.Error(), "rolled back") || f2.SystemLoaded {
		t.Fatalf("rollback without a gui domain: %v", err)
	}
	if _, err := os.Stat(s2.SystemArtifact()); !os.IsNotExist(err) {
		t.Fatal("system plist left")
	}
	// An agent plist that Koinon did not write would start at the next login.
	s3, f3 := systemServices(t)
	f3.NoDomain, f3.AgentLoaded = true, false
	os.WriteFile(s3.DaemonArtifact(), []byte("hand-written agent"), 0644)
	if err := installSystem(s3, f3); err == nil || !strings.Contains(err.Error(), "system_step_failed: verify_agent") {
		t.Fatalf("unowned agent plist: %v", err)
	}
	if _, err := os.Stat(s3.SystemArtifact()); !os.IsNotExist(err) {
		t.Fatal("system plist installed beside an unowned agent")
	}
	// A failure to read the domain that is not its absence stops before any change.
	s4, f4 := systemServices(t)
	f4.Fail = "print gui/501"
	if err := installSystem(s4, f4); err == nil || !strings.Contains(err.Error(), "system_step_failed: observe_agent") {
		t.Fatalf("unreadable domain: %v", err)
	}
}

// Restart names the service mode and its restart command and runs no sudo (#283 chunk 03).
func TestRestartCommand(t *testing.T) {
	s, f := systemServices(t)
	if mode, command, note := s.Restart(context.Background()); mode != "agent" ||
		command != "launchctl kickstart -k gui/501/"+label || !strings.Contains(note, "desktop login session") {
		t.Fatalf("agent: %s %q %q", mode, command, note)
	}
	os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", "/state"), 0644)
	if mode, command, _ := s.Restart(context.Background()); mode != "system" || command != "sudo launchctl kickstart -k system/"+label {
		t.Fatalf("system: %s %q", mode, command)
	}
	bob := s
	bob.User = "bob"
	os.WriteFile(s.SystemArtifact(), bob.RenderSystemDaemon("/b/bin/koinon", "/bob"), 0644)
	os.Remove(s.DaemonArtifact())
	if mode, command, _ := s.Restart(context.Background()); mode != "manual" || command != "" {
		t.Fatalf("another user's daemon: %s %q", mode, command)
	}
	for _, call := range f.Calls {
		if strings.HasPrefix(call, "sudo ") || strings.HasPrefix(call, "launchctl ") {
			t.Fatalf("Restart ran %s", call)
		}
	}
	l := platform.Services{Backend: "systemd", Home: t.TempDir(), Run: f.Run}
	if mode, _, _ := l.Restart(context.Background()); mode != "manual" {
		t.Fatalf("linux without a unit: %s", mode)
	}
	os.MkdirAll(filepath.Dir(l.DaemonArtifact()), 0755)
	os.WriteFile(l.DaemonArtifact(), l.RenderDaemon("/p/bin/koinon", "/state"), 0644)
	if mode, command, _ := l.Restart(context.Background()); mode != "systemd" || command != "systemctl --user restart koinon.service" {
		t.Fatalf("systemd: %s %q", mode, command)
	}
}
