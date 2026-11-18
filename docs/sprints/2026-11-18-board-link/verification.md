# Live check — decision cards over the link (#746)

The live check of chunk 03 (`chunks/03-remote-read.md`, Work item 6, and its Live check decision),
run on 2026-11-18 between 19:58 and 21:05 UTC on two of the maintainer's paired computers. Private
runtime values (pair IDs, message and session IDs, addresses) are left out.

## Build

- Head: `9c5d4fe53ba6edd7efef7015cfab665a2a77fd35` of #750, after its peer review and refusal
  review passed and its full `go test -race -timeout 30m ./...` passed on a frozen worktree.
- Built from a clean frozen worktree of that head: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
  -trimpath -ldflags "-s -w -X main.commit=9c5d4fe53ba6edd7efef7015cfab665a2a77fd35" ./cmd/koinon`.
  `koinon version`: version `dev`, that commit. SHA-256 of the binary
  `cae69845302560727a3ae21571fde40299dc4c3983ac09a07409277dfd02d6b6`, the same on both computers.
- Installed with `koinon install` from that binary: nebula first, then blackhole (both
  linux/amd64, both were v0.13.1). Each daemon answered `status` with schema 40 and version `dev`.
  On nebula the install replaced the Homebrew link with a copy, as docs/INSTALL.md says for a
  binary from outside a keg.

## Existing cards (decision.md, item 7)

nebula held three recipient rows that name agents as `NAME@blackhole`, all `unknown_name` at
v0.13.1: one on an archived card, two on a card in progress.

| Step | Result |
| --- | --- |
| nebula upgraded, blackhole still v0.13.1 | The open card's two rows are `remote` with the blackhole pair. Its four notices (named and verdict, for each agent) are `unsupported` with `link_operation_unknown`. The archived card's row stays `unknown_name` (only open cards are repaired). |
| blackhole upgraded | All four notices `received` within 20 seconds, with no edit of the card. |

## Synthetic card

A synthetic decision card on blackhole, made for this check, names one test session on nebula by
its participant address, `claude-live-746-a@nebula`. Its context holds a unique marker text. Two
test sessions ran on nebula in a private tmux server, started with `koinon claude` and
`koinon codex`: session A holds that address; session B (Codex) is not named. No real agent was
tagged. The maintainer gave the verdict (approved) in the dashboard.

| Step | Session | Result |
| --- | --- | --- |
| Card created | — | The recipient resolves as `remote` with the nebula pair; its notice `received`. |
| Notice | A | Woken by its ordinary wake. One message from `decision-board@blackhole`: "You are named on decision card … on another computer (revision 1). The card lives on computer blackhole (pair …); read it there with decision_get REFERENCE@blackhole, or reference … and pair …." No card text. |
| `decision_get REFERENCE@blackhole` | A | The live card, revision 1, `active`, with `origin` `blackhole` and its pair. |
| `decision_list`, filter `mine`, computer `blackhole` | A | One item, this card; `more` false. |
| `decision_pending`, computer `blackhole` | A | No deliveries (no verdict yet). |
| `decision_get REFERENCE@blackhole` | B | `not_found` ("no decision item that names you has this reference"), with the computer and pair. |
| `decision_pending`, computer `blackhole` | B | No deliveries, with `origin`. |
| Verdict notice | A | "Decision card … has a verdict: approved (verdict … on its computer, revision 1). … After you handle the verdict, acknowledge it with decision_ack, verdict_ids […] and pair …." |
| `decision_pending`, computer `blackhole` | A | One delivery: approved, revision 1, with `entry`; the page has `record`. |
| `decision_ack`, the verdict ID and the pair | A | `acknowledged` [that ID], `already` [], `origin` blackhole. |
| The same `decision_ack` again | A | `acknowledged` [], `already` [that ID]. |
| `decision_pending`, computer `blackhole` | A | No deliveries. |
| `decision_get` on blackhole | — | The delivery to `claude-live-746-a@nebula` is `acknowledged`, its notice `received`. |

## Nothing stored on the agent's computer

After the reads and again after the verdict and the acknowledgements, nebula's store had no
`decision_items` row for the card, and no message held the card's marker text, title or where
text. A byte search of the database file and its write-ahead log for the marker and for two
phrases of the card's text found none.

## After the check

The test sessions and their private tmux server were stopped, and their directories and the
copied binary were removed from nebula. Both computers keep the `dev` build at schema 40 until
the next release. mac-studio, neutron and build-server stay at their releases; their notices
show `unsupported` until their upgrade.
