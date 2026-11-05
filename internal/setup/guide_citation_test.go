package setup

import (
	"bytes"
	"strings"
	"testing"
)

// The guide names a decision by its reference and leaves the citation rule to the project
// (#580). It gives the record form and names no record file or record tool (#623).
func TestGuideLeavesDecisionCitationToTheProject(t *testing.T) {
	for family := range guideFamily {
		var out bytes.Buffer
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, bad := range []string{"DECISIONS.md", "Cite ", "cite the decision", "decision_record", "decision record --", "Decision record fact"} {
			if strings.Contains(text, bad) {
				t.Fatalf("%s guide holds %q", family, bad)
			}
		}
		flat := strings.Join(strings.Fields(text), " ")
		for _, want := range []string{"Name a decision by its card reference, never by verdict ID", "a verdict ID exists only in this machine's store",
			"How a project cites its decisions is that project's own rule.", "in the ADR, PDR, WDR or sprint document that holds the rule",
			"Koinon writes no record file.", "Decided by (the person's name, not a role), Date, Context (the question and the options), Decision, Reason and scope, and Review when",
			"Put a declined decision into the record's Alternatives Considered.", "koinon decision export PATH"} {
			if !strings.Contains(flat, want) {
				t.Fatalf("%s guide lacks %q", family, want)
			}
		}
	}
}
