package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	// The session works in a checkout, so its items record there (#536).
	checkout := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", checkout).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	resource, err := core.CheckoutResource(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-guide",
		Repository: resource.Repository, Directory: resource.Directory}); err != nil {
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
	if len(lines) != 12 {
		t.Fatalf("guide decision commands: %v", lines)
	}
	// The item is active, so close and restore reach the daemon and are refused there; the
	// author then withdraws it (#504).
	refused := map[string]string{"close": "not_in_progress", "restore": "not_found"}
	export := filepath.Join(t.TempDir(), "board.jsonl")
	for _, op := range []string{"create", "edit", "get", "list", "history", "pending", "ack", "close", "restore", "record", "withdraw", "export"} {
		line := strings.NewReplacer("$DSH_SESSION_ID", "synthetic-guide", "REF", "G-1", "KEY", "synthetic-key", "PATH", export,
			"UNIX_SECONDS", deadline, "REVISION", "2", "'title'", "Synthetic", "'reason'", "done", "VERDICT_ID", "1").Replace(lines[op])
		if op == "edit" {
			line = strings.Replace(line, "--if-revision 2", "--if-revision 1", 1)
		}
		args := strings.Fields(line)[2:]
		if op == "record" {
			// The item has no verdict yet: the record appends nothing and creates no file.
			args = append(args, "--directory", checkout)
		}
		reply, err := run(args)
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
	if _, err := os.Lstat(filepath.Join(checkout, core.DecisionRecordFile)); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict created the file: %v", err)
	}
	// The withdrawn item reopens with restore at its revision.
	reply, err := run([]string{"restore", "--as", "deepseek:synthetic-guide", "--reference", "G-1", "--if-revision", "2"})
	if err != nil || reply["result"].(map[string]any)["state"] != "active" || reply["result"].(map[string]any)["revision"] != float64(3) {
		t.Fatalf("reopen: %v %v", reply, err)
	}
	reply, err = run([]string{"history", "--as", "deepseek:synthetic-guide", "--reference", "G-1", "--withdrawals"})
	if err != nil || len(reply["result"].(map[string]any)["withdrawals"].([]any)) != 1 {
		t.Fatalf("history --withdrawals: %v %v", reply, err)
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

// koinon decision record refuses a directory outside a Git checkout and needs --reference.
func TestDecisionRecordCommandRefusals(t *testing.T) {
	if err := decisionCommand(context.Background(), []string{"record", "--as", "deepseek:synthetic", "--reference", "R-1", "--directory", t.TempDir(),
		"--state-dir", t.TempDir(), "--address", "127.0.0.1:1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("record outside a checkout or daemon succeeded")
	}
}

// shipFactsFile writes a valid ship facts file with the Decision record fact into checkout; an
// empty mode leaves the fact out.
func shipFactsFile(t *testing.T, checkout, mode string) {
	t.Helper()
	text := "# Repository facts — ship\n\n## Version\n1\n## Skill\nship\n## Sources\n- AGENTS.md\n\n## Work target\nsynthetic\n" +
		"## Commit policy\nsynthetic\n## Required checks\nsynthetic\n## PR and merge policy\nsynthetic\n"
	if mode != "" {
		text += "## Decision record\n" + mode + "\n"
	}
	if err := os.MkdirAll(filepath.Join(checkout, ".koinon", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".koinon", "skills", "ship.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// decision record applies the checkout's Decision record fact, and its refusals reach the
// command's JSON report with their codes: another repository's item, an invalid mode and a
// symbolic link (#536, #589). The default none opens no record file; full appends after a
// file in another format; two worktrees of one repository apply their own facts.
func TestDecisionRecordCommandModes(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.name=synthetic", "-c", "user.email=synthetic@example.invalid",
		"commit", "-q", "--allow-empty", "-m", "synthetic"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	common, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "secret"), []byte(strings.Repeat("1", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	item := map[string]any{"reference": "CLI-1", "where": "synthetic", "author_repository": common + "-other"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := any(item)
		if strings.HasSuffix(r.URL.Path, "/history") {
			result = map[string]any{"reference": "CLI-1", "more": false, "verdicts": []any{map[string]any{"verdict_id": 1, "revision": 1,
				"verdict": "approved", "at": 1, "note": "Synthetic note.", "snapshot": map[string]any{"title": "Synthetic",
					"context": "Synthetic context.", "recommendation": "## Recommendation: synthetic choice", "commands": []string{}}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	record := func(dir string) (map[string]any, error) {
		var out bytes.Buffer
		err := decisionCommand(context.Background(), []string{"record", "--as", "deepseek:synthetic", "--reference", "CLI-1", "--directory", dir,
			"--state-dir", state, "--address", strings.TrimPrefix(server.URL, "http://")}, &out)
		if err != nil {
			return errorReport(err), err
		}
		var reply map[string]any
		if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		return reply["result"].(map[string]any), nil
	}
	if got, err := record(repo); err == nil || got["code"] != "decision_other_repository" {
		t.Errorf("other repository: %v", got)
	}
	item["author_repository"] = common
	file := filepath.Join(repo, "DECISIONS.md")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	// No facts file: none, and the symbolic link is not opened.
	if got, err := record(repo); err != nil || got["mode"] != "none" || len(got["appended"].([]any)) != 0 ||
		!strings.Contains(got["instruction"].(string), "the document that holds it") {
		t.Errorf("default none: %v %v", got, err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("none wrote through the link: %v", err)
	}
	shipFactsFile(t, repo, "")
	if got, err := record(repo); err != nil || got["mode"] != "none" {
		t.Errorf("fact absent: %v %v", got, err)
	}
	shipFactsFile(t, repo, "full")
	if got, err := record(repo); err == nil || got["code"] != "decision_record_invalid" {
		t.Errorf("symbolic link: %v", got)
	}
	os.Remove(file)
	shipFactsFile(t, repo, "sometimes")
	if got, err := record(repo); err == nil || got["code"] != "decision_record_mode_invalid" {
		t.Errorf("invalid mode: %v", got)
	}
	shipFactsFile(t, repo, "full, with the card text")
	foreign := "# Private project format\n"
	if err := os.WriteFile(file, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := record(repo); err != nil || got["mode"] != "full" || len(got["appended"].([]any)) != 1 {
		t.Errorf("full after a foreign file: %v %v", got, err)
	}
	if data, _ := os.ReadFile(file); !strings.HasPrefix(string(data), foreign) || !strings.Contains(string(data), "> Synthetic context.") {
		t.Errorf("full entry:\n%s", data)
	}
	// A second worktree of the same repository applies its own fact.
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "--detach", other).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	shipFactsFile(t, other, "short")
	if got, err := record(other); err != nil || got["mode"] != "short" || len(got["appended"].([]any)) != 1 {
		t.Errorf("short in the second worktree: %v %v", got, err)
	}
	data, _ := os.ReadFile(filepath.Join(other, "DECISIONS.md"))
	if !strings.Contains(string(data), "- Recommendation: synthetic choice\n") || !strings.Contains(string(data), "> Synthetic note.\n") ||
		strings.Contains(string(data), "Synthetic context.") {
		t.Errorf("short entry:\n%s", data)
	}
}
