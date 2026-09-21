import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import uninstall_finalize as removal


class FinalizeRemovalTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.prefix = Path(self.temp.name).resolve()
        self.names = ('first.py', 'second.py')
        for name in (*self.names, 'install.json', '.install.lock', 'retained-store'):
            path = self.prefix / name
            path.write_text('retained' if name == 'retained-store' else '{}')
            path.chmod(0o600)
        removal.prepare(self.prefix, self.names)

    def test_standalone_recovery_finishes_after_partial_runtime_deletion(self):
        unlink = Path.unlink
        def interrupted(path, *args, **kwargs):
            if path.name == 'second.py':
                raise OSError('synthetic interruption')
            return unlink(path, *args, **kwargs)
        with patch.object(Path, 'unlink', interrupted):
            with self.assertRaises(OSError):
                removal.finish(self.prefix, locked=True, names=self.names)
        self.assertFalse((self.prefix / 'first.py').exists())
        self.assertTrue((self.prefix / 'second.py').exists())
        result = subprocess.run([sys.executable, '-I', str(self.prefix / removal.RECOVERY),
                                 '--prefix', str(self.prefix)], cwd=self.prefix, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.prefix / 'second.py').exists())
        self.assertFalse((self.prefix / 'install.json').exists())
        self.assertEqual((self.prefix / 'retained-store').read_text(), 'retained')
        self.assertTrue((self.prefix / '.install.lock').exists())

    def test_changed_runtime_refuses_before_deleting_any_file(self):
        (self.prefix / 'second.py').write_text('operator change')
        with self.assertRaises(ValueError):
            removal.finish(self.prefix, locked=True, names=self.names)
        self.assertTrue((self.prefix / 'first.py').exists())
        self.assertTrue((self.prefix / 'install.json').exists())

    def test_manifest_cannot_expand_the_generated_runtime_allowlist(self):
        path = self.prefix / removal.MANIFEST
        value = json.loads(path.read_text())
        value['files']['retained-store'] = removal.digest(b'retained')
        path.write_text(json.dumps(value))
        result = subprocess.run([sys.executable, '-I', str(self.prefix / removal.RECOVERY),
                                 '--prefix', str(self.prefix)], cwd=self.prefix, capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.prefix / 'first.py').exists())
        self.assertEqual((self.prefix / 'retained-store').read_text(), 'retained')
