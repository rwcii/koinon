#!/bin/sh
# Run each preflight lens once on the committed head, each in a fresh agent worker with no forge
# credential, and keep the reports (PDR 008, Decision 5; WDR 004, Decision 8).
#
# Usage: scripts/preflight-run.sh [--base REF]
#
# The worker is `claude -p` for an author of any family. It has only the Read, Grep and Glob
# tools, loads no settings, auto-memory or instruction files and gets no forge or Koinon
# variable. It reads only its worktree: in the default permission mode, with no settings that
# allow more, Claude Code asks before a read outside the working directory, and a -p run has
# nobody to answer, so the read is denied; usage.json counts the denials; Claude Code still
# gives it the account's email address, which the publish script refuses in a report. Codex's
# read-only sandbox can read outside the worktree, so a codex worker is not used.
#
# The lens policy is .koinon/preflight/lenses.md at the merge base with REF: each "## NAME"
# section is one enabled lens and its brief. The change that adopts the preflight brings the
# first policy, so a base without one gives the head's. The base default is origin/develop. Each
# worker runs in a detached worktree of the head, outside this checkout, with the change in
# .preflight/change.diff. The reports go to _preflight/HEAD/RUN/ in the main checkout, which Git
# ignores, or to $PREFLIGHT_REPORTS.
# Exit status 0: every lens approves. 1: a lens rejects. 2: a lens failed or the run could not
# start.
set -eu
umask 077

die() {
	echo "preflight-run: $*" >&2
	exit 2
}

base=origin/develop
while [ $# -gt 0 ]; do
	case $1 in
	--base)
		[ $# -ge 2 ] || die "--base needs a ref"
		base=$2
		shift 2
		;;
	*) die "usage: scripts/preflight-run.sh [--base REF]" ;;
	esac
done
command -v jq >/dev/null || die "jq is required"
command -v claude >/dev/null || die "claude is not on PATH"

[ -z "$(git status --porcelain --untracked-files=no)" ] || die "commit your change first: the run reviews commits, not the working tree"
head=$(git rev-parse HEAD)
base_sha=$(git rev-parse --verify "$base^{commit}") || die "unknown base $base"
merge_base=$(git merge-base "$base_sha" "$head") || die "the head has no merge base with $base"
[ "$merge_base" != "$head" ] || die "the head is already in $base; give the pull request's base branch"
policy_path=.koinon/preflight/lenses.md
if git cat-file -e "$merge_base:$policy_path" 2>/dev/null; then
	policy_source="the base ($merge_base)"
	policy=$(git show "$merge_base:$policy_path")
elif git cat-file -e "$head:$policy_path" 2>/dev/null; then
	policy_source="the head; the base has no lens policy (first adoption)"
	policy=$(git show "$head:$policy_path")
else
	die "no lens policy at $policy_path"
fi
lenses=$(printf '%s\n' "$policy" | sed -n 's/^## \([a-z][a-z0-9-]*\)[[:space:]]*$/\1/p')
[ -n "$lenses" ] || die "the lens policy has no '## NAME' section"

reports=${PREFLIGHT_REPORTS:-}
if [ -z "$reports" ]; then
	common=$(git rev-parse --path-format=absolute --git-common-dir)
	reports=$(dirname "$common")/_preflight
fi
run=$(date -u +%Y%m%dT%H%M%SZ)-$(printf '%010d' "$$")
mkdir -p "$reports/$head/$run"
out=$(cd "$reports/$head/$run" && pwd -P)
work=$(mktemp -d "${TMPDIR:-/tmp}/preflight.XXXXXX")
work=$(cd "$work" && pwd -P)
cleanup() {
	git worktree remove --force "$work/head" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
git worktree add --quiet --detach "$work/head" "$head" || die "cannot make the frozen worktree"
mkdir "$work/no-forge" "$work/head/.preflight"
# The worker reads the change from these files. The person's diff settings do not shape them.
git diff --no-ext-diff --no-color --no-textconv --src-prefix=a/ --dst-prefix=b/ "$merge_base...$head" >"$work/head/.preflight/change.diff"
git log --no-color --format='%H %s' "$merge_base..$head" >"$work/head/.preflight/commits.txt"

echo "preflight-run: head $head, base $base ($merge_base), lens policy from $policy_source"
echo "preflight-run: reports in $out"
status=0
results=
for lens in $lenses; do
	brief=$(printf '%s\n' "$policy" | awk -v lens="$lens" '
		/^## / { on = ($2 == lens); next }
		on { print }')
	prompt=$out/$lens.prompt.md
	cat >"$prompt" <<EOF
You are the $lens lens of a preflight review. This is an author check before a pull request.
Work in the current directory, a detached worktree of head $head.
The change, \`git diff $merge_base...$head\`, is in .preflight/change.diff, and its commits are in
.preflight/commits.txt. Review it and the code and docs that it affects.

$brief

Write your report in Markdown: each finding with its file and line, its failure and its
evidence. End the report with exactly one last line, either
VERDICT: APPROVE
or
VERDICT: REJECT
EOF
	report=$out/$lens.md
	usage=$out/$lens.usage.json
	raw=$out/$lens.raw
	set +e
	(
		cd "$work/head" &&
			env -u GH_TOKEN -u GITHUB_TOKEN -u GH_ENTERPRISE_TOKEN -u GITHUB_ENTERPRISE_TOKEN \
				-u SSH_AUTH_SOCK -u KOINON_LAUNCH_ID -u KOINON_STATE_DIR -u KOINON_DAEMON_ADDRESS \
				-u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT \
				GH_CONFIG_DIR="$work/no-forge" GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false \
				CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 CLAUDE_CODE_DISABLE_CLAUDE_MDS=1 \
				claude -p --output-format json --no-session-persistence --strict-mcp-config \
				--setting-sources "" --disable-slash-commands --tools "Read,Grep,Glob" \
				--permission-mode default <"$prompt" >"$raw"
	)
	worker=$?
	set -e
	reason=
	if [ "$worker" -ne 0 ]; then
		reason="the claude worker exited with status $worker"
	else
		jq -r '.result // empty' "$raw" >"$report" 2>/dev/null || reason="the claude worker's answer is not JSON"
		jq '{event_key: (.session_id // "unknown"), usage: (.usage // "unknown"), cost_usd: (.total_cost_usd // "unknown"),
			denied_reads: ((.permission_denials // []) | length)}' \
			"$raw" >"$usage" 2>/dev/null || echo '{"usage": "unknown"}' >"$usage"
	fi
	verdict=failed
	if [ -z "$reason" ]; then
		last=$(grep -v '^[[:space:]]*$' "$report" | tail -n 1 | tr -d '\r' | sed 's/[[:space:]]*$//')
		case $last in
		"VERDICT: APPROVE") verdict=approve ;;
		"VERDICT: REJECT") verdict=reject ;;
		*) reason="the report has no last VERDICT line" ;;
		esac
	fi
	case $verdict in
	approve) echo "preflight-run: $lens approves" ;;
	reject)
		echo "preflight-run: $lens rejects; read $report"
		[ "$status" -eq 2 ] || status=1
		;;
	failed)
		echo "preflight-run: $lens failed: $reason" >&2
		status=2
		;;
	esac
	results="$results$(jq -cn --arg lens "$lens" --arg verdict "$verdict" --arg reason "$reason" \
		'{lens: $lens, verdict: $verdict, reason: $reason}')
"
done

printf '%s' "$results" | jq -s --arg head "$head" --arg base "$base" --arg merge_base "$merge_base" --arg run "$run" \
	--arg policy "$policy_source" --argjson status "$status" \
	'{head: $head, base: $base, merge_base: $merge_base, run: $run, family: "claude", policy: $policy,
	  approved: ($status == 0), lenses: .}' >"$out/result.json"
echo "preflight-run: result $out/result.json"
exit "$status"
