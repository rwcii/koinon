# Managed context handoff — definition of done

This is a proposed technical plan for [decision.md](decision.md). Gate A and the
maintainer's configuration, Enable carry-over and terminal-input choices are settled.
Gate B approved the reviewed values, interfaces and three-chunk split on 2026-10-13,
with the reserved overwatch sender added (decision comments 6136155493/6136161569).
Codex's native-compaction default and leaving native compaction settings unchanged are
plan proposals, as recorded in PDR 006; they are not a new maintainer quotation.

## Completion evidence

The issue is complete when the three implementation chunks are merged and the following
matrix has evidence. A family/platform is called available only after its native rows pass;
the CLI version of the run is recorded, and a later version is not refused (PDR 006,
Decision 5). An unavailable family keeps monitoring and ordinary Koinon tools; it shows the
specific missing interface or proof. An unsupported source never reports zero context.

| Check | Observable result | Required evidence |
| --- | --- | --- |
| D1 Configuration | Six family rows supply mode and threshold. All sessions start disabled. Editing a family does not enable any session. A session change applies only to its current native key and revision. | Store restart, concurrent updates, malformed values, absent login, Host/Origin and CSRF tests; one accepted audit in the change transaction. |
| D2 Context | Only the admitted source for that family and current epoch can cross the threshold. The trigger checks source time and confirmation time, available token counts and a positive limit. | Synthetic clocks: equality, below/above threshold, future time, stale source with fresh confirmation, fresh source with stale confirmation, missing/zero limit, unsupported family, compaction and daemon restart. |
| D3 One request | One eligible session has at most one current cycle. Repeated samples, lost replies and concurrent ticks produce one typed handoff request and one cycle ID. | Request commit and inbox/wake rows are atomic. Failure to admit them creates no request or terminal operation. Queries recover the same ID after restart. |
| D4 Readiness | The requested session reports ready for the exact cycle, revision and pre-reset native epoch. An ordinary peer message or another session's tool call cannot do this. | Native MCP identity and DeepSeek command guards; stale/cancelled/duplicate/conflicting reports; family, holder, host and epoch changes; strict argument rejection. |
| D5 Handoff | Before reset, the reported artifact is a new nonempty regular file in the verified main checkout's ignored own-family handoff directory. The daemon records metadata, not its body. | Temporary repositories/worktrees; timestamp and size checks; copied Git pointer, other family, traversal, escaping symlink, changed inode, missing/empty file, nonignored file and repository change. No handoff body in audit, status or wakes. |
| D6 Exact terminal | Each input is preceded by current host/start and pane ancestry proof, including no intervening agent, and a freshly observed appropriate prompt. | Private tmux tests: different pane, reused PID, replaced host, nested agent, unresolved socket alias, copy mode, busy shell, background task, menu, pending text, missing tmux and process-table failure. No name-only target selection. |
| D7 Input and prompts | The adapter enters the family's bare clear and pickup commands, reads back each step, and handles recognized cycle prompts. It does not append task text or enter peer bodies. | Literal text and Enter are separate operations. Completion-menu, confirmation and alert fixtures prove the selected response and read-back. A native permission decision uses existing native authorization; an unresolved choice is displayed with its exact stage. Sending keys alone is never success. |
| D8 Reset | The native transcript/thread epoch changes as proven by the admitted adapter while the expected host and pane remain. Retained Claude MCP identity is reconciled separately. | Dedicated receiving-session evidence: clear command receipt, old/new epoch, retained or changed MCP caller, registration/holder result. A near-zero context value or banner alone is supporting evidence. |
| D9 Pickup | The receiving model reads its own saved checkpoint and reports the restored task and pause/continue state for that cycle through its current native tool call. | Dedicated installed-skill and missing-skill cases. The latter proves command discovery/acceptance, post-clear canonical library read and model receipt without installing a skill. An initialization or queue receipt alone fails this row. |
| D10 Enable continuity | Enable follows only the successful managed successor with the same verified pane and participant. Same-host Claude can retain its caller key while advancing its native epoch. | Failed/uncertain reset, ordinary clear, changed pane/participant, unheld/conflicting holder, another role and native-session claim cases. Participant claims retain their generation; no new claim-transfer rule. |
| D11 Durability | Each transition and wait has a visible stage, time and reason. Crashes never silently resend uncertain terminal input. | Crash injection before/after every database commit, text send, Enter, reset receipt and pickup receipt. Read-back reconciles an observable result; an unprovable result becomes uncertain and ends Enable. |
| D12 Capacity | Admitted cycles can record their bounded terminal outcomes when ordinary storage is full, while existing work controls still complete. | A structural page bound for the actual tables/indexes plus saturation tests at maximum strings and page fragmentation. Test admission failure, all active cycles, cancel/timeout/failure/success, debt restoration, audit retention and restart. A row-count bound alone does not pass. |
| D13 Regression | Native compaction, current wake delivery, registration, dashboard actions, memory and work remain functional. | Whole Go suite on Linux/macOS, representative existing controls at saturation, isolated native metadata/proof harnesses and exact-head CI. |

## Proposed limits and clocks

- Mode is `observe`, `native_compaction` (Codex) or `handoff_cycle`. Threshold is an integer
  from 1 through 100 percent, default 75. Claude's proposed default is `handoff_cycle`;
  Codex's is `native_compaction`; the other families default to `observe`. Every session's
  Enable is false until the maintainer changes it. The mode never edits native settings.
- Evaluate at the existing five-second observation cadence. A trigger needs source age at
  most two minutes and readable confirmation age at most 90 seconds. Existing display
  freshness stays unchanged. Reset/prompt readiness is checked directly immediately before
  input; the display's two-minute idle value is insufficient.
- A handoff request has a 15-minute readiness deadline. Reset and pickup each have a
  two-minute receipt deadline, excluding a displayed prompt wait of at most ten minutes.
  The entire cycle expires after 30 minutes. An expired or cancelled cycle enters no further
  command. Wait polling changes no durable record unless the stage or reason changes.
- At most 16 active cycles, one per host/start and native session. Keep at most 1,024
  completed cycles for 30 days. Each has at most 16 durable transitions; reaching that
  bound ends it with a fixed reason rather than creating an unbounded event stream.
- Artifact basenames are at most 128 bytes. Native metadata fields are bounded by their
  existing registration limits; the handoff size limit is one MiB. Published DTOs carry
  display names, cycle IDs, stage/time/reason and metadata counts, never native IDs,
  terminal/socket paths, prompt captures or artifact contents.

These limits are engineering proposals. Any implementation correction within the accepted
scope is documented and independently reviewed; no unavailable interface is described as
built because a specification names it.

## Native proof matrix

| Family | Current context evidence | First-cycle position |
| --- | --- | --- |
| Claude | `claude_statusline`; registry activity; same-host post-clear observations corrected by #304 | Candidate first adapter. Dedicated Linux/macOS clear, epoch, installed/missing-skill pickup and prompt proof must pass before Enable is available. |
| Codex | `codex_rollout`; native compaction; thread changes on clear | Monitoring and proposed native-compaction default. Chunk 04 adds the cycle adapter after #275's code: the Linux matrix passed with Codex CLI 0.162.0 ([native-evidence.md](native-evidence.md)); on macOS, X0, X1 and X3 passed with 0.162.1 and the approval case X2 is open, so macOS stays unverified. |
| Antigravity | No admitted context source | Show context-source unavailable; no cycle support claim. |
| OpenCode | No admitted context source | Show context-source unavailable; no cycle support claim. |
| Grok | No admitted context source; leader support separately tracked by #329 | Show context-source unavailable; no cycle support claim. |
| DeepSeek | No admitted context source; live wake proven under #199 | Show the missing source/proof; command identity continues to guard its existing tools. |

Native cases run in dedicated temporary repositories and private tmux sessions with the
installed family's normal permissions. Never reset an ongoing work session for a test.
The test report states CLI version, OS, fixture and result. Preserve captures privately;
public evidence contains synthetic labels and the observations needed to judge the result.
Service-manager tests remain in guarded disposable CI; native model tests are separate.

## Reserved system sender — D3 and D4

The handoff request comes from reserved system identity `overwatch`, with its own sender
family/key and typed cycle/Enable-revision metadata. It has system origin, never maintainer
origin. Test native registration, aliasing and participant holding cannot claim it; peer
text and model arguments cannot forge its typed request. Its guide instruction identifies
Enable as the authorization for this cycle, rather than inventing a human chat post.
Existing name collisions preserve state and report a conflict; migration cannot silently
rename or take over another record. Future agentic capabilities are outside this deliverable.

## Reviewed interaction tests — D6, D9 and D11

- D6: race a Grok-style pane-typing wake with clear and pickup under the shared host/pane
  serializer. During ready through waiting_pickup, no notice text or Enter interleaves;
  its inbox/wake record stays pending. Scheduling resumes after the cycle ends.
- D9: both installed-skill and missing-skill native evidence show the cleared model read
  the installed guide's Managed cycle reporting section, queried its own cycle and issued
  the pickup report. Prove the guide reference survives the missing-skill bootstrap.
- D11: race retire, purge and participant-holder choice against every active stage. End
  Enable and cancel when all entered effects are reconciled; an unreceipted input makes
  the outcome uncertain. Preserve terminal evidence/debt before purge removes its target.
  The existing dashboard inbox clear only acknowledges that inbox: it neither cancels a
  cycle nor supplies readiness. Its cleared request remains recoverable through the cycle
  query. An unrelated native clear/epoch change ends the pinned cycle separately.

## Publication and review

Each implementation chunk uses the repository's check and ship facts: signed human DCO,
latest develop, whole vet and race suite with terminal exit 0, whitespace/privacy review,
four CGO-free builds and native-tagged compile/vet, independent exact-commit peer and
refusal verdicts, required exact-head CI, one open PR per target and squash auto-merge.
One frozen findings batch is followed by bounded accepted fixes. A plan or queue receipt is
not evidence for D8 or D9. Close #196 only after its available adapters satisfy this matrix.
