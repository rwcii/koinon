package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/platform/launchdtest"
)

// freePort returns a port that is free on both loopback families at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	for range 20 {
		v4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := v4.Addr().(*net.TCPAddr).Port
		v6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
		v4.Close()
		if err == nil {
			v6.Close()
			return port
		}
	}
	t.Fatal("no port free on both loopback families")
	return 0
}

// The address of a client command: --address, then KOINON_DAEMON_ADDRESS, then the state
// directory's recorded port, then 127.0.0.1:47671 (#405).
func TestResolveAddress(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	resolve := func(args ...string) (string, error) {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		flags.String("state-dir", root, "")
		address := addressFlag(flags)
		if err := flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		err := resolveAddress(flags)
		return *address, err
	}
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	if got, err := resolve(); err != nil || got != "127.0.0.1:47671" {
		t.Fatalf("no record: %q %v", got, err)
	}
	if err := core.RecordPort(root, 47670); err != nil {
		t.Fatal(err)
	}
	if got, err := resolve(); err != nil || got != "127.0.0.1:47670" {
		t.Fatalf("recorded: %q %v", got, err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if got, err := resolve("--state-dir", other); err != nil || got != "127.0.0.1:47671" {
		t.Fatalf("another state directory: %q %v", got, err)
	}
	t.Setenv("KOINON_DAEMON_ADDRESS", "127.0.0.1:5000")
	t.Setenv("KOINON_STATE_DIR", other)
	if got, err := resolve(); err != nil || got != "127.0.0.1:47670" {
		t.Fatalf("another state directory's environment: %q %v", got, err)
	}
	t.Setenv("KOINON_STATE_DIR", root)
	if got, err := resolve(); err != nil || got != "127.0.0.1:5000" {
		t.Fatalf("environment: %q %v", got, err)
	}
	if got, err := resolve("--address", "[::1]:6000"); err != nil || got != "[::1]:6000" {
		t.Fatalf("flag: %q %v", got, err)
	}
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	os.WriteFile(filepath.Join(root, "port"), []byte("not a port\n"), 0600)
	if _, err := resolve(); err == nil || !strings.Contains(err.Error(), "port_invalid") {
		t.Fatalf("damaged record: %v", err)
	}
	// An explicit address needs no record.
	if got, err := resolve("--address", "127.0.0.1:7000"); err != nil || got != "127.0.0.1:7000" {
		t.Fatalf("flag with a damaged record: %q %v", got, err)
	}
	// Every command that calls the daemon resolves its address this way: each reads the
	// damaged record. S stands for the state directory option, AS for a caller.
	for _, line := range []string{
		"mcp S", "status S", "peers S AS", "peer-status S AS x", "send S AS x body", "inbox S AS", "ack S AS 1",
		"cycle-status S --as deepseek:x",
		"register S --as deepseek:x --dsh-url http://127.0.0.1:1 --dsh-credentials /synthetic",
		"memory status S AS", "recover S", "work list S AS", "work checkout status S AS", "library list S AS",
		"library state S AS", "room list S AS", "decision list S AS --filter waiting", "context show S",
		"participant list S", "sessions list S", "maintenance sweep S", "inbox clear S", "claims release S",
		"rooms prune S", "dashboard S --no-open",
	} {
		line = strings.ReplaceAll(line, " AS", " --as codex:synthetic")
		args := strings.Fields(strings.ReplaceAll(line, " S", " --state-dir "+root))
		err := run(context.Background(), args, strings.NewReader(""), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "port_invalid") {
			t.Errorf("%s: %v", line, err)
		}
	}
}

// koinon serve --state-dir, which the installed service runs, listens at the recorded port
// on both loopback families; the commands and the dashboard link follow it (#405).
func TestServeAndClientsUseRecordedPort(t *testing.T) {
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	root := filepath.Join(t.TempDir(), "state")
	port := freePort(t)
	if err := core.RecordPort(root, port); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"serve", "--state-dir", root}, nil, output{ready}) }()
	var body []byte
	select {
	case body = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	var started struct {
		Listeners []string `json:"listeners"`
	}
	json.Unmarshal(body, &started)
	want := core.Listeners(port)
	if len(started.Listeners) != 2 || started.Listeners[0] != want[0] || started.Listeners[1] != want[1] {
		t.Fatalf("listeners %v, want %v", started.Listeners, want)
	}
	for _, args := range [][]string{{"status"}, {"participant", "list"}} {
		var out bytes.Buffer
		if err := run(ctx, append(args, "--state-dir", root), nil, &out); err != nil || !strings.Contains(out.String(), `"ok":true`) {
			t.Fatalf("%v: %s %v", args, out.String(), err)
		}
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"dashboard", "--state-dir", root, "--no-open"}, nil, &out); err != nil ||
		!strings.HasPrefix(out.String(), "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/login?token=") {
		t.Fatalf("dashboard: %q %v", out.String(), err)
	}
	// KOINON_DAEMON_ADDRESS comes before the record, and --address before both.
	elsewhere := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	t.Setenv("KOINON_DAEMON_ADDRESS", elsewhere)
	t.Setenv("KOINON_STATE_DIR", root)
	var down unavailable
	if err := run(ctx, []string{"status", "--state-dir", root}, nil, &out); !errors.As(err, &down) || down.address != elsewhere {
		t.Fatalf("environment address: %v", err)
	}
	out.Reset()
	if err := run(ctx, []string{"status", "--state-dir", root, "--address", want[1]}, nil, &out); err != nil {
		t.Fatalf("flag address: %s %v", out.String(), err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A listen option replaces only its own family's listener; the other one stays at the
// recorded port.
func TestServeListenOptionsOverrideRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	port := freePort(t)
	if err := core.RecordPort(root, port); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0"}, nil, output{ready})
	}()
	var body []byte
	select {
	case body = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	var started struct {
		Listeners []string `json:"listeners"`
	}
	json.Unmarshal(body, &started)
	if len(started.Listeners) != 2 || strings.HasSuffix(started.Listeners[0], ":"+strconv.Itoa(port)) ||
		started.Listeners[1] != core.Listeners(port)[1] {
		t.Fatalf("listeners %v", started.Listeners)
	}
	cancel()
	<-done
	// A damaged record stops the daemon from starting rather than using another port.
	os.WriteFile(filepath.Join(root, "port"), []byte("0\n"), 0600)
	if err := run(context.Background(), []string{"serve", "--state-dir", root}, nil, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "port_invalid") {
		t.Fatalf("damaged record: %v", err)
	}
}

// With no daemon answering, the service's restart command is named at the recorded port
// only (#405).
func TestDaemonDownAtRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := core.RecordPort(root, 47670); err != nil {
		t.Fatal(err)
	}
	f := launchdtest.New(501, platform.DaemonLabel)
	dir := t.TempDir()
	s := platform.Services{Backend: "launchd", Home: filepath.Join(dir, "home"), UID: 501, User: "alice", Run: f.Run,
		SystemDir: filepath.Join(dir, "LaunchDaemons")}
	os.MkdirAll(s.SystemDir, 0755)
	os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", root), 0644)
	newServices = func() (platform.Services, error) { return s, nil }
	defer func() { newServices = platform.NewServices }()
	var down unavailable
	if err := daemonDown(context.Background(), root, "127.0.0.1:47670"); !errors.As(err, &down) || down.mode != "system" {
		t.Fatalf("recorded port: %v", err)
	}
	err := daemonDown(context.Background(), root, "127.0.0.1:47671")
	if !errors.As(err, &down) || down.mode != "manual" || !strings.Contains(down.note, "--listen 127.0.0.1:47671") {
		t.Fatalf("former default port: %v", err)
	}
}

// koinon install refuses a port outside 1 to 65535 before it changes anything.
func TestInstallPortRefusal(t *testing.T) {
	for _, value := range []string{"0", "65536", "-1", "http"} {
		err := run(context.Background(), []string{"install", "--port", value, "--prefix", filepath.Join(t.TempDir(), "prefix")}, nil, io.Discard)
		var usage usageError
		if !errors.As(err, &usage) || usage.code != "invalid_request" {
			t.Fatalf("--port %s: %v", value, err)
		}
	}
}

// koinon mcp without --address reads its address again for each call, so it follows a
// daemon that restarts on another recorded port; a given --address stays (#549).
func TestMCPFollowsRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	if followAddress(root, true) != nil {
		t.Fatal("a given --address is replaced")
	}
	follow := followAddress(root, false)
	if got, err := follow(); err != nil || got != "127.0.0.1:47671" {
		t.Fatalf("no record: %q %v", got, err)
	}
	if err := core.RecordPort(root, 47681); err != nil {
		t.Fatal(err)
	}
	if got, err := follow(); err != nil || got != "127.0.0.1:47681" {
		t.Fatalf("after the port change: %q %v", got, err)
	}
	// A launcher's address for this state directory still comes first.
	t.Setenv("KOINON_DAEMON_ADDRESS", "127.0.0.1:5000")
	t.Setenv("KOINON_STATE_DIR", root)
	if got, err := follow(); err != nil || got != "127.0.0.1:5000" {
		t.Fatalf("environment: %q %v", got, err)
	}
}
