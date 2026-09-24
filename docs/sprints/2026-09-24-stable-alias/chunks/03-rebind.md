# Chunk 03 — rebind and retirement

Read `decision.md` and `definition-of-done.md` first. Criteria 3, 4, 5, and the rebind and
retirement parts of 8. Depends on chunk 02.

## Outcome

`session.py rebind --predecessor <old thread>` moves the alias from the predecessor to this
Codex thread and stops the predecessor, on verified same-pane evidence or on the user's
direct authorization. The `reconnect` guide topic and the `pickup` skill use it in place of
`stop_predecessor`.

## Specification

- **Command.** `session.py rebind --predecessor OLD [--user-authorized]`, for the current
  thread from `CODEX_THREAD_ID`, like `ensure`. It needs approval (runs outside the sandbox).
  The current thread must be registered and running (run `ensure` first).
- **Checks, all required, else refuse with a typed code and stop nothing:**
  - OLD is a saved Codex registration of the same repository (`same_repository`) and not this
    thread (`same_thread`);
  - without `--user-authorized`: both registrations have an `observed` `terminal.json` from
    chunk 01, with equal `socket` and `pane_id` (`terminal_mismatch` or
    `terminal_not_recorded`), and this thread's record is fresh from the `ensure` just run.
    A matching `host.json` is reported and never sufficient alone (`host_only`).
  - `--user-authorized` is the agent's statement that the user named this predecessor; the
    command records it in the result and skips the terminal match only.
- **Sequence.** Each step is one read-decide-write of the lease under `names.lock`, and the lock
  is released before any stop, start or wait (chunk 02, lock boundary):
  1. Check the lease: `holder = OLD` in `held`, or already `moving` from OLD to this key. Write
     `state = moving, from = OLD, to = this key, operation = this command`. From now on no
     notifier publishes the alias, and chunk 02's take refuses (`alias_busy`) while
     `operation` is live.
  2. Stop OLD with the existing `stop` path (inbox, checkpoint and claims stay).
  3. Under the lock, confirm that OLD has no live record carrying the alias; write
     `holder = this key, state = publishing, operation = this command`.
  4. Restart this thread's service (the existing stop and start paths) and wait until its
     live record publishes the alias.
  5. Under the lock, write `state = held, operation = null`.
  Report the number of records in OLD's inbox store, counted without reading any body.
- **Predecessor not the holder.** When the lease does not name OLD (another key holds the
  alias, or none does), steps 1 and 3–5 do not apply: stop OLD (step 2), then take the alias
  only under chunk 02's take rule, and report which rule applied.
- **Recovery.** A transient state whose `operation` is dead is completed, never taken over:
  `moving` by the next `rebind` or `ensure` of `to` (from step 2), `publishing` by the next
  `ensure` of `holder` (chunk 02, publication on a running service). `from` cancels a `moving`
  back to `held` only while `from` still has a live record and `operation` is dead. A third key
  follows chunk 02's take rule.
- **Already stopped.** OLD not running: skip the stop, report `already_stopped`, move the lease.
- **Guide.** In `koinon/guidance.py`, the Codex `reconnect` view replaces `stop_predecessor`
  with a `rebind` recipe (`needs_approval`) and states the rule of criteria 3 and 4. DeepSeek
  keeps `stop_predecessor`. The guidance revision changes; the notifiers already announce it.
- **Skills.** `agents/skills/pickup/SKILL.md` step 4 runs the guide's `rebind` recipe for a Codex
  predecessor in the same pane, and reports the predecessor and the recipe in every other case.
  `agents/skills/peer-tmux/SKILL.md` refers to the alias for a Codex peer. Neither repeats a
  `session.py` recipe.

## Tests

The rebind and no-inheritance tests of `definition-of-done.md`, and the edge cases for an
already stopped predecessor, an interrupted rebind (kill the command after each of steps 1–5),
and a third thread's `ensure` held at a barrier between each pair of steps.

## Live check 2

After merge and an authorized runtime upgrade: live check 2 of `definition-of-done.md`.

## Documentation

`PROTOCOL.md` (rebind and retirement rule), `docs/INSTALL.md` (after a Codex `/clear`),
`CHANGELOG.md`.
