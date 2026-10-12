package mcp

import (
	"strings"
	"testing"
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
		})
	}
}
