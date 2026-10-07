//go:build native

// Package nativetest runs the Go daemon's installation, upgrade and removal under the
// host's real user service manager: systemd on Ubuntu, launchd on macOS (sprint chunk
// 11, workflow native-go-lifecycle.yml). It changes the account's services, so it runs
// only on a disposable CI runner, with KOINON_NATIVE_JOB=1 and `go test -tags native`.
// The upgrade starts from the pinned main release, taken by `git archive` from history
// (never from this checkout's Python code), and from the committed fixture trees.
package nativetest

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
)

// baseline is the main release that the upgrade starts from.
const baseline = "3d60c73c88f3110ca4bc91a9a0cc7e16f7daf827"

var binary string

func TestMain(m *testing.M) {
	if os.Getenv("KOINON_NATIVE_JOB") != "1" {
		fmt.Println("skipped: the native tests change this account's services; set KOINON_NATIVE_JOB=1 on a disposable runner")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "koinon-native-bin-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "koinon")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/koinon")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func koinon(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var result map[string]any
	json.Unmarshal(stdout.Bytes(), &result)
	if err != nil {
		return result, fmt.Errorf("koinon %s: %v\n%s%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return result, nil
}

func must(t *testing.T, args ...string) map[string]any {
	t.Helper()
	result, err := koinon(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func services(t *testing.T) platform.Services {
	t.Helper()
	s, err := platform.NewServices()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// daemonPID is the running daemon's process ID under the manager, or 0.
func daemonPID(t *testing.T) int {
	t.Helper()
	s := services(t)
	if s.Backend == "launchd" {
		out, _ := exec.Command("launchctl", "print", "gui/"+strconv.Itoa(s.UID)+"/"+platform.DaemonLabel).CombinedOutput()
		if m := regexp.MustCompile(`(?m)^\s*pid = (\d+)`).FindSubmatch(out); m != nil {
			pid, _ := strconv.Atoi(string(m[1]))
			return pid
		}
		return 0
	}
	out, _ := exec.Command("systemctl", "--user", "show", "-p", "MainPID", "--value", platform.DaemonUnit).Output()
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return pid
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func status(state string) bool {
	secret, err := core.ReadSecret(state)
	if err != nil {
		return false
	}
	_, err = core.GetStatus(context.Background(), "127.0.0.1:47671", secret)
	return err == nil
}

func call(t *testing.T, state, path string, body any) map[string]any {
	t.Helper()
	secret, err := core.ReadSecret(state)
	if err != nil {
		t.Fatal(err)
	}
	data, err := core.Call(context.Background(), "127.0.0.1:47671", secret, path, body)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	return result
}

func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "fixture"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	common, err := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	return common
}

// TestGoLifecycle installs, starts, kills, uninstalls and reinstalls the daemon; the
// state survives the uninstall.
func TestGoLifecycle(t *testing.T) {
	dir := t.TempDir()
	prefix, state := filepath.Join(dir, "prefix space"), filepath.Join(dir, "state")
	report := must(t, "install", "--prefix", prefix, "--state-dir", state)
	if report["service"] != "running" {
		t.Fatalf("install %v", report)
	}
	defer koinon(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	repo := filepath.Join(dir, "repo")
	gitRepo(t, repo)
	must(t, "register", "--as", "deepseek:native-lifecycle", "--repository", repo, "--directory", repo, "--dsh-url", "http://127.0.0.1:9",
		"--dsh-credentials", filepath.Join(dir, "none"), "--state-dir", state)
	before := daemonPID(t)
	if before == 0 {
		t.Fatal("no daemon process under the manager")
	}
	if err := exec.Command("kill", "-KILL", strconv.Itoa(before)).Run(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manager to restart the daemon", func() bool {
		pid := daemonPID(t)
		return pid != 0 && pid != before && status(state)
	})
	must(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	if _, err := os.Stat(services(t).DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatalf("artifact left after uninstall: %v", err)
	}
	if status(state) {
		t.Fatal("the daemon still answers after uninstall")
	}
	if _, err := os.Stat(filepath.Join(state, "state.sqlite3")); err != nil {
		t.Fatalf("uninstall removed the state: %v", err)
	}
	must(t, "install", "--prefix", prefix, "--state-dir", state)
	sessions := call(t, state, "/v1/status", map[string]any{})["sessions"].(map[string]any)
	if sessions["total"] != float64(1) {
		t.Fatalf("state lost across reinstall: %v", sessions)
	}
}

// extract writes the pinned main release from Git history into dir.
func extract(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "-C", "../..", "archive", baseline).Output()
	if err != nil {
		t.Fatalf("git archive %s (the workflow needs fetch-depth 0): %v", baseline, err)
	}
	r := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, h.Name)
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0700)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0700)
			data, _ := io.ReadAll(r)
			os.WriteFile(target, data, os.FileMode(h.Mode)&0700)
		}
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

type pythonRelease struct {
	t                   *testing.T
	source, prefix      string
	state, repo, claude string
	env                 []string
}

func (p pythonRelease) run(args ...string) (string, error) {
	cmd := exec.Command("python3", args...)
	cmd.Env = p.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (p pythonRelease) must(args ...string) string {
	p.t.Helper()
	out, err := p.run(args...)
	if err != nil {
		p.t.Fatalf("python3 %v: %v\n%s", args, err, out)
	}
	return out
}

// TestUpgradeFromMain installs the main release with a running memory service, adds the
// committed fixture inboxes and stores, and upgrades: a racing Python start is refused
// by the marker, a failure before the swap restores Python, a failure after it resumes,
// and the imported state reads back through the Go daemon.
func TestUpgradeFromMain(t *testing.T) {
	dir := t.TempDir()
	p := pythonRelease{t: t, source: filepath.Join(dir, "main-release"), prefix: filepath.Join(dir, "python prefix"),
		state: filepath.Join(dir, "python-state"), repo: filepath.Join(dir, "repo"), claude: filepath.Join(dir, "claude")}
	extract(t, p.source)
	gitRepo(t, p.repo)
	os.MkdirAll(p.claude, 0700)
	p.env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+p.claude, "PYTHONDONTWRITEBYTECODE=1")
	backend := services(t).Backend
	p.must(filepath.Join(p.source, "scripts", "install.py"), "--configure-memory", "--repo", p.repo, "--prefix", p.prefix,
		"--state-dir", p.state, "--unit-dir", filepath.Join(dir, "legacy-units"), "--service-backend", backend)
	defer p.run(filepath.Join(p.prefix, "scripts", "uninstall.py"), "--prefix", p.prefix)
	p.must(filepath.Join(p.prefix, "memory.py"), "--state-dir", p.state, "--repo-path", p.repo, "--consumer", "native-fixture",
		"note", "A known entry written by the main release.")
	// The committed fixture inboxes and stores join the installation's state.
	fixture := filepath.Join(dir, "fixture")
	copyTree(t, "../importer/testdata/python-state", fixture)
	for _, part := range []string{"sessions", "memory"} {
		entries, _ := os.ReadDir(filepath.Join(fixture, "state", part))
		for _, e := range entries {
			copyTree(t, filepath.Join(fixture, "state", part, e.Name()), filepath.Join(p.state, part, e.Name()))
		}
	}
	var m struct {
		Repositories map[string]struct{ Path, Key string } `json:"repositories"`
		Threads      map[string]string                     `json:"threads"`
		Stores       map[string]map[string]any             `json:"stores"`
	}
	data, _ := os.ReadFile(filepath.Join(fixture, "fixture.json"))
	json.Unmarshal(data, &m)
	second := gitRepo(t, filepath.Join(dir, "second"))
	goPrefix, goState := filepath.Join(dir, "go prefix"), filepath.Join(dir, "go-state")
	args := []string{"upgrade", "--from-python", "--python-prefix", p.prefix, "--prefix", goPrefix, "--state-dir", goState}
	good := append(append([]string{}, args...), "--repository", m.Repositories["a"].Key+"="+second,
		"--repository", m.Repositories["b"].Key+"="+m.Repositories["b"].Path)
	defer koinon(t, "uninstall", "--prefix", goPrefix, "--state-dir", goState)

	original, _ := os.ReadFile(filepath.Join(p.prefix, "install.json"))
	// The marker makes the main release's own commands refuse.
	operation := filepath.Join(dir, "probe-operation")
	if err := legacy.PublishMarker(p.prefix, operation, strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	if out, err := p.run(filepath.Join(p.prefix, "memory_service.py"), "ensure", "--prefix", p.prefix, "--repo", p.repo); err == nil {
		t.Fatalf("memory-service ensure ran under the marker:\n%s", out)
	}
	if out, err := p.run(filepath.Join(p.source, "scripts", "install.py"), "--configure-memory", "--repo", second, "--prefix", p.prefix,
		"--state-dir", p.state, "--service-backend", backend); err == nil {
		t.Fatalf("install ran under the marker:\n%s", out)
	}
	if err := legacy.RestoreRaw(p.prefix, operation, original); err != nil {
		t.Fatal(err)
	}

	// A failure before the swap: store b resolves to store a's repository.
	conflict := append(append([]string{}, args...), "--repository", m.Repositories["a"].Key+"="+second,
		"--repository", m.Repositories["b"].Key+"="+second)
	if _, err := koinon(t, conflict...); err == nil || !strings.Contains(err.Error(), "source_conflict") {
		t.Fatalf("conflicting upgrade: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(p.prefix, "install.json")); !bytes.Equal(after, original) {
		t.Fatal("install.json was not restored after the failed attempt")
	}
	memory := p.must(filepath.Join(p.prefix, "memory_service.py"), "status", "--prefix", p.prefix, "--repo", p.repo)
	if !strings.Contains(memory, `"running": true`) {
		t.Fatalf("the memory service did not restart: %s", memory)
	}
	p.must(filepath.Join(p.prefix, "memory.py"), "--state-dir", p.state, "--repo-path", p.repo, "--consumer", "native-fixture",
		"note", "Written after the restore.")

	// A failure after the swap: uninstall.py cannot run; the rerun resumes.
	if _, err := koinon(t, append(append([]string{}, good...), "--python", "/usr/bin/false")...); err == nil || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("interrupted upgrade: %v", err)
	}
	result := must(t, good...)
	if result["phase"] != "complete" {
		t.Fatalf("resumed upgrade %v", result)
	}
	if _, err := os.Stat(filepath.Join(p.prefix, "install.json")); !os.IsNotExist(err) {
		t.Fatalf("the Python installation remains: %v", err)
	}
	waitFor(t, "the Go daemon", func() bool { return status(goState) })

	// The imported state reads back through the daemon.
	reg := call(t, goState, "/v1/sessions/register", core.Registration{Family: "codex", ID: m.Threads["codex"], Repository: second, Directory: filepath.Dir(second)})
	if reg["ok"] != true {
		t.Fatalf("register %v", reg)
	}
	me := core.Key{Family: "codex", ID: m.Threads["codex"]}
	inbox := call(t, goState, "/v1/inbox/read", map[string]any{"caller": me, "after": 0, "limit": 10})
	if !strings.Contains(fmt.Sprint(inbox), "Fourth message, waiting.") {
		t.Fatalf("known message missing: %v", inbox)
	}
	work := call(t, goState, "/v1/work/work-get", map[string]any{"caller": me, "work_id": m.Stores["a"]["live_work"]})
	if !strings.Contains(fmt.Sprint(work), "#1: live claim") {
		t.Fatalf("known claim missing: %v", work)
	}
	must(t, "register", "--as", "deepseek:native-upgrade", "--repository", p.repo, "--directory", p.repo, "--dsh-url", "http://127.0.0.1:9",
		"--dsh-credentials", filepath.Join(dir, "none"), "--state-dir", goState)
	recall := must(t, "memory", "recall", "--as", "deepseek:native-upgrade", "--state-dir", goState, "known entry")
	if !strings.Contains(fmt.Sprint(recall), "A known entry written by the main release.") {
		t.Fatalf("known memory entry missing: %v", recall)
	}
	recall = must(t, "memory", "recall", "--as", "deepseek:native-upgrade", "--state-dir", goState, "after the restore")
	if !strings.Contains(fmt.Sprint(recall), "Written after the restore.") {
		t.Fatalf("an entry written after the restore was lost: %v", recall)
	}
	if runtime.GOOS == "darwin" && backend != "launchd" || runtime.GOOS == "linux" && backend != "systemd" {
		t.Fatalf("unexpected backend %s", backend)
	}
}
