package contributor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// skillBlock returns the first sh block after heading in a skill, with <agent-id> replaced.
func skillBlock(t *testing.T, skill, heading string) string {
	t.Helper()
	text := read(t, filepath.Join("agents", "skills", skill, "SKILL.md"))
	_, section, ok := strings.Cut(text, heading)
	if !ok {
		t.Fatalf("%s has no %q", skill, heading)
	}
	_, block, ok := strings.Cut(section, "```sh\n")
	if !ok {
		t.Fatalf("%s %q has no sh block", skill, heading)
	}
	block, _, _ = strings.Cut(block, "```")
	return strings.ReplaceAll(block, "<agent-id>", "claude")
}

// runBlock runs a block with bash -e in dir, with Git kept from looking above dir.
func runBlock(t *testing.T, block, dir string) string {
	t.Helper()
	cmd := exec.Command("bash", "-e", "-c", block)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir), "HOME="+t.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("block exited: %v\n%s", err, out)
	}
	return string(out)
}

// The handoff and pickup skills' location blocks run under bash -e in a directory that is
// not a Git checkout, and in a repository (#485).
func TestHandoffLocationBlocksRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	write := skillBlock(t, "handoff", "## 2. Create and check the directory")
	find := skillBlock(t, "pickup", "## 2. Read the progress note, then find the newest handoff")
	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(plain, "_handoff", "claude")
	if out := runBlock(t, write, plain); !strings.Contains(out, "ready: "+dir) || strings.Contains(out, "not ready") {
		t.Fatalf("outside Git: %s", out)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory: %v %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-26-010000.md"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runBlock(t, find, plain); !strings.Contains(out, "newest handoff: "+filepath.Join(dir, "2026-10-26-010000.md")) {
		t.Fatalf("pickup outside Git: %s", out)
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if out := runBlock(t, write, repo); !strings.Contains(out, "ready: "+filepath.Join(repo, "_handoff", "claude")) || strings.Contains(out, "not ready") {
		t.Fatalf("in a repository: %s", out)
	}
	if exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); err != nil || !strings.Contains(string(exclude), "/_handoff/") {
		t.Fatalf("exclude: %q %v", exclude, err)
	}
}

// The handoff skill seeds from the progress note only when the note is newer than the last
// snapshot (HANDOFF-WORDS): a newer note, an older or equally old note, no note, an oversized
// note and a note with no snapshot, in a directory whose path has spaces.
func TestHandoffSeedBlock(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	seed := skillBlock(t, "handoff", "## 4. Seed from the progress note and the last handoff of this agent")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "a dir with spaces", "_handoff", "claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "progress.md")
	older := filepath.Join(dir, "2026-11-02-010000.md")
	last := filepath.Join(dir, "2026-11-03-010000-label.md")
	run := func() string {
		return strings.TrimSpace(runBlock(t, "dir='"+dir+"'\n"+seed, root))
	}
	at := func(path string, when time.Time) {
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	if out := run(); out != "seed: none" {
		t.Fatalf("empty directory: %s", out)
	}
	write(note, "# Progress — claude\n")
	if out := run(); out != "seed: note" {
		t.Fatalf("note with no snapshot: %s", out)
	}
	write(older, "synthetic")
	write(last, "synthetic")
	at(older, base)
	at(last, base.Add(time.Minute))
	at(note, base.Add(2*time.Minute))
	if out := run(); out != "seed: note, then "+last {
		t.Fatalf("newer note: %s", out)
	}
	for name, when := range map[string]time.Time{"older": base, "equal": base.Add(time.Minute)} {
		at(note, when)
		if out := run(); out != "seed: "+last+"; note older, absent or unusable" {
			t.Fatalf("%s note: %s", name, out)
		}
	}
	write(note, strings.Repeat("x", 4097))
	at(note, base.Add(2*time.Minute))
	if out := run(); out != "seed: "+last+"; note older, absent or unusable" {
		t.Fatalf("oversized note: %s", out)
	}
	if err := os.Remove(note); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "seed: "+last+"; note older, absent or unusable" {
		t.Fatalf("no note: %s", out)
	}
}

// The handoff template quotes the maintainer's directions apart from the agent's proposals,
// pickup reads and reports them that way, and the cycle request and the guide say that the
// pause is for that cycle only (HANDOFF-WORDS).
func TestHandoffCarriesMaintainerWords(t *testing.T) {
	handoff := read(t, filepath.Join("agents", "skills", "handoff", "SKILL.md"))
	pickup := read(t, filepath.Join("agents", "skills", "pickup", "SKILL.md"))
	guide := read(t, filepath.Join("internal", "setup", "guide.go"))
	request := read(t, filepath.Join("internal", "core", "context_cycle.go"))
	for _, c := range []struct {
		name, text, want string
	}{
		{"handoff", handoff, "## Maintainer direction\n- \"<the maintainer's words>\" (<date>, <where: terminal, dashboard or board card REFERENCE>),\n  answering: <the question or proposal it answered>"},
		{"handoff", handoff, "## My proposals not yet raised\n- <a draft, open point or idea of yours that the maintainer has not seen>"},
		{"handoff", handoff, "write them as yours, not as questions from the maintainer or\nas rules"},
		{"handoff", handoff, "- Decisions: <confirmed choices and their sources; maintainer directions are quoted in the snapshot>"},
		{"pickup", pickup, "and always its Goal, Blockers and tasks, Next\nsteps, Maintainer direction and My proposals not yet raised sections: the note does not replace\nthem."},
		{"pickup", pickup, "report each proposal as yours, not as a\nquestion or a rule from the maintainer. The maintainer's current instructions are a direct\nsource as always."},
		{"guide", guide, "The\n  pause is for this cycle only; your handoff does not record it as a rule for the next\n  session."},
		{"request", request, "This pause is for this cycle only; the handoff does not record it as a rule for the next session."},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("%s lacks %q", c.name, c.want)
		}
	}
}

// A decision waits for the maintainer only when a board item holds it: the handoff names the
// item with its state, and pickup reads the board after the reconnect, before its report, and
// reports a claim without a live item as a difference (#600). The checks are of the skill text;
// the behavior is checked by a fresh-context pickup run in the pull request's evidence.
func TestHandoffWaitsNameBoardItems(t *testing.T) {
	handoff := read(t, filepath.Join("agents", "skills", "handoff", "SKILL.md"))
	pickup := read(t, filepath.Join("agents", "skills", "pickup", "SKILL.md"))
	for _, c := range []struct {
		name, text, want string
	}{
		{"handoff", handoff, "- Decisions waiting for the maintainer: <each decision item's reference, its state and revision as\n  read now, and what waits on it; none when no item holds one>"},
		{"handoff", handoff, "A decision waits for the maintainer only when a decision item on the board holds it."},
		{"handoff", handoff, "A draft that the maintainer has not\nseen goes under My proposals not yet raised, never as waiting or pending."},
		{"handoff", handoff, "When the board cannot be read, write the claim as\nunverified, with the error."},
		{"handoff", handoff, "this rule adds no item before you act on them (WDR 005, Decision 5)."},
		{"handoff", handoff, "- Waiting for the maintainer: <decision item references and states as read, or none>"},
		{"pickup", pickup, "1. Read `decision_list` with the filter `waiting`, and `decision_pending`, each page by page to\n   its end."},
		{"pickup", pickup, "2. Read each decision item that the note or the handoff names with `decision_get`, also one\n   that is not in the waiting list."},
		{"pickup", pickup, "a claim that names no item, or an item that does not exist, is a difference too: report\n     it as drift, not as a wait, and not as a question for the maintainer."},
		{"pickup", pickup, "an active item of kind `fyi`, at any revision, is information for the maintainer, not a\n     decision that waits"},
		{"pickup", pickup, "an active item of kind `decision` or `task` at a newer revision than the claim names was\n     reopened or edited"},
		{"pickup", pickup, "it is the previous session's own proposal only when the handoff says so,\n     and without a source, report the source as unknown."},
		{"pickup", pickup, "leave the claim unverified: report it with the\nerror, not as none and not as a wait, and continue the other steps."},
		{"pickup", pickup, "- The next steps, corrected for the differences; the first one that still applies is the next\n  action. A claim that the board read did not confirm adds no wait for the maintainer to it."},
		{"pickup", pickup, "- Decisions waiting for the maintainer, only from the board read of step 4, with each reference\n  and revision; each claim that the read did not confirm, as drift or as unverified."},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("%s lacks %q", c.name, c.want)
		}
	}
	for _, c := range []struct{ name, text, old string }{
		{"handoff", handoff, "Blockers and open decisions"},
		{"pickup", pickup, "- Blockers and decisions that need the maintainer."},
	} {
		if strings.Contains(c.text, c.old) {
			t.Errorf("%s still has %q", c.name, c.old)
		}
	}
	// The board is read once the daemon knows the session: in step 4, after the reconnect.
	board := strings.Index(pickup, "Then check the decision board")
	if board < strings.Index(pickup, "## 4. Reconnect to the daemon") || board > strings.Index(pickup, "## 5. Orient, then continue") {
		t.Errorf("pickup reads the board outside step 4")
	}
}

// A handoff records the goal, its blockers and their tasks, and the next steps, and pickup
// reads them also with a usable note, checks them against the live state and reports them
// first (#602). The checks are of the skill text; the behavior is checked by a fresh-context
// write and pickup run in the pull request's evidence.
func TestHandoffKeepsGoalHierarchy(t *testing.T) {
	handoff := read(t, filepath.Join("agents", "skills", "handoff", "SKILL.md"))
	pickup := read(t, filepath.Join("agents", "skills", "pickup", "SKILL.md"))
	for _, c := range []struct {
		name, text, want string
	}{
		{"handoff", handoff, "## Goal\n- Goal: <the outcome that the work serves>; source:"},
		{"handoff", handoff, "- Success criteria: <the checkable conditions that complete the goal>"},
		{"handoff", handoff, "## Blockers and tasks\n- Blocker 1: <what stops progress towards the goal now>; evidence:"},
		{"handoff", handoff, "  - Task 1.1: <the work that clears it, and its approach>; state: <done, underway with its\n    branch, commit, pull request or running job, or not started>"},
		{"handoff", handoff, "## Next steps\n1. <the first open task that you would do now: the next action>"},
		{"handoff", handoff, "- Next action: <step 1 of Next steps>"},
		{"handoff", handoff, "The Goal section names the outcome that the work serves, not the current pull request or task: a\npull request is a task under a blocker."},
		{"handoff", handoff, "When no\nsource records a goal, write unknown and continue the work that you are authorized to do."},
		{"handoff", handoff, "- Goal: <the goal and its source>; done when: <the success criteria>\n- Blockers: <each blocker and its evidence, then its tasks with their states; or none>"},
		{"handoff", handoff, "- Next steps: <the open tasks in order; the first is the next action>"},
		{"handoff", handoff, "always with its Goal, Blockers and tasks, Next steps, Maintainer direction and My\nproposals not yet raised sections."},
		{"pickup", pickup, "An older handoff has Goal and success criteria and Resume here in their place: read those\nas its goal and its next action, without a stop."},
		{"pickup", pickup, "Read a running job's state; do not\nstart it again."},
		{"pickup", pickup, "Report briefly, the goal's hierarchy first:"},
		{"pickup", pickup, "the outcome that the work serves, not the\n  current pull request or task; unknown when the handoff records none. Asked for the goal, give\n  this goal."},
		{"pickup", pickup, "An underway task goes on from where it stopped, a job that finished\nis not run again, and open work without a blocker can go on while a blocker waits for someone\nelse."},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("%s lacks %q", c.name, c.want)
		}
	}
	for _, c := range []struct{ name, text, old string }{
		{"handoff", handoff, "## Goal and success criteria"},
		{"handoff", handoff, "- Next action: <the single first step>"},
		{"handoff", handoff, "- Task: <the current task and its issue>"},
		{"pickup", pickup, "- The goal and success criteria, if they are still valid."},
	} {
		if strings.Contains(c.text, c.old) {
			t.Errorf("%s still has %q", c.name, c.old)
		}
	}
	// The goal's hierarchy comes first in the report.
	report := pickup[strings.Index(pickup, "## 5. Orient, then continue"):]
	if strings.Index(report, "- The goal, with its source") > strings.Index(report, "- Where things stand now") {
		t.Errorf("pickup reports where things stand before the goal")
	}
}
