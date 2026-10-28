package contributor

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// recordEntries are the references, in lower case, that DECISIONS.md holds an entry for: a
// whole-line anchor and a whole-line verdict marker of the same reference. Card text is quoted
// line by line and commands are indented, so an anchor or marker inside them is not a line of
// its own and does not count.
func recordEntries(record string) map[string]bool {
	anchor := regexp.MustCompile(`^<a id="([a-z0-9-]+)"></a>$`)
	marker := regexp.MustCompile(`^<!-- koinon-decision ([A-Za-z0-9-]+) revision [0-9]+ [a-z]+ [0-9]+ -->$`)
	anchors, markers := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(record, "\n") {
		if m := anchor.FindStringSubmatch(line); m != nil {
			anchors[m[1]] = true
		}
		if m := marker.FindStringSubmatch(line); m != nil {
			markers[strings.ToLower(m[1])] = true
		}
	}
	entries := map[string]bool{}
	for reference := range anchors {
		if markers[reference] {
			entries[reference] = true
		}
	}
	return entries
}

// verdictIDExempt are the files whose text the verdict ID scan does not read: the record
// quotes card text and the maintainer's words as given, and the changelog keeps its released
// entries as they were.
var verdictIDExempt = map[string]bool{"DECISIONS.md": true, "CHANGELOG.md": true}

// Tracked documents cite a decision board verdict as DECISIONS.md#REFERENCE, never by its
// verdict ID, and each cited reference has its entry in DECISIONS.md (#536). The same script
// checks pull request text in CI and commit messages in the commit-msg hook.
func TestDecisionCitationsResolve(t *testing.T) {
	out, err := exec.Command("git", "-C", root(t), "ls-files", "*.md").Output()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root(t), "scripts/decision-citations.sh")
	check := func(text string) (string, bool) {
		cmd := exec.Command("sh", script)
		cmd.Stdin = strings.NewReader(text)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.String(), err == nil
	}
	entries := recordEntries(read(t, "DECISIONS.md"))
	cite := regexp.MustCompile(`DECISIONS\.md#([A-Za-z0-9-]+)`)
	// GitHub writes every anchor in lower case, so a link's target is the lower-case reference.
	link := regexp.MustCompile(`\]\([./]*DECISIONS\.md#([^)]*)\)`)
	for _, name := range strings.Fields(string(out)) {
		text := read(t, name)
		if name == "DECISIONS.md" {
			continue
		}
		if found, ok := check(text); !ok && !verdictIDExempt[name] {
			t.Errorf("%s cites a verdict ID:\n%s", name, found)
		}
		for _, m := range cite.FindAllStringSubmatch(text, -1) {
			if m[1] != "REFERENCE" && !entries[strings.ToLower(m[1])] {
				t.Errorf("%s cites DECISIONS.md#%s, which has no entry", name, m[1])
			}
		}
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			if m[1] != strings.ToLower(m[1]) {
				t.Errorf("%s links DECISIONS.md#%s; GitHub's anchor is lower case", name, m[1])
			}
		}
	}
	// The check finds each form of a verdict ID and passes a record citation.
	for _, text := range []string{"(verdict 66)", "Verdict 7 approved it.", "verdicts 46 and 47", "verdict ID 12", "verdict #3", "VERDICT 5", "Verdict Id 9"} {
		if _, ok := check(text + "\n"); ok {
			t.Errorf("not refused: %q", text)
		}
	}
	for _, text := range []string{"DECISIONS.md#PICKUP-WAIT-525", "the verdict was approved", "a verdict ID exists only in one store"} {
		if found, ok := check(text + "\n"); !ok {
			t.Errorf("refused %q: %s", text, found)
		}
	}
	// An entry needs its own anchor and marker lines; a quoted anchor or marker in card text
	// is no entry.
	sample := "# Decisions\n\n<a id=\"real-1\"></a>\n\n## REAL-1\n\n<!-- koinon-decision REAL-1 revision 1 approved 5 -->\n\n" +
		"Context:\n\n> <a id=\"quoted-1\"></a>\n> <!-- koinon-decision QUOTED-1 revision 1 approved 5 -->\n> Do not cite verdict 5.\n\n" +
		"<a id=\"anchor-only\"></a>\n\n    <!-- koinon-decision ANCHOR-ONLY revision 1 approved 5 -->\n"
	if got := recordEntries(sample); len(got) != 1 || !got["real-1"] {
		t.Errorf("entries: %v", got)
	}
	// The hook runs the same script on every line above the scissors line of commit
	// --verbose, comment lines too, because a cleanup mode can keep them.
	hook := read(t, ".githooks/commit-msg")
	if !strings.Contains(hook, "scripts/decision-citations.sh") {
		t.Error("the commit-msg hook does not run the check")
	}
	for message, refused := range map[string]bool{"Synthetic\n\n# verdict 5\n": true, "Synthetic VERDICT 5\n": true, "Synthetic\n": false,
		"Synthetic\n# ------------------------ >8 ------------------------\n+verdict 5\n": false} {
		file := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
		if err := os.WriteFile(file, []byte(message), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", filepath.Join(root(t), ".githooks/commit-msg"), file)
		cmd.Dir = root(t)
		if err := cmd.Run(); (err != nil) != refused {
			t.Errorf("hook on %q: %v", message, err)
		}
	}
	if workflow := read(t, ".github/workflows/pr-text.yml"); !strings.Contains(workflow, "sh scripts/decision-citations.sh") ||
		!strings.Contains(workflow, "decision-citations:") || !strings.Contains(workflow, "edited") {
		t.Error("the pull request text workflow does not run the check on each edit")
	}
}
