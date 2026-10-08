# Chunk 02 — alias

Read `decision.md` and `definition-of-done.md` first. Criteria 1, 2, and the alias parts of 7
and 8. Depends on chunk 01.

## Outcome

A Codex registration takes the stable alias of its repository when no live registration holds
it. The holder's registry record publishes the alias as its `name`, so Claude sessions and
`bridge.py send` reach it by that name. At every point at most one live record carries the
alias.

## Specification

### Alias name

- One state root and one `names.lock` serve every repository, so the alias is reserved per
  repository, not derived afresh. The repository digest is the SHA-256 hex digest of
  `repository_identity.repo_common_directory()` of the registration's repository (the
  absolute Git common directory); `repo_identity()` is its first 16 digits.
- **Reservation**, under `<state_root>/names.lock`, the first time a Codex registration of a
  repository needs an alias: the candidate is `codex-<label>` (the per-thread name without its
  final `-<two hex>` suffix, as `session.py` `details()` builds it). When the candidate is
  reserved for another repository, or equals any saved per-thread name or live registry name,
  probe `codex-<label>-<first N hex of the repository digest>` for N = 4, 6, 8, … 64 and take
  the first free name. At least four hex digits never match a per-thread name, which ends in
  two. Distinct repositories have distinct full digests, so the probe ends with a free name;
  only a non-Koinon registry record that carries the 64-digit name refuses, with
  `alias_unavailable`.
- The reservation is permanent for that repository and is stored in its lease (below). Every
  later registration of the repository uses the stored alias.
- In `save_registration()`, the occupied set also holds every reserved alias, so a new
  per-thread name never equals an alias.
- Codex only; DeepSeek and Claude get none.

### Lease

`<state_root>/aliases/<alias>.json`, private, written with `durable_state.publish` under
`names.lock`: `{alias, repository, holder, state, from, to, operation, changed_at_ms}`.

- `holder` is a session key or null. `state` is `held`, `publishing` or `moving`. `from` and
  `to` are set only in `moving`. `operation` is `{pid, proc_start}` of the command that set a
  transient state (`publishing`, `moving`), else null.
- The lease is the only source of the holder. It carries no inbox, checkpoint, claim or thread
  ID.
- **Lock boundary.** `names.lock` is held only for a read-decide-write of the lease, or for a
  notifier's read of the lease plus the creation of its registry record. It is never held while
  a service starts or stops, or while a command waits for readiness.

### The one-holder rule

A notifier publishes the alias only when, at its start and under `names.lock`, the lease names
its own key as `holder` in state `held` or `publishing`. In every other case it publishes its
per-thread name. It never rewrites its record in place. Therefore:

- the lease names at most one publishable key at any time;
- the lease leaves a key (take or move) only when that key has no live record carrying the
  alias, checked under `names.lock`;
- a notifier that starts later reads the new lease and cannot publish the alias.

A live record is one that `bridge.peers()` reports for the key's bridge, with the alias as
`name`.

### Take at `ensure`

Under `names.lock`, before the service starts, a Codex `ensure` for key K:

- lease absent: write `holder = K, state = publishing, operation = this command`;
- `holder = K`: continue;
- `state = held`, another holder with no live record and a stopped lifecycle
  (`session_observation.lifecycle`): take it as above;
- `state = publishing` or `moving` whose `operation` is live: no take, report
  `alias_busy`;
- a transient state whose `operation` is dead stays reserved for its successor: only `holder`
  (for `publishing`) or `to` (for `moving`, chunk 03) completes it, and `from` may cancel a
  `moving` (chunk 03). A third key K takes it only when that recorded successor (`holder` or
  `to`) has a stopped lifecycle and no live record; K's own state does not count;
- otherwise, a live holder: no take, report `alias_held_by` with the holder's per-thread name.

Before a take from a dead holder, remove its stale record only when all hold: entrypoint
`runtime_names.REGISTRY_ENTRYPOINT`, same owner, pid dead or start time different, and
`bridgeOwner` equal to the `owner` in the holder's `notify-ready.json`. A record with the alias
as `name` that is not Koinon's, or that is live, refuses the take with `alias_occupied` and the
path.

### Publication on a running service

`session_service_manager.ensure()` today returns early for a running service. For a Codex key
that the lease names as `holder` while its live record does not publish the alias (a running
pre-upgrade participant, or a crash before the restart), `ensure` sets `publishing` with this
command as `operation`, restarts the service through the existing stop and start paths, waits
until the live record publishes the alias, then writes `state = held, operation = null`. A
failure leaves `publishing` with a dead `operation`, which the next `ensure` of the holder
completes.

### Notifier

The notifier finds its alias in the lease under `<state_root>/aliases/` whose `repository`
equals its registration's repository, and `<state_root>` from `--state-dir`
(`<state_root>/sessions/<key>`). The service command lines stay unchanged: installed service
definitions are compared byte for byte. Every record
also carries `koinonName` (the per-thread name) and `koinonAlias` (the alias, holder or not).

### Send and reporting

- `bridge.py send` accepts only a `uds:` address today (`peer_transport.target_path`); a
  Koinon sender reads the address from `bridge.py peers`. `send` now also accepts a peer name
  as its target, resolved in the client before the control request, from the live records
  that `bridge.peers()` reports: exactly one record with that `name` sends to its address; none
  fails with `alias_unheld` when the name is a reserved alias, else `peer_not_found`; more than
  one fails with `peer_ambiguous`. A `uds:` target is unchanged. (Plan correction during the
  build: the first version said `send` already resolved names.)
- `bridge.py peers` adds `alias`, `alias_holder` (bool) and `thread_name`.
- `ensure`, `status` and `guide` add `alias` with `name`, `held`, `state`, and when not held,
  the holder's per-thread name or `none`.

## Tests

The alias-allocation, registry-publication, no-holder, running-participant and reporting tests
of `definition-of-done.md`, and the edge cases for a crashed holder and a non-Koinon record.

## Live check 1

After merge and an authorized runtime upgrade: live check 1 of `definition-of-done.md`.

## Documentation

`PROTOCOL.md` (alias, lease, one-holder rule, registry fields), `docs/INSTALL.md` (addressing a
Codex peer by alias), `README.md` if it names peer addressing, `CHANGELOG.md`.
