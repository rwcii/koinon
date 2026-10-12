package mcp

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Native identity admits direct starts to messaging, repository memory and work, without
// granting participant features. Each family's existing native identity source is used.
func TestDirectStartPlainPeer(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			h.s.c.Parents = func() (map[int]int, error) { return map[int]int{4242: 1}, nil }
			h.s.c.Tmux = func(context.Context, string, ...string) (string, error) {
				t.Error("plain peer ran a terminal naming command")
				return "", nil
			}
			if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
				t.Fatalf("git: %v %s", err, out)
			}
			call := func(name string, args map[string]any) map[string]any {
				t.Helper()
				for k, v := range c.args {
					args[k] = v
				}
				reply, isError := h.tool(name, args, c.meta)
				if isError {
					t.Fatalf("%s: %v", name, reply)
				}
				return reply
			}
			call("peers", map[string]any{})
			plain := h.sessions()[0]
			if plain.Name == "" || plain.Repository == "" || plain.Address != "" || plain.Role != "" || plain.HoldsAddress || plain.Succession != nil || plain.Liveness != "none" {
				t.Fatalf("plain peer: %+v", plain)
			}
			call("send", map[string]any{"to": plain.Name, "body": "synthetic plain message"})
			inbox := call("inbox", map[string]any{})["inbox"].(map[string]any)
			messages := inbox["messages"].([]any)
			if len(messages) != 1 || messages[0].(map[string]any)["body"] != "synthetic plain message" || inbox["participant"] != nil {
				t.Fatalf("plain inbox: %v", inbox)
			}
			call("ack", map[string]any{"through": 1})
			if after := call("inbox", map[string]any{"after": 1})["inbox"].(map[string]any); after["acked_through"] != float64(1) || len(after["messages"].([]any)) != 0 {
				t.Fatalf("ack: %v", after)
			}
			call("memory_record", map[string]any{"type": "finding", "body": "synthetic plain finding"})
			page := call("memory_sync", map[string]any{})["result"].(map[string]any)
			entries := page["entries"].([]any)
			if len(entries) != 1 || entries[0].(map[string]any)["consumer"] != plain.Name {
				t.Fatalf("plain memory: %v", page)
			}
			call("memory_ack", map[string]any{"snapshot_id": page["snapshot_id"]})
			deadline := float64(time.Now().Unix() + 600)
			item := call("work_create", map[string]any{"title": "synthetic plain work", "criteria": "synthetic criteria", "non_goals": "synthetic non-goals", "key": "plain-create", "deadline": deadline})["result"].(map[string]any)
			call("work_start", map[string]any{"work_id": item["work_id"], "if_revision": 1, "checkpoint": "synthetic checkpoint", "next_artifact": "synthetic artifact", "progress_deadline": deadline, "key": "plain-start", "deadline": deadline})
			active := call("work_get", map[string]any{"work_id": item["work_id"]})["result"].(map[string]any)
			if claim := active["current_claim"].(map[string]any); claim["consumer"] != plain.Family+":"+plain.ID {
				t.Fatalf("plain work consumer: %v", claim)
			}
			h.s.renew(context.Background())
			renewed := h.sessions()[0]
			if renewed.Revision <= plain.Revision || renewed.Address != "" || renewed.Succession != nil {
				t.Fatalf("renewed plain peer: %+v", renewed)
			}
			if len(h.s.obs.naming) != 0 {
				t.Fatal("plain peer scheduled terminal naming")
			}
		})
	}
}

func TestDirectStartStillNeedsNativeIdentity(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			delete(h.env, "CLAUDE_CODE_SESSION_ID")
			delete(h.env, "GROK_SESSION_ID")
			if reply, isError := h.tool("peers", map[string]any{}, nil); !isError || reply["code"] != "identity_unavailable" || len(h.sessions()) != 0 {
				t.Fatalf("missing native identity: %v", reply)
			}
		})
	}
}

func TestPlainPeerRecoversAfterOutage(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	meta := map[string]any{"threadId": "synthetic-plain"}
	if reply, isError := h.tool("peers", map[string]any{}, meta); isError {
		t.Fatal(reply)
	}
	first := h.session("synthetic-plain")
	h.outage("synthetic-plain")
	h.s.renew(context.Background())
	again := h.session("synthetic-plain")
	if again.State != "active" || again.Name != first.Name || again.Address != "" || again.Succession != nil {
		t.Fatalf("recovered plain peer: %+v", again)
	}
	if reply, isError := h.tool("inbox", map[string]any{}, meta); isError {
		t.Fatal(reply)
	}
}
