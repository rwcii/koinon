# Chunk 02 — Participants

Criteria 1, 2, 9 and 10 of `decision.md`, and the upgrade constraint.

## Current state

- `names` holds one `alias` row per family and repository (`names_alias` unique index), with
  `holder_id`. `assignNames` (`internal/core/names.go`) reserves the alias and gives it to the
  registering or renewing session when the recorded holder is no longer active
  (`TestAliasHolderMoves`).
- `sessions` has no role. A launch record (`launches`, `core.LaunchTarget`) is used only for
  non-Claude families.

## Change

1. **Schema (next version).** `sessions.role TEXT NOT NULL DEFAULT ''` and
   `sessions.subagent INTEGER NOT NULL DEFAULT 0` (chunk 01 may add the latter first);
   `names.role TEXT NOT NULL DEFAULT ''`; the alias index becomes unique on
   `(family, repository, role) WHERE kind='alias'`. A `participant_events` table records each
   holder change and refused change (participant, time, former holder, new holder, reason,
   actor `session`, `maintainer` or `daemon`), bounded by the existing retention rules. Existing
   alias rows keep their name and holder with role `''`.
2. **Role.** `koinon <family> --role <role>` validates the role (lower-case letters, digits and
   hyphens, 1 to 24 characters, starts with a letter, not made only of hexadecimal digits so it
   can never equal a peer-name suffix) and stores it in the launch record. `koinon mcp` sends the
   launch ID for every family; for Claude the store reads only the role from the launch record and
   keeps the Claude wake target. The registration carries no role from tool arguments.
3. **Address.** A participant's address is `<family>-<label>` without a role and
   `<family>-<label>-<role>` with one. When that name is taken, the existing `lengths` fallback
   applies and the result reports the address that was given.
4. **Holder rules** replace the alias part of `assignNames`, in the registration or renewal
   transaction:
   - A sub-agent session or a session without a repository has no participant.
   - When the participant's holder is this session, nothing changes.
   - When another active session holds it, nothing changes (chunk 04 adds succession).
   - When it has no active holder, count the active, non-retired, non-sub-agent sessions of the
     same family, repository and role that the participant has not fenced (chunk 03 adds the
     fence; until then none is fenced). When this session is the only one, it becomes the holder.
     Otherwise the holder stays empty and the participant records `conflict` with the qualifying
     peer names.
   - A participant event records every change.
5. **Maintainer choice.** A dashboard action on the session view, `participant-holder`, sets the
   holder of a participant to a named active session of that family, repository and role, with
   the usual CSRF, origin and revision checks, and is audited. It is refused for a sub-agent or a
   session of another participant. Chunk 03 adds fencing to this action.
6. **Reporting.** `Session` gains `role`, `subagent`, `address` (the participant's address, held
   or not) and `holds_address`. `peers` (MCP and `koinon peers`) and the dashboard session list
   show them, and the dashboard shows a participant's conflict and its last event.

## Done

- The store tests of `definition-of-done.md`, "Participants" and "Upgrade", with
  `TestAliasHolderMoves` replaced by tests of the new rules, including reversed renewal order and
  concurrent registration.
- Dashboard action tests (choice, refusals, audit). Launcher role validation tests.
- `PROTOCOL.md` (participants, roles, holder rules, the action), `docs/USAGE.md`,
  `docs/INSTALL.md` (`--role`), the installed `koinon guide` text, `CHANGELOG.md`.
