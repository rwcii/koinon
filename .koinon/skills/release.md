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
Merge PR from develop to main after the maintainer's separately recorded release approval.
## Publish steps
Use the approved version tag on the verified main merge. The tag triggers Release binaries:
four CGO-free binaries and SHA256SUMS, GitHub release assets, then the Homebrew tap formula.
Verify all publication jobs and the resulting binaries/formula; reconcile an uncertain run.
## Release checks
Dated changelog, exact-head CI and independent review, supported fresh installation evidence,
and upgrade from current main in a scratch prefix preserving state. Use disposable guarded
CI for real service-manager checks; no service-manager tests in the maintainer's account.
Verify the candidate and current released baseline before requesting the final release approval.
