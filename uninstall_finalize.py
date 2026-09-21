"""Self-contained, resumable runtime-file removal; no service or state-directory deletion."""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import stat
import tempfile
from platform_support import sync_state_file, sync_state_directory

MANIFEST = '.uninstall-files.json'
RECOVERY = '.uninstall-finalize.py'
ALLOWED_FILES = ()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_owned(path, *, private=False):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1
                or info.st_mode & (0o077 if private else 0o022) or info.st_size > 4 * 1024 * 1024):
            raise ValueError('unsafe removal file: ' + str(path))
        with os.fdopen(os.dup(fd), 'rb') as stream:
            content = stream.read(4 * 1024 * 1024 + 1)
        current = path.lstat()
        if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino) or len(content) > 4 * 1024 * 1024:
            raise ValueError('removal file changed: ' + str(path))
        return content
    finally:
        os.close(fd)


def publish(path, content):
    fd, temporary = tempfile.mkstemp(prefix='.uninstall-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(content)
            stream.flush()
            sync_state_file(stream.fileno())
        os.replace(temporary, path)
        sync_state_directory(path.parent)
        with path.open('rb') as stream:
            sync_state_file(stream.fileno())
    finally:
        Path(temporary).unlink(missing_ok=True)


def selected_path(prefix, name):
    relative = Path(name)
    if relative.is_absolute() or not relative.parts or any(part in ('.', '..') for part in relative.parts):
        raise ValueError('invalid runtime removal path')
    path = prefix / relative
    for parent in (prefix, *[prefix.joinpath(*relative.parts[:n]) for n in range(1, len(relative.parts))]):
        try:
            info = parent.lstat()
        except FileNotFoundError:
            return path
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022):
            raise ValueError('unsafe runtime removal directory: ' + str(parent))
    return path


def prepare(prefix, names):
    prefix = Path(prefix)
    if read_owned(prefix / MANIFEST, private=True) is not None:
        raise ValueError('runtime cleanup already prepared; run ' + str(prefix / RECOVERY))
    files = {}
    for name in names:
        data = read_owned(selected_path(prefix, name))
        files[name] = None if data is None else digest(data)
    config = read_owned(prefix / 'install.json')
    from platform_support import standalone_state_sync_source
    program = Path(__file__).read_bytes().replace(b'ALLOWED_FILES = ()',
                                                ('ALLOWED_FILES = ' + repr(tuple(names))).encode(), 1)
    program = program.replace(
        b'from platform_support import sync_state_file, sync_state_directory\n',
        standalone_state_sync_source().encode(), 1)
    current = read_owned(prefix / RECOVERY, private=True)
    if current is not None and current != program:
        raise ValueError('unrelated removal recovery program')
    publish(prefix / RECOVERY, program)
    manifest = dict(version=1, prefix=str(prefix), files=files,
                    configuration=None if config is None else digest(config))
    publish(prefix / MANIFEST, json.dumps(manifest, sort_keys=True).encode())


@contextmanager
def ownership_lock(prefix, held):
    if held:
        yield
        return
    fd = os.open(prefix / '.install.lock', os.O_RDWR | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise ValueError('unsafe installation lock')
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        os.close(fd)


def finish(prefix, *, locked=False, names=()):
    prefix = Path(prefix)
    with ownership_lock(prefix, locked):
        raw = read_owned(prefix / MANIFEST, private=True)
        value = json.loads(raw) if raw is not None else None
        if (not isinstance(value, dict) or set(value) != {'version', 'prefix', 'files', 'configuration'}
                or value['version'] != 1 or value['prefix'] != str(prefix)
                or not isinstance(value['files'], dict) or len(value['files']) > 256
                or set(value['files']) != set(ALLOWED_FILES or names)):
            raise ValueError('invalid runtime removal manifest')
        selected = []
        for name, expected in value['files'].items():
            path = selected_path(prefix, name)
            data = read_owned(path)
            if data is not None:
                if digest(data) != expected:
                    raise ValueError('runtime file changed during removal: ' + str(path))
                selected.append((path, expected))
        config = read_owned(prefix / 'install.json')
        if (config is None and selected and value['configuration'] is not None
                or config is not None and digest(config) != value['configuration']):
            raise ValueError('installation configuration changed during removal')
        for recovery_name in (RECOVERY, MANIFEST):
            recovery_path = prefix / recovery_name
            fd = os.open(recovery_path, os.O_RDONLY | os.O_NOFOLLOW)
            try:
                sync_state_file(fd)
                sync_state_directory(prefix)
                sync_state_file(fd)
            finally:
                os.close(fd)
        for path, expected in selected:
            data = read_owned(path)
            if data is not None:
                if digest(data) != expected:
                    raise ValueError('runtime file changed before removal: ' + str(path))
                path.unlink()
                sync_state_directory(path.parent)
        if config is not None:
            if read_owned(prefix / 'install.json') != config:
                raise ValueError('installation configuration changed before removal')
            (prefix / 'install.json').unlink()
            sync_state_directory(prefix)
        (prefix / MANIFEST).unlink()
        (prefix / RECOVERY).unlink()
        sync_state_directory(prefix)
    return dict(status='removed', retained='state, archives and permanent locks')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prefix', type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(finish(args.prefix)))
