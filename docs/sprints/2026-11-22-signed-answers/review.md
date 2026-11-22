# Signed answers and recipient results — review

## Draft passes on `contract.md` (2026-11-22)

| From | Finding | Disposition |
| --- | --- | --- |
| claude-koinon-voice F1 | A node can forge the unsigned version refusal and strip the signatures | Section 4: no fallback to v1; shown as the node's unverified claim |
| claude-koinon-voice F2 | `status` as an HTTP status does not exist on the MCP and link paths | Section 3.2: one application status on every transport |
| claude-koinon-voice F3 | The client knows the node by its pin, not by an enrollment | Terms: the node pin is the pin of the node that the client sends through |
| claude-koinon-voice F4 | An unsigned answer to a write must not confirm "not run" | Section 3.5 |
| claude-koinon-voice F5 | Vectors for a signed `outcome`, a node `outcome_unknown` and a `session` | Section 5 |
| codex-koinon 1 | Compatibility of v1 and chunk 01; earlier receipts; agents' counts stay private | Sections 1.1, 1.2, 1.4 and 2.2 |
| codex-koinon 2 | `session` and answers given before a digest exists | Sections 2.5, 3 and 4 |
| codex-koinon 3 | Checks by role, code and transport; mixed releases; request IDs | Sections 2.4, 3.4, 3.5 and 4 |
| codex-koinon 4 | The answer size with escaping and the frame around it | Section 3.7 |
| codex-koinon 5 | Distinct node and target keys; role and strictness negatives | Section 5 |
| codex-koinon-voice | Transport status and MCP error flags must not override the verified answer | Section 3.6 |
| codex-koinon recheck 1 | The `session` exceptions contradict "each answer" | Sections 2.5 and 3; definition of done |
| codex-koinon recheck 2 | "Sends nothing more" conflicts with reading the outcome | Section 4: never sends the write again or as v1; may read a signed `outcome` |

claude-koinon-voice: no findings on revision 2.
