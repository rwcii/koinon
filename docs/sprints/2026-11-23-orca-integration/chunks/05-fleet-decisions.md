# Chunk 05 — Fleet Decisions

Cites `../decision.md` (The fleet board; Node keys in the mesh; acceptance 5 and 6) and
`../definition-of-done.md` (Fleet Decisions).

## Outcome

A Koinon instance with the fleet role shows a **Fleet** item in its navigation and a Fleet
Decisions page with the open cards of every connected node, read live. A verdict there needs
the admin password and is signed by the fleet board's own key for one node and revision.

## Work

1. `internal/fleet`: the key store (Ed25519 key encrypted with a key derived from the admin
   password by a memory-hard KDF, parameters stored beside it, file mode 0600 in the state
   directory), unlock for one action, and the signed client (version 2 envelopes, the answer
   check with `signed.VerifyAnswer`).
2. `cmd/koinon/fleet.go`: `koinon fleet enable` (asks for the admin password twice, makes the
   key), `koinon fleet enroll` (writes the enroll statement for a node), `koinon fleet status`,
   `koinon fleet disable` (removes the keys).
2a. Reads: the read authentication that the maintainer decides (pending card; recommendation:
   a second key with scope `read`, and a key scope in `internal/core/maintainer_keys.go` that
   refuses writes for it).
2b. Unreachable node: unreachable before the send, unknown outcome after it, the outcome read
   when the node answers again; nothing queued.
3. Dashboard: the **Fleet** item in the navigation when the role is on; Fleet Decisions with
   the Decisions page layout, each row's node, a node filter, unreachable nodes shown as such;
   the node's own cards read locally; the verdict form asks for the admin password.
4. Audit on the board for each action; the node's audit and verdict record show
   `by USER (fleet-board)` through the existing signed path.
5. Records and docs: PDR 011 (the fleet board decision; the node key check for spec section
   3); PROTOCOL; USAGE; README; CHANGELOG.

## Tests

The cases of the definition of done, with `-race`; the live check on one Linux and one macOS
node into `../verification.md`.
