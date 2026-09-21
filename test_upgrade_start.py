"""Synthetic native startup while the exact selected upgrade gate is active."""
import unittest
from unittest.mock import patch

import memory_service
import session_service
import session_service_manager
import test_upgrade_capture as capture
import test_upgrade_gate as gate
import upgrade_exclusion
import upgrade_quiescence
import upgrade_start


class SessionStartupTests(unittest.TestCase):
    def setUp(self):
        gate.GateTests.setUp(self)

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def ready(self):
        gate.GateTests.advance(self, 10)
        (self.prefix / 'entry.py').write_bytes((self.source / 'entry.py').read_bytes())

    def test_old_runtime_and_wrong_phase_refuse_before_native_action(self):
        with self.owner() as owner:
            selected = upgrade_quiescence.selection(owner, owner.loaded['documents']['components']['items'][0])
            with self.assertRaises(upgrade_start.StartupError):
                upgrade_start.validate(owner, selected, 'session')
            gate.GateTests.advance(self, 10)
            with patch('platform_support.session_manager_action') as action:
                with self.assertRaises(upgrade_start.StartupError):
                    session_service_manager.ensure(selected, upgrade=owner)
                action.assert_not_called()

    def test_only_explicit_coordinator_starts_selected_new_runtime(self):
        self.ready()
        with self.owner() as owner:
            selected = upgrade_quiescence.selection(owner, owner.loaded['documents']['components']['items'][0])
            with self.assertRaises(ValueError):
                session_service_manager.ensure(selected)
            with patch.object(session_service.Selection, 'validate_programs'), \
                    patch.object(session_service_manager, 'observation', side_effect=[
                        dict(status='absent'), dict(status='absent'),
                        dict(status='observed', pid=0), dict(status='observed', pid=0)]), \
                    patch.object(session_service_manager, 'status', return_value=dict(status='running')), \
                    patch('platform_support.session_manager_action') as action:
                self.assertEqual(session_service_manager.ensure(selected, upgrade=owner)['status'], 'running')
                self.assertEqual([call.args[1] for call in action.call_args_list], ['register', 'start'])
            self.assertEqual(owner.journal.read()['step'], 10)

    def test_runtime_substitution_after_registration_prevents_start(self):
        self.ready()
        with self.owner() as owner:
            selected = upgrade_quiescence.selection(owner, owner.loaded['documents']['components']['items'][0])
            def change_runtime(record, operation):
                (self.prefix / 'entry.py').write_text('unexpected replacement')
            with patch.object(session_service.Selection, 'validate_programs'), \
                    patch.object(session_service_manager, 'observation', side_effect=[
                        dict(status='absent'), dict(status='absent'),
                        dict(status='observed', pid=0), dict(status='observed', pid=0)]), \
                    patch('platform_support.session_manager_action', side_effect=change_runtime) as action:
                with self.assertRaises(upgrade_start.StartupError):
                    session_service_manager.ensure(selected, upgrade=owner)
                self.assertEqual([call.args[1] for call in action.call_args_list], ['register'])


class MemoryStartupTests(unittest.TestCase):
    def setUp(self):
        capture.MemoryCaptureTests.setUp(self)

    def owner(self):
        return capture.MemoryCaptureTests.owner(self)

    def test_owned_memory_start_uses_exact_upgrade_configuration(self):
        with self.owner() as owner:
            capture.MemoryCaptureTests.advance(self, owner, 10)
            selected = upgrade_quiescence.selection(owner, owner.loaded['documents']['components']['items'][0])
            with patch('platform_support.memory_manager_available', return_value=True), \
                    patch.object(memory_service, 'manager_observation', side_effect=[
                        dict(status='absent'), dict(status='absent'),
                        dict(status='observed', pid=0), dict(status='observed', pid=0),
                        dict(status='observed', pid=0), dict(status='observed', pid=0)]), \
                    patch.object(memory_service, 'managed_status', return_value=dict(status='running', running=True)), \
                    patch('platform_support.memory_manager_action') as action:
                self.assertTrue(memory_service.ensure_managed(selected, upgrade=owner)['running'])
                self.assertEqual([call.args[1] for call in action.call_args_list],
                                 ['register', 'activate', 'restart'])
            self.assertEqual(owner.journal.read()['step'], 10)
            with patch('platform_support.memory_manager_available', return_value=True), \
                    patch('platform_support.memory_manager_action') as action:
                with self.assertRaises(ValueError):
                    memory_service.ensure_managed(selected)
                action.assert_not_called()
