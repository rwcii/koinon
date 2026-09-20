"""Bounded maintenance on synthetic schema-5 stores; production remains schema 4."""
import asyncio
from contextlib import redirect_stdout
import io
from pathlib import Path
import socket
import subscriptions
import tempfile
import threading
import time
import unittest
from unittest.mock import patch, AsyncMock

import memory
import work_items
import work_maintenance
import work_schema
import work_storage
import test_work_items
from database_worker import CapacityError


class MaintenanceTests(unittest.TestCase):
    setUp = test_work_items.WorkCommandsTests.setUp
    request = test_work_items.WorkCommandsTests.request
    call = test_work_items.WorkCommandsTests.call
    create = test_work_items.WorkCommandsTests.create
    start = test_work_items.WorkCommandsTests.start
    mutation = test_work_items.WorkCommandsTests.mutation

    def sweep(self, now):
        return self.commands.maintenance.sweep(now)

    def finish(self):
        item = self.start(self.create())
        return self.call(self.mutation('finish', item, outcome='completed', references=['tests']))

    def test_expiry_wins_one_event_and_clock_steps_do_not_duplicate(self):
        item = self.start(self.create())
        wake = []
        self.store.on_change = lambda: wake.append(self.store.head())
        before = self.store.head()
        now = item['claim']['expires_at'] + 1
        result = self.sweep(now)
        self.assertEqual(self.store.head(), before + 1)
        self.assertEqual(wake, [before + 1])
        self.assertEqual(self.store.db.execute('SELECT kind FROM work_events ORDER BY seq DESC LIMIT 1').fetchone()[0], 'lease-expired')
        self.assertEqual(result['pending_due'], 0)
        self.assertEqual(result['last_successful_sweep'], now)
        self.sweep(now - 100)
        self.sweep(now + 100)
        self.assertEqual(self.store.head(), before + 1)
        self.assertEqual(self.store.work_debt().event_slots, 0)

    def test_overdue_then_expiry_and_interrupted_batch_resume(self):
        items = [self.start(self.create()) for _ in range(3)]
        before = self.store.head()
        real = work_items.WorkItems.reconcile
        count = 0
        def fail_second(work, target, now):
            nonlocal count
            count += 1
            if count == 2:
                raise OSError('injected')
            return real(work, target, now)
        with patch.object(work_items.WorkItems, 'reconcile', fail_second):
            with self.assertRaises(OSError):
                self.sweep(self.now + 601)
        state = self.commands.maintenance.state.snapshot()
        self.assertEqual(state['fault'], 'storage_error')
        self.assertEqual(state['pending_due'], 2)
        self.assertEqual(self.store.head(), before + 1)
        self.sweep(self.now + 601)
        self.assertEqual(self.store.head(), before + 3)
        self.sweep(self.now + 601)
        self.assertEqual(self.store.head(), before + 3)
        self.sweep(items[0]['claim']['expires_at'] + 1)
        self.assertEqual(self.store.head(), before + 6)
        self.assertIsNone(self.commands.maintenance.state.snapshot()['fault'])

    def test_batch_limit_is_applied_by_query(self):
        for _ in range(3):
            self.start(self.create())
        before = self.store.head()
        with patch.object(work_maintenance, 'MAX_TRANSITIONS', 2):
            result = self.sweep(self.now + 601)
        self.assertEqual(self.store.head(), before + 2)
        self.assertEqual(result['pending_due'], 1)
        # Production allows only 16 claims. Exercise the scheduler's literal 32
        # boundary independently, without inventing an invalid 33-claim store.
        maintenance = self.commands.maintenance
        with patch.object(maintenance.work, 'due_targets', return_value=range(32)) as targets, \
             patch.object(maintenance.work, 'reconcile') as reconcile:
            maintenance.sweep(self.now)
        targets.assert_called_once_with(self.now, 32)
        self.assertEqual(reconcile.call_count, 32)

    def test_one_bundle_and_item_per_job_preserves_open_work_and_snapshots(self):
        finished = [self.finish(), self.finish()]
        active = self.start(self.create())
        open_item = self.create()
        self.commands.sync(dict(consumer='reader', record_format=2))
        snapshots = self.store.db.execute('SELECT * FROM snapshot_items').fetchall()
        replay = self.store.db.execute('SELECT * FROM idem').fetchall()
        # Keep another active obligation during reclamation to check debt preservation.
        with self.store.transaction():
            self.store.db.execute('UPDATE work_items SET expires_at=? WHERE lifecycle=?', (self.now - 1, 'finished'))
        debt = self.store.work_debt()
        head, counter = self.store.head(), self.store.meta('work_id_counter')
        result = self.sweep(self.now)
        self.assertEqual(result['expired_items'], 1)
        self.assertEqual(result['inactive_bundles'], 1)
        self.assertEqual(self.store.work_debt(), debt)
        self.assertEqual(self.store.head(), head)
        self.assertEqual(self.store.meta('work_id_counter'), counter)
        self.assertEqual(self.store.db.execute('SELECT * FROM snapshot_items').fetchall(), snapshots)
        self.assertEqual(self.store.db.execute('SELECT * FROM idem').fetchall(), replay)
        self.assertEqual(self.store.floor(), finished[0]['seq'])
        self.assertTrue(self.work.get(active['work_id'], self.now)['lease_valid'])
        self.assertEqual(self.work.get(open_item['work_id'], self.now)['lifecycle'], 'open')
        self.sweep(self.now)
        self.assertEqual(self.store.floor(), finished[1]['seq'])
        self.assertEqual(self.sweep(self.now)['expired_items'], 0)
        self.assertGreater(self.create()['work_id'], open_item['work_id'])

    def test_atomic_capacity_failure_hidden_item_stays_intact(self):
        item = self.finish()
        with self.store.transaction(control=False):
            self.work.engine().reclaim_one()
        now = self.now + work_items.RETENTION
        before = tuple(self.store.db.iterdump())
        with patch.object(self.store, 'enforce_pages', side_effect=memory.MemoryError_('capacity', 'injected')):
            with self.assertRaises(memory.MemoryError_):
                self.sweep(now)
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        with self.assertRaises(memory.MemoryError_) as error:
            self.work.get(item['work_id'], now)
        self.assertEqual(error.exception.code, 'work_not_found')
        self.assertEqual(self.commands.maintenance.state.snapshot()['fault'], 'capacity')
        self.assertEqual(self.sweep(now)['expired_items'], 0)

    def test_background_cleanup_converges_above_ordinary_page_ceiling(self):
        finished = self.finish()
        active = self.start(self.create())
        with self.store.transaction():
            self.store.db.execute('UPDATE work_items SET expires_at=? WHERE work_id=?',
                                  (self.now - 1, finished['work_id']))
        debt = self.store.work_debt()
        ordinary = self.store.ceilings(False, debt)['pages']
        # Allocate a real database file into control headroom, then leave its pages
        # on the freelist. Deletion is not guaranteed to shrink page_count.
        with self.store.transaction(control=True):
            self.store.db.execute('CREATE TABLE synthetic_padding(body BLOB)')
            self.store.db.execute('INSERT INTO synthetic_padding VALUES(zeroblob(?))',
                                  ((ordinary + 4 - self.store.pages()) * memory.PAGE_SIZE,))
            self.store.db.execute('DROP TABLE synthetic_padding')
        self.assertGreater(self.store.pages(), ordinary)
        self.assertLess(self.store.pages(), self.store.ceilings(True, debt)['pages'])
        # Start-boundary cleanup still cannot borrow this headroom.
        with self.assertRaises(memory.MemoryError_) as caught:
            with self.store.transaction(control=False):
                self.work.engine().reclaim_one()
        self.assertEqual(caught.exception.code, 'capacity')
        result = self.sweep(self.now)
        self.assertEqual(result['inactive_bundles'], 0)
        self.assertEqual(result['expired_items'], 0)
        self.assertIsNone(result['fault'])
        self.assertEqual(self.store.work_debt(), debt)
        self.assertTrue(self.work.get(active['work_id'], self.now)['lease_valid'])
        self.assertEqual(self.store.floor(), finished['seq'])

    def test_corrupt_inactive_obligations_are_reported_without_skipping(self):
        self.finish()
        self.store.db.execute('UPDATE claim_bundles SET end_credit=1 WHERE active=0')
        before = tuple(self.store.db.iterdump())
        with self.assertRaises(memory.MemoryError_) as error:
            self.sweep(self.now + work_items.RETENTION)
        self.assertEqual(error.exception.code, 'incompatible_store')
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        self.assertEqual(self.commands.maintenance.state.snapshot()['fault'], 'incompatible_store')

    def test_diagnostic_query_failure_does_not_discard_readable_status(self):
        with patch.object(self.commands.maintenance.work, 'maintenance_counts', side_effect=OSError('injected')):
            status = self.commands.status()
        self.assertEqual(status['head'], self.store.head())
        self.assertIn('usage', status)
        self.assertEqual(status['work_maintenance']['fault'], 'storage_error')
        self.assertIsNone(status['work_maintenance']['observed_at'])

    def test_sweep_reports_completion_time(self):
        with patch.object(work_maintenance.time, 'time', side_effect=[self.now, self.now + 10]):
            result = self.commands.maintenance.sweep()
        self.assertEqual(result['observed_at'], self.now)
        self.assertEqual(result['last_successful_sweep'], self.now + 10)

    def test_failure_between_deletes_rolls_back_floor_and_both_histories(self):
        self.finish()
        with self.store.transaction(control=False):
            self.work.engine().reclaim_one()
        self.store.db.execute("CREATE TEMP TRIGGER fail_delete BEFORE DELETE ON work_items BEGIN SELECT RAISE(ABORT, 'injected'); END")
        before = tuple(self.store.db.iterdump())
        with self.assertRaises(Exception):
            self.sweep(self.now + work_items.RETENTION)
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        self.store.db.execute('DROP TRIGGER fail_delete')
        self.assertEqual(self.sweep(self.now + work_items.RETENTION)['expired_items'], 0)

    def test_maximum_history_is_removed_without_fts_deletes(self):
        item = self.finish()
        with self.store.transaction(control=False):
            self.work.engine().reclaim_one()
        with self.store.transaction():
            for revision in range(2, work_storage.MAX_SCOPES_PER_ITEM + 1):
                self.store.db.execute('INSERT INTO work_scope_revisions SELECT work_id,?,ts,consumer,author,title,criteria,non_goals FROM work_scope_revisions WHERE work_id=? AND revision=1', (revision, item['work_id']))
            for _ in range(work_storage.MAX_EVENTS - 3):
                seq = self.store.work_event('updated', item['work_id'], item['revision'], {}, consumer='writer', author=None, pid=None, now=self.now)
            self.store.db.execute('UPDATE work_items SET latest_seq=? WHERE work_id=?', (seq, item['work_id']))
        head = self.store.head()
        with patch.object(self.store, '_unindex', side_effect=AssertionError('work has no FTS posting')):
            self.sweep(self.now + work_items.RETENTION)
        for table in ('work_items', 'work_events', 'work_scope_revisions', 'entries'):
            self.assertEqual(self.store.db.execute('SELECT count(*) FROM ' + table).fetchone()[0], 0)
        self.assertEqual(self.store.floor(), head)
        self.assertEqual(self.store.head(), head)
        self.assertEqual(self.store.db.execute('PRAGMA integrity_check').fetchone()[0], 'ok')

    def test_corrupt_stream_pair_is_not_deleted(self):
        item = self.finish()
        with self.store.transaction(control=False):
            self.work.engine().reclaim_one()
        self.store.db.execute("UPDATE entries SET type='decision' WHERE seq=?", (item['seq'],))
        before = tuple(self.store.db.iterdump())
        with self.assertRaises(memory.MemoryError_) as error:
            self.sweep(self.now + work_items.RETENTION)
        self.assertEqual(error.exception.code, 'incompatible_store')
        self.assertEqual(tuple(self.store.db.iterdump()), before)

    def test_blocked_status_and_reads_observe_without_writes(self):
        item = self.start(self.create())
        self.store.blocked = 'synthetic blocked store'
        before = tuple(self.store.db.iterdump())
        with patch('time.time', return_value=self.now + 601):
            state = self.commands.status()['work_maintenance']
        self.assertEqual(state['pending_due'], 1)
        self.assertEqual(state['observed_at'], self.now + 601)
        self.work.get(item['work_id'], self.now + 601)
        self.assertEqual(tuple(self.store.db.iterdump()), before)


async def until(predicate):
    async with asyncio.timeout(5):
        while not predicate():
            await asyncio.sleep(.001)


class MaintenanceLoopTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.services = []
        self.tasks = []

    async def asyncTearDown(self):
        for task in self.tasks:
            task.cancel()
        await asyncio.gather(*self.tasks, return_exceptions=True)
        for service in self.services:
            await service.worker.close()
        self.tmp.cleanup()

    def service(self, staged=True):
        def factory():
            store = memory.Store(self.root / 'memory.sqlite3', '0123456789abcdef')
            if staged:
                with store.transaction():
                    work_schema.migrate(store.db, store.repo, memory.SCHEMA_STATEMENTS)
            return store
        service = memory.Service(self.root, '0123456789abcdef', factory)
        self.services.append(service)
        return service

    def loop(self, service):
        task = asyncio.create_task(service.maintain_work())
        self.tasks.append(task)
        return task

    async def test_schema4_exits_disabled_without_fault(self):
        service = self.service(False)
        await asyncio.wait_for(self.loop(service), 2)
        state = service.maintenance_state.snapshot()
        self.assertFalse(state['enabled'])
        self.assertIsNone(state['last_successful_sweep'])
        self.assertIsNone(state['fault'])
        status = await service.command(dict(op='status'), None)
        self.assertNotIn('work_items', status['capabilities'])
        self.assertEqual(status['schema'], 4)

    async def test_full_queue_skips_and_does_not_use_reserved_priority(self):
        service = self.service()
        call = AsyncMock(side_effect=[CapacityError(), {'enabled': False}])
        with patch.object(service.worker, 'call', call), patch.object(work_maintenance, 'INTERVAL', .01):
            await asyncio.wait_for(self.loop(service), 2)
        self.assertEqual(service.maintenance_state.snapshot()['skipped_submissions'], 1)
        self.assertEqual(call.call_count, 2)
        for args in call.call_args_list:
            self.assertEqual(args.args, ('maintain_work',))
            self.assertEqual(args.kwargs, {})

    async def test_no_overlap_and_cancelled_waiter_drains_accepted_job(self):
        service = self.service()
        entered, release, finished = threading.Event(), threading.Event(), threading.Event()
        real = memory.MemoryCommands.maintain_work
        def blocked(commands):
            entered.set()
            if not release.wait(5):
                raise RuntimeError('test barrier timed out')
            result = real(commands)
            finished.set()
            return result
        try:
            with patch.object(memory.MemoryCommands, 'maintain_work', blocked), patch.object(work_maintenance, 'INTERVAL', .001):
                task = self.loop(service)
                await until(entered.is_set)
                await asyncio.sleep(.025)
                self.assertEqual(service.worker.snapshot()['ordinary_queued'], 0)
                task.cancel()
                await asyncio.gather(task, return_exceptions=True)
                close = asyncio.create_task(service.worker.close())
                await asyncio.sleep(.01)
                self.assertFalse(close.done())
                release.set()
                await close
                self.assertTrue(finished.is_set())
                self.assertIsNone(service.maintenance_state.snapshot()['fault'])
        finally:
            release.set()

    async def test_startup_reports_readiness_before_sweep_and_shutdown_waits(self):
        service = self.service()
        entered, release = threading.Event(), threading.Event()
        output = io.StringIO()
        ready = []
        real = memory.MemoryCommands.maintain_work
        def blocked(commands):
            ready.append(bool(output.getvalue()))
            entered.set()
            if not release.wait(5):
                raise RuntimeError('test barrier timed out')
            return real(commands)
        sock = socket.socket(socket.AF_UNIX)
        sock.bind(str(self.root / 'control.sock'))
        try:
            with patch.object(memory.MemoryCommands, 'maintain_work', blocked), redirect_stdout(output):
                task = asyncio.create_task(service.run(sock))
                self.tasks.append(task)
                await until(entered.is_set)
                service.stop.set()
                await asyncio.sleep(.02)
                self.assertFalse(task.done())
                release.set()
                await asyncio.wait_for(task, 2)
            self.assertEqual(ready, [True])
            self.assertTrue(service.worker.snapshot()['closing'])
        finally:
            release.set()
            sock.close()

    async def test_idle_subscriber_wakes_after_due_commit(self):
        service = self.service()
        old = time.time() - 1000
        # Seed a due item on the owning worker; no request drives its reconciliation.
        def seed(commands):
            work = work_items.WorkItems(commands.store, memory.MemoryError_)
            created = work.command(dict(op='work-create', consumer='writer', key='create',
                deadline=old + 3600, title='Synthetic', criteria='Pass', non_goals='None'), now=old)
            work.command(dict(op='work-start', consumer='writer', key='start', deadline=old + 3600,
                work_id=created['work_id'], if_revision=1, checkpoint='Ready', next_artifact='Test',
                progress_deadline=old + 600), now=old)
        with patch.object(memory.MemoryCommands, 'maintain_work', seed):
            await service.worker.call('maintain_work')
        path = self.root / 'control.sock'
        server = await asyncio.start_unix_server(service.handle, path)
        path.chmod(0o600)
        try:
            async with subscriptions.open_hints(self.root,
                    dict(op='subscribe', protocol=1, generation=service.generation,
                         repo=service.repo, consumer='reader'), legacy_socket=path) as connection:
                self.loop(service)
                await asyncio.wait_for(connection.changed(), 2)
                status = await service.command(dict(op='status'), None)
                self.assertEqual(status['head'], 3)
                self.assertEqual(status['work_maintenance']['pending_due'], 0)
        finally:
            server.close()
            service.hints.close()
            await server.wait_closed()

    async def test_status_timeout_uses_timestamped_cached_observation(self):
        service = self.service()
        state = service.maintenance_state
        state.update(enabled=True, observed_at=123, pending_due=2)
        with patch('memory.database_status', AsyncMock(return_value=(None, {'database_observed_fault': None}))):
            status = await service.command(dict(op='status'), None)
        self.assertEqual(status['work_maintenance']['observed_at'], 123)
        self.assertEqual(status['work_maintenance']['pending_due'], 2)
