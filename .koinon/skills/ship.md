# Repository facts — ship

## Version
1
## Skill
ship
## Sources
- AGENTS.md
- CONTRIBUTING.md
- .github/workflows/tests.yml
- .github/workflows/native-go-lifecycle.yml

## Work target
feature/*, fix/* or chore/* branch from develop, in an isolated worktree; squash PR into develop.
## Commit policy
Cryptographically signed, human-authored DCO commits (git commit -S -s); no automated attribution.
Refer to the maintainer in Git prose. Never commit directly to develop or main.
## Required checks
Use check and .koinon/skills/check.md: go vet ./..., go test -race -timeout 30m ./...,
whitespace/private-content checks, shell syntax when affected, four CGO-free builds and
native-tagged compile/vet for runtime/install changes. Add visible changes under Unreleased.
## PR and merge policy
One open PR per target branch, including drafts, across every agent family. Fetch and merge
latest develop before freezing or pushing; merge develop into a published branch, never
rewrite it. A different agent family reviews the exact frozen head, records its verdict on
the PR and sets peer-review status on that same full hash. Required CI and conversations
must pass before merge. Use squash auto-merge for every PR into develop, under the
maintainer's authorized task. Read back the merge and issue state.
Promotion to main has its separate release gate. Close only issues fully delivered.
