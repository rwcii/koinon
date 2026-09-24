"""Stable alias of a Codex participant: reservation, lease and the one-holder rule.

One state root and one `names.lock` serve every repository, so each repository reserves
its alias once, in its lease `<state_root>/aliases/<alias>.json`. The lease is the only
source of the holder. A notifier publishes the alias as its registry name only when, at
its start and under `names.lock`, the lease names its own key as holder in state `held`
or `publishing`; it never rewrites its record in place. The lease leaves a key only
when that key has no live record carrying the alias, checked under the same lock.
Therefore at most one live registry record carries an alias at any time.

`names.lock` is held only for one read-decide-write of a lease, or for a notifier's
read of the lease plus the creation of its registry record; never while a service
starts, stops or waits. The lease carries no inbox, checkpoint, claim or thread ID.
"""
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import time

from koinon import durable_state
from koinon import platform_support
from koinon import repository_identity
from koinon import runtime_names
from koinon.peer_transport import private_dir

STATES = ('held', 'publishing', 'moving')
TRANSIENT = ('publishing', 'moving')
_SUFFIX = re.compile(r'-[0-9a-f]{2}$')


def state_root_of(state):
    """The installation state root of a session state directory, or None."""
    state = Path(state)
    return state.parent.parent if state.parent.name == 'sessions' else None


def repository_digest(repo):
    """SHA-256 of the absolute Git common directory; None outside a repository."""
    try:
        return hashlib.sha256(str(repository_identity.repo_common_directory(repo)).encode()).hexdigest()
    except ValueError:
        return None


def base_name(name):
    """The per-thread name without its two-hex suffix: `codex-<label>`."""
    return _SUFFIX.sub('', name)


@contextmanager
def names_lock(state_root):
    """The lock that also serializes per-thread name assignment (`session.py`)."""
    state_root = Path(state_root)
    private_dir(state_root)
    with (state_root / 'names.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield


def _directory(state_root):
    return Path(state_root) / 'aliases'


def leases(state_root):
    """Every valid lease; an invalid file is skipped here and refused when written."""
    found = []
    directory = _directory(state_root)
    try:
        paths = sorted(directory.glob('*.json'))
    except OSError:
        return found
    for path in paths:
        try:
            value = durable_state.read(path, max_bytes=4096)
        except (OSError, ValueError):
            continue
        if (isinstance(value, dict) and value.get('alias') == path.stem
                and value.get('state') in STATES and isinstance(value.get('repository'), str)):
            found.append(value)
    return found


def _write(state_root, lease):
    private_dir(_directory(state_root))
    durable_state.publish(_directory(state_root) / (lease['alias'] + '.json'),
                          dict(lease, changed_at_ms=int(time.time() * 1000)), max_bytes=4096)
    return lease


def operation():
    """This command, as the owner of a transient lease state."""
    pid = os.getpid()
    return dict(pid=pid, proc_start=platform_support.proc_start(pid))


def operation_live(value):
    if not isinstance(value, dict) or not isinstance(value.get('pid'), int):
        return False
    try:
        return (platform_support.process_alive(value['pid'])
                and platform_support.same_process(value.get('proc_start'),
                                                  platform_support.proc_start(value['pid'])))
    except OSError:
        return False


def session_names(state_root):
    """Saved per-thread name of every registration, by session key."""
    names = {}
    for path in (Path(state_root) / 'sessions').glob('*/session.json'):
        try:
            value = durable_state.read(path, max_bytes=65536)
        except (OSError, ValueError):
            continue
        if isinstance(value, dict) and isinstance(value.get('name'), str):
            names[path.parent.name] = value['name']
    return names


def live_for(records, name):
    """The live registry records of the registration whose per-thread name is name."""
    return [record for record in records
            if record.get('thread_name', record.get('name')) == name]


def publishes(records, name, alias):
    return any(record.get('name') == alias for record in live_for(records, name))


def _reserve(state_root, digest, name, records, existing):
    base = base_name(name)
    taken = {lease['alias'] for lease in existing}
    taken |= set(session_names(state_root).values())
    taken |= {record.get('name') for record in records}
    for candidate in [base] + [f'{base}-{digest[:n]}' for n in range(4, 65, 2)]:
        if candidate not in taken:
            return dict(alias=candidate, repository=digest, holder=None, state='held',
                        **{'from': None}, to=None, operation=None)
    return None


def _stale_records(registry, alias, holder_state):
    """Registry records named alias: (removable dead Koinon records of the holder, blockers)."""
    removable, blockers = [], []
    try:
        owner = durable_state.read(Path(holder_state) / 'notify-ready.json') if holder_state else None
    except (OSError, ValueError):
        owner = None
    owner = owner.get('owner') if isinstance(owner, dict) else None
    for path in sorted(Path(registry).glob('*.json')):
        if not path.stem.isdigit() or path.is_symlink():
            continue
        try:
            info = path.stat()
            record = json.loads(path.read_text()) if stat.S_ISREG(info.st_mode) and info.st_size <= 65536 else None
        except (OSError, ValueError):
            continue
        if not isinstance(record, dict) or record.get('name') != alias:
            continue
        pid = int(path.stem)
        try:
            alive = (platform_support.process_alive(pid)
                     and platform_support.same_process(record.get('procStart'), platform_support.proc_start(pid)))
        except OSError:
            alive = False
        if (not alive and info.st_uid == os.geteuid()
                and record.get('entrypoint') == runtime_names.REGISTRY_ENTRYPOINT
                and owner is not None and record.get('bridgeOwner') == owner):
            removable.append(path)
        else:
            blockers.append(path)
    return removable, blockers


def registry_folder():
    return Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude'))) / 'sessions'


def prepare(state_root, key, repo, name, *, peers, stopped, registry=None):
    """Reserve and take the alias for one Codex `ensure`, under `names.lock`.

    peers() returns the live registry records; stopped(key) says whether a session's
    lifecycle is stopped. Returns a report with `restart` true when this key must restart
    its running service so that its notifier publishes the alias.
    """
    digest = repository_digest(repo)
    if digest is None:
        return dict(state='unavailable', reason='not_a_repository', restart=False)
    registry = registry_folder() if registry is None else Path(registry)
    with names_lock(state_root):
        records = peers()
        existing = leases(state_root)
        lease = next((value for value in existing if value['repository'] == digest), None)
        if lease is None:
            lease = _reserve(state_root, digest, name, records, existing)
            if lease is None:
                return dict(state='unavailable', reason='alias_unavailable', restart=False)
            _write(state_root, lease)
        alias = lease['alias']
        running = bool(live_for(records, name))
        names = session_names(state_root)
        holder = lease['holder']
        if holder == key and lease['state'] != 'moving':
            if publishes(records, name, alias):
                if lease['state'] != 'held':
                    _write(state_root, dict(lease, state='held', operation=None))
                return dict(name=alias, held=True, state='held', restart=False)
            _write(state_root, dict(lease, state='publishing', operation=operation()))
            return dict(name=alias, held=False, state='publishing', restart=running)
        if holder is not None and holder != key:
            successor = lease['to'] if lease['state'] == 'moving' else holder
            if lease['state'] in TRANSIENT and operation_live(lease.get('operation')):
                return dict(name=alias, held=False, state=lease['state'], reason='alias_busy', restart=False)
            if live_for(records, names.get(successor)) or not stopped(successor):
                reason = 'alias_busy' if lease['state'] in TRANSIENT else 'alias_held_by'
                return dict(name=alias, held=False, state=lease['state'], reason=reason,
                            holder=names.get(successor), restart=False)
        elif holder == key:
            # A `moving` lease is completed by the rebind of chunk 03, never here.
            return dict(name=alias, held=False, state='moving', reason='alias_busy', restart=False)
        holder_state = Path(state_root) / 'sessions' / holder if holder else None
        removable, blockers = _stale_records(registry, alias, holder_state)
        if blockers:
            return dict(name=alias, held=False, state=lease['state'], reason='alias_occupied',
                        paths=[str(path) for path in blockers], restart=False)
        for path in removable:
            path.unlink()
        _write(state_root, dict(lease, holder=key, state='publishing', operation=operation(),
                                **{'from': None}, to=None))
        return dict(name=alias, held=False, state='publishing', restart=running)


def confirm(state_root, key, name, *, peers):
    """After this key's service is ready: `publishing` becomes `held` once the alias is live."""
    with names_lock(state_root):
        records = peers()
        for lease in leases(state_root):
            if lease['holder'] == key and lease['state'] == 'publishing':
                if publishes(records, name, lease['alias']):
                    _write(state_root, dict(lease, state='held', operation=None))
                    return True
                return False
    return False


@contextmanager
def publication(state, name, repo):
    """For a notifier start: yield (registry name, alias) while holding `names.lock`.

    The caller creates its registry record inside the block, so a take or a move under
    the same lock either sees the new record or is seen by this start.
    """
    state_root = state_root_of(state)
    if state_root is None:
        yield name, None
        return
    key = Path(state).name
    digest = repository_digest(repo)
    with names_lock(state_root):
        chosen, alias = name, None
        for lease in leases(state_root):
            if lease['holder'] == key and lease['state'] in ('held', 'publishing'):
                chosen = alias = lease['alias']
                break
            if lease['repository'] == digest:
                alias = lease['alias']
        yield chosen, alias


def report(state_root, key, repo, name):
    """Read-only alias state of one Codex registration for `ensure`, `status` and `guide`."""
    digest = repository_digest(repo)
    lease = next((value for value in leases(state_root)
                  if value['holder'] == key or (digest is not None and value['repository'] == digest)), None)
    if lease is None:
        return dict(state='unknown', reason='not_reserved')
    held = lease['holder'] == key and lease['state'] == 'held'
    found = dict(state='observed', name=lease['alias'], held=held, lease=lease['state'])
    if lease['holder'] != key:
        found['holder'] = session_names(state_root).get(lease['holder']) if lease['holder'] else None
    return found


def reserved(state_root, alias):
    """Whether alias is a reserved alias of this state root."""
    return any(lease['alias'] == alias for lease in leases(state_root))
