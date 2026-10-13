package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func TestLibraryStateEveryNativeFamilyAndNoArgumentIdentity(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
			h.env["CODEX_HOME"], h.env["HOME"] = t.TempDir(), t.TempDir()
			args := func(extra map[string]any) map[string]any {
				for k, v := range c.args {
					extra[k] = v
				}
				return extra
			}
			value, failed := h.tool("library_state", args(map[string]any{"scope": "project", "item_ids": []string{"skill.koinon-guide"}}), c.meta)
			if failed {
				t.Fatal(value)
			}
			state := value["library"].(map[string]any)["items"].([]any)[0].(map[string]any)
			if state["state"] != "missing" || state["decision_reason"] != nil {
				t.Fatalf("project: %+v", state)
			}
			value, failed = h.tool("library_state", args(map[string]any{"item_ids": []string{"skill.koinon-guide"}}), c.meta)
			if failed {
				t.Fatal(value)
			}
			state = value["library"].(map[string]any)["items"].([]any)[0].(map[string]any)
			if c.family == "codex" {
				if state["state"] != "missing" {
					t.Fatal(state)
				}
			} else if state["reason"] != "unsupported_scope" || state["state"] != nil {
				t.Fatal("invented discovery", state)
			}
			for _, bad := range []map[string]any{{"family": "codex"}, {"root": h.env["CODEX_HOME"]}, {"path": "arbitrary"}, {"directory": t.TempDir()}, {"directory": nil}, {"scope": nil}, {"item_ids": nil}, {"item_ids": []string{"skill.koinon-guide", "skill.koinon-guide"}}} {
				if value, failed := h.tool("library_state", args(bad), c.meta); !failed {
					t.Fatalf("accepted unverified argument %+v: %v", bad, value)
				}
			}
			data, _ := json.Marshal(value)
			for _, private := range []string{h.env["HOME"], h.env["CODEX_HOME"], h.s.c.Directory} {
				if strings.Contains(string(data), private) {
					t.Fatal("private root in native state")
				}
			}
		})
	}
}

func TestNativeMachineDecisionAndProjectChoiceStayIndependent(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.launch("codex")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	h.env["CODEX_HOME"], h.env["HOME"] = t.TempDir(), t.TempDir()
	meta := map[string]any{"threadId": "synthetic-library-choice"}
	read, failed := h.tool("library_read", map[string]any{"item_id": "skill.pair"}, meta)
	if failed {
		t.Fatal(read)
	}
	data := read["library"].(map[string]any)
	item := data["item"].(map[string]any)
	args := map[string]any{"item_id": "skill.pair", "decision": "declined", "evidence": "synthetic exact-change decision", "catalog_digest": data["catalog_digest"], "item_digest": item["content_digest"], "if_revision": 0}
	choice, failed := h.tool("library_decision", args, meta)
	if failed {
		t.Fatal(choice)
	}
	current := choice["library"].(map[string]any)
	if current["revision"] != float64(1) {
		t.Fatal(choice)
	}
	choice, failed = h.tool("library_decision", args, meta)
	if failed || choice["library"].(map[string]any)["unchanged"] != true {
		t.Fatalf("lost reply: %v", choice)
	}
	state, failed := h.tool("library_state", map[string]any{"item_ids": []string{"skill.pair"}}, meta)
	if failed {
		t.Fatal(state)
	}
	machine := state["library"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if machine["state"] != "missing" || machine["recommended"] != false || machine["decision"].(map[string]any)["decision"] != "declined" {
		t.Fatal(machine)
	}
	state, failed = h.tool("library_state", map[string]any{"scope": "project", "item_ids": []string{"skill.pair"}}, meta)
	if failed {
		t.Fatal(state)
	}
	project := state["library"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if project["decision"] != nil || project["recommended"] != true {
		t.Fatal("machine decline suppressed project choice", project)
	}
	// Apply a synthetic approved fixture with the agent's normal editing operation,
	// verify content, then record its result. No Koinon command writes this file.
	file := filepath.Join(h.env["CODEX_HOME"], "skills", "pair", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(item["content"].(string)), 0600); err != nil {
		t.Fatal(err)
	}
	args["decision"], args["if_revision"], args["result"] = "adopted", 1, []library.TargetDigest{{ID: "skill", Digest: item["content_digest"].(string)}}
	choice, failed = h.tool("library_decision", args, meta)
	if failed {
		t.Fatal(choice)
	}
	state, failed = h.tool("library_state", map[string]any{"item_ids": []string{"skill.pair"}}, meta)
	if failed || state["library"].(map[string]any)["items"].([]any)[0].(map[string]any)["state"] != "present" {
		t.Fatal(state)
	}
	before, _ := os.ReadFile(file)
	args["evidence"] = "synthetic stale competing decision"
	args["if_revision"] = 1
	value, failed := h.tool("library_decision", args, meta)
	if !failed || value["code"] != "decision_conflict" {
		t.Fatal(value)
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("receipt refusal changed adopted fixture")
	}
}

func TestProjectLibraryReceiptsSharedAcrossListedWorktrees(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.launch("codex")
	repo := h.s.c.Directory
	git := func(args ...string) {
		t.Helper()
		options := []string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.com", "-C", repo}
		if out, err := exec.Command("git", append(options, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-m", "synthetic fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--detach", linked)
	h.env["HOME"], h.env["CODEX_HOME"] = t.TempDir(), t.TempDir()
	meta := map[string]any{"threadId": "synthetic-shared-receipt"}
	read, failed := h.tool("library_read", map[string]any{"item_id": "skill.pair"}, meta)
	if failed {
		t.Fatal(read)
	}
	catalog := read["library"].(map[string]any)
	item := catalog["item"].(map[string]any)
	inspect := func(directory string) map[string]any {
		t.Helper()
		args := map[string]any{"scope": "project", "item_ids": []string{"skill.pair"}}
		if directory != "" {
			args["directory"] = directory
		}
		value, failed := h.tool("library_state", args, meta)
		if failed {
			t.Fatal(value)
		}
		return value["library"].(map[string]any)["items"].([]any)[0].(map[string]any)
	}
	initial := inspect("")
	for _, root := range []string{repo, linked} {
		file := filepath.Join(root, "agents", "skills", "pair", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		body := item["content"].(string)
		if root == linked {
			body = "synthetic linked-worktree workflow edit"
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := library.Decision{Marker: library.DecisionMarker, Version: library.DecisionVersion, ItemID: "skill.pair", Family: "codex", Scope: "project", ScopeDigest: initial["scope_digest"].(string), CatalogDigest: catalog["catalog_digest"].(string), ItemDigest: item["content_digest"].(string), Kind: "item", Choice: "adopted", Evidence: "synthetic project outcome", Result: []library.TargetDigest{{ID: "skill", Digest: item["content_digest"].(string)}}, Adopted: []library.TargetDigest{{ID: "skill", Digest: item["content_digest"].(string)}}}
	body, _ := d.Encode()
	record, failed := h.tool("memory_record", map[string]any{"type": "decision", "body": string(body)}, meta)
	if failed {
		t.Fatal(record)
	}
	seq := record["result"].(map[string]any)["seq"]
	mainState, linkedState := inspect(""), inspect(linked)
	if mainState["state"] != "present" || linkedState["state"] != "edited" || mainState["scope_digest"] != linkedState["scope_digest"] || mainState["decision_seq"] != seq || linkedState["decision_seq"] != seq {
		t.Fatalf("worktree receipt/content conflated: main=%v linked=%v", mainState, linkedState)
	}
}

func TestDirectCodexWithoutHomeForwardingDoesNotInspectDefaultRoot(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.env["HOME"] = t.TempDir()
	defaultRoot := filepath.Join(h.env["HOME"], ".codex")
	canary := filepath.Join(defaultRoot, "skills", "pair", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(canary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canary, []byte("synthetic wrong default root"), 0600); err != nil {
		t.Fatal(err)
	}
	openedDefault := false
	h.s.c.LibraryReadRoot = func(root string) (library.ReadRoot, error) {
		if root == defaultRoot {
			openedDefault = true
		}
		return library.OpenReadRoot(root)
	}
	value, failed := h.tool("library_state", map[string]any{"item_ids": []string{"skill.pair"}}, map[string]any{"threadId": "synthetic-direct-no-forwarding"})
	if failed {
		t.Fatal(value)
	}
	state := value["library"].(map[string]any)
	found := false
	for _, reason := range state["limitations"].([]any) {
		found = found || reason == "native_root_unverified"
	}
	item := state["items"].([]any)[0].(map[string]any)
	if openedDefault || !found || item["state"] != nil || item["recommended"] != false {
		t.Fatal("unknown native home was reported as verified default", value)
	}
}
