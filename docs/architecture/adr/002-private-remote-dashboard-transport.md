---
adr: "002"
title: "Private Remote Dashboard Transport"
status: accepted
created: "2026-11-11"
updated: "2026-11-12"
authors:
  - "Andrew Deugarte (@andyde8)"
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

**Authors:** Andrew Deugarte (@andyde8)

## Executive Summary

An optional dashboard-only loopback listener in the existing daemon receives
private HTTPS traffic through Tailscale Serve. The listener reuses the dashboard
handlers and never mounts the agent API. Remote sign-in uses the existing
maintainer secret or a transient one-use ticket explicitly created by a signed-in
dashboard, as amended by PDR 010, Decision 4.
The local agent API and optional LAN HTTPS dashboard retain their existing roles
under ADR 001. Product behavior is in PDR 010. Decision 4 adds the link between the
maintainer's Koinon computers to Remote, with its own loopback TLS listener and a raw TCP
Serve mapping (#670).

This record accepts the authorized design. It does not assert real proxy,
cellular, or physical-device validation; the evidence and remaining steps are in
[the verification record](../../sprints/2026-11-11-remote-dashboard/verification.md).

## 1. Problem Context

The maintainer wants to control the existing dashboard from a phone away from
home. Forwarding the agent listener would mix browser acceptance with the
private agent API. A public relay or embedded VPN would add an independent
networking and credential lifecycle.

The [design specification](../../sprints/2026-11-11-remote-dashboard/spec.md) recommends an external
Tailscale installation and a dedicated browser surface. ADR 001, Decisions 2 and
4, governs the existing loopback API and LAN dashboard. PDR 009 governs the
maintainer credential; PDR 005 governs the authority of maintainer messages.

## 2. Decision: Separate browser transport and sessions

### Decision

**Proposed by:** Andrew Deugarte (@andyde8), 2026-11-11.

**Decided by:** Robert Capps, 2026-11-12 (#645), for this record as a whole: “yes we take it.”

**Context:** Implement the specification's recommended first version through
Tailscale Serve, or use an SSH tunnel, public relay, embedded VPN, agent-listener
forwarding, or native phone app. The goal is private dashboard control away from
home while reusing the existing daemon and login.

**Proposer’s words:** “I authorize implementation of the spec’s recommended first version:
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
- Remote login uses the maintainer secret or the scoped remote sign-in ticket
  described in Decision 3 below. A local one-time login link or API bearer does
  not authenticate it. Remote sessions have a distinct cookie name,
  `Secure`, `HttpOnly`, `SameSite=Strict`, and `Path=/dashboard/`, with no Domain.
  The session is bound to the remote surface and origin, so copying an ID into a
  local cookie does not authorize the local surface, or vice versa. Writes retain
  Origin and session-bound CSRF checks.
- Remote browser idle time is seven days, with the existing seven-day absolute
  maximum (PDR 010, Refinement 5). Local browser and maintainer-token idle time
  remains 24 hours under PDR 009. Restart,
  secret change, remote disable, and external-origin change end the relevant
  logins. An identity or Serve/Funnel conflict observed at status or the
  30-second reconciliation closes the surface and ends its remote logins.
  Remote actions use the existing dashboard audit and verdict attribution.
  Login reuses the existing global maintainer-secret backoff; the network access
  policy selects the intended maintainer devices.
- An injectable adapter inspects Tailscale and Serve before mutation. A private
  ownership intent is written before adding the mapping. Disable closes the listener
  first, then writes disabled intent before removing the mapping; when that write
  fails, the surface stays closed and the reconciler retries the write. Repeats and restart recovery finish the same
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

The dashboard setup entry point follows PDR 010, Decision 3. Its authenticated
browser routes call the same daemon lifecycle as the CLI; they do not proxy
browser requests to `/v1/`. The remote status and QR routes use the existing
session boundary, and enable/disable retain the existing Origin and CSRF checks.

An explicit setup check can use the installed Tailscale CLI's native feature
instructions query. The v1.102.3 [client](https://github.com/tailscale/tailscale/blob/v1.102.3/client/local/local.go)
and [LocalAPI handler](https://github.com/tailscale/tailscale/blob/v1.102.3/ipn/localapi/localapi.go)
use a read-permitted POST that requests control-plane instructions; it does not
set Serve configuration or accept account consent. The adapter accepts only the
native HTTPS Serve approval page on `login.tailscale.com`. That transient account
URL is separate from the stable dashboard URL and is not placed in a QR, private
ownership journal, audit target, or diagnostic log. Unsupported versions use an
actionable link to official setup instructions. This is a native provider
contract within the existing injectable-adapter boundary, not a new transport.

Remote lifecycle controls can close the listener serving their own request.
Those authenticated control routes, including the full Settings diagnostic render,
release the listener's request read lock before reconciliation or shutdown, so they can take its write lock without
waiting on themselves. Ordinary dashboard requests retain the existing drain
behavior. A remote disable can close its response connection; the UI keeps the
same unknown-result and no-replay semantics as other interrupted actions.

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

## 3. Decision: Transient remote sign-in tickets

PDR 010, Decision 4 records the 2026-11-11 choice of Andrew Deugarte (@andyde8), accepted by Robert Capps on 2026-11-12 (#645), between the original
URL-only browser QR with password entry and a one-time QR sign-in ticket with
ordinary-URL password fallback. Its context, answer, and product scope are the
source for this authentication amendment. This changes browser QR sign-in,
while retaining the private transport, CLI URL-only QR, existing password,
remote session boundary, and absent agent API. PDR 010, Refinement 5 separately
extends remote browser idle time to seven days while keeping the seven-day
absolute limit; local browser and maintainer-token idle time remains 24 hours.

A signed-in browser makes a session-bound CSRF-protected POST to create a random
remote ticket and a PNG QR. The ticket is hashed in daemon memory, expires after
two minutes, and is bound to the configured external origin. An atomic redemption
permits one remote session. Disable, secret change, or daemon restart removes
outstanding tickets. The two-minute ticket lifetime is separate from the
established session's seven-day idle and absolute limits. The raw ticket, QR
credential, and redemption body are not
persisted in the ownership journal, audit targets, or logs.

The implementation retains at most eight outstanding tickets and evicts the
oldest when issuing another, matching the existing local `maxLoginLinks` bounded
memory policy. Tickets also retain their issuing session's hash: issuer logout,
idle or absolute expiry, and session eviction revoke its outstanding tickets.
These are engineering bounds in `dashboard_pair.go` and `dashboard_auth.go`, not
additional requirements attributed to Andrew Deugarte (@andyde8).

Only a dashboard session on the host (`dashboard`) issues tickets; a `remote:` session
gets a refusal and the remote view shows no QR control. A ticket from a remote session
would start a new session with a new `created` time, so a chain of tickets would renew
remote access past the seven-day absolute maximum (PDR 009, Decision 2) and could fill
the 16-entry login table without the secret (review of PR #644).

The QR image comes from `github.com/skip2/go-qrcode` at
`v0.0.0-20200617195104-da1b6568686e`: MIT-style license (Copyright (c) 2014 Tom
Harwood), no dependencies of its own, encoding only, no network or file access. It has
no tagged release and no upstream change since 2020. Alternatives: a QR encoder written
in Koinon (more code to keep correct for one image), a browser-side library (a script
dependency on every dashboard page), or no QR (the phone types the URL and the
password). A new release of the library, or a defect in it, is a review condition for
this choice.

The QR carries `/dashboard/pair#ticket=...` at that origin. This differs from the
stable dashboard URL copied for bookmarks. A GET to the landing page does not
consume the ticket. The script clears the fragment with the browser history API
before sending one redemption POST with the expected HTTPS Origin. The ticket
itself supplies the one-time redemption proof; this unauthenticated redemption
has no prior browser session from which to derive a CSRF token. It creates the
existing `Secure`, `HttpOnly`, `SameSite=Strict`, dashboard-path remote cookie and
cannot create a local or API session. Invalid, expired, used, or wrong-origin
tickets fail without falling back to proxy identity headers or a local login link.

The browser never retries a redemption after an uncertain response. It offers
password sign-in or a newly created QR; the ordinary stable URL uses the password
form when signed out. No-JavaScript clients use that form. Fragment delivery keeps
the ticket out of the landing request URL; clearing it before redemption limits
its subsequent browser-address exposure. The displayed QR remains a short-lived
credential that must be kept private while outstanding.

The shared dashboard CSP permits `blob:` only in `img-src` so the script can
show the returned PNG through a temporary object URL. This image-source allowance
applies to all dashboard pages through the common headers; it adds no script
origin. The QR script accepts only PNG responses, rejects non-positive or greater
than 300-second lifetime headers, and clears expired object URLs. Ticket creation
and redemption use ten-second request deadlines. Successful redemption navigation
is pinned to the read-only, same-origin `/dashboard/sessions` route, without a
query or fragment. These frontend guardrails bound image lifetime, waiting, and
navigation; they are implementation choices supporting the authorized flow, not
human-mandated limits or a change to the server's two-minute ticket lifetime.

### Alternatives Considered

| Alternative | Tradeoff |
| --- | --- |
| Password after every browser QR scan | Keeps the original design, but does not implement Andrew's explicit one-time QR choice. |
| Ticket in a query string | Sends the credential in the landing request URL; a fragment permits a non-consuming GET and explicit redemption body. |
| Permanent pairing or saved credential | Adds a durable credential lifecycle that the one-time sign-in request does not need. |

### Rationale

1. Existing browser authorization can issue a narrowly scoped remote sign-in ticket.
2. Expiry, atomic one-use redemption, origin binding, and memory-only hashes keep
   it separate from a permanent login URL.
3. The existing remote cookie and password fallback preserve the established session boundary.

## 4. Decision: The link between Koinon computers is part of Remote

### Decision

Remote also carries the link between the maintainer's own Koinon computers (#670). Remote
enable adds a raw TCP Serve mapping (`tailscale serve --bg --tcp=PORT tcp://127.0.0.1:N`, never
`--tls-terminated-tcp` and never the HTTPS route) from a separate, changeable link port
(47672 by default) to a second loopback listener in the daemon, journaled in
`remote-dashboard.json` with the dashboard mapping's ownership rules. Remote disable closes the
link listener and its connections and cancels open invitations, then removes the mapping;
pairs stay. A disable whose record write fails keeps the link closed.

The link listener speaks TLS 1.3 only, end to end between the two daemons, through the raw TCP
mapping; Tailscale's WireGuard is around it. Each daemon has its own Ed25519 key in the private
`link-key.pem`, with a self-signed certificate renewed with the same key, and pins the other
daemon's key (SHA-256 of its SubjectPublicKeyInfo) at pairing. `VerifyConnection` checks the pin
on every connection, with session tickets off; a key that is not paired is refused in the
handshake, except while an invitation is open, and then only for the pairing operation. Pairing
is one invitation: the joining daemon checks the inviting key against the invitation's pin
before it sends the invitation's single-use secret. The listener serves no agent API and no
dashboard route. Tailnet membership, the dashboard login and the agent API bearer give no link
access, and the maintainer secret never crosses the link. A daemon dials a pair's saved
Tailscale addresses, refreshed from `tailscale status --json` by an exact tailnet-name match,
and never resolves a name through the operating system.

Invite, join, unpair and rename need the maintainer: the dashboard login, or a maintainer token
on the agent API (PDR 009). On a paired connection the link also carries `directory` and
`name-update` (#670 chunk 02): each computer reads the other's receivers, without session IDs,
paths or credentials, into memory as a display hint; the link exposes no store or control
operation. Implementation: `internal/core/link.go`, `internal/core/link_routes.go`,
`internal/link`, and `remote.Manager.EnsureTCP` and `RemoveTCP` in `internal/remote/link.go`.

**Provenance (LINK-670-PAIR):**

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 5, approved, after feedback on revisions 1, 3 and 4
  ([decision record](../../sprints/2026-11-12-system-link/decision.md)).
- **Context:** how two of the maintainer's daemons find, trust and reach each other. Revision 1
  proposed a separate link listener with pinned keys. Feedback: "agreed, propose a design. How
  can we EASILY introduce them to each other?" Revision 3 proposed one invitation. Feedback:
  "looks good, how do we manage secure connections? Will this be LAN only, LAN plus Tailscale,
  something else?" Revision 4 proposed any reachable network with pinned keys. Feedback: "I
  think this should be an offshoot of the remote function, i.e., you must enable remote to
  enable peer Koinon nodes. How will node to node traffic be protected? TLS?"
- **Decision:** links are part of Remote; pairing is one invitation; invite, join and unpair
  need the maintainer; a raw TCP Serve mapping to a loopback link listener; TLS 1.3 between the
  daemons with pinned keys; disable closes the link and keeps the pairs; the agent API stays on
  loopback.
- **Reason and scope:** Remote already gives each computer a tailnet name and a managed Serve
  setup, so pairing needs no address. The raw TCP mapping keeps the pinned daemon keys; the
  HTTPS mapping ends TLS in tailscaled. This amends the scope of Decision 2, which approved
  Remote for browser access only.
- **Review when:** computers without Tailscale must link, or Tailscale Serve changes its TCP
  forwarding.

### Alternatives Considered

A link listener on any LAN, tailnet or VPN address (revision 4); LAN only; tailnet device
identity as the trust; links on the Remote HTTPS path with a credential per pair, which loses
the pinned daemon keys; a direct tailnet listener outside Serve; a 6-digit code compared on two
screens; LAN discovery by mDNS.

### Enforcement and evidence

`internal/core/link_test.go` and `internal/core/link_routes_test.go` run two daemons with real
TLS and a synthetic Tailscale adapter: pairing through one invitation; reused, expired and
cancelled invitations; a wrong pin before any secret; Remote disable with an open invitation,
during a connection and with a failed record write; restart while off; unpair on both sides; no
TLS session resumption after unpair; a refused unpaired key shown in status; the maintainer token and dashboard checks of each change, and audit records without
the invitation. The raw TCP Serve probe on a tailnet host is in
[the verification record](../../sprints/2026-11-12-system-link/verification.md).


## 5. Decision: Remote control v2 durable authentication and atomic receipts

**Source:** Andrew Deugarte's direct implementation request, 2026-11-12.
[PDR 010 §9](../../product/pdr/010-private-remote-dashboard-access.md#9-refinement-remote-control-v2-fixed-sign-in-and-recovery)
records the retention question, alternatives and his quoted fixed-48-hour answer.
The architecture below implements that scope; it does not introduce a separate
lifetime decision. See also `docs/REMOTE-CONTROL-V2-SPEC.md`.

Use the existing private SQLite database and opaque bearer cookie; persist only
its hash, a noncredential session ID, origin, credential-generation binding,
fixed deadline, observations, label and revocation state. The generation is a
domain-separated digest of the actual maintainer secret record's random salt;
it detects replacement while the daemon is stopped without saving the password
or a reusable credential. Issuance/revocation commit before success. Storage
failure denies access immediately and reports incomplete durable revocation.

The per-start CSRF key stays ephemeral. Surviving browsers fetch fresh tokens
without replacing unsent text or replaying actions. Listener shutdown is separated
from credential revocation; Tailscale mapping ownership and conflict rules remain.

Engineering choice: versioned, namespaced JSON records live in the existing
non-STRICT `settings` table. SQLite retains JSON text under its INTEGER affinity;
known timing readers select timing keys explicitly. This preserves schema37 and
its exact catalog for previous binaries. Older binaries ignore these v2 rows and
still require sign-in; their in-memory sessions cannot use v2 credentials.
A typed-table migration can replace this representation when rollback compatibility
can be changed deliberately. Never use a WAL-attached sidecar for cross-database
receipt/mutation atomicity.

Receipt records share the mutation's database transaction and host operation-ID
namespace. An engineering bound of 10,000 unexpired receipts rejects additional
receipt-backed writes with capacity rather than evicting a promised seven-day
receipt; retained-result lookup still works when ordinary write admission blocks. Seven-day deduplication checks type/target/origin and payload digest;
reauthentication changes attribution, never the namespace. Message delivery and
acknowledgement remain separate effects. Decision revision conflicts are retained
as original outcomes. Attention cursors use a separate browser namespace and never
change inbox acknowledgement. Private browser drafts are not daemon storage.

Review when schema compatibility changes or production evidence calls for a new
storage bound. Automated and actual phone/platform acceptance stay distinct.

## Conclusion

Private remote browser access has a dedicated listener and credential surface,
while retaining the daemon's existing actions and authority. Its setup owns one
Serve mapping and records recoverable lifecycle transitions.

## Document History

| Date | Change |
| --- | --- |
| 2026-11-11 | Record Andrew's authorized first version and its transport boundary. |
| 2026-11-11 | Clarify shutdown ownership, reconciliation, native naming scope, and interrupted cleanup after review. |
| 2026-11-11 | Clarify the shared lifecycle, native setup query, and self-closing browser controls. |
| 2026-11-11 | Amend remote sign-in with transient origin-bound QR tickets authorized in PDR 010, Decision 4. |
| 2026-11-11 | Clarify remote-only seven-day idle time and its separation from ticket expiry. |
| 2026-11-12 | Accepted by Robert Capps (#645). Name the proposer as Andrew Deugarte (@andyde8). Only a host dashboard session issues tickets. Record the QR library and its alternatives (review of PR #644). |
| 2026-11-12 | Decision 4: the link between Koinon computers is part of Remote (LINK-670-PAIR, Robert Capps, #670). |
| 2026-11-13 | Decision 4: the link carries the receiver directory and name updates (#670 chunk 02). |
| 2026-11-12 | Record Andrew's v2 implementation authorization, fixed 48-hour policy, durable sessions and explicit recovery scope. |
