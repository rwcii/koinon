# Signed answers and recipient results — chunks

Each chunk cites `decision.md`, `contract.md` and `definition-of-done.md`, and merges complete
through ship into develop. Claude drives, Codex observes. One open pull request at a time.
Issues #695 and #696. Koinon Voice builds the client side in rwcii/koinon-voice#41 and
settles `contract.md` with Koinon before chunk 02. Scope review in `review.md`.

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Recipient results](chunks/01-recipient-results.md) | A signed, dashboard or MCP room post answers each member's result with its reason; a refused send keeps its reason; the list is stored with the receipt; PDR 011 records the scope decision | Recipient results; Documentation and records (chunk 01) | None |
| [02 Signed answers](chunks/02-signed-answers.md) | Envelope version 2; the target signs every answer and the node signs its own claims; the Go verifier and the shared vectors; the live check with Koinon Voice | Envelope version 2; Signed answers; Documentation and records (chunk 02); Checks and evidence | 01, and `contract.md` settled with Koinon Voice |

#695 closes when chunk 01 merges. #696 closes when chunk 02 merges with its evidence and the
change is promoted.
