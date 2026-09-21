"""Interrupted synthetic shutdown/backup/replacement through the phase driver."""
import unittest
from unittest.mock import patch

import test_upgrade_gate as fixtures
import upgrade_coordinator
from upgrade_documents import Documents
import upgrade_exclusion


class CoordinatorTests(unittest.TestCase):
    def setUp(self):
        fixtures.GateTests.setUp(self)
        fixtures.GateTests.advance(self, 2)
        for name in ('runtime-backup', 'component-backup-000'):
            (self.operation / name).mkdir(mode=0o700)

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def test_prepared_pipeline_stops_at_durable_replacement_with_gate_closed(self):
        old = (self.prefix / 'entry.py').read_bytes()
        with self.owner() as owner, patch('platform_support.session_manager_observation', return_value=dict(status='absent')):
            phase = upgrade_coordinator.through_replacement(owner)
            self.assertEqual(phase['step'], 9)
            self.assertEqual((self.prefix / 'entry.py').read_bytes(), (self.source / 'entry.py').read_bytes())
            self.assertEqual((self.operation / 'runtime-backup' / 'entry.py').read_bytes(), old)
            self.assertEqual(upgrade_coordinator.through_replacement(owner), phase)
            self.assertIsNotNone(upgrade_exclusion.read(self.prefix))

    def test_missing_prepared_destination_refuses_before_shutdown(self):
        (self.operation / 'runtime-backup').rmdir()
        with self.owner() as owner, patch('upgrade_quiescence.stop_phase') as stop:
            with self.assertRaises((ValueError, OSError)):
                upgrade_coordinator.through_replacement(owner)
            stop.assert_not_called()
            self.assertEqual(owner.journal.read()['step'], 2)

    def test_lost_backup_completion_resumes_retained_capture_before_replacing(self):
        put = Documents.put
        def interrupt(documents, name, value):
            digest = put(documents, name, value)
            if name == 'backups':
                raise OSError('synthetic lost backup completion')
            return digest
        with self.owner() as owner, patch('platform_support.session_manager_observation', return_value=dict(status='absent')):
            old = (self.prefix / 'entry.py').read_bytes()
            with patch.object(Documents, 'put', new=interrupt):
                with self.assertRaises(OSError):
                    upgrade_coordinator.through_replacement(owner)
            self.assertEqual(owner.journal.read()['step'], 6)
            self.assertEqual((self.prefix / 'entry.py').read_bytes(), old)
            self.assertEqual(upgrade_coordinator.through_replacement(owner)['step'], 9)

    def test_completed_replacement_does_not_hide_a_substituted_runtime(self):
        with self.owner() as owner, patch('platform_support.session_manager_observation', return_value=dict(status='absent')):
            upgrade_coordinator.through_replacement(owner)
            (self.prefix / 'entry.py').write_text('unexpected operator edit')
            with self.assertRaises(ValueError):
                upgrade_coordinator.through_replacement(owner)
            self.assertEqual((self.prefix / 'entry.py').read_text(), 'unexpected operator edit')
