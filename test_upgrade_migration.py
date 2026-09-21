"""Declared migrations checked against independent released schema fixtures."""
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

import memory
import test_work_foundation as released
import upgrade_backup
import upgrade_inventory
import upgrade_migration


class MigrationExpectationTests(unittest.TestCase):
    def fixture(self, version):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name).resolve()
        home, retained, workspace = (root / name for name in ('state', 'backup', 'workspace'))
        for path in (home, retained, workspace):
            path.mkdir(mode=0o700)
        path = home / 'memory.sqlite3'
        if version == 5:
            store = memory.Store(path, released.REPO, fts=False)
            store.close()
        else:
            db = sqlite3.connect(path, isolation_level=None)
            try:
                db.execute('PRAGMA auto_vacuum=INCREMENTAL')
                db.execute('PRAGMA page_size=4096')
                released.legacy(db, version)
                db.execute("INSERT INTO idem VALUES ('synthetic-key', 'fingerprint', 0, 1.0, 2.0)")
                db.execute("INSERT INTO cursors VALUES ('synthetic-reader', 0, 0, NULL, 1, 0, 1.0)")
            finally:
                db.close()
        snapshot = upgrade_backup.capture(home, ['memory.sqlite3' + suffix for suffix in ('', '-wal', '-shm', '-journal')])
        backup = upgrade_backup.copy(snapshot, retained)['destination']
        return path, backup, workspace

    def test_supported_migrations_match_gated_startup_and_preserve_backup(self):
        for version in (3, 4, 5):
            with self.subTest(version=version):
                path, backup, workspace = self.fixture(version)
                store = memory.Store(path, released.REPO, defer_index=True)
                try:
                    expected = upgrade_migration.expected_memory(backup, workspace, released.REPO, store.meta('store_id'))
                    result = upgrade_migration.verify(expected, upgrade_inventory.capture(store.db))
                    self.assertTrue(result['verified'])
                    self.assertEqual(result['source_schema'], version)
                    if version == 4:
                        self.assertEqual(result['identity']['store_id'], 'a' * 32)
                    if version == 3:
                        self.assertIn('new_store_uuid', result['changes'])
                finally:
                    store.close()
                upgrade_backup.verify(backup)

    def test_existing_uuid_change_is_not_a_declared_exception(self):
        _, backup, workspace = self.fixture(4)
        with self.assertRaisesRegex(upgrade_migration.MigrationError, 'UUID'):
            upgrade_migration.expected_memory(backup, workspace, released.REPO, 'b' * 32)

    def test_migration_cannot_rewrite_existing_replay_fields(self):
        _, backup, workspace = self.fixture(4)
        migrate = upgrade_migration.work_schema.migrate
        def corrupt(db, repo, statements):
            migrate(db, repo, statements)
            db.execute("UPDATE idem SET fingerprint='changed'")
        with patch.object(upgrade_migration.work_schema, 'migrate', side_effect=corrupt):
            with self.assertRaisesRegex(upgrade_migration.MigrationError, 'replay fields'):
                upgrade_migration.expected_memory(backup, workspace, released.REPO, 'a' * 32)
        upgrade_backup.verify(backup)

    def test_equal_row_count_cannot_hide_a_changed_cursor(self):
        path, backup, workspace = self.fixture(4)
        store = memory.Store(path, released.REPO, defer_index=True)
        try:
            expected = upgrade_migration.expected_memory(backup, workspace, released.REPO, store.meta('store_id'))
            store.db.execute("UPDATE cursors SET issued=1")
            with self.assertRaises(upgrade_migration.MigrationError):
                upgrade_migration.verify(expected, upgrade_inventory.capture(store.db))
        finally:
            store.close()
