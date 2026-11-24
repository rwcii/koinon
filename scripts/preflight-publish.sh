#!/bin/sh
# Post the newest preflight result of a pull request's head: one comment and the preflight
# commit status on that head (PDR 008, Decision 5; WDR 004, Decision 8).
#
# Usage: scripts/preflight-publish.sh PR
#
# It reads the result that scripts/preflight-run.sh wrote for the pull request's current head,
# and refuses when there is none. When a comment for that result is already on the pull
# request, it prints its link and posts nothing. A value that the result does not have shows as
# "unknown". It reads the posted status back, up to 5 times, STATUS_READBACK_DELAY seconds
# apart (2 by default), because GitHub can show a new status a few seconds late.
set -eu

die() {
	echo "preflight-publish: $*" >&2
	exit 1
}

[ $# -eq 1 ] || die "usage: scripts/preflight-publish.sh PR"
pr=$1
printf '%s\n' "$pr" | grep -Eq '^[1-9][0-9]*$' || die "PR must be a pull request number: $pr"
command -v jq >/dev/null || die "jq is required"

pull=$(gh pr view "$pr" --json headRefOid,baseRefName) || die "cannot read pull request #$pr"
head=$(printf '%s' "$pull" | jq -r .headRefOid)
printf '%s\n' "$head" | grep -Eq '^[0-9a-f]{40}$' || die "unexpected head of pull request #$pr: $head"
base_ref=$(printf '%s' "$pull" | jq -r .baseRefName)
reports=${PREFLIGHT_REPORTS:-}
if [ -z "$reports" ]; then
	common=$(git rev-parse --path-format=absolute --git-common-dir) || die "run this command in the checkout"
	reports=$(dirname "$common")/_preflight
fi
dir=
for result in "$reports/$head"/*/result.json; do
	[ -e "$result" ] && dir=$(dirname "$result")
done
[ -n "$dir" ] || die "no preflight result for head $head of pull request #$pr; run scripts/preflight-run.sh at that head"
result=$dir/result.json
[ "$(jq -r .head "$result")" = "$head" ] || die "the result in $dir is not for head $head"
# The lens policy comes from the merge base, so the result must be for the pull request's base.
expected=$(git merge-base "origin/$base_ref" "$head") || die "cannot find the merge base of origin/$base_ref and $head; fetch first"
[ "$(jq -r .merge_base "$result")" = "$expected" ] ||
	die "the result in $dir was run against $(jq -r .base "$result"), not the pull request's base $base_ref; run scripts/preflight-run.sh --base origin/$base_ref"
run=$(jq -r .run "$result")
# The run name enters a jq filter and the comment marker: only the form that preflight-run.sh
# writes is accepted.
printf '%s\n' "$run" | grep -Eq '^[0-9]{8}T[0-9]{6}Z-[0-9]{10}$' || die "the result in $dir has an unexpected run name: $run"
marker="<!-- koinon-preflight head=$head run=$run -->"

# A report is posted in public: refuse one with a secret, a private path (a home directory that
# names a person, or the running user's home) or an email address.
# Tokens: GitHub, private keys, Anthropic and OpenAI (sk-), JSON web tokens, AWS, Slack, xAI and
# Google keys.
secrets='gh[pousr]_[A-Za-z0-9]{20,}|github_pat_|-----BEGIN [A-Z ]*PRIVATE KEY|sk-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|AKIA[0-9A-Z]{16}|xox[abprs]-|xai-[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_-]{35}'
private='/home/[A-Za-z0-9_]|/var/home/[A-Za-z0-9_]|/Users/[A-Za-z0-9_]|[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}'
for report in "$dir"/*.md; do
	[ -e "$report" ] || continue
	case $report in *.prompt.md) continue ;; esac
	if grep -Eq "$secrets|$private" "$report" || { [ -n "${HOME:-}" ] && grep -Fq "$HOME/" "$report"; }; then
		die "$report has a secret, a private path or an email address; remove it from the report and run this command again"
	fi
done

me=$(gh api user --jq .login) || die "cannot read the gh login"
existing=$(gh api --paginate "repos/{owner}/{repo}/issues/$pr/comments" \
	--jq ".[] | select(.user.login == \"$me\" and (.body | contains(\"$marker\"))) | .html_url") || die "cannot read the comments of pull request #$pr"
approved=$(jq -r .approved "$result")
if [ "$approved" = true ]; then
	state=success
	title=APPROVED
else
	state=failure
	title="NOT APPROVED"
fi
# Print a report as a fenced block, so that its Markdown and HTML cannot change the comment. The
# fence is longer than the longest backtick run in the report.
fenced() {
	fence=$(awk '{ while (match($0, /`+/)) { if (RLENGTH > n) n = RLENGTH; $0 = substr($0, RSTART + RLENGTH) } }
		END { n = (n < 3) ? 3 : n + 1; while (n-- > 0) printf "`" }' "$1")
	echo "${fence}text"
	if [ "$(wc -c <"$1")" -le 12000 ]; then
		cat "$1"
	else
		# Whole lines only, so that no character is cut; a first line longer than the limit is cut.
		cut=$(head -c 12000 "$1" | sed '$d')
		[ -n "$cut" ] || cut=$(head -c 12000 "$1")
		printf '%s\n' "$cut"
	fi
	echo "$fence"
	if [ "$(wc -c <"$1")" -gt 12000 ]; then
		printf '\n(The report is longer; the full text is in the run folder.)\n'
	fi
}
post_status() {
	gh api -X POST "repos/{owner}/{repo}/statuses/$head" -f state="$state" -f context=preflight \
		-f target_url="$1" -f description="Preflight $title, run $run" >/dev/null ||
		die "the comment is at $1, but the status post failed; run this command again"
	# GitHub can show a new status a few seconds after the post: read it back up to 5 times.
	tries=0
	while :; do
		tries=$((tries + 1))
		if read_back=$(gh api "repos/{owner}/{repo}/commits/$head/status" --jq '[.statuses[] | select(.context == "preflight")][0].state'); then
			[ "$read_back" != "$state" ] || break
			failure="preflight on $head reads back as '$read_back', not $state"
		else
			failure="posted; the read-back failed"
		fi
		[ "$tries" -lt 5 ] || die "$failure after $tries reads"
		sleep "${STATUS_READBACK_DELAY:-2}"
	done
	echo "preflight-publish: $state on $head: $1"
}
if [ -n "$existing" ]; then
	url=$(printf '%s\n' "$existing" | head -n 1)
	posted=$(gh api "repos/{owner}/{repo}/commits/$head/status" --jq '[.statuses[] | select(.context == "preflight")][0].target_url // ""') ||
		die "cannot read the statuses of $head"
	if [ "$posted" = "$url" ]; then
		echo "preflight-publish: already posted: $url"
		exit 0
	fi
	# The comment of an earlier attempt is there, but its status is not.
	post_status "$url"
	exit 0
fi
body=$(mktemp "${TMPDIR:-/tmp}/preflight-body.XXXXXX")
trap 'rm -f "$body"' EXIT
trap 'exit 130' INT TERM
{
	echo "$marker"
	echo "## Preflight"
	echo
	echo "**$title** on \`$head\`: base \`$(jq -r .merge_base "$result")\`, run \`$run\`, $(jq -r .family "$result") workers, lens policy from $(jq -r .policy "$result")."
	echo
	echo "| Lens | Verdict | Tokens in | Tokens out |"
	echo "| --- | --- | --- | --- |"
	for lens in $(jq -r '.lenses[].lens' "$result"); do
		verdict=$(jq -r --arg l "$lens" '.lenses[] | select(.lens == $l) | .verdict + (if .reason != "" then " (" + .reason + ")" else "" end)' "$result")
		tokens_in=unknown
		tokens_out=unknown
		if [ -f "$dir/$lens.usage.json" ]; then
			tokens_in=$(jq -r '.usage | if type == "object" then (.input_tokens // "unknown") else "unknown" end' "$dir/$lens.usage.json" 2>/dev/null || echo unknown)
			tokens_out=$(jq -r '.usage | if type == "object" then (.output_tokens // "unknown") else "unknown" end' "$dir/$lens.usage.json" 2>/dev/null || echo unknown)
		fi
		echo "| $lens | $verdict | $tokens_in | $tokens_out |"
	done
	for lens in $(jq -r '.lenses[].lens' "$result"); do
		echo
		echo "<details><summary>$lens report</summary>"
		echo
		if [ -f "$dir/$lens.md" ]; then
			fenced "$dir/$lens.md"
		else
			echo "No report."
		fi
		echo
		echo "</details>"
	done
} >"$body"

url=$(gh api -X POST "repos/{owner}/{repo}/issues/$pr/comments" -F "body=@$body" --jq .html_url) ||
	die "the comment post failed; read the pull request comments before a retry"
post_status "$url"
