package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/platform/launchdtest"
)

// With no daemon answering, status and recover name the service mode and the command that
// restarts it, and run nothing (#283 chunk 03).
func TestStatusAndRecoverWithoutDaemon(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	// An address where nothing listens: a port taken and released again.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := listener.Addr().String()
	listener.Close()
	for _, args := range [][]string{{"status"}, {"recover"}} {
		err := run(context.Background(), append(args, "--state-dir", root, "--address", free), nil, &strings.Builder{})
		var down unavailable
		if !errors.As(err, &down) || down.mode != "manual" || !strings.Contains(down.note, "koinon serve --state-dir") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	// At the service's address, the mode comes from the service manager.
	f := launchdtest.New(501, platform.DaemonLabel)
	dir := t.TempDir()
	s := platform.Services{Backend: "launchd", Home: filepath.Join(dir, "home"), UID: 501, User: "alice", Run: f.Run,
		SystemDir: filepath.Join(dir, "LaunchDaemons")}
	os.MkdirAll(s.SystemDir, 0755)
	os.WriteFile(s.SystemArtifact(), s.RenderSystemDaemon("/p/bin/koinon", root), 0644)
	newServices = func() (platform.Services, error) { return s, nil }
	defer func() { newServices = platform.NewServices }()
	err = daemonDown(context.Background(), root, "127.0.0.1:47671")
	var down unavailable
	if !errors.As(err, &down) || down.mode != "system" || down.command != "sudo launchctl kickstart -k system/"+platform.DaemonLabel ||
		!strings.Contains(err.Error(), "restart it with: sudo launchctl kickstart -k system/") {
		t.Fatalf("system mode: %v", err)
	}
	for _, call := range f.Calls {
		if strings.HasPrefix(call, "sudo ") {
			t.Fatalf("ran %s", call)
		}
	}
}
