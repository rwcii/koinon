# System link — decision

## Problem

Robert Capps, 2026-11-12, terminal: "let's plan a new feature for koinon to allow separate
systems running koinon to connect to each other and pass messages." Filed as
[#670](https://github.com/rwcii/koinon/issues/670).

Each Koinon daemon serves its agent API only on loopback, with its own secret and one private
store (ADR 001, Decision 2). ADR 001 does not adopt an agent API beyond loopback. So agent
sessions on two computers cannot exchange messages today.

## Scope decisions

### LINK-670-WHO: which systems connect

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 1, approved.
- **Context:** whose Koinon installations the first deliverable connects. The options were the
  maintainer's own computers, each with its own Koinon daemon, or also Koinon installations of
  other people.
- **Decision:** the first deliverable connects only computers that the maintainer owns and
  operates, each with its own Koinon daemon.
- **Reason and scope:** one owner decides for each side, so fewer ownership and consent
  questions apply. The same owner does not mean a shared secret or automatic trust: each
  daemon keeps its own credentials, session identities and private store. ADR 002 accepted
  browser access only; it does not approve daemon messages or a new listener.
- **Alternatives considered:** also connecting Koinon installations of other people. That
  needs explicit rules for their consent and for their memory, rooms and decision board.
- **Review when:** Koinon installations of other people must connect.

### LINK-670-PAIR: how computers pair and how link traffic is protected

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 5, approved, after feedback on revisions 1, 3 and 4.
- **Context:** how two of the maintainer's daemons find, trust and reach each other.
  Revision 1 proposed a separate link listener, a key per daemon, mutual pins, the maintainer
  secret for invite, join and unpair, and unpair at once. Feedback: "agreed, propose a design.
  How can we EASILY introduce them to each other?" Revision 3 proposed one invitation: Copy on
  one computer, Paste and Connect on the other. Feedback: "looks good, how do we manage secure
  connections? Will this be LAN only, LAN plus Tailscale, something else?" Revision 4 proposed
  any reachable network with pinned keys. Feedback: "I think this should be an offshoot of the
  remote function, i.e., you must enable remote to enable peer Koinon nodes. How will node to
  node traffic be protected? TLS?"
- **Decision:**
  1. Links are part of Remote. **Add a computer** and the paired computers are in the Remote
     settings. Both computers must have Remote on; with Remote off there is no pairing and no
     link traffic.
  2. Pairing is one invitation: **Copy invitation** on one computer (`koinon link invite`, also
     as a QR code), **Paste** and **Connect** on the other (`koinon link join`, which reads the
     invitation from the terminal or standard input, never from the command line). The
     invitation holds the computer's tailnet name and link port, its key fingerprint and a
     random single-use secret that cannot be guessed, with a short expiry. The joining daemon
     checks the key against that fingerprint before it sends the secret or accepts the pair.
  3. Invite, join and unpair need the maintainer login or secret on that computer. Agents that
     hold the secret can act; there is no check of process ancestry (PDR 009).
  4. Transport: Remote adds a raw TCP Serve mapping for a separate, changeable link port, to a
     link listener on loopback. Remote enable adds it and disable removes it.
  5. Protection: TLS 1.3 from daemon to daemon, through the raw TCP mapping, with each daemon's
     own key and the other's pinned key checked on each connection. Tailscale's WireGuard is
     around it. Tailnet membership, the dashboard login and the agent API bearer give no link
     access. No long-lived daemon credential is copied between computers, and the maintainer
     secret never crosses the link.
  6. Remote disable closes link sessions, stops reconnects and cancels open invitations at once;
     pairs stay unless unpaired. Unpair rejects the other daemon at once on this side.
  7. The agent API stays on loopback. Link routes and credentials are separate from the
     dashboard and the agent API.
- **Reason and scope:** the maintainer wants nodes to be part of Remote. Remote already gives
  each computer a tailnet name and a managed Serve setup, so pairing needs no address. The raw
  TCP mapping keeps the pinned daemon keys; the HTTPS mapping ends TLS in tailscaled. This
  amends the scope of ADR 002, which approved Remote for browser access only.
- **Alternatives considered:** a link listener on any LAN, tailnet or VPN address (revision 4);
  LAN only; Tailscale only with tailnet device identity as trust; links on the Remote HTTPS path
  with a credential per pair (loses the pinned daemon keys); a direct tailnet listener outside
  Serve (Remote could also own it; declined because the approved design reuses Serve); comparing a 6-digit code on two screens; LAN discovery by mDNS.
- **Review when:** computers without Tailscale must link, or Tailscale Serve changes its TCP
  forwarding.

### LINK-670-WHAT: what crosses the link

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 1, approved.
- **Context:** which Koinon objects pass between paired computers in the first deliverable.
  The options were agent direct messages with a small receiver list, also rooms across
  computers, and also dashboard messages to sessions on the other computer.
- **Decision:**
  1. Agent-to-agent direct messages cross the link. A message goes into the receiver's inbox
     with that computer's ordinary wake. Receipt means that the other daemon stored it; receipt,
     wake, read and acknowledgement are separate states.
  2. Each computer sees a small list of the other computer's sessions that can receive: name or
     participant address, family, role, a repository label, and observed activity with its time.
     No native session IDs, folder paths, process data or credentials cross. An old entry does
     not show a current receiver.
  3. Each message carries the paired computer that it came from. The link channel exposes no
     operation that acts as the maintainer, as overwatch or as a local agent, and none that
     reaches local controls, rooms, decisions or context cycles. A receiving agent can still do
     authorized local work that an ordinary peer message asks for (WDR 002).
  4. Bodies cross only on the paired link and stay runtime data in each daemon's private state.
  5. Memory, work items and claims, rooms, the decision board, context cycles and the maintainer
     inbox stay on each computer. Dashboard messages to a session on the other computer are not
     in this deliverable. Pairing and Remote settings stay in the dashboard.
- **Reason and scope:** the request is to pass messages, and direct messages are the existing
  message surface. Memory, work and decisions are stores for each repository; syncing them is a
  different problem. The exclusions are the scope of this first deliverable, not a permanent ban.
- **Alternatives considered:** rooms across computers (membership and post order across two
  stores); dashboard messages to the other computer (a protected maintainer origin across
  computers).
- **Review when:** agents need shared memory, work items, rooms or decisions across computers,
  or the maintainer wants to write to the other computer from one dashboard.

### LINK-670-ADDR: how agents address a session on another computer

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 2, approved, after feedback on revision 1.
- **Context:** revision 1 proposed `NAME@COMPUTER`, where the receiving computer sets a local
  label for each paired computer, by default its tailnet machine name. Feedback: "agree. When a
  node is first set up for multi-node connection, it should select either a long or short name
  to advertise its agents. hostname -s should populate the shortened version. This way we don't
  end up with every address being [agent]@[long host name]." (The bracketed parts replace the
  example address in the feedback.)
- **Decision:**
  1. The first **Add a computer** on a computer asks once for the name that it advertises. The
     field is filled with the lower-cased short name from `hostname -s`; the long host name or a
     typed name is possible. Setup shows the resulting address, for example
     `codex-koinon@devbox`. The name is saved; a later host name change does not rename it. It
     can be changed in the Remote settings.
  2. The computer sends this name in its invitations and on the link. The other computer uses it
     as COMPUTER in `NAME@COMPUTER`.
  3. When two paired computers advertise the same name, the receiving computer gives the second
     a free local name, for example `devbox-2`, for that pair only.
  4. A name has lower-case letters, digits, hyphens and dots. A wrong value is refused with the
     reason. Full addresses get room for a long NAME and a long COMPUTER; local name limits do
     not change.
  5. A bare name always means this computer. The other computer resolves NAME to its current
     holder. An unknown COMPUTER is an error at the sender, with no DNS lookup and no new
     connection.
  6. The name is for addresses and display only. Trust stays with the pinned keys; replies and
     queued messages stay bound to the pair; a name change from the other computer changes only
     that pair's name. No message goes through a second computer.
- **Reason and scope:** short addresses that each computer chooses for itself. On macOS and
  Linux, `hostname -s` gives the host name up to its first dot, without a change of case
  ([macOS hostname.c](https://raw.githubusercontent.com/apple-oss-distributions/shell_cmds/main/hostname/hostname.c),
  [Linux hostname 3.23](https://sources.debian.org/src/hostname/3.23/hostname.c)).
- **Alternatives considered:** a label that the receiving computer chooses (revision 1); one
  global name for each session across computers; the key fingerprint as COMPUTER.

### LINK-670-OFFLINE: delivery when the other computer cannot be reached

- **Decided by:** Robert Capps.
- **Date:** 2026-11-12, revision 1, approved.
- **Context:** what happens to a message when the other computer is off, asleep or away. The
  options were a queue at the sender with no expiry, a queue with an expiry (for example 7
  days), read and acknowledgement states sent back to the sender, and no queue.
- **Decision:**
  1. A valid send to a paired computer is stored at once in a private queue on the sending
     computer; the sender gets a message ID and "queued here" only after that store.
  2. While Remote is on, the daemon delivers the queue in send order for each paired computer
     and retries after a failure. With Remote off, messages stay queued. A restart keeps the
     queue and its order.
  3. The other computer stores the message and answers "received there", or "refused" with the
     reason. A refusal does not stop the next messages. "Busy" or "full" is temporary and is
     retried.
  4. "Received there" (stored in the inbox, not read) is the last state that the sender sees.
  5. No message is stored twice, also after a lost answer and a long break.
  6. No expiry: a message waits until it is received or refused, or until the maintainer
     cancels it or unpairs.
  7. Unpair or cancel stops attempts. A message whose answer was lost shows "may have been
     delivered". A new pairing does not revive old queued messages.
  8. The queue has count and size limits, for all pairs and for each pair; at a limit a new send
     is refused, and no queued message is dropped. Finished queue entries are kept 30 days after
     their result (proposed value).
- **Reason and scope:** the maintainer's computers are not always on. A queue at the sender
  loses no message and keeps the order. No expiry matches local messages, which wait until they
  are handled. Limits: a message can arrive very late; a stuck message at the front delays later
  messages to the same computer; the sender sees no read state.
- **Alternatives considered:** an expiry (a sender can lose a message without a reply); read
  and acknowledgement states across computers (more traffic and states); no queue (agents must
  retry).
- **Review when:** messages that arrive very late cause harm, or agents need the read state of
  a message on another computer.

### REL-NEXT-VERSION: the release number for this work

- **Decided by:** Robert Capps.
- **Date:** 2026-11-13, revision 1, approved.
- **Context:** the number of the next release from develop after v0.10.3. That release holds
  this deliverable (#670: `koinon link`, messages between computers) and moves the store to
  schema 38, which a v0.10.x binary refuses, so an upgrade cannot go back. v0.10.1 to v0.10.3
  were patch releases. The options were v0.10.4, v0.11.0 and v1.0.0. The card's context also
  cited an agent memory note of a 2026-11-01 direction to clear the number with the
  maintainer.
- **Decision:** v0.11.0. Robert's note: "yes, 0.11.0 is the release version".
- **Reason and scope:** a new feature and a one-way schema change; a patch number would hide
  that an upgrade cannot go back to v0.10.x. v1.0.0 would say that the product is stable,
  which nobody decided. This covers this release only. REL-SCOPE-673 (in
  [the remote control v2 decision record](../2026-11-12-remote-control-v2/decision.md)) adds
  #673 to the same release.
- **Alternatives considered:** v0.10.4 (keeps the v0.10 line, hides the feature and the schema
  change); v1.0.0 (claims stability).

## Acceptance criteria

1. On two of the maintainer's computers with Remote on, **Copy invitation** on one and
   **Paste** and **Connect** on the other pair them; both show the other as connected. The
   same works with `koinon link invite` and `koinon link join`.
2. A computer that is not paired, a wrong key, a reused or expired invitation, and Remote off
   give no pairing and no message access.
3. Link traffic is TLS 1.3 between the two daemons, with each daemon's pinned key checked on
   each connection.
4. An agent on one computer sees the other computer's receivers as `NAME@COMPUTER`, where
   COMPUTER is the name that the other computer advertises, by default its lower-cased
   `hostname -s`.
5. An agent sends to `NAME@COMPUTER`; the receiver gets it in its inbox with its ordinary wake,
   from `SENDER@COMPUTER`, and its reply reaches the first agent.
6. With the other computer off, a message waits on the sending computer as "queued here" and
   arrives, in order and once, after the other computer is reachable again. The sender then
   sees "received there", or "refused" with the reason.
7. Unpair or Remote off stops link traffic at once; unpair cancels the pair's queued messages,
   and a message whose answer was lost shows "may have been delivered".
8. A message from another computer never acts as the maintainer, as overwatch or as a local
   agent.
9. Memory, work items, rooms, decisions and context cycles stay on each computer; the agent API
   stays on loopback.
10. The evidence includes a live pairing and message round trip between a Linux and a macOS
    computer.
