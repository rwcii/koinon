package contributor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

// The merge gates of WDR 004, Decision 8 (#795): scripts/board-verdict.sh,
// scripts/preflight-run.sh and scripts/preflight-publish.sh run against fake gh, koinon and
// agent workers.

// fakeForge answers the scripts' gh calls. "gh pr view" prints $FAKE/head. A status POST is
// appended to $FAKE/statuses.jsonl, and the combined status lists them newest first. The issue
// comments are in $FAKE/comments.json; a comment POST appends to it. Every call is logged in
// $FAKE/calls. A file $FAKE/fail-status makes a status POST fail.
const fakeForge = `#!/bin/sh
set -eu
echo "$*" >> "$FAKE/calls"
if [ "$1" = repo ]; then
	echo rwcii/koinon
	exit 0
fi
if [ "$1" = pr ]; then
	case "$*" in
	*--jq*) cat "$FAKE/head" ;;
	*) jq -cn --arg h "$(cat "$FAKE/head")" '{headRefOid: $h, baseRefName: "develop"}' ;;
	esac
	exit 0
fi
shift
method=GET
filter=.
endpoint=
fields=
while [ $# -gt 0 ]; do
	case $1 in
	--paginate) ;;
	--jq) filter=$2; shift ;;
	-X) method=$2; shift ;;
	-f | -F) fields="$fields
$2"; shift ;;
	*) endpoint=$1 ;;
	esac
	shift
done
field() { printf '%s\n' "$fields" | sed -n "s/^$1=//p"; }
case "$method $endpoint" in
"POST "*/statuses/*)
	[ ! -e "$FAKE/fail-status" ] || { echo "HTTP 502: synthetic failure" >&2; exit 1; }
	jq -cn --arg s "$(field state)" --arg c "$(field context)" --arg u "$(field target_url)" --arg sha "${endpoint##*/}" \
		'{state: $s, context: $c, target_url: $u, sha: $sha}' >> "$FAKE/statuses.jsonl"
	;;
"GET user")
	echo '{"login": "synthetic-author"}' | jq -r "$filter"
	;;
"GET "*/commits/*/status)
	jq -s '{statuses: reverse}' "$FAKE/statuses.jsonl" | jq -r "$filter"
	;;
"GET "*/issues/*/comments)
	jq -r "$filter" "$FAKE/comments.json"
	;;
"POST "*/issues/*/comments)
	body=$(field body)
	body=${body#@}
	url="https://example.invalid/comment/$(jq length "$FAKE/comments.json")"
	jq --rawfile b "$body" --arg u "$url" '. + [{body: $b, html_url: $u, user: {login: "synthetic-author"}}]' "$FAKE/comments.json" > "$FAKE/comments.new"
	mv "$FAKE/comments.new" "$FAKE/comments.json"
	printf '{"html_url": "%s"}' "$url" | jq -r "$filter"
	;;
*)
	echo "unexpected gh call: $method $endpoint" >&2
	exit 2
	;;
esac
`

// fakeKoinon prints $FAKE/card.json as the answer of koinon decision get.
const fakeKoinon = `#!/bin/sh
echo "$*" >> "$FAKE/koinon-calls"
cat "$FAKE/card.json"
`

// fakeWorker is a claude worker. It logs its directory and whether a forge or Koinon
// variable reached it, and answers with the verdict in $FAKE/verdict-LENS: APPROVE, REJECT,
// NONE (no verdict line) or EXIT (a failed worker).
const fakeWorker = `#!/bin/sh
prompt=$(cat)
lens=$(printf '%s\n' "$prompt" | sed -n 's/^You are the \([a-z-]*\) lens.*/\1/p')
{
	echo "lens=$lens pwd=$(pwd) family=$(basename "$0") diff=$(test -s .preflight/change.diff && echo yes || echo no)"
	echo "GH_TOKEN=${GH_TOKEN:-unset} GITHUB_TOKEN=${GITHUB_TOKEN:-unset} KOINON_LAUNCH_ID=${KOINON_LAUNCH_ID:-unset} GH_CONFIG_DIR=${GH_CONFIG_DIR:-unset}"
	echo "memory-off=${CLAUDE_CODE_DISABLE_AUTO_MEMORY:-unset} instructions-off=${CLAUDE_CODE_DISABLE_CLAUDE_MDS:-unset} args=$*"
} >> "$FAKE/workers"
verdict=$(cat "$FAKE/verdict-$lens" 2>/dev/null || echo APPROVE)
[ "$verdict" != EXIT ] || exit 3
report="Synthetic $lens report."
[ "$verdict" = NONE ] || report="$report
VERDICT: $verdict"
jq -n --arg r "$report" '{result: $r, session_id: "synthetic", usage: {input_tokens: 10, output_tokens: 2}, total_cost_usd: 0.01}'
`

type gateEnv struct {
	fake, bin string
}

func newGateEnv(t *testing.T) gateEnv {
	t.Helper()
	for _, tool := range []string{"jq", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	dir := t.TempDir()
	e := gateEnv{fake: filepath.Join(dir, "fake"), bin: filepath.Join(dir, "bin")}
	for _, d := range []string{e.fake, e.bin} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"gh": fakeForge, "koinon": fakeKoinon, "claude": fakeWorker} {
		if err := os.WriteFile(filepath.Join(e.bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	e.write(t, "statuses.jsonl", "")
	e.write(t, "comments.json", "[]")
	return e
}

func (e gateEnv) write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.fake, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func (e gateEnv) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(e.fake, name))
	return string(b)
}

// run runs a repository script in dir with the fakes first on PATH, the person's Git
// configuration ignored and a forge token that no worker may see.
func (e gateEnv) run(t *testing.T, dir, script string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(root(t), "scripts", script), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+e.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE="+e.fake,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GH_TOKEN=synthetic-forge-token",
		"GITHUB_TOKEN=synthetic-forge-token", "KOINON_LAUNCH_ID=synthetic-launch", "TMPDIR="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

const gateHead = "0123456789abcdef0123456789abcdef01234567"

func card(verdict string, revision, approved int, text string) string {
	result := map[string]any{"reference": "MERGE-42", "revision": revision}
	if verdict != "" {
		result["verdict"] = map[string]any{"verdict": verdict, "revision": approved,
			"snapshot": map[string]any{"title": "Merge PR", "where": text, "context": "", "recommendation": ""}}
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	return string(b)
}

func TestBoardVerdictScript(t *testing.T) {
	names := "rwcii/koinon#42@" + gateHead
	for _, c := range []struct {
		name, card, prHead, want string
		args                     []string
		posted                   bool
	}{
		{"approval of the exact head", card("approved", 3, 3, names), gateHead, "board-verdict: success on " + gateHead, nil, true},
		{"the pull request moved", card("approved", 3, 3, names), strings.Repeat("f", 40), "a new head needs a new card", nil, false},
		{"declined", card("declined", 3, 3, names), gateHead, "has no approval (verdict: declined)", nil, false},
		{"no verdict", card("", 3, 0, names), gateHead, "has no approval (verdict: none)", nil, false},
		{"approval of an earlier revision", card("approved", 4, 3, names), gateHead, "approval is for revision 3", nil, false},
		{"the target only in other card text", strings.Replace(card("approved", 3, 3, "koinon"), `"where":"koinon"`, `"where":"koinon","commands":["scripts/board-verdict.sh --as FAMILY:ID 42 `+gateHead+` MERGE-42"],"context":"PR #42 at `+gateHead+`"`, 1), gateHead, "is not the target rwcii/koinon#42@" + gateHead, nil, false},
		{"a target without the head", card("approved", 3, 3, "rwcii/koinon#42@"+strings.Repeat("e", 40)), gateHead, "is not the target", nil, false},
		{"a target of another pull request", card("approved", 3, 3, "rwcii/koinon#421@"+gateHead), gateHead, "is not the target", nil, false},
		{"the target with more text", card("approved", 3, 3, "rwcii/koinon#42@"+gateHead+" and rwcii/koinon#43@"+gateHead), gateHead, "is not the target", nil, false},
		{"a target in another repository", card("approved", 3, 3, "other/koinon#42@"+gateHead), gateHead, "is not the target", nil, false},
		{"short head", card("approved", 3, 3, names), gateHead, "full 40-character commit hash", []string{"--as", "claude:synthetic", "42", "0123456", "MERGE-42"}, false},
		{"no caller", card("approved", 3, 3, names), gateHead, "--as FAMILY:ID is required", []string{"42", gateHead, "MERGE-42"}, false},
		{"--as without a value", card("approved", 3, 3, names), gateHead, "--as needs FAMILY:ID", []string{"--as"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newGateEnv(t)
			e.write(t, "card.json", c.card)
			e.write(t, "head", c.prHead+"\n")
			args := c.args
			if args == nil {
				args = []string{"--as", "claude:synthetic", "42", gateHead, "MERGE-42"}
			}
			out, err := e.run(t, t.TempDir(), "board-verdict.sh", nil, args...)
			if !strings.Contains(out, c.want) || (err == nil) != c.posted {
				t.Fatalf("err %v, output:\n%s", err, out)
			}
			statuses := e.read("statuses.jsonl")
			if c.posted != strings.Contains(statuses, `"context":"board-verdict"`) {
				t.Fatalf("statuses: %q", statuses)
			}
			if c.posted && !strings.Contains(statuses, `"sha":"`+gateHead+`"`) {
				t.Fatalf("status not on the head: %q", statuses)
			}
		})
	}
	// A failed status post is reported, not taken as success.
	e := newGateEnv(t)
	e.write(t, "card.json", card("approved", 3, 3, names))
	e.write(t, "head", gateHead+"\n")
	e.write(t, "fail-status", "")
	if out, err := e.run(t, t.TempDir(), "board-verdict.sh", nil, "--as", "claude:synthetic", "42", gateHead, "MERGE-42"); err == nil ||
		!strings.Contains(out, "the status post failed") {
		t.Fatalf("failed post: %v %s", err, out)
	}
}

// writeResult writes a run folder as scripts/preflight-run.sh does.
func writeResult(t *testing.T, reports, head, mergeBase, run string, approved bool, usage bool) string {
	t.Helper()
	dir := filepath.Join(reports, head, run)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	verdict := "approve"
	if !approved {
		verdict = "reject"
	}
	result := map[string]any{"head": head, "base": "origin/develop", "merge_base": mergeBase, "run": run,
		"family": "claude", "policy": "the base", "approved": approved,
		"lenses": []map[string]string{{"lens": "security", "verdict": "approve", "reason": ""}, {"lens": "code", "verdict": verdict, "reason": ""}}}
	b, _ := json.Marshal(result)
	files := map[string]string{"result.json": string(b), "security.md": "No findings.\nVERDICT: APPROVE\n", "code.md": "A finding.\nVERDICT: " + strings.ToUpper(verdict) + "\n"}
	if usage {
		files["security.usage.json"] = `{"usage": {"input_tokens": 10, "output_tokens": 2}}`
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// publishRepository is a repository whose origin/develop is the base of its head, as the
// pull request's base branch; it returns the repository, the head and their merge base.
func publishRepository(t *testing.T, e gateEnv) (string, string, string) {
	t.Helper()
	repo := gateRepository(t, e, true)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("update-ref", "refs/remotes/origin/develop", "base")
	head := git("rev-parse", "HEAD")
	e.write(t, "head", head+"\n")
	return repo, head, git("rev-parse", "base")
}

func TestPreflightPublishScript(t *testing.T) {
	statusOf := func(e gateEnv) string {
		var last string
		for _, line := range strings.Split(strings.TrimSpace(e.read("statuses.jsonl")), "\n") {
			if strings.Contains(line, `"context":"preflight"`) {
				last = line
			}
		}
		return last
	}
	publish := func(t *testing.T, e gateEnv, repo, reports string) (string, error) {
		return e.run(t, repo, "preflight-publish.sh", []string{"PREFLIGHT_REPORTS=" + reports}, "42")
	}
	t.Run("posts once for the exact head", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, true)
		out, err := publish(t, e, repo, reports)
		if err != nil || !strings.Contains(out, "success on "+head) {
			t.Fatalf("%v %s", err, out)
		}
		comments := e.read("comments.json")
		for _, want := range []string{"koinon-preflight head=" + head, "**APPROVED**", "| security | approve | 10 | 2 |", "| code | approve | unknown | unknown |", "security report"} {
			if !strings.Contains(comments, want) {
				t.Errorf("comment lacks %q:\n%s", want, comments)
			}
		}
		if s := statusOf(e); !strings.Contains(s, `"state":"success"`) || !strings.Contains(s, "example.invalid/comment/0") || !strings.Contains(s, head) {
			t.Fatalf("status %q", s)
		}
		out, err = publish(t, e, repo, reports)
		if err != nil || !strings.Contains(out, "already posted") || strings.Count(e.read("calls"), "POST") != 2 {
			t.Fatalf("second run posted again: %v %s\n%s", err, out, e.read("calls"))
		}
	})
	t.Run("a rejected result posts failure", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, "20261123T000000Z-0000000001", false, false)
		if out, err := publish(t, e, repo, reports); err != nil || !strings.Contains(statusOf(e), `"state":"failure"`) ||
			!strings.Contains(e.read("comments.json"), "NOT APPROVED") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("no result for the pull request's head", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, strings.Repeat("e", 40), base, "20261123T000000Z-0000000001", true, true)
		out, err := publish(t, e, repo, reports)
		if err == nil || !strings.Contains(out, "no preflight result for head "+head) || strings.Contains(e.read("calls"), "POST") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("a result run against another base", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, _ := publishRepository(t, e)
		reports := t.TempDir()
		// As scripts/preflight-run.sh --base HEAD~0 would give, with the head's own policy.
		writeResult(t, reports, head, head, "20261123T000000Z-0000000001", true, true)
		out, err := publish(t, e, repo, reports)
		if err == nil || !strings.Contains(out, "not the pull request's base develop") || strings.Contains(e.read("calls"), "POST") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("a report with a secret or a private path", func(t *testing.T) {
		for _, text := range []string{"token ghp_" + strings.Repeat("A", 36), "see /home/someone/.ssh/id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----",
			"key sk-proj-" + strings.Repeat("a", 30), "token eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0", "the account is someone@example.com",
			"see /var/home/someone/notes"} {
			e := newGateEnv(t)
			repo, head, base := publishRepository(t, e)
			reports := t.TempDir()
			dir := writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, true)
			os.WriteFile(filepath.Join(dir, "code.md"), []byte(text+"\nVERDICT: APPROVE\n"), 0600)
			out, err := publish(t, e, repo, reports)
			if err == nil || !strings.Contains(out, "has a secret, a private path or an email address") || strings.Contains(e.read("calls"), "POST") {
				t.Fatalf("%q: %v %s", text, err, out)
			}
		}
	})
	t.Run("a report that quotes a documented path", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		dir := writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, true)
		os.WriteFile(filepath.Join(dir, "code.md"), []byte("docs/INSTALL.md names ~/.claude/skills and /root/.config.\nVERDICT: APPROVE\n"), 0600)
		if out, err := publish(t, e, repo, reports); err != nil || !strings.Contains(e.read("comments.json"), "~/.claude/skills") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("a result with another run name", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, `x" or true or "`, true, true)
		out, err := publish(t, e, repo, reports)
		if err == nil || !strings.Contains(out, "unexpected run name") || strings.Contains(e.read("calls"), "POST") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("a report is a fenced block", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		dir := writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, true)
		os.WriteFile(filepath.Join(dir, "code.md"), []byte("</details> ```` <!-- x -->\nVERDICT: APPROVE\n"), 0600)
		if out, err := publish(t, e, repo, reports); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		var comments []map[string]any
		json.Unmarshal([]byte(e.read("comments.json")), &comments)
		body, _ := comments[len(comments)-1]["body"].(string)
		if !strings.Contains(body, "`````text\n</details> ```` <!-- x -->\nVERDICT: APPROVE\n`````\n") {
			t.Fatalf("body:\n%s", body)
		}
	})
	t.Run("the newest run of the head", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, "20261123T000000Z-0000000001", false, false)
		writeResult(t, reports, head, base, "20261123T010000Z-0000000001", true, false)
		if out, err := publish(t, e, repo, reports); err != nil || !strings.Contains(e.read("comments.json"), "run=20261123T010000Z-0000000001") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("a marker comment by another user is not the result", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, false)
		forged := `[{"body": "<!-- koinon-preflight head=` + head + ` run=20261123T000000Z-0000000001 --> APPROVED", "html_url": "https://example.invalid/forged", "user": {"login": "someone-else"}}]`
		e.write(t, "comments.json", forged)
		out, err := publish(t, e, repo, reports)
		if err != nil || strings.Contains(statusOf(e), "forged") || strings.Count(e.read("comments.json"), "koinon-preflight") != 2 {
			t.Fatalf("%v %s\n%s", err, out, e.read("statuses.jsonl"))
		}
	})
	t.Run("a comment without its status gets the status", func(t *testing.T) {
		e := newGateEnv(t)
		repo, head, base := publishRepository(t, e)
		reports := t.TempDir()
		writeResult(t, reports, head, base, "20261123T000000Z-0000000001", true, false)
		e.write(t, "fail-status", "")
		if out, err := publish(t, e, repo, reports); err == nil || !strings.Contains(out, "the status post failed") {
			t.Fatalf("%v %s", err, out)
		}
		os.Remove(filepath.Join(e.fake, "fail-status"))
		out, err := publish(t, e, repo, reports)
		if err != nil || !strings.Contains(statusOf(e), `"state":"success"`) || strings.Count(e.read("comments.json"), "koinon-preflight") != 1 {
			t.Fatalf("%v %s\n%s", err, out, e.read("comments.json"))
		}
	})
}

// gateRepository makes a repository whose base commit has a two-lens policy and whose head
// changes a file and weakens the policy to one lens.
func gateRepository(t *testing.T, e gateEnv, policyAtBase bool) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=synthetic", "-c", "user.email=synthetic@example.invalid",
			"-c", "commit.gpgsign=false", "-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	policy := filepath.Join(repo, ".koinon", "preflight", "lenses.md")
	if err := os.MkdirAll(filepath.Dir(policy), 0700); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "file"), []byte("base\n"), 0600)
	if policyAtBase {
		os.WriteFile(policy, []byte("# Lenses\n\n## security\n\nCheck security.\n\n## code\n\nCheck code.\n"), 0600)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("branch", "base")
	os.WriteFile(filepath.Join(repo, "file"), []byte("head\n"), 0600)
	os.WriteFile(policy, []byte("# Lenses\n\n## standards\n\nCheck standards.\n"), 0600)
	git("add", "-A")
	git("commit", "-q", "-m", "head")
	return repo
}

func readResult(t *testing.T, reports string) map[string]any {
	t.Helper()
	found, _ := filepath.Glob(filepath.Join(reports, "*", "*", "result.json"))
	if len(found) != 1 {
		t.Fatalf("results: %v", found)
	}
	var result map[string]any
	b, _ := os.ReadFile(found[0])
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPreflightRunScript(t *testing.T) {
	t.Run("approves with the base policy and no forge credential", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, true)
		reports := t.TempDir()
		out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + reports}, "--base", "base")
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		result := readResult(t, reports)
		lenses, _ := json.Marshal(result["lenses"])
		if result["approved"] != true || string(lenses) != `[{"lens":"security","reason":"","verdict":"approve"},{"lens":"code","reason":"","verdict":"approve"}]` {
			t.Fatalf("result %v", result)
		}
		workers := e.read("workers")
		if !strings.Contains(workers, `--tools Read,Grep,Glob`) || !strings.Contains(workers, "--setting-sources  --disable-slash-commands") ||
			!strings.Contains(workers, "--permission-mode default") || strings.Contains(workers, "--add-dir") || strings.Contains(workers, "--allowedTools") {
			t.Fatalf("claude worker tools or settings:\n%s", workers)
		}
		if strings.Count(workers, "lens=") != 2 || strings.Contains(workers, "diff=no") || strings.Contains(workers, "memory-off=unset") || strings.Contains(workers, "instructions-off=unset") || strings.Contains(workers, "synthetic-forge-token") || strings.Contains(workers, "synthetic-launch") ||
			strings.Contains(workers, "GH_CONFIG_DIR=unset") || strings.Contains(workers, "pwd="+repo+" ") || !strings.Contains(workers, "family=claude") {
			t.Fatalf("workers:\n%s", workers)
		}
		usage, _ := filepath.Glob(filepath.Join(reports, "*", "*", "security.usage.json"))
		if b, _ := os.ReadFile(usage[0]); !strings.Contains(string(b), "input_tokens") {
			t.Fatalf("usage %s", b)
		}
		// The frozen worktree is gone after the run.
		list, _ := exec.Command("git", "-C", repo, "worktree", "list").Output()
		if strings.Count(string(list), "\n") != 1 {
			t.Fatalf("worktrees left:\n%s", list)
		}
	})
	for _, c := range []struct {
		name, verdict, want string
		code                int
	}{
		{"a lens rejects", "REJECT", "code rejects", 1},
		{"a report without a verdict line", "NONE", "code failed: the report has no last VERDICT line", 2},
		{"a worker fails", "EXIT", "code failed: the claude worker exited with status 3", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newGateEnv(t)
			repo := gateRepository(t, e, true)
			e.write(t, "verdict-code", c.verdict)
			reports := t.TempDir()
			out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + reports}, "--base", "base")
			exit, _ := err.(*exec.ExitError)
			if exit == nil || exit.ExitCode() != c.code || !strings.Contains(out, c.want) || readResult(t, reports)["approved"] != false {
				t.Fatalf("%v %s", err, out)
			}
		})
	}
	t.Run("first adoption uses the head's policy", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, false)
		reports := t.TempDir()
		out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + reports}, "--base", "base")
		result := readResult(t, reports)
		if err != nil || !strings.Contains(result["policy"].(string), "first adoption") || !strings.Contains(e.read("workers"), "lens=standards") {
			t.Fatalf("%v %s %v", err, out, result)
		}
	})
	t.Run("a base that holds the head", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, true)
		out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + t.TempDir()}, "--base", "HEAD")
		if err == nil || !strings.Contains(out, "the head is already in HEAD") || e.read("workers") != "" {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("the family option is gone", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, true)
		out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + t.TempDir()}, "--family", "codex")
		if err == nil || !strings.Contains(out, "usage: scripts/preflight-run.sh [--base REF]") || e.read("workers") != "" {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("the run name", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, true)
		reports := t.TempDir()
		if out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + reports}, "--base", "base"); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if run, _ := readResult(t, reports)["run"].(string); !regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9]{10}$`).MatchString(run) {
			t.Fatalf("run %q", run)
		}
	})
	t.Run("uncommitted changes", func(t *testing.T) {
		e := newGateEnv(t)
		repo := gateRepository(t, e, true)
		os.WriteFile(filepath.Join(repo, "file"), []byte("dirty\n"), 0600)
		out, err := e.run(t, repo, "preflight-run.sh", []string{"PREFLIGHT_REPORTS=" + t.TempDir()}, "--base", "base")
		if err == nil || !strings.Contains(out, "commit your change first") || e.read("workers") != "" {
			t.Fatalf("%v %s", err, out)
		}
	})
}

// No skill names main as a merge target: project branches are facts, not skill text
// (agents/skills/AGENTS.md, "Keep procedures portable"), and with Koinon's facts the release
// stops for the maintainer's merge (WDR 004, Decision 8; the chunk 04 definition of done). The
// gates are optional facts whose defaults keep a project's steps.
func TestMergeGatesAreOptInFacts(t *testing.T) {
	mainMerge := regexp.MustCompile(`(?i)\bmerge[^.]{0,80}\b(into|to) ` + "`?" + `main\b`)
	skills, _ := filepath.Glob(filepath.Join(root(t), "agents", "skills", "*", "SKILL.md"))
	for _, path := range skills {
		b, _ := os.ReadFile(path)
		if m := mainMerge.FindString(strings.Join(strings.Fields(string(b)), " ")); m != "" {
			t.Errorf("%s tells an agent to merge into main: %q", path, m)
		}
	}
	if mainMerge.FindString("Merge the promotion PR into main.") == "" {
		t.Fatal("the main-merge pattern missed its control")
	}
	catalog, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]map[string]string{"ship": {"Merge approval": "none"}, "release": {"Promotion merge": "the promotion policy"}}
	for _, item := range catalog.Items {
		for heading, want := range defaults[item.Name] {
			var found bool
			for _, field := range item.Facts.Fields {
				if field.Heading == heading {
					found = true
					if field.Required || field.Default == nil || *field.Default != want {
						t.Errorf("%s %q: required %v, default %v", item.Name, heading, field.Required, field.Default)
					}
				}
			}
			if !found {
				t.Errorf("%s has no %q fact", item.Name, heading)
			}
		}
	}
	for name, step := range map[string]string{"ship": "When the Merge approval fact names a board approval", "release": "When the Promotion merge fact names the maintainer"} {
		if !strings.Contains(strings.Join(strings.Fields(read(t, "agents/skills/"+name+"/SKILL.md")), " "), step) {
			t.Errorf("%s lacks the conditional step %q", name, step)
		}
	}
	for name, want := range map[string]string{".koinon/skills/ship.md": "## Merge approval\nA board approval", ".koinon/skills/release.md": "## Promotion merge\nThe maintainer merges",
		"scripts/setup-repo.sh": `{"context": "decision-citations"}, {"context": "board-verdict"}'`} {
		if !strings.Contains(read(t, name), want) {
			t.Errorf("%s lacks %q", name, want)
		}
	}
}
