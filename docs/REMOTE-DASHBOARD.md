# Private remote dashboard

Use the existing dashboard from a phone away from home through Tailscale Serve. The
phone and host join the same private Tailscale network; the browser signs in with
the existing maintainer secret. The host must stay awake, online, and running
Koinon and Tailscale. This feature does not wake a sleeping host.

The optional LAN HTTPS dashboard configured by `koinon install
--dashboard-address HOST:PORT` remains separate. Neither dashboard listener
serves the agent API. See [ADR 002](architecture/adr/002-private-remote-dashboard-transport.md)
and [PDR 010](product/pdr/010-private-remote-dashboard-access.md).

## Set up from the dashboard

Open the dashboard for the Koinon computer you want to reach. Select **Go remote**
from the header, or open **Settings → Remote access**. The section uses the
existing dashboard layout, sign-in, and controls.

1. Select **Check my setup**. The section shows the computer listener, Tailscale
   connection, and Serve mapping separately. If Tailscale is missing, use **Get
   Tailscale** to install it on the computer and phone, then sign in with the same
   account on both. If the maintainer secret is missing, set it on the computer
   with `koinon maintainer secret set`.
2. If **Open Tailscale approval** appears, follow it to the account page in a
   separate tab. Complete the required HTTPS or Serve account step, return to
   Koinon, and select **Check again**. Where the installed Tailscale version
   cannot provide a setup link, the section links to the official HTTPS
   instructions and reports the next step.
3. Select **Enable remote access** when setup is ready. Returning from an account
   page, refreshing, or reconnecting never enables access automatically. Enable
   uses the same persistent listener and owned mapping as `koinon remote enable`.
4. Connect Tailscale on the phone. Copy the dashboard link or scan its QR code,
   then sign in with the existing maintainer secret. The QR contains only the
   URL, without a password or login token. Keep the link for later visits.
5. Complete the cellular check described below. The section always labels phone
   reachability **Unverified**; a ready host does not prove that the phone can
   reach it.

**Turn off remote access** opens a confirmation section before the button that
stops the listener and removes its owned mapping. If you do this from the remote
page, the connection can close before the browser receives confirmation. Use the
local dashboard or `koinon remote status` to check the result before trying again.
The local dashboard stays available.

The setup check can ask Tailscale's control plane for feature instructions; it
neither approves the account step nor changes a Serve mapping. Normal status
refreshes do not repeat that account query. Setup instructions and enable
eligibility stay at the last explicit check, labeled separately from refreshed
host status. Select **Check again** after the account step. If a read fails, the section labels
displayed status as potentially stale. The page retains an open confirmation
section or form while its action is running.

These controls require the daemon version that includes them. Before upgrading a
shared service, inspect `koinon install --plan` and coordinate its current users.
The command-line workflow remains available.

## Set up from the command line

1. Install Tailscale on the host and phone using the [platform instructions](https://tailscale.com/download).
   Connect both to the intended private network. Restrict the network's access
   policy to the intended maintainer devices, especially in a network with other people.
2. Set a maintainer secret on the host, if one is not already set:

   ```sh
   koinon maintainer secret set
   ```

   The command reads it without putting it in arguments. `koinon maintainer secret
   generate` is the existing alternative; keep its one-time output private.
3. Enable [HTTPS certificates](https://tailscale.com/docs/how-to/set-up-https-certificates)
   in the Tailscale admin console if needed. Serve needs that account permission
   and the host's `.ts.net` name. Then run:

   ```sh
   koinon remote enable
   ```

   Follow any reported account or operating-system step, then repeat enable.
   Repeating the command preserves a working setup. On macOS the CLI bundled at
   `/Applications/Tailscale.app/Contents/MacOS/Tailscale` is also found when
   `tailscale` is absent from `PATH`.
4. Open the printed URL on the phone, or scan its QR code, and enter the maintainer
   secret. Remote login reuses the existing global maintainer-secret backoff, so
   failed guesses on any login surface share that wait. The QR contains only the
   HTTPS dashboard URL. Bookmark it or add it to
   the home screen.
5. Turn phone Wi-Fi off. Read agents, send a message, and submit a decision verdict.
   Verify receipt and the recorded verdict on the host. Host readiness is distinct
   from this cellular check; Koinon reports phone access as unverified.

```sh
koinon remote status
koinon remote status --json
koinon remote disable
```

Each command accepts `--state-dir DIR` and `--address 127.0.0.1:PORT` for a selected
local daemon. `--json` prints machine-readable status without a QR. Status reports
the configured URL, local listener, observed Tailscale state, and next setup step.
When remote access is disabled, the retained link is labeled as a saved URL; it
does not indicate an active listener.
A missing or old daemon needs installation of this version before these commands
can work; inspect `koinon install --plan` before changing a shared service.

## URL, restarts, and configuration ownership

Serve provides `https://MACHINE.TAILNET.ts.net/dashboard/` on HTTPS port 443.
This adapter takes the native `Self.DNSName` reported by Tailscale and validates
its `.ts.net` name, following the official [MagicDNS naming](https://tailscale.com/docs/features/magicdns#fully-qualified-domain-names-vs-machine-names)
and [HTTPS certificate](https://tailscale.com/docs/how-to/set-up-https-certificates)
contracts. That scope describes this external Serve adapter; it does not decide
whether a future transport can support custom domains.
The URL stays stable while the host's Tailscale machine identity and hostname stay
the same. After a rename or identity change, disable the old configuration, enable
again, and use the URL from status. If Serve retains the old-name mapping, use
[name-change recovery](#name-change-recovery) first. That process ends old remote sessions.

The private state directory holds the owner-checked, mode-0600
`remote-dashboard.json`. It records the selected loopback backend port, origin,
enabled state, and ownership intent. The daemon restores the same listener after
restart, and retries Serve restoration while its dependency starts. On orderly
shutdown, the daemon removes its exact owned mapping before releasing the backend
port; the enabled ownership record stays, so the next start recreates the same
URL and port. If that cleanup fails, shutdown reports the error and retains the
record. A saved port conflict is reported instead of silently selecting another
port; restoration removes the owned forwarding so it does not knowingly point to
the occupying service. Upgrade retains this state. Logins end on daemon restart,
so sign in again after upgrade or reboot.

An abrupt process kill, or unavailable Tailscale during shutdown cleanup, can
leave the mapping pointing at a released loopback port until reconciliation.
This feature follows ADR 001's same-user local trust boundary; it does not defend
against another malicious process running as the same user. Keep that practical
limit separate from the tested browser and agent-API isolation.

Reconciliation runs on a 30-second cadence, and status also inspects the mapping.
An observed Tailscale identity change or Serve/Funnel conflict closes the remote
surface and ends its logins. Resolve the reported conflict before enabling again.

Enable checks existing Serve configuration before recording a new mapping. It
refuses a conflicting root, foreground configuration, TCP service, or Funnel
configuration on the required port. An existing matching mapping without Koinon's
ownership record is not adopted. Other mounts and ports stay in Tailscale's
configuration; Koinon never runs `tailscale serve reset`.

Disable records the disabled state first, stops only the remote listener, and ends
its sessions. It removes only the exact root mapping recorded as Koinon's own,
using explicit `--set-path=/`; a changed mapping is reported and preserved. If
cleanup fails, the ownership record remains so a repeat or restart can finish it.
Disable keeps local and LAN dashboard access, and does not log the host out of
Tailscale. Uninstall cleans up the owned mapping after stopping the daemon and
retains the journal on a cleanup failure. If Tailscale is missing during uninstall,
the disabled ownership journal and Koinon binary stay. Install Tailscale again,
then repeat the same `koinon uninstall` command to finish; no journal editing is
needed.

## Name-change recovery

Tailscale's targeted Serve CLI addresses the current machine name. If a saved
old-name root still exists, Koinon stops its listener and retains ownership rather
than risk removing a different current-name mapping. The error names the saved
origin and gives the old machine label. No Serve reset or journal edit is needed.

On the original tailnet, temporarily restore that machine name, wait until
`tailscale status --json` reports the saved DNS name, and finish cleanup:

```sh
tailscale set --hostname=OLD_MACHINE_NAME
koinon remote disable
tailscale set --hostname=DESIRED_MACHINE_NAME
koinon remote enable
```

On macOS without a `tailscale` command in PATH, use
`/Applications/Tailscale.app/Contents/MacOS/Tailscale` for those two commands.
[Machine-name changes](https://tailscale.com/docs/concepts/machine-names) can also
use the Tailscale admin console. If the old name or tailnet cannot be restored,
the network owner must resolve that account/name step before Koinon can verify
old-mapping removal. Keep the disabled ownership record for that cleanup. Actual
old-key retention and this rename path still need the live platform check.

## Phone behavior

All existing views and actions use the same daemon state and action handlers:
agents and session controls, messages and delivery, rooms, decisions and history,
launch, work, memory, audit, settings, and health. Registration alone does not
mean an agent is busy; unknown observations stay unknown.

The page shows its host, connection state, and last successful refresh. Visible
pages read every five seconds and refresh on foregrounding or reconnecting. Data
is labeled stale after 15 seconds without a successful displayed-data refresh.
An open form or unsent field holds content replacement, preserving the current
screen and draft. A successful connectivity read while content is held does not
make the displayed data current. Drafts live only in page memory; a browser reload
or discarded tab can lose them. Private pages are not cached for offline use.

A submitted form disables repeated submission while it runs. If its response is
lost, the page says **Result unknown; check the latest state before trying
again.** Read the latest message, decision, room, or session before deciding to
retry. Reconnect and login never replay a write. Existing revision checks and
operation keys keep their existing meaning; this feature does not promise
exactly-once delivery for every action. An expired login offers a safe relative
return screen after sign-in, without submitting the prior action.

## Diagnostics

| Observation | Next step |
| --- | --- |
| Tailscale is missing | Install it on the host and phone; connect both to the same private network. |
| Host is stopped, signed out, or offline | Open Tailscale and connect or complete its account prompt. Repeat enable. |
| No usable `.ts.net` name | Check MagicDNS and the host's name in the Tailscale admin console. |
| HTTPS is unavailable | Enable HTTPS certificates in the admin console using the linked instructions. |
| Serve permission is denied | Complete the account permission; on Linux check the Tailscale operator setting. Repeat enable. |
| Required root or port is already used | Inspect `tailscale serve status --json`; resolve the reported conflict explicitly. Koinon preserves it. |
| Saved loopback port is occupied | Free the saved port and repeat enable, or disable successfully then enable to choose an available backend port. The daemon's local API remains available. |
| Network policy denies the phone | Check the phone's network/account and the tailnet access policy for this host's HTTPS port. Host status cannot prove phone permission. |
| Host is configured but phone fails | Check host wake state, both Tailscale connections, network policy, and the URL from status. Complete the cellular test separately. |

The [verification record](REMOTE-DASHBOARD-VERIFICATION.md) separates automated,
source, real proxy, platform, and physical-device evidence.
