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

// retiredRecordReferences are the card references of the entries of the retired record file
// (#623). Each decision now lives in the ADR, PDR or WDR that holds its rule (WDR 001,
// Decision 7), and documents cite it by that record and its card reference.
var retiredRecordReferences = []string{"493-1", "493-2", "493-3", "493-4", "493-5", "493-6", "COMPACT-ENABLE-509",
	"PICKUP-WAIT-525", "DECISION-RECORDS-536", "WORK-LIMIT-529", "SUBAGENT-CLEAR-533", "BOARD-VERDICTS-537", "CLI-RULES-550",
	"SPEAK-SCOPE-553", "CARD-FORM-562", "IDLE-COMPACT-564", "CLAUDE-IDLE-566", "PULLED-IDLE-569", "GROK-IDLE-568",
	"AGY-IDLE-568", "CARD-LABELS-582", "CYCLE-NO-NEW-WORK-487", "READY-TIMEOUT-ENABLE-487", "RESTORE-STEPS-487", "PICKUP-RESET-588"}

// retiredFileName is the record file that Koinon no longer writes or keeps (#623).
var retiredFileName = "DECISIONS" + ".md"

// retiredFileNamed reports whether a tracked file can still name the retired record file: the
// changelog keeps its released entries as they were, WDR 001 records the decision that retired
// the file with Robert Capps's words, and the tests that refuse the name must name it.
func retiredFileNamed(name string) bool {
	return name == "CHANGELOG.md" || name == "docs/engineering/wdr/001-decision-records-govern-constraints.md" || strings.HasSuffix(name, "_test.go")
}

// Tracked documents name a decision board verdict by its card reference, never by its
// verdict ID (#536). The retired record file is gone, no other tracked file names it, and each
// of its card references is in an ADR, PDR or WDR (#623). The same script checks pull request
// text in CI and commit messages in the commit-msg hook.
func TestDecisionCitationsResolve(t *testing.T) {
	script := filepath.Join(root(t), "scripts/decision-citations.sh")
	check := func(text string) (string, bool) {
		cmd := exec.Command("sh", script)
		cmd.Stdin = strings.NewReader(text)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.String(), err == nil
	}
	if _, err := os.Lstat(filepath.Join(root(t), retiredFileName)); !os.IsNotExist(err) {
		t.Fatalf("%s still exists: %v", retiredFileName, err)
	}
	docs, err := exec.Command("git", "-C", root(t), "ls-files", "*.md").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Fields(string(docs)) {
		if found, ok := check(read(t, name)); !ok && name != "CHANGELOG.md" {
			t.Errorf("%s cites a verdict ID:\n%s", name, found)
		}
	}
	all, err := exec.Command("git", "-C", root(t), "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	record := regexp.MustCompile(`^docs/(architecture/adr|product/pdr|engineering/wdr)/[0-9]{3}-`)
	records := ""
	for _, name := range strings.Fields(string(all)) {
		data, err := os.ReadFile(filepath.Join(root(t), name))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(retiredFileName)) && !retiredFileNamed(name) {
			t.Errorf("%s names %s", name, retiredFileName)
		}
		if record.MatchString(name) {
			records += string(data)
		}
	}
	for _, reference := range retiredRecordReferences {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(reference) + `\b`).MatchString(records) {
			t.Errorf("no ADR, PDR or WDR holds %s", reference)
		}
	}
	// The check finds each form of a verdict ID and passes a record citation.
	for _, text := range []string{"(verdict 66)", "Verdict 7 approved it.", "verdicts 46 and 47", "verdict ID 12", "verdict #3", "VERDICT 5", "Verdict Id 9"} {
		if _, ok := check(text + "\n"); ok {
			t.Errorf("not refused: %q", text)
		}
	}
	for _, text := range []string{"PICKUP-WAIT-525 in PDR 006, Decision 3", "the verdict was approved", "a verdict ID exists only in one store"} {
		if found, ok := check(text + "\n"); !ok {
			t.Errorf("refused %q: %s", text, found)
		}
	}
	hook := read(t, ".githooks/commit-msg")
	if !strings.Contains(hook, "scripts/decision-citations.sh") {
		t.Error("the commit-msg hook does not run the check")
	}
	// The hook refuses everything that a real commit can keep: comment lines that commit -F
	// keeps, a scissors line that commit -F keeps with the text below it, also Git's own template
	// saved and given again with -F, and any case. Git runs the hook before its cleanup, so it
	// also reads the diff of commit --verbose.
	repo := t.TempDir()
	git := func(env []string, args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "core.hooksPath=" + filepath.Join(repo, ".githooks"),
			"-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		return cmd.Run()
	}
	if err := git(nil, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".githooks/commit-msg", "scripts/decision-citations.sh"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(read(t, name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scissors := "# ------------------------ >8 ------------------------"
	for _, c := range []struct {
		name, message string
		refused       bool
	}{
		{"heading", "Synthetic\n\n# verdict 5\n", true},
		{"upper case", "Synthetic VERDICT 5\n", true},
		{"scissors kept by -F", "Synthetic\n" + scissors + "\nverdict 5\n", true},
		{"saved template given to -F", "Synthetic\n" + scissors + "\n# Do not modify or remove the line above.\n# Everything below it will be ignored.\n+verdict 5\n", true},
		{"plain", "Synthetic\n", false},
	} {
		file := filepath.Join(t.TempDir(), "message")
		if err := os.WriteFile(file, []byte(c.message), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := git(nil, "commit", "-q", "--allow-empty", "-F", file); (err != nil) != c.refused {
			t.Errorf("%s: commit -F: %v", c.name, err)
		}
	}
	// commit --verbose puts the staged diff, here a verdict ID, below its scissors line; the
	// hook cannot tell that Git removes it.
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("verdict 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git(nil, "add", "notes.txt"); err != nil {
		t.Fatal(err)
	}
	if err := git([]string{"GIT_EDITOR=true"}, "commit", "-q", "--verbose", "-e", "-m", "Synthetic notes"); err == nil {
		t.Error("commit --verbose with a verdict ID in the diff was accepted")
	}
	if err := git(nil, "commit", "-q", "-m", "Synthetic notes"); err != nil {
		t.Errorf("the same commit without --verbose: %v", err)
	}
	if workflow := read(t, ".github/workflows/pr-text.yml"); !strings.Contains(workflow, "sh scripts/decision-citations.sh") ||
		!strings.Contains(workflow, "decision-citations:") || !strings.Contains(workflow, "edited") {
		t.Error("the pull request text workflow does not run the check on each edit")
	}
}

// The pull request text job checks the title and body of every pull request, and the commit
// messages of one whose base is not main: a promotion to main carries develop's commits, some
// from before the check, and develop's history is not rewritten (#548). The job's own script
// runs here against a repository with a legacy citation in its history.
func TestPullRequestTextChecksCommitsOutsideMain(t *testing.T) {
	workflow := read(t, ".github/workflows/pr-text.yml")
	if !strings.Contains(workflow, "BASE_REF: ${{ github.event.pull_request.base.ref }}") {
		t.Fatal("the job does not read the base branch")
	}
	_, after, found := strings.Cut(workflow, "        run: |\n")
	if !found {
		t.Fatal("the job has no run script")
	}
	var lines []string
	for _, line := range strings.Split(after, "\n") {
		if line != "" && !strings.HasPrefix(line, "          ") {
			break
		}
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	script := strings.Join(lines, "\n")

	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Synthetic",
			"-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts/decision-citations.sh"), []byte(read(t, "scripts/decision-citations.sh")), 0o755); err != nil {
		t.Fatal(err)
	}
	git("add", "scripts")
	git("commit", "-q", "-m", "Synthetic base")
	base := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "Synthetic legacy commit that cites verdict 61")
	legacy := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "Synthetic clean commit")
	clean := git("rev-parse", "HEAD")

	for _, c := range []struct {
		name, baseRef, title, base, head string
		refused                          bool
	}{
		{"promotion with a legacy commit", "main", "Release", base, clean, false},
		{"develop pull request with the legacy commit", "develop", "Change", base, clean, true},
		{"promotion title with a verdict ID", "main", "Release after verdict 5", base, clean, true},
		{"develop title with a verdict ID", "develop", "Change after verdict 5", legacy, clean, true},
		{"develop pull request with clean commits", "develop", "Change", legacy, clean, false},
	} {
		cmd := exec.Command("bash", "-e", "-c", script)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "TITLE="+c.title, "BODY=Synthetic body", "BASE="+c.base, "HEAD="+c.head, "BASE_REF="+c.baseRef)
		if out, err := cmd.CombinedOutput(); (err != nil) != c.refused {
			t.Errorf("%s: refused %v, want %v\n%s", c.name, err != nil, c.refused, out)
		}
	}
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "TITLE=Change", "BODY=Synthetic body after VERDICT 7", "BASE="+base, "HEAD="+base, "BASE_REF=main")
	if err := cmd.Run(); err == nil {
		t.Error("a promotion body with a verdict ID was accepted")
	}
}
