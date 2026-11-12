# System link — definition of done

Every chunk cites `decision.md` and this file. Tests are synthetic and run in CI on Linux and
macOS (WDR 004, Decision 3). Each guarantee is proven once, in the chunk that adds it, at the
boundary where Koinon enforces it. Two daemons in one test run use separate private state
directories and a fake Tailscale adapter; the TLS between them is real.

## Identity, name and keys

- Each daemon has one link key (Ed25519) and a self-signed certificate in its private state
  directory, mode 0600 and owner-checked. Restarts reuse them. Certificate renewal keeps the
  key, so the pin (SHA-256 of the SubjectPublicKeyInfo) does not change. The maintainer does no
  manual work for certificate expiry. Key replacement is unpair and pair again.
- The advertised name (LINK-670-ADDR) is asked once at the first **Add a computer** and in
  `koinon link name`. The field is filled with the lower-cased `hostname -s` value; the long
  host name and a typed value are possible; one trailing root dot is removed. Grammar: lower-case
  ASCII letters, digits, hyphens and dots; at most 253 bytes; each dot-separated label 1 to 63
  bytes with alphanumeric ends. A wrong value is refused with the reason and the field stays
  editable. Setup shows the resulting address. A host name change does not rename it.
- Tests: the short-name fill on Linux and macOS with synthetic host names (with and without
  dots, upper case, a trailing dot); the long name unavailable; refusals; restart reuse; a
  renewal that keeps the pin.

## Pairing and Remote

- Pairing needs Remote on, on both computers (LINK-670-PAIR). With Remote off, invite, join and
  link traffic are refused with a step that names `koinon remote enable`.
- Invite, join, unpair and rename need the maintainer login (dashboard) or the maintainer
  secret (CLI) on that computer. Agents that hold the secret can act. No ancestry check
  (PDR 009).
- The invitation holds the tailnet name and link port, the key pin, the advertised name and a
  random single-use secret of at least 128 bits, with a 10-minute expiry. `koinon link join`
  reads it from the terminal or standard input, never from argv. The joining daemon verifies
  the pin before it sends the secret or accepts the pair. Both pins commit only after both
  sides have verified each other.
- Wrong pin, wrong or reused secret, expiry and cancel give no pair and no message access. An
  interrupted pairing can leave a pending record on one side; the next status read or invite
  shows it, and the maintainer can finish or cancel it. No test claims an atomic commit on two
  hosts.
- Remote enable adds one raw TCP Serve mapping (`tailscale serve --tcp`, never
  `--tls-terminated-tcp` and never the HTTPS route) from the link port to a loopback TLS
  listener, under the same write-ahead ownership record as the dashboard mapping. The link
  port is separate from the dashboard HTTPS port and can be changed. Remote disable closes link
  sessions, stops reconnects and cancels open invitations at once; pairs stay. A disable whose
  record write fails keeps links closed (the runtime off state, as for the dashboard). Restart
  with Remote off opens no link listener.
- The link listener accepts only TLS 1.3 with a client certificate. `VerifyConnection` checks
  the pin on every connection, resumed sessions included (or resumption is off). An unpaired
  key gets only the pairing exchange of an open invitation; everything else is refused before
  any application data. The agent API is never served on the link port.
- Unpair removes the pin on this side at once and closes its live connections; the other side
  learns it on its next connection. Re-pairing creates a new pair generation.
- Status (dashboard Remote settings, `koinon link status`, `koinon remote status`) shows each
  paired computer as connected, offline or pending from an observed authenticated connection,
  with the time; saved pins alone never show "connected". A rejected unexpected key is shown.
- Tests: two daemons pair through invite and join; each refusal above; disable during pairing
  and during a connection; disable with a failed record write; restart while off; resumed TLS
  after unpair is refused; a stale invitation after disable is refused; a real raw TCP Serve
  probe on a tailnet host before acceptance (evidence in `verification.md`).

## Directory and addressing

- A paired computer sends a directory of its sessions that can receive: peer name or
  participant address, family, role, repository label, observed activity and its time. No
  native session IDs, folder paths, host or process data, or credentials. A stale entry is
  shown with its time and never as current.
- `NAME@COMPUTER` (LINK-670-ADDR): COMPUTER is the pair's effective local name: the advertised
  name, or a free local name (`name-2`) when it collides here, or a maintainer override. The
  advertised, effective and override names are stored separately and changed atomically. A
  rename from the other computer changes only that pair's name. A bare name is local only. An
  unknown COMPUTER is refused at the sender, with no DNS lookup and no connection. A known
  COMPUTER with a valid NAME is sent; the other daemon resolves NAME, including a participant
  address to its current holder, and refuses an unknown or unheld name. No forwarding of
  `NAME@OTHER` through a second computer.
- Address limits: NAME keeps its local rules; COMPUTER has the grammar above; send, peers,
  peer status, inbox and reply paths and the dashboard field accept a full address of up to
  64 + 1 + 253 bytes. Tests with a long NAME and a long COMPUTER round trip without truncation.
- `peers` (MCP and CLI) and the dashboard list remote entries as `NAME@COMPUTER`, marked
  remote, with the time seen.
- Tests: asymmetric names, rename, collision with the default and with a rename, override,
  stale directory, bare local name equal to a remote NAME, alias succession and unheld on the
  other computer, unpair and re-pair.

## Messages

- A send to `NAME@COMPUTER` that passes validation and the caps commits to a private outbox on
  the sending daemon, bound to the pair generation, before the reply "queued here" with a
  cross-node message ID. Remote off pauses network attempts only; the queue stays. Restart
  keeps the queue, its order and its IDs.
- Stream rules (one stream per pair generation and direction):
  1. The sender sends only its unresolved head, and sends the next one only after it stored the
     head's terminal result. Wire sequence numbers are assigned when an item becomes the head.
  2. The receiver accepts a terminal operation only at highWater+1. One transaction stores
     highWater, the head's exact result (received or refused) and a digest of the sequence,
     recipient, body and envelope; the inbox insert is in that transaction only for a receipt.
     A permanent refusal (unknown name, unheld, inactive) advances the mark; busy or full does
     not and is retried with backoff. A gap is a protocol error.
  3. For n == highWater the receiver checks the digest and returns the stored result again;
     n < highWater is obsolete and gets no insert and no invented result; the sender ignores
     stale responses.
  4. A cancel of a queued, never-sent item removes it before it gets a sequence. A cancel of an
     attempted head stops attempts and shows "may have been delivered"; its sequence is never
     reused for another body. Unpair retires the generation.
  5. A live generation's highWater, last result and digest survive inbox ack, retention and
     purge at both sides.
- One accepted cross-node message ID is inserted at most once. Two sends of the same text are two
  messages.
- States the sender sees: queued here, received there, refused there (with reason), cancelled,
  may have been delivered. "Received there" means a committed inbox row, not a read. The
  receiver's wake, read and ack stay local. No time expiry.
- The received message carries a typed origin: the pair (immutable) and the sender's name on
  that computer, shown as `NAME@COMPUTER`. The sending daemon derives the sender from its own
  authenticated caller. The receiver creates no local session for a remote name. A remote
  message can never have the maintainer or overwatch origin, and the link exposes no local
  control, room, decision or cycle operation. A reply to it goes back through the stored pair.
- Caps: a global and a per-pair count and byte limit on the outbox. At a cap a send is refused
  (`link_queue_full`); no accepted message is evicted. Finished outbox entries are deleted 30
  days after their terminal result; pending entries never age out.
- Tests: crash before and after the receiver's commit and before the sender stores the result;
  lost receipt; lost refusal; long disconnection with body ack and retention at the receiver;
  concurrent enqueue; temporary backpressure; head-of-line blocking; cancel of a queued head, a
  middle item and an uncertain head; delayed old frames; gaps and reordered requests; the same
  sequence with a changed body; Remote disable races and restart; alias changes; unpair during
  uncertain delivery; a remote message that claims the maintainer origin is refused.

## Documentation and records

- Each chunk updates, in the same pull request: README.md, PROTOCOL.md, docs/USAGE.md,
  docs/REMOTE-DASHBOARD.md, `koinon guide` text, CHANGELOG.md, and the records: ADR 002
  amendment (Remote also carries the daemon link, LINK-670-PAIR), ADR 001 (a link listener
  beside the loopback agent API), and a PDR for links (LINK-670-WHO, WHAT, ADDR, OFFLINE).
- Public text uses synthetic host names and addresses only.

## Checks and evidence

- The project checks of AGENTS.md, the four CGO_ENABLED=0 builds, and CI on Linux and macOS
  at the exact head.
- Before the last chunk merges: a live pairing and message round trip between a Linux and a
  macOS computer on one tailnet, with the commands and results in `verification.md`. The
  maintainer runs the steps on the computer that the agents cannot reach.
