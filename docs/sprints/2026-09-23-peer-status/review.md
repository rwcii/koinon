# Sprint 2026-09-23 — peer status — Review

The reviewer is the Codex agent; the author is the Claude agent. Each finding is listed with
its disposition.

## Decision (reviewed at c4a8250)

| Finding | Disposition |
| --- | --- |
| Criterion 4 made the same missing terminal event both `busy` and `unknown`. Bridge or notifier liveness is not participant liveness. | Fixed: `busy` and `idle` require the selected participant's own process to be verified live; an unverified or ended process is `unknown`. |
| Criterion 6 did not separate the source's recording time from Koinon's read time, and so conflicted with the 15-second freshness rule in `docs/DELIVERY.md`. | Fixed: each value carries both times; a fresh read keeps an old context value of a live participant; a cached reading expires after 15 seconds or a disconnect. |
| Criterion 7 requires a work title and checkpoint, which are free text that criterion 9 excluded. | Fixed: criterion 9 permits the work title and checkpoint explicitly; transcript, message, prompt and file content stay excluded. |
| Criterion 7 named no memory store, and gave no result for a missing link. | Fixed: the peer is associated with a memory store and a participant session key; a missing association or an unavailable service is `unknown`; a successful empty query is no claimed work. |
| Criterion 10 could be read as making read-only reviewers claim work, against `docs/WORK-ITEMS-POLICY.md`. | Fixed: the building agent holds the item; a read-only reviewer does not claim work. |

Concurrence on the decision at 255b9e7.

## User change after concurrence

The user rejected an opt-in status line: Koinon must not stop reporting Claude context because
a user forgot an installation step, and a custom status line must keep working. Changed in
criteria 2 and 3 and the settings constraint: installation and upgrade set the integration up
by default with an option to decline it; the existing command keeps its input, output and exit
status and runs even when Koinon's part fails; the previous value is restored exactly; a
missing or changed integration reports `statusline_missing` with the repair command.

## User change (reviewed at 5809a23)

| Finding | Disposition |
| --- | --- |
| The upgrade edits user settings without preflight, saved evidence or reporting; a retry could wrap the command twice or replace the saved original; a saved decline must survive upgrade. | Fixed in criterion 3: the original is saved once; repeats never nest or replace it; a decline survives upgrade; the upgrade covers the edit in preflight, evidence and its report. |
| Exact restoration overwrites later user edits; a concurrent Claude Code write could be overwritten; a lock or atomic rename does not coordinate Claude Code. | Fixed in criterion 3: restore only while the entry is still Koinon's command, otherwise keep it and report; compare before and after replacement, a difference is a reported conflict; the remaining race is documented. |
| The tests must cover a failing user command and a failing Koinon part separately, with the input read once. | Fixed in criterion 3. |

## Criterion 3 wording (reviewed at 22a1dd4)

| Finding | Disposition |
| --- | --- |
| "No settings edit silently overwrites a concurrent change" contradicts the acknowledged race: a Claude Code write after the final comparison is replaced and the post-check sees only Koinon's bytes. | Fixed: the criterion claims detection of observable changes only, and the documentation tells the user not to change Claude settings during installation, upgrade or removal. |

Concurrence on the decision at ba2816e. Gate A was approved by the user at ba2816e.
