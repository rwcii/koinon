#!/bin/sh
# Rerun the failed jobs of one workflow run attempt when no runner acquired any of them (#630).
# A GitHub-hosted macOS job can wait for a runner until GitHub cancels it, with the annotation
# below; no step runs, and the run fails. A run with any other failed job is not rerun, so a
# real failure stays visible (WDR 004, Decision 6). At most two reruns: attempts 1 and 2.
#
# Environment: REPO (owner/name), RUN (run ID), ATTEMPT (the run attempt that triggered this
# check), GH_TOKEN for gh. Exit 0 after a rerun or a decision not to rerun. A failed gh call or
# a malformed answer ends the script with another status, before any rerun.
set -eu

unacquired="The job was not acquired by Runner of type hosted even after multiple attempts"

case $ATTEMPT in
1 | 2) ;;
*)
	echo "run $RUN attempt $ATTEMPT: not rerun; attempts 1 and 2 are rerun at most"
	exit 0
	;;
esac

list=$(mktemp)
trap 'rm -f "$list"' EXIT
# One line for each failed job: its ID, its runner ID and the number of steps that ran.
gh api --paginate "repos/$REPO/actions/runs/$RUN/attempts/$ATTEMPT/jobs?per_page=100" \
	--jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "cancelled" or .conclusion == "timed_out") | "\(.id) \(.runner_id // 0) \([(.steps // [])[] | select(.conclusion != null)] | length)"' > "$list"
if [ ! -s "$list" ]; then
	echo "run $RUN attempt $ATTEMPT: no failed job; not rerun"
	exit 0
fi

rerun=yes
while read -r id runner steps; do
	if [ "$runner" != 0 ] || [ "$steps" != 0 ]; then
		echo "job $id ran (runner $runner, $steps steps) and failed; not rerun"
		rerun=no
		continue
	fi
	# One line for each page: true when a whole annotation message equals the sentence.
	found=$(gh api --paginate "repos/$REPO/check-runs/$id/annotations?per_page=100" --jq "any(.[]; .message == \"$unacquired\")")
	if printf '%s\n' "$found" | grep -qx true; then
		echo "job $id: no runner acquired it"
	else
		echo "job $id failed without a runner and without the unacquired annotation; not rerun"
		rerun=no
	fi
done < "$list"
if [ "$rerun" != yes ]; then
	exit 0
fi

# A stale or repeated event, or a rerun that started meanwhile, must not add a rerun.
now=$(gh api "repos/$REPO/actions/runs/$RUN" --jq '"\(.run_attempt) \(.status) \(.conclusion)"')
if [ "$now" != "$ATTEMPT completed failure" ]; then
	echo "run $RUN is now \"$now\", not attempt $ATTEMPT completed with failure; not rerun"
	exit 0
fi
gh api -X POST "repos/$REPO/actions/runs/$RUN/rerun-failed-jobs" > /dev/null
echo "run $RUN attempt $ATTEMPT: failed jobs rerun"
