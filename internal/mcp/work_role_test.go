package mcp

import (
	"os/exec"
	"testing"
	"time"
)

// Each native MCP adapter carries the typed proposal and derives ownership from
// native metadata; none takes a model-supplied actor or changes the legacy path.
func TestWorkRoleToolsAllFamilies(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			h.launch(c.family)
			if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			call := func(name string, args map[string]any) map[string]any {
				t.Helper()
				for k, v := range c.args {
					args[k] = v
				}
				out, bad := h.tool(name, args, c.meta)
				if bad {
					t.Fatalf("%s: %v", name, out)
				}
				return out["result"].(map[string]any)
			}
			deadline := float64(time.Now().Unix() + 600)
			first := call("work_create", map[string]any{"title": "synthetic role", "criteria": "c", "non_goals": "n", "key": "role-create",
				"deadline": deadline, "proposed_role": map[string]any{"family": c.family, "role": ""}})
			id := first["work_id"].(string)
			call("work_create", map[string]any{"title": "synthetic second", "criteria": "c", "non_goals": "n", "key": "second-create",
				"deadline": deadline, "proposed_role": map[string]any{"family": c.family, "role": ""}})
			page := call("work_list", map[string]any{"assigned_to_me": true, "limit": 1})
			if page["more"] != true || page["next_after"] != id {
				t.Fatalf("first assignment page: %v", page)
			}
			page = call("work_list", map[string]any{"assigned_to_me": true, "after": page["next_after"], "limit": 1})
			if page["more"] != false || len(page["items"].([]any)) != 1 {
				t.Fatalf("final assignment page: %v", page)
			}
			started := call("work_start", map[string]any{"work_id": id, "if_revision": 1, "checkpoint": "c", "next_artifact": "a",
				"progress_deadline": deadline, "key": "role-start", "deadline": deadline})
			item := call("work_get", map[string]any{"work_id": id})
			claim := item["current_claim"].(map[string]any)
			if claim["ownership"] != "native" || claim["consumer"] == "participant:"+h.sessions()[0].Address ||
				item["role_resolution"].(map[string]any)["state"] != "held" {
				t.Fatalf("native role claim: %v", item)
			}
			generation := started["claim"].(map[string]any)["generation"]
			call("work_propose", map[string]any{"work_id": id, "if_revision": 2, "proposed_role": nil})
			call("work_release", map[string]any{"work_id": id, "if_revision": 3, "claim_generation": generation,
				"checkpoint": "synthetic role release", "key": "role-release", "deadline": deadline})
			if retry := call("work_release", map[string]any{"work_id": id, "if_revision": 3, "claim_generation": generation,
				"checkpoint": "synthetic role release", "key": "role-release", "deadline": deadline}); retry["duplicate"] != true {
				t.Fatal("native release retry failed")
			}
			args := map[string]any{"work_id": id, "native_actor": "synthetic-forgery"}
			for k, v := range c.args {
				args[k] = v
			}
			if _, bad := h.tool("work_get", args, c.meta); !bad {
				t.Fatal("accepted a model-supplied actor")
			}
		})
	}
}
