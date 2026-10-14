# Chunk 01 — decision records and agent queries

No dependency. Cites `decision.md` (criteria 2, 6, 9, 10, 11) and `definition-of-done.md`
(Records and agent queries, Content, Storage).

## Storage

Schema: the next free version on develop. Two ordinary tables, created in `migrate`:

- `decision_items`: integer ID; unique reference (`[A-Za-z0-9-]`, at most 32 bytes); item
  order; title; where; agent text; context; recommendation; kind (`decision` or `fyi`);
  state (`active`, `in_progress` or `archived`); commands (JSON array); revision; author
  family and peer name; created, updated and archived times. Indexes: (state, kind, ID) for
  the lists, (state, archived time, ID) for the deep-archive sweep, and the unique
  reference. Agents do not write the state; this chunk creates every item active.
- `decision_recipients`: item ID, ordinal, the name as written, the resolved kind
  (`participant` or `peer`) and target, or the reason it did not resolve. Index: (kind,
  target, item ID) for the `mine` list.

Limits are in the definition of done. Field byte limits: reference 32, title 256,
where 256, agent text 8 KiB, context 16 KiB, recommendation 8 KiB. Recipients are resolved
when the item is written and again when the board or a verdict reads them.

## Agent interface

MCP tools, `POST /v1/decisions/<op>` and `koinon decision <op> --as FAMILY:ID`, with caller
identity from native metadata as for every tool:

- `decision_create`: reference, item order, title, where, agent text, context,
  recommendation, kind, commands, recipients, key, deadline. The fixed key and deadline make
  a retry return the first result.
- `decision_edit`: reference, `if_revision`, and the fields to change. A stale revision is
  `stale_revision`. Editing a decided item is refused in this chunk with `decided`; chunk 02
  replaces this with reopen.
- `decision_list`: `filter` (`waiting`, `active`, `in_progress`, `archived`, `mine`),
  `after`, `limit`.
  Summaries only, keyset-paged on item ID as the definition of done states.
- `decision_get`: reference. The full record, with its recipients and their resolution.
  Chunk 02 adds the current verdict, the history count and `decision_history`.

Validation, credential-format refusal and the command rule apply on every write. The
guide's new Decisions section names the tools and the ASD-STE100 rule for card prose.

## Dashboard

A read-only Decisions view lists active items as escaped plain text, with a reference lookup,
so that the chunk is usable on its own. Chunk 03 replaces it with the designed board.

## Docs

PROTOCOL (records, limits, refusals, credential formats), USAGE, the guide, tool
descriptions and CHANGELOG.
