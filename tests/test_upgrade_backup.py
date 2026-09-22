from contextlib import closing
from pathlib import Path
import sqlite3
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from koinon import upgrade_backup as backup


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source, self.target = self.root / 'source', self.root / 'backup'
        self.source.mkdir(mode=0o700)
        self.target.mkdir(mode=0o700)
        self.database = self.source / 'state.db'
        with closing(sqlite3.connect(self.database)) as db, db:
            db.execute('CREATE TABLE retained (id INTEGER PRIMARY KEY, value TEXT)')
            db.execute('INSERT INTO retained VALUES (1, ?)', ('preserved',))
        self.database.chmod(0o600)
        self.names = ['state.db', 'state.db-wal', 'state.db-shm']
        self.snapshot = backup.capture(self.source, self.names)

    def test_database_and_explicit_sidecar_absences_survive_copy_and_repeat(self):
        result = backup.copy(self.snapshot, self.target)
        self.assertIsNone(result['destination']['files']['state.db-wal'])
        self.assertIsNone(result['destination']['files']['state.db-shm'])
        path = self.target / 'state.db'
        inode = path.stat().st_ino
        with closing(sqlite3.connect(path)) as db:
            self.assertEqual(db.execute('SELECT * FROM retained').fetchall(), [(1, 'preserved')])
        self.assertEqual(backup.copy(self.snapshot, self.target), result)
        self.assertEqual(path.stat().st_ino, inode)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_changed_source_or_new_sidecar_refuses_before_copy(self):
        (self.source / 'state.db-wal').write_bytes(b'new sidecar')
        (self.source / 'state.db-wal').chmod(0o600)
        with self.assertRaises(backup.BackupError):
            backup.copy(self.snapshot, self.target)
        self.assertFalse((self.target / 'state.db').exists())

    def test_different_retained_backup_is_preserved(self):
        path = self.target / 'state.db'
        path.write_bytes(b'other retained evidence')
        path.chmod(0o600)
        with self.assertRaises(backup.BackupError):
            backup.copy(self.snapshot, self.target)
        self.assertEqual(path.read_bytes(), b'other retained evidence')

    def test_failed_post_rename_flush_never_becomes_success_without_confirmation(self):
        sync = backup.platform_support.sync_state_directory
        def failing(path):
            if Path(path) == self.target:
                raise OSError('synthetic directory flush')
            sync(path)
        with patch.object(backup.platform_support, 'sync_state_directory', side_effect=failing):
            with self.assertRaises(OSError):
                backup.copy(self.snapshot, self.target)
            path = self.target / 'state.db'
            self.assertTrue(path.exists())
            inode = path.stat().st_ino
            with self.assertRaises(OSError):
                backup.copy(self.snapshot, self.target)
        result = backup.copy(self.snapshot, self.target)
        self.assertEqual(path.stat().st_ino, inode)
        self.assertEqual(result['source'], self.snapshot['sha256'])

    def test_large_file_is_streamed_beyond_source_manifest_limit(self):
        path = self.source / 'large-state.db'
        with path.open('wb') as stream:
            for _ in range(5):
                stream.write(b'x' * (1 << 20))
        path.chmod(0o600)
        snapshot = backup.capture(self.source, ['large-state.db'])
        result = backup.copy(snapshot, self.target)
        self.assertEqual(result['destination']['bytes'], 5 << 20)
        self.assertEqual(result['destination']['files']['large-state.db']['sha256'],
                         snapshot['files']['large-state.db']['sha256'])

    def test_space_refusal_precedes_backup_file_publication(self):
        with patch.object(backup.shutil, 'disk_usage', return_value=SimpleNamespace(free=0)):
            with self.assertRaises(backup.BackupError):
                backup.copy(self.snapshot, self.target)
        self.assertFalse((self.target / 'state.db').exists())

    def test_nested_copy_reconfirms_both_rename_directories(self):
        nested = self.source / 'notifier'
        nested.mkdir(mode=0o700)
        path = nested / 'journal.db'
        path.write_bytes(b'synthetic journal')
        path.chmod(0o600)
        snapshot = backup.capture(self.source, ['notifier/journal.db'])
        backup.copy(snapshot, self.target)
        calls = []
        sync = backup.platform_support.sync_state_directory
        def recording(path):
            calls.append(Path(path))
            sync(path)
        with patch.object(backup.platform_support, 'sync_state_directory', side_effect=recording):
            backup.copy(snapshot, self.target)
        self.assertIn(self.target, calls)
        self.assertIn(self.target / 'notifier', calls)

    def test_retry_reconfirms_intermediate_directory_links(self):
        nested = self.source / 'one' / 'two' / 'three'
        for directory in (self.source / 'one', self.source / 'one' / 'two', nested):
            directory.mkdir(mode=0o700)
        path = nested / 'state.db'
        path.write_bytes(b'synthetic state')
        path.chmod(0o600)
        snapshot = backup.capture(self.source, ['one/two/three/state.db'])
        backup.copy(snapshot, self.target)
        sync = backup.platform_support.sync_state_directory
        def failing(path):
            if Path(path) == self.target / 'one':
                raise OSError('synthetic intermediate-directory flush')
            sync(path)
        with patch.object(backup.platform_support, 'sync_state_directory', side_effect=failing):
            with self.assertRaisesRegex(OSError, 'intermediate-directory'):
                backup.copy(snapshot, self.target)
        self.assertEqual(backup.copy(snapshot, self.target)['source'], snapshot['sha256'])

    def test_directory_confirmation_outside_root_refuses_before_flushing(self):
        with patch.object(backup.platform_support, 'sync_state_directory') as sync:
            for parent in (self.source, self.target / '..' / 'source'):
                with self.assertRaises(backup.BackupError):
                    backup._sync_parents(self.target, parent)
            sync.assert_not_called()

    def test_symlink_and_reserved_bookkeeping_names_are_refused(self):
        (self.target / 'state.db').symlink_to(self.database)
        with self.assertRaises((OSError, ValueError)):
            backup.copy(self.snapshot, self.target)
        self.assertTrue((self.target / 'state.db').is_symlink())
        for name in (backup.LOCK, backup.TEMP):
            with self.assertRaises(backup.BackupError):
                backup.capture(self.source, [name])


if __name__ == '__main__':
    unittest.main()
