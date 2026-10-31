package setup

import (
	"bytes"
	"strings"
	"testing"
)

// The guide names a decision by its reference and leaves the citation rule to the project
// (#580).
func TestGuideLeavesDecisionCitationToTheProject(t *testing.T) {
	for family := range guideFamily {
		var out bytes.Buffer
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if strings.Contains(text, "DECISIONS.md#") || strings.Contains(text, "Cite ") || strings.Contains(text, "cite the decision") {
			t.Fatalf("%s guide tells the project how to cite a decision", family)
		}
		for _, want := range []string{"Name a decision\nby its reference", "verdict ID exists only in this machine's store", "How a project cites its\ndecisions is that project's own rule."} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s guide lacks %q", family, want)
			}
		}
	}
}
