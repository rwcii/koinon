"""Bounded private JSON publication under an already-held service ownership lock."""
import json
import errno
import os
from pathlib import Path
import stat

import platform_support

MAX_BYTES = 4096
MAX_DOCUMENT_BYTES = 1024 * 1024
MAX_PRIVATE_FILE_BYTES = 4 * 1024 * 1024


class StateReadBusyError(BlockingIOError):
    def __init__(self):
        super().__init__(errno.EAGAIN, 'state record changed during bounded read')


class StateFileError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def byte_limit(max_bytes):
    limit = MAX_BYTES if max_bytes is None else max_bytes
    if type(limit) is not int or not 0 < limit <= MAX_DOCUMENT_BYTES:
        raise ValueError('invalid state-file byte limit')
    return limit


def validate(info, *, max_bytes=None):
    _validate(info, byte_limit(max_bytes))


def _validate(info, limit):
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
            or info.st_mode & 0o077 or info.st_nlink != 1 or info.st_size > limit):
        raise StateFileError('unsafe_state_file')


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise StateFileError('invalid_state_file')
        result[key] = value
    return result


def open_validated(path, limit, *, writable=False):
    """Return an owned private regular-file descriptor, or None on absence.

    Caller closes the descriptor and bounds any reads. Binary callers may select
    up to 4 MiB; JSON callers retain their separate 1 MiB explicit ceiling and
    4 KiB default. Atomic replacement retries are shared by all callers.
    """
    if type(limit) is not int or not 0 < limit <= MAX_PRIVATE_FILE_BYTES:
        raise ValueError('invalid private-file byte limit')
    access = os.O_RDWR if writable else os.O_RDONLY
    for attempt in range(3):
        try:
            fd = os.open(path, access | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
        except FileNotFoundError:
            return None
        try:
            info = os.fstat(fd)
            if info.st_nlink == 0:
                # Atomic publication may unlink our opened predecessor before
                # fstat. Reopen the current name; never relax link validation or
                # consume the detached descriptor's contents.
                try:
                    current = os.lstat(path)
                except FileNotFoundError:
                    os.close(fd)
                    return None
                if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
                    os.close(fd)
                    continue
            _validate(info, limit)
        except BaseException:
            os.close(fd)
            raise
        break
    else:
        raise StateReadBusyError()
    return fd


def _read_value(fd, limit):
    data = bytearray()
    while len(data) <= limit:
        chunk = os.read(fd, limit + 1 - len(data))
        if not chunk:
            break
        data.extend(chunk)
    if len(data) > limit:
        raise StateFileError('unsafe_state_file')
    try:
        value = json.loads(data, object_pairs_hook=pairs)
    except (ValueError, UnicodeError, RecursionError) as exc:
        raise StateFileError('invalid_state_file') from exc
    if not isinstance(value, dict):
        raise StateFileError('invalid_state_file')
    return value


def read(path, *, max_bytes=None):
    limit = byte_limit(max_bytes)
    fd = open_validated(path, limit)
    if fd is None:
        return None
    try:
        return _read_value(fd, limit)
    finally:
        os.close(fd)


def confirm(path, expected, *, max_bytes=None):
    """Confirm retained bytes and their durability without rewriting the record.

    The caller holds the same ownership lock used for publication. This is for
    recovery from an ambiguous publication result, including a failed directory
    flush after replacement. Absence, substitution, or another flush failure
    refuses; ordinary read() remains nonmutating and does not promise durability.
    """
    limit = byte_limit(max_bytes)
    path = Path(path)
    directory = path.parent.lstat()
    if (not stat.S_ISDIR(directory.st_mode) or directory.st_uid != os.geteuid()
            or directory.st_mode & 0o077):
        raise StateFileError('unsafe_state_directory')
    fd = open_validated(path, limit, writable=True)
    if fd is None:
        raise StateFileError('missing_state_file')
    try:
        original = os.fstat(fd)
        value = _read_value(fd, limit)
        def encoded(item):
            return json.dumps(item, sort_keys=True, separators=(',', ':'), allow_nan=False)
        if encoded(value) != encoded(expected):
            raise StateFileError('state_file_changed')
        def stamp(info):
            return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
        def unchanged():
            named, opened, parent = path.lstat(), os.fstat(fd), path.parent.lstat()
            validate(named, max_bytes=limit)
            validate(opened, max_bytes=limit)
            if (stamp(named) != stamp(original) or stamp(opened) != stamp(original)
                    or (parent.st_dev, parent.st_ino) != (directory.st_dev, directory.st_ino)
                    or not stat.S_ISDIR(parent.st_mode) or parent.st_uid != os.geteuid()
                    or parent.st_mode & 0o077):
                raise StateFileError('state_file_changed')
        unchanged()
        platform_support.sync_state_file(fd)
        platform_support.sync_state_directory(path.parent)
        platform_support.sync_state_file(fd)
        unchanged()
        return value
    finally:
        os.close(fd)


def publish(path, value, *, max_bytes=None):
    """Publish or raise; an error after replacement has an unknown durable outcome.

    The caller serializes this with the notifier's state/participant ownership.
    A fixed replacement name bounds retained scratch files across process crashes.
    No caller may treat an exception as proof that the replacement did not occur.
    """
    limit = byte_limit(max_bytes)
    path = Path(path)
    if not isinstance(value, dict):
        raise StateFileError('invalid_state_file')
    data = json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()
    if len(data) > limit:
        raise StateFileError('state_file_capacity')
    directory = path.parent.lstat()
    if (not stat.S_ISDIR(directory.st_mode) or directory.st_uid != os.geteuid()
            or directory.st_mode & 0o077):
        raise StateFileError('unsafe_state_directory')
    try:
        validate(path.lstat(), max_bytes=limit)
    except FileNotFoundError:
        pass
    temp = path.with_name(path.name + '.tmp')
    try:
        validate(temp.lstat(), max_bytes=limit)
    except FileNotFoundError:
        pass
    else:
        # Only this fixed, private replacement belongs to the held ownership lock.
        temp.unlink()
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        offset = 0
        while offset < len(data):
            written = os.write(fd, data[offset:])
            if written <= 0:
                raise OSError('state write made no progress')
            offset += written
        platform_support.sync_state_file(fd)
        os.replace(temp, path)
        platform_support.sync_state_directory(path.parent)
        # On macOS this also requests a device flush after directory metadata sync.
        platform_support.sync_state_file(fd)
    finally:
        os.close(fd)
        temp.unlink(missing_ok=True)
