"""Admission during upgrade, tested with private synthetic services and records."""
import asyncio
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

import bridge
from koinon import durable_state
import session_service
from koinon import session_service_artifacts
from koinon import session_service_manager
from koinon.database_worker import DatabaseWorker
import memory
from koinon import session_service_config
import test_upgrade_plan as fixtures
from koinon import upgrade_exclusion
from koinon import upgrade_gate
from koinon.upgrade_journal import Journal
from koinon import upgrade_plan


class GateTests(unittest.TestCase):
    originally_running = True
    def setUp(self):
        fixtures.PlanTests.setUp(self)
        self.home = Path(self.config['state_root']) / 'sessions'
        self.home.mkdir(parents=True, mode=0o700)
        self.home.parent.chmod(0o700)
        registration = dict(thread='synthetic', name='synthetic', repo=str(self.root))
        key, _, _ = session_service_config.registration_identity(registration)
        self.home = self.home / key
        self.home.mkdir(mode=0o700)
        record = session_service_config.selection(self.prefix, sys.executable, self.home,
                                                 self.config, registration, 'systemd')
        running = getattr(self, 'originally_running', False)
        component = dict(kind='session', selection=record,
                         manager=dict(status='observed' if running else 'absent'),
                         registered=running, running=running, owner={} if running else None)
        self.prepared = fixtures.PlanTests.prepare(self, components=[component])
        import json
        (self.prefix / 'install.json').write_text(json.dumps(self.config))
        (self.prefix / 'install.json').chmod(0o600)
        self.record = record
        (self.home / 'native-service').mkdir(mode=0o700)
        durable_state.publish(self.home / 'session.json', registration)
        session_service_artifacts.publish(record)
        self.journal = Journal(self.operation, self.prepared['sha256'])
        with upgrade_exclusion.operation(self.operation, self.prepared['sha256']) as owner:
            owner.activate()

    def advance(self, step):
        while self.journal.read()['step'] < step:
            current = self.journal.read()
            evidence = 'a' * 64 if current['step'] % 2 == 0 else None
            if current['step'] == 16:
                from koinon.upgrade_documents import Documents
                evidence = Documents(self.operation).put('release', dict(version=1,
                    plan=self.prepared['sha256'], members=[
                        dict(component=0, kind='bridge', generation='b' * 32),
                        dict(component=0, kind='notifier', generation='c' * 32)]))
            self.journal.advance(current, evidence=evidence)

    def gate(self):
        return upgrade_gate.select(self.prefix, 'bridge', self.home, 'b' * 32)

    def test_selected_session_verifies_owned_artifact_while_excluded(self):
        selection = session_service.Selection(self.prefix, self.home, upgrading=True)
        self.assertEqual(selection.record, self.record)
        observed = dict(status='observed', artifact=self.record['artifact'], pid=0,
                        argv=session_service.platform_support.session_service_command(self.record))
        observed['executable'] = observed['argv'][0]
        with patch.object(session_service.platform_support, 'session_manager_observation', return_value=observed):
            self.assertEqual(session_service_manager.observation(selection), observed)
        Path(self.record['artifact']).write_text('changed artifact')
        with self.assertRaises(ValueError):
            session_service.Selection(self.prefix, self.home, upgrading=True)

    def test_upgrade_access_still_refuses_ordinary_ensure_and_missing_marker(self):
        selection = session_service.Selection(self.prefix, self.home, upgrading=True)
        with self.assertRaises(ValueError):
            session_service_manager.ensure(selection)
        (self.prefix / 'install.json').unlink()
        with self.assertRaises(ValueError):
            session_service_artifacts.verify_owned(self.record, upgrading=True)

    def test_new_runtime_cannot_open_selected_state_before_replacement_phase(self):
        with self.assertRaises(upgrade_gate.GateError):
            self.gate()
        self.advance(10)
        self.assertFalse(self.gate().released())

    def test_release_pending_keeps_gate_closed_and_completion_releases(self):
        self.advance(10)
        gate = self.gate()
        self.advance(16)
        self.assertFalse(gate.released())
        self.advance(17)
        self.assertTrue(gate.released())
        self.assertEqual(gate.status()['generation'], 'b' * 32)

    def test_missing_marker_does_not_release_an_already_bound_gate(self):
        self.advance(10)
        gate = self.gate()
        (self.prefix / 'install.json').unlink()
        with self.assertRaises(upgrade_gate.GateError):
            gate.released()

    def test_state_outside_selection_and_wrong_inventory_generation_refuse(self):
        self.advance(10)
        with self.assertRaises(upgrade_gate.GateError):
            upgrade_gate.select(self.prefix, 'bridge', self.source, 'other')
        gate = self.gate()
        request = dict(op='upgrade-inventory', plan=self.prepared['sha256'], generation='other')
        with self.assertRaises(upgrade_gate.GateError):
            gate.authorize_inventory(request)
        request['generation'] = gate.generation
        gate.authorize_inventory(request)
        self.advance(17)
        with self.assertRaises(upgrade_gate.GateError):
            gate.authorize_inventory(request)

    def test_restarted_unverified_child_does_not_inherit_global_release(self):
        self.advance(16)
        verified = self.gate()
        successor = upgrade_gate.select(self.prefix, 'bridge', self.home, 'd' * 32)
        notifier = upgrade_gate.select(self.prefix, 'notifier', self.home, 'c' * 32)
        self.advance(17)
        self.assertTrue(verified.released())
        self.assertTrue(notifier.released())
        with self.assertRaisesRegex(upgrade_gate.GateError, 'not verified'):
            successor.released()

    def test_missing_release_membership_evidence_never_releases(self):
        self.advance(16)
        gate = self.gate()
        self.journal.advance(self.journal.read(), evidence='f' * 64)
        with self.assertRaises(ValueError):
            gate.released()

    def test_post_release_restart_is_live_readiness_not_another_comparison(self):
        self.advance(17)
        successor = upgrade_gate.select(self.prefix, 'bridge', self.home, 'd' * 32)
        self.assertTrue(successor.released())
        self.assertTrue(successor.status()['post_release_start'])
        with self.assertRaises(upgrade_gate.GateError):
            successor.authorize_inventory(dict(op='upgrade-inventory', plan=self.prepared['sha256'], generation='d' * 32))

    def test_gate_creation_reads_current_durable_phase_not_old_plan_snapshot(self):
        self.advance(16)
        loaded = upgrade_exclusion.read(self.prefix)
        self.advance(17)
        successor = upgrade_gate.Gate(loaded, 'bridge', self.home, 'd' * 32)
        self.assertTrue(successor.post_release_start)
        self.assertTrue(successor.released())


class InactiveGateTests(unittest.TestCase):
    def test_inactive_component_cannot_restart_after_release(self):
        GateTests.setUp(self)
        GateTests.advance(self, 17)
        with self.assertRaisesRegex(upgrade_gate.GateError, 'inactive'):
            upgrade_gate.select(self.prefix, 'bridge', self.home, 'd' * 32)


class ClosedGate:
    def released(self):
        return False

    def status(self):
        return dict(plan='a' * 64, generation='synthetic', released=False)

    async def wait(self, stop):
        await stop.wait()
        return False


class RuntimeAdmissionTests(unittest.IsolatedAsyncioTestCase):
    async def wait_until(self, predicate, task):
        async with asyncio.timeout(5):
            while not predicate():
                if task.done():
                    await task
                    self.fail('service exited before readiness')
                await asyncio.sleep(.005)

    async def test_bridge_listener_opens_after_gate_release(self):
        import contextlib
        import io
        import tempfile
        class ReleaseGate(ClosedGate):
            def __init__(self):
                self.release = asyncio.Event()
            def released(self):
                return self.release.is_set()
            async def wait(self, stop):
                await self.release.wait()
                return True
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            gate = ReleaseGate()
            with patch.object(upgrade_gate, 'select', return_value=gate):
                service = bridge.Bridge(root)
            peer = root / 'peer.sock'
            service.address = 'uds:' + str(peer)
            with contextlib.redirect_stdout(io.StringIO()) as output:
                task = asyncio.create_task(service.run())
                writer = None
                try:
                    await self.wait_until(lambda: bool(output.getvalue()), task)
                    gate.release.set()
                    async with asyncio.timeout(5):
                        while writer is None:
                            try:
                                _, writer = await asyncio.open_unix_connection(str(peer))
                            except ConnectionRefusedError:
                                await asyncio.sleep(.005)
                    writer.close()
                    await writer.wait_closed()
                    # Ordinary control admission is restored by the same decision.
                    self.assertEqual(await service.command(dict(op='ack', through=0)), 'acknowledged locally')
                finally:
                    gate.release.set()
                    service.stop.set()
                    await asyncio.wait_for(task, 5)

    async def test_peer_listener_waits_for_release_and_gate_fault_drains_worker(self):
        import contextlib
        import io
        import socket
        import tempfile
        from koinon.peer_transport import control_exchange
        class FaultGate(ClosedGate):
            def __init__(self):
                self.fail = asyncio.Event()
            async def wait(self, stop):
                await self.fail.wait()
                raise upgrade_gate.GateError('synthetic missing evidence')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            gate = FaultGate()
            with patch.object(upgrade_gate, 'select', return_value=gate):
                service = bridge.Bridge(root)
            peer = root / 'peer.sock'
            service.address = 'uds:' + str(peer)
            with contextlib.redirect_stdout(io.StringIO()) as output:
                task = asyncio.create_task(service.run())
                try:
                    await self.wait_until(lambda: bool(output.getvalue()), task)
                    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                        with self.assertRaises(ConnectionRefusedError):
                            client.connect(str(peer))
                    reply, _ = await control_exchange(root, dict(op='status'))
                    self.assertFalse(reply['result']['upgrade']['released'])
                    gate.fail.set()
                    with self.assertRaisesRegex(upgrade_gate.GateError, 'missing evidence'):
                        await asyncio.wait_for(task, 5)
                    self.assertFalse(peer.exists())
                    self.assertFalse(bridge.platform_support.control_socket_path(root).exists())
                    self.assertTrue(service.closing)
                finally:
                    service.stop.set()
                    gate.fail.set()
                    await asyncio.gather(task, return_exceptions=True)

    async def test_notifier_does_not_publish_or_deliver_before_release(self):
        import argparse
        import contextlib
        import io
        import tempfile
        from koinon import notification_runtime
        from koinon.participant_lock import identity
        from test_notification_runtime import SyntheticProvider
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            state = root / 'state'
            state.mkdir(mode=0o700)
            bus = bridge.Bridge(state)
            bus.address = 'uds:' + str(root / 'peer.sock')
            provider = SyntheticProvider()
            options = argparse.Namespace(agent='codex', thread='synthetic-upgrade', after=0,
                                         name='synthetic', repo=str(root))
            with patch.object(upgrade_gate, 'select', return_value=ClosedGate()):
                notifier = notification_runtime.Runtime(options, state, identity('codex', options.thread),
                                                       provider=provider, registry=root / 'registry')
            with contextlib.redirect_stdout(io.StringIO()) as output:
                bus_task = asyncio.create_task(bus.run())
                notifier_task = None
                try:
                    await self.wait_until(lambda: bool(output.getvalue()), bus_task)
                    notifier_task = asyncio.create_task(notifier.run())
                    await self.wait_until(lambda: (state / 'notify-ready.json').exists(), notifier_task)
                    self.assertIsNone(notifier.delivery)
                    self.assertIsNone(notifier.worker)
                    self.assertFalse((root / 'registry').exists())
                    self.assertEqual(provider.messages, [])
                    notifier.stop.set()
                    await asyncio.wait_for(notifier_task, 5)
                    self.assertFalse((state / 'notify-ready.json').exists())
                finally:
                    notifier.stop.set()
                    bus.stop.set()
                    await asyncio.gather(*([bus_task, notifier_task] if notifier_task else [bus_task]),
                                         return_exceptions=True)

    async def test_bridge_mutation_refused_before_worker_submission(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            with patch.object(upgrade_gate, 'select', return_value=ClosedGate()):
                service = bridge.Bridge(root)
            service.worker = DatabaseWorker(lambda: bridge.InboxStore(root))
            try:
                before = await service.command(dict(op='status'))
                with self.assertRaisesRegex(ValueError, 'upgrade in progress'):
                    await service.command(dict(op='ack', through=1))
                after = await service.command(dict(op='status'))
                self.assertEqual(before['ack_through'], after['ack_through'])
                self.assertFalse(after['upgrade']['released'])
                await service.command(dict(op='stop'))
                self.assertTrue(service.stop.is_set())
            finally:
                await service.worker.close()

    async def test_gated_memory_refuses_factory_without_deferred_startup(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            with patch.object(upgrade_gate, 'select', return_value=ClosedGate()):
                with self.assertRaises(upgrade_gate.GateError):
                    memory.Service(root, 'synthetic', lambda: self.fail('ordinary factory ran'))

    async def test_memory_mutation_and_maintenance_remain_gated(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            with patch.object(upgrade_gate, 'select', return_value=ClosedGate()):
                service = memory.Service(root, 'synthetic', lambda: memory.Store(root / 'memory.sqlite3', 'synthetic'),
                                         gated_store_factory=lambda gate: memory.Store(root / 'memory.sqlite3', 'synthetic', defer_index=True))
            maintenance = asyncio.create_task(service.maintain_work())
            try:
                before = await service.command(dict(op='status'), 1)
                with self.assertRaisesRegex(ValueError, 'upgrade in progress'):
                    await service.command(dict(op='note', body='must not be written', type='finding', consumer='synthetic'), 1)
                after = await service.command(dict(op='status'), 1)
                self.assertEqual(before['head'], after['head'])
                self.assertFalse(after['upgrade']['released'])
                service.stop.set()
                await asyncio.wait_for(maintenance, 2)
            finally:
                maintenance.cancel()
                await service.worker.close()

    async def test_deferred_index_retries_capacity_without_stopping_service(self):
        from types import SimpleNamespace
        from unittest.mock import AsyncMock, Mock
        from koinon.database_worker import CapacityError
        stop = asyncio.Event()
        call = AsyncMock(side_effect=[CapacityError(), None, {'enabled': False}])
        service = SimpleNamespace(upgrade=SimpleNamespace(wait=AsyncMock(return_value=True)),
                                  stop=stop, closing=False, worker=SimpleNamespace(call=call),
                                  maintenance_state=SimpleNamespace(skipped=Mock()))
        with patch.object(memory.work_maintenance, 'INTERVAL', .001):
            await memory.Service.maintain_work(service)
        self.assertEqual([item.args for item in call.call_args_list],
                         [('resume_index',), ('resume_index',), ('maintain_work',)])
        service.maintenance_state.skipped.assert_called_once_with()
        self.assertFalse(stop.is_set())

    async def test_stop_during_deferred_index_capacity_wait_prevents_retry(self):
        from types import SimpleNamespace
        from unittest.mock import AsyncMock, Mock
        from koinon.database_worker import CapacityError
        stop = asyncio.Event()
        def busy(*args):
            stop.set()
            raise CapacityError()
        call = AsyncMock(side_effect=busy)
        service = SimpleNamespace(upgrade=SimpleNamespace(wait=AsyncMock(return_value=True)),
                                  stop=stop, closing=False, worker=SimpleNamespace(call=call),
                                  maintenance_state=SimpleNamespace(skipped=Mock()))
        await memory.Service.maintain_work(service)
        call.assert_called_once_with('resume_index')

    async def test_real_full_worker_queue_does_not_kill_deferred_index_task(self):
        import threading
        from types import SimpleNamespace
        from unittest.mock import AsyncMock
        entered, release = threading.Event(), threading.Event()
        resumed = []
        class Owner:
            def status(self):
                entered.set()
                if not release.wait(5):
                    raise RuntimeError('synthetic blocker timed out')
            def ordinary(self):
                return None
            def resume_index(self):
                resumed.append(True)
            def maintain_work(self):
                return {'enabled': False}
            def close(self):
                pass
        worker = DatabaseWorker(Owner)
        state = memory.work_maintenance.MaintenanceState()
        service = SimpleNamespace(upgrade=SimpleNamespace(wait=AsyncMock(return_value=True)),
                                  stop=asyncio.Event(), closing=False, worker=worker,
                                  maintenance_state=state)
        pending, maintenance = [], None
        try:
            pending.append(asyncio.create_task(worker.call('status', priority=True)))
            self.assertTrue(await asyncio.to_thread(entered.wait, 2))
            pending.extend(asyncio.create_task(worker.call('ordinary')) for _ in range(16))
            async with asyncio.timeout(2):
                while worker.snapshot()['ordinary_queued'] != 16:
                    await asyncio.sleep(.001)
            with patch.object(memory.work_maintenance, 'INTERVAL', .001):
                maintenance = asyncio.create_task(memory.Service.maintain_work(service))
                async with asyncio.timeout(2):
                    while state.snapshot()['skipped_submissions'] == 0:
                        if maintenance.done():
                            await maintenance
                            self.fail('maintenance exited before retry')
                        await asyncio.sleep(.001)
                self.assertFalse(service.stop.is_set())
                release.set()
                await asyncio.wait_for(asyncio.gather(*pending), 3)
                await asyncio.wait_for(maintenance, 3)
            self.assertEqual(resumed, [True])
            self.assertFalse(service.stop.is_set())
        finally:
            release.set()
            await asyncio.gather(*pending, return_exceptions=True)
            if maintenance is not None:
                service.stop.set()
                await asyncio.gather(maintenance, return_exceptions=True)
            await worker.close()


class DeferredIndexTests(unittest.TestCase):
    def test_same_schema_gated_open_preserves_catalog_and_metadata_until_release(self):
        import tempfile
        from koinon import upgrade_inventory
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'memory.sqlite3'
            original = memory.Store(path, 'a' * 16, fts=False)
            try:
                before = upgrade_inventory.capture(original.db)
            finally:
                original.close()
            gated = memory.Store(path, 'a' * 16, defer_index=True)
            try:
                self.assertEqual(upgrade_inventory.capture(gated.db), before)
                with patch.object(gated, '_open_fts', wraps=gated._open_fts) as open_index:
                    gated.resume_index()
                    open_index.assert_called_once_with()
                    gated.resume_index()
                    open_index.assert_called_once_with()
            finally:
                gated.close()
