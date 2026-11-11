---
adr: "002"
title: "Private Remote Dashboard Transport"
status: accepted
created: "2026-11-11"
updated: "2026-11-11"
authors:
  - "Andrew"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "001"
related_pdrs:
  - "010"
tags:
  - architecture
  - security
  - auth
  - integration
---

# ADR 002: Private Remote Dashboard Transport

**Date:** 2026-11-11

**Status:** Accepted

**Authors:** Andrew

## Executive Summary

An optional dashboard-only loopback listener in the existing daemon receives
private HTTPS traffic through Tailscale Serve. The listener reuses the dashboard
handlers, requires the existing maintainer secret, and never mounts the agent API.
The local agent API and optional LAN HTTPS dashboard retain their existing roles
under ADR 001. Product behavior is in PDR 010.

This record accepts the authorized design. It does not assert real proxy,
cellular, or physical-device validation; the evidence and remaining steps are in
[the verification record](../../REMOTE-DASHBOARD-VERIFICATION.md).

## 1. Problem Context

The maintainer wants to control the existing dashboard from a phone away from
home. Forwarding the agent listener would mix browser acceptance with the
private agent API. A public relay or embedded VPN would add an independent
networking and credential lifecycle.

The [design specification](../../REMOTE-DASHBOARD-SPEC.md) recommends an external
Tailscale installation and a dedicated browser surface. ADR 001, Decisions 2 and
4, governs the existing loopback API and LAN dashboard. PDR 009 governs the
maintainer credential; PDR 005 governs the authority of maintainer messages.

## 2. Decision: Separate browser transport and sessions

### Decision

**Decided by:** Andrew.

**Date:** 2026-11-11.

**Context:** Implement the specification's recommended first version through
Tailscale Serve, or use an SSH tunnel, public relay, embedded VPN, agent-listener
forwarding, or native phone app. The goal is private dashboard control away from
home while reusing the existing daemon and login.

**Answer:** “I authorize implementation of the spec’s recommended first version:
private remote access to the existing dashboard through Tailscale Serve, with the
existing maintainer login.”

**Reason and scope:** The external service supplies private HTTPS without adding
a Koinon cloud service or duplicating dashboard actions. This covers browser
access, authentication isolation, setup ownership, and restart restoration. It
does not add terminal streaming, desktop control, or a native phone application.

**Review when:** Browser access without a phone VPN becomes a requirement, or
Tailscale's verified forwarding contract changes.

The implementation follows these boundaries from the authorized specification:

- Serve forwards to a dedicated literal `127.0.0.1` HTTP listener. Its selected
  port and exact external HTTPS origin are saved in the private state directory.
  `/` redirects to `/dashboard/`; the dashboard and static assets share the
  existing handlers. `/v1/` is absent.
- The configured external Host is checked directly. Expected write Origin comes
  from saved setup configuration, never `Forwarded`, `X-Forwarded-*`, or identity
  headers. The installed-version Tailscale proxy source preserves the external
  Host for HTTP backends; real forwarding verification remains a separate
  acceptance step. HTTPS names and certificate prerequisites follow the official
  [Serve contract](https://tailscale.com/docs/features/tailscale-serve).
  The adapter accepts the native `Self.DNSName` under `.ts.net`, matching the
  official [MagicDNS FQDN](https://tailscale.com/docs/features/magicdns#fully-qualified-domain-names-vs-machine-names)
  and [HTTPS certificate](https://tailscale.com/docs/how-to/set-up-https-certificates)
  naming contracts. This scopes the current external Serve adapter, rather than
  deciding future custom-domain support.
- Remote login uses the maintainer secret. A local one-time login link or API
  bearer does not authenticate it. Remote sessions have a distinct cookie name,
  `Secure`, `HttpOnly`, `SameSite=Strict`, and `Path=/dashboard/`, with no Domain.
  The session is bound to the remote surface and origin, so copying an ID into a
  local cookie does not authorize the local surface, or vice versa. Writes retain
  Origin and session-bound CSRF checks.
- PDR 009's 24-hour idle and seven-day absolute session limits remain. Restart,
  secret change, remote disable, and external-origin change end the relevant
  logins. An identity or Serve/Funnel conflict observed at status or the
  30-second reconciliation closes the surface and ends its remote logins.
  Remote actions use the existing dashboard audit and verdict attribution.
  Login reuses the existing global maintainer-secret backoff; the network access
  policy selects the intended maintainer devices.
- An injectable adapter inspects Tailscale and Serve before mutation. A private
  ownership intent is written before adding the mapping, and disabled intent
  before stopping or removing it. Repeats and restart recovery finish the same
  transition. Unrelated configuration is preserved; conflicting root, foreground,
  TCP, or Funnel configuration is reported. Removal compares the exact owned
  target and uses explicit `--set-path=/` as required by the Tailscale CLI's
  targeted-removal behavior. No Serve reset or Tailscale logout is part of this
  feature. Orderly shutdown removes the exact owned mapping before releasing
  the backend port, preserving enabled ownership intent for the next startup.
  A saved-port conflict triggers owned-mapping removal during restoration, while
  the journal remains for retry. Uninstall cleans up the recorded mapping after
  stopping the daemon. If Tailscale is missing, uninstall retains the disabled
  ownership record and binary; restoring Tailscale and repeating uninstall
  finishes cleanup without editing the journal.

The missing-client recovery follows the authorized mapping-ownership requirement:
absence of the `tailscale` client does not prove that `tailscaled` or its persistent
Serve configuration is absent. Clearing ownership without inspecting it could
leave forwarding to a later process on the released port. Cleanup therefore keeps
its disabled journal and reports the dependency step; it does not reset Serve.
The client and daemon are separate native tools, and `--bg` configuration persists
under the [Serve contract](https://tailscale.com/docs/reference/tailscale-cli/serve).

After a successful explicit disable, a new enable can choose an available backend
port if the released port is occupied. Restart restoration retains the configured
port and reports a conflict. For an old-name Serve key, the targeted CLI needs the
old machine identity temporarily restored to remove it safely; the exact
[recovery commands](../../REMOTE-DASHBOARD.md#name-change-recovery) use Tailscale's
[documented machine-name setting](https://tailscale.com/docs/concepts/machine-names).
Koinon never performs that account or machine-name change automatically. Live
name-retention behavior remains a platform acceptance check.

### Alternatives Considered

| Alternative | Tradeoff |
| --- | --- |
| SSH tunnel from the phone | Fits the local boundary, but adds everyday phone tunnel management. |
| Public relay and pairing | Removes a phone VPN dependency, but adds relay operation and a new credential lifecycle. |
| Embedded Tailscale | Integrates networking, but adds networking dependencies and service lifecycle inside Koinon. |
| Forward the agent listener | Avoids another listener, but mixes remote browser access with private API routing. |
| Native phone app | Adds packaging and release work and duplicates the current web interface. |

These alternatives remain possible future work; the decision chooses the first
version's transport, not a permanent prohibition.

### Rationale

1. One set of handlers keeps actions and state continuous across browser surfaces.
2. A distinct listener and session binding make the API and cookie boundary testable.
3. Targeted ownership and a private journal preserve other Serve uses across failure,
   restart, disable, upgrade, and uninstall.

### Enforcement and evidence

`internal/core/dashboard_remote.go` defines the browser-only boundary;
`internal/core/dashboard_auth.go` binds sessions; `internal/core/remote.go`
serializes lifecycle and persistence; `internal/remote` adapts the installed
Tailscale CLI. Synthetic tests use temporary state and an injected adapter rather
than a person's account. Real macOS/Linux Serve and phone tests are separately
recorded in the verification record. Static builds do not prove those paths.

### Trade-offs Accepted

Both host and phone need Tailscale, suitable network policy, and an online awake
host. A renamed Tailscale identity can change the URL. Tailscale setup can need
account or operating-system interaction outside the Koinon command.

An abrupt kill, or failed mapping cleanup during shutdown, can leave Serve
pointing to a released loopback port until reconciliation. Within ADR 001's
same-user local trust boundary, the feature does not claim protection from a
malicious process running as the same user or eliminate that ownership window.
Cleanup failures remain reported and journaled; the next restoration checks the
saved port and mapping before restoring service.

## Conclusion

Private remote browser access has a dedicated listener and credential surface,
while retaining the daemon's existing actions and authority. Its setup owns one
Serve mapping and records recoverable lifecycle transitions.

## Document History

| Date | Change |
| --- | --- |
| 2026-11-11 | Record Andrew's authorized first version and its transport boundary. |
| 2026-11-11 | Clarify shutdown ownership, reconciliation, native naming scope, and interrupted cleanup after review. |
