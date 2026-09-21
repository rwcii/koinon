from contextlib import ExitStack
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from participant_lock import file_lock
import upgrade_observation as observation


class ObservationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.selection = SimpleNamespace(backend='systemd', home=self.home,
                                         record=dict(identity='synthetic'),
                                         records=SimpleNamespace(read=Mock(return_value=None)),
                                         owner_path=self.home / 'supervisor.json')

    def session_context(self, manager, portable):
        stack = ExitStack()
        stack.enter_context(patch.object(observation.session_service_manager, 'observation', return_value=manager))
        stack.enter_context(patch.object(observation.session_service, 'status', return_value=portable))
        return stack

    def test_inactive_registration_and_running_state_remain_distinct(self):
        for manager in (dict(status='absent'), dict(status='observed', pid=0)):
            with self.session_context(manager, dict(status='unobserved', owner=None)):
                result = observation.session(self.selection)
            self.assertFalse(result['running'])
            self.assertEqual(result['registered'], manager['status'] == 'observed')
        self.assertEqual(list(self.home.iterdir()), [])

    def test_running_requires_the_same_owned_manager_process(self):
        owner = dict(pid=123, generation='synthetic')
        self.selection.records.read.return_value = owner
        for pid in (123, 124, 0):
            with self.session_context(dict(status='observed', pid=pid), dict(status='running', owner=owner)):
                if pid == 123:
                    result = observation.session(self.selection)
                    self.assertTrue(result['running'])
                    owner['generation'] = 'changed afterwards'
                    self.assertEqual(result['owner']['generation'], 'synthetic')
                else:
                    with self.assertRaises(observation.ObservationError):
                        observation.session(self.selection)

    def test_unknown_or_changing_manager_is_not_stopped(self):
        with self.session_context(dict(status='unknown'), dict(status='unobserved', owner=None)):
            with self.assertRaises(observation.ObservationError):
                observation.session(self.selection)
        with self.session_context({}, dict(status='unobserved', owner=None)):
            with patch.object(observation.session_service_manager, 'observation',
                              side_effect=[dict(status='observed', pid=0), dict(status='absent')]):
                with self.assertRaises(observation.ObservationError):
                    observation.session(self.selection)

    def test_changed_owner_refuses_even_when_both_manager_reads_match(self):
        self.selection.records.read.return_value = dict(pid=123)
        with self.session_context(dict(status='absent'), dict(status='unobserved', owner=None)):
            with self.assertRaises(observation.ObservationError):
                observation.session(self.selection)

    def test_inactive_owner_lock_or_orphan_endpoint_refuses(self):
        with self.session_context(dict(status='absent'), dict(status='unobserved', owner=None)):
            with file_lock(self.home / 'supervisor.lock', 'busy', None):
                with self.assertRaises(observation.ObservationError):
                    observation.session(self.selection)
            with patch.object(observation.session_observation, 'endpoint_present', return_value=True):
                with self.assertRaises(observation.ObservationError):
                    observation.session(self.selection)

    def test_stopped_memory_supervisor_does_not_hide_live_child(self):
        owner = dict(pid=123, child=dict(pid=124))
        with patch.object(observation.memory_service, 'manager_observation', return_value=dict(status='observed', pid=0)), \
             patch.object(observation.memory_service, 'read_record', return_value=owner), \
             patch.object(observation.memory_service, 'observation', return_value=dict(status='stopped')), \
             patch.object(observation.memory_service, 'process_state', side_effect=['dead', 'alive']):
            with self.assertRaises(observation.ObservationError):
                observation.memory(self.selection)

    def test_manual_selection_refuses_before_any_manager_observation(self):
        self.selection.backend = 'manual'
        with patch.object(observation.memory_service, 'manager_observation') as memory, \
             patch.object(observation.session_service_manager, 'observation') as session:
            for operation in (observation.session, observation.memory):
                with self.assertRaises(observation.ObservationError):
                    operation(self.selection)
            memory.assert_not_called()
            session.assert_not_called()


if __name__ == '__main__':
    unittest.main()
