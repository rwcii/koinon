"""Staged memory artifact ownership and recoverable publication; no manager calls.

Only new selections and exact repeats are supported here. Runtime upgrades,
retargeting, activation, and removal require the later lifecycle operations.
"""
from contextlib import contextmanager
import copy
import hashlib
import os
from pathlib import Path
import stat
import tempfile

import durable_state
import install_state
import memory_service_config as configuration
from participant_lock import file_lock, OwnershipError
import platform_support
import runtime_names
from work_policy import absolute_path

MAX_ARTIFACT_BYTES = 65536


def digest(content):
    return hashlib.sha256(content).hexdigest()


def _parents(path):
    """Refuse aliases and writable ancestors, allowing the system temporary root."""
    for parent in reversed(path.parents):
        info = parent.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, os.getuid())
                or (info.st_mode & 0o022 and not
                    (info.st_uid == 0 and info.st_mode & stat.S_ISVTX))):
            raise RegistrationPathError(parent)
    info = path.parent.lstat()
    if info.st_uid != os.getuid() or info.st_mode & 0o022:
        raise RegistrationPathError(path.parent)



class RegistrationPathError(ValueError):
    def __init__(self, path):
        self.paths = (str(path),)
        super().__init__('unsafe or conflicting manager registration path: ' + str(path))


def preflight_registration(record, paths):
    """Read-only checks before systemd creates loader and enablement links.

    Missing suffix directories are allowed; existing ancestors may not be aliases
    or writable by another user/group. Never repair permissions or adopt a target.
    """
    artifact = Path(record['artifact'])
    for raw in paths:
        path = absolute_path(str(raw))
        closest = None
        for parent in reversed(path.parents):
            try:
                info = parent.lstat()
            except FileNotFoundError:
                break
            if (not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, os.geteuid())
                    or (info.st_mode & 0o022 and not (info.st_uid == 0 and info.st_mode & stat.S_ISVTX))):
                raise RegistrationPathError(parent)
            closest = info
        if closest is None or closest.st_uid != os.geteuid() or closest.st_mode & 0o022:
            raise RegistrationPathError(path.parent)
        try:
            info = path.lstat()
        except FileNotFoundError:
            continue
        if path == artifact:
            continue  # The caller verifies canonical artifact bytes separately.
        if (not stat.S_ISLNK(info.st_mode) or info.st_uid != os.geteuid()
                or os.readlink(path) != str(artifact)):
            raise RegistrationPathError(path)

def _read(path):
    _parents(path)
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_mode & 0o077 or info.st_nlink != 1):
            raise ValueError('artifact must be a private owned regular file')
        content = stream.read(MAX_ARTIFACT_BYTES + 1)
        if len(content) > MAX_ARTIFACT_BYTES:
            raise ValueError('artifact exceeds supported size')
        current = path.lstat()
        if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
            raise ValueError('artifact changed while reading')
        return content


def _expected(prefix, python, record):
    prefix = absolute_path(str(prefix))
    python = absolute_path(str(python))
    if not python.is_file() or not os.access(python, os.X_OK):
        raise ValueError('selected Python interpreter is not an executable file')
    _parents(prefix / 'install.json')
    key, _ = configuration.verify_selection(record)
    if record['backend'] == 'manual':
        raise ValueError('manual operation has no managed artifact')
    content = platform_support.memory_service_artifact(prefix, python, key, record)
    if len(content) > MAX_ARTIFACT_BYTES or digest(content) != record['artifact_digest']:
        raise ValueError('artifact digest does not match selected command')
    return key, Path(record['artifact']), content


@contextmanager
def _boundary(prefix, artifact=None):
    """Never pass codes from the memory protocol into installer errors."""
    try:
        yield
    except runtime_names.NameConflict:
        raise
    except (OSError, ValueError) as exc:
        raise runtime_names.NameConflict('invalid_install_configuration',
                                         tuple(path for path in (*getattr(exc, 'paths', ()),
                                               Path(prefix) / 'install.json', artifact)
                                               if path is not None)) from exc


def verify_owned(prefix, python, record, *, observed_artifact=None, upgrading=False):
    """Verify disk ownership; an optional manager path must match literally.

    This does not query a manager or prove that any process is running.
    """
    with _boundary(prefix, record.get('artifact') if isinstance(record, dict) else None):
        key, path, expected = _expected(prefix, python, record)
        if upgrading:
            import upgrade_exclusion
            config = upgrade_exclusion.component_configuration(prefix, 'memory', record)
        else:
            config = runtime_names.install_config(prefix)
        saved = config.get('memory_services', {}).get('repositories', {}).get(key)
        if saved != record or record['state'] != 'installed':
            raise ValueError('artifact has no completed owning registration')
        if observed_artifact is not None and str(observed_artifact) != str(path):
            raise ValueError('loaded manager artifact differs from registration')
        if _read(path) != expected:
            raise ValueError('owned artifact is missing or changed')
        return record


def verify_removing(prefix, python, record):
    """Validate retained removal evidence; a removed artifact is an expected phase."""
    desired = {field: copy.deepcopy(record[field]) for field in configuration.base_fields(record)}
    desired['state'] = 'installed'
    with _boundary(prefix, record.get('artifact')):
        key, path, expected = _expected(prefix, python, desired)
        saved = runtime_names.install_config(prefix).get('memory_services', {}).get('repositories', {}).get(key)
        if saved != record or record['state'] != 'removing':
            raise ValueError('memory removal evidence changed')
        if _read(path) not in (None, expected):
            raise ValueError('memory artifact changed during removal')
    return desired


def verify_loader_link(path, expected):
    """Verify one private same-user literal loader link, never an alias chain."""
    path, expected = absolute_path(str(path)), absolute_path(str(expected))
    if path.name != expected.name:
        raise RegistrationPathError(path)
    _parents(path)
    before = path.lstat()
    if (not stat.S_ISLNK(before.st_mode) or before.st_uid != os.geteuid()
            or os.readlink(path) != str(expected)):
        raise RegistrationPathError(path)
    after = path.lstat()
    if ((before.st_dev, before.st_ino) != (after.st_dev, after.st_ino)
            or os.readlink(path) != str(expected)):
        raise RegistrationPathError(path)


def verify_loaded(prefix, python, record, observed_artifact, *, upgrading=False):
    """Accept an exact artifact or its private same-user systemd loader link."""
    verify_owned(prefix, python, record, upgrading=upgrading)
    with _boundary(prefix, observed_artifact):
        path = absolute_path(str(observed_artifact))
        expected = Path(record['artifact'])
        if path == expected:
            return record
        if record['backend'] != 'systemd':
            raise ValueError('loaded manager artifact differs from registration')
        verify_loader_link(path, expected)
        verify_owned(prefix, python, record, upgrading=upgrading)
        return record


def _prepare(prefix, python, desired, config):
    if not config:
        raise ValueError('installation configuration is required')
    key, path, content = _expected(prefix, python, desired)
    if desired['state'] != 'installed':
        raise ValueError('publication requires an installed desired selection')
    inventory = config.get('memory_services', dict(version=1, repositories={}))
    configuration.validate(inventory)
    old = inventory['repositories'].get(key)
    current = _read(path)
    _parents(_lock_path(path))
    if old is None:
        configuration.admit(inventory, desired)  # Capacity before writing a lock or record.
        if current is not None:
            raise ValueError('artifact exists without this installation owning it')
    else:
        base = {field: old[field] for field in configuration.base_fields(old)}
        base['state'] = 'installed'
        if old['state'] == 'removing':
            raise ValueError('publication cannot resume state removing')
        if base != desired:
            raise ValueError('selection changes require explicit lifecycle reconciliation')
        if old['state'] == 'installed':
            if current != content:
                raise ValueError('installed artifact is missing or changed')
        elif old['state'] == 'pending':
            observed = None if current is None else digest(current)
            if observed not in (old['before_digest'], old['after_digest']):
                raise ValueError('unexpected artifact during publication recovery')
            # This primitive creates artifacts, not upgrades of existing templates.
            # A nonempty preimage must be the same exact owned command and policy.
            if current is not None and current != content:
                raise ValueError('pending preimage requires explicit upgrade reconciliation')
            if old['before_digest'] not in (None, digest(content)):
                raise ValueError('unsupported pending template replacement')
    return key, path, content, old, current


def _lock_path(path):
    # Shared across prefixes, outside the manager's artifact scan directory.
    return path.parent.parent / ('.koinon-memory-artifact-' +
                                 hashlib.sha256(str(path).encode()).hexdigest() + '.lock')


@contextmanager
def _artifact_lock(path):
    lock = _lock_path(path)
    try:
        with file_lock(lock, 'configuration_busy', None,
                       timeout=install_state.LOCK_TIMEOUT) as fd:
            held, current = os.fstat(fd), lock.lstat()
            if (held.st_dev, held.st_ino) != (current.st_dev, current.st_ino):
                raise ValueError('artifact lock was replaced')
            yield
    except OwnershipError as exc:
        raise runtime_names.installation_lock_error(exc.code, lock) from exc


def _save(state, key, record):
    inventory = copy.deepcopy(state.config.get('memory_services', dict(version=1, repositories={})))
    inventory['repositories'][key] = record
    state.merge({'memory_services': inventory})


def _fsync_directory(path):
    platform_support.sync_state_directory(path)


def _confirm(path, content):
    """Confirm exact retained artifact bytes without changing its inode."""
    _parents(path)
    fd = durable_state.open_validated(path, MAX_ARTIFACT_BYTES, writable=True)
    if fd is None:
        raise ValueError('owned artifact is missing')
    try:
        original = os.fstat(fd)
        def stamp(info):
            return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
        def unchanged():
            if (_read(path) != content or stamp(path.lstat()) != stamp(original)
                    or stamp(os.fstat(fd)) != stamp(original)):
                raise ValueError('owned artifact changed during confirmation')
        unchanged()
        platform_support.sync_state_file(fd)
        _fsync_directory(path.parent)
        platform_support.sync_state_file(fd)
        unchanged()
    finally:
        os.close(fd)


def _replace(path, content, before):
    fd, temporary = tempfile.mkstemp(prefix='.memory-artifact-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(content)
            stream.flush()
            platform_support.sync_state_file(stream.fileno())
            if _read(path) != before:
                raise ValueError('artifact changed during publication; pending evidence retained')
            os.replace(temporary, path)
            _fsync_directory(path.parent)
            platform_support.sync_state_file(stream.fileno())
    finally:
        Path(temporary).unlink(missing_ok=True)


def publish(prefix, python, desired):
    """Publish one staged artifact, returning desired state, never live health.

    The installation and artifact directories must already exist. Preflight is
    read-only; locked revalidation precedes any saved record or artifact mutation.
    The artifact lock excludes other installed prefixes in the same namespace.
    No public CLI invokes this until lifecycle integration is complete.
    """
    desired = copy.deepcopy(desired)
    with _boundary(prefix, desired.get('artifact') if isinstance(desired, dict) else None):
        prefix = absolute_path(str(prefix))
        config = runtime_names.install_config(prefix)
        _, path, _, _, _ = _prepare(prefix, python, desired, config)
        with install_state.locked(prefix) as state, _artifact_lock(path):
            key, path, content, old, before = _prepare(prefix, python, desired, state.config)
            if old and old['state'] == 'installed':
                _confirm(path, content)
                state.confirm()
                return desired
            if not old:
                _save(state, key, dict(desired, state='pending', before_digest=None,
                                       after_digest=desired['artifact_digest']))
            if before != content:
                _replace(path, content, before)
            else:
                # Reconfirm all file/directory/device flushes after an ambiguous rename.
                _confirm(path, content)
            if _read(path) != content:
                raise ValueError('artifact changed before completion; pending evidence retained')
            _save(state, key, desired)
            return desired
