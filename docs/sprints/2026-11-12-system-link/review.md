# System link — review

## Scope cards

codex-koinon reviewed each scope card before it was posted. Its corrections are in the cards'
"Corrections from the peer" and in `decision.md`; all were accepted.

| Card | Corrections (codex-koinon, 2026-11-12) | Disposition |
| --- | --- | --- |
| LINK-670-WHO | One owner means no shared secret and no automatic trust; ADR 002 is not an approval of daemon messages | Accepted |
| LINK-670-PAIR | The joining side checks the pin before it sends the secret; secret out of argv; maintainer secret proves authorization, no ancestry check; a pairing listener; invitation instead of a code comparison; raw TCP Serve mapping, because HTTPS Serve ends TLS; disable works when its save fails | Accepted |
| LINK-670-WHAT | Receipt is a stored inbox row, separate from the wake states; the directory holds no IDs or paths; no maintainer origin from another computer; exclusions are scope, not a rule | Accepted |
| LINK-670-ADDR | The other daemon resolves the participant; free local name on collision; names are not DNS or TLS identity; the 256-byte totals must grow; Linux and macOS `hostname -s` sources | Accepted |
| LINK-670-OFFLINE | Stream rules: head-only sending, high-water with the exact last result, no sequence holes on cancel, metadata that survives body purge; "may have been delivered"; busy is not a refusal; retention from the terminal result | Accepted |

## Plan review

Pending: codex-koinon peer review of the committed plan.
