#!/bin/sh
# Post the board-verdict commit status on a pull request head after the maintainer approved a
# decision card that names that pull request and its exact head (WDR 004, Decision 8).
#
# Usage: scripts/board-verdict.sh --as FAMILY:ID PR HEAD REFERENCE
#
# --as names the calling Koinon session, as for other koinon commands. The script reads the
# card with koinon decision get, and posts the status only when:
#   - the card's current verdict is "approved", for the card's current revision;
#   - the Where field that the maintainer approved is exactly the target OWNER/REPO#PR@HEAD,
#     with this repository, the pull request and the full HEAD (other card text can name other
#     heads and pull requests);
#   - the pull request's head is HEAD.
# It then reads the status back, up to 5 times, STATUS_READBACK_DELAY seconds apart (2 by
# default), until the newest board-verdict status is the one it posted, by its status ID,
# because GitHub can show a new status a few seconds late. A new head needs a new card or a
# new revision.
set -eu

die() {
	echo "board-verdict: $*" >&2
	exit 1
}

as=
if [ "${1:-}" = --as ]; then
	[ $# -ge 2 ] || die "--as needs FAMILY:ID"
	as=$2
	shift 2
fi
[ $# -eq 3 ] || die "usage: scripts/board-verdict.sh --as FAMILY:ID PR HEAD REFERENCE"
[ -n "$as" ] || die "--as FAMILY:ID is required"
pr=$1
head=$2
reference=$3
printf '%s\n' "$pr" | grep -Eq '^[1-9][0-9]*$' || die "PR must be a pull request number: $pr"
printf '%s\n' "$head" | grep -Eq '^[0-9a-f]{40}$' || die "HEAD must be a full 40-character commit hash: $head"
printf '%s\n' "$reference" | grep -Eq '^[A-Za-z0-9-]{1,32}$' || die "REFERENCE must be a card reference: $reference"
command -v jq >/dev/null || die "jq is required"

card=$(koinon decision get --as "$as" --reference "$reference" 2>&1) || die "cannot read card $reference: $card"
printf '%s' "$card" | jq -e '.ok == true' >/dev/null 2>&1 || die "cannot read card $reference: $card"
verdict=$(printf '%s' "$card" | jq -r '.result.verdict.verdict // "none"')
[ "$verdict" = approved ] || die "card $reference has no approval (verdict: $verdict)"
revision=$(printf '%s' "$card" | jq -r '.result.revision')
approved=$(printf '%s' "$card" | jq -r '.result.verdict.revision')
[ "$revision" = "$approved" ] || die "card $reference is at revision $revision, but the approval is for revision $approved"
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner) || die "cannot read the repository name"
target="$repo#$pr@$head"
where=$(printf '%s' "$card" | jq -r '.result.verdict.snapshot.where // ""')
[ "$where" = "$target" ] ||
	die "the approved Where field of card $reference is not the target $target"

current=$(gh pr view "$pr" --json headRefOid --jq .headRefOid) || die "cannot read pull request #$pr"
[ "$current" = "$head" ] || die "pull request #$pr is at $current, not $head; a new head needs a new card"

status_id=$(gh api -X POST "repos/{owner}/{repo}/statuses/$head" -f state=success -f context=board-verdict \
	-f description="Card $reference approved at revision $approved" --jq .id) || die "the status post failed; read the head's statuses before a retry"
printf '%s\n' "$status_id" | grep -Eq '^[1-9][0-9]*$' ||
	die "the status post returned no status ID; read the head's statuses before a retry"
# GitHub can show a new status a few seconds after the post: read it back up to 5 times,
# until the newest board-verdict status is the one just posted (an older one can also be a
# success).
tries=0
while :; do
	tries=$((tries + 1))
	if state=$(gh api "repos/{owner}/{repo}/commits/$head/status" --jq '[.statuses[] | select(.context == "board-verdict")][0] | "\(.id) \(.state)"'); then
		[ "$state" != "$status_id success" ] || break
		failure="board-verdict on $head reads back as '$state', not status $status_id with success"
	else
		failure="the status was posted, but the read-back failed"
	fi
	[ "$tries" -lt 5 ] || die "$failure after $tries reads"
	sleep "${STATUS_READBACK_DELAY:-2}"
done
echo "board-verdict: success on $head (pull request #$pr, card $reference, revision $approved)"
