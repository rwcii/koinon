import importlib.util
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from repo_root import ROOT

with patch.object(sys, 'path', [str(ROOT / 'scripts'), *sys.path]):
    spec = importlib.util.spec_from_file_location('preflight_uninstall', ROOT / 'scripts/uninstall.py')
    uninstall = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(uninstall)


class UninstallPreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix, self.state, self.units = (self.root / name for name in ('app', 'state', 'units'))
        for path in (self.prefix, self.state, self.units):
            path.mkdir(mode=0o700)
        self.runtime = self.prefix / 'bridge.py'
        self.runtime.write_text('retained runtime')
        self.config = dict(state_root=str(self.state), unit_dir=str(self.units), participants=[],
                           work_items=dict(rules={'synthetic': {}}))
        self.session = self.state / 'sessions' / ('a' * 16)
        self.session.mkdir(parents=True)

    def assert_retained(self, path):
        saved = path.read_bytes()
        with patch.object(uninstall.work_guidance, 'remove_locked') as guidance, \
                patch.object(uninstall.subprocess, 'run') as run, \
                patch.object(uninstall.platform_support, 'user_service_manager') as manager:
            with self.assertRaisesRegex(ValueError, str(path)):
                uninstall.uninstall(self.prefix, SimpleNamespace(config=self.config))
            guidance.assert_not_called()
            run.assert_not_called()
            manager.assert_not_called()
        self.assertEqual(self.runtime.read_text(), 'retained runtime')
        self.assertEqual(path.read_bytes(), saved)

    def test_native_session_even_without_registration_retains_runtime_and_guidance(self):
        native = self.session / 'native-service.json'
        native.write_text('{"retained": "even incomplete native evidence must prevent legacy removal"}')
        self.assert_retained(native)

    def test_memory_selection_retains_runtime_even_when_no_manager_is_running(self):
        artifact = self.units / 'synthetic-memory.service'
        artifact.write_text('retained owned artifact')
        self.config['memory_services'] = dict(repositories={'synthetic': dict(artifact=str(artifact))})
        self.assert_retained(artifact)

    def test_invalid_legacy_registration_refuses_before_guidance_mutation(self):
        registration = self.session / 'session.json'
        registration.write_text(json.dumps(dict(thread=123)))
        self.assert_retained(registration)

    def test_damaged_legacy_unit_refuses_before_guidance_mutation(self):
        artifact = self.units / 'koinon-bridge.service'
        artifact.write_text('[Service]\nExecStart="/python" "' + str(self.runtime) + '"\n')
        self.assert_retained(artifact)
