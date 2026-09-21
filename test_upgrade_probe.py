"""Private inventory must join kernel PID, durable generation and unreleased gate."""
from contextlib import ExitStack
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

import upgrade_probe as probe


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.component = dict(kind='memory', selection=dict(service_directory='/synthetic/state'))
        self.child = dict(pid=123, generation='b' * 32, proc_start='synthetic-child')
        self.owner = dict(pid=124, generation='c' * 32, child=self.child)
        self.selection = SimpleNamespace(home=Path('/synthetic/state'), owner_path=Path('/synthetic/owner'))
        self.exclusion = SimpleNamespace(loaded=dict(sha256='a' * 64),
            journal=SimpleNamespace(directory=Path('/synthetic/operation')),
            verify=lambda: dict(step=10))
        self.gate = dict(plan='a' * 64, operation='/synthetic/operation',
                         generation='b' * 32, released=False)
        self.status = dict(pid=123, generation='b' * 32, upgrade=self.gate, repo='a' * 16)

    def controls(self, stack, replies):
        stack.enter_context(patch.object(probe, '_selected', return_value=(self.selection, 10)))
        stack.enter_context(patch.object(probe.memory_service, 'managed_status',
            return_value=dict(status='running', managed=True, running=True)))
        stack.enter_context(patch.object(probe.memory_service, 'read_record', return_value=self.owner))
        return stack.enter_context(patch.object(probe, 'control_exchange', new=AsyncMock(side_effect=replies)))

    def test_generation_bound_inventory_is_bracketed_by_owned_gate_observations(self):
        inventory = dict(format=1, synthetic='retained logical records')
        with ExitStack() as stack:
            exchange = self.controls(stack, [(dict(ok=True, result=self.status), 123),
                (dict(ok=True, result=inventory), 123), (dict(ok=True, result=self.status), 123)])
            result = probe.capture(self.exclusion, self.component)
            self.assertEqual(result['inventory'], inventory)
            self.assertEqual(result['owner'], self.owner)
            self.assertEqual(exchange.call_args_list[0].args[1], dict(op='hello'))
            request = exchange.call_args_list[1].args[1]
            self.assertEqual(request, dict(op='upgrade-inventory', plan='a' * 64, generation='b' * 32, repo='a' * 16))

    def test_wrong_kernel_pid_generation_or_released_gate_refuses(self):
        for status, pid in ((self.status, 999),
                            (dict(self.status, generation='d' * 32), 123),
                            (dict(self.status, upgrade=dict(self.gate, released=True)), 123),
                            (dict(self.status, upgrade=dict(self.gate, plan='d' * 64)), 123)):
            with self.subTest(status=status, pid=pid), ExitStack() as stack:
                self.controls(stack, [(dict(ok=True, result=status), pid)])
                with self.assertRaises(probe.ProbeError):
                    probe.gated(self.exclusion, self.component)

    def test_inventory_from_successor_pid_is_not_accepted(self):
        with ExitStack() as stack:
            self.controls(stack, [(dict(ok=True, result=self.status), 123),
                                  (dict(ok=True, result={}), 999)])
            with self.assertRaises(probe.ProbeError):
                probe.capture(self.exclusion, self.component)

    def test_changed_supervisor_during_inventory_refuses(self):
        before = dict(owner=self.owner, children=dict(memory=self.status))
        after = dict(before, owner=dict(self.owner, generation='e' * 32))
        with patch.object(probe, 'gated', side_effect=[before, after]), \
                patch.object(probe, 'control_exchange', new=AsyncMock(return_value=(dict(ok=True, result={}), 123))):
            with self.assertRaises(probe.ProbeError):
                probe.capture(self.exclusion, self.component)

    def test_session_requires_both_bridge_and_notifier_gates(self):
        component = dict(kind='session', selection=dict(state_directory='/synthetic/state'))
        owner = dict(self.owner, children=dict(bridge=self.child,
                     notifier=dict(self.child, pid=125, generation='d' * 32)))
        selection = SimpleNamespace(home=self.selection.home, records=SimpleNamespace(read=lambda: owner))
        notifier = dict(pid=125, generation='d' * 32,
                        upgrade=dict(self.gate, generation='d' * 32, released=True))
        with patch.object(probe, '_selected', return_value=(selection, 10)), \
                patch.object(probe.session_service_manager, 'status',
                             return_value=dict(status='running', managed=True)), \
                patch.object(probe, 'control_exchange', new=AsyncMock(side_effect=[
                    (dict(ok=True, result=self.status), 123), (dict(ok=True, result=notifier), 125)])):
            with self.assertRaises(probe.ProbeError):
                probe.gated(self.exclusion, component)


class LiveMemoryHandshakeTests(unittest.IsolatedAsyncioTestCase):
    async def test_actual_gated_memory_hello_supplies_probe_identity(self):
        import os
        import tempfile
        import memory
        import upgrade_gate
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            gate = SimpleNamespace(status=lambda: dict(plan='a' * 64,
                operation='/synthetic/operation', generation=service.generation, released=False))
            with patch.object(upgrade_gate, 'select', return_value=gate):
                service = memory.Service(root, 'a' * 16,
                    lambda: memory.Store(root / 'memory.sqlite3', 'a' * 16),
                    gated_store_factory=lambda gate: memory.Store(root / 'memory.sqlite3', 'a' * 16, defer_index=True))
            try:
                value = await service.command(dict(op='hello'), os.getpid())
                exclusion = SimpleNamespace(loaded=dict(sha256='a' * 64),
                    journal=SimpleNamespace(directory=Path('/synthetic/operation')))
                probe._gate(exclusion, value,
                            dict(pid=os.getpid(), generation=service.generation), os.getpid())
                self.assertTrue(value['healthy'])
            finally:
                await service.worker.close()
