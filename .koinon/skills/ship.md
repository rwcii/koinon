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
- docs/engineering/wdr/004-publication-and-verification.md
- scripts/board-verdict.sh

## Work target
feature/*, fix/* or chore/* branch from develop, in an isolated worktree; squash PR into develop.
## Commit policy
Cryptographically signed, human-authored DCO commits (git commit -S -s); no automated attribution.
Refer to the maintainer in Git prose. Commit on the work branch; develop and main take changes through PRs.
## Required checks
Use check and .koinon/skills/check.md: go vet ./..., go test -race -timeout 30m ./...,
whitespace/private-content checks, shell syntax when affected, four CGO-free builds and
native-tagged compile/vet for runtime/install changes. Add visible changes under Unreleased.
## PR and merge policy
One open PR per target branch, including drafts, across every agent family. Fetch and merge
latest develop before freezing or pushing; merge develop into a published branch, keeping
its history. The other agent family reviews the exact frozen head, records its verdict on
the PR and sets peer-review status on that same full hash; it also runs refusal-review and
sets the refusal-review status on that hash (WDR 001, Decision 6). The ruleset merges with
required CI passed and conversations resolved. Use squash auto-merge for every PR into develop.
Read back the merge and issue state. Promotion to main uses the release skill. Close each issue
when it is fully delivered.
## Extra checks
Before the pull request opens, run the preflight skill at the frozen head with
.koinon/skills/preflight.md; after it opens, post the result with
scripts/preflight-publish.sh PR. A new head needs a new run (WDR 004, Decision 8).
## Merge approval
A board approval for each pull request into develop (WDR 004, Decision 8). After preflight,
peer-review and refusal-review pass on the frozen head, put a card on the decision board that
names the pull request (#N) and its full head. After the maintainer approves it, run
scripts/board-verdict.sh --as FAMILY:ID PR HEAD REFERENCE, which posts the board-verdict status
on that head. Set auto-merge after that status reads back as success.
## Docs and agent files
README.md, PROTOCOL.md, CONTRIBUTING.md, AGENTS.md, CLAUDE.md, CHANGELOG.md (Unreleased),
docs/USAGE.md, docs/INSTALL.md, docs/REMOTE-DASHBOARD.md, docs/PERMISSIONS.md,
docs/WORK-ITEMS-COMMANDS.md, agents/skills/README.md, agents/skills/AGENTS.md, each
agents/skills/*/SKILL.md, the guide text in internal/setup/guide.go (`koinon guide --agent
FAMILY`), the command usage text in cmd/koinon/main.go, and the ADR, PDR and WDR README
indexes. The historical documents that say so are out of scope.
