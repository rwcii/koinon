import sqlite3
import unittest

import upgrade_inventory as inventory


class InventoryTests(unittest.TestCase):
    def database(self):
        db = sqlite3.connect(':memory:', isolation_level=None)
        self.addCleanup(db.close)
        return db

    def test_logical_hash_ignores_insertion_order_and_physical_rewrite(self):
        first, second = self.database(), self.database()
        for db, values in ((first, (3, 1, 2)), (second, (2, 1, 3))):
            db.execute('CREATE TABLE records(value)')
            db.executemany('INSERT INTO records VALUES(?)', ((x,) for x in values))
        before = inventory.capture(first)
        second.execute('VACUUM')
        self.assertEqual(before, inventory.capture(second))

    def test_equal_counts_cannot_hide_cursor_or_claim_changes(self):
        db = self.database()
        db.execute('CREATE TABLE cursors(consumer TEXT PRIMARY KEY, seq INTEGER)')
        db.execute('CREATE TABLE claims(owner TEXT PRIMARY KEY, reserved INTEGER)')
        db.execute("INSERT INTO cursors VALUES('reader',12)")
        db.execute("INSERT INTO claims VALUES('writer',640)")
        before = inventory.capture(db)
        db.execute('UPDATE cursors SET seq=11')
        db.execute('UPDATE claims SET reserved=639')
        change = inventory.compare(before, inventory.capture(db))
        self.assertFalse(change['equal'])
        self.assertEqual(change['changed_tables'], ['claims', 'cursors'])

    def test_storage_classes_and_duplicate_multiplicity_are_preserved(self):
        db = self.database()
        db.execute('CREATE TABLE records(value)')
        results = []
        for value in (None, 1, 1.0, '1', b'1'):
            db.execute('DELETE FROM records')
            db.execute('INSERT INTO records VALUES(?)', (value,))
            results.append(inventory.capture(db)['tables']['records']['sha256'])
        self.assertEqual(len(set(results)), 5)
        before = inventory.capture(db)
        db.execute('INSERT INTO records SELECT value FROM records')
        self.assertFalse(inventory.compare(before, inventory.capture(db))['equal'])

    def test_sequence_watermark_survives_empty_table_comparison(self):
        db = self.database()
        db.execute('CREATE TABLE inbox(seq INTEGER PRIMARY KEY AUTOINCREMENT, frame TEXT)')
        before = inventory.capture(db)
        db.execute("INSERT INTO inbox(frame) VALUES('synthetic')")
        db.execute('DELETE FROM inbox')
        change = inventory.compare(before, inventory.capture(db))
        self.assertEqual(change['changed_tables'], ['sqlite_sequence'])

    def test_catalog_changes_and_quoted_identifiers_are_visible(self):
        db = self.database()
        db.execute('CREATE TABLE "odd""name"("x""y" TEXT)')
        db.execute('INSERT INTO "odd""name" VALUES(?)', ('text',))
        before = inventory.capture(db)
        db.execute('CREATE INDEX extra ON "odd""name"("x""y")')
        change = inventory.compare(before, inventory.capture(db))
        self.assertTrue(change['catalog_changed'])
        self.assertFalse(change['equal'])
        self.assertEqual(change['changed_tables'], [])

    def test_views_are_recorded_without_evaluating_functions(self):
        db = self.database()
        db.create_function('forbidden', 0, lambda: self.fail('view evaluated'))
        db.execute('CREATE VIEW view_data AS SELECT forbidden()')
        result = inventory.capture(db)
        self.assertEqual(result['tables'], {})
        self.assertEqual(result['objects'][0]['kind'], 'view')

    def test_capacity_failure_releases_snapshot_and_restores_sqlite_limit(self):
        db = self.database()
        db.execute('CREATE TABLE records(value)')
        db.executemany('INSERT INTO records VALUES(?)', [(1,), (2,)])
        previous = db.getlimit(sqlite3.SQLITE_LIMIT_LENGTH)
        for limits in (dict(max_rows=1), dict(max_bytes=1)):
            with self.assertRaises(inventory.InventoryError):
                inventory.capture(db, **limits)
            self.assertFalse(db.in_transaction)
            self.assertEqual(previous, db.getlimit(sqlite3.SQLITE_LIMIT_LENGTH))
        self.assertEqual(inventory.capture(db)['rows'], 2)

    def test_existing_transaction_is_not_rolled_back(self):
        db = self.database()
        db.execute('CREATE TABLE records(value)')
        db.execute('BEGIN')
        db.execute('INSERT INTO records VALUES(1)')
        with self.assertRaises(inventory.InventoryError):
            inventory.capture(db)
        self.assertTrue(db.in_transaction)
        self.assertEqual(db.execute('SELECT count(*) FROM records').fetchone()[0], 1)

    def test_oversized_cell_refuses_without_returning_partial_inventory(self):
        db = self.database()
        db.execute('CREATE TABLE records(value)')
        db.execute('INSERT INTO records VALUES(?)', ('x' * (inventory.MAX_VALUE_BYTES + 1),))
        with self.assertRaises(inventory.InventoryError):
            inventory.capture(db)
        self.assertFalse(db.in_transaction)

    def test_generated_columns_refuse_instead_of_hiding_values(self):
        db = self.database()
        db.execute('CREATE TABLE records(value, generated AS (value+1))')
        with self.assertRaises(inventory.InventoryError):
            inventory.capture(db)

    def test_read_only_file_connection_does_not_change_database(self):
        import pathlib
        import tempfile
        with tempfile.TemporaryDirectory() as home:
            path = pathlib.Path(home) / 'state.sqlite3'
            db = sqlite3.connect(path)
            db.execute('CREATE TABLE records(value)')
            db.execute('INSERT INTO records VALUES(12)')
            db.commit()
            db.close()
            before = path.read_bytes()
            with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as readonly:
                self.assertEqual(inventory.capture(readonly)['rows'], 1)
            readonly.close()
            self.assertEqual(before, path.read_bytes())


    def test_real_memory_with_search_hashes_every_owned_shadow(self):
        from pathlib import Path
        import tempfile
        import memory
        with tempfile.TemporaryDirectory() as home:
            path = Path(home) / 'memory.sqlite3'
            store = memory.Store(path, '0123456789abcdef')
            try:
                store.note('synthetic-writer', 'finding', 'retained searchable note')
                has_search = store.fts
            finally:
                store.close()
            db = sqlite3.connect(path.as_uri() + '?mode=ro', uri=True)
            try:
                captured = inventory.capture(db)
            finally:
                db.close()
            self.assertTrue(memory.SCHEMA_TABLES <= captured['tables'].keys())
            self.assertEqual(captured['tables']['entries']['rows'], 1)
            if has_search:
                self.assertTrue(memory.FTS_SHADOWS <= captured['tables'].keys())
                self.assertNotIn('search', captured['tables'])
                self.assertTrue(any(row['name'] == 'search' for row in captured['objects']))

    def test_real_inbox_and_journal_include_delivery_and_uncertainty(self):
        from pathlib import Path
        import tempfile
        import bridge
        import notification_journal
        with tempfile.TemporaryDirectory() as home:
            root = Path(home)
            store = bridge.InboxStore(root)
            try:
                store.store(42, dict(type='user', message=dict(content='synthetic message')))
            finally:
                store.close()
            expected = notification_journal.identity('codex', 'a' * 64, 'b' * 32, 0)
            journal_path = root / 'notify-journal.sqlite3'
            journal = notification_journal.Journal(journal_path, expected, create=True, bootstrap=True)
            journal.close()
            for path, required in (
                (root / 'inbox.sqlite3', {'inbox', 'inbox_meta', 'memory_binding',
                                        'delivery_identity', 'delivery_record', 'sqlite_sequence'}),
                (journal_path, {'meta', 'work', 'attempt', 'attempt_member', 'receipt_outbox'})):
                with self.subTest(database=path.name):
                    db = sqlite3.connect(path.as_uri() + '?mode=ro', uri=True)
                    try:
                        captured = inventory.capture(db)
                    finally:
                        db.close()
                    self.assertTrue(required <= captured['tables'].keys())

    def test_catalog_and_row_limits_fail_without_silently_omitting_tables(self):
        from unittest.mock import patch
        db = self.database()
        db.execute('CREATE TABLE first(value)')
        db.execute('CREATE TABLE second(value)')
        db.execute('INSERT INTO first VALUES(1)')
        db.execute('INSERT INTO second VALUES(2)')
        with patch.object(inventory, 'MAX_OBJECTS', 1):
            with self.assertRaises(inventory.InventoryError):
                inventory.capture(db)
        with self.assertRaises(inventory.InventoryError):
            inventory.capture(db, max_rows=1)
        self.assertEqual(inventory.capture(db, max_rows=2)['rows'], 2)


    def test_unsupported_table_list_is_a_build_refusal_before_catalog_read(self):
        class OlderSQLite(sqlite3.Connection):
            def execute(self, sql, *args):
                if sql == 'PRAGMA main.table_list':
                    return super().execute('SELECT 1 WHERE 0')
                if 'main.sqlite_schema' in sql:
                    raise AssertionError('catalog read before capability check')
                return super().execute(sql, *args)

        db = sqlite3.connect(':memory:', factory=OlderSQLite)
        self.addCleanup(db.close)
        previous = db.getlimit(sqlite3.SQLITE_LIMIT_LENGTH)
        with self.assertRaises(inventory.UnsupportedSQLiteError) as caught:
            inventory.capture(db)
        self.assertEqual(caught.exception.code, 'unsupported_sqlite')
        self.assertIn('SQLite 3.37', str(caught.exception))
        self.assertFalse(db.in_transaction)
        self.assertEqual(db.getlimit(sqlite3.SQLITE_LIMIT_LENGTH), previous)

    def test_empty_database_has_valid_empty_inventory(self):
        result = inventory.capture(self.database())
        self.assertEqual(result['rows'], 0)
        self.assertEqual(result['tables'], {})


if __name__ == '__main__':
    unittest.main()
