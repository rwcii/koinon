package setup

import (
	"bytes"
	"strings"
	"testing"
)

// The card form gives the labels after Fast or complete in their order, each only when it
// has relevant data (#582, DECISIONS.md#CARD-LABELS-582).
func TestGuideCardFormLabels(t *testing.T) {
	labels := []string{"**Corrections from the peer:**", "**Open disagreements:**", "**Other options:**",
		"**A separate choice, which you can add to any option:**", "Then each open point with how to handle it."}
	for family := range guideFamily {
		var out bytes.Buffer
		if err := Guide(family, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		start := strings.Index(text, "**Fast or complete:**")
		if start < 0 || !strings.Contains(text, "other\noptions can follow at the end, under **Other options:**.") {
			t.Fatalf("%s guide lacks the card form", family)
		}
		block := text[start:]
		if !strings.Contains(block, "give each one only when it has relevant data") ||
			!strings.Contains(block, "or \"none\" when a\n  peer reviewed it and no disagreement is open") {
			t.Fatalf("%s guide lacks the relevant-data rule", family)
		}
		at := 0
		for _, label := range labels {
			i := strings.Index(block[at:], label)
			if i < 0 {
				t.Fatalf("%s guide: %q is missing or out of order", family, label)
			}
			at += i + len(label)
		}
		for _, want := range []string{"what it keeps and what it\n  costs", "with its cost, what it does not solve, whether you recommend it, and the default\n  when the maintainer does not take it"} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s guide lacks %q", family, want)
			}
		}
	}
}
