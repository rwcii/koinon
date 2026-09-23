import waiting
from contextlib import closing
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from koinon import upgrade_backup as backup
from koinon import upgrade_backup_inventory as inspection


class BackupInventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source, self.retained, self.workspace = [self.root / name for name in
                                                     ('source', 'retained', 'workspace')]
        for path in (self.source, self.retained, self.workspace):
            path.mkdir(mode=0o700)
        self.path = self.source / 'state.db'
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute('CREATE TABLE retained(value TEXT)')
            db.execute("INSERT INTO retained VALUES ('first')")
        self.path.chmod(0o600)
        self.names = ['state.db', 'state.db-wal', 'state.db-shm', 'state.db-journal']

    def frozen(self):
        return backup.copy(backup.capture(self.source, self.names), self.retained)['destination']

    def test_inventory_is_bound_to_backup_and_removes_only_disposable_copy(self):
        snapshot = self.frozen()
        result = inspection.capture(snapshot, 'state.db', self.workspace)
        self.assertEqual(result['backup'], snapshot['sha256'])
        self.assertEqual(result['inventory']['tables']['retained']['rows'], 1)
        self.assertEqual(backup.verify(snapshot), snapshot)
        self.assertEqual(list(self.workspace.iterdir()), [])

    def test_committed_wal_data_is_included_without_changing_retained_sidecars(self):
        # Deliberate abrupt exit leaves a committed transaction in WAL rather
        # than checkpointing it through ordinary connection close.
        script = '''import os, sqlite3, sys
connection = sqlite3.connect(sys.argv[1])
connection.execute('PRAGMA journal_mode=WAL')
connection.execute('PRAGMA wal_autocheckpoint=0')
connection.execute("INSERT INTO retained VALUES ('committed in wal')")
connection.commit()
os._exit(0)
'''
        subprocess.run([sys.executable, '-c', script, str(self.path)], check=True,
                       timeout=waiting.timeout(), capture_output=True)
        snapshot = self.frozen()
        self.assertGreater(snapshot['files']['state.db-wal']['bytes'], 0)
        self.assertIsNotNone(snapshot['files']['state.db-shm'])
        result = inspection.capture(snapshot, 'state.db', self.workspace)
        self.assertEqual(result['inventory']['tables']['retained']['rows'], 2)
        self.assertEqual(backup.verify(snapshot), snapshot)

    def test_missing_sidecar_selection_refuses_before_workspace_creation(self):
        snapshot = backup.copy(backup.capture(self.source, ['state.db']),
                               self.retained)['destination']
        with self.assertRaisesRegex(backup.BackupError, 'sidecars'):
            inspection.capture(snapshot, 'state.db', self.workspace)
        self.assertEqual(list(self.workspace.iterdir()), [])

    def test_changed_retained_evidence_during_inspection_refuses(self):
        snapshot = self.frozen()
        original = inspection.inventory.capture
        def changed(db):
            result = original(db)
            (self.retained / 'state.db').write_bytes(b'changed synthetic evidence')
            return result
        with patch.object(inspection.inventory, 'capture', side_effect=changed):
            with self.assertRaises(backup.BackupError):
                inspection.capture(snapshot, 'state.db', self.workspace)
        self.assertEqual(list(self.workspace.iterdir()), [])
        self.assertEqual((self.retained / 'state.db').read_bytes(), b'changed synthetic evidence')

    def test_workspace_inside_backup_and_nonprivate_workspace_are_refused(self):
        snapshot = self.frozen()
        nested = self.retained / 'scratch'
        nested.mkdir(mode=0o700)
        with self.assertRaises(backup.BackupError):
            inspection.capture(snapshot, 'state.db', nested)
        self.workspace.chmod(0o755)
        with self.assertRaises(backup.BackupError):
            inspection.capture(snapshot, 'state.db', self.workspace)


if __name__ == '__main__':
    unittest.main()
