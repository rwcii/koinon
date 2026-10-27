package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// The own-cycle tools take no epoch, caller, host or pane from the model: the native client
// attaches the epoch. Claude's epoch is the transcript in the registry record of the pinned
// host; a reused host PID or an unreadable host gives none.
func TestCycleToolsAttachNativeEpoch(t *testing.T) {
	o := newObserveHarness(t, "claude-code", "/opt/agent/bin/claude")
	start := int64(42)
	o.s.c.ProcessStart = func(int) (int64, error) { return start, nil }
	o.s.claudeHostChecked = false
	o.request("initialize", map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	config := t.TempDir()
	o.env["CLAUDE_CONFIG_DIR"] = config
	o.env["CLAUDE_CODE_SESSION_ID"] = "synthetic-before-clear"
	os.MkdirAll(filepath.Join(config, "sessions"), 0700)
	data, _ := json.Marshal(map[string]any{"pid": 4242, "sessionId": "synthetic-after-clear",
		"status": "idle", "statusUpdatedAt": time.Now().Add(-time.Second).UnixMilli(), "entrypoint": "cli"})
	if err := os.WriteFile(filepath.Join(config, "sessions", "4242.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"cycle_id": "c", "revision": 1, "artifact": "a.md", "epoch": "synthetic-model-epoch"},
		{"cycle_id": "c", "revision": 1, "artifact": "a.md", "session_id": "other"},
		{"cycle_id": "c", "revision": 1, "artifact": "a.md", "outcome": "resumed"},
		{"cycle_id": "c", "revision": 1},
	} {
		if reply, isError := o.tool("context_cycle_ready", args, nil); !isError || reply["code"] != "invalid_arguments" && reply["code"] != "identity_unavailable" {
			t.Fatalf("ready %v: %v", args, reply)
		}
	}
	if reply, isError := o.tool("context_cycle_pickup", map[string]any{"cycle_id": "c", "revision": 1, "artifact": "a.md"}, nil); !isError || reply["code"] != "invalid_arguments" {
		t.Fatalf("pickup without outcome: %v", reply)
	}
	if got := o.s.cycleEpoch(claudeKey("synthetic-before-clear")); got != "synthetic-after-clear" {
		t.Fatalf("claude epoch: %q", got)
	}
	start++
	if got := o.s.cycleEpoch(claudeKey("synthetic-before-clear")); got != "" {
		t.Fatalf("reused host PID gave epoch %q", got)
	}
	if got := o.s.cycleEpoch(codexKey("synthetic-thread")); got != "synthetic-thread" {
		t.Fatalf("codex epoch: %q", got)
	}
	// agy's epoch is the call's own conversation, which /clear replaces (#512).
	if got := o.s.cycleEpoch(agyKey("synthetic")); got != "synthetic" {
		t.Fatalf("agy epoch: %q", got)
	}
	// OpenCode's epoch is the call's own session, which the cycle replaces (#513).
	if got := o.s.cycleEpoch(core.Key{Family: "opencode", ID: "ses_synthetic"}); got != "ses_synthetic" {
		t.Fatalf("opencode epoch: %q", got)
	}
	if got := o.s.cycleEpoch(core.Key{Family: "grok", ID: "synthetic"}); got != "" {
		t.Fatalf("grok epoch: %q", got)
	}
}

func claudeKey(id string) core.Key { return core.Key{Family: "claude", ID: id} }
func codexKey(id string) core.Key  { return core.Key{Family: "codex", ID: id} }
func agyKey(id string) core.Key    { return core.Key{Family: "agy", ID: id} }
