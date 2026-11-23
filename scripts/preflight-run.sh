#!/bin/sh
# Run each preflight lens once on the committed head, each in a fresh read-only agent worker
# with no forge credential, and keep the reports (PDR 008, Decision 5; WDR 004, Decision 8).
#
# Usage: scripts/preflight-run.sh --family claude|codex [--base REF]
#
# The lens policy is .koinon/preflight/lenses.md at the base commit: each "## NAME" section
# is one enabled lens and its brief. The base default is origin/develop. Each worker runs in a
# detached worktree of the head, outside this checkout. The reports go to
# _preflight/HEAD/RUN/ in the main checkout, which Git ignores, or to $PREFLIGHT_REPORTS.
# Exit status 0: every lens approves. 1: a lens rejects. 2: a lens failed or the run could not
# start.
set -eu

die() {
	echo "preflight-run: $*" >&2
	exit 2
}

family=
base=origin/develop
while [ $# -gt 0 ]; do
	case $1 in
	--family) family=${2:-}; shift 2 || die "--family needs claude or codex" ;;
	--base) base=${2:-}; shift 2 || die "--base needs a ref" ;;
	*) die "usage: scripts/preflight-run.sh --family claude|codex [--base REF]" ;;
	esac
done
case $family in
claude | codex) ;;
*) die "--family must be claude or codex" ;;
esac
command -v jq >/dev/null || die "jq is required"
command -v "$family" >/dev/null || die "$family is not on PATH"

[ -z "$(git status --porcelain --untracked-files=no)" ] || die "commit your change first: the run reviews commits, not the working tree"
head=$(git rev-parse HEAD)
base_sha=$(git rev-parse --verify "$base^{commit}") || die "unknown base $base"
merge_base=$(git merge-base "$base_sha" "$head") || die "the head has no merge base with $base"
policy_path=.koinon/preflight/lenses.md
if git cat-file -e "$merge_base:$policy_path" 2>/dev/null; then
	policy_source="the base ($merge_base)"
	policy=$(git show "$merge_base:$policy_path")
elif git cat-file -e "$head:$policy_path" 2>/dev/null; then
	# The change that adopts preflight brings the first lens policy.
	policy_source="the head; the base has no lens policy (first adoption)"
	policy=$(git show "$head:$policy_path")
else
	die "no lens policy at $policy_path"
fi
lenses=$(printf '%s\n' "$policy" | sed -n 's/^## \([a-z][a-z0-9-]*\)[[:space:]]*$/\1/p')
[ -n "$lenses" ] || die "the lens policy has no '## NAME' section"

common=$(git rev-parse --path-format=absolute --git-common-dir)
reports=${PREFLIGHT_REPORTS:-$(dirname "$common")/_preflight}
run=$(date -u +%Y%m%dT%H%M%SZ)-$$
out=$reports/$head/$run
mkdir -p "$out"
work=$(mktemp -d "${TMPDIR:-/tmp}/preflight.XXXXXX")
cleanup() {
	git worktree remove --force "$work/head" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM
git worktree add --quiet --detach "$work/head" "$head" || die "cannot make the frozen worktree"
mkdir "$work/no-forge"

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
Work read-only in the current directory, a detached worktree of head $head.
Review the change \`git diff $merge_base...$head\` and the code and docs that it affects.
Do not edit files and do not use the network.

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
	# No forge credential reaches a worker, and no worker joins Koinon.
	set +e
	(
		cd "$work/head" &&
			env -u GH_TOKEN -u GITHUB_TOKEN -u GH_ENTERPRISE_TOKEN -u GITHUB_ENTERPRISE_TOKEN \
				-u SSH_AUTH_SOCK -u KOINON_LAUNCH_ID -u KOINON_STATE_DIR -u KOINON_DAEMON_ADDRESS \
				-u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT \
				GH_CONFIG_DIR="$work/no-forge" GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false \
				sh -c '
				case $1 in
				claude)
					claude -p --output-format json --no-session-persistence --strict-mcp-config \
						--setting-sources local --permission-mode default \
						--allowedTools "Read,Grep,Glob,Bash(git diff:*),Bash(git log:*),Bash(git show:*)" \
						--disallowedTools "Edit,Write,NotebookEdit,WebFetch,WebSearch" <"$2" >"$3"
					;;
				codex)
					codex exec --ephemeral --ignore-user-config --sandbox read-only --skip-git-repo-check \
						-C . --json -o "$3.last" - <"$2" >"$3"
					;;
				esac' worker "$family" "$prompt" "$raw"
	)
	worker=$?
	set -e
	reason=
	if [ "$worker" -ne 0 ]; then
		reason="the $family worker exited with status $worker"
	elif [ "$family" = claude ]; then
		jq -r '.result // empty' "$raw" >"$report" 2>/dev/null || reason="the claude worker's answer is not JSON"
		jq '{event_key: (.session_id // "unknown"), usage: (.usage // "unknown"), cost_usd: (.total_cost_usd // "unknown")}' \
			"$raw" >"$usage" 2>/dev/null || echo '{"usage": "unknown"}' >"$usage"
	else
		cp "$raw.last" "$report" 2>/dev/null || reason="the codex worker wrote no last message"
		jq -s '{event_key: ([.[] | .thread_id? // empty][0] // "unknown"), usage: ([.[] | select(.type == "turn.completed") | .usage][-1] // "unknown")}' \
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
	--arg family "$family" --arg policy "$policy_source" --argjson status "$status" \
	'{head: $head, base: $base, merge_base: $merge_base, run: $run, family: $family, policy: $policy,
	  approved: ($status == 0), lenses: .}' >"$out/result.json"
echo "preflight-run: result $out/result.json"
exit "$status"
