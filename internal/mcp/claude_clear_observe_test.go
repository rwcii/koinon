package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeRegistryAfterClearRequiresPinnedHost(t *testing.T) {
	o := newObserveHarness(t, "claude-code", "/opt/agent/bin/claude")
	start := int64(42)
	o.s.c.ProcessStart = func(int) (int64, error) { return start, nil }
	// This fixture initialized before installing the synthetic process reader. Restart
	// only the fixture's initialization state to exercise the real initial pinning path.
	o.s.claudeHostChecked = false
	o.request("initialize", map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	config := t.TempDir()
	o.env["CLAUDE_CONFIG_DIR"] = config
	o.env["CLAUDE_CODE_SESSION_ID"] = "synthetic-before-clear"
	os.MkdirAll(filepath.Join(config, "sessions"), 0700)
	record := filepath.Join(config, "sessions", "4242.json")
	data, _ := json.Marshal(map[string]any{"pid": 4242, "sessionId": "synthetic-after-clear",
		"status": "idle", "statusUpdatedAt": time.Now().Add(-time.Second).UnixMilli(), "entrypoint": "cli"})
	if err := os.WriteFile(record, data, 0600); err != nil {
		t.Fatal(err)
	}
	o.tool("peers", map[string]any{}, nil)
	o.s.observeOnce(context.Background())
	reports := o.taken(1)
	if len(reports) != 1 || reports[0].Caller.ID != "synthetic-before-clear" || reports[0].Activity == nil || reports[0].Activity.State != "idle" {
		t.Fatalf("post-clear observation lost or identity moved: %+v", reports)
	}
	// Reusing the PID, including another initialization, never refreshes the pin.
	start++
	o.request("initialize", map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	if o.s.claudeActivity("synthetic-before-clear") != nil {
		t.Fatal("reused host PID accepted")
	}
	o.s.c.ProcessStart = func(int) (int64, error) { return 0, errors.New("unreadable") }
	if o.s.claudeActivity("synthetic-before-clear") != nil {
		t.Fatal("unreadable host accepted")
	}
	o.s.claudeHostStart = 0
	if o.s.claudeActivity("synthetic-before-clear") != nil {
		t.Fatal("changed ID without pinned host accepted")
	}
}
