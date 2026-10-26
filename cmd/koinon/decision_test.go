package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

// The decision commands in the installed DeepSeek guide reach the decision API as written.
func TestDecisionCommandsOfTheDeepSeekGuide(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-guide", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var guide bytes.Buffer
	if err := setup.Guide("deepseek", &guide); err != nil {
		t.Fatal(err)
	}
	deadline := strconv.FormatInt(time.Now().Unix()+600, 10)
	run := func(args []string) (map[string]any, error) {
		var out bytes.Buffer
		args = append([]string{args[0], "--state-dir", root, "--address", d.Addresses()[0]}, args[1:]...)
		if err := decisionCommand(context.Background(), args, &out); err != nil {
			return nil, err
		}
		var reply map[string]any
		return reply, json.Unmarshal(out.Bytes(), &reply)
	}
	lines := map[string]string{}
	for _, line := range strings.Split(guide.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "koinon decision ") {
			lines[strings.Fields(line)[2]] = line
		}
	}
	if len(lines) != 10 {
		t.Fatalf("guide decision commands: %v", lines)
	}
	// The item is active, so close and restore reach the daemon and are refused there; the
	// author then withdraws it (#504).
	refused := map[string]string{"close": "not_in_progress", "restore": "not_found"}
	for _, op := range []string{"create", "edit", "get", "list", "history", "pending", "ack", "close", "restore", "withdraw"} {
		line := strings.NewReplacer("$DSH_SESSION_ID", "synthetic-guide", "REF", "G-1", "KEY", "synthetic-key",
			"UNIX_SECONDS", deadline, "REVISION", "2", "'title'", "Synthetic", "'reason'", "done", "VERDICT_ID", "1").Replace(lines[op])
		if op == "edit" {
			line = strings.Replace(line, "--if-revision 2", "--if-revision 1", 1)
		}
		reply, err := run(strings.Fields(line)[2:])
		var refusal core.RefusedError
		if want := refused[op]; want != "" {
			if !errors.As(err, &refusal) || refusal.Code != want {
				t.Fatalf("%s: %v %v", line, reply, err)
			}
			continue
		}
		if err != nil || reply["ok"] != true {
			t.Fatalf("%s: %v %v", line, reply, err)
		}
	}
	// The withdrawn item reopens with restore at its revision.
	reply, err := run([]string{"restore", "--as", "deepseek:synthetic-guide", "--reference", "G-1", "--if-revision", "2"})
	if err != nil || reply["result"].(map[string]any)["state"] != "active" || reply["result"].(map[string]any)["revision"] != float64(3) {
		t.Fatalf("reopen: %v %v", reply, err)
	}
	if _, err := run([]string{"withdraw", "--as", "deepseek:synthetic-guide", "--reference", "G-1", "--if-revision", "3", "--key", "k3", "--deadline", deadline}); err == nil {
		t.Fatal("withdraw without --reason")
	}
	reply, err = run([]string{"create", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--title", "Two commands", "--kind", "fyi",
		"--command", "make check", "--command", "go vet ./...", "--recipient", "synthetic-nobody", "--key", "k2", "--deadline", deadline})
	if err != nil || len(reply["result"].(map[string]any)["recipients"].([]any)) != 1 {
		t.Fatalf("repeated flags: %v %v", reply, err)
	}
	reply, err = run([]string{"get", "--as", "deepseek:synthetic-guide", "--reference", "G-2"})
	if err != nil || len(reply["result"].(map[string]any)["commands"].([]any)) != 2 {
		t.Fatalf("get: %v %v", reply, err)
	}
	// A false clear flag changes nothing.
	reply, err = run([]string{"edit", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--if-revision", "1", "--title", "Kept lists",
		"--no-commands=false", "--no-recipients=false"})
	if err != nil || len(reply["result"].(map[string]any)["commands"].([]any)) != 2 || len(reply["result"].(map[string]any)["recipients"].([]any)) != 1 {
		t.Fatalf("false clear flags: %v %v", reply, err)
	}
	if _, err := run([]string{"edit", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--if-revision", "2", "--command", "ls", "--no-commands=false"}); err != nil {
		t.Fatalf("--command with --no-commands=false: %v", err)
	}
	reply, err = run([]string{"edit", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--if-revision", "3", "--no-commands", "--no-recipients"})
	if err != nil || len(reply["result"].(map[string]any)["commands"].([]any)) != 0 || len(reply["result"].(map[string]any)["recipients"].([]any)) != 0 {
		t.Fatalf("clear lists: %v %v", reply, err)
	}
	if _, err := run([]string{"edit", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--if-revision", "4", "--command", "ls", "--no-commands"}); err == nil {
		t.Fatal("--command with --no-commands")
	}
	if _, err := run([]string{"create", "--as", "deepseek:synthetic-guide", "--reference", "G-3", "--title", "t", "--kind", "fyi"}); err == nil {
		t.Fatal("create without --key and --deadline")
	}
	if _, err := run([]string{"edit", "--as", "deepseek:synthetic-guide", "--reference", "G-2", "--title", "t"}); err == nil {
		t.Fatal("edit without --if-revision")
	}
}
