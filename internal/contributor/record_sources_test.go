package contributor

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cardSource finds a decision board card named as a source, such as "board card
// TAILSCALE-AUTO-656" or "(card LINK-693-SIGNED, revision 10", also when the line wraps
// between the word and the reference (#723). A label inside a record, such as
// "READY-TIMEOUT-ENABLE-487 in PDR 006, Decision 2", names the record and is not a match.
var cardSource = regexp.MustCompile("(?i)\\bcards?\\s+`?[a-z0-9]+(?:-[a-z0-9]+)+")

// cardSources joins the lines of each paragraph, so that a wrapped reference is found and a
// heading such as "## Scope cards" does not join the next paragraph.
func cardSources(text string) []string {
	var found []string
	for _, paragraph := range regexp.MustCompile(`\n[ \t]*\n`).Split(text, -1) {
		found = append(found, cardSource.FindAllString(strings.Join(strings.Fields(paragraph), " "), -1)...)
	}
	return found
}

// The repository record holds a decision and is its source: no decision record or sprint
// document names a board card as a source (WDR 001, Decision 7; #723). This scan is a
// regression guard for the known forms, not proof that every sentence has a source.
func TestRecordsNameNoCardAsSource(t *testing.T) {
	for _, text := range []string{
		"**Decided by:** Robert Capps, 2026-11-12, board card TAILSCALE-AUTO-656 (#656).",
		"Status: approved by Robert Capps on 2026-11-15 (card LINK-693-SIGNED, revision 10, at 4e324d5;",
		"- **Decided by:** Robert Capps, 2026-11-07, on the decision board, card\n  CARD-FORM-AT-WRITE-635, revision 3",
		"**Decided by:** Robert Capps, 2026-11-16, board card k685-remote-configure (approved, revision",
		"for the live check (card `LINK-670-LIVE`, revision 2)",
	} {
		if len(cardSources(text)) != 1 {
			t.Errorf("not found as a card source: %q", text)
		}
	}
	for _, text := range []string{
		"READY-TIMEOUT-ENABLE-487 in PDR 006, Decision 2",
		"**Decided by:** Robert Capps, 2026-11-12, on the decision board (#656).",
		"PDR 011 Decision 6 (LINK-693-SIGNED) records its approval.",
		"each of its 25 card references is in an ADR, PDR or WDR",
		"a card reference or a verdict ID",
		"## Scope cards\n\ncodex-koinon reviewed each scope card",
	} {
		if found := cardSources(text); len(found) != 0 {
			t.Errorf("a record citation found as a card source: %q: %v", text, found)
		}
	}
	all, err := exec.Command("git", "-C", root(t), "ls-files", "docs/architecture/adr", "docs/product/pdr", "docs/engineering/wdr", "docs/sprints").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Fields(string(all)) {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root(t), name))
		if err != nil {
			t.Fatal(err)
		}
		if found := cardSources(string(data)); len(found) != 0 {
			t.Errorf("%s names a board card as a source: %v", name, found)
		}
	}
}
