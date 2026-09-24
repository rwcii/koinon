# Chunk 01 — Identity

Criteria 3 (status) and 4 of [decision.md](../decision.md). Closes #131.

## Current state

- `session.py` `main()`: when `native-service.json` is present, `ensure` and `status` return
  `session_service.main([...])`, whose result (`session_service.py`, status;
  `koinon/session_service_manager.py`) has lifecycle and owner data only.
- The legacy path prints `result()`: `status`, `name`, `state_dir`, `start_command`,
  `inbox_command`.
- For an unregistered thread, the lock block's `else` branch calls `save_registration` for every
  action, `status` included. `koinon/session_install.py` `stage()` then refuses the saved
  `session.json` ("existing legacy session requires explicit upgrade").

## Change

1. `status` for a thread with no `session.json` returns
   `{"status": "unregistered", "state_dir": ..., "agent": ...}` and exits 0. It creates no
   file anywhere: no lock file in the prefix or the state directory, no directory, no
   registration. Any configuration read on this path uses the read-only
   `runtime_names.install_config`, never `install_state.locked`. It prints no name: a name is
   assigned only at registration.
2. Native `ensure` and `status` add `name`, `state_dir` and `inbox_command` from the saved
   registration to the service result, with the same values the legacy `result()` gives.
3. `docs/INSTALL.md` ("Enable the current session", "Use and inspect") and `CHANGELOG.md`
   describe the output and the read-only `status`.

## Tests

The "Read-only status and guide" tests of the definition of done that name `status` and
`ensure`, on legacy and native selections, Linux and macOS.
