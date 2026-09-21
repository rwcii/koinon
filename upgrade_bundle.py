"""Prepare a frozen, private Python recovery archive before runtime replacement.

The coordinator supplies the complete dependency allowlist and a plan-bound
source manifest. This helper neither chooses a release nor runs the archive.
"""
import hashlib
import io
import os
from pathlib import Path
import stat
import zipfile

import durable_state
import platform_support
from participant_lock import file_lock
import upgrade_manifest as manifest

ARCHIVE = 'recovery.pyz'
MAX_BYTES = min(manifest.MAX_FILE_BYTES, durable_state.MAX_PRIVATE_FILE_BYTES)


class BundleError(ValueError):
    pass


def _private_directory(root):
    root = manifest.check_root(root)
    info = root.lstat()
    if info.st_mode & 0o077:
        raise BundleError('recovery directory must be private')
    return root


def _private_file(info):
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
            or info.st_mode & 0o077 or info.st_nlink != 1 or info.st_size > MAX_BYTES):
        raise BundleError('unsafe recovery archive')


def _stamp(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def _confirm(root, expected):
    """Reconfirm bytes, name and durability, including an earlier failed rename flush."""
    path = root / ARCHIVE
    directory = root.lstat()
    fd = durable_state.open_validated(path, MAX_BYTES, writable=True)
    if fd is None:
        raise BundleError('recovery archive missing')
    try:
        original = os.fstat(fd)
        _private_file(original)
        if manifest.read_selected(root, ARCHIVE) != expected:
            raise BundleError('retained recovery archive differs; preserve it')
        def unchanged():
            _private_directory(root)
            named, opened, parent = path.lstat(), os.fstat(fd), root.lstat()
            _private_file(named)
            _private_file(opened)
            if (_stamp(named) != _stamp(original) or _stamp(opened) != _stamp(original)
                    or (parent.st_dev, parent.st_ino) != (directory.st_dev, directory.st_ino)):
                raise BundleError('recovery archive ownership changed')
        unchanged()
        platform_support.sync_state_file(fd)
        platform_support.sync_state_directory(root)
        platform_support.sync_state_file(fd)
        unchanged()
    finally:
        os.close(fd)


def _publish(root, content):
    path = root / ARCHIVE
    try:
        info = path.lstat()
    except FileNotFoundError:
        pass
    else:
        _private_file(info)
        _confirm(root, content)
        return
    temporary = root / (ARCHIVE + '.tmp')
    try:
        _private_file(temporary.lstat())
    except FileNotFoundError:
        pass
    else:
        temporary.unlink()
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        offset = 0
        while offset < len(content):
            count = os.write(fd, content[offset:])
            if count <= 0:
                raise OSError('recovery archive write made no progress')
            offset += count
        platform_support.sync_state_file(fd)
        os.replace(temporary, path)
        platform_support.sync_state_directory(root)
        platform_support.sync_state_file(fd)
    finally:
        os.close(fd)
        temporary.unlink(missing_ok=True)


def prepare(directory, source, entrypoint):
    """Freeze explicit source bytes into a deterministic archive; never overwrite a different one."""
    root = _private_directory(manifest.select_root(directory))
    source = manifest.verify(source)
    manifest.names_checked((entrypoint,))
    if (entrypoint not in source['files'] or not entrypoint.endswith('.py')
            or '__main__.py' in source['files']):
        raise BundleError('explicit Python entrypoint and unambiguous archive names required')
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_STORED) as archive:
        for name in sorted(source['files']):
            content = manifest.read_selected(source['root'], name)
            expected = source['files'][name]
            if len(content) != expected['bytes'] or hashlib.sha256(content).hexdigest() != expected['sha256']:
                raise BundleError('source changed during recovery preparation')
            for selected in ((name, '__main__.py') if name == entrypoint else (name,)):
                info = zipfile.ZipInfo(selected, date_time=(1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (stat.S_IFREG | 0o600) << 16
                archive.writestr(info, content)
                if output.tell() > MAX_BYTES:
                    raise BundleError('recovery archive capacity exceeded')
    content = output.getvalue()
    if len(content) > MAX_BYTES:
        raise BundleError('recovery archive capacity exceeded')
    manifest.verify(source)
    directory_identity = root.stat()
    lock = root / 'bundle.lock'
    with file_lock(lock, 'upgrade_bundle_busy', None) as fd:
        named, opened = lock.lstat(), os.fstat(fd)
        _private_directory(root)
        current = root.stat()
        if ((named.st_dev, named.st_ino) != (opened.st_dev, opened.st_ino)
                or (current.st_dev, current.st_ino) != (directory_identity.st_dev, directory_identity.st_ino)):
            raise BundleError('recovery bundle ownership changed')
        _publish(root, content)
        result = dict(version=1, source=manifest.fingerprint(source), entrypoint=entrypoint,
                      archive=manifest.capture(root, (ARCHIVE,)))
    return result


def verify(expected, source, *, require_source=True):
    """Read-only verification against a descriptor already bound by the caller's plan."""
    if (not isinstance(expected, dict) or set(expected) != {'version', 'source', 'entrypoint', 'archive'}
            or type(expected['version']) is not int or expected['version'] != 1
            or not manifest.hex_digest(expected['source'])
            or not isinstance(expected['archive'], dict)
            or not isinstance(expected['archive'].get('files'), dict)):
        raise BundleError('invalid recovery bundle descriptor')
    if type(require_source) is not bool:
        raise BundleError('source verification selection must be boolean')
    source = manifest.verify(source) if require_source else manifest.validate(source)
    manifest.names_checked((expected['entrypoint'],))
    if (expected['source'] != manifest.fingerprint(source)
            or expected['entrypoint'] not in source['files']
            or not expected['entrypoint'].endswith('.py')
            or '__main__.py' in source['files']
            or set(expected['archive'].get('files', {})) != {ARCHIVE}):
        raise BundleError('recovery bundle does not match frozen source')
    archive = manifest.verify(expected['archive'])
    root = _private_directory(Path(archive['root']))
    _private_file((root / ARCHIVE).lstat())
    return root / ARCHIVE
