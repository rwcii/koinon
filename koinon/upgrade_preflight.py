"""Read-only inputs for upgrade preparation; no installer code is executed.

These checks are prerequisites, not a complete permission to stop services. The
public coordinator must retain the installation lock, establish backup/schema
capacity, freeze the observations, and publish exclusion before shutdown.
"""
import ast
import hashlib
from itertools import islice
from pathlib import Path
import stat
import os

from koinon import upgrade_discovery
from koinon import upgrade_manifest as manifest
from koinon import upgrade_observation
from koinon import upgrade_plan
from koinon import upgrade_replace


class PreflightError(ValueError):
    pass


class UnownedMemoryError(PreflightError):
    def __init__(self, report):
        self.report = report
        super().__init__('unowned memory state found; upgrade refused before shutdown')


class UnownedServiceError(PreflightError):
    def __init__(self, report):
        self.report = report
        super().__init__('unowned service references this runtime; upgrade refused before shutdown')


def runtime_manifest(root):
    """Capture exactly the literal shipped FILES list, without importing install.py."""
    root = manifest.select_root(root)
    source = manifest.read_selected(root, 'scripts/install.py')
    try:
        tree = ast.parse(source, filename='scripts/install.py')
    except (SyntaxError, ValueError) as exc:
        raise PreflightError('installer file list cannot be parsed') from exc
    assignments = [node for node in tree.body if isinstance(node, ast.Assign)
                   and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name)
                   and node.targets[0].id == 'FILES']
    stores = [node for node in ast.walk(tree) if isinstance(node, ast.Name)
              and node.id == 'FILES' and isinstance(node.ctx, ast.Store)]
    if len(assignments) != 1 or len(stores) != 1:
        raise PreflightError('one literal installer FILES assignment required')
    try:
        names = manifest.names_checked(ast.literal_eval(assignments[0].value))
    except (ValueError, TypeError, SyntaxError) as exc:
        raise PreflightError('installer FILES must be a bounded literal file list') from exc
    if 'scripts/install.py' not in names:
        raise PreflightError('installer must include its own file list')
    captured = manifest.capture(root, names)
    if captured['files']['scripts/install.py'] != dict(
            bytes=len(source), sha256=hashlib.sha256(source).hexdigest()):
        raise PreflightError('installer file list changed during capture')
    return captured


def runtime_pair(prefix, source):
    """Reject overlap and unsupported file removals before preparing recovery."""
    prefix, source = map(manifest.select_root, (prefix, source))
    if prefix == source or prefix in source.parents or source in prefix.parents:
        raise PreflightError('source and installed runtime roots must be disjoint')
    old, new = runtime_manifest(prefix), runtime_manifest(source)
    upgrade_replace.preflight(new, old)
    retired = upgrade_replace.retirements(new, old)
    bytecode_caches(prefix, dict(runtime=old, source=new))
    return dict(runtime=old, source=new)


def bytecode_caches(prefix, pair):
    """Name the untrusted caches replacement will quarantine; refuse unmovable ones."""
    retired = upgrade_replace.retirements(pair['source'], pair['runtime'])
    return upgrade_replace.untrusted_caches(
        prefix, [*pair['source']['files'], *(name for name, _ in retired)])


def memory_ownership(config):
    """Enumerate memory state in installation-selected roots without probing it.

    A directory without an exact saved selection is external even if stopped or
    empty. No directory is adopted, deleted, or opened as SQLite. This inventory
    covers the configured state root and every saved memory state root, not
    arbitrary custom service locations elsewhere on the host.
    """
    records = config.get('memory_services', {}).get('repositories', {})
    roots = {str(Path(config['state_root']))}
    roots.update(record['state_root'] for record in records.values())
    selected = {str(manifest.select_root(record['state_root']) / 'memory' / key)
                for key, record in records.items()}
    unowned, scanned = [], []
    for root in sorted(roots):
        root = manifest.select_root(root)
        directory = root / 'memory'
        try:
            info = directory.lstat()
        except FileNotFoundError:
            scanned.append(dict(root=str(root), memory_directory_present=False))
            continue
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o077):
            raise PreflightError('memory inventory directory must be owned and private')
        stamp = lambda value: (value.st_dev, value.st_ino, value.st_mtime_ns, value.st_ctime_ns)
        with os.scandir(directory) as entries:
            names = sorted(entry.name for entry in islice(entries, upgrade_plan.MAX_COMPONENTS + 1))
        if len(names) > upgrade_plan.MAX_COMPONENTS:
            raise PreflightError('memory inventory exceeds upgrade capacity')
        for name in names:
            if len(name) != 16 or any(char not in '0123456789abcdef' for char in name):
                raise PreflightError('unrecognized memory inventory entry')
            path = directory / name
            child = path.lstat()
            if (not stat.S_ISDIR(child.st_mode) or child.st_uid != os.geteuid()
                    or child.st_mode & 0o077):
                raise PreflightError('memory inventory entry must be owned and private')
            if str(path) not in selected:
                unowned.append(dict(service_directory=str(path), action='refused',
                                    reason='no_saved_managed_selection'))
        manifest.check_root(root)
        if stamp(directory.lstat()) != stamp(info):
            raise PreflightError('memory inventory changed during enumeration')
        scanned.append(dict(root=str(root), memory_directory_present=True))
    return dict(version=1, scope='installation_selected_state_roots',
                roots=scanned, unowned=unowned,
                action='refused' if unowned else 'no_unowned_state_found')


def observe_locked(prefix, installed):
    """Keep the caller's installation lock through preparation and exclusion."""
    ownership = memory_ownership(installed.config)
    if ownership['unowned']:
        raise UnownedMemoryError(ownership)
    observed = upgrade_observation.installation_locked(prefix, installed)
    services = upgrade_discovery.inventory(prefix, installed.config, observed['components'])
    if services['action'] == 'refused':
        raise UnownedServiceError(services)
    if upgrade_discovery.inventory(prefix, installed.config, observed['components']) != services:
        raise PreflightError('external service discovery changed during preflight')
    if memory_ownership(installed.config) != ownership:
        raise PreflightError('memory inventory changed during preflight')
    return dict(observed, memory_ownership=ownership, service_ownership=services)


def database_check(root, database, workspace, *, kind, repo=None):
    """Validate a consistent online SQLite copy without treating it as the backup.

    The source is opened read-only and never migrated. SQLite may use its existing
    WAL shared-memory coordination; the authoritative retained backup is still
    taken only after owned shutdown. All migration trials affect private scratch.
    """
    from contextlib import closing
    import sqlite3
    import tempfile
    import time
    from koinon import durable_state
    import memory
    from koinon import upgrade_backup
    from koinon import upgrade_migration
    root, workspace = map(manifest.check_root, (root, workspace))
    if workspace.lstat().st_mode & 0o077:
        raise PreflightError('preflight workspace must be private')
    if root == workspace or root in workspace.parents or workspace in root.parents:
        raise PreflightError('preflight workspace and selected state must be disjoint')
    manifest.names_checked([database])
    path = root / database
    fd = durable_state.open_validated(path, upgrade_backup.MAX_FILE_BYTES)
    if fd is None:
        raise PreflightError('running component database is absent')
    identity = os.fstat(fd)
    def same_source():
        manifest.check_root(root)
        named = path.lstat()
        if ((named.st_dev, named.st_ino) != (identity.st_dev, identity.st_ino)
                or not stat.S_ISREG(named.st_mode)):
            raise PreflightError('selected database changed during preflight')
    try:
        same_source()
        with tempfile.TemporaryDirectory(prefix='preflight-db-', dir=workspace) as temporary:
            scratch = Path(temporary)
            copied, trial = scratch / 'copy', scratch / 'trial'
            copied.mkdir(mode=0o700)
            trial.mkdir(mode=0o700)
            deadline = time.monotonic() + 30
            def progress(status, remaining, total):
                if time.monotonic() >= deadline or total * page_size > upgrade_backup.MAX_FILE_BYTES:
                    raise PreflightError('online schema preflight exceeds its copy budget')
            with closing(sqlite3.connect(path.as_uri() + '?mode=ro', uri=True, timeout=1)) as source:
                source.execute('PRAGMA trusted_schema=OFF')
                source.execute('PRAGMA query_only=ON')
                page_size = source.execute('PRAGMA page_size').fetchone()[0]
                pages = source.execute('PRAGMA page_count').fetchone()[0]
                if pages * page_size > upgrade_backup.MAX_FILE_BYTES:
                    raise PreflightError('selected database exceeds backup file capacity')
                if kind == 'memory' and pages * page_size >= memory.ORDINARY_MAX_PAGES * memory.PAGE_SIZE:
                    raise PreflightError('near-full memory store; upgrade refused before shutdown')
                target_path = copied / database
                with closing(sqlite3.connect(target_path)) as target:
                    target_path.chmod(0o600)
                    source.backup(target, pages=64, progress=progress, sleep=.01)
            same_source()
            snapshot = upgrade_backup.capture(copied,
                [database + suffix for suffix in ('', '-wal', '-shm', '-journal')])
            if kind == 'memory':
                with closing(sqlite3.connect(target_path)) as db:
                    db.execute('PRAGMA trusted_schema=OFF')
                    db.execute('PRAGMA query_only=ON')
                    # Validate the schema before consuming metadata as identity.
                    from koinon import work_schema
                    version = work_schema.validate(db, repo, memory.SCHEMA_STATEMENTS)
                    identity_rows = dict(db.execute('SELECT key,value FROM meta'))
                expected = upgrade_migration.expected_memory(snapshot, trial, repo,
                    identity_rows['store_id'] if version >= 4 else '0' * 32)
            elif kind == 'session':
                expected = upgrade_migration.expected_inbox(snapshot, trial)
            else:
                raise PreflightError('unsupported database preflight kind')
            # Probe support even for an empty database; SQLite silently ignores
            # unknown PRAGMAs, so the inventory helper performs the capability check.
            import json
            try:
                encoded = json.dumps(expected, sort_keys=True, ensure_ascii=True, allow_nan=False).encode()
            except (TypeError, ValueError) as exc:
                raise PreflightError('unsupported value in preservation report') from exc
            from koinon.upgrade_documents import MAX_BYTES
            if len(encoded) + 4096 > MAX_BYTES:
                raise PreflightError('migration report exceeds private document capacity')
            return dict(database=database, source_schema=expected['source_schema'],
                        target_schema=expected['target_schema'], bytes=pages * page_size,
                        rows=expected['inventory']['rows'], report_bytes=len(encoded))
    finally:
        os.close(fd)


def storage_budget(components, runtime, operation):
    """Bound regular state files and reserve copy, scratch and document headroom."""
    import json
    import shutil
    from koinon import platform_support
    from koinon import upgrade_backup
    from koinon.upgrade_documents import MAX_BYTES
    total, largest = runtime['bytes'], 0
    evidence_bytes = 4 * len(json.dumps(runtime).encode()) + 16384
    states = []
    for component in components:
        record = component['selection']
        root = manifest.check_root(record['state_directory'] if component['kind'] == 'session'
                                   else record['service_directory'])
        endpoints = {str(platform_support.control_socket_path(root))}
        if component['kind'] == 'session':
            endpoints.add(str(platform_support.control_socket_path(root / 'notifier')))
        files, pending, entries, size = {}, [root], 0, 0
        while pending:
            directory = pending.pop()
            with os.scandir(directory) as children:
                for entry in children:
                    entries += 1
                    if entries > 240:
                        raise PreflightError('component inventory lacks backup entry headroom')
                    info = entry.stat(follow_symlinks=False)
                    path = Path(entry.path)
                    if info.st_uid != os.geteuid() or info.st_mode & 0o022:
                        raise PreflightError('unsafe component state entry')
                    if stat.S_ISDIR(info.st_mode):
                        pending.append(path)
                    elif stat.S_ISSOCK(info.st_mode) and str(path) in endpoints:
                        continue
                    elif stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
                        if info.st_size > upgrade_backup.MAX_FILE_BYTES:
                            raise PreflightError('component file exceeds backup capacity')
                        size += info.st_size
                        files[str(path.relative_to(root))] = dict(bytes=info.st_size,
                            sha256='0' * 64, mode=0o600)
                    else:
                        raise PreflightError('unrecognized component state entry')
        if size > upgrade_backup.MAX_TOTAL_BYTES:
            raise PreflightError('component exceeds backup capacity')
        if shutil.disk_usage(root).free < size + (16 << 20):
            raise PreflightError('insufficient state filesystem space for migration')
        total += size
        largest = max(largest, size)
        # The current backup aggregate retains both source and destination maps;
        # reserve additional paths/sidecar names and recheck exact publication.
        evidence_bytes += 4 * len(json.dumps(dict(root=str(root), files=files, selection=record)).encode()) + 8192
        states.append(dict(root=str(root), estimated_bytes=size))
    if evidence_bytes > MAX_BYTES - 65536:
        raise PreflightError('aggregate backup evidence exceeds document capacity')
    required = 2 * total + 2 * largest + (64 << 20)
    free = shutil.disk_usage(operation).free
    if free < required:
        raise PreflightError('insufficient free space for retained backup and migration scratch')
    return dict(version=1, estimated_backup_bytes=total, required_free_bytes=required,
                observed_free_bytes=free, state=states)
