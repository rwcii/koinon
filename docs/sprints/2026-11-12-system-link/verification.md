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

## Live Linux and macOS round trip (M15), chunk 03 — 2026-11-13

Reported by claude-koinon; the maintainer ran the macOS steps. Both computers ran a temporary
second Koinon built from 5764a03 with `live-check.sh` (this directory), each with its own state
directory and ports: agents 47681, link 47682, dashboard HTTPS 8444. The installed Koinon on
each computer did not change. Each agent used the build's MCP server through command-line
options.

### LINK-670-LIVE: how the live check runs

- **Decided by:** Robert Capps.
- **Date:** 2026-11-13, revision 2, approved.
- **Context:** the definition of done needs a live pairing and message round trip between a
  Linux and a macOS computer before the last chunk merges. The build moves a store to schema
  38, which the installed release cannot read. The options were a temporary second Koinon from
  the PR build on each computer, and an install of the PR build as the real Koinon on both.
- **Decision:** run a temporary second Koinon from the PR build on the Linux host and on the
  Mac, with its own state directory and ports; the maintainer runs the Mac steps; record the
  commands and results here with synthetic names; at the end each side turns Remote off, which
  removes its Serve mappings, stops the daemon and deletes its directory.
- **Reason and scope:** this check only. The real stores stay on the release; the check covers
  the PR build, not an installed upgrade.
- **Alternatives considered:** installing the PR build as the real Koinon (the stores could not
  go back to the release before the next release).

### Results

| Step | Command | Result |
| --- | --- | --- |
| Build, Linux | build of 5764a03 | binary in the check directory |
| Build, macOS | `live-check.sh build 5764a03…` | `go install` of the commit from the module proxy |
| Start | `live-check.sh start linux-live`, `start mac-live` | Remote on (Serve 8444), name set, link listener and Serve ready, no pairs |
| First join | `live-check.sh join` on macOS | failed after 5 s: TCP between the two computers was blocked by the tailnet policy in both directions; `tailscale ping` worked. The CLI reported `daemon_unavailable` (#677) |
| Tailnet rule | maintainer added TCP 47682 between the two computers | TCP to the link port succeeds both ways |
| Join | same invitation, 13:21:08 UTC | `Paired with linux-live.`; both show the other as connected |
| Receivers | `peers` on Linux | `claude-work-45@mac-live`, `remote: true`, `computer: mac-live`, activity `idle` |
| Linux to macOS | `send` to `claude-work-45@mac-live`, 13:30:44 UTC | `queued` with a `link_id`; `delivery` and `koinon link queue` show `received` 122 ms later |
| macOS to Linux | the Mac agent answers with `send`, told to set `reply_to` to the message ID (its tool call was not read), 13:30:50 UTC | in the Linux agent's inbox after its ordinary wake: sender `claude-work-45@mac-live`, `origin: link` |
| macOS queue | `live-check.sh status` on macOS | `linux-live: 0 waiting, 1 finished`; the reply to `codex-work-65@linux-live`, 19 bytes, `received there` at 13:30:50 UTC |
| Cleanup, Linux | `live-check.sh stop` after the agent ended | Remote off, Serve and link listener off; `tailscale serve status` shows only the original 443 mapping; no test process; the directory is gone; the installed daemon still runs v0.10.3, schema 37 |
| Cleanup, macOS | `live-check.sh stop` after `/exit` in the agent | Remote off, Serve and link listener off (Koinon's own Serve read); the directory and the script are gone. The script's last `tailscale serve status` failed: the macOS app puts no `tailscale` command on the PATH, so the script now ends with Koinon's Serve read |

Findings: `koinon link name` needs Remote on first (the script turns Remote on, then names the
computer); the tailnet policy must allow TCP on the link port between the computers, as
docs/REMOTE-DASHBOARD.md says; #677 tracks the misleading `daemon_unavailable` of a join that
cannot reach the other computer.
