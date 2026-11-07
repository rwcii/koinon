package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The card form reaches an author at each write (CARD-FORM-AT-WRITE-635 in PDR 007,
// Decision 1, #635): the tool field text and the command usage hold these rules, and the
// create and edit replies hold CardFormNotes. A session can lose the guide at a compaction;
// the tool text and the reply come with each write.
const (
	cardContextRunes        = 2500
	cardRecommendationRunes = 3000

	// CardTitleForm, CardContextForm and CardRecommendationForm are the form rules for the
	// tool field text and the command usage.
	CardTitleForm   = "The action, in one line."
	CardContextForm = "On a decision card: a short list of facts, at most 2500 characters. Each list item " +
		"has one fact, its source (a file and line, a record, a pull request or a report) and the date you " +
		"checked it. When the card changes an earlier direction of the maintainer, the first item names that " +
		"direction and what changes."
	CardRecommendationForm = "On a decision card, at most 3000 characters: the heading \"## Recommendation:\" " +
		"and the action in one line, then **What:**, **Why:** and **Fast or complete:**, in that order. Then, in " +
		"this order and each only when it has relevant data: **Corrections from the peer:**, **Open " +
		"disagreements:**, **Other options:** and **A separate choice, which you can add to any option:**. " +
		"Write short active sentences in ASD-STE100."
	// CardFormReply tells an author what form_notes in a reply means.
	CardFormReply = "The reply has form_notes when a decision card does not follow the form: correct the card " +
		"with decision_edit. A note does not refuse the card."
)

var (
	cardListItem = regexp.MustCompile(`^(?:[-*+] |[0-9]+[.)] )`)
	// cardRequired and cardOptional are the labels in their order after the heading.
	cardRequired = []string{"**What:**", "**Why:**", "**Fast or complete:**"}
	cardOptional = []string{"**Corrections from the peer:**", "**Open disagreements:**", "**Other options:**",
		"**A separate choice, which you can add to any option:**"}
)

// CardFormNotes checks the text of a decision card against the card form and says what to
// correct, or nil. The check is on the text only: a note refuses nothing and changes no
// state. Only the decision kind has the form.
func CardFormNotes(kind, context, recommendation string) []string {
	if kind != "decision" {
		return nil
	}
	notes := []string{}
	if !cardContextIsList(context) {
		notes = append(notes, "context: write a short list of facts, with one list item for each fact, its source and its date.")
	}
	if n := utf8.RuneCountInString(context); n > cardContextRunes {
		notes = append(notes, fmt.Sprintf("context: %d characters; keep it to %d or fewer.", n, cardContextRunes))
	}
	lines := strings.Split(recommendation, "\n")
	if !cardHasHeading(lines) {
		notes = append(notes, "recommendation: start with the heading \"## Recommendation:\" and the action in one line.")
	}
	last, ordered := -1, true
	for _, label := range append(append([]string{}, cardRequired...), cardOptional...) {
		at := cardLabelLine(lines, label)
		if at < 0 {
			continue
		}
		if at < last {
			ordered = false
		}
		last = at
	}
	for _, label := range cardRequired {
		if cardLabelLine(lines, label) < 0 {
			notes = append(notes, "recommendation: add "+label)
		}
	}
	if !ordered {
		notes = append(notes, "recommendation: put the labels in the order of the card form.")
	}
	if n := utf8.RuneCountInString(recommendation); n > cardRecommendationRunes {
		notes = append(notes, fmt.Sprintf("recommendation: %d characters; keep it to %d or fewer.", n, cardRecommendationRunes))
	}
	if len(notes) == 0 {
		return nil
	}
	return notes
}

// cardContextIsList reports a context with text in which each line at the left margin is a
// list item; an indented line continues its item.
func cardContextIsList(context string) bool {
	found := false
	for _, line := range strings.Split(context, "\n") {
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if !cardListItem.MatchString(line) {
			return false
		}
		found = true
	}
	return found
}

// cardHasHeading reports a first line with text that starts with the form's heading.
func cardHasHeading(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return strings.HasPrefix(line, "## Recommendation:")
		}
	}
	return false
}

// cardLabelLine is the index of the first line that starts with label, or -1.
func cardLabelLine(lines []string, label string) int {
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), label) {
			return i
		}
	}
	return -1
}
