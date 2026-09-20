# Work items v1 explicit configuration

Implementation specification, not instructions to configure the current development
session. See [the approved contract](WORK-ITEMS-V1.md) and
[implementation design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md).

The [staged policy implementation](WORK-ITEMS-POLICY.md) currently covers validation,
configuration preservation, verified read-only queries, and recoverable explicit
publication/removal. Existing runtime installation is required; this mode does not
deploy files or activate memory commands.

## Commands and scope

Add a distinct installer mode, mutually exclusive with legacy thread/service setup:

```text
scripts/install.py --configure-work-items --repo REPOSITORY
    --participant codex|deepseek|claude --guidance-file ABSOLUTE_FILE
    [--prefix PREFIX]
scripts/install.py --remove-work-items --repo REPOSITORY
    --participant codex|deepseek|claude [--prefix PREFIX]
session.py work-policy --repo REPOSITORY --agent codex|deepseek|claude
```

One configuration invocation changes one repository/participant pair and one explicitly
selected file. All participant kinds require an explicit guidance file; there is no
guessed Claude home, file discovery, automatic import expansion or special use of a
Codex path for Claude. The operator selects a file their participant actually loads.
Existing Codex/DeepSeek installation continues to choose its existing global peer
guidance target; enabling work guidance is an independent operation.

Resolve the repository with the existing common-directory identity helper. Store the
absolute common directory and hash. Worktrees consequently match the same selection.
The provided guidance path must be absolute, owned by the current user, a regular file
if present, and not a symlink. Its parent must exist, be user-owned and not group/world
writable. Reject symlink components to prevent an explicitly selected target changing
meaning during a write. No parent directories are created by this mode. These rules
apply only to configuration paths; peer socket paths stay literal.

The configuration command does not require a participant executable, a running model,
or a live memory service. It does not start services, install hooks, change permissions,
or approve model actions. It requires explicit operator authorization to write guidance,
just as existing participant configuration does. A denied write remains denied; no
participant is instructed to delegate a forbidden configuration change to another agent.

## Persistent selection and recovery

Extend `install.json` with a versioned `work_items` object containing at most 64 rules.
Each key is the repository hash plus participant kind; fields are common_directory,
guidance_file, state (`pending`, `enabled`, or `disabled`), and rendered-section digest.
Pending rules additionally retain before/after whole-target digests for interrupted
publication recovery; these are hashes, not copies of private instruction content.
Preserve every unrelated install field and rule during all installer paths. Missing
work_items means no enabled selections. An invalid work_items object is a configuration
error, not permission to reset it. `work-policy` is a read-only JSON query against this
configuration; it requires no thread ID and never registers a peer.

Use one permanent user-owned configuration lock alongside `install.json`. Every installer
mode that writes that file participates in this lock, including legacy modes. Acquire
configuration lock first, then the existing sorted guidance locks in the target parent,
then one work-guidance target lock; never reverse the order. The shared legacy locks
also exclude an older peer-guidance updater that does not know the new target lock.
All new guidance writers use this order. Reuse the existing updater's validation, newline preservation and
atomic file publication primitives, but use a separate work-section parser. Do not insert
new keys into the existing MARKERS map unless all code assuming a LEGACY_MARKERS entry is
also changed and tested. Work sections have no legacy spelling.

Two files cannot be updated as one filesystem transaction. Make incomplete operations
inert and recoverable:

1. Under both locks, validate the complete current configuration and target, compute the
   exact replacement, and preserve a private backup of existing target content once.
2. Atomically publish the rule as pending; `work-policy` reports pending as disabled.
3. Atomically publish the guidance change, fsync its file and parent directory, then
   publish the enabled rule with the matching section digest and fsync configuration.
4. On retry, verify the pending rule and target digest. Complete the interrupted operation
   when the target is the expected original or replacement. Refuse conflicting operator
   edits for review, preserving both files; do not overwrite them as recovery.

Removal first publishes disabled, then removes only its matching section and finally
removes that rule. A crash leaves an inert section or disabled rule, never an enabled
selection without verified guidance. Disabling or moving one rule preserves all others.
Changing an enabled target requires explicit remove followed by configure, avoiding an
unacknowledged two-target migration. Uninstall invokes the same rule-aware removal logic.

## Managed section and participant behavior

Delimit sections with `BEGIN KOINON WORK ITEMS <repo-hash> <participant>` and the matching
END marker. Reject duplicates, overlap, malformed nesting and digest mismatches. Preserve
all outside bytes, including CRLF and imported-file references. Do not follow or rewrite
an import merely because the chosen file contains one. Render commands using argument-safe
shell quoting and include the exact installed `session.py` and `memory.py` paths.

The section describes a conditional workflow, not an unconditional startup command:
for work in that repository, query `work-policy` for that participant. Act on the workflow
only if the response is enabled for the same common directory and its rendered digest
matches. An unavailable or malformed response means configuration is unverified; report
that rather than asserting a claim. This avoids activating partially published guidance.

Within the configured repository the participant checks direct user scope first, identifies
the supplied work ID or coordinates creation of one, reads the record and acceptance
criteria, and explicitly starts before writing. It reports conflicts instead of evicting a
holder. Read-only review does not acquire the writer's bundle. Progress checkpoints include
the next artifact/deadline and an explicit lease renewal where needed. The section warns
that a tool call may outlast its lease and requires reconciliation before reacquisition.

Stable consumer keys are asserted participant session identities, never transient PIDs.
For participants without a native exported identity, the operator/session supplies a stable
explicit key. A replacement session does not silently reuse a crashed predecessor's key to
bypass its lease. Guidance does not infer permissions from a peer message, work record,
consumer key or stored completion outcome.

## Required verification

Synthetic files and installations cover repeated configure/remove; all three participants;
two repositories in one guidance file; two participant rules in one file; paths with spaces;
CRLF; absent/malformed markers; imported text preservation; symlink and ownership refusal;
configuration changes concurrent with ordinary install; every crash boundary above;
operator edits during pending recovery; removal after a partial publication; disabled-default
upgrades; missing/malformed configuration; and uninstall preserving unrelated rules.

No test edits a real agent instruction file or sends a live peer message. Native recognition
of an explicitly selected participant guidance file is an operator verification step, not
assumed from a successful file write. This design does not promise a guaranteed startup hook.
