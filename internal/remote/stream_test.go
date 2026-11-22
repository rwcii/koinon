package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tailscale reports TUN false in userspace networking mode; a missing field is the usual
// kernel interface (#688).
func TestStatusReportsUserspaceNetworking(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"BackendState":"Running","TUN":false}`: true,
		`{"BackendState":"Running","TUN":true}`:  false,
		`{"BackendState":"Running"}`:             false,
	} {
		f := newFake()
		f.statusRaw = []byte(raw)
		s, err := (Manager{Adapter: f}).Inspect(context.Background())
		if err != nil || s.Userspace != want {
			t.Fatalf("%s: userspace %v, %v", raw, s.Userspace, err)
		}
	}
}

// writeTailscale puts a synthetic tailscale command first and alone on PATH.
func writeTailscale(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + argsFile + "\n" + body
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	return argsFile
}

// DialStream runs tailscale nc HOST PORT and carries the connection over its standard input
// and output, with deadlines; Close ends the command.
func TestExecDialStreamUsesTailscaleNC(t *testing.T) {
	argsFile := writeTailscale(t, "exec cat\n")
	conn, err := (Manager{Adapter: Exec{}}).DialStream(context.Background(), "[fd7a:115c:a1e0::2]:47672")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("synthetic hello")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("synthetic hello"))
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "synthetic hello" {
		t.Fatalf("echo %q %v", got, err)
	}
	if args, _ := os.ReadFile(argsFile); strings.TrimSpace(string(args)) != "nc fd7a:115c:a1e0::2 47672" {
		t.Fatalf("args %q", args)
	}
	if conn.RemoteAddr().String() != "[fd7a:115c:a1e0::2]:47672" || conn.RemoteAddr().Network() != "tailscale-nc" {
		t.Fatalf("remote %v", conn.RemoteAddr())
	}
	// A read deadline ends a read that gets no data.
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := conn.Read(got); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	cmd := conn.(*streamConn).cmd
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("close left the command running")
	}
	conn.Close() // a second close is harmless
}

// A connection that tailscaled cannot make ends the command; the first read sees the end,
// as for a refused TCP connection.
func TestExecDialStreamFailureEndsTheStream(t *testing.T) {
	writeTailscale(t, "echo 'synthetic dial failure' >&2\nexit 1\n")
	conn, err := (Exec{}).DialStream(context.Background(), "100.64.0.2:47672")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after a failed dial: %v", err)
	}
	// Refusals before any command runs.
	if _, err := (Exec{}).DialStream(context.Background(), "no-port"); err == nil {
		t.Fatal("an address without a port was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Exec{}).DialStream(ctx, "100.64.0.2:47672"); err == nil {
		t.Fatal("a cancelled dial ran")
	}
	if _, err := (Manager{Adapter: newFake()}).DialStream(context.Background(), "100.64.0.2:47672"); code(err) != "tailscale_stream_unavailable" {
		t.Fatalf("adapter without streams: %v", err)
	}
}
