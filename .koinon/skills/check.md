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

## Base branch
origin/develop
## Required checks
go vet ./...
go test -race -timeout 30m ./...
git diff --check origin/develop...HEAD
git diff --check
Scan added lines and read the final diff for private content; inspect every matching path or
address individually. Confirm visible changes have an Unreleased CHANGELOG.md entry.
## Conditional checks
For shell edits: bash -n scripts/setup-repo.sh and sh -n .githooks/pre-commit.
Runtime or installation changes: four CGO_ENABLED=0 builds (linux/amd64, linux/arm64,
darwin/amd64, darwin/arm64), plus compile/vet native-tagged tests locally without enabling
real services. Actual service-manager checks run only on disposable guarded CI runners.
## Required CI
Full Linux/macOS Go matrix and required native lifecycle jobs on the exact PR head.
