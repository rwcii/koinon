"""Bounded, resumable copies of explicitly selected stopped-state files.

The coordinator must establish and retain writer exclusion and inventory every
required file, including SQLite sidecars. This helper neither proves shutdown nor
chooses that inventory. Its descriptor is evidence only for the selected files.
"""
import hashlib
import os
from pathlib import Path
import shutil
import stat

from koinon import durable_state
from koinon.participant_lock import file_lock
from koinon import platform_support
from koinon import upgrade_manifest as manifest

MAX_FILE_BYTES = durable_state.MAX_PRIVATE_FILE_BYTES
MAX_TOTAL_BYTES = 4 << 30
CHUNK_BYTES = 1 << 20
LOCK = '.backup.lock'
TEMP = '.backup-copy.tmp'


class BackupError(ValueError):
    pass


def _names(names):
    names = manifest.names_checked(names)
    if LOCK in names or TEMP in names:
        raise BackupError('backup selection collides with reserved bookkeeping names')
    return names


def _parents(root, name, *, create=False):
    manifest.check_root(root)
    parent = root
    for part in Path(name).parts[:-1]:
        parent = parent / part
        try:
            info = parent.lstat()
        except FileNotFoundError:
            if not create:
                return
            parent.mkdir(mode=0o700)
            platform_support.sync_state_directory(parent.parent)
            info = parent.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o022 or create and info.st_mode & 0o077):
            raise BackupError('unsafe selected state directory')


def _stamp(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def _write(fd, data):
    offset = 0
    while offset < len(data):
        count = os.write(fd, data[offset:])
        if count <= 0:
            raise OSError('backup write made no progress')
        offset += count


def _file(root, name, *, output=None):
    """A source replacement is an inconsistent snapshot, never silently reopened."""
    _parents(root, name)
    path = root / name
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    except FileNotFoundError:
        return None
    try:
        original = os.fstat(fd)
        if (not stat.S_ISREG(original.st_mode) or original.st_uid != os.geteuid()
                or original.st_mode & 0o022 or original.st_mode & 0o7000
                or original.st_nlink != 1 or original.st_size > MAX_FILE_BYTES):
            raise BackupError('unsafe or oversized selected state file')
        digest, total = hashlib.sha256(), 0
        while total <= MAX_FILE_BYTES:
            data = os.read(fd, min(CHUNK_BYTES, MAX_FILE_BYTES + 1 - total))
            if not data:
                break
            total += len(data)
            if total > MAX_FILE_BYTES:
                raise BackupError('selected state file exceeded backup capacity')
            digest.update(data)
            if output is not None:
                _write(output, data)
        _parents(root, name)
        if (_stamp(path.lstat()) != _stamp(original) or _stamp(os.fstat(fd)) != _stamp(original)
                or total != original.st_size):
            raise BackupError('selected state file changed during capture')
        return dict(bytes=total, sha256=digest.hexdigest(), mode=stat.S_IMODE(original.st_mode))
    finally:
        os.close(fd)


def capture(root, names):
    """Capture selected bytes and explicit absences after caller-established shutdown."""
    root = manifest.select_root(root)
    files, total = {}, 0
    for name in _names(names):
        value = _file(root, name)
        total += 0 if value is None else value['bytes']
        if total > MAX_TOTAL_BYTES:
            raise BackupError('selected state exceeds backup capacity')
        files[name] = value
    result = dict(version=1, root=str(root), files=files, bytes=total)
    result['sha256'] = manifest.fingerprint(result)
    return result


def validate(expected):
    if (not isinstance(expected, dict) or set(expected) != {'version', 'root', 'files', 'bytes', 'sha256'}
            or type(expected['version']) is not int or expected['version'] != 1
            or not isinstance(expected['root'], str) or not Path(expected['root']).is_absolute()
            or not isinstance(expected['files'], dict) or not manifest.hex_digest(expected['sha256'])
            or type(expected['bytes']) is not int or not 0 <= expected['bytes'] <= MAX_TOTAL_BYTES):
        raise BackupError('invalid stopped-state snapshot')
    _names(list(expected['files']))
    total = 0
    for value in expected['files'].values():
        if value is None:
            continue
        if (not isinstance(value, dict) or set(value) != {'bytes', 'sha256', 'mode'}
                or type(value['bytes']) is not int or not 0 <= value['bytes'] <= MAX_FILE_BYTES
                or not manifest.hex_digest(value['sha256']) or type(value['mode']) is not int
                or not 0 <= value['mode'] <= 0o777 or value['mode'] & 0o022):
            raise BackupError('invalid stopped-state file entry')
        total += value['bytes']
    unsigned = {key: value for key, value in expected.items() if key != 'sha256'}
    if total != expected['bytes'] or manifest.fingerprint(unsigned) != expected['sha256']:
        raise BackupError('stopped-state snapshot digest mismatch')
    return expected


def verify(expected):
    expected = validate(expected)
    root = manifest.check_root(expected['root'])
    for name, value in expected['files'].items():
        if _file(root, name) != value:
            raise BackupError('stopped state changed since its frozen snapshot')
    return expected


def _destination(root):
    root = manifest.check_root(root)
    if root.lstat().st_mode & 0o077:
        raise BackupError('backup destination must be private')
    return root


def _sync_parents(root, parent):
    # An earlier mkdir may be visible despite a failed parent-directory flush.
    # Retry every link inside the frozen destination, not only rename endpoints.
    try:
        relative = parent.relative_to(root)
    except ValueError as exc:
        raise BackupError('backup directory is outside its frozen root') from exc
    if '..' in relative.parts:
        raise BackupError('backup directory contains parent traversal')
    for _ in range(len(relative.parts) + 1):
        platform_support.sync_state_directory(parent)
        parent = parent.parent


def _confirm(root, name, expected):
    path = root / name
    fd = durable_state.open_validated(path, MAX_FILE_BYTES, writable=True)
    if fd is None:
        raise BackupError('retained backup file missing')
    try:
        original = os.fstat(fd)
        if _file(root, name) != expected or _stamp(path.lstat()) != _stamp(original):
            raise BackupError('retained backup differs; preserve it')
        platform_support.sync_state_file(fd)
        _sync_parents(root, path.parent)
        platform_support.sync_state_file(fd)
        if _stamp(os.fstat(fd)) != _stamp(original) or _stamp(path.lstat()) != _stamp(original):
            raise BackupError('retained backup changed during confirmation')
    finally:
        os.close(fd)


def copy(snapshot, destination, *, allow_nested_destination=False):
    """Copy/reconfirm frozen bytes; publish no completion descriptor on partial failure."""
    snapshot = verify(snapshot)
    source = manifest.check_root(snapshot['root'])
    target = _destination(destination)  # Already frozen by the coordinator; no alias resolution.
    nested = source in target.parents
    if (source == target or target in source.parents
            or nested and not allow_nested_destination):
        raise BackupError('backup source and destination must be disjoint')
    if nested:
        relative = target.relative_to(source)
        if any(Path(name).is_relative_to(relative) or relative.is_relative_to(Path(name))
               for name in snapshot['files']):
            raise BackupError('nested backup destination overlaps selected source files')
    lock = target / LOCK
    directory = target.stat()
    with file_lock(lock, 'upgrade_backup_busy', None) as lock_fd:
        held, named = os.fstat(lock_fd), lock.lstat()
        if (held.st_dev, held.st_ino) != (named.st_dev, named.st_ino):
            raise BackupError('backup lock identity changed')
        expected_files, missing_bytes = {}, 0
        for name, original in snapshot['files'].items():
            desired = None if original is None else dict(original, mode=0o600)
            expected_files[name] = desired
            current = _file(target, name)
            if current is not None and current != desired:
                raise BackupError('unexpected retained backup file; preserve it')
            if current is None and desired is not None:
                missing_bytes += desired['bytes']
        overhead = (len(expected_files) + 1) * 65536
        if shutil.disk_usage(target).free < missing_bytes + overhead:
            raise BackupError('insufficient backup space before copy')
        for name, original in snapshot['files'].items():
            if original is None:
                continue
            desired = expected_files[name]
            if _file(target, name) is not None:
                _confirm(target, name, desired)
                continue
            _parents(target, name, create=True)
            temporary = target / TEMP
            old = durable_state.open_validated(temporary, MAX_FILE_BYTES)
            if old is not None:
                os.close(old)
                temporary.unlink()
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
            try:
                if _file(source, name, output=fd) != original:
                    raise BackupError('stopped source changed during backup copy')
                platform_support.sync_state_file(fd)
                path = target / name
                if path.exists() or path.is_symlink():
                    raise BackupError('backup destination appeared during copy')
                os.replace(temporary, path)
                _sync_parents(target, path.parent)
                platform_support.sync_state_file(fd)
            finally:
                os.close(fd)
                temporary.unlink(missing_ok=True)
        verify(snapshot)
        result = capture(target, list(snapshot['files']))
        if result['files'] != expected_files:
            raise BackupError('backup verification failed; completion forbidden')
        current = _destination(target).stat()
        if (current.st_dev, current.st_ino) != (directory.st_dev, directory.st_ino):
            raise BackupError('backup destination identity changed')
        return dict(version=1, source=snapshot['sha256'], destination=result)
