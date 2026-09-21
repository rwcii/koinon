"""Synthetic installation exclusion; no real managers or installed configuration."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import install_state
import runtime_names
import upgrade_bundle
import upgrade_exclusion as exclusion
import upgrade_manifest as manifest
import upgrade_plan


class ExclusionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix, self.source = self.root / 'installed', self.root / 'source'
        for path in (self.prefix, self.source, self.prefix / '.upgrade'):
            path.mkdir(mode=0o700)
        self.directory = self.prefix / '.upgrade' / ('a' * 32)
        self.directory.mkdir(mode=0o700)
        for path in (self.prefix, self.source):
            (path / 'entry.py').write_text('print("synthetic")\n')
            (path / 'entry.py').chmod(0o600)
        self.original = dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'), opaque={'keep': [1]})
        self.config = self.prefix / 'install.json'
        self.config.write_text(json.dumps(self.original))
        self.config.chmod(0o600)
        source = manifest.capture(self.source, ['entry.py'])
        runtime = manifest.capture(self.prefix, ['entry.py'])
        recovery = upgrade_bundle.prepare(self.directory, source, 'entry.py')
        self.plan = upgrade_plan.prepare(self.directory, self.prefix, source=source,
                                        runtime=runtime, installation=self.original,
                                        components=[], recovery=recovery)

    def operation(self):
        return exclusion.operation(self.directory, self.plan['sha256'])

    def test_marker_refuses_ordinary_readers_and_preserves_configuration(self):
        with self.operation() as operation:
            operation.activate()
            operation.verify()
        with self.assertRaises(runtime_names.NameConflict) as raised:
            runtime_names.install_config(self.prefix)
        self.assertEqual(raised.exception.code, 'installation_upgrading')
        self.assertIn(str(self.directory), str(raised.exception))
        with self.assertRaises(runtime_names.NameConflict):
            with install_state.locked(self.prefix):
                self.fail('ordinary installation entered while upgrading')
        loaded = exclusion.read(self.prefix)
        self.assertEqual(loaded['documents']['installation'], self.original)
        self.assertEqual(loaded['sha256'], self.plan['sha256'])

    def test_memory_selection_preserves_upgrade_diagnostic(self):
        import memory_service
        with self.operation() as operation:
            operation.activate()
        with self.assertRaises(memory_service.RunnerError) as raised:
            memory_service.Selection(self.prefix, self.root)
        self.assertEqual(raised.exception.code, 'installation_upgrading')
        self.assertEqual(raised.exception.exit_status, 78)
        self.assertIn(str(self.directory), raised.exception.paths)

    def test_session_cli_preserves_upgrade_diagnostic(self):
        import contextlib
        import io
        import session_service
        with self.operation() as operation:
            operation.activate()
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            status = session_service.main(['ensure', '--prefix', str(self.prefix),
                                           '--state-dir', str(self.root / 'session'),
                                           '--backend', 'systemd'])
        self.assertEqual(status, 78)
        result = json.loads(output.getvalue())
        self.assertEqual(result['code'], 'installation_upgrading')
        self.assertIn('resume', result['recovery'])

    def test_ambiguous_marker_publication_requires_successful_retry_flush(self):
        sync = install_state.platform_support.sync_state_directory
        def fail_installation(path):
            if path == self.prefix:
                raise OSError('flush')
            return sync(path)
        with self.operation() as operation:
            with patch.object(install_state.platform_support, 'sync_state_directory', side_effect=fail_installation):
                with self.assertRaises(OSError):
                    operation.activate()
            self.assertEqual(json.loads(self.config.read_text())['installation_state'], 'upgrading')
            with patch.object(install_state.platform_support, 'sync_state_directory', side_effect=fail_installation):
                with self.assertRaises(OSError):
                    operation.activate()
            operation.activate()
            self.assertEqual(operation.verify()['step'], 0)

    def test_missing_marker_after_preparation_cannot_be_recreated(self):
        with self.operation() as operation:
            operation.activate()
            operation.journal.advance(operation.journal.read(), evidence='a' * 64)
            self.config.write_text(json.dumps(self.original))
            with self.assertRaises(exclusion.ExclusionError):
                operation.activate()
        self.assertEqual(json.loads(self.config.read_text()), self.original)

    def test_changed_original_refuses_without_overwriting_operator_data(self):
        changed = dict(self.original, opaque={'changed': True})
        self.config.write_text(json.dumps(changed))
        with self.operation() as operation:
            with self.assertRaises(runtime_names.NameConflict):
                operation.activate()
        self.assertEqual(json.loads(self.config.read_text()), changed)

    def test_marker_cannot_select_another_prefix(self):
        with self.operation() as operation:
            operation.activate()
        other = self.root / 'other'
        other.mkdir(mode=0o700)
        (other / 'install.json').write_bytes(self.config.read_bytes())
        (other / 'install.json').chmod(0o600)
        with self.assertRaises(exclusion.ExclusionError):
            exclusion.read(other)

    def test_ordinary_admission_restored_only_after_final_readiness_record(self):
        with self.operation() as operation:
            operation.activate()
            with self.assertRaises(exclusion.ExclusionError):
                operation.finish()
            while operation.journal.read()['step'] < 19:
                current = operation.journal.read()
                operation.journal.advance(current, evidence='b' * 64 if current['step'] % 2 == 0 else None)
            operation.finish()
            operation.finish()
        self.assertEqual(runtime_names.install_config(self.prefix), self.original)
        self.assertIsNone(exclusion.read(self.prefix))

    def test_observation_and_marker_share_one_retained_installation_lock(self):
        import upgrade_observation
        from participant_lock import file_lock, OwnershipError
        Path(self.original['state_root']).mkdir(mode=0o700)
        with install_state.locked(self.prefix) as installed:
            with patch.object(install_state, 'locked', side_effect=AssertionError('nested installation lock')):
                observed = upgrade_observation.installation_locked(self.prefix, installed)
                self.assertEqual(observed['components'], [])
                with self.operation() as operation:
                    operation.activate_locked(installed)
                with self.assertRaises(OwnershipError):
                    with file_lock(self.prefix / '.install.lock', 'busy', None, timeout=0):
                        self.fail('preflight released the registration exclusion lock')
        self.assertEqual(exclusion.read(self.prefix)['sha256'], self.plan['sha256'])
