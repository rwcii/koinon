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
	f.PlistJSON = `{"ProgramArguments":["/p/bin/koinon","serve","--state-dir","/state"],"UserName":"alice","KeepAlive":true}`
	p, err := s.ReadSystem(context.Background())
	if err != nil || !p.Present || p.Marked || s.CheckHandWritten(p, "/p/bin/koinon", "/state") != nil {
		t.Fatalf("matching plist: %+v %v %v", p, err, s.CheckHandWritten(p, "/p/bin/koinon", "/state"))
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
		"ProgramArguments": `{"ProgramArguments":["/other/koinon","serve","--state-dir","/state"],"UserName":"alice"}`,
		"state directory":  `{"ProgramArguments":["/p/bin/koinon","serve","--state-dir","/elsewhere"],"UserName":"alice"}`,
		"UserName":         `{"ProgramArguments":["/p/bin/koinon","serve","--state-dir","/state","--lock-wait","60s"],"UserName":"bob"}`,
	} {
		f.PlistJSON = plist
		p, _ := s.ReadSystem(context.Background())
		if err := s.CheckHandWritten(p, "/p/bin/koinon", "/state"); err == nil ||
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
