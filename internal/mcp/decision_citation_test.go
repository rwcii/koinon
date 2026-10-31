package mcp

import (
	"strings"
	"testing"
)

// No tool description tells the project how to cite a decision; decision_record names a
// decision by its reference (#580).
func TestToolsLeaveDecisionCitationToTheProject(t *testing.T) {
	for _, tool := range toolList {
		description, _ := tool["description"].(string)
		if strings.Contains(description, "DECISIONS.md#") || strings.Contains(strings.ToLower(description), "cite") {
			t.Fatalf("%s tells the project how to cite a decision: %s", tool["name"], description)
		}
		if tool["name"] == "decision_record" && !strings.Contains(description, "name the decision by its reference, never by verdict ID") {
			t.Fatalf("decision_record: %s", description)
		}
	}
}
