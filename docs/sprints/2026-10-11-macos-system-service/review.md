# Sprint 2026-10-11 — macOS system service — Review

Review of the committed plan by the other agent family, with the disposition of each finding.

## Round 1 (Codex, plan b0dbf8d)

| # | Finding | Disposition |
| --- | --- | --- |
| 1 | Medium. Chunk 01 made every service's lock loser wait forever, which changes the default services beyond the decision's "unchanged except log paths", while #283 requires the loser to exit without a tight restart loop. | Fixed. `--lock-wait 60s`: the loser waits up to 60 seconds, then logs `lock_held` and exits 0. The agent and systemd do not restart a clean exit; the LaunchDaemon retries about every 70 seconds. The decision's constraint now names this default-service change as required by criteria 2 and 3. |
| 2 | High. Chunk 02 started the system service before stopping the GUI agent, used GUI-domain commands that fail with error 125 over SSH, had no rollback, and presence-based detection would then skip the agent forever. Status from the old agent could pass as success. | Fixed. Every launchctl step runs through `sudo` (works over SSH). Order: disable and boot out the agent, verify with `print-disabled` and `print`, then install, bootstrap and kickstart the system service, then verify that the answering daemon's PID is the system service's. Failures at each step roll back to the enabled agent; the system plist exists only after success. Every later install reconciles the agent again. Tests inject a failure at each step and error 125 for plain GUI calls. |
| 3 | Medium. A hand-written plist naming a different executable passed the checks, so the old executable restarted and the upgrade falsely succeeded. | Fixed. Its `ProgramArguments` must equal the agent's (binary link of this prefix, `serve`, `--state-dir`, same state); otherwise a fixed error names the differing field and nothing changes. Tested. |

## Round 2 (Codex, plan 2f13df0, bounded to finding 2)

| # | Finding | Disposition |
| --- | --- | --- |
| 2a | Item 1 said the system plist is written only after success, but step 4 writes it before verification; detection must handle the partial state, including interruption. | Fixed. The plist records intent, not readiness. Every install verifies readiness (step 5) and reports success only then; an interrupted transition completes through reconciliation on the next run. |
| 2b | The rollback after steps 2 or 3 only re-enabled the agent, but bootout had already unloaded it; it must restore the original loaded/disabled state and never create an agent. | Fixed. Step 1 records the agent's loaded and disabled state; rollback restores exactly that (enable only if it was enabled, bootstrap only if it was loaded). The agent plist stays until step 6. |
| 2c | Root `launchctl` on `gui/<uid>` from a Background session is unverified. | Fixed. Stated as a proof obligation: the native CI case is built first, and a failure returns the plan to gate B. |
