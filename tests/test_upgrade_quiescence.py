"""Synthetic manager observations for owned upgrade shutdown; no live jobs."""
import unittest
from unittest.mock import patch

import session_service_artifacts
import test_upgrade_gate as fixtures
import upgrade_exclusion
import upgrade_quiescence


class QuiescenceTests(unittest.TestCase):
    def setUp(self):
        fixtures.GateTests.setUp(self)

    def advance(self, step):
        fixtures.GateTests.advance(self, step)

    def operation_owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def test_absent_inactive_session_is_not_started_and_repeat_reobserves(self):
        self.advance(2)
        with self.operation_owner() as owner, \
                patch('platform_support.session_manager_observation', return_value=dict(status='absent')) as observe, \
                patch('platform_support.session_manager_action') as start, \
                patch('platform_support.session_manager_deactivate') as deactivate:
            result = upgrade_quiescence.stop_phase(owner, 'session')
            self.assertEqual(result['components'][0]['selection'], self.record)
            self.assertFalse(result['components'][0]['registered'])
            self.assertFalse(result['components'][0]['running'])
            count = observe.call_count
            self.assertEqual(upgrade_quiescence.stop_phase(owner, 'session'), result)
            self.assertGreater(observe.call_count, count)
            start.assert_not_called()
            deactivate.assert_not_called()
            self.assertEqual(owner.journal.read()['step'], 2)
        self.assertEqual(session_service_artifacts.load(self.home), self.record)

    def test_shutdown_outside_pending_phase_refuses_before_manager_action(self):
        with self.operation_owner() as owner, patch('platform_support.session_manager_observation') as observe:
            with self.assertRaises(upgrade_quiescence.QuiescenceError):
                upgrade_quiescence.stop_phase(owner, 'session')
            observe.assert_not_called()

    def test_memory_phase_requires_sessions_still_stopped(self):
        self.advance(4)
        with self.operation_owner() as owner, \
                patch('platform_support.session_manager_observation', return_value=dict(status='unknown')), \
                patch('memory_service.deactivate_owned') as stop:
            with self.assertRaises(ValueError):
                upgrade_quiescence.stop_phase(owner, 'memory')
            stop.assert_not_called()

    def test_changed_artifact_refuses_without_deactivation(self):
        self.advance(2)
        from pathlib import Path
        Path(self.record['artifact']).write_text('foreign replacement')
        with self.operation_owner() as owner, patch('platform_support.session_manager_deactivate') as deactivate:
            with self.assertRaises(ValueError):
                upgrade_quiescence.stop_phase(owner, 'session')
            deactivate.assert_not_called()
