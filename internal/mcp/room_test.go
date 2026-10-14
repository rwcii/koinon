package mcp

import (
	"strings"
	"testing"
	"time"
)

// room_list and room_members take the caller from native identity in every family; no
// argument names a caller, and a roster query from a non-member is refused (#217).
func TestRoomToolsEveryNativeFamily(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			with := func(args map[string]any) map[string]any {
				for k, v := range c.args {
					args[k] = v
				}
				return args
			}
			r, failed := h.tool("room_list", with(map[string]any{}), c.meta)
			if failed || r["ok"] != true || len(r["result"].(map[string]any)["rooms"].([]any)) != 0 {
				t.Fatalf("room_list: %v", r)
			}
			r, failed = h.tool("room_members", with(map[string]any{"room_id": strings.Repeat("a", 32)}), c.meta)
			if !failed || r["code"] != "not_room_member" {
				t.Fatalf("room_members of a non-member: %v", r)
			}
			for _, bad := range []map[string]any{{"caller": "other"}, {"family": "codex"}, {"limit": 101}} {
				if _, failed := h.tool("room_list", with(bad), c.meta); !failed {
					t.Fatalf("room_list accepted %v", bad)
				}
			}
			if _, failed := h.tool("room_members", with(map[string]any{}), c.meta); !failed {
				t.Fatal("room_members without room_id")
			}
			room := strings.Repeat("a", 32)
			post := map[string]any{"room_id": room, "body": "synthetic", "key": "k", "deadline": 1e12}
			if r, failed := h.tool("room_post", with(map[string]any{"room_id": room, "body": "synthetic", "key": "k", "deadline": float64(time.Now().Unix() + 600)}), c.meta); !failed || r["code"] != "not_room_member" {
				t.Fatalf("room_post by a non-member: %v", r)
			}
			for _, bad := range []string{"key", "deadline", "body"} {
				args := with(map[string]any{})
				for k, v := range post {
					if k != bad {
						args[k] = v
					}
				}
				if _, failed := h.tool("room_post", args, c.meta); !failed {
					t.Fatalf("room_post without %s", bad)
				}
			}
			if _, failed := h.tool("room_post", with(map[string]any{"room_id": room, "body": "x", "key": "k", "deadline": 1e12, "origin": "maintainer"}), c.meta); !failed {
				t.Fatal("room_post accepted an origin argument")
			}
			if r, failed := h.tool("room_read", with(map[string]any{"room_id": room}), c.meta); !failed || r["code"] != "not_room_member" {
				t.Fatalf("room_read by a non-member: %v", r)
			}
		})
	}
}
