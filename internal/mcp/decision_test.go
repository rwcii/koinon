package mcp

import (
	"fmt"
	"testing"
	"time"
)

// The decision tools take the caller from native identity in every family; unknown and
// missing arguments, including verdict and agent-row fields, are refused by name (#206).
func TestDecisionToolsEveryNativeFamily(t *testing.T) {
	for i, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			with := func(args map[string]any) map[string]any {
				for k, v := range c.args {
					args[k] = v
				}
				return args
			}
			reference := fmt.Sprintf("T-%d", i)
			deadline := float64(time.Now().Unix() + 600)
			r, failed := h.tool("decision_create", with(map[string]any{"reference": reference, "title": "Synthetic " + c.family,
				"kind": "decision", "commands": []string{"go test ./..."}, "key": "k", "deadline": deadline}), c.meta)
			if failed || r["ok"] != true || r["result"].(map[string]any)["revision"] != 1.0 {
				t.Fatalf("create: %v", r)
			}
			r, failed = h.tool("decision_edit", with(map[string]any{"reference": reference, "if_revision": 1, "order": 2}), c.meta)
			if failed || r["result"].(map[string]any)["revision"] != 2.0 {
				t.Fatalf("edit: %v", r)
			}
			r, failed = h.tool("decision_get", with(map[string]any{"reference": reference}), c.meta)
			if failed || r["result"].(map[string]any)["family"] != nil || r["result"].(map[string]any)["author_family"] != c.family {
				t.Fatalf("get: %v", r)
			}
			r, failed = h.tool("decision_list", with(map[string]any{"filter": "waiting"}), c.meta)
			if failed || len(r["result"].(map[string]any)["items"].([]any)) != 1 {
				t.Fatalf("list: %v", r)
			}
			for tool, args := range map[string]map[string]any{
				"decision_edit":   {"reference": reference, "if_revision": 2, "verdict": "approved"},
				"decision_create": {"reference": "X-1", "title": "t", "kind": "fyi", "key": "k2", "deadline": deadline, "caller": "other"},
				"decision_list":   {"filter": "waiting", "activity": "idle"},
				"decision_get":    {"reference": reference, "audit": true},
			} {
				// A caller argument is refused before the argument check, as identity_unavailable.
				if r, failed := h.tool(tool, with(args), c.meta); !failed || (r["code"] != "invalid_arguments" && args["caller"] == nil) {
					t.Fatalf("%s accepted %v: %v", tool, args, r)
				}
			}
			for tool, args := range map[string]map[string]any{
				"decision_create": {"reference": "X-1", "title": "t", "kind": "fyi", "key": "k2"},
				"decision_edit":   {"reference": reference, "title": "x"},
				"decision_list":   {},
				"decision_get":    {},
			} {
				if r, failed := h.tool(tool, with(args), c.meta); !failed || r["code"] != "invalid_arguments" {
					t.Fatalf("%s without a required argument: %v", tool, r)
				}
			}
		})
	}
}

// The chunk 02 decision tools reach the daemon in every native family, and no tool or
// argument can give a verdict.
func TestDecisionVerdictToolsEveryNativeFamily(t *testing.T) {
	for i, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			with := func(args map[string]any) map[string]any {
				for k, v := range c.args {
					args[k] = v
				}
				return args
			}
			reference := fmt.Sprintf("U-%d", i)
			deadline := float64(time.Now().Unix() + 600)
			if r, failed := h.tool("decision_create", with(map[string]any{"reference": reference, "title": "t", "kind": "decision", "key": "k", "deadline": deadline}), c.meta); failed {
				t.Fatalf("create: %v", r)
			}
			if r, failed := h.tool("decision_history", with(map[string]any{"reference": reference}), c.meta); failed || len(r["result"].(map[string]any)["verdicts"].([]any)) != 0 {
				t.Fatalf("history: %v", r)
			}
			if r, failed := h.tool("decision_pending", with(map[string]any{}), c.meta); failed || len(r["result"].(map[string]any)["deliveries"].([]any)) != 0 {
				t.Fatalf("pending: %v", r)
			}
			if r, failed := h.tool("decision_ack", with(map[string]any{"verdict_ids": []int{1}}), c.meta); failed || len(r["result"].(map[string]any)["acknowledged"].([]any)) != 0 {
				t.Fatalf("ack: %v", r)
			}
			if r, failed := h.tool("decision_close", with(map[string]any{"reference": reference, "if_revision": 1, "key": "k", "deadline": deadline}), c.meta); !failed || r["code"] != "not_in_progress" {
				t.Fatalf("close: %v", r)
			}
			if r, failed := h.tool("decision_restore", with(map[string]any{"reference": reference}), c.meta); !failed || r["code"] != "not_found" {
				t.Fatalf("restore: %v", r)
			}
			// The author withdraws the item and reopens it (#504).
			if r, failed := h.tool("decision_withdraw", with(map[string]any{"reference": reference, "if_revision": 1, "key": "k", "deadline": deadline}), c.meta); !failed || r["code"] != "invalid_arguments" {
				t.Fatalf("withdraw without a reason: %v", r)
			}
			if r, failed := h.tool("decision_withdraw", with(map[string]any{"reference": reference, "if_revision": 1, "reason": "done", "key": "k", "deadline": deadline}), c.meta); failed ||
				r["result"].(map[string]any)["state"] != "archived" || r["result"].(map[string]any)["withdrawn"] != true {
				t.Fatalf("withdraw: %v", r)
			}
			if r, failed := h.tool("decision_restore", with(map[string]any{"reference": reference, "if_revision": 1}), c.meta); failed ||
				r["result"].(map[string]any)["state"] != "active" || r["result"].(map[string]any)["revision"] != float64(2) {
				t.Fatalf("reopen: %v", r)
			}
			if r, failed := h.tool("decision_history", with(map[string]any{"reference": reference, "withdrawals": true}), c.meta); failed ||
				len(r["result"].(map[string]any)["withdrawals"].([]any)) != 1 {
				t.Fatalf("withdrawal history: %v", r)
			}
			for tool, args := range map[string]map[string]any{
				"decision_withdraw": {"reference": reference, "if_revision": 2, "reason": "done", "key": "k2", "deadline": deadline, "verdict": "approved"},
				"decision_ack":      {"verdict_ids": []int{1}, "verdict": "approved"},
				"decision_close":    {"reference": reference, "if_revision": 1, "key": "k", "deadline": deadline, "verdict": "approved"},
				"decision_pending":  {"name": "other"},
			} {
				if r, failed := h.tool(tool, with(args), c.meta); !failed || r["code"] != "invalid_arguments" {
					t.Fatalf("%s accepted %v: %v", tool, args, r)
				}
			}
			for _, tool := range []string{"decision_verdict", "decision_approve", "decision_dismiss"} {
				if r, failed := h.tool(tool, with(map[string]any{"reference": reference}), c.meta); !failed || r["code"] != "unknown_tool" {
					t.Fatalf("%s: %v", tool, r)
				}
			}
		})
	}
}
