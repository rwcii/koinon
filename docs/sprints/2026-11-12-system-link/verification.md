# System link — verification

Host names and addresses in this file are synthetic examples.

## Raw TCP Serve probe, before chunk 01 code — 2026-11-12

Reported by claude-koinon; same host only. A second device is covered by the live Linux and
macOS evidence of chunk 03.

- Host: Linux, Tailscale 1.104.5, Remote on (the dashboard's HTTPS mapping on port 443).
- Probe: a standalone Go program outside the repository. Server on `127.0.0.1:47901` and
  client, each with its own Ed25519 self-signed certificate, TLS 1.3 only, the server with
  `RequireAnyClientCert`; both check the other's SHA-256 SubjectPublicKeyInfo pin in
  `VerifyConnection`.

| Step | Command or target | Result |
| --- | --- | --- |
| Loopback baseline | client to `127.0.0.1:47901` | pass: reply, TLS 1.3, both pins match |
| Mapping | `tailscale serve --bg --tcp=47902 tcp://127.0.0.1:47901` | `serve status` shows `"47902": {"TCPForward": "127.0.0.1:47901"}` |
| By tailnet name | client to `devbox.example-tailnet.ts.net:47902` | fail before TLS: the operating system's resolver (`127.0.0.53`) has no answer for the tailnet name |
| By Tailscale address | client to `100.64.0.10:47902` | pass: reply, TLS 1.3, server pin matches at the client, client pin matches at the server |
| Cleanup | `tailscale serve --tcp=47902 off` | `serve status` shows only the dashboard's port 443 again |

Nothing else listened on port 47902, so the passing connection used the Serve TCP forward.
Result: the raw TCP mapping passes mutual TLS 1.3 unchanged. Dialing by tailnet name through the
operating system's resolver is not reliable, so the definition of done now dials saved Tailscale
addresses and refreshes them from `tailscale status` (codex-koinon agreed, 2026-11-12).
