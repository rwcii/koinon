"""Native fixture cleanup must preserve an unconfirmed live job."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import memory_service
from koinon import platform_support
from repo_root import ROOT

spec = importlib.util.spec_from_file_location('native_memory_fixture',
    ROOT / 'scripts/test-native-memory.py')
fixture_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture_module)


class NativeFixtureCleanupTests(unittest.TestCase):
    def test_failed_guarded_stop_never_disables_or_deletes(self):
        fixture = fixture_module.Fixture.__new__(fixture_module.Fixture)
        fixture.selection = SimpleNamespace(owner_path=Path('/synthetic/owner'))
        fixture.owner_dead = Mock(return_value=False)
        fixture.stop = Mock(side_effect=memory_service.RunnerError('shutdown_unconfirmed'))
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='observed')), \
                patch.object(memory_service, 'read_record', return_value={'synthetic': True}), \
                patch.object(platform_support, 'memory_manager_action') as action:
            with self.assertRaises(memory_service.RunnerError) as caught:
                fixture.remove()
            self.assertEqual(caught.exception.code, 'shutdown_unconfirmed')
            fixture.stop.assert_called_once_with()
            action.assert_not_called()

    def test_unknown_manager_never_attempts_stop_or_disable(self):
        fixture = fixture_module.Fixture.__new__(fixture_module.Fixture)
        fixture.selection = object()
        fixture.stop = Mock()
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='unknown')), \
                patch.object(platform_support, 'memory_manager_action') as action:
            with self.assertRaisesRegex(RuntimeError, 'ownership unknown'):
                fixture.remove()
            fixture.stop.assert_not_called()
            action.assert_not_called()
