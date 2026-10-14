package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckoutTool(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.launch("codex")
	meta := map[string]any{"threadId": "synthetic-checkout"}
	repo := h.s.c.Directory
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	first, bad := h.tool("work_checkout", map[string]any{}, meta)
	if bad || first["result"].(map[string]any)["resource"].([]any)[0] != "exact" {
		t.Fatalf("default: %v", first)
	}
	second, bad := h.tool("work_checkout", map[string]any{"directory": "sub"}, meta)
	if bad || second["result"].(map[string]any)["resource"].([]any)[1] != first["result"].(map[string]any)["resource"].([]any)[1] {
		t.Fatalf("subdirectory: %v", second)
	}
	foreign := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", foreign).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	for _, args := range []map[string]any{{"directory": foreign}, {"directory": nil}, {"directory": 1}, {"directory": ""}, {"consumer": "x"}, {"caller": "x"}} {
		if value, bad := h.tool("work_checkout", args, meta); !bad {
			t.Fatalf("accepted %v: %v", args, value)
		}
	}
	if value, bad := h.tool("work_checkout", map[string]any{}, nil); !bad || value["code"] != "identity_unavailable" {
		t.Fatalf("native identity: %v", value)
	}
	// Derivation did not bootstrap a work store or create any claim.
	list, bad := h.tool("work_list", map[string]any{}, meta)
	if bad || len(list["result"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("work changed: %v", list)
	}
}

func TestCheckoutToolWriterHandoff(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.launch("codex")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	a, b := map[string]any{"threadId": "synthetic-writer"}, map[string]any{"threadId": "synthetic-requester"}
	call := func(name string, args, meta map[string]any) map[string]any {
		t.Helper()
		value, bad := h.tool(name, args, meta)
		if bad {
			t.Fatalf("%s: %v", name, value)
		}
		return value
	}
	status := call("work_checkout", map[string]any{}, a)["result"].(map[string]any)
	resource := status["resource"]
	deadline := time.Now().Unix() + 600
	created := call("work_create", map[string]any{"title": "pair task", "criteria": "synthetic criteria", "non_goals": "synthetic non-goals",
		"key": "synthetic-checkout-create", "deadline": deadline}, a)["result"].(map[string]any)
	id := created["work_id"]
	start := func(revision any, key string, meta map[string]any) map[string]any {
		return call("work_start", map[string]any{"work_id": id, "if_revision": revision, "checkpoint": "checkpoint read; ready to drive",
			"next_artifact": "synthetic commit", "progress_deadline": deadline, "key": key, "deadline": deadline,
			"resources": []any{resource}}, meta)["result"].(map[string]any)
	}
	started := start(created["revision"], "synthetic-checkout-start", a)
	writer := call("work_checkout", map[string]any{}, b)["result"].(map[string]any)["writer"].(map[string]any)
	if writer["generation"] != started["claim"].(map[string]any)["generation"] || writer["lease_valid"] != true {
		t.Fatalf("visible token: %v", writer)
	}
	call("work_checkout_request", map[string]any{"note": "ready to drive"}, b)
	inbox := call("inbox", map[string]any{}, a)["inbox"].(map[string]any)
	requester := inbox["messages"].([]any)[0].(map[string]any)["sender_name"]
	released := call("work_release", map[string]any{"work_id": id, "if_revision": started["revision"], "claim_generation": writer["generation"],
		"checkpoint": "synthetic commit ready for pickup", "handoff_to": requester, "checkout_resource": resource.([]any)[1],
		"key": "synthetic-checkout-handoff", "deadline": deadline}, a)["result"].(map[string]any)
	if released["role_released"] != true {
		t.Fatalf("handoff: %v", released)
	}
	call("inbox", map[string]any{}, b)
	status = call("work_checkout", map[string]any{}, b)["result"].(map[string]any)
	if status["state"] != "released" {
		t.Fatalf("notification accepted the role: %v", status)
	}
	accepted := start(released["revision"], "synthetic-checkout-accept", b)
	status = call("work_checkout", map[string]any{}, a)["result"].(map[string]any)
	if status["writer"].(map[string]any)["generation"] != accepted["claim"].(map[string]any)["generation"] || status["writer"].(map[string]any)["peer"] != requester {
		t.Fatalf("accepted writer: %v", status)
	}
	for _, args := range []map[string]any{{"note": nil}, {"note": 1}, {"consumer": "other"}, {"caller": "other"}, {"directory": nil}} {
		if value, bad := h.tool("work_checkout_request", args, a); !bad {
			t.Fatalf("accepted request args %v: %v", args, value)
		}
	}
}
