# Staged work configuration and policy query

This part provides configuration validation, serialized installer updates, and the
read-only policy query. Public memory startup remains schema 4. Work guidance
publication, configure/remove commands, and runtime activation are later parts.
Existing participant installation continues to manage only its existing peer guidance.
No work-item rule is created or enabled by an ordinary install.

Run the installed `session.py work-policy --repo REPOSITORY --agent PARTICIPANT`,
where PARTICIPANT is `codex`, `deepseek`, or `claude`. This action requires an explicit
participant, but no thread identity, participant executable, running memory service,
or peer registration. Other session actions still support only Codex and DeepSeek.
The repository uses the same resolved absolute Git common directory and 16-character
SHA-256 prefix as memory; worktrees therefore share one selection.

The JSON reply contains `version`, `repo`, `common_directory`, `participant`, `state`,
`enabled`, `digest`, and `guidance_file`. Missing selections return disabled with null
digest and guidance file. Both pending and disabled stored rules report `state: disabled`
and `enabled: false`. An enabled rule returns its recorded section digest. Consumers
must also check the repository identity and that digest against the managed section;
this query does not parse guidance or turn configuration into permission to act.
Malformed configuration or unsafe enabled guidance paths return a configuration
error, rather than an empty selection. No policy query creates a configuration lock,
state directory, registry entry, or guidance file.

`install.json` may contain `work_items: {"version": 1, "rules": {...}}`, with at most
64 rules keyed by `<repository-hash>:<participant>`. Every rule has exactly
`common_directory`, `guidance_file`, `state`, and `digest`. Pending rules additionally
have `before_digest` and `after_digest`; digests are lowercase 64-character SHA-256
hex strings, never copies of instruction content. Paths are bounded absolute paths.
The selected guidance path must have no symlink components, an existing user-owned
parent not writable by group or others, and a user-owned regular target if present.
Filesystem checks apply only to enabled selections; pending/disabled rules remain
disabled even when their old guidance path has disappeared.
These configuration checks do not change literal peer socket addressing.

All installer modes perform non-writing validation, then acquire the permanent
user-owned `.install.lock` beside `install.json` and revalidate fresh configuration.
The configuration lock precedes existing guidance locks. A monotonic 30-second
wait bounds contention; a live holder that does not release it produces
`configuration_busy` with retryable exit status 75 and the lock path. Check the
other installer and retry; never delete a held lock. Configuration writes merge
only the selected installation fields, preserving all unknown fields and all work
rules, and publish atomically with file and directory fsync. A refused validation
never resets existing configuration. The lock inode is not removed or replaced.
Read-only consumers need no lock because they see one atomically published file.

The shared writer is a staging primitive for the later two-file guidance publication
protocol. It is not an operator-facing command to enable rules by editing JSON. Native
recognition of a participant's explicitly chosen guidance file remains an operator
verification step when that later configuration mode is available.
