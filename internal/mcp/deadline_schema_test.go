package mcp

import (
	"strings"
	"testing"
)

// Every tool whose deadline goes with a key says that the deadline is a Unix time in seconds
// and names the horizon, in both modes (#534).
func TestRetryDeadlineIsDescribed(t *testing.T) {
	seen := 0
	for mode, tools := range map[string][]map[string]any{"session": toolList, "maintainer": maintainerTools} {
		for _, tool := range tools {
			schema, _ := tool["inputSchema"].(map[string]any)
			properties, _ := schema["properties"].(map[string]any)
			deadline, ok := properties["deadline"].(map[string]any)
			if !ok {
				continue
			}
			if _, key := properties["key"]; !key {
				continue
			}
			seen++
			description, _ := deadline["description"].(string)
			if deadline["type"] != "number" || !strings.Contains(description, "Unix time in seconds") || !strings.Contains(description, "86400 s") {
				t.Errorf("%s %v: deadline %v", mode, tool["name"], deadline)
			}
		}
	}
	// room_post, memory_record, the work tools with key, decision_create, decision_close,
	// decision_withdraw and the maintainer's room_post.
	if seen < 7 {
		t.Fatalf("only %d tools with key and deadline", seen)
	}
}
