//go:build native

package nativetest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// TestSystemMode runs `koinon install --system` on the real launchd with the runner's
// passwordless sudo (#283 chunk 02). The runner's own launchd session is Aqua, so the system
// steps run over ssh localhost in a Background session, as from SSH or tmux on a Mac.
func TestSystemMode(t *testing.T) {
	if services(t).Backend != "launchd" {
		t.Skip("system mode is macOS only")
	}
	if os.Getenv("KOINON_SYSTEM_INNER") == "1" {
		systemModeInner(t, os.Getenv("KOINON_SYSTEM_PREFIX"), os.Getenv("KOINON_SYSTEM_STATE"))
		return
	}
	dir := tempDir(t)
	prefix, state := filepath.Join(dir, "prefix"), filepath.Join(dir, "state")
	// The plain install that system mode replaces.
	if report := must(t, "install", "--prefix", prefix, "--state-dir", state); report["service"] != "running" {
		t.Fatalf("plain install %v", report)
	}
	s := services(t)
	defer func() {
		// Leave the runner as the other native tests expect it.
		exec.Command("sudo", "-n", "launchctl", "bootout", "system/"+platform.DaemonLabel).Run()
		exec.Command("sudo", "-n", "rm", "-f", s.SystemArtifact()).Run()
		exec.Command("launchctl", "enable", "gui/"+strconv.Itoa(s.UID)+"/"+platform.DaemonLabel).Run()
		koinon(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	}()
	overSSH(t, fmt.Sprintf("KOINON_SYSTEM_INNER=1 KOINON_SYSTEM_PREFIX=%s KOINON_SYSTEM_STATE=%s", shellQuote(prefix), shellQuote(state)),
		"^TestSystemMode$")
}

// overSSH runs one test of this package again over ssh localhost, with Remote Login and a
// generated key on the disposable runner.
func overSSH(t *testing.T, env, run string) {
	t.Helper()
	sh := func(name string, args ...string) error {
		out, err := exec.Command(name, args...).CombinedOutput()
		t.Logf("$ %s %s\n%s(err=%v)", name, strings.Join(args, " "), out, err)
		return err
	}
	sh("sudo", "-n", "launchctl", "enable", "system/com.openssh.sshd")
	if err := sh("sudo", "-n", "launchctl", "bootstrap", "system", "/System/Library/LaunchDaemons/ssh.plist"); err != nil {
		sh("sudo", "-n", "systemsetup", "-f", "-setremotelogin", "on")
	}
	key := filepath.Join(t.TempDir(), "id")
	if err := sh("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	ssh := filepath.Join(home, ".ssh")
	// sshd's StrictModes refuses keys under a writable .ssh; the runner's is 0777.
	os.MkdirAll(ssh, 0700)
	os.Chmod(ssh, 0700)
	f, err := os.OpenFile(filepath.Join(ssh, "authorized_keys"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(pub)
	f.Close()
	test, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// TestMain builds ../../cmd/koinon with go: the package directory and this PATH.
	command := fmt.Sprintf("cd %s && PATH=%s KOINON_NATIVE_JOB=1 %s %s -test.run '%s' -test.v -test.count=1",
		shellQuote(pkg), shellQuote(os.Getenv("PATH")), env, shellQuote(test), run)
	if err := sh("ssh", "-i", key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=20", "localhost", command); err != nil {
		t.Fatalf("the test over ssh failed: %v", err)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func systemModeInner(t *testing.T, prefix, state string) {
	s := services(t)
	sh := func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		t.Logf("$ %s %s\n%s(err=%v)", name, strings.Join(args, " "), out, err)
		return string(out), err
	}
	if out, _ := sh("launchctl", "managername"); strings.TrimSpace(out) != "Background" {
		t.Fatalf("the session is %q, not Background", strings.TrimSpace(out))
	}
	agent := "gui/" + strconv.Itoa(s.UID) + "/" + platform.DaemonLabel
	systemPID := func() int {
		out, _ := sh("sudo", "-n", "launchctl", "print", "system/"+platform.DaemonLabel)
		if m := regexp.MustCompile(`(?m)^\s*pid = (\d+)`).FindStringSubmatch(out); m != nil {
			pid, _ := strconv.Atoi(m[1])
			return pid
		}
		return 0
	}
	agentStopped := func() {
		t.Helper()
		out, err := sh("sudo", "-n", "launchctl", "print-disabled", "gui/"+strconv.Itoa(s.UID))
		if err != nil || !regexp.MustCompile(`"`+regexp.QuoteMeta(platform.DaemonLabel)+`"\s*=>\s*(disabled|true)`).MatchString(out) {
			t.Fatalf("the agent is not disabled: %v", err)
		}
		if _, err := sh("sudo", "-n", "launchctl", "print", agent); err == nil {
			t.Fatal("the agent is still loaded")
		}
	}
	daemons := func() int {
		out, _ := sh("pgrep", "-f", "serve --state-dir "+state)
		return len(strings.Fields(out))
	}
	report := must(t, "install", "--prefix", prefix, "--state-dir", state, "--system")
	if report["service"] != "system" || report["artifact"] != s.SystemArtifact() {
		t.Fatalf("system install %v", report)
	}
	pid := systemPID()
	if out, _ := sh("ps", "-o", "user=", "-p", strconv.Itoa(pid)); pid == 0 || strings.TrimSpace(out) != s.User {
		t.Fatalf("the LaunchDaemon (pid %d) runs as %q, not %q", pid, strings.TrimSpace(out), s.User)
	}
	agentStopped()
	if st := must(t, "status", "--state-dir", state); int(st["pid"].(float64)) != pid {
		t.Fatalf("status pid %v, system pid %d", st["pid"], pid)
	}
	// A stray agent started again is stopped by the next install; one daemon remains.
	if err := os.WriteFile(s.DaemonArtifact(), s.RenderDaemon(filepath.Join(prefix, "bin", "koinon"), state), 0644); err != nil {
		t.Fatal(err)
	}
	sh("sudo", "-n", "launchctl", "enable", agent)
	if _, err := sh("sudo", "-n", "launchctl", "bootstrap", "gui/"+strconv.Itoa(s.UID), s.DaemonArtifact()); err != nil {
		t.Fatalf("stray agent: %v", err)
	}
	if report := must(t, "install", "--prefix", prefix, "--state-dir", state); report["service"] != "system" {
		t.Fatalf("second install %v", report)
	}
	agentStopped()
	waitFor(t, "one daemon", func() bool { return daemons() == 1 })
	if st := must(t, "status", "--state-dir", state); int(st["pid"].(float64)) != systemPID() {
		t.Fatalf("status pid %v after the second install", st["pid"])
	}
	log, err := os.ReadFile(s.DaemonLog())
	if err != nil || !bytes.Contains(log, []byte(`"event":"start"`)) || !bytes.Contains(log, []byte(`"event":"stop"`)) {
		t.Fatalf("daemon log without a start and a stop: %v\n%s", err, log)
	}
	if info, err := os.Stat(s.DaemonLog()); err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(s.UID) {
		t.Fatalf("the daemon log is not owned by the user: %v", err)
	}
	if report := must(t, "uninstall", "--prefix", prefix, "--state-dir", state); report["service"] != "removed" {
		t.Fatalf("uninstall %v", report)
	}
	if _, err := os.Stat(s.SystemArtifact()); !os.IsNotExist(err) {
		t.Fatalf("system plist left: %v", err)
	}
	if status(state) {
		t.Fatal("the daemon still answers after uninstall")
	}
}
