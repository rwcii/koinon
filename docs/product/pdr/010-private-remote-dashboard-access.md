---
pdr: "010"
title: "Private Remote Dashboard Access"
status: accepted
created: "2026-11-11"
updated: "2026-11-13"
authors:
  - "Andrew Deugarte (@andyde8)"
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

**Status:** Accepted

**Authors:** Andrew Deugarte (@andyde8)

## Executive Summary

The maintainer can configure private access to the existing dashboard through
Tailscale Serve and sign in with the existing maintainer secret. Setup provides a
stable HTTPS URL. The browser can explicitly create a one-time sign-in QR;
the CLI QR remains a URL-only shortcut. Ordinary URL access while signed out
uses the existing maintainer secret, labeled Koinon password. Phone pages retain all
existing actions and distinguish host readiness, displayed-data freshness, and
unverified phone reachability.

The mechanism is ADR 002. This record accepts the authorized behavior; it does
not claim that the physical-device and cellular acceptance tests have passed.

**Accepted by:** Robert Capps, 2026-11-12 (#645): “yes we take it.” Context: the peer
review of PR #644 asked whether Koinon takes this second way to reach the dashboard from
off the host, beside the network dashboard over HTTPS (PDR 009, Decision 2; #624).
Scope: the behavior in this record, as fixed in PR #644, for release v0.10.2. Andrew
Deugarte (@andyde8) proposed it; his words below are the proposal, and this acceptance
is the decision.

## 1. Problem Context

The [specification](../../sprints/2026-11-11-remote-dashboard/spec.md) captures a request for dashboard
control from a phone away from home. The existing local dashboard already holds
the needed actions and state, but everyday phone access needs persistent setup,
a usable layout, and honest handling of interrupted reads and writes.

PDR 009 supplies the credential and login lifetime. PDR 005 gives dashboard
messages their maintainer authority. Remote access does not identify a particular
phone or person merely because the network accepts its connection.

## 2. Decision: Persistent private access with honest recovery

### Decision

**Proposed by:** Andrew Deugarte (@andyde8), 2026-11-11.

**Decided by:** Robert Capps, 2026-11-12 (#645), for this record as a whole: “yes we take it.”

**Context:** Implement the recommended private dashboard first version, compared
with SSH tunnel management, a public relay, embedded VPN, or native phone app.
The choice must support access away from home with the existing dashboard login.

**Proposer’s words:** “I authorize implementation of the spec’s recommended first version:
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
[specification](../../sprints/2026-11-11-remote-dashboard/spec.md), including its setup, mobile,
authentication, and reconnection requirements; the transport quote alone is not
the source of every detail.

The authorized specification defines the following first-version behavior.
Decision 4 below amends the browser QR sign-in flow; the CLI QR remains URL-only:

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

For remote browser sessions, §9 replaces the restart, lifetime and drafts rows above: a
remote sign-in lasts a fixed 48 hours and survives a routine restart or upgrade, and
draft recovery after a reload is an explicit opt-in.

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

The [verification record](../../sprints/2026-11-11-remote-dashboard/verification.md) tracks the
specification's matrix: actual Serve behavior on macOS and Linux, cellular reads
and writes with host verification, current iOS Safari and Android Chrome,
360/390-pixel layouts, keyboard/zoom, login lifecycle, preserved drafts, uncertain
write response, lifecycle restoration, and configuration ownership. Synthetic
tests and static builds are distinct from physical-device evidence.

## 3. Decision: Remote setup in the existing dashboard

### Decision

**Proposed by:** Andrew Deugarte (@andyde8), 2026-11-11.

**Decided by:** Robert Capps, 2026-11-12 (#645), for this record as a whole: “yes we take it.”

**Context:** Add a customer-facing setup entry point, or keep remote setup in the
terminal. Andrew requested a **Go remote** dashboard UI and asked to review a
standalone HTML preview before implementation. He rejected a visual redesign and
approved the revised preview that used Koinon's actual navigation, tables, and
Settings layout.

**Proposer’s words:** “ok this is good for now implement this UI on this branch.”

**Reason and scope:** The approved preview makes remote setup discoverable inside
the dashboard while retaining Koinon's interface. It adds a header entry point
and a **Remote access** section in Settings on the existing feature branch. It
does not authorize a separate dashboard redesign or a claim that physical phone
validation has passed.

The flow checks host prerequisites, gives a Tailscale account approval link when
available, waits for the person to return and check again, and offers an explicit
**Enable remote access** action. The original approved version then showed the
stable URL, URL-only QR, and existing maintainer sign-in instructions; Decision 4
below amends that browser QR and simplifies the presentation. A **Turn off remote access** disclosure
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

## 4. Decision: One-time QR sign-in and a concise remote view

### Decision

**Proposed by:** Andrew Deugarte (@andyde8), 2026-11-11.

**Decided by:** Robert Capps, 2026-11-12 (#645), for this record as a whole: “yes we take it.”

**Context:** The first version's QR opened the ordinary dashboard URL and asked
for the maintainer secret. Andrew reported that password sign-in worked on his
phone. The proposed alternative was a short-lived, one-time QR sign-in ticket,
with the saved dashboard URL kept separate and requiring the password when
signed out. Andrew chose that alternative instead of the original URL-only QR.
He also asked to remove **Go remote** from a dashboard that is already remote.

**Proposer’s words:** “yeah let’s do that so the qr code is a one time sign in ticket. and then if you reach it through the URL I guess it would be password needed.”

This authorization followed the phone report in the same message; the quotation
above isolates the authentication choice from the reported test result.

For the header, Andrew said: “also it still says go remote on the mobile version. that shouldn't be there lol I’m already remote! commit changes.”

**Reason and scope:** Scanning a QR created by an already signed-in dashboard
should complete one remote sign-in without making the person type the same
password on the phone. This explicitly changes the browser QR from the original
URL-only design. The separately copied or bookmarked dashboard URL still needs
the Koinon password when signed out. The CLI QR remains URL-only. Hiding the
header control depends on the actual remote surface, so a local narrow window
still has it and a remote desktop window does not.

For setup clarity, Andrew requested: “there is a lot of unnecessary info presented throughout the setup can u make it easier for users who maybe don't understand quite everything break it down to be as simple as possible and concise UI wise.”

**Implementation choices in response:** Show one next step for the current
state, keep technical status in details, and collapse the other Settings
sections. Label the existing maintainer secret **Koinon password**. These are
presentation choices responding to the request, not verbatim decisions from
Andrew or an additional credential.

A signed-in dashboard on the host creates the QR through an explicit CSRF-protected
action. A remote session cannot create one: a chain of codes would renew a remote
session past the seven-day absolute maximum (PDR 009, Decision 2) and fill the login
table without the secret (review of PR #644).
The ticket permits one sign-in to the configured remote origin. The
implementation uses a two-minute expiry for this temporary credential. Its use creates the same remote session with the existing cookie
attributes and lifetime; it does not grant the agent API or create a permanent
passwordless link. Tickets stay in daemon memory as hashes and are invalidated
on disable, secret change, or restart. The stable URL never contains the ticket.

Landing-page GETs do not consume tickets. The phone removes the ticket fragment
from its address before one Origin-checked redemption POST. An expired or used
ticket, lost response, or unsupported browser leads to a newly created QR or the
ordinary password form, without automatic redemption retry. ADR 002 records the
mechanism and browser boundary.

Andrew's phone report establishes reported password sign-in success. It does not
specify cellular use, device/browser versions, or the remaining acceptance matrix.
The new ticket flow needs its own verification.

### Alternatives Considered

| Alternative | Why not this change |
| --- | --- |
| Keep the browser QR URL-only | Andrew explicitly chose one-time QR sign-in after the phone password test. |
| Make the saved URL a permanent sign-in link | Andrew retained password-needed access through the ordinary URL. |
| Hide Go remote only at phone widths | The reason is that the current connection is already remote; viewport width does not establish that. |

### Rationale

1. A signed-in dashboard supplies the authorization for one convenient phone sign-in.
2. Short-lived, single-use tickets do not turn the stable URL into a credential.
3. The remote view describes the connection the person is already using.

## 5. Refinement: Longer remote browser sessions

**Requested by:** Andrew Deugarte (@andyde8), 2026-11-11. **Decided by:** Robert Capps, 2026-11-12 (#645), for this record as a whole: “yes we take it.” Superseded for remote browsers by §9.

**Context:** After phone sign-in, Andrew reported being signed out after putting
his phone away for five minutes and asked for a longer session and a
recommendation. The implementation
previously allowed 24 hours idle and seven days absolute, so a five-minute expiry
was not the configured behavior. The cause of the report is not established.

**Implementation choice:** In response to the request, the recommended refinement
extends only remote browser idle time to seven days, retaining the existing
seven-day absolute maximum. Local browser and maintainer API-token sessions keep
24 hours idle. This records the recommendation implemented within the requested
longer-session scope; it does not attribute an explicit choice of seven days to
Andrew.

**Reason and scope:** The person can return to a phone dashboard without an idle
expiry during its week-long maximum lifetime. Logout, disable, password change,
restart, and browser-cookie loss can still require sign-in sooner. The two-minute
one-time QR expiry only limits the ticket before redemption; it does not shorten
an established session. The extension is not evidence that the reported
five-minute sign-out is fixed. Andrew later reported the password screen in a
direct Safari/Chrome browser view. Browser versions and a reproduction remain
needed to identify the cause.

## 6. Decision: The terminal runs the Linux operator step after confirmation

**Decided by:** Robert Capps, 2026-11-12, board card TAILSCALE-AUTO-656 (#656).

**Context:** On Linux, tailscaled lets only root or the Tailscale operator change Serve,
and an ordinary account cannot make itself operator: the first grant needs sudo. Since
#652, `koinon remote enable` refuses with `tailscale_operator_required` and names
`sudo tailscale set --operator=NAME`, which the person runs. When v0.10.3 was released
with that manual step, Robert asked to "release it as is, make sure it's clear how this
gets run at enable, and file an issue to review automating this" (#656). Operator status
is control of all of this computer's Tailscale settings, not only Serve; Tailscale
v1.104.5 has no Serve-only grant. Since #653, status also names the tailnet policy entry
for TCP 443, which the person adds. The options were: keep both steps manual; run the
operator step at the terminal after confirmation and keep the policy step manual; or
also write the tailnet policy with a stored credential.

**Decision:** On Linux, when `koinon remote enable` gets `tailscale_operator_required`
and runs at a terminal, it asks once: "Grant NAME control of this computer's Tailscale
settings, including more than Serve, by running sudo tailscale set --operator=NAME?
[y/N]". NAME is the account that runs Koinon, read from the passwd entry of its UID, as
for the manual step. On yes, Koinon runs that one fixed command through sudo, without a
shell; sudo asks for the password, and Koinon never reads or stores it. Koinon then asks
the daemon to enable once more. On no, an empty answer, the end of input or a sudo
failure, enable shows the manual step, as before, and does not ask again. The dashboard,
`--json` output, scripts and `koinon remote status` do not change; the daemon never runs
sudo. The tailnet policy stays a manual step that status names.

**Reason and scope:** The operator step is the one that stops setup at the terminal, and
it needs the password once. The question states the full operator scope. The policy step
stays manual for now: a writer would need a credential with the `policy_file` scope that
changes the whole tailnet's access, its storage and renewal, and a check against a
GitOps-managed policy, which overwrites an edit made outside its repository. This
limits the scope of this change. It does not rule out policy automation.

**Review when:** Tailscale offers a grant that covers only Serve, or automating the
tailnet policy is raised again.

### Alternatives Considered

1. **Keep both steps manual.** No code and no new question; the person runs one sudo
   command once per computer. Not chosen: the step stops setup at the terminal.
2. **Also write the tailnet policy with a stored `policy_file` credential.** No manual
   policy step, but Koinon would hold a secret that can change the whole tailnet's
   access, and a GitOps-managed policy can overwrite or reject the edit. Declined for
   now.

## 7. Refinement: A chosen Serve HTTPS port

**Requested by:** Robert Capps, 2026-11-12 (#665): "we should be able to set the port,
and it propagates to all aspects of the interface, including QR code and link
generation."

**Context:** Remote access always used Serve's HTTPS port 443. On a host where a
container publishes `443:443` on all addresses, tailscaled and the container cannot share
the port, and whichever starts second fails. A Serve mapping moved by hand would
disagree with Koinon's ownership record and its links.

**Decision:** `koinon remote enable --https-port PORT` (the API's `https_port`) chooses
Serve's HTTPS port, 1 to 65535. The port is part of the saved remote origin,
`https://HOST.TAILNET.ts.net:PORT`, written without a port for 443, so the URL, the QR
code, the sign-in tickets, the Host and Origin checks, the remote sessions and the
tailnet policy entry all use it. An enable without a port keeps the saved one, or 443.
A new port on an owned mapping takes the disable steps for the old one first.

**Reason and scope:** One saved origin keeps every remote surface on the same port.
Ownership, conflict and identity checks keep their rules on the chosen port; the host's
Tailscale identity is its name without the port. The dashboard does not choose the port.

## 8. Refinement: This computer and the tailnet only

**Requested by:** Robert Capps, 2026-11-12 (#665): "ask for the dashboard to be mountable
localhost and tailnet only, as an option to mounting it on the LAN IP. In fact, the
localhost and tailnet only is probably the better solution."

**Context:** The first install asked whether the dashboard opens only on this computer or
also on a network address. `--dashboard-address none` with remote access enabled already
kept it off the LAN, but nothing offered that as a choice.

**Decision:** The install question offers a third answer, "On this computer and your tailnet,
through Tailscale Serve", recommended on a computer in a tailnet. It asks the Serve HTTPS
port, saves the local-only answer, offers the maintainer secret when none is set and enables
remote access after the start; a failed enable leaves the install complete and names the
command. The dashboard's remote access panel names where the dashboard opens and offers
**Use this computer and the tailnet only**, which enables remote access, saves the local
answer, removes the network address and closes the network listener at once.

**Reason and scope:** The tailnet URL gives HTTPS without a self-signed certificate and keeps
the dashboard off the LAN. The switch changes nothing when remote access cannot be enabled,
so a person who uses the dashboard from another computer keeps a way in. Opening a network address stays an install
step.


## 9. Refinement: Remote control v2 fixed sign-in and recovery

**Requested by:** Andrew Deugarte (@andyde8), 2026-11-12, direct conversation.
**Decided by:** Robert Capps, 2026-11-13, board card REL-SCOPE-673 (approved, revision 2),
for release v0.11.0. Robert Capps takes as his own decisions the fixed 48-hour sign-in that
survives a restart, and crash survival with its storage-failure, process-loss and
clock-rollback limit. His words: "this is the next release. Why defer it?" and "673 comes
with .11". The [sprint decision record](../../sprints/2026-11-12-remote-control-v2/decision.md)
holds the context and the open real-phone checks (#672).

**Context:** How long should remote browser sign-in last? The draft considered
90-day/year retention and the earlier seven-day policy; Andrew first requested
about 24 hours, then changed the requested fixed window to 48 hours. The choice
was the duration, rather than adding a sliding extension or changing local/API
policy.

**Decision:** Andrew said, “Let's make it 48 actually,” then explicitly authorized
implementation: “The confirmed session policy is a fixed 48 hours from sign-in,
with no automatic extension. Remote login should survive routine restarts and
upgrades within that original window. Local browser and maintainer API-token
policies stay unchanged.” These words were verified against the direct
conversation before recording this clarification.

**Reason and scope:** Retain phone sign-in within that fixed window across routine
restart/upgrade. This supersedes the earlier remote seven-day proposal. It covers
remote browser sessions and the requested v2 implementation; it does not change
local browser or maintainer API-token lifetimes. The later crash-survival and
failed-persistence decision is recorded with its question, options and answer in
[the v2 continuation](../../sprints/2026-11-12-remote-control-v2/decision.md).

Remote sessions are browser sessions with explicit individual/all revocation,
labels and a separate 32-session engineering capacity. Activity does not extend
the deadline. Password replacement (even while stopped), remote disable and origin
change revoke remote credentials and pending tickets. Routine shutdown and temporary
network loss preserve the fixed window. Public sign-in remains generic.

Reconnect refreshes reads and CSRF without write replay. Browser draft recovery is
optional, off initially, private and explicit, with seven-day retention and documented
offline-erasure limits. Messages, room posts and verdicts have transactional host
receipts and explicit same-ID recovery; external terminal/process side effects do
not gain exactly-once claims. Remote starts on Attention with browser-specific read
cursors, distinct from agent acknowledgement. Observed diagnostics do not prove
cellular access or actual device retention.

The 32-session bound, storage representation, jitter and UI labels are engineering
choices supporting the authorized spec, not separately dictated maintainer policy.
Review when field device acceptance exposes retention/recovery issues or capacity
requires revision. Actual phone/platform evidence is tracked in the v2 sprint.

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
| 2026-11-11 | Amend browser QR sign-in and remote-view presentation after Andrew's phone test and explicit choice. |
| 2026-11-11 | Record the requested longer-session refinement and its remote-only scope, without assigning a cause to the phone sign-out report. |
| 2026-11-12 | Accepted by Robert Capps (#645). Name the proposer as Andrew Deugarte (@andyde8) and Robert Capps as the decider. A remote session cannot create a sign-in QR (review of PR #644). Correct the spelling in quotes. |
| 2026-11-12 | Record Robert Capps's decision TAILSCALE-AUTO-656 (#656): the terminal runs the Linux operator step after confirmation; the tailnet policy step stays manual. |
| 2026-11-12 | Record Robert Capps's request for a chosen Serve HTTPS port (#665). |
| 2026-11-12 | Record Robert Capps's request for a "localhost and tailnet only" choice (#665). |
| 2026-11-12 | Record Andrew's v2 implementation authorization, fixed 48-hour policy, durable sessions and explicit recovery scope. |
| 2026-11-13 | Record Robert Capps's decision REL-SCOPE-673: §9 ships in v0.11.0 as his decision; Andrew stays the proposer. §5 is superseded for remote browsers. |
