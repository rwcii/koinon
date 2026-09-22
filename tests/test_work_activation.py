"""Public schema-5 startup and migration on private synthetic stores only."""
import asyncio
from pathlib import Path
import sqlite3
import tempfile
import time
import unittest
from unittest.mock import patch

import memory
import work_schema

REPO = '0123456789abcdef'


class ActivationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)/'memory.sqlite3'

    def legacy(self, version=4):
        with patch.object(memory, 'SCHEMA', 4):
            store = memory.Store(self.path, REPO, fts=False)
        if version == 3:
            with store.transaction():
                store.db.execute("DELETE FROM meta WHERE key='store_id'")
                store.set_meta('schema', 3)
        return store

    def test_fresh_start_and_restart_use_complete_schema_five(self):
        store = memory.Store(self.path, REPO, fts=False)
        self.assertEqual(store.accounting_version(), 5)
        self.assertEqual(work_schema.validate(store.db, REPO, memory.SCHEMA_STATEMENTS), 5)
        identity = store.meta('store_id')
        store.close()
        with patch.object(work_schema, 'migrate', side_effect=AssertionError('unexpected second migration')):
            reopened = memory.Store(self.path, REPO, fts=False)
        self.addCleanup(reopened.close)
        self.assertEqual(reopened.meta('store_id'), identity)

    def test_three_and_four_migrate_preserving_notes_replay_and_frozen_snapshot(self):
        for version in (3, 4):
            with self.subTest(version=version):
                self.path = Path(self.tmp.name)/f'legacy-{version}.sqlite3'
                old = self.legacy(version)
                old.note('writer', 'decision', 'before migration', key='note-key', deadline=time.time()+600)
                commands = memory.MemoryCommands(self.path.parent, REPO, old)
                snapshot = commands.sync(dict(consumer='reader'))
                preserved = {table: old.db.execute('SELECT * FROM '+table).fetchall()
                             for table in ('entries', 'cursors', 'snapshots', 'snapshot_items')}
                replay = old.db.execute('SELECT * FROM idem').fetchall()
                identity, head, floor = old.meta('store_id'), old.head(), old.meta('floor')
                old.close()
                new = memory.Store(self.path, REPO, fts=False)
                try:
                    self.assertEqual(new.accounting_version(), 5)
                    self.assertEqual(new.head(), head)
                    self.assertEqual(new.meta('floor'), floor)
                    if version == 4:
                        self.assertEqual(new.meta('store_id'), identity)
                    else:
                        self.assertEqual(len(new.meta('store_id')), 32)
                    for table, rows in preserved.items():
                        self.assertEqual(new.db.execute('SELECT * FROM '+table).fetchall(), rows)
                    self.assertEqual(new.db.execute('SELECT key,fingerprint,seq,ts,deadline FROM idem').fetchall(), replay)
                    resumed = memory.MemoryCommands(self.path.parent, REPO, new)
                    page = resumed.sync(dict(consumer='reader', record_format=2))
                    self.assertEqual(page['snapshot_id'], snapshot['snapshot_id'])
                    resumed.ack(dict(consumer='reader', record_format=2, snapshot_id=page['snapshot_id']))
                finally:
                    new.close()

    def test_every_migration_ddl_failure_rolls_back_public_startup(self):
        connect = sqlite3.connect
        for version in (3, 4):
            for index, failing in enumerate(work_schema.MIGRATION_STATEMENTS):
                with self.subTest(version=version, statement=index):
                    self.path = Path(self.tmp.name)/f'rollback-{version}-{index}.sqlite3'
                    old = self.legacy(version)
                    old.note('writer', 'decision', 'preserve through failed upgrade')
                    before = tuple(old.db.iterdump())
                    old.close()
                    class FailingConnection(sqlite3.Connection):
                        def execute(self, sql, *args, **kwargs):
                            if sql == failing:
                                raise RuntimeError('synthetic migration interruption')
                            return super().execute(sql, *args, **kwargs)
                    def injected(path, *args, **kwargs):
                        if str(path) == str(self.path):
                            kwargs['factory'] = FailingConnection
                        return connect(path, *args, **kwargs)
                    with patch.object(memory.sqlite3, 'connect', side_effect=injected), self.assertRaises(RuntimeError):
                        memory.Store(self.path, REPO, fts=False)
                    db = connect(self.path)
                    try:
                        self.assertEqual(tuple(db.iterdump()), before)
                    finally:
                        db.close()

    def test_catalog_refusal_precedes_any_write_capable_configuration(self):
        for extra in ('CREATE TABLE foreign_data(value)',
                      'CREATE INDEX unowned_index ON entries(body)',
                      'CREATE TRIGGER unowned_trigger AFTER INSERT ON entries BEGIN SELECT 1; END'):
            with self.subTest(extra=extra):
                self.path = Path(self.tmp.name)/(str(len(list(Path(self.tmp.name).iterdir())))+'.sqlite3')
                old = self.legacy()
                old.db.execute(extra)
                old.close()
                before = self.path.read_bytes()
                with patch.object(memory.Store, 'configure', side_effect=AssertionError('write before validation')), \
                     self.assertRaises(memory.MemoryError_) as refused:
                    memory.Store(self.path, REPO)
                self.assertEqual(refused.exception.code, 'incompatible_store')
                self.assertEqual(self.path.read_bytes(), before)

    def test_near_full_migration_refuses_and_preserves_legacy_rows(self):
        old = self.legacy()
        old.note('writer', 'decision', 'preserve at capacity')
        before = tuple(old.db.iterdump())
        pages = old.pages()
        old.close()
        with patch.object(memory, 'MAX_PAGES', pages + memory.COMMIT_SLACK), \
             self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO, fts=False)
        self.assertIn(refused.exception.code, ('capacity', 'storage_blocked'))
        db = sqlite3.connect(self.path)
        try:
            self.assertEqual(tuple(db.iterdump()), before)
        finally:
            db.close()

    def test_malformed_version_refuses_before_configuration(self):
        old = self.legacy()
        old.db.execute("UPDATE meta SET value='not-a-version' WHERE key='schema'")
        old.close()
        before = self.path.read_bytes()
        with patch.object(memory.Store, 'configure', side_effect=AssertionError('write before validation')), \
             self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO)
        self.assertEqual(refused.exception.code, 'incompatible_store')
        self.assertEqual(self.path.read_bytes(), before)

    def test_catalog_programming_error_is_not_mislabeled_as_incompatible_store(self):
        old = self.legacy()
        try:
            with patch.object(work_schema, 'catalog', side_effect=sqlite3.ProgrammingError('synthetic defect')), \
                 self.assertRaises(sqlite3.ProgrammingError):
                work_schema.validate(old.db, REPO, memory.SCHEMA_STATEMENTS)
        finally:
            old.close()

    def test_declared_legacy_version_cannot_hide_work_schema_objects(self):
        store = memory.Store(self.path, REPO, fts=False)
        store.set_meta('schema', 4)
        store.close()
        before = self.path.read_bytes()
        with patch.object(memory.Store, 'configure', side_effect=AssertionError('write before validation')), \
             self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO)
        self.assertEqual(refused.exception.code, 'incompatible_store')
        self.assertEqual(self.path.read_bytes(), before)

    def test_startup_schema_error_mapping_is_explicit_and_unknown_codes_are_internal(self):
        old = self.legacy()
        old.close()
        with patch.object(work_schema, 'validate', side_effect=work_schema.SchemaError('capacity', 'synthetic limit')), \
             self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO)
        self.assertEqual(refused.exception.code, 'capacity')
        with patch.object(work_schema, 'validate', side_effect=work_schema.SchemaError('unknown-code', 'synthetic defect')), \
             self.assertRaises(RuntimeError):
            memory.Store(self.path, REPO)

    def test_public_work_counter_exhaustion_is_capacity_and_changes_nothing(self):
        store = memory.Store(self.path, REPO, fts=False)
        try:
            with store.transaction():
                store.set_meta('work_id_counter', work_schema.MAX_COUNTER)
            before = tuple(store.db.iterdump())
            commands = memory.MemoryCommands(self.path.parent, REPO, store)
            with self.assertRaises(memory.MemoryError_) as refused:
                commands.command(dict(op='work-create', consumer='writer', key='exhausted',
                    deadline=time.time()+600, title='Synthetic', criteria='No room', non_goals='No deployment'), 12345)
            self.assertEqual(refused.exception.code, 'capacity')
            self.assertEqual(tuple(store.db.iterdump()), before)
        finally:
            store.close()

    def test_old_runtime_refuses_new_store_without_writing(self):
        store = memory.Store(self.path, REPO, fts=False)
        store.close()
        before = self.path.read_bytes()
        with patch.object(memory, 'SCHEMA', 4), self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO)
        self.assertEqual(refused.exception.code, 'schema_too_new')
        self.assertEqual(self.path.read_bytes(), before)

    def test_empty_interrupted_schema_five_completes_but_populated_work_is_refused(self):
        store = memory.Store(self.path, REPO, fts=False)
        store.db.execute('DELETE FROM meta')
        store.close()
        adopted = memory.Store(self.path, REPO, fts=False)
        self.assertEqual(adopted.accounting_version(), 5)
        adopted.db.execute('DELETE FROM meta')
        adopted.db.execute("INSERT INTO work_events VALUES (1,?,1,'created','{}')", ('a'*32,))
        adopted.close()
        before = self.path.read_bytes()
        with self.assertRaises(memory.MemoryError_) as refused:
            memory.Store(self.path, REPO)
        self.assertEqual(refused.exception.code, 'incompatible_store')
        self.assertEqual(self.path.read_bytes(), before)


class ActivationServiceTests(unittest.IsolatedAsyncioTestCase):
    async def test_two_consumer_first_adoption_keeps_review_outside_writer_claim(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            service = memory.Service(root, REPO, lambda: memory.Store(root/'memory.sqlite3', REPO, fts=False))
            async def mutation(op, key, **fields):
                return await service.command(dict(op='work-'+op, consumer='writer', key=key,
                    deadline=time.time()+600, **fields), 12345)
            try:
                item = await mutation('create', 'create', title='Synthetic bootstrap',
                                      criteria='Review and complete', non_goals='No deployment')
                started = await mutation('start', 'start', work_id=item['work_id'], if_revision=item['revision'],
                    checkpoint='Initial review', next_artifact='Completed fixture', progress_deadline=time.time()+120)
                view = await service.command(dict(op='work-get', work_id=item['work_id'], consumer='reviewer'), 12346)
                self.assertEqual(view['current_claim']['consumer'], 'writer')
                with self.assertRaises(memory.MemoryError_) as conflict:
                    await service.command(dict(op='work-start', consumer='reviewer', key='competing-start',
                        deadline=time.time()+600, work_id=item['work_id'], if_revision=started['revision'],
                        checkpoint='Review', next_artifact='Review result', progress_deadline=time.time()+120), 12346)
                self.assertEqual(conflict.exception.code, 'claim_conflict')
                snapshot = await service.command(dict(op='sync', consumer='reviewer', record_format=2), 12346)
                await service.command(dict(op='ack', consumer='reviewer', record_format=2,
                                           snapshot_id=snapshot['snapshot_id']), 12346)
                finished = await mutation('finish', 'finish', work_id=item['work_id'], if_revision=started['revision'],
                                         claim_generation=started['claim']['generation'], outcome='completed',
                                         references=['synthetic:test-result'])
                delta = await service.command(dict(op='sync', consumer='reviewer', record_format=2), 12346)
                completed = await service.command(dict(op='work-get', work_id=item['work_id']), 12346)
                self.assertEqual(completed['lifecycle'], 'finished')
                self.assertEqual(delta['entries'][-1]['event_kind'], 'finished')
            finally:
                await service.worker.close()

    async def test_public_service_advertises_formats_and_guards_bound_and_unbound_readers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            service = memory.Service(root, REPO, lambda: memory.Store(root/'memory.sqlite3', REPO, fts=False))
            try:
                for op in ('hello', 'status'):
                    result = await service.command(dict(op=op), None)
                    self.assertEqual((result['protocol'], result['schema']), (1, 5))
                    self.assertTrue({'work_items_v1', 'memory_record_format_2'} <= set(result['capabilities']))
                for targeted in (False, True):
                    for op in ('sync', 'ack'):
                        request = dict(op=op, consumer='old-reader')
                        if targeted:
                            request.update(repo=REPO, generation=service.generation)
                        with self.assertRaises(memory.MemoryError_) as refused:
                            await service.command(request, None)
                        self.assertEqual(refused.exception.code, 'client_upgrade_required')
                status = await service.command(dict(op='status'), None)
                self.assertEqual(status['head'], 0)
                result = await service.command(dict(op='sync', consumer='new-reader', record_format=2), None)
                self.assertEqual(result['kind'], 'snapshot')
            finally:
                await service.worker.close()


if __name__ == '__main__':
    unittest.main()
