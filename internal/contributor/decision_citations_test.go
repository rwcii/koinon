package contributor

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Tracked documents cite a decision board verdict as DECISIONS.md#REFERENCE, never by its
// verdict ID, and each cited reference has its anchor in DECISIONS.md (#536). The same script
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
	record := read(t, "DECISIONS.md")
	cite := regexp.MustCompile(`DECISIONS\.md#([A-Za-z0-9-]+)`)
	// GitHub writes every anchor in lower case, so a link's target is the lower-case reference.
	link := regexp.MustCompile(`\]\([./]*DECISIONS\.md#([^)]*)\)`)
	for _, name := range strings.Fields(string(out)) {
		text := read(t, name)
		if found, ok := check(text); !ok {
			t.Errorf("%s cites a verdict ID:\n%s", name, found)
		}
		if name == "DECISIONS.md" {
			continue
		}
		for _, m := range cite.FindAllStringSubmatch(text, -1) {
			if m[1] != "REFERENCE" && !strings.Contains(record, `<a id="`+strings.ToLower(m[1])+`"></a>`) {
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
	for _, text := range []string{"(verdict 66)", "Verdict 7 approved it.", "verdicts 46 and 47", "verdict ID 12", "verdict #3"} {
		if _, ok := check(text + "\n"); ok {
			t.Errorf("not refused: %q", text)
		}
	}
	for _, text := range []string{"DECISIONS.md#PICKUP-WAIT-525", "the verdict was approved", "a verdict ID exists only in one store"} {
		if found, ok := check(text + "\n"); !ok {
			t.Errorf("refused %q: %s", text, found)
		}
	}
	// The hook and the workflow run the same script.
	if hook := read(t, ".githooks/commit-msg"); !strings.Contains(hook, "scripts/decision-citations.sh") {
		t.Error("the commit-msg hook does not run the check")
	}
	if workflow := read(t, ".github/workflows/pr-text.yml"); !strings.Contains(workflow, "sh scripts/decision-citations.sh") ||
		!strings.Contains(workflow, "decision-citations:") || !strings.Contains(workflow, "edited") {
		t.Error("the pull request text workflow does not run the check on each edit")
	}
}
