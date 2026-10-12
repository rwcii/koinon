package mcp

import (
	"github.com/rwcii/koinon/internal/core"
	"testing"
)

// Every feature that catalog metadata may require must have implemented tools. This
// guard rejects advertising context management (or another future feature) prematurely.
func TestImplementedFeatureTools(t *testing.T) {
	implemented := map[string][]string{"peer-messaging": {"peers", "send", "inbox", "ack"}, "work-claims": {"work_start", "work_update", "work_release"}}
	names := map[string]bool{}
	for _, tool := range toolList {
		names[tool["name"].(string)] = true
	}
	for _, feature := range core.SupportedFeatures() {
		tools, ok := implemented[feature]
		if !ok {
			t.Fatalf("feature %s has no implemented tool evidence", feature)
		}
		for _, name := range tools {
			if !names[name] {
				t.Fatalf("feature %s requires missing tool %s", feature, name)
			}
		}
	}
}
