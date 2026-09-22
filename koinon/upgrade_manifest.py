"""Bounded, read-only manifests for explicitly selected upgrade files.

No discovery, copying, installation, deletion or service action occurs here. The
coordinator owns the file allowlist and must quiesce state writers before using
this for a backup. A runtime manifest alone is not a consistent state backup.
"""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import stat

from koinon import path_permissions

MAX_FILES = 256
MAX_FILE_BYTES = 4 << 20
MAX_TOTAL_BYTES = 64 << 20


class ManifestError(ValueError):
    pass


def names_checked(names):
    if not isinstance(names, (tuple, list)) or not 0 < len(names) <= MAX_FILES:
        raise ManifestError('bounded explicit file list required')
    result = []
    for name in names:
        if not isinstance(name, str) or not name or len(name.encode()) > 1024 or '\x00' in name:
            raise ManifestError('invalid manifest name')
        path = PurePosixPath(name)
        if (not path.parts or path.is_absolute() or any(part in ('.', '..') for part in path.parts)
                or str(path) != name or name in result):
            raise ManifestError('duplicate or noncanonical manifest name')
        result.append(name)
    return tuple(sorted(result))


def select_root(root):
    """Resolve the explicit selection once, including OS aliases such as /var.

    The resulting canonical root is frozen in the manifest. Subsequent file reads
    validate that exact path without resolving newly introduced symlinks.
    """
    root = Path(root)
    if not root.is_absolute() or '..' in root.parts or len(str(root).encode()) > 4096:
        raise ManifestError('absolute manifest root required')
    return check_root(root.resolve(strict=True))


def check_root(root):
    root = Path(root)
    if not root.is_absolute() or '..' in root.parts or len(str(root).encode()) > 4096:
        raise ManifestError('absolute manifest root required')
    for parent in (*reversed(root.parents), root):
        info = parent.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, os.geteuid())
                or path_permissions.writable_by_others(info)
                and not path_permissions.temporary_root(info)):
            raise ManifestError('unsafe manifest ancestor: '
                                + path_permissions.describe(parent, info))
    info = root.lstat()
    if info.st_uid != os.geteuid() or path_permissions.writable_by_others(info):
        raise ManifestError('unsafe manifest root: ' + path_permissions.describe(root, info))
    return root


def _stamp(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def read_selected(root, name):
    """Read one owned regular file, checking ancestor and file identity twice."""
    names_checked((name,))
    root = check_root(root)
    path = root / name
    for parent in reversed(path.relative_to(root).parents):
        selected = root / parent
        info = selected.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
            raise ManifestError('unsafe selected directory: ' + str(selected))
    parents = {}
    for parent in path.parents:
        info = parent.lstat()
        parents[parent] = (info.st_dev, info.st_ino)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o022 or info.st_nlink != 1 or info.st_size > MAX_FILE_BYTES):
            raise ManifestError('unsafe or oversized selected file: ' + str(path))
        with os.fdopen(os.dup(fd), 'rb') as stream:
            content = stream.read(MAX_FILE_BYTES + 1)
        if (len(content) > MAX_FILE_BYTES or _stamp(os.fstat(fd)) != _stamp(info)
                or _stamp(path.lstat()) != _stamp(info)):
            raise ManifestError('selected file changed during read: ' + str(path))
        # Refuse a renamed parent even if its old descriptor returned good bytes.
        check_root(root)
        for parent, identity in parents.items():
            current = parent.lstat()
            if (current.st_dev, current.st_ino) != identity:
                raise ManifestError('selected directory changed during read: ' + str(parent))
            if parent != root and root in parent.parents and (
                    not stat.S_ISDIR(current.st_mode) or current.st_uid != os.geteuid()
                    or current.st_mode & 0o022):
                raise ManifestError('selected directory changed during read: ' + str(parent))
        return content
    finally:
        os.close(fd)


def capture(root, names):
    return _capture_selected(select_root(root), names)


def _capture_selected(root, names):
    root = check_root(root)
    files, total = {}, 0
    for name in names_checked(names):
        content = read_selected(root, name)
        total += len(content)
        if total > MAX_TOTAL_BYTES:
            raise ManifestError('manifest total byte limit exceeded')
        files[name] = dict(bytes=len(content), sha256=hashlib.sha256(content).hexdigest())
    result = dict(version=1, root=str(root), files=files, bytes=total)
    result['sha256'] = fingerprint(result)
    return result


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'),
                                     ensure_ascii=True, allow_nan=False).encode()).hexdigest()


def hex_digest(value):
    return (isinstance(value, str) and len(value) == 64
            and all(char in '0123456789abcdef' for char in value))


def validate(expected):
    """Validate frozen manifest structure without assuming the old files still exist."""
    if (not isinstance(expected, dict)
            or set(expected) != {'version', 'root', 'files', 'bytes', 'sha256'}
            or type(expected['version']) is not int or expected['version'] != 1
            or not isinstance(expected['files'], dict)
            or not isinstance(expected['root'], str)
            or type(expected['bytes']) is not int or not 0 <= expected['bytes'] <= MAX_TOTAL_BYTES
            or not hex_digest(expected['sha256'])):
        raise ManifestError('invalid manifest')
    names_checked(list(expected['files']))
    for value in expected['files'].values():
        if (not isinstance(value, dict) or set(value) != {'bytes', 'sha256'}
                or type(value['bytes']) is not int or not 0 <= value['bytes'] <= MAX_FILE_BYTES
                or not hex_digest(value['sha256'])):
            raise ManifestError('invalid manifest file entry')
    if sum(value['bytes'] for value in expected['files'].values()) != expected['bytes']:
        raise ManifestError('manifest byte count mismatch')
    unsigned = {key: value for key, value in expected.items() if key != 'sha256'}
    if fingerprint(unsigned) != expected['sha256']:
        raise ManifestError('manifest fingerprint mismatch')
    return expected


def verify(expected):
    """Require the entire frozen selection and bytes; never adopt a changed source."""
    expected = validate(expected)
    # The stored root is already canonical. Never follow a newly substituted
    # alias while resuming an operation against this frozen identity.
    actual = _capture_selected(check_root(expected['root']), list(expected['files']))
    if actual != expected:
        raise ManifestError('selected source differs from frozen manifest')
    return actual
