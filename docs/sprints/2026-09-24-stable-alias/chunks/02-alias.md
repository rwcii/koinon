# Chunk 02 — alias

Read `decision.md` and `definition-of-done.md` first. Criteria 1, 2, and the alias parts of 7
and 8. Depends on chunk 01.

## Outcome

A Codex registration takes the stable alias of its repository when no live registration holds
it. The holder's registry record publishes the alias as its `name`, so Claude sessions and
`bridge.py send` reach it by that name. At every point at most one live record carries the
alias.

## Specification

- **Alias name.** The per-thread name without its final `-<two hex>` suffix, as built by
  `session.py` `details()` (`codex-<label>`). Codex only; DeepSeek and Claude get none.
- **Reservation.** In `save_registration()`, under the existing `<state_root>/names.lock`:
  the occupied set also holds every alias of every saved registration, so a per-thread name
  never equals an alias; and an alias is not taken while it equals a live per-thread name.
- **Lease.** `<state_root>/aliases/<alias>.json`, private, written with `durable_state.publish`
  under `names.lock`: `{alias, holder: <session key>, state: 'held' | 'moving', from, to,
  changed_at_ms}`. The lease is the only source of the holder. It carries no inbox, checkpoint,
  claim or thread ID.
- **Take at `ensure`.** Under `names.lock`, before the service starts: when the lease is absent,
  or its holder has no live registry record, write `holder = this key, state = held`. A live
  record is one that `bridge.peers()` reports for the holder's bridge. Before the take, remove a
  dead holder's record only when all hold: entrypoint `runtime_names.REGISTRY_ENTRYPOINT`, same
  owner, pid dead or start time different, `bridgeOwner` equal to
  the `owner` in the holder's `notify-ready.json`.
  A record with the alias as `name` that is not Koinon's, or that is live, refuses the take with
  `alias_occupied` and the path. A live holder: no take, report `alias_held_by` with the holder's
  per-thread name.
- **Publication.** The notifier derives the alias from `--name` and the lease path from
  `--state-dir` (`<state_root>/sessions/<key>`). The service command lines stay unchanged:
  installed service definitions are compared byte for byte. At start, under `names.lock`, it
  reads the lease: holder equal to its own key and `state = held` publishes `name = <alias>`;
  any other case publishes its per-thread name. Every record also carries `koinonName` (the
  per-thread name) and `koinonAlias` (the alias, holder or not). The notifier never rewrites its
  record in place; a change of holder takes effect at the next start of that notifier.
- **Send.** `bridge.py send` resolves names from the registry as today. A name that no live
  record carries, and that equals a lease's alias, fails with `alias_unheld` and names the
  alias. `bridge.py peers` adds `alias`, `alias_holder` (bool) and `thread_name`.
- **Reporting.** `ensure`, `status` and `guide` add `alias` with `name`, `held`, and when not
  held, the holder's per-thread name or `none`.

## Tests

The alias-allocation, registry-publication, no-holder, and reporting tests of
`definition-of-done.md`, and the edge cases for a crashed holder and a non-Koinon record.

## Live check 1

After merge and an authorized runtime upgrade: live check 1 of `definition-of-done.md`.

## Documentation

`PROTOCOL.md` (alias, lease, registry fields), `docs/INSTALL.md` (addressing a Codex peer by
alias), `README.md` if it names peer addressing, `CHANGELOG.md`.
