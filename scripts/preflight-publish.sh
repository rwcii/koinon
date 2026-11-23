#!/bin/sh
# Post the newest preflight result of a pull request's head: one comment and the preflight
# commit status on that head (PDR 008, Decision 5; WDR 004, Decision 8).
#
# Usage: scripts/preflight-publish.sh PR
#
# It reads the result that scripts/preflight-run.sh wrote for the pull request's current head,
# and refuses when there is none. When a comment for that result is already on the pull
# request, it prints its link and posts nothing. A value that the result does not have shows as
# "unknown".
set -eu

die() {
	echo "preflight-publish: $*" >&2
	exit 1
}

[ $# -eq 1 ] || die "usage: scripts/preflight-publish.sh PR"
pr=$1
printf '%s\n' "$pr" | grep -Eq '^[1-9][0-9]*$' || die "PR must be a pull request number: $pr"
command -v jq >/dev/null || die "jq is required"

head=$(gh pr view "$pr" --json headRefOid --jq .headRefOid) || die "cannot read pull request #$pr"
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
run=$(jq -r .run "$result")
marker="<!-- koinon-preflight head=$head run=$run -->"

existing=$(gh api --paginate "repos/{owner}/{repo}/issues/$pr/comments" \
	--jq ".[] | select(.body | contains(\"$marker\")) | .html_url") || die "cannot read the comments of pull request #$pr"
approved=$(jq -r .approved "$result")
if [ "$approved" = true ]; then
	state=success
	title=APPROVED
else
	state=failure
	title="NOT APPROVED"
fi
post_status() {
	gh api -X POST "repos/{owner}/{repo}/statuses/$head" -f state="$state" -f context=preflight \
		-f target_url="$1" -f description="Preflight $title, run $run" >/dev/null ||
		die "the comment is at $1, but the status post failed; run this command again"
	read_back=$(gh api "repos/{owner}/{repo}/commits/$head/status" --jq '[.statuses[] | select(.context == "preflight")][0].state') ||
		die "posted; the read-back failed"
	[ "$read_back" = "$state" ] || die "preflight on $head reads back as '$read_back', not $state"
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
trap 'rm -f "$body"' EXIT INT TERM
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
			head -c 12000 "$dir/$lens.md"
			[ "$(wc -c <"$dir/$lens.md")" -le 12000 ] || printf '\n\n(The report is longer; the full text is in the run folder.)\n'
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
