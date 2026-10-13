# Managed context handoff — proposed implementation plan

Sources: [decision.md](decision.md), [definition-of-done.md](definition-of-done.md),
[evidence.md](evidence.md), PDR 006 and the canonical `handoff`, `pickup` and `peer-tmux`
skills. Gate A is approved. This technical plan awaits independent review and Gate B.
The maintainer assigns implementation chunks; the pair agrees each chunk's criteria before
edits. Terminal interaction is part of the requested product function.

| Order | Complete increment | Dependencies |
| --- | --- | --- |
| 01 | [Context policy and observations](chunks/01-monitor.md): family settings, session Enable records, capability/freshness display and epoch-aware trigger selection | Published develop; no native command execution |
| 02 | [Correlated handoff contract](chunks/02-handoff.md): bounded durable cycles, typed request, own-session readiness, artifact checks, audit and capacity proof | 01 merged; #145 catalog/read interfaces |
| 03 | [Verified native cycle](chunks/03-native-cycle.md): evidence-first family adapter, clear/pickup and prompt handling, crash reconciliation, successor Enable and complete dashboard | 02 merged; family/platform proof and #275 where applicable |

Each chunk is independently testable and mergeable. Until 03 admits a native adapter,
Enable shows the missing capability and the daemon executes no cycle. This is an explicit
implementation state, not a permanent restriction on terminal input. Monitoring remains
usable independently. A missing interface is investigated in the adapter work; it is not
replaced by an assumed command or invented identity.

## Proposed shared contract

### Settings, source and identity

Extend the existing integer `settings` rows with mode/threshold keys for each supported
family. Modes map to fixed integers; decode unknown stored values to the documented default
and show the configuration error. Family updates use one dashboard transaction with its
audit record and apply after commit. They grant no session Enable.

Add `context_enables`, keyed by private `(family, session_id)`, with enabled, revision,
participant address, pinned host/start, exact unresolved socket/pane and update time.
The dashboard selects the displayed peer name plus session revision, resolves it inside the
transaction and checks current identity. No model sets a family or native caller. Admission
requires a cycle-capable adapter for this platform/version and a verified launched host.
Changing family mode away from the cycle cancels pending cycles; changing the threshold
applies to the next request and does not rewrite an admitted cycle. Disabling a session
fences further input immediately, including an already waiting cycle.

Context-source time and readable confirmation time remain separate. Cycle selection uses
the DoD clocks, not the looser display freshness. Add private native-epoch metadata to the
admitted native observation path: Claude's owned registry/transcript session epoch under
the pinned parent host/start; Codex's current rollout thread epoch. Epoch values come from
the native reader, not model arguments. Existing observed context fields stay compatible.
An unverified epoch is an explicit unavailable reason. A new epoch or compaction resets the
crossing latch; unchanged high samples cannot create another request for the same epoch.
A successful cycle is eligible again only on a verified new epoch and new threshold evidence.

### Durable cycle and stage transitions

Add `context_cycles` with opaque cycle ID, revision, pinned private recipient/host/pane,
participant, Enable revision, pre-reset epoch, family mode/threshold snapshot, state,
fixed reason, source/confirmation times, deadlines, artifact metadata, adapter version,
catalog/item digests, input intent/receipt flags, successor metadata and remaining credit.
Add bounded `context_cycle_events` keyed by `(cycle_id, ordinal)`, at most 16 per cycle.
They contain fixed stage/result/reason, time and public display label, never text captures.
Choose the next schema version against published develop at implementation time; preserve
existing messages, claims, cursors, receipts and exact catalog validation.

| State | Transition evidence | Next result |
| --- | --- | --- |
| requested | Atomic typed request and capacity admission | waiting_ready |
| waiting_ready | Own-session ready call, current epoch and verified artifact | ready |
| ready | Direct fresh idle/prompt and host/pane recheck | clear_intent |
| clear_intent | Recorded intent precedes external text/Enter | clear_received or uncertain |
| clear_received | Verified native reset epoch in the same host/pane | reset_verified |
| reset_verified | Current native bootstrap can receive canonical pickup | pickup_intent |
| pickup_intent | Recorded intent precedes bare pickup input | waiting_pickup or uncertain |
| waiting_pickup | Current receiving model reports restored checkpoint; native registration and epoch reconcile | complete |
| waiting_prompt | A recognized prompt records its preceding stage; a response is read back | preceding stage, failed or uncertain |
| any unfinished state | Disable, deadline, lost proof, contradiction or unprovable external result | cancelled, failed or uncertain |

Terminal states record one outcome and end their debt. Failed, cancelled or uncertain
cycles end Enable. Completion carries Enable only after same-pane, same-participant
successor verification; ordinary clears and unrelated registration cannot carry it.
Retained Claude caller identity with a proven new native epoch is supported by that adapter,
not forced through invented session retirement. Existing succession handles a changed
caller and its holder result; the controller does not administratively move the address.

Every transition uses a compared revision. Identical current retries return the current
receipt without an additional write; conflicting/stale input returns the current state.
Reads move no inbox, memory or work cursor. Cycle cancellation and native actions serialize
on the exact host/pane. Recheck the Enable/cycle revision immediately before every side
effect; record an already-entered operation whose result races cancellation as such.

### Request, tools and handoff artifact

The daemon writes a typed managed-cycle request to the exact native recipient's inbox in
the admission transaction. Its fixed body identifies the cycle and asks that session to
finish or pause its step and use its own canonical handoff procedure. Its metadata states
the originating Enable revision; it is an internal managed request, not a fabricated
maintainer-authored chat post. The usual content-free wake follows the committed inbox row.
The complete own-cycle query remains available if a wake fails.

Proposed native tools are `context_cycle_status`, `context_cycle_ready` and
`context_cycle_pickup`; equivalent DeepSeek command forms keep its existing explicit-native
guard. Status returns only this session's current/retained cycle. Ready accepts cycle ID,
compared revision and artifact basename. Pickup accepts cycle ID, compared revision,
artifact basename, outcome `resumed` or `paused`, and a bounded restored-work report.
The report is at most 1,024 UTF-8 bytes; it is private runtime data, not audit text.
No argument accepts caller identity, epoch, socket, pane, host, permission mode or another
session. Strict decoder and family-purpose coverage apply to every new tool.

The native client attaches its verified current epoch. The daemon correlates ready with
the pinned requester and pickup with the separately verified managed successor. An
ordinary `send` body, acknowledgement or peer report is neither transition. The restored
work report is private runtime data; audit/status omit its body. Native proof tests must
show that the model made the pickup call after reading the intended checkpoint.

Resolve the registered Git common directory and actual worktree through existing checked
checkout logic. Verify the main checkout's own `_handoff/<family[-role]>/` directory,
the timestamped basename, regular file, nonzero bounded size, current file identity and
creation/modification after the request. Verify Git ignores it and tracks nothing there.
The daemon does not read the artifact body. A changed inode requires a fresh ready check.
The native agent writes its own handoff and retains task/pause state under the canonical
skill. No memory import, replacement file or claim release is performed by the controller.

### Capacity and crash behavior

Admission is an ordinary transaction that creates the cycle, typed request, initial event
and audit, and funds all promised remaining transitions. Proposed maximum credit is 512
pages per active cycle (16 cycles); chunk 02 must derive a structural upper bound for its
actual tables, keys, event strings, audit inserts and terminal flag updates that fits this
budget. If it cannot, correct the allocation/schema before publishing; no unsupported page
constant passes review. Include this credit in `debtPages` and all write ceilings.

Bound events and reasons, serialize one terminal outcome, debit a fixed proven amount per
funded transition and release unused credit at the terminal commit. Wait polls and status
reads write nothing. Old terminal cycles/events may be deleted under bounded reclamation;
active cycles and their credits are never evicted. Test the shared work controls alongside
all admitted cycles at ordinary saturation and after migration/restart.

On restart, reload Enable and cycle records, but treat observations and prompt/epoch proof
as unknown until native evidence returns. A recorded clear/pickup intent is never resent
merely because no receipt was committed. Inspect the exact target and reconcile a result
that the native adapter can prove. Otherwise record uncertain, end Enable and show the last
confirmed step and saved artifact metadata. A later cycle requires a fresh handoff.

## Wake, dashboard and reporting contract

### Wake and dashboard interaction

Pane-typing wake adapters use the same host/pane serialization as the executor. From
ready through waiting_pickup, including prompt waits, defer them without marking them
delivered or losing inbox rows. Never interleave notice text/Enter. After a terminal
cycle result, resume ordinary wake scheduling against the current proven recipient.
Socket/API wakes may queue an ordinary inbox notice; a notice cannot report ready/pickup.

Dashboard retire, purge and participant-holder changes fence affected active cycles and
end their Enable in the action transaction. Cancel when every entered effect is reconciled;
an outstanding external input intent is uncertain. The executor checks the fence before
its next input. Purge retains terminal evidence and releases debt before deleting the
target. An unrelated native clear/epoch change cancels the pinned cycle, or makes an
unreceipted input uncertain. The existing dashboard clear action acknowledges its inbox;
it neither resets native context nor cancels a cycle. The cycle query survives the cleared
request, and acknowledgement never supplies readiness.

### Post-clear reporting instruction

The instruction lives in the installed `koinon guide --agent FAMILY` section Managed cycle
reporting, generated by `internal/setup/guide.go`. MCP initialization points to the guide;
the canonical pickup reconnect step reads it after reset. The section instructs the model
to query `context_cycle_status`; for its waiting_pickup cycle, finish canonical pickup of
the returned own artifact, then call `context_cycle_pickup` with ID/revision, basename and
restored resumed/paused work report. It does not copy the handoff/pickup procedures.
The proven missing-skill bootstrap carries this same guide reference into the cleared
session. D9 evidence must show the model read the section and made the report; an older
installed guide or retained initialization string alone is insufficient.

## Review and approval

The other family reviews the frozen plan once against S1-S6 and D1-D13. Findings and their
dispositions go in [review.md](review.md); accepted fixes receive bounded review. Present
the reviewed hash and concrete implementation split for Gate B. Gate A's settled choices
are not asked again. No product code, native session reset or host setup is performed by
this planning branch.
