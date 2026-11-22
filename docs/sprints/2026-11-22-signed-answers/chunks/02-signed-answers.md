# Chunk 02 — Signed answers

Cites `../decision.md` (Scope decision items 7 to 10; acceptance 5 to 9), `../contract.md`
sections 2 to 5 and `../definition-of-done.md` (Envelope version 2; Signed answers;
Documentation and records; Checks and evidence).

## Outcome

A client that signs a version 2 envelope gets an answer that the target signed with its link
key, or a claim that the node signed with its own. It can check each one against the pins that
it enrolled. Version 1 works as before. Go and Koinon Voice check the same vectors. The live
check with a Koinon Voice build is recorded.

## Work

1. `internal/signed`: version 2 parse, check and signed bytes; the answer object, its signed
   bytes, signing with an Ed25519 link key, and a verifier.
2. Target: sign each answer to a v2 envelope (`internal/core/signed_actions.go`,
   `maintainerRead`), with the link identity (`internal/link/identity.go`).
3. Node: return a target's signed answer with its signed field values unchanged
   (`internal/core/signed_forward.go`): `validReply` checks the v1 form for a v1 envelope and
   the signed object of `../contract.md` section 3.1 for a v2 envelope, and also passes an
   earlier target's unsigned `signature_refused`, reason `version`, through unchanged and
   unsigned (`../contract.md` section 4). Sign the node's own claims for v2.
4. Size: the 1 MiB answer limit counts the signed object.
5. Vectors: a Go test that produces and checks `contract.md` section 5; the values go into
   `contract.md`.
6. Records and docs: PDR 011, Decision 6 shipped and #696; ADR 002; PROTOCOL.md; CHANGELOG.md.
7. Live check: `../verification.md` with the steps and results, on the computers and build that
   the maintainer names.

## Tests

The Envelope version 2 and Signed answers tests of the definition of done; the four
`CGO_ENABLED=0` builds.

## Out of this chunk

Nothing of #695 or #696.
