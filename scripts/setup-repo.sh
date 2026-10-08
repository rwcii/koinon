#!/usr/bin/env bash
# Apply the personal-repository branch and merge policy. Does not change visibility.
set -euo pipefail
repo="${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
git config core.hooksPath .githooks
# The remote deletes a head branch when its pull request merges, so without pruning the
# clone keeps a tracking ref for every branch that is already gone. A stale ref reads as a
# live branch, so treat the remote as the authority and drop what it no longer reports.
# This removes local tracking refs only, and the shared config covers linked worktrees.
git config fetch.prune true
gh api --method PATCH "repos/$repo" \
  -F allow_squash_merge=true -F allow_merge_commit=true -F allow_rebase_merge=false \
  -F delete_branch_on_merge=true \
  -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=PR_BODY \
  -f merge_commit_title=PR_TITLE -f merge_commit_message=PR_BODY >/dev/null

# Update our named rulesets in place; never delete another rule or weaken protection
# temporarily. Required checks match the Go and native lifecycle matrices.
for branch in develop main; do
  method=squash
  if [[ "$branch" == main ]]; then method=merge; fi
  payload="$(cat <<JSON
{
  "name": "$branch branch policy", "target": "branch", "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/heads/$branch"], "exclude": []}},
  "rules": [
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0,
      "dismiss_stale_reviews_on_push": true, "require_code_owner_review": false,
      "require_last_push_approval": false, "required_review_thread_resolution": true,
      "allowed_merge_methods": ["$method"]}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": true,
      "required_status_checks": [
        {"context": "go (ubuntu-latest)"}, {"context": "go (macos-latest)"},
        {"context": "go-lifecycle (ubuntu-latest, systemd)"},
        {"context": "go-lifecycle (macos-latest, launchd)"}]}},
    {"type": "non_fast_forward"}, {"type": "deletion"}
  ]
}
JSON
)"
  if ! existing="$(gh api "repos/$repo/rulesets" 2>&1)"; then
    echo "Cannot read rulesets; merge settings and local hook applied: $existing" >&2
    exit 1
  fi
  id="$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$branch branch policy\") | .id")"
  endpoint="repos/$repo/rulesets"
  verb=POST
  if [[ -n "$id" ]]; then endpoint="$endpoint/$id"; verb=PUT; fi
  if ! result="$(gh api --method "$verb" "$endpoint" --input - <<< "$payload" 2>&1)"; then
    echo "Ruleset configuration failed; check account support and permissions: $result" >&2
    exit 1
  fi
  echo "$branch: PR + $method, Go and native lifecycle checks, no force-push or deletion"
done
