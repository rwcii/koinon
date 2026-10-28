package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// decision_record writes only into a checkout of the session's own repository, refuses unknown
// and missing arguments, and creates no file for an item without a recordable verdict (#536).
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
	r, failed := h.tool("decision_record", with(map[string]any{"reference": "R-1"}), c.meta)
	if failed || r["ok"] != true || len(r["result"].(map[string]any)["appended"].([]any)) != 0 {
		t.Fatalf("no verdict: %v", r)
	}
	if _, err := os.Lstat(filepath.Join(h.s.c.Directory, "DECISIONS.md")); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict created the file: %v", err)
	}
}
