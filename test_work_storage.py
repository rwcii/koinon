"""Storage enforcement on real synthetic Stores; no public schema-5 switch."""
import inspect
import json
from pathlib import Path
import sqlite3
import tempfile
import time
import unittest
from unittest.mock import patch

import claims
import memory
import work_schema
import work_storage

REPO = '0123456789abcdef'
WORK = '00000000000000000000000000000001'


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / 'memory.sqlite3'
        self.store = memory.Store(self.path, REPO, fts=False)
        self.addCleanup(self.store.close)
        self.commands = memory.MemoryCommands(self.path.parent, REPO, self.store)

    def migrate(self):
        with self.store.transaction():
            work_schema.migrate(self.store.db, REPO, memory.SCHEMA_STATEMENTS)

    def acquire(self, number=1):
        with self.store.transaction(control=False):
            return claims.LeaseEngine(self.store.db, lambda *args: None).acquire(
                f'{number:032x}', 'writer', 1, now=time.time())

    def unchanged_on_refusal(self, callback, code='capacity'):
        before = tuple(self.store.db.iterdump())
        with self.assertRaises(memory.MemoryError_) as caught:
            callback()
        self.assertEqual(caught.exception.code, code)
        self.assertEqual(tuple(self.store.db.iterdump()), before)


class CompatibilityTests(Base):
    def test_legacy_ceilings_are_unchanged_and_no_claim_table_is_read(self):
        trace = []
        self.store.db.set_trace_callback(trace.append)
        ordinary = self.store.ceilings(False)
        control = self.store.ceilings(True)
        self.store.db.set_trace_callback(None)
        self.assertFalse(any('claim_bundles' in sql for sql in trace))
        self.assertEqual(self.store.work_debt(), claims.Debt(0, 0))
        self.assertEqual(memory.MAX_PAGES, 16328)
        self.assertEqual(memory.ORDINARY_MAX_PAGES, 14280)
        self.assertEqual(ordinary['pages'], 14280 - memory.COMMIT_SLACK)
        self.assertEqual(control['pages'], 16328 - memory.COMMIT_SLACK)
        self.assertEqual(ordinary['logical'], 32 * 1024**2 - memory.RESERVED_BYTES)
        self.assertEqual(control['logical'], 32 * 1024**2)
        self.assertEqual(ordinary['entries'], 5000 - memory.RESERVED_ENTRIES)
        self.assertEqual(control['entries'], 5000)
        self.assertEqual(ordinary['idem'], 20000)
        self.assertEqual(control['idem'], 20000)

    def test_real_migration_changes_debt_without_a_version_cache(self):
        self.assertEqual(self.store.accounting_version(), 4)
        self.migrate()
        self.acquire()
        self.assertEqual(self.store.accounting_version(), 5)
        self.assertEqual(self.store.work_debt(), claims.Debt(1, 1))
        self.assertEqual(self.store.ceilings(False)['pages'],
                         memory.ORDINARY_MAX_PAGES - memory.COMMIT_SLACK - 640)
        self.assertEqual(self.store.ceilings(True)['idem'], memory.MAX_IDEM_ROWS - 1)

    def test_production_constructor_still_refuses_schema_five_without_an_override(self):
        self.migrate()
        self.store.close()
        with self.assertRaises(memory.MemoryError_) as caught:
            memory.Store(self.path, REPO)
        self.assertEqual(caught.exception.code, 'schema_too_new')
        self.assertEqual(list(inspect.signature(memory.Store).parameters), ['path', 'repo', 'fts'])
        self.assertEqual(memory.SCHEMA, 4)

    def test_missing_claim_table_or_schema_never_means_zero_debt(self):
        self.migrate()
        with self.assertRaises(sqlite3.OperationalError):
            with self.store.transaction():
                self.store.db.execute('DROP TABLE claim_bundles')
        self.assertEqual(self.store.accounting_version(), 5)
        self.unchanged_on_refusal(lambda: self._remove_schema(), 'incompatible_store')

    def _remove_schema(self):
        with self.store.transaction():
            self.store.db.execute("DELETE FROM meta WHERE key='schema'")


class AccountingTests(Base):
    def test_work_rows_replay_and_stream_share_the_projection_formula(self):
        self.migrate()
        before = self.store.usage()
        with self.store.transaction(control=False):
            self.store.db.execute('INSERT INTO work_items '
                '(work_id,revision,lifecycle,title,criteria,non_goals,created_at,'
                'created_consumer,scope_revision,latest_seq) '
                "VALUES (?,1,'open','é title','criteria','',100,'writer',1,1)", (WORK,))
            self.store.db.execute('INSERT INTO work_scope_revisions VALUES (?,?,?,?,?,?,?,?)',
                                  (WORK, 1, 100, 'writer', None, 'é title', 'criteria', ''))
            self.store.db.execute('INSERT INTO entries '
                '(seq,ts,type,scope,scope_target,body,consumer) '
                "VALUES (1,100,'work-event','repo',?,'','writer')", (WORK,))
            self.store.db.execute('INSERT INTO work_events VALUES (?,?,?,?,?)',
                                  (1, WORK, 1, 'created', json.dumps({'title': 'é title'})))
            self.store.db.execute('INSERT INTO idem VALUES (?,?,?,?,?,?,?)',
                                  ('work:key', 'fingerprint', 1, 100, 200, 'work-create', '{}'))
            self.store.set_meta('head', 1)
        self.acquire()
        expected = sum(self.store.charge(work_row=row)
                       for table in work_storage.TABLES
                       for row in self.store.db.execute('SELECT * FROM ' + table))
        expected += self.store.charge(entry=('', None, None, WORK, 'writer'))
        expected += self.store.charge(work_replay=('work:key', 'work-create', '{}'))
        after = self.store.usage()
        self.assertEqual(after['logical'] - before['logical'], expected)
        self.assertEqual(after['work_logical'], expected)
        # Snapshots are independently charged copies, not work sub-budget rows.
        with self.store.transaction():
            self.store.db.execute('INSERT INTO snapshot_items VALUES (?,?,?,?,?)',
                                  ('synthetic', 0, 1, '{}', 2))
        self.assertEqual(self.store.usage()['logical'], after['logical'] + 2)
        self.assertEqual(self.store.usage()['work_logical'], expected)

    def test_new_obligations_are_charged_at_the_transaction_boundary(self):
        self.migrate()
        before = tuple(self.store.db.iterdump())
        cap = self.store.pages() + memory.COMMIT_SLACK + 639
        with patch.object(memory, 'ORDINARY_MAX_PAGES', cap):
            with self.assertRaises(memory.MemoryError_):
                self.acquire()
        self.assertEqual(tuple(self.store.db.iterdump()), before)

    def test_new_credit_cannot_undercut_retained_replay_slots(self):
        self.migrate()
        with self.store.transaction():
            self.store.db.execute("INSERT INTO idem(key,deadline) VALUES ('retained',?)",
                                  (time.time() + 600,))
        with patch.object(memory, 'MAX_IDEM_ROWS', 1):
            self.unchanged_on_refusal(self.acquire, 'idem_capacity')
        self.assertEqual(self.store.work_debt(), claims.Debt(0, 0))

    def test_sql_and_python_charges_agree_on_all_sqlite_value_types(self):
        db = sqlite3.connect(':memory:')
        try:
            db.execute('CREATE TABLE synthetic(value)')
            for value in ('é\x00quote\"', b'\x00\xff', 12345, 1.25, None, ''):
                db.execute('DELETE FROM synthetic')
                db.execute('INSERT INTO synthetic VALUES (?)', (value,))
                actual = db.execute('SELECT ' + work_storage.sql_payload_bytes(('value',))
                                     + ' FROM synthetic').fetchone()[0]
                self.assertEqual(actual, work_storage.payload_bytes((value,)))
        finally:
            db.close()

    def test_control_uses_remaining_debt_without_spending_it_twice(self):
        self.migrate()
        self.acquire(1)
        self.acquire(2)
        before = tuple(self.store.db.iterdump())
        pages = self.store.pages()
        # Releasing one claim leaves 640 pages promised to the other. This cap
        # is one page below that promise: clearing one bundle is insufficient.
        with patch.object(memory, 'MAX_PAGES', pages + memory.COMMIT_SLACK + 639):
            with self.assertRaises(memory.MemoryError_), self.store.transaction():
                claims.LeaseEngine(self.store.db, lambda *args: None).release(
                    WORK, 'writer', 1, now=time.time())
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        with patch.object(memory, 'MAX_PAGES', pages + memory.COMMIT_SLACK + 640):
            with self.store.transaction():
                claims.LeaseEngine(self.store.db, lambda *args: None).release(
                    WORK, 'writer', 1, now=time.time())
        self.assertEqual(self.store.work_debt(), claims.Debt(1, 1))

    def test_direct_transactions_preserve_shared_logical_replay_and_entry_slots(self):
        self.migrate()
        self.acquire()
        use, debt = self.store.usage(), self.store.work_debt()

        def entry():
            with self.store.transaction():
                self.store.db.execute("INSERT INTO entries(seq,body) VALUES (1,'growth')")

        with patch.object(memory, 'MAX_LOGICAL_BYTES', use['logical'] + debt.logical_bytes):
            self.unchanged_on_refusal(entry)
        with patch.object(memory, 'MAX_ENTRIES', debt.event_slots):
            self.unchanged_on_refusal(entry)

        def replay():
            with self.store.transaction():
                self.store.db.execute("INSERT INTO idem(key) VALUES ('new-key')")

        with patch.object(memory, 'MAX_IDEM_ROWS', debt.replay_slots):
            self.unchanged_on_refusal(replay, 'idem_capacity')

    def test_work_subbudget_and_event_slots_are_checked_without_mutation_wrapper(self):
        self.migrate()
        self.acquire()

        def event():
            with self.store.transaction():
                self.store.db.execute('INSERT INTO work_events VALUES (?,?,?,?,?)',
                                      (1, WORK, 1, 'created', '{}'))

        with patch.object(work_storage, 'MAX_EVENTS', 2):
            self.unchanged_on_refusal(event)
        budget = self.store.usage()['work_logical'] + self.store.work_debt().logical_bytes
        with patch.object(work_storage, 'MAX_LOGICAL_BYTES', budget):
            self.unchanged_on_refusal(event)


class WritePathTests(Base):
    def setUp(self):
        super().setUp()
        self.migrate()
        self.acquire()

    def test_progress_uses_note_reserve_but_cannot_take_work_pages(self):
        self.commands.register('reader')
        with self.store.transaction():
            self.store.db.execute("UPDATE cursors SET updated=0 WHERE consumer='reader'")
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK
        with patch.object(memory, 'ORDINARY_MAX_PAGES', 0), patch.object(memory, 'MAX_PAGES', cap):
            self.commands.touch('reader')
        with patch.object(memory, 'MAX_PAGES', cap - 1):
            self.unchanged_on_refusal(lambda: self.commands.touch('reader'))

    def test_snapshot_issue_and_ack_rollback_at_shared_boundary(self):
        self.commands.register('reader')
        self.store.note('writer', 'decision', 'synthetic note')
        sid = memory.freeze(self.store, 'reader')
        request = dict(record_format=2, consumer='reader', snapshot_id=sid)
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK - 1
        with patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(lambda: self.commands.snapshot_page('reader', sid, request))
        self.commands.snapshot_page('reader', sid, request)
        # touch() is independently admitted; avoid making an activity refresh the
        # reason this test fails before reaching the actual ack transaction.
        with patch.object(self.commands, 'touch'), patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(lambda: self.commands.ack(request))
        self.commands.ack(request)
        self.store.note('writer', 'decision', 'next synthetic note')
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK - 1
        with patch.object(self.commands, 'touch'), patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(lambda: self.commands.sync(dict(record_format=2, consumer='reader')))
        delta = self.commands.sync(dict(record_format=2, consumer='reader'))
        with patch.object(self.commands, 'touch'), patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(lambda: self.commands.ack(
                dict(record_format=2, consumer='reader', through=delta['next_cursor'])))

    def test_expired_snapshot_clear_preserves_work_reserve(self):
        self.commands.register('reader')
        sid = memory.freeze(self.store, 'reader')
        with self.store.transaction():
            self.store.db.execute('UPDATE snapshots SET created=? WHERE id=?',
                                  (time.time() - memory.SNAPSHOT_TTL - 1, sid))
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK - 1
        with patch.object(self.commands, 'touch'), patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(lambda: self.commands.sync(dict(record_format=2, consumer='reader')))

    def test_retirement_and_fts_creation_use_transaction_enforcement(self):
        self.commands.register('retiring-reader')
        with self.store.transaction():
            self.store.db.execute('UPDATE cursors SET updated=?',
                                  (time.time() - memory.CONSUMER_TTL - 1,))
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK - 1
        before = tuple(self.store.db.iterdump())
        with patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(self.store.expire)
            self.assertFalse(self.store._open_fts())
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        self.store.expire()
        self.assertIsNone(self.store.cursor('retiring-reader'))
        self.assertEqual(self.store.db.execute('SELECT consumer FROM retired').fetchall(),
                         [('retiring-reader',)])

    def test_expiry_and_reclaim_cannot_commit_deletion_over_work_reserve(self):
        self.store.note('writer', 'decision', 'expired', expires=time.time() - 1)
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK - 1
        with patch.object(memory, 'MAX_PAGES', cap):
            self.unchanged_on_refusal(self.store.expire)
            self.unchanged_on_refusal(self.store.reclaim)
        self.store.expire()
        self.assertEqual(self.store.db.execute('SELECT count(*) FROM entries').fetchone()[0], 0)
        self.assertEqual(self.store.work_debt(), claims.Debt(1, 1))

    def test_fts_rebuild_rolls_back_without_using_work_credits(self):
        self.store.fts = self.store._open_fts()
        if not self.store.fts:
            self.skipTest('SQLite without FTS5')
        self.store.note('writer', 'decision', 'synthetic searchable note')
        with self.store.transaction():
            self.store.set_meta('indexed_through', -1)
        original = self.store.enforce_pages
        failed = []

        def capacity_at_rebuild(control, debt=None):
            if self.store.meta('indexed_through') == str(self.store.head()):
                failed.append(True)
                with patch.object(memory, 'MAX_PAGES',
                                  self.store.pages() + debt.pages + memory.COMMIT_SLACK - 1):
                    return original(control, debt)
            return original(control, debt)

        with patch.object(self.store, 'enforce_pages', side_effect=capacity_at_rebuild):
            self.store._reconcile_index()
        self.assertEqual(failed, [True])
        self.assertFalse(self.store.index_usable())
        self.assertEqual(self.store.work_debt(), claims.Debt(1, 1))
        self.store._reconcile_index()
        self.assertTrue(self.store.index_usable())

    def test_vacuum_is_non_growing_and_keeps_reset_guards(self):
        for _ in range(30):
            self.store.note('writer', 'finding', 'x' * 4000, expires=time.time() - 1)
        self.store.expire()
        for operation in (self.store.reclaim, self.store.recover):
            with self.subTest(operation=operation.__name__):
                before = self.store.pages()
                trace = []
                self.store.db.set_trace_callback(trace.append)
                operation()
                self.store.db.set_trace_callback(None)
                self.assertLessEqual(self.store.pages(), before)
                vacuum = next(i for i, sql in enumerate(trace) if sql == 'PRAGMA incremental_vacuum')
                self.assertIn('PRAGMA wal_checkpoint(TRUNCATE)', trace[:vacuum])
                self.assertIn('PRAGMA wal_checkpoint(TRUNCATE)', trace[vacuum + 1:])
                self.assertEqual(self.store.work_debt(), claims.Debt(1, 1))

    def test_unverified_or_growing_vacuum_blocks_further_writes(self):
        for result in (sqlite3.OperationalError('synthetic page observation failure'), 101):
            with self.subTest(result=result):
                self.store.blocked = None
                with patch.object(self.store, 'pages', side_effect=[result]):
                    with self.assertRaises(memory.MemoryError_) as caught:
                        self.store.verify_vacuum_pages(100)
                self.assertEqual(caught.exception.code, 'storage_blocked')
                self.assertIsNotNone(self.store.blocked)
                with self.assertRaises(memory.MemoryError_):
                    with self.store.transaction():
                        self.fail('a blocked store accepted a write')

    def test_vacuum_paths_block_when_page_observation_fails(self):
        for operation in (self.store.reclaim, self.store.recover):
            for stage in (operation.__name__, 'verify_vacuum_pages'):
                with self.subTest(operation=operation.__name__, stage=stage):
                    self.store.blocked = None
                    original = self.store.pages
                    trace = []

                    def observe():
                        if inspect.currentframe().f_back.f_code.co_name == stage:
                            raise sqlite3.OperationalError('synthetic observation failure')
                        return original()

                    self.store.db.set_trace_callback(trace.append)
                    with patch.object(self.store, 'pages', new=observe):
                        with self.assertRaises(memory.MemoryError_) as caught:
                            operation()
                    self.store.db.set_trace_callback(None)
                    self.assertEqual(caught.exception.code, 'storage_blocked')
                    self.assertIsNotNone(self.store.blocked)
                    self.assertEqual('PRAGMA incremental_vacuum' in trace,
                                     stage == 'verify_vacuum_pages')

    def test_full_ordinary_store_preserves_all_admitted_controls_and_note_progress(self):
        for number in range(2, 17):
            self.acquire(number)
        self.commands.register('reader')
        self.store.note('writer', 'decision', 'snapshot seed')
        sid = memory.freeze(self.store, 'reader')
        request = dict(record_format=2, consumer='reader', snapshot_id=sid)
        timings = {}

        def timed(label, callback):
            start = time.perf_counter()
            result = callback()
            timings[label] = round((time.perf_counter() - start) * 1000, 3)
            return result

        written = 0
        while written < memory.MAX_ENTRIES:
            try:
                self.store.note('writer', 'decision', 'x' * memory.MAX_BODY)
                written += 1
            except memory.MemoryError_ as exc:
                self.assertEqual(exc.code, 'capacity')
                break
        self.assertGreater(written, 100)
        self.assertLess(written, memory.MAX_ENTRIES)
        self.assertEqual(self.store.work_debt(), claims.Debt(16, 16))
        timed('usage_ms', self.store.usage)
        timed('touch_ms', lambda: self.commands.touch('reader'))
        timed('snapshot_page_ms', lambda: self.commands.snapshot_page('reader', sid, request))
        timed('ack_ms', lambda: self.commands.ack(request))
        timed('note_control_ms', lambda: self.store.note(
            'writer', 'directive', 'withdrawn', revokes=1))
        def refuse():
            with self.assertRaises(memory.MemoryError_) as caught:
                self.store.note('writer', 'decision', 'x' * memory.MAX_BODY)
            self.assertEqual(caught.exception.code, 'capacity')
        timed('ordinary_refusal_ms', refuse)
        self.assertEqual(self.store.work_debt(), claims.Debt(16, 16))

        # Representative maximum encoded events and replay results. This tests
        # the shared transaction's funding, not the not-yet-exposed work API.
        for number in range(1, 17):
            work_id = f'{number:032x}'
            for kind in ('progress-overdue', 'finished'):
                with self.store.transaction():
                    seq = self.store.head() + 1
                    self.store.db.execute('INSERT INTO entries '
                        '(seq,ts,type,scope,scope_target,body,consumer) '
                        "VALUES (?,?,'work-event','repo',?,'','writer')", (seq, time.time(), work_id))
                    self.store.db.execute('INSERT INTO work_events VALUES (?,?,?,?,?)',
                        (seq, work_id, 1, kind, 'x' * (16 * 1024)))
                    if kind == 'progress-overdue':
                        self.store.db.execute('UPDATE claim_bundles SET overdue_credit=0, '
                            'overdue_recorded=1 WHERE generation=?', (number,))
                    else:
                        self.store.db.execute('UPDATE claim_bundles SET active=0, '
                            'overdue_credit=0,end_credit=0 WHERE generation=?', (number,))
                        self.store.db.execute('INSERT INTO idem VALUES (?,?,?,?,?,?,?)',
                            ('work:' + work_id, 'fingerprint', seq, time.time(), time.time() + 600,
                             'work-finish', 'x' * 2048))
                    self.store.set_meta('head', seq)
        self.assertEqual(self.store.work_debt(), claims.Debt(0, 0))
        self.assertEqual(self.store.usage()['work_counts']['work_events'], 32)
        self.assertEqual(self.store.db.execute('PRAGMA integrity_check').fetchone(), ('ok',))
        print('\nStorage saturation observations (no latency assertions): ' +
              json.dumps(dict(notes=written, pages=self.store.pages(), timings=timings),
                         sort_keys=True), flush=True)


if __name__ == '__main__':
    unittest.main()
