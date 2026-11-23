# Repository facts — release

## Version
1
## Skill
release
## Sources
- AGENTS.md
- CONTRIBUTING.md
- .github/workflows/release.yml
- .github/workflows/native-go-lifecycle.yml
- docs/INSTALL.md

## Promotion path
Merge the PR from develop to main.
## Publish steps
Use the version tag on the verified main merge. The tag triggers Release binaries:
four CGO-free binaries, their Homebrew bottles, koinon-skills.tar.gz and SHA256SUMS; the
homebrew-check job pours the bottles; then GitHub release assets, then the Homebrew tap formula.
Verify all publication jobs and the resulting binaries/formula; reconcile an uncertain run.
## Release checks
Dated changelog, exact-head CI and independent review, supported fresh installation evidence,
and upgrade from current main in a scratch prefix that keeps its state. The real
service-manager checks run on disposable CI runners. Verify the candidate and the current
released baseline.
Failed CI runs (WDR 004, Decision 6): the baseline is the previous release tag on main. List
the runs of every workflow on all branches from the tag's commit time through the candidate's
exact-head CI with the repository Actions runs API (`created>=` the commit time, every page;
split the time range when a query reaches 1,000 results). For a run with `run_attempt` above 1,
read each attempt 1..N from its attempts endpoint. List each failed run and attempt in the
promotion PR evidence with its cause, fix or open issue.
## Docs and agent files
README.md, PROTOCOL.md, CONTRIBUTING.md, AGENTS.md, CLAUDE.md, CHANGELOG.md (Unreleased),
docs/USAGE.md, docs/INSTALL.md, docs/REMOTE-DASHBOARD.md, docs/PERMISSIONS.md,
docs/WORK-ITEMS-COMMANDS.md, agents/skills/README.md, agents/skills/AGENTS.md, each
agents/skills/*/SKILL.md, the guide text in internal/setup/guide.go (`koinon guide --agent
FAMILY`), the command usage text in cmd/koinon/main.go, and the ADR, PDR and WDR README
indexes. The historical documents that say so are out of scope.
