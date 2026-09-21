import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import component_install


class ComponentInstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.repo, self.prefix, self.state = (self.root / name for name in ('repo', 'app', 'state'))
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        (self.bin / 'git').symlink_to(shutil.which('git'))
        self.env = dict(os.environ, PATH=str(self.bin))

    def install(self, backend='systemd', *extra):
        return subprocess.run([sys.executable, str(Path(__file__).parent / 'scripts/install.py'),
                               '--configure-memory', '--repo', str(self.repo), '--prefix', str(self.prefix),
                               '--state-dir', str(self.state), '--service-backend', backend, '--no-start', *extra],
                              env=self.env, capture_output=True, text=True, timeout=20)

    def test_memory_only_no_start_and_repeat_need_neither_participant_nor_manager(self):
        first = self.install()
        self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
        config = json.loads((self.prefix / 'install.json').read_text())
        self.assertEqual(config['participants'], [])
        self.assertIsNone(config['codex'])
        record = next(iter(config['memory_services']['repositories'].values()))
        artifact = Path(record['artifact'])
        before, inode = artifact.read_bytes(), artifact.stat().st_ino
        self.assertFalse(self.state.exists())
        repeated = self.install()
        self.assertEqual(repeated.returncode, 0, repeated.stdout + repeated.stderr)
        self.assertEqual(artifact.read_bytes(), before)
        self.assertEqual(artifact.stat().st_ino, inode)
        self.assertFalse(self.state.exists())

    def test_manual_memory_selection_has_no_managed_artifact(self):
        result = self.install('manual')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        config = json.loads((self.prefix / 'install.json').read_text())
        record = next(iter(config['memory_services']['repositories'].values()))
        self.assertIsNone(record['artifact'])
        self.assertFalse((self.prefix / 'service-artifacts').exists())

    def test_repository_and_saved_backend_changes_refuse_before_runtime_write(self):
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        runtime = self.prefix / 'bridge.py'
        before = runtime.stat().st_mtime_ns
        refused = self.install('manual')
        self.assertNotEqual(refused.returncode, 0)
        self.assertEqual(runtime.stat().st_mtime_ns, before)

    def test_invalid_repository_refuses_before_creating_prefix(self):
        with self.assertRaises(ValueError):
            component_install.memory_selection(self.prefix, self.root / 'missing', self.state, 'manual', {})
        self.assertFalse(self.prefix.exists())

    def test_explicit_thread_and_memory_stage_native_session_without_global_guidance(self):
        result = self.install('systemd', '--thread', 'synthetic-installer-thread', '--codex', sys.executable)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        config = json.loads((self.prefix / 'install.json').read_text())
        self.assertEqual(config['participants'], [])
        self.assertEqual(config['session_backend'], 'systemd')
        selections = list((self.state / 'sessions').glob('*/native-service.json'))
        self.assertEqual(len(selections), 1)
        selection = json.loads(selections[0].read_text())
        self.assertEqual(selection['state'], 'installed')
        self.assertTrue(Path(selection['artifact']).is_file())
        self.assertFalse((selections[0].parent / 'supervisor-owner.json').exists())

    def test_fresh_explicit_repository_thread_selects_both_components(self):
        command = [sys.executable, str(Path(__file__).parent / 'scripts/install.py'),
                   '--repo', str(self.repo), '--thread', 'synthetic-fresh-thread', '--codex', sys.executable,
                   '--prefix', str(self.prefix), '--state-dir', str(self.state),
                   '--service-backend', 'systemd', '--no-start']
        result = subprocess.run(command, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        config = json.loads((self.prefix / 'install.json').read_text())
        self.assertEqual(len(config['memory_services']['repositories']), 1)
        self.assertEqual(len(list((self.state / 'sessions').glob('*/native-service.json'))), 1)

    def test_existing_guidance_install_does_not_silently_add_memory(self):
        base = [sys.executable, str(Path(__file__).parent / 'scripts/install.py'),
                '--configure-codex', '--codex', sys.executable, '--codex-home', str(self.root / 'codex'),
                '--prefix', str(self.prefix), '--state-dir', str(self.state), '--no-start']
        first = subprocess.run(base, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
        repeated = subprocess.run(base + ['--repo', str(self.repo)], env=self.env,
                                  capture_output=True, text=True, timeout=20)
        self.assertEqual(repeated.returncode, 0, repeated.stdout + repeated.stderr)
        self.assertIn('Existing installation scope retained', repeated.stdout)
        config = json.loads((self.prefix / 'install.json').read_text())
        self.assertNotIn('memory_services', config)
        selected = self.install()
        self.assertEqual(selected.returncode, 0, selected.stdout + selected.stderr)

    def test_missing_prefix_ancestors_are_private_under_group_writable_umask(self):
        self.prefix = self.root / 'new-parent' / 'nested' / 'app'
        previous = os.umask(0o002)
        try:
            result = self.install()
        finally:
            os.umask(previous)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for directory in (self.prefix, self.prefix.parent, self.prefix.parent.parent):
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)

    def test_interrupted_fresh_install_retains_component_intent_on_retry(self):
        from scripts import install as installer
        from unittest.mock import patch
        command = ['scripts/install.py', '--configure-codex', '--repo', str(self.repo),
                   '--codex', sys.executable, '--codex-home', str(self.root / 'codex'),
                   '--prefix', str(self.prefix), '--state-dir', str(self.state),
                   '--service-backend', 'systemd', '--no-start']
        previous = os.umask(0o077)
        try:
            with patch.object(sys, 'argv', command), \
                    patch.object(component_install, 'copy_runtime', side_effect=OSError('synthetic copy interruption')):
                with self.assertRaisesRegex(OSError, 'synthetic copy interruption'):
                    installer.main()
        finally:
            os.umask(previous)
        config = json.loads((self.prefix / 'install.json').read_text())
        record = next(iter(config['memory_services']['repositories'].values()))
        self.assertEqual(record['state'], 'pending')
        self.assertFalse(Path(record['artifact']).exists())
        resumed = subprocess.run([sys.executable, *command], capture_output=True, text=True, timeout=20)
        self.assertEqual(resumed.returncode, 0, resumed.stdout + resumed.stderr)
        config = json.loads((self.prefix / 'install.json').read_text())
        record = next(iter(config['memory_services']['repositories'].values()))
        self.assertEqual(record['state'], 'installed')
        self.assertTrue(Path(record['artifact']).is_file())

    def test_runtime_publication_preserves_complete_files_and_exact_repeats(self):
        from unittest.mock import patch
        source, destination = self.root / 'source.py', self.root / 'runtime.py'
        source.write_bytes(b'new complete module')
        destination.write_bytes(b'old complete module')
        with patch.object(component_install.os, 'replace', side_effect=OSError('synthetic publication interruption')):
            with self.assertRaises(OSError):
                component_install.copy_runtime(source, destination)
        self.assertEqual(destination.read_bytes(), b'old complete module')
        component_install.copy_runtime(source, destination)
        inode = destination.stat().st_ino
        component_install.copy_runtime(source, destination)
        self.assertEqual(destination.read_bytes(), source.read_bytes())
        self.assertEqual(destination.stat().st_ino, inode)
