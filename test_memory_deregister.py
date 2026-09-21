from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import platform_support


class MemoryDeregisterTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.artifact = self.root / 'artifacts' / 'synthetic.service'
        self.artifact.parent.mkdir(mode=0o700)
        self.artifact.write_text('retained artifact')
        self.paths = (self.root / 'units' / self.artifact.name,
                      self.root / 'units' / 'default.target.wants' / self.artifact.name)
        self.paths[0].parent.mkdir(mode=0o700)
        self.paths[1].parent.mkdir(mode=0o700)
        for path in self.paths:
            path.symlink_to(self.artifact)
        self.record = dict(backend='systemd', artifact=str(self.artifact))

    def test_exact_links_removed_and_missing_links_allow_retry(self):
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support, 'memory_registration_paths', return_value=self.paths), \
                patch.object(platform_support.subprocess, 'run') as manager:
            platform_support.memory_manager_deregister(self.record)
            platform_support.memory_manager_deregister(self.record)
        self.assertTrue(self.artifact.exists())
        self.assertTrue(all(not path.is_symlink() for path in self.paths))
        self.assertEqual(manager.call_count, 2)
        self.assertEqual(manager.call_args.args[0][-1], 'daemon-reload')

    def test_conflicting_enablement_preserves_every_link_and_artifact(self):
        self.paths[1].unlink()
        self.paths[1].symlink_to(self.root / 'other.service')
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support, 'memory_registration_paths', return_value=self.paths), \
                patch.object(platform_support.subprocess, 'run') as manager:
            with self.assertRaises(ValueError):
                platform_support.memory_manager_deregister(self.record)
            manager.assert_not_called()
        self.assertTrue(all(path.is_symlink() for path in self.paths))
        self.assertEqual(self.artifact.read_text(), 'retained artifact')
