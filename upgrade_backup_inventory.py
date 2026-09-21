"""Inspect verified backup copies without opening retained SQLite files.

Caller-established writer exclusion and a complete backup selection are required.
The expected snapshot digest must already be bound to the operation evidence.
"""
from contextlib import closing
from pathlib import Path
import sqlite3
import tempfile

import upgrade_backup as backup
import upgrade_inventory as inventory
import upgrade_manifest as manifest


def capture(snapshot, database, workspace):
    """Return logical evidence from a disposable, byte-verified SQLite copy.

    SQLite may recover journals or maintain WAL shared memory while opening even
    a database intended only for inspection. Only the private disposable copy is
    opened; retained backup files are verified again before returning evidence.
    This does not declare any migration acceptable or publish a completion record.
    """
    snapshot = backup.verify(snapshot)
    names = manifest.names_checked([database, database + '-wal',
                                    database + '-shm', database + '-journal'])
    if any(name not in snapshot['files'] for name in names):
        raise backup.BackupError('database and all journal sidecars must be selected explicitly')
    if snapshot['files'][database] is None:
        raise backup.BackupError('selected backup database is absent')
    workspace = manifest.check_root(workspace)
    if workspace.lstat().st_mode & 0o077:
        raise backup.BackupError('inventory workspace must be private')
    source = manifest.check_root(snapshot['root'])
    if source == workspace or source in workspace.parents or workspace in source.parents:
        raise backup.BackupError('inventory workspace and retained backup must be disjoint')
    files = {name: snapshot['files'][name] for name in names}
    selected = dict(version=1, root=str(source), files=files,
                    bytes=sum(value['bytes'] for value in files.values() if value is not None))
    selected['sha256'] = manifest.fingerprint(selected)
    with tempfile.TemporaryDirectory(prefix='sqlite-inventory-', dir=workspace) as temporary:
        target = Path(temporary)
        backup.copy(selected, target)
        # mode=rw refuses accidental creation, but permits SQLite's own recovery
        # on this disposable copy. Application SQL remains query-only.
        with closing(sqlite3.connect((target / database).as_uri() + '?mode=rw',
                                     uri=True, timeout=0)) as db:
            db.execute('PRAGMA trusted_schema=OFF')
            db.execute('PRAGMA query_only=ON')
            captured = inventory.capture(db)
        backup.verify(snapshot)
    return dict(version=1, backup=snapshot['sha256'], database=database,
                inventory=captured)
