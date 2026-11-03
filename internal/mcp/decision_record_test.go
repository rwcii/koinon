package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// decision_record writes only into a checkout of the session's own repository, refuses unknown
// and missing arguments, applies the checkout's record mode, and creates no file for an item
// without a recordable verdict (#536, #589).
func TestDecisionRecordTool(t *testing.T) {
	c := callers[0]
	h := c.harness(t)
	with := func(args map[string]any) map[string]any {
		for k, v := range c.args {
			args[k] = v
		}
		return args
	}
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	// A launched session in a repository holds its participant, so its items have a repository.
	h.launch(c.family)
	other := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	deadline := float64(time.Now().Unix() + 600)
	if r, failed := h.tool("decision_create", with(map[string]any{"reference": "R-1", "title": "Synthetic", "kind": "decision", "key": "k", "deadline": deadline}), c.meta); failed {
		t.Fatalf("create: %v", r)
	}
	for _, args := range []map[string]any{{}, {"reference": "R-1", "verdict": "approved"}, {"reference": "R-1", "directory": ""}} {
		if r, failed := h.tool("decision_record", with(args), c.meta); !failed || r["code"] != "invalid_arguments" {
			t.Fatalf("%v: %v", args, r)
		}
	}
	for _, directory := range []string{other, nonRepositoryDir(t)} {
		if r, failed := h.tool("decision_record", with(map[string]any{"reference": "R-1", "directory": directory}), c.meta); !failed || r["code"] != "repo_unresolved" {
			t.Fatalf("directory %s: %v", directory, r)
		}
	}
	if r, failed := h.tool("decision_record", with(map[string]any{"reference": "R-404"}), c.meta); !failed || r["code"] != "not_found" {
		t.Fatalf("unknown item: %v", r)
	}
	// Without a facts file the mode is none (#589).
	r, failed := h.tool("decision_record", with(map[string]any{"reference": "R-1"}), c.meta)
	if failed || r["ok"] != true || r["result"].(map[string]any)["mode"] != "none" || len(r["result"].(map[string]any)["appended"].([]any)) != 0 {
		t.Fatalf("default none: %v", r)
	}
	facts := "# Repository facts — ship\n\n## Version\n1\n## Skill\nship\n## Sources\n- AGENTS.md\n\n## Work target\nx\n## Commit policy\nx\n" +
		"## Required checks\nx\n## PR and merge policy\nx\n## Decision record\n"
	os.MkdirAll(filepath.Join(h.s.c.Directory, ".koinon", "skills"), 0o755)
	write := func(mode string) {
		if err := os.WriteFile(filepath.Join(h.s.c.Directory, ".koinon", "skills", "ship.md"), []byte(facts+mode+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sometimes")
	if r, failed := h.tool("decision_record", with(map[string]any{"reference": "R-1"}), c.meta); !failed || r["code"] != "decision_record_mode_invalid" {
		t.Fatalf("invalid mode: %v", r)
	}
	write("short")
	r, failed = h.tool("decision_record", with(map[string]any{"reference": "R-1"}), c.meta)
	if failed || r["ok"] != true || r["result"].(map[string]any)["mode"] != "short" || len(r["result"].(map[string]any)["appended"].([]any)) != 0 {
		t.Fatalf("no verdict: %v", r)
	}
	if _, err := os.Lstat(filepath.Join(h.s.c.Directory, "DECISIONS.md")); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict created the file: %v", err)
	}
}
