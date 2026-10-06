package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type output struct{ ch chan []byte }

func (o output) Write(p []byte) (int, error) { o.ch <- append([]byte(nil), p...); return len(p), nil }

func TestServeStatusAndShutdown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, output{ready})
	}()
	var body []byte
	select {
	case body = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	var status struct {
		Listeners []string `json:"listeners"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", status.Listeners[0]}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"total":0`)) {
		t.Fatal(out.String())
	}
	if err := run(context.Background(), []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, &out); err == nil {
		t.Fatal("second daemon started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown deadline")
	}
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", status.Listeners[0]}, &out); err == nil {
		t.Fatal("dead daemon reported running")
	}
	secret, _ := os.ReadFile(filepath.Join(root, "secret"))
	if bytes.Contains(body, secret) || bytes.Contains(out.Bytes(), secret) {
		t.Fatal("secret in command output")
	}
}
