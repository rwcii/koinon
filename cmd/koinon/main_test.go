package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestWorkCommands(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	// Missing required options are refused before any daemon contact or state creation.
	for args, missing := range map[string]string{
		"work create --as codex:x":                              "--criteria, --deadline, --key, --non-goals, --title",
		"work start --as codex:x":                               "--checkpoint, --deadline, --if-revision, --key, --next-artifact, --progress-deadline, WORK_ID",
		"work update 00000000000000000000000000000001 --as c:x": "--checkpoint, --claim-generation, --if-revision, --next-artifact, --progress, --progress-deadline",
		"work release --if-revision 1 --as c:x":                 "--checkpoint, --claim-generation, WORK_ID",
		"work finish x --as c:x --if-revision 1":                "--claim-generation, --outcome",
		"claim renew x --as c:x":                                "--claim-generation, --if-claim-revision",
	} {
		err := run(context.Background(), append(strings.Fields(args), "--state-dir", root), nil, io.Discard)
		var usage usageError
		if !errors.As(err, &usage) || usage.code != "invalid_request" || !strings.HasSuffix(usage.message, missing) {
			t.Fatalf("%s: %v", args, err)
		}
	}
	for _, args := range []string{"work", "work unknown", "claim release x", "work propose x --as c:x --if-revision 1",
		"work propose x --as c:x --if-revision 1 --proposed-assignee a --clear-assignee", "work get x --as c:x --if-revision 1",
		"work get x y --as c:x"} {
		if err := run(context.Background(), append(strings.Fields(args), "--state-dir", root), nil, io.Discard); err == nil {
			t.Fatalf("accepted: %s", args)
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused commands created state")
	}
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
	work := func(args ...string) (map[string]any, error) {
		var out bytes.Buffer
		args = append(append([]string{}, args...), "--state-dir", root, "--address", address, "--as", "deepseek:synthetic-d")
		if err := run(context.Background(), args, nil, &out); err != nil {
			return nil, err
		}
		var result map[string]any
		json.Unmarshal(out.Bytes(), &result)
		return result["result"].(map[string]any), nil
	}
	deadline := fmt.Sprint(time.Now().Unix() + 600)
	created, err := work("work", "create", "--title", "t", "--criteria", "c", "--non-goals", "n", "--key", "k", "--deadline", deadline,
		"--proposed-assignee", "codex:x", "--reference", "r1", "--reference", "r2")
	if err != nil {
		t.Fatal(err)
	}
	id := created["work_id"].(string)
	if _, err := work("work", "propose", id, "--if-revision", "1", "--clear-assignee"); err != nil {
		t.Fatal(err)
	}
	if item, err := work("work", "get", id); err != nil || item["proposed_assignee"] != nil || len(item["references"].([]any)) != 2 {
		t.Fatalf("propose clear: %v %v", item, err)
	}
	started, err := work("work", "start", id, "--if-revision", "2", "--checkpoint", "c", "--next-artifact", "a",
		"--progress-deadline", deadline, "--key", "s", "--deadline", deadline, "--path-resource", "docs", "--exact-resource", "db")
	if err != nil {
		t.Fatal(err)
	}
	generation := fmt.Sprint(started["claim"].(map[string]any)["generation"])
	if item, err := work("work", "get", id); err != nil || len(item["current_claim"].(map[string]any)["resources"].([]any)) != 3 ||
		item["current_claim"].(map[string]any)["consumer"] != "deepseek:synthetic-d" {
		t.Fatalf("resources: %v %v", item, err)
	}
	if r, err := work("claim", "renew", id, "--claim-generation", generation, "--if-claim-revision", "1"); err != nil || r["seq"] != nil {
		t.Fatalf("renew: %v %v", r, err)
	}
	if r, err := work("work", "list", "--lifecycle", "active"); err != nil || len(r["items"].([]any)) != 1 {
		t.Fatalf("list: %v %v", r, err)
	}
	var refused core.RefusedError
	if _, err := work("work", "start", id, "--if-revision", "3", "--checkpoint", "c", "--next-artifact", "a", "--progress-deadline",
		deadline, "--key", "s2", "--deadline", deadline, "--consumer", "other"); !errors.As(err, &refused) || refused.Code != "claim_conflict" || len(refused.Details) == 0 {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := work("work", "finish", id, "--if-revision", "3", "--claim-generation", generation, "--outcome", "completed",
		"--reference", "synthetic-pr"); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
}
