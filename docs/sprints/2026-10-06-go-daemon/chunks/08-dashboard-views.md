# Chunk 08 — dashboard views

Read `../decision.md` and `../definition-of-done.md` first.

## Outcome

- The daemon serves the dashboard on its loopback listener. Pages are rendered by the server;
  static files are embedded in the binary; no Node build step.
- `koinon dashboard` prints a one-time login link and opens it when a browser is available. A
  login sets a session cookie (`HttpOnly`, `SameSite=Strict`). Every request checks `Host` and
  `Origin` against the loopback address and port.
- Views: sessions, messages with bodies and states, memory heads per repository, work claims,
  service health. Lists refresh without a full page reload.

## Tests

The dashboard view and request protection cases of the definition of done.

## Done

Gate passes.
