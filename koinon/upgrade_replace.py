"""Resumable runtime file replacement under retained writer exclusion.

Only the frozen source is published. A runtime path the new release no longer
ships is retired only where `upgrade_layout` declares where it moved to, and only
after its replacement is published and confirmed, so an interruption always leaves
a readable runtime and the frozen backup still holds every old path.
The coordinator must call preflight before shutting down selected services.
"""
import hashlib
import importlib.util
import os
from pathlib import Path
import stat
import tempfile

from koinon import durable_state
from koinon import platform_support
from koinon import upgrade_backup
from koinon.upgrade_documents import Documents
from koinon import upgrade_layout
from koinon import upgrade_manifest as manifest


class ReplacementError(ValueError):
    pass


def retirements(source, runtime):
    """Old runtime paths this release declares as moved, oldest path first.

    A path the new release does not ship is only ever a declared move. An
    undeclared disappearance refuses here, before anything is shut down.
    """
    try:
        return upgrade_layout.declared(set(runtime['files']) - set(source['files']), source['files'])
    except upgrade_layout.LayoutError as error:
        raise ReplacementError(str(error)) from None


def preflight(source, runtime):
    manifest.validate(source)
    manifest.validate(runtime)
    retired = retirements(source, runtime)
    for name in (*source['files'], *(old for old, _ in retired)):
        if name == 'install.json' or name == '.install.lock' or Path(name).parts[0] == '.upgrade':
            raise ReplacementError('runtime file selection overlaps upgrade ownership state')
    return manifest.names_checked(list(source['files']))


def _content(root, name):
    try:
        data = manifest.read_selected(root, name)
    except FileNotFoundError:
        return None
    return dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest())


def _parents(root, name):
    current = root
    device = root.lstat().st_dev
    for part in Path(name).parts[:-1]:
        parent = current
        current = current / part
        try:
            current.lstat()
        except FileNotFoundError:
            current.mkdir(mode=0o700)
        info = manifest.check_root(current).lstat()
        if info.st_dev != device:
            raise ReplacementError('runtime publication crosses a filesystem boundary')
        platform_support.sync_state_directory(parent)
        platform_support.sync_state_directory(current)


def _confirm(root, name, expected):
    path = root / name
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_nlink != 1 or info.st_mode & 0o022):
            raise ReplacementError('unsafe published runtime file')
        if _content(root, name) != expected:
            raise ReplacementError('published runtime content changed')
        platform_support.sync_state_file(fd)
        platform_support.sync_state_directory(path.parent)
        platform_support.sync_state_file(fd)
        named = path.lstat()
        if (named.st_dev, named.st_ino) != (info.st_dev, info.st_ino) or _content(root, name) != expected:
            raise ReplacementError('runtime file changed during confirmation')
    finally:
        os.close(fd)


def _backup_evidence(guard, phase):
    documents = Documents(guard.exclusion.journal.directory)
    value = documents.read('backups', phase['receipts'][3])
    if not isinstance(value, dict) or set(value) != {'version', 'runtime', 'components'} or value['version'] != 1:
        raise ReplacementError('invalid aggregate backup completion')
    runtime = value['runtime']
    old = guard.exclusion.loaded['documents']['runtime']
    if documents.read('capture-runtime-source', runtime['source_document']) != dict(version=1, source=runtime['source']):
        raise ReplacementError('runtime source evidence changed')
    if documents.read('capture-runtime-backup', runtime['backup_document']) != runtime['backup']:
        raise ReplacementError('runtime backup evidence changed')
    captured = upgrade_backup.validate(runtime['source'])
    if (captured['root'] != old['root'] or set(captured['files']) != set(old['files'])
            or any(captured['files'][name] is None or
                   {key: captured['files'][name][key] for key in ('bytes', 'sha256')} != expected
                   for name, expected in old['files'].items())
            or runtime['backup']['source'] != captured['sha256']):
        raise ReplacementError('runtime backup does not match the frozen old runtime')
    upgrade_backup.verify(runtime['backup']['destination'])
    components = value['components']
    frozen = guard.exclusion.loaded['documents']['components']['items']
    if (not isinstance(components, dict) or set(components) != {'version', 'components'}
            or components['version'] != 1 or not isinstance(components['components'], list)
            or len(components['components']) != len(frozen)):
        raise ReplacementError('aggregate component backup is incomplete')
    for index, (entry, component) in enumerate(zip(components['components'], frozen)):
        if entry['kind'] != component['kind'] or entry['selection'] != component['selection']:
            raise ReplacementError('component backup selection differs from the frozen plan')
        source = documents.read(f'capture-{index:03d}-source', entry['source_document'])
        if source != dict(version=1, component=manifest.fingerprint(component), source=entry['source']):
            raise ReplacementError('component source evidence differs')
        if documents.read(f'capture-{index:03d}-backup', entry['backup_document']) != entry['backup']:
            raise ReplacementError('component backup evidence differs')
        if entry['backup']['source'] != entry['source']['sha256']:
            raise ReplacementError('component backup has the wrong source')
        upgrade_backup.verify(entry['source'])
        upgrade_backup.verify(entry['backup']['destination'])
    return value


def bytecode_paths(root, names):
    """Check only this interpreter's derived caches for selected Python sources.

    Unchecked-hash caches can remain valid across arbitrary source replacement;
    relying on mtimes or Python's normal invalidation is insufficient. Unknown
    files elsewhere in __pycache__ are outside this selection and are preserved.
    """
    root = manifest.check_root(root)
    selected = []
    for name in manifest.names_checked(list(names)):
        if not name.endswith('.py'):
            continue
        for optimization in ('', '1', '2'):
            path = Path(importlib.util.cache_from_source(str(root / name), optimization=optimization))
            try:
                parent = path.parent.lstat()
            except FileNotFoundError:
                continue
            manifest.check_root(path.parent)
            try:
                info = path.lstat()
            except FileNotFoundError:
                continue
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_nlink != 1 or info.st_mode & 0o022):
                raise ReplacementError('unsafe selected Python bytecode cache; preserve it')
            selected.append((path, (info.st_dev, info.st_ino), (parent.st_dev, parent.st_ino)))
    return selected


def _invalidate_bytecode(guard, root, names):
    guard.verify()
    for path, identity, parent_identity in bytecode_paths(root, names):
        manifest.check_root(path.parent)
        parent, named = path.parent.lstat(), path.lstat()
        if ((named.st_dev, named.st_ino) != identity
                or (parent.st_dev, parent.st_ino) != parent_identity):
            raise ReplacementError('selected Python cache changed before invalidation')
        path.unlink()
        platform_support.sync_state_directory(path.parent)
    guard.verify()


def _retire(guard, root, runtime, retired, progress, progress_path):
    """Remove each declared old path, after its replacement is confirmed.

    The postimage is published and confirmed before the preimage goes away, so an
    interruption leaves a runtime that still reads. A path already gone is a
    completed step on resume, not a reason to refuse; a path whose content is
    neither its frozen preimage nor absent is an operator change and refuses.
    """
    if retired:
        _invalidate_bytecode(guard, root, [old for old, _ in retired])
    for index in range(progress['retired'], len(retired)):
        old, new = retired[index]
        guard.verify()
        _confirm(root, new, guard.exclusion.loaded['documents']['source']['files'][new])
        current = _content(root, old)
        if current is not None and current != runtime['files'].get(old):
            raise ReplacementError('retired runtime path changed before removal')
        durable_state.publish(progress_path, dict(progress, retired=index))
        if current is not None:
            path = root / old
            path.unlink()
            platform_support.sync_state_directory(path.parent)
        if _content(root, old) is not None:
            raise ReplacementError('retired runtime path is still present')
        progress = dict(progress, retired=index + 1)
        durable_state.publish(progress_path, progress)
    guard.verify()
    return progress


def replace(guard):
    """Publish or reconfirm every file; never advance the global phase journal."""
    guard.verify()
    phase = guard.exclusion.journal.read()
    if phase['step'] != 8:
        raise ReplacementError('replacement requires its pending phase')
    loaded = guard.exclusion.loaded
    source, runtime = (loaded['documents'][name] for name in ('source', 'runtime'))
    names = preflight(source, runtime)
    retired = retirements(source, runtime)
    manifest.verify(source)
    _backup_evidence(guard, phase)
    root = manifest.check_root(runtime['root'])
    bytecode_paths(root, names)
    progress_path = guard.exclusion.journal.directory / 'replacement-progress.json'
    progress = durable_state.read(progress_path)
    if progress is None:
        for name in names:
            if _content(root, name) != runtime['files'].get(name):
                raise ReplacementError('runtime preimage changed before replacement intent')
        progress = dict(version=1, plan=loaded['sha256'], index=0, retired=0)
    if (not isinstance(progress, dict) or set(progress) != {'version', 'plan', 'index', 'retired'}
            or type(progress['version']) is not int or progress['version'] != 1
            or progress['plan'] != loaded['sha256'] or type(progress['index']) is not int
            or not 0 <= progress['index'] <= len(names) or type(progress['retired']) is not int
            or not 0 <= progress['retired'] <= len(retired)
            or progress['retired'] and progress['index'] != len(names)):
        raise ReplacementError('invalid retained replacement progress')
    # Reconfirm completed postimages. Missing evidence is not permission to
    # overwrite a substituted file or reconstruct earlier completed work.
    for name in names[:progress['index']]:
        _confirm(root, name, source['files'][name])
    for index in range(progress['index'], len(names)):
        name, desired = names[index], source['files'][names[index]]
        guard.verify()
        current = _content(root, name)
        if current not in (runtime['files'].get(name), desired):
            raise ReplacementError('runtime path has neither its frozen preimage nor postimage')
        # The durable index identifies the pending file before publication.
        durable_state.publish(progress_path, dict(progress, index=index))
        _parents(root, name)
        if current != desired:
            data = manifest.read_selected(Path(source['root']), name)
            if dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest()) != desired:
                raise ReplacementError('selected source changed before publication')
            path = root / name
            fd, temporary = tempfile.mkstemp(prefix='.upgrade-runtime-', dir=path.parent)
            try:
                with os.fdopen(fd, 'wb') as stream:
                    stream.write(data)
                    stream.flush()
                    platform_support.sync_state_file(stream.fileno())
                    if _content(root, name) != current:
                        raise ReplacementError('runtime preimage changed during staging')
                    os.replace(temporary, path)
                    platform_support.sync_state_directory(path.parent)
                    platform_support.sync_state_file(stream.fileno())
            finally:
                Path(temporary).unlink(missing_ok=True)
        _confirm(root, name, desired)
        progress = dict(progress, index=index + 1)
        durable_state.publish(progress_path, progress)
    guard.verify()
    _invalidate_bytecode(guard, root, names)
    _retire(guard, root, runtime, retired, progress, progress_path)
    actual = manifest.capture(root, list(names))
    if actual['files'] != source['files']:
        raise ReplacementError('runtime differs from frozen source after replacement')
    return dict(version=1, plan=loaded['sha256'], runtime=actual,
                retired=[old for old, _ in retired])


def confirm(guard):
    """Revalidate recorded replacement completion without repeating publication."""
    guard.verify()
    phase = guard.exclusion.journal.read()
    if phase['step'] != 9:
        raise ReplacementError('replacement confirmation requires its completed phase')
    _backup_evidence(guard, phase)
    loaded = guard.exclusion.loaded
    source = loaded['documents']['source']
    for name, expected in source['files'].items():
        _confirm(Path(loaded['plan']['canonical_prefix']), name, expected)
    _invalidate_bytecode(guard, Path(loaded['plan']['canonical_prefix']), list(source['files']))
    current = manifest.capture(loaded['plan']['canonical_prefix'], list(source['files']))
    prefix = Path(loaded['plan']['canonical_prefix'])
    retired = retirements(source, loaded['documents']['runtime'])
    for old_name, _ in retired:
        if _content(prefix, old_name) is not None:
            raise ReplacementError('retired runtime path is present again')
    receipt = Documents(guard.exclusion.journal.directory).read('replacement', phase['receipts'][4])
    if receipt != dict(version=1, plan=loaded['sha256'], runtime=current,
                       retired=[old_name for old_name, _ in retired]):
        raise ReplacementError('replacement completion differs from current runtime')
    guard.verify()
    return receipt
