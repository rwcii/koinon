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
