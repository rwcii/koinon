package setup

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// The card form gives the labels after Fast or complete in their order, each only when it
// has relevant data (#582, CARD-LABELS-582 in PDR 007, Decision 1).
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

// The field text at each write has the guide's form rules (CARD-FORM-AT-WRITE-635), so a
// session that lost the guide at a compaction still has them.
func TestCardFormTextMatchesTheGuide(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var text strings.Builder
	if err := Guide("claude", &text); err != nil {
		t.Fatal(err)
	}
	guide := strings.ToLower(flat(text.String()))
	for form, rules := range map[string][]string{
		core.CardTitleForm: {"The action, in one line"},
		core.CardContextForm: {"a short list of facts", "its source (a file and line, a record, a pull request or a report) and the date you checked it",
			"When the card changes an earlier direction of the maintainer, the first item names that direction and what changes",
			"Quote the maintainer only from the maintainer's own words"},
		core.CardRecommendationForm: {"one recommendation, not a menu of options",
			"**What:** the concrete change, its source and what gets recorded, with numbered steps when there are steps",
			"**Why:** the defect or the goal, from the records (recommend the fix for the defect, not for its symptom)",
			"**Fast or complete:** which one it is, each gap or limit that stays, with its source, and the cost in time, money or CI",
			"**Fix loop:** \"yes\" or \"no\", then the number of fix rounds on this deliverable so far and the references of its earlier cards",
			"\"yes\" when the card asks for another fix, retry or exception because an earlier fix of the same deliverable did not close it",
			"**On track:** \"on track\" or \"off track\", then the current task, goal or deliverable that the work belongs to, and where it was assigned or approved: an issue, a sprint chunk, a work item, a board verdict, or the maintainer's words with the date",
			"\"On track\" when all of the work in the card is part of that assigned or approved work",
			"\"Off track\" when the card asks for, or the agent already started, work that nobody assigned or approved: a side task, a scope that the agent added, or a change made \"while I am here\"",
			"the card says in plain words what the extra work is, that nobody approved it, and where the agent looked for an approval and found none",
			"Work with no approval source is off track",
			"This label is not about delay",
			"Each answer is the first text after its label, on the label's line",
			"**Corrections from the peer:** each correction from a peer who reviewed it",
			"**Open disagreements:** each position that is still open, with whose it is, or \"none\" when a peer reviewed it and no disagreement is open",
			"**Other options:** the real alternatives, a list in which each says what it keeps and what it costs",
			"**A separate choice, which you can add to any option:** a choice that is independent of the options, with its cost, what it does not solve, whether you recommend it, and the default when the maintainer does not take it",
			"Then each open point with how to handle it",
			"A recommendation that drops or defers a goal that the maintainer set says so in its heading, and names it as a limit in Fast or complete",
			"short active sentences, one idea each, and common words"},
	} {
		for _, rule := range rules {
			if !strings.Contains(form, rule) {
				t.Errorf("field text has no %q", rule)
			}
			if !strings.Contains(guide, strings.ToLower(rule)) {
				t.Errorf("guide has no %q", rule)
			}
		}
	}
}
