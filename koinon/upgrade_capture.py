"""Stopped component capture while retaining native startup exclusion.

The coordinator owns the operation lock, prepares private destinations before
shutdown, and keeps this guard alive through capture and runtime replacement.
This module never opens the selected SQLite databases.
"""
from contextlib import contextmanager, ExitStack
import os
from pathlib import Path
import stat

import memory_service
from koinon.participant_lock import file_lock
import session_service
from koinon import session_observation
from koinon import session_endpoints
from koinon import session_service_manager
from koinon import upgrade_backup
from koinon import upgrade_exclusion
from koinon.upgrade_documents import Documents
from koinon import upgrade_manifest
from koinon import upgrade_quiescence
from koinon import upgrade_reservation


class CaptureError(ValueError):
    pass


class Guard:
    def __init__(self, exclusion, stopped):
        self.exclusion, self.stopped = exclusion, stopped
        self.locks, self.endpoints, self.roots = [], [], []

    def verify(self):
        loaded = upgrade_exclusion.read(self.exclusion.prefix)
        if (loaded is None or loaded['sha256'] != self.exclusion.loaded['sha256']
                or loaded['phase']['step'] not in (6, 7, 8, 9)):
            raise CaptureError('capture exclusion or phase changed')
        for path, fd, identity in self.locks:
            opened, named = os.fstat(fd), path.lstat()
            if (opened.st_dev, opened.st_ino) != identity or (named.st_dev, named.st_ino) != identity:
                raise CaptureError('capture lock inode changed')
        for root, identity in self.roots:
            info = upgrade_manifest.check_root(root).lstat()
            if (info.st_dev, info.st_ino) != identity:
                raise CaptureError('selected state directory changed')
        for root, captured in self.endpoints:
            if session_endpoints.capture(root) != captured:
                raise CaptureError('capture endpoint reservation changed')
        for original, component in zip(self.stopped, self.exclusion.loaded['documents']['components']['items']):
            selected = upgrade_quiescence.selection(self.exclusion, component)
            if component['kind'] == 'session':
                owner = selected.records.read()
                portable = session_service.status(selected)
                manager = (dict(status='absent') if selected.backend == 'manual'
                           else session_service_manager.observation(selected))
                quiet = (portable['status'] in ('stopped', 'unobserved')
                         and not session_observation.endpoint_present(selected.home / 'notifier'))
            else:
                owner = memory_service.read_record(selected, selected.owner_path)
                manager = (dict(status='absent') if selected.backend == 'manual'
                           else memory_service.manager_observation(selected))
                quiet = (not session_observation.endpoint_present(selected.home)
                         and (owner is None or owner['configuration'] == selected.configuration
                              and memory_service.process_state(owner) == 'dead'
                              and (owner['child'] is None or memory_service.process_state(owner['child']) == 'dead')))
            if not quiet or owner != original['owner'] or manager.get('status') != 'absent':
                raise CaptureError('selected writer or manager changed while capture was excluded')


@contextmanager
def hold(exclusion):
    if exclusion.verify()['step'] not in (6, 7, 8, 9):
        raise CaptureError('capture requested outside backup/replacement phases')
    components = exclusion.loaded['documents']['components']['items']
    observed = []
    for component in components:
        selected = upgrade_quiescence.selection(exclusion, component)
        owner = (selected.records.read() if component['kind'] == 'session'
                 else memory_service.read_record(selected, selected.owner_path))
        observed.append(dict(owner=owner))
    guard = Guard(exclusion, observed)
    with ExitStack() as stack:
        for component in components:
            selected = upgrade_quiescence.selection(exclusion, component)
            home = upgrade_manifest.check_root(selected.home)
            info = home.lstat()
            guard.roots.append((home, (info.st_dev, info.st_ino)))
            names = (('lifecycle.lock', 'registration.lock', 'supervisor.lock', 'notifier.lock')
                     if component['kind'] == 'session' else ('manager.lock', 'supervisor.lock', 'start.lock'))
            for name in names:
                path = home / name
                fd = stack.enter_context(file_lock(path, 'upgrade_writer_in_use', None))
                info = os.fstat(fd)
                guard.locks.append((path, fd, (info.st_dev, info.st_ino)))
        guard.verify()
        for component in components:
            if component['kind'] == 'session':
                root = Path(component['selection']['state_directory'])
                captured = stack.enter_context(upgrade_reservation.hold(
                    exclusion.journal.directory, root, exclusion.loaded['sha256']))
                guard.endpoints.append((root, captured))
        guard.verify()
        yield guard
        guard.verify()


def names(root, *, databases, reserved=()):
    """Bounded complete regular-file inventory plus explicit SQLite sidecar absences."""
    root = upgrade_manifest.check_root(root)
    pending, selected, count = [root], [], 0
    reserved = {str(Path(path)) for path in reserved}
    while pending:
        directory = pending.pop()
        with os.scandir(directory) as entries:
            for entry in entries:
                count += 1
                if count > 256:
                    raise CaptureError('component state inventory exceeds supported capacity')
                path = Path(entry.path)
                info = entry.stat(follow_symlinks=False)
                if info.st_uid != os.geteuid() or info.st_mode & 0o022:
                    raise CaptureError('unsafe component state inventory entry')
                if str(path) in reserved and stat.S_ISSOCK(info.st_mode):
                    continue
                if stat.S_ISDIR(info.st_mode):
                    pending.append(path)
                elif stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
                    selected.append(str(path.relative_to(root)))
                else:
                    raise CaptureError('unexpected nonregular component state entry')
    selected.extend(name + suffix for name in databases for suffix in ('', '-wal', '-shm', '-journal'))
    return upgrade_manifest.names_checked(sorted(set(selected)))


def copy_components(guard, destinations):
    """Capture and verify every component using already prepared private destinations.

    The returned snapshots are evidence for a later durable aggregate completion
    record. Any failure leaves existing copies intact and no aggregate is returned.
    """
    guard.verify()
    if guard.exclusion.journal.read()['step'] != 6:
        raise CaptureError('new backup capture requires the pending backup phase')
    documents = Documents(guard.exclusion.journal.directory)
    components = guard.exclusion.loaded['documents']['components']['items']
    if not isinstance(destinations, list) or len(destinations) != len(components):
        raise CaptureError('one prepared backup destination is required per component')
    targets = [upgrade_manifest.check_root(path) for path in destinations]
    if any(guard.exclusion.journal.directory not in target.parents for target in targets):
        raise CaptureError('component backup destination is outside the operation')
    if len(set(targets)) != len(targets):
        raise CaptureError('component backup destinations must be distinct')
    results = []
    for index, (component, destination) in enumerate(zip(components, targets)):
        record = component['selection']
        root = Path(record['state_directory'] if component['kind'] == 'session' else record['service_directory'])
        databases = ('inbox.sqlite3', 'notify-journal.sqlite3') if component['kind'] == 'session' else ('memory.sqlite3',)
        selected = names(root, databases=databases, reserved=[value['path'] for _, value in guard.endpoints])
        snapshot = upgrade_backup.capture(root, selected)
        # Freeze the entire selection before copying: a retry may not silently
        # omit a removed file or accept newly changed stopped data.
        source_digest = documents.put(f'capture-{index:03d}-source', dict(
            version=1, component=upgrade_manifest.fingerprint(component), source=snapshot))
        backup = upgrade_backup.copy(snapshot, destination)
        guard.verify()
        if names(root, databases=databases, reserved=[value['path'] for _, value in guard.endpoints]) != selected:
            raise CaptureError('component inventory changed during backup')
        upgrade_backup.verify(snapshot)
        backup_digest = documents.put(f'capture-{index:03d}-backup', backup)
        results.append(dict(kind=component['kind'], selection=record, source=snapshot, backup=backup,
                            source_document=source_digest, backup_document=backup_digest))
    guard.verify()
    for result in results:
        databases = ('inbox.sqlite3', 'notify-journal.sqlite3') if result['kind'] == 'session' else ('memory.sqlite3',)
        if names(Path(result['source']['root']), databases=databases,
                 reserved=[value['path'] for _, value in guard.endpoints]) != tuple(sorted(result['source']['files'])):
            raise CaptureError('component inventory changed before aggregate completion')
        upgrade_backup.verify(result['source'])
        upgrade_backup.verify(result['backup']['destination'])
    return dict(version=1, components=results)


def copy_runtime(guard, destination):
    """Retain the complete frozen old runtime separately from component state."""
    guard.verify()
    if guard.exclusion.journal.read()['step'] != 6:
        raise CaptureError('runtime backup requires the pending backup phase')
    destination = upgrade_manifest.check_root(destination)
    if guard.exclusion.journal.directory not in destination.parents:
        raise CaptureError('runtime backup destination is outside the operation')
    runtime = guard.exclusion.loaded['documents']['runtime']
    upgrade_manifest.verify(runtime)
    snapshot = upgrade_backup.capture(Path(runtime['root']), list(runtime['files']))
    documents = Documents(guard.exclusion.journal.directory)
    source_digest = documents.put('capture-runtime-source', dict(version=1, source=snapshot))
    backup = upgrade_backup.copy(snapshot, destination, allow_nested_destination=True)
    upgrade_manifest.verify(runtime)
    guard.verify()
    backup_digest = documents.put('capture-runtime-backup', backup)
    return dict(version=1, source=snapshot, backup=backup,
                source_document=source_digest, backup_document=backup_digest)
