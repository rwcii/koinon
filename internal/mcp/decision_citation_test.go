package mcp

import (
	"strings"
	"testing"
)

// No tool description tells the project how to cite a decision (#580), and none names a record
// file or a record tool (#623): decision_record is removed, and decision_get and
// decision_pending say that their record field gives the record form.
func TestToolsLeaveDecisionCitationToTheProject(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range toolList {
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		seen[name] = true
		if strings.Contains(description, "DECISIONS.md") || strings.Contains(strings.ToLower(description), "cite") ||
			strings.Contains(description, "decision_record") || strings.Contains(description, "Decision record fact") {
			t.Fatalf("%s names a record file or tool, or tells the project how to cite: %s", name, description)
		}
		if (name == "decision_get" || name == "decision_pending") && !strings.Contains(description, "how to record the decision") {
			t.Fatalf("%s does not give the record instruction: %s", name, description)
		}
	}
	if seen["decision_record"] || !seen["decision_get"] || !seen["decision_pending"] {
		t.Fatalf("tools: %v", seen)
	}
}
