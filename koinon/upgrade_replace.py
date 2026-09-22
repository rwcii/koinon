"""Resumable runtime file replacement under retained writer exclusion.

Only the frozen source is published. A runtime path the new release no longer
ships is retired only where `upgrade_layout` declares where it moved to, and only
after its replacement is published and confirmed, so an interruption always leaves
a readable runtime and the frozen backup still holds every old path.
The coordinator must call preflight before shutting down selected services.
"""
import hashlib
from itertools import islice
import os
from pathlib import Path
import stat
import sys
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


# An untrusted directory may hold caches from at most this many interpreter tags
# (`cpython-313`, `cpython-314`, ...). With three optimization levels, a directory
# for N selected modules therefore holds at most N * 3 * MAX_CACHE_TAGS entries,
# a bound derived from the runtime rather than a fixed count.
MAX_CACHE_TAGS = 8
_CACHE_OPTIMIZATIONS = ('', '.opt-1', '.opt-2')


def _cache_name(name):
    """Split `<stem>.<tag>[.opt-N].pyc` into (stem, tag), or return (None, None)."""
    if not name.endswith('.pyc'):
        return None, None
    parts = name[:-4].split('.')
    if parts[-1] in ('opt-1', 'opt-2'):
        parts = parts[:-1]
    if (len(parts) != 2 or not parts[0].isidentifier() or not parts[1]
            or not all(char.isalnum() or char in '_-' for char in parts[1])):
        return None, None
    return parts[0], parts[1]


def _cache_entry(name):
    return _cache_name(name)[0]


def _cache_sources(root, names):
    """Map each runtime cache directory to the selected source stems it serves.

    The path is derived from the runtime layout, never from
    `importlib.util.cache_from_source`: that follows `sys.pycache_prefix`, which
    the coordinator sets to a private directory, and would then select the wrong
    caches to check, quarantine and invalidate.
    """
    directories = {}
    for name in manifest.names_checked(list(names)):
        if name.endswith('.py'):
            path = root / name
            directories.setdefault(path.parent / '__pycache__', set()).add(path.stem)
    return directories


def _safe(info, kind):
    return kind(info.st_mode) and info.st_uid == os.geteuid() and not info.st_mode & 0o022


def _classify(directory, stems):
    """Return None when absent, 'trusted' or 'untrusted'; refuse what cannot move safely.

    A trusted directory is owned and writable only by its owner, and each cache
    for a selected source in it is a private regular file. Unrelated files in a
    trusted directory are preserved, as before. An untrusted directory may hold
    only caches for selected sources, from at most MAX_CACHE_TAGS interpreters, so
    that quarantine never carries away data the operator did not create by importing.
    """
    try:
        info = directory.lstat()
    except FileNotFoundError:
        return None
    parent = manifest.check_root(directory.parent)
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid():
        raise ReplacementError('untrusted_cache_contents: %s is not an owned directory' % directory)
    # Only the owner can add entries to a directory without group or other write,
    # so its full listing is bounded by the owner; a writable one is read bounded.
    limit = len(stems) * len(_CACHE_OPTIMIZATIONS) * MAX_CACHE_TAGS
    trusted = not info.st_mode & 0o022
    with os.scandir(directory) as scan:
        entries = sorted(entry.name for entry in (scan if trusted else islice(scan, limit + 1)))
    for name in entries:
        if trusted and _cache_entry(name) in stems:
            value = (directory / name).lstat()
            if not _safe(value, stat.S_ISREG) or value.st_nlink != 1:
                trusted = False
    if trusted:
        return 'trusted'
    if len(entries) > limit:
        raise ReplacementError('untrusted_cache_contents: %s holds more than %d entries'
                               % (directory, limit))
    tags = {_cache_name(name)[1] for name in entries if _cache_entry(name) in stems}
    if len(tags) > MAX_CACHE_TAGS:
        raise ReplacementError('untrusted_cache_contents: %s holds caches for more than %d '
                               'interpreters' % (directory, MAX_CACHE_TAGS))
    for name in entries:
        value = (directory / name).lstat()
        if (_cache_entry(name) not in stems or not stat.S_ISREG(value.st_mode)
                or value.st_uid != os.geteuid()):
            raise ReplacementError('untrusted_cache_contents: unsafe Python bytecode cache entry %s'
                                   % (directory / name))
    if info.st_dev != parent.lstat().st_dev:
        raise ReplacementError('untrusted_cache_contents: %s is on another filesystem' % directory)
    return 'untrusted'


def untrusted_caches(root, names):
    """Report group- or other-writable caches for quarantine; refuse unmovable ones.

    Preflight calls this before shutdown, so every refusal happens while the
    services still run. The coordinator never reads these caches: it runs from a
    private `sys.pycache_prefix` (docs/LEGACY-ADOPTION-DESIGN.md, G1).
    """
    root = manifest.check_root(root)
    return sorted(str(directory) for directory, stems in _cache_sources(root, names).items()
                  if _classify(directory, stems) == 'untrusted')


def quarantine(guard, root, names):
    """Move each untrusted cache directory into the operation, journaled and resumable.

    Each move records its identity before the rename and its completion after,
    so a resume finds the directory at exactly one of its two paths. The
    quarantined directory is retained, never deleted or chmodded.
    """
    guard.verify()
    root = manifest.check_root(root)
    directory = guard.exclusion.journal.directory
    plan = guard.exclusion.loaded['sha256']
    path = directory / 'cache-quarantine.json'
    progress = durable_state.read(path) or dict(version=1, plan=plan, items=[], completed=0)
    if (not isinstance(progress, dict) or set(progress) != {'version', 'plan', 'items', 'completed'}
            or progress['version'] != 1 or progress['plan'] != plan
            or not isinstance(progress['items'], list) or type(progress['completed']) is not int
            or not len(progress['items']) - 1 <= progress['completed'] <= len(progress['items'])):
        raise ReplacementError('invalid retained cache quarantine progress')
    target = directory / 'untrusted-cache'
    try:
        target.mkdir(mode=0o700)
    except FileExistsError:
        pass
    manifest.check_root(target)
    platform_support.sync_state_directory(directory)

    def settled(item):
        moved = Path(item['destination']).lstat()
        if (moved.st_dev, moved.st_ino) != (item['dev'], item['ino']):
            raise ReplacementError('quarantined cache identity changed')

    def move(item):
        source = Path(item['directory'])
        try:
            info = source.lstat()
        except FileNotFoundError:
            info = None
        if info is not None and (info.st_dev, info.st_ino) == (item['dev'], item['ino']):
            os.rename(source, item['destination'])
        settled(item)
        platform_support.sync_state_directory(source.parent)
        platform_support.sync_state_directory(target)

    for index in range(progress['completed'], len(progress['items'])):
        move(progress['items'][index])
        progress = dict(progress, completed=index + 1)
        durable_state.publish(path, progress)
    for item in progress['items']:
        settled(item)
    for source, stems in sorted(_cache_sources(root, names).items()):
        guard.verify()
        if _classify(source, stems) != 'untrusted':
            continue
        info = source.lstat()
        item = dict(directory=str(source), dev=info.st_dev, ino=info.st_ino,
                    destination=str(target / ('%03d' % len(progress['items']))))
        progress = dict(progress, items=[*progress['items'], item])
        durable_state.publish(path, progress)
        move(item)
        progress = dict(progress, completed=len(progress['items']))
        durable_state.publish(path, progress)
    guard.verify()
    return [item['directory'] for item in progress['items']]


def bytecode_paths(root, names):
    """Check only this interpreter's derived caches for selected Python sources.

    Unchecked-hash caches can remain valid across arbitrary source replacement;
    relying on mtimes or Python's normal invalidation is insufficient. Unknown
    files elsewhere in __pycache__ are outside this selection and are preserved.
    An untrusted directory must be quarantined before this is called.
    """
    root = manifest.check_root(root)
    tag = sys.implementation.cache_tag
    selected = []
    for directory, stems in sorted(_cache_sources(root, names).items()):
        state = _classify(directory, stems)
        if state is None or tag is None:
            continue
        if state != 'trusted':
            raise ReplacementError('unsafe selected Python bytecode cache; quarantine it first')
        parent = directory.lstat()
        for stem in sorted(stems):
            for optimization in _CACHE_OPTIMIZATIONS:
                path = directory / ('%s.%s%s.pyc' % (stem, tag, optimization))
                try:
                    info = path.lstat()
                except FileNotFoundError:
                    continue
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


def _absent(root, name):
    """Establish that a retired path is gone, and that its removal is durable."""
    if _content(root, name) is not None:
        raise ReplacementError('retired runtime path is still present')
    platform_support.sync_state_directory((root / name).parent)


def _retire(guard, root, runtime, retired, progress, progress_path):
    """Remove each declared old path, after its replacement is confirmed.

    The postimage is published and confirmed before the preimage goes away, so an
    interruption leaves a runtime that still reads. A path already gone is a
    completed step on resume, not a reason to refuse; a path whose content is
    neither its frozen preimage nor absent is an operator change and refuses.
    """
    if retired:
        _invalidate_bytecode(guard, root, [old for old, _ in retired])
    # Reconfirm the entries an earlier attempt recorded. A checkpoint is a claim
    # that the path is gone durably, so it is re-established, never assumed.
    for old, _ in retired[:progress['retired']]:
        _absent(root, old)
    for index in range(progress['retired'], len(retired)):
        old, new = retired[index]
        guard.verify()
        _confirm(root, new, guard.exclusion.loaded['documents']['source']['files'][new])
        current = _content(root, old)
        if current is not None and current != runtime['files'].get(old):
            raise ReplacementError('retired runtime path changed before removal')
        durable_state.publish(progress_path, dict(progress, retired=index))
        if current is not None:
            (root / old).unlink()
        # Absence and its directory flush are re-established before the checkpoint
        # on every attempt, including a resumed one whose unlink already happened.
        # An unlink whose flush failed is not a completed retirement.
        _absent(root, old)
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
    cached = [*names, *(old for old, _ in retired)]
    untrusted_caches(root, cached)
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
    quarantined = quarantine(guard, root, cached)
    _invalidate_bytecode(guard, root, names)
    _retire(guard, root, runtime, retired, progress, progress_path)
    actual = manifest.capture(root, list(names))
    if actual['files'] != source['files']:
        raise ReplacementError('runtime differs from frozen source after replacement')
    return dict(version=1, plan=loaded['sha256'], runtime=actual,
                retired=[old for old, _ in retired], quarantined=quarantined)


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
    prefix = Path(loaded['plan']['canonical_prefix'])
    retired = retirements(source, loaded['documents']['runtime'])
    quarantined = quarantine(guard, prefix, [*source['files'], *(old for old, _ in retired)])
    _invalidate_bytecode(guard, prefix, list(source['files']))
    current = manifest.capture(loaded['plan']['canonical_prefix'], list(source['files']))
    for old_name, _ in retired:
        _absent(prefix, old_name)
    receipt = Documents(guard.exclusion.journal.directory).read('replacement', phase['receipts'][4])
    if receipt != dict(version=1, plan=loaded['sha256'], runtime=current,
                       retired=[old_name for old_name, _ in retired], quarantined=quarantined):
        raise ReplacementError('replacement completion differs from current runtime')
    guard.verify()
    return receipt
