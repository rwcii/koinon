# Chunk 11 — install and upgrade

Read `../decision.md`, `../definition-of-done.md`, `docs/INSTALL.md` and
`docs/RUNTIME-UPGRADE-DESIGN.md` first.

## Outcome

- `koinon install` places the binary, writes and starts the systemd user unit or launchd agent,
  and runs `koinon setup` for the agents the user names.
- `koinon import` reads a Python-era state tree (inboxes, checkpoints, memory stores, work items,
  claims) and writes it into the daemon's database; it is idempotent and resumable.
- The upgrade from the `main` release: a documented command stops the Python services, imports,
  starts the Go daemon, reconfigures the agents, and removes the Python services only after the
  import verifies. A failure before that point leaves the Python runtime running.
- `koinon uninstall` stops and removes the service and the agent configuration it added, and
  keeps the state.
- The release workflow builds the four binaries and attaches them with checksums.

## Tests

The import cases of the definition of done; the native workflows on Ubuntu and macOS for
install, upgrade from the current `main` release, and uninstall.

## Done

Gate passes; live check 4 recorded on the pull request.
