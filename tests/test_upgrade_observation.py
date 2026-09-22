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

    def test_manual_selection_uses_foreground_observation_without_native_manager(self):
        self.selection.backend = 'manual'
        with patch.object(observation.memory_service, 'manager_observation') as memory, \
             patch.object(observation.session_service_manager, 'observation') as session, \
             patch('upgrade_manual.observe', return_value={'manual': True}) as manual:
            for operation in (observation.memory,):
                self.assertEqual(operation(self.selection), {'manual': True})
            self.assertEqual(manual.call_count, 1)
            memory.assert_not_called()
            session.assert_not_called()


class InstallationObservationTests(unittest.TestCase):
    def setUp(self):
        import sys
        import durable_state
        import session_service_artifacts as artifacts
        import session_service_config as configuration
        import test_session_service_artifacts as fixtures
        fixtures.NativeSessionArtifactsTests.setUp(self)
        self.record = configuration.selection(self.prefix, sys.executable, self.home,
                                              self.config, self.registration, 'systemd')
        artifacts.publish(self.record)
        self.component = dict(kind='session', selection=self.record, registered=False,
                              running=False, manager=dict(status='absent'), owner=None)
        observing = patch.object(observation, 'session', return_value=self.component)
        self.observe = observing.start()
        self.addCleanup(observing.stop)
        self.publish = durable_state.publish

    def test_saved_session_is_enumerated_without_changing_registration(self):
        before = (self.home / 'native-service.json').read_bytes()
        result = observation.installation(self.prefix)
        self.assertEqual(result['installation'], self.config)
        self.assertEqual(result['components'], [self.component])
        self.assertFalse(result['components'][0]['running'])
        self.assertEqual((self.home / 'native-service.json').read_bytes(), before)
        self.assertFalse((self.prefix / '.upgrade').exists())
        self.observe.assert_called_once()

    def test_unknown_entry_and_capacity_refuse_before_service_observation(self):
        unexpected = self.home.parent / 'unexpected'
        unexpected.write_text('unclassified')
        with self.assertRaises(observation.ObservationError):
            observation.installation(self.prefix)
        unexpected.unlink()
        with patch('upgrade_plan.MAX_COMPONENTS', 0):
            with self.assertRaises(observation.ObservationError):
                observation.installation(self.prefix)
        self.observe.assert_not_called()

    def test_legacy_and_foreign_prefix_refuse_before_service_observation(self):
        path = self.home / 'native-service.json'
        path.unlink()
        with self.assertRaisesRegex(observation.ObservationError, 'explicit upgrade adapter'):
            observation.installation(self.prefix)
        self.publish(path, dict(self.record, prefix=str(self.root / 'other-prefix')))
        with self.assertRaisesRegex(observation.ObservationError, 'different runtime'):
            observation.installation(self.prefix)
        self.observe.assert_not_called()

    def test_new_entry_during_observation_is_not_omitted(self):
        def changed(selection):
            (self.home.parent / ('a' * 16)).mkdir(mode=0o700)
            return self.component
        self.observe.side_effect = changed
        with self.assertRaisesRegex(observation.ObservationError, 'changed'):
            observation.installation(self.prefix)

    def test_configuration_change_during_observation_refuses(self):
        def changed(selection):
            self.publish(self.prefix / 'install.json', dict(self.config, opaque='changed'))
            return self.component
        self.observe.side_effect = changed
        with self.assertRaisesRegex(observation.ObservationError, 'changed'):
            observation.installation(self.prefix)

    def test_invalid_manual_memory_selection_refuses_before_component_observation(self):
        from test_memory_service_config import inventory, record
        self.publish(self.prefix / 'install.json', dict(self.config,
                     memory_services=inventory(record(backend='manual'))))
        with self.assertRaisesRegex(observation.ObservationError, 'cannot verify saved memory'):
            observation.installation(self.prefix)
        self.observe.assert_not_called()

    def test_different_interpreter_has_explicit_refusal_before_observation(self):
        installed_python = self.record['python']
        with patch.object(observation.sys, 'executable', installed_python + '.different'):
            with self.assertRaisesRegex(observation.ObservationError, 'interpreter mismatch') as caught:
                observation.installation(self.prefix)
        self.assertIn(installed_python, str(caught.exception))
        self.observe.assert_not_called()

    def test_memory_verification_refusal_names_interpreter_without_false_diagnosis(self):
        from test_memory_service_config import inventory, record
        self.publish(self.prefix / 'install.json', dict(self.config,
                     memory_services=inventory(record())))
        cause = observation.memory_service.RunnerError('configuration_error')
        with patch.object(observation.memory_service, 'Selection', side_effect=cause):
            with self.assertRaisesRegex(observation.ObservationError, 'memory selection with interpreter') as caught:
                observation.installation(self.prefix)
        self.assertIs(caught.exception.__cause__, cause)
        self.assertIn(observation.sys.executable, str(caught.exception))
        self.assertIn('artifact/selection', str(caught.exception))
        self.assertNotIn('interpreter mismatch', str(caught.exception))
        self.observe.assert_not_called()

    def test_every_saved_native_memory_is_observed(self):
        from test_memory_service_config import inventory, record
        records = [record('/synthetic/a/.git'), record('/synthetic/b/.git')]
        self.publish(self.prefix / 'install.json', dict(self.config,
                     memory_services=inventory(*records)))
        def selected(prefix, repository):
            return SimpleNamespace(record=next(r for r in records if r['common_directory'] == repository))
        with patch.object(observation.memory_service, 'Selection', side_effect=selected), \
             patch.object(observation, 'memory', side_effect=lambda item: dict(kind='memory', selection=item.record)) as probe:
            result = observation.installation(self.prefix)
        self.assertEqual(probe.call_count, 2)
        self.assertEqual({item['selection']['common_directory'] for item in result['components']
                          if item['kind'] == 'memory'}, {r['common_directory'] for r in records})

    def test_absent_session_directory_is_empty_without_creating_it(self):
        import shutil
        shutil.rmtree(self.home.parent)
        result = observation.installation(self.prefix)
        self.assertEqual(result['components'], [])
        self.assertFalse(self.home.parent.exists())
        self.observe.assert_not_called()

    def test_missing_configuration_is_not_treated_as_empty_installation(self):
        (self.prefix / 'install.json').unlink()
        with self.assertRaisesRegex(observation.ObservationError, 'configuration'):
            observation.installation(self.prefix)
        self.observe.assert_not_called()


if __name__ == '__main__':
    unittest.main()
