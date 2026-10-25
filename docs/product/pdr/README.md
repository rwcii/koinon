# Product Decision Records (PDRs)

PDRs document **how Koinon behaves** — what each command, tool and dashboard action does for
the maintainer and the agent sessions it serves. For process rules, the template, naming, and
lifecycle, see [PDR_PROCESS.md](PDR_PROCESS.md).
[WDR 001](../../engineering/wdr/001-decision-records-govern-constraints.md) records why
Koinon keeps these records and how a constraint becomes one.

## How PDRs Relate to ADRs

ADRs document **how the system is built** (architecture, patterns, infrastructure). PDRs
document **how the product behaves**. The two often come in pairs: an ADR decides the
mechanism, a PDR decides the behavior that mechanism serves. Cross-reference via
`related_adrs` / `related_pdrs` in frontmatter.

## PDRs

| Number | Title | Status |
| ------ | ----- | ------ |
| 001    | [Session Registration and Participant Continuity](001-session-registration-participant-continuity.md) | Draft |
| 002    | [Native Wake and Terminal Interaction](002-native-wake-terminal-interaction.md) | Draft |
| 003    | [Runtime Installation and Recovery](003-runtime-installation-and-recovery.md) | Draft |
| 004    | [Coordinated Work Results and Role Routing](004-work-results-role-routing.md) | Accepted |
| 005    | [Room Conversations and Human Origin](005-room-conversations-human-origin.md) | Accepted |
| 006    | [Context Monitoring and Managed Handoffs](006-context-monitoring-managed-handoffs.md) | Draft |
| 007    | [Human Decision Board](007-human-decision-board.md) | Draft |
| 008    | [Portable Skills and Explicit Adoption](008-portable-skills-explicit-adoption.md) | Draft |
| 009    | [Maintainer Secret and Maintainer MCP Mode](009-maintainer-secret-and-mcp-mode.md) | Accepted |
