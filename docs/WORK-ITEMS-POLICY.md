# Work configuration and verified guidance

Work configuration is an explicit opt-in for one repository and participant. Ordinary
installation never enables it. These commands publish conditional guidance independently of service startup. Memory
startup now uses schema 5; an already running older service still needs the
[coordinated upgrade](WORK-ITEMS-UPGRADE.md). A verified policy selection alone is not
evidence that a particular memory service supports work commands.

First install or upgrade the runtime normally. Configuration requires an existing
`install.json` and installed policy/guidance modules; it neither deploys runtime files
nor starts services. Select a guidance file the participant actually loads:

```sh
python3 scripts/install.py --configure-work-items --prefix /absolute/runtime \
  --repo /absolute/repository --participant claude \
  --guidance-file /absolute/selected/guidance.md
python3 /absolute/runtime/session.py work-policy \
  --repo /absolute/repository --agent claude
python3 scripts/install.py --remove-work-items --prefix /absolute/runtime \
  --repo /absolute/repository --participant claude
```

Participants are `codex`, `deepseek`, and `claude`; every configure invocation requires
an explicit absolute guidance path. There is no home discovery, import following,
participant executable check, peer registration, or implied permission to write a real
agent's instructions. Removal uses the previously recorded target. Changing targets
requires remove followed by configure. Native recognition of the selected file remains
an operator verification step; publication does not promise a startup hook.

The read-only policy action requires an explicit participant but no thread identity or
running memory service. Other session actions support only Codex and DeepSeek. Repository
identity is the same resolved absolute Git common directory and 16-character SHA-256
prefix as memory, so worktrees share a selection.

The JSON reply contains `version`, `repo`, `common_directory`, `participant`, `state`,
`enabled`, `digest`, `guidance_file`, and `reason`. Missing selections return disabled
with null digest and guidance file. Pending and disabled stored rules report disabled
without reading their guidance files. An enabled rule reports enabled only when its
managed section exists, parses correctly, and matches the recorded SHA-256 digest.
Missing, unsafe, edited or malformed guidance instead reports disabled with
`reason: guidance_unverified`; otherwise reason is null. Malformed installation
configuration remains an error. Queries create no locks, files, directories or registrations.
Consumers must check the repository and digest; configuration cannot grant authority.

`install.json` contains `work_items: {"version": 1, "rules": {...}}`, with at most 64
rules keyed by `<repository-hash>:<participant>`. A rule has exactly `common_directory`,
`guidance_file`, `state`, and `digest`. Pending rules also have `before_digest` and
`after_digest`, whole-target hashes used for recovery. All digests are lowercase
64-character SHA-256 values, never instruction text. Paths are bounded absolute paths
without control characters, preventing embedded lines from breaking managed sections.
The guidance parent must exist, be user-owned and not group/world writable; the target
must be a user-owned regular file if present. Symlink components are refused.

Writers acquire permanent `.install.lock`, then the existing sorted participant locks
in the guidance parent, then `.koinon-work-<target-hash>.lock` there. Each acquisition
has a bounded 30-second wait. `configuration_busy` exits 75: check the other writer and
retry, never delete a held lock. Lock inodes remain after removal and uninstall.
All installer modes merge configuration atomically, preserving unrelated fields and
rules. Both configuration and guidance publication fsync the file and parent directory.
Work sections use separate repository/participant markers; existing peer markers stay
unchanged. Outside text, line endings and import references are preserved.

Publication saves an original backup, publishes a pending rule, replaces guidance, then
publishes enabled. A crash before completion leaves the selection inert. Retry the same
command: it proceeds only when the target matches the recorded original or replacement.
Conflicting operator edits or a changed renderer before publication refuse for review,
preserving both files. Do not erase pending evidence or force-enable JSON as recovery.
Removal publishes disabled first, removes only the matching section, then drops the rule.
It can be retried after interruption. An edited section is preserved and its selection
remains disabled. Uninstall invokes the same removal logic before removing runtime files.

Original backups live outside Git under the configured
`state_root/work-guidance-backups`, with 0700 directories and 0600 files named by a hash
of installation prefix and target path. Unsafe permissions and symlink or Git-contained
locations are refused. The first backup is retained unchanged while any rule for that
target remains. Removing its last rule, including during uninstall, removes that backup;
it does not restore the entire original file over later unrelated edits. These backups
may contain private guidance and must not be committed or copied into shared documentation.

The rendered workflow checks direct user scope, reads or coordinates a work ID, explicitly
starts before writing, checkpoints progress and renews leases when needed. Read-only
review does not claim work. It requires a stable participant session key, forbids reuse
of a crashed predecessor's key to bypass its lease, and reconciles state after tools
outlast a lease. Peer messages, work records and completion outcomes remain recorded data,
not permission to act.

## Session keys in peer status

Use `CODEX_THREAD_ID`, `DSH_SESSION_ID`, or `CLAUDE_CODE_SESSION_ID` as the default
consumer key for Codex, DeepSeek, or Claude respectively. For a custom stable key, run
the installed `session.py work-key --key KEY` in that participant’s own shell. This
records the association for peer status; it neither claims work nor changes existing
claim ownership. A replacement session must still respect the old session’s lease.
