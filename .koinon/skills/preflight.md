# Repository facts — preflight

## Version
1
## Skill
preflight
## Sources
- AGENTS.md
- CONTRIBUTING.md
- docs/engineering/wdr/004-publication-and-verification.md
- docs/product/pdr/008-portable-skills-explicit-adoption.md
- .koinon/preflight/lenses.md
- scripts/preflight-run.sh
- scripts/preflight-publish.sh

## Review run command
scripts/preflight-run.sh --base origin/develop, for an author of any family. Each lens runs
once in a fresh claude worker in a detached worktree of the head, with the change in
.preflight/change.diff, no forge credential and no Koinon connection.
## Review publish command
scripts/preflight-publish.sh PR, with the author's gh login. It takes the newest result of
the pull request's current head, and posts one comment and the preflight commit status on that
head. It refuses when there is no result for that head, when the result was run against
another base than the pull request's, and when a report has a secret, a private path or an
email address. Each report is posted as a fenced block.
## Lens policy
.koinon/preflight/lenses.md at the base commit: security, code, standards and refusal. The
change that adopts preflight runs with the head's policy, because its base has none.
## Base ref
origin/develop
## Route and image rules
The worker is `claude -p` on this computer, for an author of any family, with no MCP servers,
no forge token and no Git credential prompt. It has only the Read, Grep and Glob tools, loads
no settings, auto-memory or instruction files, and reads only its worktree; Claude Code still
gives it the account's email address. A codex worker is not used: Codex's read-only sandbox can
read outside the worktree. The publish command checks each report before it posts it.
## Report folder
_preflight/HEAD/RUN/ in the main checkout, ignored by Git.
## Review procedure
Accept or drop each finding with a reason, as for peer-review findings (WDR 003, Decision 4);
fix the accepted ones, commit, and run again at the new head.
## Docs and agent files
README.md, PROTOCOL.md, CONTRIBUTING.md, AGENTS.md, CLAUDE.md, CHANGELOG.md (Unreleased),
docs/USAGE.md, docs/INSTALL.md, docs/REMOTE-DASHBOARD.md, docs/PERMISSIONS.md,
docs/WORK-ITEMS-COMMANDS.md, agents/skills/README.md, agents/skills/AGENTS.md, each
agents/skills/*/SKILL.md, the guide text in internal/setup/guide.go (`koinon guide --agent
FAMILY`), the command usage text in cmd/koinon/main.go, and the ADR, PDR and WDR README
indexes. The historical documents that say so are out of scope.
