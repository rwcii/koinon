# Preflight lenses

Koinon's preflight lens policy (PDR 008, Decision 5; WDR 004, Decision 8). Each "## NAME"
section is one enabled lens; its text is the brief that `scripts/preflight-run.sh` gives that
lens's worker. A section with no text takes the standard lens of that name in
`agents/skills/preflight/SKILL.md` (Standard lenses), at the same commit. The maintainer chose
the first four lenses on 2026-11-23, adding refusal to the three proposed: "security, code,
standards, refusal", and added the wording lens on 2026-11-24 (WDR 004, Decision 8). The run
reads this file and the skill at the base commit, so a change cannot weaken its own lenses.

## security

Find security defects in the change. Read ADR 001 (native caller identity, the loopback daemon
and its private store) and the records that the change cites. Check: credentials and secrets
stay in their private files and out of logs, messages, URLs and public text; file modes of
private state; input that reaches a shell, a path, SQL, HTML or a tmux command without quoting
or validation; path traversal and symlinks; listeners that leave loopback or the tailnet;
signed actions and their checks; a new network call or dependency. Report each defect with its
failure scenario.

## code

Find correctness defects in the change by reading it. Check: each changed behavior and its
error paths; races and leaks (CI runs `go test -race`; read the code for them); Linux and macOS
differences (`internal/platform`); upgrade from the released state; tests that prove the change
and would fail without it; tests that reach the person's real configuration, services or tmux
sessions (WDR 006). Report each defect with the input that breaks it.

## standards

Check the change against the repository's records and docs. Each significant decision has
one record with its source, scope and status, in the pull request that does the work (WDR 001,
Decision 7); a maintainer decision carries the person's name, the date, the context, the
words and the reason. README, USAGE, PROTOCOL, INSTALL, CONTRIBUTING, the skills and their
README agree with the code at the head (WDR 004, Decision 7). Each visible change has an
Unreleased CHANGELOG entry. Public text holds no private path, address or name.

## refusal

Check the change for new refusals, gates and restrictions that have no source: text, a
setting, a hook, a code condition or a test that blocks approved work. Each one must cite the
maintainer decision or the record that requires it (WDR 001, Decision 6; #487). Check that a
maintainer decision recorded in the change has its context, the maintainer's words and its
scope, and that no decision is attributed to the maintainer without them.

## wording
