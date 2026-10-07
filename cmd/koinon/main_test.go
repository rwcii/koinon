package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
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
		done <- run(ctx, []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, nil, output{ready})
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
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", status.Listeners[0]}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"total":0`)) {
		t.Fatal(out.String())
	}
	if err := run(context.Background(), []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, nil, &out); err == nil {
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
	if err := run(context.Background(), []string{"status", "--state-dir", root, "--address", status.Listeners[0]}, nil, &out); err == nil {
		t.Fatal("dead daemon reported running")
	}
	secret, _ := os.ReadFile(filepath.Join(root, "secret"))
	if bytes.Contains(body, secret) || bytes.Contains(out.Bytes(), secret) {
		t.Fatal("secret in command output")
	}
}

func TestMessageCommands(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, nil, output{ready})
	}()
	var status struct {
		Listeners []string `json:"listeners"`
	}
	select {
	case body := <-ready:
		if err := json.Unmarshal(body, &status); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	address := status.Listeners[0]
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for _, key := range []string{"codex:synthetic-a", "opencode:synthetic-b"} {
		family, id, _ := strings.Cut(key, ":")
		if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/register", core.Registration{Family: family, ID: id, Directory: directory}); err != nil {
			t.Fatal(err)
		}
	}
	command := func(in string, args ...string) (map[string]any, error) {
		t.Helper()
		var out bytes.Buffer
		args = append(args[:1], append([]string{"--state-dir", root, "--address", address}, args[1:]...)...)
		if err := run(context.Background(), args, strings.NewReader(in), &out); err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s: %v", out.String(), err)
		}
		return result, nil
	}
	peers, err := command("", "peers", "--as", "codex:synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	var recipient string
	for _, p := range peers["peers"].([]any) {
		if p := p.(map[string]any); p["family"] == "opencode" {
			recipient = p["name"].(string)
		}
	}
	if _, err := command("", "send", "--as", "codex:synthetic-a", recipient, "first synthetic"); err != nil {
		t.Fatal(err)
	}
	if _, err := command("second synthetic", "send", "--as", "codex:synthetic-a", recipient, "-"); err != nil {
		t.Fatal(err)
	}
	inbox, err := command("", "inbox", "--as", "opencode:synthetic-b", "--after", "1")
	if err != nil {
		t.Fatal(err)
	}
	messages := inbox["inbox"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["body"] != "second synthetic" {
		t.Fatalf("inbox: %v", inbox)
	}
	if acked, err := command("", "ack", "--as", "opencode:synthetic-b", "2"); err != nil || acked["acked_through"] != float64(2) {
		t.Fatalf("ack: %v %v", acked, err)
	}
	var refused core.RefusedError
	if _, err := command("", "ack", "--as", "opencode:synthetic-b", "3"); !errors.As(err, &refused) || refused.Code != "ack_beyond_last" {
		t.Fatalf("ack beyond last: %v", err)
	}
	for _, args := range [][]string{{"peers"}, {"peers", "--as", "codex"}, {"send", "--as", "codex:synthetic-a", recipient}, {"ack", "--as", "codex:synthetic-a", "two"}} {
		if _, err := command("", args...); err == nil || errors.As(err, &refused) {
			t.Fatalf("invalid command accepted: %v %v", args, err)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAgentCommands(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"guide", "--agent", "codex"}, nil, &out); err != nil || !strings.Contains(out.String(), "## Codex") {
		t.Fatalf("guide: %v %s", err, out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"hook", "agy-stop"}, strings.NewReader(`{}`), &out); err != nil || out.String() != "{}\n" {
		t.Fatalf("hook: %v %q", err, out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"setup", "deepseek"}, nil, &out); err != nil || !strings.Contains(out.String(), `"changed":[]`) {
		t.Fatalf("setup: %v %s", err, out.String())
	}
	for _, args := range [][]string{{"guide"}, {"guide", "--agent", "x"}, {"hook", "other"}, {"setup"}, {"setup", "codex", "--cli", "relative"}} {
		if err := run(context.Background(), args, nil, &out); err == nil {
			t.Fatalf("accepted: %v", args)
		}
	}
}

func TestMemoryCommands(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--state-dir", root, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0"}, nil, output{ready})
	}()
	var status struct {
		Listeners []string `json:"listeners"`
	}
	select {
	case body := <-ready:
		json.Unmarshal(body, &status)
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("startup deadline")
	}
	address := status.Listeners[0]
	secret, _ := core.ReadSecret(root)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-d", Repository: repo, Directory: repo}); err != nil {
		t.Fatal(err)
	}
	memory := func(args ...string) (map[string]any, error) {
		var out bytes.Buffer
		args = append([]string{"memory", args[0], "--state-dir", root, "--address", address, "--as", "deepseek:synthetic-d"}, args[1:]...)
		if err := run(context.Background(), args, nil, &out); err != nil {
			return nil, err
		}
		var result map[string]any
		json.Unmarshal(out.Bytes(), &result)
		return result["result"].(map[string]any), nil
	}
	if r, err := memory("record", "--type", "gotcha", "--scope", "task", "--scope-target", "t1", "synthetic gotcha"); err != nil || r["seq"] != float64(1) {
		t.Fatalf("record: %v %v", r, err)
	}
	page, err := memory("sync", "--consumer", "cli")
	if err != nil || page["kind"] != "snapshot" {
		t.Fatalf("sync: %v %v", page, err)
	}
	if r, err := memory("ack", "--consumer", "cli", "--snapshot-id", page["snapshot_id"].(string)); err != nil || r["complete"] != true {
		t.Fatalf("ack: %v %v", r, err)
	}
	if r, err := memory("ack", "--consumer", "cli", "--through", "1"); err != nil || r["cursor"] != float64(1) {
		t.Fatalf("numeric ack: %v %v", r, err)
	}
	if r, err := memory("recall", "gotcha"); err != nil || len(r["entries"].([]any)) != 1 {
		t.Fatalf("recall: %v %v", r, err)
	}
	if r, err := memory("status"); err != nil || r["head"] != float64(1) {
		t.Fatalf("status: %v %v", r, err)
	}
	var refused core.RefusedError
	if _, err := memory("ack", "--consumer", "fresh", "--through", "1"); !errors.As(err, &refused) || refused.Code != "not_bootstrapped" {
		t.Fatalf("refusal: %v", err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"recover", "--state-dir", root, "--address", address}, nil, &out); err != nil || !strings.Contains(out.String(), `"blocked":null`) {
		t.Fatalf("recover: %v %s", err, out.String())
	}
	for _, args := range [][]string{{"memory"}, {"memory", "unknown"}, {"memory", "status"}, {"memory", "record", "--as", "deepseek:synthetic-d"}} {
		if err := run(context.Background(), args, nil, &out); err == nil {
			t.Fatalf("accepted: %v", args)
		}
	}
	cancel()
	<-done
}
