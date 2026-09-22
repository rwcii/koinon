"""Foreground handoff never infers health from a command or stale owner record."""
from contextlib import ExitStack
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import upgrade_manual as manual
import upgrade_observation


class ManualTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.home = Path(temporary.name)
        self.selection = SimpleNamespace(backend='manual', home=self.home,
            record={'identity': 'synthetic'}, owner_path=self.home / 'supervisor.json',
            start_command=lambda: ['/synthetic/python', '/synthetic/runtime/memory_service.py', 'run'])
        self.exclusion = SimpleNamespace(journal=SimpleNamespace(directory=self.home / 'operation'),
            loaded={'sha256': 'a' * 64}, verify=lambda: {'step': 10})
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.read = self.stack.enter_context(patch.object(manual.memory_service, 'read_record', return_value=None))
        self.portable = self.stack.enter_context(patch.object(manual.memory_service, 'observation',
            return_value={'status': 'unavailable'}))
        self.state = self.stack.enter_context(patch.object(manual.memory_service, 'process_state', return_value='dead'))
        self.manager = self.stack.enter_context(patch.object(manual.memory_service, 'manager_observation'))

    def test_stopped_returns_exact_pending_handoff_without_starting_or_native_calls(self):
        with patch('subprocess.Popen') as spawn, self.assertRaises(manual.HandoffRequired) as caught:
            manual.ensure(self.exclusion, self.selection, 'memory')
        pending = caught.exception.handoff
        self.assertEqual(pending['argv'], self.selection.start_command())
        self.assertEqual(pending['plan'], 'a' * 64)
        self.assertEqual(pending['phase'], 10)
        self.assertEqual(pending['status'], 'manual_handoff_required')
        spawn.assert_not_called()
        self.manager.assert_not_called()

    def test_running_requires_stable_live_owner_and_portable_child_handshake(self):
        owner = {'pid': 123, 'generation': 'synthetic', 'child': {'pid': 124}}
        self.read.return_value = owner
        self.portable.return_value = {'status': 'running'}
        self.state.return_value = 'alive'
        self.assertEqual(manual.ensure(self.exclusion, self.selection, 'memory')['status'], 'running')
        self.state.return_value = 'unknown'
        with self.assertRaises(upgrade_observation.ObservationError):
            manual.ensure(self.exclusion, self.selection, 'memory')
        self.state.return_value = 'alive'
        self.read.side_effect = [owner, dict(owner, pid=125)]
        with self.assertRaises(upgrade_observation.ObservationError):
            manual.ensure(self.exclusion, self.selection, 'memory')
        self.manager.assert_not_called()

    def test_unowned_refused_or_ambiguous_process_does_not_offer_start(self):
        for status in ('externally_managed', 'refused', 'unknown', 'starting', 'backoff'):
            self.portable.return_value = {'status': status}
            with self.subTest(status=status), self.assertRaises(upgrade_observation.ObservationError):
                manual.ensure(self.exclusion, self.selection, 'memory')
        owner = {'pid': 123, 'generation': 'synthetic', 'child': {'pid': 124}}
        self.read.return_value = owner
        self.portable.return_value = {'status': 'stopped'}
        for states in (['alive'], ['unknown'], ['dead', 'alive'], ['dead', 'unknown']):
            self.state.side_effect = states
            with self.subTest(states=states), self.assertRaises(upgrade_observation.ObservationError):
                manual.ensure(self.exclusion, self.selection, 'memory')

    def test_retained_endpoint_or_lifetime_lock_does_not_offer_start(self):
        with patch.object(upgrade_observation.session_observation, 'endpoint_present', return_value=True):
            with self.assertRaises(upgrade_observation.ObservationError):
                manual.ensure(self.exclusion, self.selection, 'memory')
        with patch.object(upgrade_observation.session_observation, 'lock_held', return_value=True):
            with self.assertRaises(upgrade_observation.ObservationError):
                manual.ensure(self.exclusion, self.selection, 'memory')

    def test_stop_requires_reobserved_exit(self):
        with patch.object(manual, 'observe', side_effect=[{'running': True}, {'running': True}]), \
                patch.object(manual.memory_service, 'stop') as stop:
            with self.assertRaisesRegex(ValueError, 'did not stop'):
                manual.stop(self.selection, 'memory')
            stop.assert_called_once_with(self.selection)
