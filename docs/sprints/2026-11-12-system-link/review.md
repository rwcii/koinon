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

codex-koinon reviewed cf907fa on #671: peer review and refusal review, changes requested
([peer](https://github.com/rwcii/koinon/pull/671#issuecomment-6838002030),
[refusal](https://github.com/rwcii/koinon/pull/671#issuecomment-6838003395)).

| Finding | Disposition |
| --- | --- |
| F1 (P2, also the refusal finding): NAME at most 64 bytes and a 318-byte total refuse existing valid names, for example a 66-byte participant address with a 253-byte COMPUTER | Accepted. NAME accepts every name that a local send accepts (up to 256 bytes); the full address is up to 510 bytes; tests include a name longer than 64 bytes and a 256-byte name, each with a 253-byte COMPUTER |
| F2 (P2): a cancel of an attempted head before the receiver's commit leaves highWater unchanged, so the next sequence is a permanent gap; chunk 03 defines only `deliver` | Accepted. An ordered `cancel` operation resolves the head's sequence: "cancelled" with no insert at highWater+1, or the stored result at highWater; the sender sends the next head only after that result; tests cover cancel before and after the commit and a lost cancel reply |
