---
pdr: "010"
title: "Private Remote Dashboard Access"
status: modified
created: "2026-11-11"
updated: "2026-11-11"
authors:
  - "Andrew"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
  - "002"
related_pdrs:
  - "005"
  - "009"
tags:
  - ux
  - integration
  - mobile
  - error-handling
---

# PDR 010: Private Remote Dashboard Access

**Date:** 2026-11-11

**Status:** Modified

**Authors:** Andrew

## Executive Summary

The maintainer can configure private access to the existing dashboard through
Tailscale Serve and sign in with the existing maintainer secret. Setup provides a
stable HTTPS URL and a QR containing only that URL. Phone pages retain all
existing actions and distinguish host readiness, displayed-data freshness, and
unverified phone reachability.

The mechanism is ADR 002. This record accepts the authorized behavior; it does
not claim that the physical-device and cellular acceptance tests have passed.

## 1. Problem Context

The [specification](../../REMOTE-DASHBOARD-SPEC.md) captures a request for dashboard
control from a phone away from home. The existing local dashboard already holds
the needed actions and state, but everyday phone access needs persistent setup,
a usable layout, and honest handling of interrupted reads and writes.

PDR 009 supplies the credential and login lifetime. PDR 005 gives dashboard
messages their maintainer authority. Remote access does not identify a particular
phone or person merely because the network accepts its connection.

## 2. Decision: Persistent private access with honest recovery

### Decision

**Decided by:** Andrew.

**Date:** 2026-11-11.

**Context:** Implement the recommended private dashboard first version, compared
with SSH tunnel management, a public relay, embedded VPN, or native phone app.
The choice must support access away from home with the existing dashboard login.

**Answer:** “I authorize implementation of the spec’s recommended first version:
private remote access to the existing dashboard through Tailscale Serve, with the
existing maintainer login.”

**Reason and scope:** Reusing the dashboard and an external private HTTPS service
provides the requested control without duplicating actions. The scope includes
setup, restoration, mobile layouts, and reconnection behavior. Terminal and
desktop control, native apps, push alerts, and multi-host views are outside this
first deliverable; they remain future options.

**Review when:** Phone access without Tailscale or remote wake becomes a requirement.

The answer above authorizes the recommended transport and existing login. The
behavioral details below come from the same explicitly authorized
[specification](../../REMOTE-DASHBOARD-SPEC.md), including its setup, mobile,
authentication, and reconnection requirements; the transport quote alone is not
the source of every detail.

The authorized specification defines the following user-visible behavior:

| Area | Behavior |
| --- | --- |
| Enable | `koinon remote enable` checks the local daemon, maintainer secret, connected Tailscale identity, HTTPS prerequisites, and existing Serve use. It configures or gives the next setup step. Repeating it is safe. |
| Open | The command prints a stable HTTPS URL and URL-only QR. The phone joins the same private network and signs in with the maintainer secret. A bookmark or home-screen shortcut provides everyday access. |
| Status | `koinon remote status` reports the URL, listener readiness, observed dependency state, and setup problem. A retained URL while disabled is labeled saved. Phone access is unverified; host configuration alone is not cellular evidence. |
| Disable | `koinon remote disable` ends remote sessions, stops the remote listener, and removes only the owned mapping. Local dashboard access and the host's Tailscale login remain available. |
| Restart and upgrade | Orderly shutdown removes the owned mapping before releasing its port and retains enabled intent for restoration of the same port and URL. Unavailable dependencies and port conflicts are reported. A daemon restart ends logins, as in PDR 009. |
| URL change | A changed Tailscale hostname or machine identity requires disable then enable; the current URL is read from status, and old remote sessions end. |
| Mobile | Existing agents, messages, rooms, decisions, launch, work, memory, audit, settings, and health remain reachable. Primary workflows fit 360 CSS pixels; touch controls have at least 44 CSS pixels of area and form text at least 16 CSS pixels. Desktop terminal typography stays outside this change. |
| Reads | Visible pages read every five seconds and on foreground/reconnect. Host, connection state, and last successful refresh are shown; displayed data is stale after 15 seconds without refresh. Unknown observations stay unknown. |
| Drafts | Open forms and unsent input hold replacement so drafts stay in page memory. The feature does not promise recovery after reload or tab discard and does not cache private pages offline. |
| Writes | Submission prevents repeated taps while running. Lost responses say “Result unknown; check the latest state before trying again.” Reconnect and login read current state and never replay a prior write automatically. Existing revision and idempotency rules still apply. |
| Login recovery | An expired login opens sign-in with a safe relative read-only dashboard return screen; it does not resubmit the previous action. |

Network policy must permit the intended maintainer devices. The host must remain
awake, online, and running Koinon and Tailscale. Account, certificate, and platform
steps can require interaction outside Koinon, and diagnostics identify that step.
Login shares PDR 009's existing global maintainer-secret backoff. An identity or
Serve/Funnel conflict observed at status or the 30-second reconciliation closes
the remote surface and ends its logins. A saved-port conflict removes the owned
forwarding during restoration and keeps the ownership record for retry.

If Tailscale is missing during uninstall, the binary and disabled ownership
journal stay so the command can finish after Tailscale is installed again. The
next step is to restore that dependency and repeat uninstall, rather than edit
the journal. ADR 002 records the remaining loopback ownership window after an
abrupt process kill or unsuccessful shutdown cleanup; this first version does
not add protection against malicious same-user processes.

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

| Alternative | Why not the first version |
| --- | --- |
| Local-only access | Does not satisfy access away from home. |
| Phone SSH tunnel | Adds everyday connection-management work. |
| Public relay or native application | Adds services, pairing, packaging, or duplicate UI before reusing the existing dashboard. |
| Automatic write retry | A lost response does not prove the action failed; replay could repeat a state-changing action. |
| Persist browser drafts offline | Adds a private-data retention contract beyond this first version's page-memory scope. |

### Rationale

1. Persistent setup makes everyday access a bookmark and login.
2. Displayed-data freshness and connection observations are separate facts.
3. Preserved drafts and explicit uncertain outcomes avoid losing input or silently
   repeating actions during mobile network transitions.

### User Impact

The maintainer can use the same host-side actions from a phone, with the same
audit, delivery, verdict, and revision behavior. Setup remains optional; ordinary
local use needs no Tailscale installation. The private network and an awake host
are dependencies, and configuration is not a claim of cellular reachability.

### Success Criteria

The [verification record](../../REMOTE-DASHBOARD-VERIFICATION.md) tracks the
specification's matrix: actual Serve behavior on macOS and Linux, cellular reads
and writes with host verification, current iOS Safari and Android Chrome,
360/390-pixel layouts, keyboard/zoom, login lifecycle, preserved drafts, uncertain
write response, lifecycle restoration, and configuration ownership. Synthetic
tests and static builds are distinct from physical-device evidence.

## 3. Decision: Remote setup in the existing dashboard

### Decision

**Decided by:** Andrew.

**Date:** 2026-11-11.

**Context:** Add a customer-facing setup entry point, or keep remote setup in the
terminal. Andrew requested a **Go remote** dashboard UI and asked to review a
standalone HTML preview before implementation. He rejected a visual redesign and
approved the revised preview that used Koinon's actual navigation, tables, and
Settings layout.

**Answer:** “ok this is good for now implement this UI on this branch .”

**Reason and scope:** The approved preview makes remote setup discoverable inside
the dashboard while retaining Koinon's interface. It adds a header entry point
and a **Remote access** section in Settings on the existing feature branch. It
does not authorize a separate dashboard redesign or a claim that physical phone
validation has passed.

The flow checks host prerequisites, gives a Tailscale account approval link when
available, waits for the person to return and check again, and offers an explicit
**Enable remote access** action. It then shows the stable URL, URL-only QR, and
existing maintainer sign-in instructions. A **Turn off remote access** disclosure
explains the loss of phone access before its explicit action. The CLI and browser
use the same lifecycle and ownership rules.

The section distinguishes the computer listener, Tailscale connection, and Serve
mapping. Phone reachability stays unverified. Normal status reads and foreground
or reconnect reads do not repeat writes or approve account consent. A lost disable
response can mean that access was already stopped; the person checks status on
the host before deciding to retry.

### Alternatives Considered

| Alternative | Why not this change |
| --- | --- |
| CLI-only setup | Leaves the requested customer-facing dashboard entry point absent. |
| A redesigned dashboard | Andrew rejected the first preview because it did not look like Koinon; the revised preview used the existing interface. |
| Enable when an approval tab returns | Returning does not prove account setup is complete, and connection recovery must not replay writes. |

### Rationale

1. Setup lives alongside the other dashboard settings and uses the existing login.
2. The account step stays with Tailscale, whose prerequisites Koinon can observe.
3. Explicit enable and disable preserve the existing uncertain-response behavior.

## Conclusion

Remote access reuses the dashboard's actions and login while making setup
persistent and recovery explicit. The phone sees measured readiness and data age;
unverified connectivity and action outcomes stay labeled as such.

## Document History

| Date | Change |
| --- | --- |
| 2026-11-11 | Record Andrew's authorized private dashboard scope and mobile behavior. |
| 2026-11-11 | Clarify the behavior's specification source, reconciliation, and lifecycle recovery after review. |
| 2026-11-11 | Record Andrew's approval of setup controls in the existing dashboard interface. |
