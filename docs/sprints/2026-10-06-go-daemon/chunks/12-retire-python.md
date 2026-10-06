# Chunk 12 — retire Python

Read `../decision.md` and `../definition-of-done.md` first.

## Outcome

The Python runtime, its tests, its entrypoints and its CI jobs are removed. `README.md`,
`PROTOCOL.md`, `docs/INSTALL.md`, the root `AGENTS.md`, `CONTRIBUTING.md`, the agent skills and
the guidance describe the Go runtime only. Design documents of removed Python subsystems are
removed or marked historical. `CHANGELOG.md` records the change. The milestone issues are closed
with the evidence of the chunk that delivered each.

## Tests

The full Go suite and the native workflows.

## Done

Gate passes; the `release` skill can promote `develop` to `main`.
