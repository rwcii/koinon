# Repository facts — peer-review

## Version
1
## Skill
peer-review
## Sources
- AGENTS.md
- CONTRIBUTING.md
- PROTOCOL.md
- docs/engineering/wdr/001-decision-records-govern-constraints.md
- docs/engineering/wdr/002-authority-within-assigned-task.md
- docs/engineering/wdr/004-publication-and-verification.md
- docs/architecture/adr/001-native-identity-local-runtime-trust.md

## Extra review gates
Frozen exact head; one verified batch, bounded verification of accepted fixes. Use the repository tests required by affected behavior.
Read the records the change cites and verify their scope. Unsourced refusals, gates and
restrictions are the separate refusal-review gate (WDR 001, Decision 6).
Review platform differences in internal/platform (AGENTS.md, WDR 004), the provider socket
paths and the content-free notices (ADR 001, Decision 3). Check that visible changes have an
Unreleased changelog entry (CONTRIBUTING.md, WDR 004).
## Verdict format
PR comment bound to the full commit and peer-review status pointing to that comment; read back state and URL.
