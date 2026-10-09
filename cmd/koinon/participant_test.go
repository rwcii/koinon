package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// The command line reads and acknowledges a held participant's inbox, and refuses a former
// holder with stale_holder (participants chunk 03).
func TestParticipantCommands(t *testing.T) {
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
	repo := filepath.Join(t.TempDir(), "koinon")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	repo, _ = filepath.EvalSymlinks(repo)
	register := func(family, id string) core.Session {
		t.Helper()
		launch, err := core.CreateLaunch(context.Background(), address, secret, core.LaunchTarget{Family: family, Directory: repo, CLI: "/synthetic/cli", HostPID: 4242})
		if err != nil {
			t.Fatal(err)
		}
		data, err := core.Call(context.Background(), address, secret, "/v1/sessions/register", core.Registration{Family: family, ID: id,
			Repository: repo, Directory: repo, LaunchID: launch, Ancestors: []int{4242}})
		if err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Session core.Session `json:"session"`
		}
		json.Unmarshal(data, &reply)
		return reply.Session
	}
	a := register("codex", "synthetic-a")
	b := register("codex", "synthetic-b")
	register("agy", "synthetic-sender")
	command := func(args ...string) (map[string]any, error) {
		t.Helper()
		var out bytes.Buffer
		offset := 1
		if args[0] == "work" || args[0] == "claim" || args[0] == "memory" {
			offset = 2
		}
		args = append(args[:offset], append([]string{"--state-dir", root, "--address", address}, args[offset:]...)...)
		if err := run(context.Background(), args, strings.NewReader(""), &out); err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s: %v", out.String(), err)
		}
		return result, nil
	}
	if _, err := command("send", "--as", "agy:synthetic-sender", a.Alias, "for the participant"); err != nil {
		t.Fatal(err)
	}
	deadline := strconv.FormatInt(time.Now().Unix()+600, 10)
	createArgs := []string{"work", "create", "--as", "codex:synthetic-a", "--title", "synthetic", "--criteria", "synthetic", "--non-goals", "synthetic", "--key", "synthetic-create-retry", "--deadline", deadline}
	created, err := command(createArgs...)
	if err != nil {
		t.Fatal(err)
	}
	workID := created["result"].(map[string]any)["work_id"].(string)
	if _, err := command("memory", "sync", "--as", "codex:synthetic-a"); err != nil {
		t.Fatal(err)
	}
	// A retires; B's renewal makes it the one qualifier and fences A, which registers again.
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/retire", core.Mutation{Family: "codex", ID: "synthetic-a", IfRevision: a.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), address, secret, "/v1/sessions/renew", core.Mutation{Family: "codex", ID: "synthetic-b", IfRevision: b.Revision}); err != nil {
		t.Fatal(err)
	}
	register("codex", "synthetic-a")
	var refused core.RefusedError
	consumer := "participant:" + a.Address
	values := map[string]string{"if_revision": "1", "claim_generation": "1", "if_claim_revision": "1", "checkpoint": "synthetic", "next_artifact": "synthetic", "progress": "synthetic", "progress_deadline": deadline, "key": "synthetic-create-retry", "deadline": deadline, "title": "synthetic", "criteria": "synthetic", "non_goals": "synthetic", "outcome": "withdrawn", "proposed_assignee": "synthetic"}
	for _, op := range core.WorkOperations() {
		parts := strings.Split(op, "-")
		args := []string{parts[0], parts[1], "--as", "codex:synthetic-a", "--consumer", consumer}
		for _, field := range core.WorkRequired[op] {
			if field != "work_id" {
				args = append(args, "--"+strings.ReplaceAll(field, "_", "-"), values[field])
			}
		}
		if op != "work-create" && op != "work-list" {
			args = append(args, workID)
		}
		if _, err := command(args...); !errors.As(err, &refused) || refused.Code != "stale_holder" {
			t.Fatalf("stale CLI %s: %v", op, err)
		}
	}
	for _, op := range []string{"sync", "ack", "status", "record", "recall"} {
		args := []string{"memory", op, "--as", "codex:synthetic-a", "--consumer", consumer}
		if op == "record" || op == "recall" {
			args = append(args, "synthetic")
		}
		if _, err := command(args...); !errors.As(err, &refused) || refused.Code != "stale_holder" {
			t.Fatalf("stale CLI memory %s: %v", op, err)
		}
	}

	if _, err := command("inbox", "--as", "codex:synthetic-a", "--participant-after", "0"); !errors.As(err, &refused) || refused.Code != "stale_holder" {
		t.Fatalf("former holder's participant read: %v", err)
	}
	if _, err := command("ack", "--as", "codex:synthetic-a", "--participant", "1", "0"); !errors.As(err, &refused) || refused.Code != "stale_holder" {
		t.Fatalf("former holder's participant ack: %v", err)
	}
	inbox, err := command("inbox", "--as", "codex:synthetic-b")
	if err != nil {
		t.Fatal(err)
	}
	participant, _ := inbox["inbox"].(map[string]any)["participant"].(map[string]any)
	if participant == nil || participant["address"] != a.Alias || participant["last_seq"] != float64(1) {
		t.Fatalf("holder inbox: %v", inbox)
	}
	acked, err := command("ack", "--as", "codex:synthetic-b", "--participant", "1", "0")
	if p, _ := acked["participant"].(map[string]any); err != nil || p == nil || p["acked_through"] != float64(1) {
		t.Fatalf("holder ack: %v %v", acked, err)
	}
}
