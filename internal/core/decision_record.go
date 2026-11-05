package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Koinon writes no record file (#623). A decision goes into the repository's own decision
// records, in the pull request that does the work; a verdict read says how.

// DecisionRecord tells the reader of a verdict how to record the decision in the repository
// (#536, #623). Entry is the current verdict and its card text, on a decision_get.
type DecisionRecord struct {
	Instruction string `json:"instruction"`
	Entry       string `json:"entry,omitempty"`
}

// decisionRecordInstruction is the record form (#623; WDR 001, Decision 7).
const decisionRecordInstruction = "When this verdict decides how the project works, record the decision in the repository that the item was created in, " +
	"in the pull request that does the work: in the ADR, PDR, WDR or sprint document that holds the rule. Koinon writes no record file. " +
	"Write it succinctly, with these parts: Decided by (the person's name, not a role), Date, Context (the question and the options), Decision, " +
	"Reason and scope, and Review when (a known condition that calls for a review of the decision; leave it out when there is none). " +
	"Correct the spelling and typing errors in quoted words and keep their meaning. Put a declined decision into the record's Alternatives Considered. " +
	"Name the decision by its card reference, never by its verdict ID, which exists only in this machine's store."

// DecisionVerdictText renders one verdict and the card text that the maintainer saw, as a
// verdict read gives it (#623). Card text is Markdown, so each field is a block quote, which
// keeps its headings out of the surrounding structure. where is the item's current text, used
// only when the verdict's snapshot has none, and marked as such.
func DecisionVerdictText(where string, v DecisionVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**, revision %d, %s.\n\n", verdictWord(v.Verdict), v.Revision,
		time.UnixMilli(v.At).UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "- Title: %s\n", oneLine(v.Snapshot.Title))
	switch {
	case v.Snapshot.Where != "":
		fmt.Fprintf(&b, "- Where: %s\n", oneLine(v.Snapshot.Where))
	case where != "":
		fmt.Fprintf(&b, "- Where (the item's text when recorded; the verdict's snapshot has none): %s\n", oneLine(where))
	}
	quoted := func(label, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n\n", label)
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				b.WriteString(">\n")
			} else {
				b.WriteString("> " + line + "\n")
			}
		}
	}
	quoted("Context", v.Snapshot.Context)
	quoted("Recommendation", v.Snapshot.Recommendation)
	if len(v.Snapshot.Commands) > 0 {
		b.WriteString("\nCommands:\n\n")
		for _, c := range v.Snapshot.Commands {
			if !printableCommand(c) {
				c = strconv.Quote(c)
			}
			b.WriteString("    " + c + "\n")
		}
	}
	// Feedback's note is the maintainer's guidance (#537).
	if v.Verdict == "feedback" {
		quoted("Feedback", v.Note)
	} else {
		quoted("Note", v.Note)
	}
	return b.String()
}

func verdictWord(verdict string) string {
	if verdict == "" {
		return verdict
	}
	return strings.ToUpper(verdict[:1]) + verdict[1:]
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
