"""Content-free participant status records shared through an account-local directory.

A writer publishes one record per participant into `directory()`, which sits next to the
Claude session registry: `${CLAUDE_CONFIG_DIR:-~/.claude}/koinon-status`.
`bridge.py peers` and the notifier `status` read them to report each participant's model,
context use and claimed work next to its presence. Records hold only the allowlisted
numbers, identifiers, states and times below; any other field is dropped before it is stored.

Each field group carries the time its source recorded the value. A reader adds its own read
time and the presence freshness window, and reports every group as unknown unless the
participant that the record names is live with its recorded process-start marker.
"""
import json
import os
import re
import stat
import time
from pathlib import Path

from koinon import participant_presence

# The writer runs on every Claude status-line update, so this module imports only what
# writing needs. Readers import the process and state-file helpers where they use them.

VERSION = 1
MAX_BYTES = 16 * 1024
FRESHNESS_MS = participant_presence.FRESHNESS_MS
KINDS = ('claude', 'bridge')
_KEY = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]{0,127}')

# The only values a record may hold, by group. Types are exact: bool is not an int here.
FIELDS = {
    'model': {'id': str},
    'context': {'limit_tokens': int, 'used_tokens': int, 'usage_available': bool},
    'activity': {'state': str},
}
ACTIVITY_STATES = ('busy', 'idle')
MAX_TEXT = 128
# The largest integer JSON carries exactly; it also keeps every ratio finite.
MAX_INT = 2 ** 53 - 1
REASONS = frozenset((
    'no_status_record', 'status_record_invalid', 'participant_not_live',
    'participant_not_associated', 'statusline_missing', 'source_unrecognized',
    'no_token_usage', 'work_association_missing', 'memory_unavailable',
    'provider_unsupported'))


def _now_ms():
    return int(time.time() * 1000)


def directory(registry=None):
    """The status directory beside a Claude session registry (`<config>/sessions`)."""
    if registry is None:
        registry = registry_directory()
    return Path(registry).parent / 'koinon-status'


def _path(kind, key, registry=None):
    if kind not in KINDS:
        raise ValueError('unknown status record kind')
    key = str(key)
    if not _KEY.fullmatch(key):
        raise ValueError('invalid status record key')
    return directory(registry) / f'{kind}-{key}.json'


def _process(value):
    if (not isinstance(value, dict) or set(value) != {'pid', 'proc_start'}
            or type(value['pid']) is not int or value['pid'] <= 0
            or not isinstance(value['proc_start'], str) or not value['proc_start']
            or len(value['proc_start']) > MAX_TEXT):
        raise ValueError('invalid participant process')
    return dict(value)


def _group(name, value):
    """Keep only allowlisted fields of one group; an unknown group keeps only its reason."""
    if not isinstance(value, dict):
        raise ValueError('invalid status group')
    source, stamp = value.get('source'), value.get('recorded_at_ms')
    if not isinstance(source, str) or not source or len(source) > MAX_TEXT:
        raise ValueError('invalid status source')
    if type(stamp) is not int or not 0 <= stamp <= MAX_INT:
        raise ValueError('invalid status source time')
    reason = value.get('reason')
    if reason is not None:
        if reason not in REASONS:
            raise ValueError('invalid status reason')
        return dict(source=source, recorded_at_ms=stamp, reason=reason)
    kept = dict(source=source, recorded_at_ms=stamp, reason=None)
    for field, kind in FIELDS[name].items():
        item = value.get(field)
        if item is None:
            continue
        if type(item) is not kind or (kind is int and not 0 <= item <= MAX_INT) or (
                kind is str and (not item or len(item) > MAX_TEXT)):
            raise ValueError('invalid status field')
        kept[field] = item
    if name == 'activity' and kept.get('state') not in ACTIVITY_STATES:
        raise ValueError('invalid activity state')
    return kept


def build(kind, *, participant, groups, identity=None, provider=None):
    """Return the stored form of a record, with every non-allowlisted value removed."""
    record = dict(version=VERSION, kind=kind,
                  participant=None if participant is None else _process(participant))
    if kind == 'bridge':
        if (not isinstance(identity, dict) or set(identity) != {'proc_start', 'generation'}
                or not all(isinstance(v, str) and v and len(v) <= MAX_TEXT for v in identity.values())):
            raise ValueError('invalid bridge identity')
        if provider not in ('codex', 'deepseek'):
            raise ValueError('invalid provider')
        record.update(identity=dict(identity), provider=provider)
    elif identity is not None or provider is not None:
        raise ValueError('identity belongs to bridge records')
    record['groups'] = {name: _group(name, value) for name, value in groups.items() if name in FIELDS}
    if record['participant'] is None and any(g['reason'] is None for g in record['groups'].values()):
        raise ValueError('an observed value needs an associated participant')
    return record


def _private_dir(path):
    """Create the directory 0700 and refuse one that is not private to this user."""
    missing = [path]
    while not missing[-1].parent.exists():
        missing.append(missing[-1].parent)
    for directory in reversed(missing):
        directory.mkdir(mode=0o700, exist_ok=True)
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError('directory must be owned by this user and mode 0700')


def _load(path, limit):
    """Read a private JSON object without following links, or return None."""
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    except OSError:
        return None
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077
                or info.st_nlink != 1 or info.st_size > limit):
            return None
        with os.fdopen(fd, 'rb') as stream:
            fd = None
            value = json.loads(stream.read(limit + 1))
        return value if isinstance(value, dict) else None
    except (OSError, ValueError, UnicodeError, RecursionError):
        return None
    finally:
        if fd is not None:
            os.close(fd)


def write(kind, key, *, participant, groups, identity=None, provider=None, registry=None):
    """Publish a record atomically, mode 0600, without following links.

    Records describe live processes and are rebuilt by their writer, so the write is
    atomic but not flushed to disk. A replacement name that a crashed or cancelled
    writer left behind is removed first.
    """
    record = build(kind, participant=participant, groups=groups, identity=identity, provider=provider)
    path = _path(kind, key, registry)
    _private_dir(path.parent)
    data = json.dumps(record, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()
    if len(data) > MAX_BYTES:
        raise ValueError('status record too large')
    temp = path.with_name(path.name + '.tmp')
    temp.unlink(missing_ok=True)
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        with os.fdopen(fd, 'wb') as stream:
            fd = None
            stream.write(data)
        os.replace(temp, path)
    finally:
        if fd is not None:
            os.close(fd)
        temp.unlink(missing_ok=True)
    return path


def remove(kind, key, *, generation=None, registry=None):
    """Remove a record; a bridge record only while it still names this generation."""
    from koinon import durable_state
    path = _path(kind, key, registry)
    if generation is not None:
        try:
            current = durable_state.read(path, max_bytes=MAX_BYTES)
        except (OSError, durable_state.StateFileError, durable_state.StateReadBusyError):
            return False
        if not current or (current.get('identity') or {}).get('generation') != generation:
            return False
    try:
        path.unlink()
        return True
    except FileNotFoundError:
        return False


def _live(process):
    from koinon import platform_support
    if not isinstance(process, dict) or type(process.get('pid')) is not int:
        return False
    pid = process['pid']
    if not platform_support.process_alive(pid):
        return False
    try:
        return platform_support.same_process(process.get('proc_start'), platform_support.proc_start(pid))
    except Exception:
        return False


def read(kind, key, *, participant=None, identity=None, now=None, registry=None):
    """Return `{'groups', 'reason'}` for a validated record.

    `participant` is the process the caller already verified (a Claude registry record);
    the record must name the same process. `identity` is the bridge's registry identity; the
    record must name the same bridge process and notifier generation. On any failure
    `groups` is empty and `reason` names the failure.
    """
    from koinon import durable_state, platform_support
    now = _now_ms() if now is None else now
    try:
        path = _path(kind, key, registry)
        record = durable_state.read(path, max_bytes=MAX_BYTES)
    except (ValueError, OSError, durable_state.StateFileError, durable_state.StateReadBusyError):
        return dict(groups={}, reason='status_record_invalid', provider=None)
    if record is None:
        return dict(groups={}, reason='no_status_record', provider=None)
    try:
        if record.get('version') != VERSION or record.get('kind') != kind:
            raise ValueError('unsupported status record')
        stored = build(kind, participant=record.get('participant'), groups=record.get('groups') or {},
                       identity=record.get('identity'), provider=record.get('provider'))
        if not all(isinstance(g, dict) for g in (record.get('groups') or {}).values()):
            raise ValueError('invalid status group')
    except (ValueError, TypeError, AttributeError):
        return dict(groups={}, reason='status_record_invalid', provider=None)
    provider = stored.get('provider')
    if kind == 'bridge' and identity is not None and stored['identity'] != identity:
        return dict(groups={}, reason='no_status_record', provider=None)
    if kind == 'claude' and participant is not None and (
            stored['participant'] is None or stored['participant']['pid'] != participant['pid']
            or not platform_support.same_process(stored['participant']['proc_start'], participant['proc_start'])):
        return dict(groups={}, reason='no_status_record', provider=None)
    if stored['participant'] is None:
        # build() admits only unknown groups here, each with its own reason.
        return dict(groups=stored['groups'], reason=None, provider=provider)
    if not _live(stored['participant']):
        return dict(groups={}, reason='participant_not_live', provider=provider)
    return dict(groups=stored['groups'], reason=None, provider=provider)


def _future(group, now):
    """A source time after the read time is not evidence, as for registry activity."""
    return (group is not None and group.get('reason') is None
            and group.get('recorded_at_ms', 0) > now)


def _base(group, reason, now):
    if reason is None and _future(group, now):
        reason = 'status_record_invalid'
    if reason is not None or group is None or group.get('reason') is not None:
        return dict(state='unknown', source=None if group is None else group.get('source'),
                    recorded_at_ms=None if group is None else group.get('recorded_at_ms'),
                    observed_at_ms=now, freshness_ms=FRESHNESS_MS,
                    reason=reason or (group or {}).get('reason') or 'no_status_record')
    return dict(state='observed', source=group['source'], recorded_at_ms=group['recorded_at_ms'],
                observed_at_ms=now, freshness_ms=FRESHNESS_MS, reason=None)


def model_view(result, now):
    group = result['groups'].get('model')
    view = _base(group, result['reason'], now)
    view['id'] = group.get('id') if view['state'] == 'observed' else None
    if view['state'] == 'observed' and view['id'] is None:
        view.update(state='unknown', id=None, reason='source_unrecognized')
    return view


def context_view(result, now):
    group = result['groups'].get('context')
    view = _base(group, result['reason'], now)
    limit = used = fill = None
    if view['state'] == 'observed':
        limit, used = group.get('limit_tokens'), group.get('used_tokens')
        if not group.get('usage_available', True) or not limit or used is None:
            view.update(state='unknown', reason='no_token_usage')
            limit = used = None
        else:
            fill = used / limit
    view.update(limit_tokens=limit, used_tokens=used, fill=fill)
    return view


def activity(result, now):
    """A `participant_presence` activity value, or None when the record holds no activity."""
    group = result['groups'].get('activity')
    if result['reason'] is not None or group is None:
        return None
    if _future(group, now):
        return participant_presence.unknown('status_record_invalid')
    if group.get('reason') is not None:
        return participant_presence.unknown(group['reason'])
    return dict(state=group['state'], source=group['source'], observed_at_ms=now,
                since_ms=group['recorded_at_ms'], freshness_ms=FRESHNESS_MS, reason=None)


def work_view(now, reason='work_association_missing'):
    """Claimed work is queried from the peer's memory store; chunk 06 adds that query."""
    return dict(state='unknown', source=None, claims=[], recorded_at_ms=None,
                observed_at_ms=now, freshness_ms=FRESHNESS_MS, reason=reason)


def unknown_views(reason, now=None):
    now = _now_ms() if now is None else now
    result = dict(groups={}, reason=reason)
    return dict(model=model_view(result, now), context=context_view(result, now),
                work=work_view(now))


def registry_directory():
    return Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude'))) / 'sessions'


def claude_process(session_id, registry=None):
    """The Claude process that the session registry names for a session, or None.

    The registry record carries the process-start marker in the form this platform
    uses, so no process query runs here; readers verify liveness when they read.
    """
    if not isinstance(session_id, str) or not _KEY.fullmatch(session_id):
        return None
    folder = Path(registry) if registry is not None else registry_directory()
    try:
        paths = sorted(folder.glob('*.json'))
    except OSError:
        return None
    for path in paths:
        if not path.stem.isdigit():
            continue
        record = _load(path, 65536)
        if not record or record.get('sessionId') != session_id or record.get('entrypoint') != 'cli':
            continue
        start = record.get('procStart')
        if isinstance(start, (int, str)) and str(start):
            return dict(pid=int(path.stem), proc_start=str(start))
    return None


def for_registry(record, pid, live_start, now=None, registry=None):
    """Return `(activity, views)` for one live registry record.

    `activity` replaces the registry's model activity when the status record holds one;
    otherwise it is None. A Claude `cli` record maps to `claude-<sessionId>.json`; a Koinon
    daemon record maps to `bridge-<pid>.json` for the same bridge process and generation.
    """
    from koinon import runtime_names
    now = _now_ms() if now is None else now
    entry = record.get('entrypoint')
    if entry == 'cli':
        session = record.get('sessionId')
        if not isinstance(session, str) or not _KEY.fullmatch(session):
            return None, unknown_views('no_status_record', now)
        result = read('claude', session, participant=dict(pid=pid, proc_start=live_start), now=now,
                      registry=registry)
        return None, views(result, now)
    if entry == runtime_names.REGISTRY_ENTRYPOINT:
        start, generation = record.get('procStart'), record.get('bridgeOwner')
        if not isinstance(start, str) or not isinstance(generation, str):
            return None, unknown_views('no_status_record', now)
        result = read('bridge', pid, identity=dict(proc_start=start, generation=generation), now=now,
                      registry=registry)
        return activity(result, now), views(result, now)
    return None, unknown_views('provider_unsupported', now)


def views(result, now=None):
    now = _now_ms() if now is None else now
    return dict(model=model_view(result, now), context=context_view(result, now),
                work=work_view(now))
