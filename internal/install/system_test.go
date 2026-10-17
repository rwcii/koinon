package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/platform/launchdtest"
)

// systemEnv is a launchd host with the modeled launchd of #283 and an earlier plain install.
func systemEnv(t *testing.T) (*env, *launchdtest.Fake) {
	t.Helper()
	e := newEnv(t, "launchd")
	f := launchdtest.New(501, platform.DaemonLabel)
	e.o.Services.Run = f.Run
	e.o.Services.StopWait = 50 * time.Millisecond
	e.o.SystemWait = 100 * time.Millisecond
	e.o.Status = func(ctx context.Context, _, _ string) (json.RawMessage, error) {
		pid, err := f.StatusPID(ctx)
		return json.RawMessage(fmt.Sprintf(`{"pid":%d}`, pid)), err
	}
	os.MkdirAll(e.o.Services.SystemDir, 0755)
	os.MkdirAll(filepath.Dir(e.o.Services.DaemonArtifact()), 0755)
	os.WriteFile(e.o.Services.DaemonArtifact(), e.o.Services.RenderDaemon(Binary(e.o.Prefix), e.o.StateDir), 0644)
	return e, f
}

// --system moves the daemon to a LaunchDaemon; later plain installs and the uninstall find
// system mode from its plist and never write or start the user agent (#283 chunk 02).
func TestInstallSystemMode(t *testing.T) {
	e, f := systemEnv(t)
	e.o.System = true
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Service != "system" || r.Artifact != e.o.Services.SystemArtifact() || r.BinaryResult != "replaced" {
		t.Fatalf("install %+v %v", r, err)
	}
	if !f.SystemLoaded || f.AgentLoaded || !f.AgentDisabled {
		t.Fatalf("state %+v", f)
	}
	if _, err := os.Stat(e.o.Services.DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatalf("agent plist left: %v", err)
	}
	// A plain install later: the agent re-enabled and started meanwhile is stopped again.
	e.o.System = false
	f.AgentDisabled, f.AgentLoaded, f.Calls = false, true, nil
	r, err = Install(context.Background(), e.o)
	if err != nil || r.Service != "system" || f.AgentLoaded || !f.AgentDisabled {
		t.Fatalf("plain install in system mode %+v %v %+v", r, err, f)
	}
	for _, call := range f.Calls {
		if strings.Contains(call, "enable gui") || strings.Contains(call, "bootstrap gui") || strings.HasPrefix(call, "launchctl ") {
			t.Fatalf("the user agent was touched: %s", call)
		}
	}
	if _, err := os.Stat(e.o.Services.DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatalf("agent plist written: %v", err)
	}
	u, err := Uninstall(context.Background(), e.o)
	if err != nil || u.Service != "removed" || u.Artifact != e.o.Services.SystemArtifact() || f.SystemLoaded {
		t.Fatalf("uninstall %+v %v", u, err)
	}
	if _, err := os.Stat(e.o.Services.SystemArtifact()); !os.IsNotExist(err) {
		t.Fatalf("system plist left: %v", err)
	}
	if !f.AgentDisabled {
		t.Fatal("uninstall enabled the agent")
	}
	if _, err := os.Stat(filepath.Join(e.o.StateDir, "secret")); err != nil {
		t.Fatalf("state removed: %v", err)
	}
}

// Refusals happen before anything changes.
func TestInstallSystemRefusals(t *testing.T) {
	linux := newEnv(t, "systemd")
	linux.o.System = true
	if _, err := Install(context.Background(), linux.o); err == nil || !strings.HasPrefix(err.Error(), "system_mode_unsupported: ") {
		t.Fatalf("linux: %v", err)
	}
	for name, change := range map[string]func(*env, *launchdtest.Fake){
		"system_mode_root":     func(e *env, _ *launchdtest.Fake) { e.o.Services.UID = 0 },
		"system_mode_no_start": func(e *env, _ *launchdtest.Fake) { e.o.NoStart = true },
		"system_plist_mismatch: another user": func(e *env, f *launchdtest.Fake) {
			bob := e.o.Services
			bob.User = "bob"
			os.WriteFile(e.o.Services.SystemArtifact(), bob.RenderSystemDaemon("/b/bin/koinon", "/bob-state"), 0644)
		},
		"system_plist_mismatch: hand-written": func(e *env, f *launchdtest.Fake) {
			e.o.System = false
			os.WriteFile(e.o.Services.SystemArtifact(), []byte("hand-written"), 0644)
			f.PlistJSON = `{"Label":"` + platform.DaemonLabel + `","ProgramArguments":["/usr/local/bin/koinon","serve","--state-dir","` + e.o.StateDir + `"],"UserName":"alice"}`
		},
	} {
		e, f := systemEnv(t)
		e.o.System = true
		change(e, f)
		code, _, _ := strings.Cut(name, ": ")
		if _, err := Install(context.Background(), e.o); err == nil || !strings.HasPrefix(err.Error(), code+": ") {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(Binary(e.o.Prefix)); !os.IsNotExist(err) {
			t.Fatalf("%s: the binary was placed: %v", name, err)
		}
		for _, call := range f.Calls {
			if strings.HasPrefix(call, "sudo ") {
				t.Fatalf("%s: %s ran", name, call)
			}
		}
	}
	// A matching hand-written LaunchDaemon is system mode, restarted and never rewritten.
	e, f := systemEnv(t)
	os.WriteFile(e.o.Services.SystemArtifact(), []byte("hand-written"), 0644)
	f.SystemLoaded = true
	f.PlistJSON = `{"Label":"` + platform.DaemonLabel + `","ProgramArguments":["` + Binary(e.o.Prefix) + `","serve","--state-dir","` + e.o.StateDir + `"],"UserName":"alice"}`
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Service != "system" {
		t.Fatalf("hand-written %+v %v", r, err)
	}
	if data, _ := os.ReadFile(e.o.Services.SystemArtifact()); string(data) != "hand-written" {
		t.Fatal("the hand-written plist was rewritten")
	}
}

// A system-mode daemon keeps a copy that brew cannot replace, also over an earlier
// user-mode link to a Homebrew keg (#397).
func TestInstallSystemModeCopiesAHomebrewKeg(t *testing.T) {
	e, _ := systemEnv(t)
	e.o.Source = homebrewKeg(t, filepath.Join(t.TempDir(), "brew"), "0.3.1", "release 0.3.1")
	r, err := Install(context.Background(), e.o)
	if err != nil || r.BinaryResult != "linked" {
		t.Fatalf("user-mode install %+v %v", r, err)
	}
	e.o.System = true
	r, err = Install(context.Background(), e.o)
	if err != nil || r.Service != "system" || r.BinaryResult != "replaced" {
		t.Fatalf("system install %+v %v", r, err)
	}
	if info, err := os.Lstat(r.Binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("system binary %v %v", info, err)
	}
	if data, _ := os.ReadFile(r.Binary); string(data) != "release 0.3.1" {
		t.Fatalf("system binary %q", data)
	}
	e.o.System = false
	if r, err = Install(context.Background(), e.o); err != nil || r.Service != "system" || r.BinaryResult != "unchanged" {
		t.Fatalf("plain install in system mode %+v %v", r, err)
	}
}
