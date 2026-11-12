# Chunk 01 — pairing

No dependency. Cites `decision.md` (LINK-670-WHO, LINK-670-PAIR, LINK-670-ADDR items 1 to 4)
and `definition-of-done.md` (Identity, name and keys; Pairing and Remote; Documentation and
records).

## Before code

A real probe on a tailnet host: a raw TCP Serve mapping (`tailscale serve --bg --tcp=PORT
tcp://127.0.0.1:BACKEND`) passes a TLS 1.3 handshake with a client certificate unchanged, from
another tailnet device or from the host to its own tailnet name. Record the commands, the
Tailscale version and the result in `verification.md`. If the probe fails, stop and correct
the plan (WDR 001, Decision 2).

## State

Private files in the state directory, mode 0600, owner-checked, written atomically:

- `link-key.pem`: the Ed25519 key and its self-signed certificate.
- `link.json`: version, advertised name, link port, and the pairs: pair ID, generation,
  advertised name, effective name, override, pin, tailnet name and port, state (`pending` or
  `paired`), created time. Pending records carry the invitation side and expiry.
- Open invitations stay in memory and in `link.json` as hashes of the secret only.

## Remote

`RemoteConfig` gets the link mapping beside the dashboard mapping, under the same write-ahead
ownership rules. `remote.Manager` gets `EnsureTCP` and `RemoveTCP` for the raw TCP forward,
with the same inspect, check, run and read-back steps as `Ensure` and `Remove`. Enable opens
the loopback link listener, then the mapping; disable closes link sessions and the listener
first, then removes the mapping; the runtime off state rules apply as for the dashboard.

## Link listener

`crypto/tls` with TLS 1.3 only, `RequireAnyClientCert`, and `VerifyConnection` that accepts a
pinned key or, while an invitation is open, the pairing exchange only. A small framed protocol
on the TLS stream (length-prefixed JSON) with operations `pair-hello`, `pair-confirm` and
`ping` in this chunk. The client side dials the paired computer's tailnet name and link port
and checks the server's pin in `VerifyConnection`.

## Interfaces

- Dashboard Remote settings: this computer's advertised name and fingerprint, **Add a
  computer** (Copy invitation, with QR; Paste invitation and Connect), the paired computers
  with state and time, Rename, Unpair, Cancel pending.
- CLI: `koinon link name|invite|join|status|unpair|cancel`, maintainer secret as for
  `koinon maintainer`; `join` reads standard input or the terminal.
- `koinon remote status` and the dashboard status include the link listener and mapping.

## Tests

The tests of the two definition-of-done sections, with two in-process daemons, real TLS and
the fake Tailscale adapter.
