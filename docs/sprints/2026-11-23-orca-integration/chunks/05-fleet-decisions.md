# Chunk 05 — Fleet Decisions

Cites `../decision.md` (The fleet board; Node keys in the mesh; acceptance 5 and 6) and
`../definition-of-done.md` (Fleet Decisions).

## Outcome

A Koinon instance with the fleet role shows a **Fleet** item in its navigation and a Fleet
Decisions page with the open cards of every connected node, read live. A verdict there needs
the admin password and is signed by the fleet board's own key for one node and revision.

## Work

1. `internal/fleet`: the key store (the write key, Ed25519, encrypted with a key derived from
   the admin password by a memory-hard KDF, parameters stored beside it; the read key, Ed25519,
   mode 0600 like the node's other private keys; both in the state directory), unlock of the
   write key for one action, and the signed client (version 2 envelopes, the answer check with
   `signed.VerifyAnswer`).
2. `cmd/koinon/fleet.go`: `koinon fleet enable` (asks for the admin password twice, checks the
   length rule of the maintainer secret, makes both keys), `koinon fleet enroll` (writes one
   enroll statement for each key, scope `read` or `full`), `koinon fleet status`, `koinon fleet
   disable` (removes the keys).
2a. Key scope in `internal/core/maintainer_keys.go`: the enroll statement names `read` or
   `full`; keys added before the change are `full`; a `read` key is refused for each write
   (`signature_refused`, reason `scope`). The read key signs every read of the Fleet pages.
2b. Unreachable node: unreachable before the send; unknown outcome after it. The board keeps
   the signed envelope until its answer is known and resends the same bytes when the node
   answers: the first answer, a single run within 5 minutes, or an expired refusal (never ran);
   after 7 days, a live read with the read key. Nothing is queued.
3. Dashboard: the **Fleet** item in the navigation when the role is on; Fleet Decisions with
   the Decisions page layout, each row's node, a node filter, unreachable nodes shown as such;
   the node's own cards read locally; the verdict form asks for the admin password.
4. Audit on the board for each action; the node's audit and verdict record show
   `by USER (fleet-board)` through the existing signed path.
5. Records and docs: PDR 011 (the fleet board decision; the read key, the outcome check, no
   verdict queue and the admin password rule; the node key check for spec section 3);
   PROTOCOL (the key scope and reason `scope`); USAGE; README; CHANGELOG.

## Tests

The cases of the definition of done, with `-race`; the live check on one Linux and one macOS
node into `../verification.md`.
