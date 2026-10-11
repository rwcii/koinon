# Chunk 03 — Recovery and re-registration

Criteria 4, 5 and the live part of 8 of `decision.md`. Ships after chunk 02 (one pull
request at a time; its live checks need system mode).

## Current state

- `koinon status` and `koinon recover` call the daemon over loopback HTTP
  (`/v1/status`, `/v1/storage/recover`); neither uses launchctl. When the daemon is down, both
  fail with a connection error and say nothing about the service.
- `koinon mcp` (`internal/mcp/tools.go`): a tool call that gets `caller_inactive` registers
  again and retries once. The renewal timer keeps a session through transient daemon
  failures, but drops it from renewal when a renewal is refused because the session expired
  or retired. An idle agent whose registration expired during an outage therefore stays
  unregistered until its next tool call, and messages to it are refused as
  `recipient_inactive`.

## Change

1. **Recover when the daemon is down.** When `status` or `recover` cannot connect, the error
   report adds `service` (`system`, `agent`, `systemd` or `manual`) and `restart_command`:
   `sudo launchctl kickstart -k system/<label>` in system mode;
   `launchctl kickstart -k gui/<uid>/<label>` for the agent, with the note that it needs the
   desktop session; `systemctl --user restart koinon.service` on Linux. Koinon prints the
   command and never runs `sudo` itself. When the daemon answers, `recover` is unchanged.
2. **Re-registration on renewal.** A renewal refused because the session expired (not retired,
   not fenced, not `stale_holder`) registers again at once with the same native identity and
   launch ID. Registration takes back the participant address under the existing rules: when no
   active session holds it. A retired or fenced session is still dropped, as today.
3. **Docs.** `docs/INSTALL.md` (state, recovery and compatibility): what `recover` does
   (blocked-storage recovery when the daemon answers; the restart command when it does not),
   `koinon status` over SSH, and the re-registration after an outage. PROTOCOL.md, MCP
   server section: the renewal rule. CHANGELOG.

## Done

The chunk's part of `definition-of-done.md`: `recover` output per mode with no `sudo` run;
the synthetic outage test for the tool-call and renewal paths, with the address taken back
when unheld and not when held. Then the maintainer's live checks 1 to 5; #283 closes after
they pass.
