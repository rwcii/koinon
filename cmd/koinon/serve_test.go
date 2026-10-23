package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// lockedBuffer collects lifecycle lines written from several daemons.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) events() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		var event map[string]any
		if line != "" && json.Unmarshal([]byte(line), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

func serveArgs(root string, extra ...string) []string {
	return append([]string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, extra...)
}

// Wake diagnostics share the lifecycle writer with shutdown; concurrent events must remain
// complete JSON lines even when the supplied writer is not itself synchronized.
func TestLifecycleLogSerializesCoreEvents(t *testing.T) {
	var out bytes.Buffer
	previous := daemonLog
	daemonLog = &out
	defer func() { daemonLog = previous }()
	logEvent := lifecycleLog()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logEvent("wake_fault", map[string]any{"class": "sqlite_error", "fault_at": int64(1000), "error_types": []string{"sqlite.Error"}, "sqlite_code": 1})
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 16 {
		t.Fatalf("log lines: %d", len(lines))
	}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["event"] != "wake_fault" || event["class"] != "sqlite_error" || event["at"] == "" {
			t.Fatalf("event: %s %v", line, err)
		}
	}
}

// The daemon logs its start, stop and lock contention; with --lock-wait a second daemon waits,
// then exits with status 0, and takes over when the lock frees within the wait (#283).
func TestServeLifecycleLogAndLockWait(t *testing.T) {
	log := &lockedBuffer{}
	daemonLog = log
	defer func() { daemonLog = os.Stderr }()
	root := filepath.Join(t.TempDir(), "state")
	first, stopFirst := context.WithCancel(context.Background())
	defer stopFirst()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- run(first, serveArgs(root), nil, output{ready}) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	// Without the flag, a second daemon fails at once, as before.
	var out bytes.Buffer
	if err := run(context.Background(), serveArgs(root), nil, &out); err == nil {
		t.Fatal("second daemon started")
	}
	// With it, the loser waits for the whole wait and exits cleanly.
	began := time.Now()
	if err := run(context.Background(), serveArgs(root, "--lock-wait", "3s"), nil, &out); err != nil {
		t.Fatalf("lock loser: %v", err)
	}
	if waited := time.Since(began); waited < 3*time.Second || waited > 8*time.Second {
		t.Fatalf("lock loser waited %v", waited)
	}
	// A loser whose wait outlasts the holder becomes the daemon.
	second, stopSecond := context.WithCancel(context.Background())
	defer stopSecond()
	secondReady := make(chan []byte, 1)
	secondDone := make(chan error, 1)
	go func() { secondDone <- run(second, serveArgs(root, "--lock-wait", "30s"), nil, output{secondReady}) }()
	time.Sleep(500 * time.Millisecond)
	stopFirst()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var body []byte
	select {
	case body = <-secondReady:
	case err := <-secondDone:
		t.Fatalf("waiting daemon: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("the waiting daemon did not take over")
	}
	var status struct {
		Listeners []string `json:"listeners"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", status.Listeners[0]}, nil, &out); err != nil ||
		!strings.Contains(out.String(), `"pid":`+strconv.Itoa(os.Getpid())) {
		t.Fatalf("status pid: %s %v", out.String(), err)
	}
	stopSecond()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	var sequence []string
	for _, event := range log.events() {
		name, _ := event["event"].(string)
		if reason, ok := event["reason"].(string); ok {
			name += ":" + reason
		}
		if _, ok := event["at"].(string); !ok {
			t.Fatalf("line without a time: %v", event)
		}
		sequence = append(sequence, name)
	}
	want := []string{"start", "stop:error", "lock_wait", "stop:lock_held", "lock_wait", "stop:canceled", "lock_acquired", "start", "stop:canceled"}
	if strings.Join(sequence, " ") != strings.Join(want, " ") {
		t.Fatalf("events %v, want %v", sequence, want)
	}
	secret, err := os.ReadFile(filepath.Join(root, "secret"))
	if err != nil || len(bytes.TrimSpace(secret)) == 0 {
		t.Fatalf("secret: %v", err)
	}
	if strings.Contains(log.buf.String(), strings.TrimSpace(string(secret))) {
		t.Fatal("the log holds the secret")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic write failure") }

// A daemon that cannot write its running report logs a stop with a fixed code (#283).
func TestServeReportFailureLogsStop(t *testing.T) {
	log := &lockedBuffer{}
	daemonLog = log
	defer func() { daemonLog = os.Stderr }()
	root := filepath.Join(t.TempDir(), "state")
	if err := run(context.Background(), serveArgs(root), nil, failingWriter{}); err == nil {
		t.Fatal("the report failure was not returned")
	}
	events := log.events()
	if len(events) != 2 || events[0]["event"] != "start" || events[1]["event"] != "stop" ||
		events[1]["reason"] != "error" || events[1]["code"] != "report_failed" {
		t.Fatalf("events %v", events)
	}
}

// The daemon writes the managed cycle's koinon-pickup skill at its start, from its own
// environment: a missing file is created and a different one replaced, so that a brew upgrade,
// which restarts the daemon, installs it (#406). The home and configuration roots are
// temporary.
func TestServeWritesPickupSkills(t *testing.T) {
	log := &lockedBuffer{}
	daemonLog = log
	defer func() { daemonLog = os.Stderr }()
	dir := t.TempDir()
	home, config := filepath.Join(dir, "home"), filepath.Join(dir, "claude-config")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	// OpenCode's configuration directory exists and agy's does not (#461).
	opencode := filepath.Join(dir, "xdg", "opencode")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	if err := os.MkdirAll(opencode, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := core.ClaudePickupSkillPath(config)
	codex := core.CodexPickupSkillPath(home)
	if err := os.MkdirAll(filepath.Dir(codex), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, []byte("an older entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The report failure returns right after the start.
	if err := run(context.Background(), serveArgs(filepath.Join(dir, "state")), nil, failingWriter{}); err == nil {
		t.Fatal("the report failure was not returned")
	}
	for _, path := range []string{claude, codex} {
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, core.PickupSkill()) {
			t.Fatalf("%s: %q %v", path, data, err)
		}
	}
	// The team skill entries go to the same places (#453).
	skills, err := core.TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		for _, path := range []string{core.ClaudeSkillPath(config, skill.Name), core.CodexSkillPath(home, skill.Name),
			filepath.Join(opencode, "skills", skill.Name, "SKILL.md")} {
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, skill.Content) {
				t.Fatalf("%s: %q %v", path, data, err)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(opencode, "skills", core.PickupSkillName, "SKILL.md")); err != nil || !bytes.Equal(data, core.PickupSkill()) {
		t.Fatalf("opencode pickup: %q %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("made agy's directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("wrote ~/.claude although CLAUDE_CONFIG_DIR is set: %v", err)
	}
	for _, event := range log.events() {
		if event["event"] == "pickup_skill" || event["event"] == "team_skill" {
			t.Fatalf("a successful write was logged as %v", event)
		}
	}
	// Without CLAUDE_CONFIG_DIR the Claude Code root is ~/.claude.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := run(context.Background(), serveArgs(filepath.Join(dir, "state")), nil, failingWriter{}); err == nil {
		t.Fatal("the report failure was not returned")
	}
	if data, err := os.ReadFile(core.ClaudePickupSkillPath(filepath.Join(home, ".claude"))); err != nil || !bytes.Equal(data, core.PickupSkill()) {
		t.Fatalf("~/.claude skill: %q %v", data, err)
	}
}

// A koinon-pickup skill that cannot be written is logged with a fixed code and does not stop
// the daemon; the other family's skill is still written (#406).
func TestServePickupSkillFailureKeepsServing(t *testing.T) {
	log := &lockedBuffer{}
	daemonLog = log
	defer func() { daemonLog = os.Stderr }()
	dir := t.TempDir()
	home, blocked := filepath.Join(dir, "home"), filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", blocked)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, serveArgs(filepath.Join(dir, "state")), nil, output{ready}) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	if data, err := os.ReadFile(core.CodexPickupSkillPath(home)); err != nil || !bytes.Equal(data, core.PickupSkill()) {
		t.Fatalf("codex skill: %q %v", data, err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	var failed []map[string]any
	for _, event := range log.events() {
		if event["event"] == "pickup_skill" {
			failed = append(failed, event)
		}
	}
	if len(failed) != 1 || failed[0]["family"] != "claude" || failed[0]["result"] != "failed" || failed[0]["code"] != "write_failed" ||
		failed[0]["path"] != core.ClaudePickupSkillPath(blocked) {
		t.Fatalf("logged %v", failed)
	}
}

// The daemon stops with an error, so that the service manager starts the new binary, when
// its launch path resolves to a new file: a brew upgrade's new keg or a renamed copy (#397).
// A missing file is not a change, and a relative launch path turns the check off.
func TestServeStopsWhenItsExecutableChanges(t *testing.T) {
	log := &lockedBuffer{}
	daemonLog = log
	defer func() { daemonLog = os.Stderr }()
	dir := t.TempDir()
	keg := func(name string) string {
		path := filepath.Join(dir, name, "koinon")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a, b := keg("a"), keg("b")
	opt := filepath.Join(dir, "opt")
	if err := os.Symlink(filepath.Dir(a), opt); err != nil {
		t.Fatal(err)
	}
	launch := filepath.Join(opt, "koinon")
	previousPath, previousEvery := launchPath, executableCheck
	launchPath, executableCheck = func() string { return launch }, 20*time.Millisecond
	defer func() { launchPath, executableCheck = previousPath, previousEvery }()

	root := filepath.Join(dir, "state")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, serveArgs(root), nil, output{ready}) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	// The opt link briefly missing, as during an upgrade, and a rewrite in place change nothing.
	if err := os.Remove(opt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := os.Symlink(filepath.Dir(a), opt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("a, rewritten"), 0755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("stopped without a change: %v", err)
	default:
	}
	// The opt link moves to the new keg, as brew upgrade does.
	tmp := filepath.Join(dir, "opt.new")
	if err := os.Symlink(filepath.Dir(b), tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, opt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.HasPrefix(err.Error(), "binary_changed: ") {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not stop after its executable changed")
	}
	events := log.events()
	if last := events[len(events)-1]; last["event"] != "stop" || last["reason"] != "binary_changed" {
		t.Fatalf("last event %v", last)
	}
	// The store was closed: a new daemon starts on the same state at once.
	again, stop := context.WithCancel(context.Background())
	againReady := make(chan []byte, 1)
	againDone := make(chan error, 1)
	go func() { againDone <- run(again, serveArgs(root), nil, output{againReady}) }()
	var body []byte
	select {
	case body = <-againReady:
	case err := <-againDone:
		t.Fatalf("restart: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("restart deadline")
	}
	// Its status names the release it runs.
	var started struct {
		Listeners []string `json:"listeners"`
	}
	json.Unmarshal(body, &started)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", started.Listeners[0]}, nil, &out); err != nil ||
		!strings.Contains(out.String(), `"version":"`+version+`"`) {
		t.Fatalf("status version: %s %v", out.String(), err)
	}
	stop()
	if err := <-againDone; err != nil {
		t.Fatal(err)
	}
}

func TestWatchExecutableNeedsAnAbsoluteResolvingPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, path := range []string{"koinon", filepath.Join(t.TempDir(), "missing")} {
		select {
		case <-watchExecutable(ctx, path, time.Millisecond):
			t.Fatalf("%s: changed", path)
		case <-time.After(50 * time.Millisecond):
		}
	}
	// A renamed copy is a change, as after a copy install.
	path := filepath.Join(t.TempDir(), "koinon")
	os.WriteFile(path, []byte("old"), 0755)
	changed := watchExecutable(ctx, path, time.Millisecond)
	os.WriteFile(path+".new", []byte("new"), 0755)
	os.Rename(path+".new", path)
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change after the rename")
	}
}
