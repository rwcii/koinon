# Repository facts — check

## Version
1
## Skill
check
## Sources
- AGENTS.md
- CONTRIBUTING.md
- .github/workflows/tests.yml
- .github/workflows/native-go-lifecycle.yml
- docs/engineering/wdr/001-decision-records-govern-constraints.md

## Base branch
origin/develop
## Required checks
go vet ./...
gofmt -l .
go test -race -timeout 30m ./...
git diff --check origin/develop...HEAD
git diff --check
`gofmt -l .` prints nothing for a formatted tree. Scan added lines and read the final diff for private content, and inspect
each matching path or address. Confirm visible changes have an Unreleased CHANGELOG.md entry.
## Conditional checks
For shell edits: bash -n scripts/setup-repo.sh, sh -n .githooks/pre-commit, sh -n .githooks/commit-msg
and sh -n scripts/decision-citations.sh.
Runtime or installation changes: four CGO_ENABLED=0 builds (linux/amd64, linux/arm64,
darwin/amd64, darwin/arm64), plus a local compile and vet of the native-tagged tests. The
service-manager checks run on disposable CI runners.
When adding a decision record or changing its status under docs/architecture/adr,
docs/product/pdr or docs/engineering/wdr, verify the series README.md index has the same
number, title and status (WDR 001).
## Required CI
Full Linux/macOS Go matrix and required native lifecycle jobs on the exact PR head.
