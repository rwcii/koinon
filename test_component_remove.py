import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import component_install
import component_remove
import durable_state
import install_state
import memory_service


class ComponentRemoveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.repo, self.prefix, self.state = (self.root / name for name in ('repo', 'app', 'state'))
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.prefix.mkdir(mode=0o700)
        config = dict(state_root=str(self.state), unit_dir=str(self.root / 'units'), participants=[])
        durable_state.publish(self.prefix / 'install.json', config)
        self.key, self.record = component_install.memory_selection(self.prefix, self.repo, self.state, 'systemd', config)
        component_install.stage_memory(self.prefix, self.record)
        self.artifact = Path(self.record['artifact'])
        self.home = Path(self.record['service_directory'])
        self.home.mkdir(mode=0o700, parents=True)
        self.retained = self.home / 'retained-store-fixture'
        self.retained.write_bytes(b'preserved store and diagnostics')

    def test_unstarted_selected_memory_removal_preserves_store(self):
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='absent')), \
                install_state.locked(self.prefix) as installed:
            component_remove.remove_memory(self.prefix, installed, self.key)
            self.assertEqual(installed.config['memory_services']['repositories'], {})
        self.assertFalse(self.artifact.exists())
        self.assertEqual(self.retained.read_bytes(), b'preserved store and diagnostics')

    def test_interrupted_after_artifact_removal_resumes_from_removing_phase(self):
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='absent')), \
                install_state.locked(self.prefix) as installed:
            merge = installed.merge
            def crash_after_unlink(updates):
                if updates.get('memory_services', {}).get('repositories') == {}:
                    raise OSError('synthetic crash before completion')
                return merge(updates)
            with patch.object(installed, 'merge', side_effect=crash_after_unlink):
                with self.assertRaises(OSError):
                    component_remove.remove_memory(self.prefix, installed, self.key)
        self.assertFalse(self.artifact.exists())
        config = json.loads((self.prefix / 'install.json').read_text())
        self.assertEqual(config['memory_services']['repositories'][self.key]['state'], 'removing')
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='absent')), \
                install_state.locked(self.prefix) as installed:
            component_remove.remove_memory(self.prefix, installed, self.key)
            self.assertEqual(installed.config['memory_services']['repositories'], {})
        self.assertTrue(self.retained.exists())

    def test_unknown_manager_preserves_selection_and_artifact(self):
        before = self.artifact.read_bytes()
        with patch.object(memory_service, 'manager_observation', return_value=dict(status='unknown')), \
                install_state.locked(self.prefix) as installed:
            with self.assertRaises(memory_service.RunnerError):
                component_remove.remove_memory(self.prefix, installed, self.key)
            self.assertEqual(installed.config['memory_services']['repositories'][self.key]['state'], 'installed')
        self.assertEqual(self.artifact.read_bytes(), before)
        self.assertTrue(self.retained.exists())


class SessionComponentRemoveTests(unittest.TestCase):
    def setUp(self):
        import sys
        import test_session_service_artifacts as fixtures
        import session_service_config
        import session_service_artifacts
        fixtures.NativeSessionArtifactsTests.setUp(self)
        self.record = session_service_config.selection(self.prefix, sys.executable, self.home,
                                                       self.config, self.registration, 'systemd')
        session_service_artifacts.publish(self.record)
        self.artifact = Path(self.record['artifact'])
        self.retained = self.home / 'retained-inbox-fixture'
        self.retained.write_bytes(b'preserved inbox and notification history')

    def test_unstarted_session_removal_is_repeatable_and_preserves_data(self):
        import session_service_manager
        with patch.object(session_service_manager, 'observation', return_value=dict(status='absent')), \
                install_state.locked(self.prefix):
            component_remove.remove_session(self.prefix, self.home)
            component_remove.remove_session(self.prefix, self.home)
        self.assertFalse(self.artifact.exists())
        self.assertEqual(durable_state.read(self.home / 'native-service.json')['state'], 'removing')
        self.assertEqual(self.retained.read_bytes(), b'preserved inbox and notification history')

    def test_interrupted_session_artifact_unlink_retains_removal_provenance(self):
        import platform_support
        import session_service_manager
        sync = platform_support.sync_state_directory
        def interrupted(path):
            if Path(path) == self.artifact.parent:
                raise OSError('synthetic directory flush failure after unlink')
            return sync(path)
        with patch.object(session_service_manager, 'observation', return_value=dict(status='absent')), \
                install_state.locked(self.prefix):
            with patch.object(platform_support, 'sync_state_directory', side_effect=interrupted):
                with self.assertRaises(OSError):
                    component_remove.remove_session(self.prefix, self.home)
            self.assertFalse(self.artifact.exists())
            component_remove.remove_session(self.prefix, self.home)
        self.assertTrue(self.retained.exists())
