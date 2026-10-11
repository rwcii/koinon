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
