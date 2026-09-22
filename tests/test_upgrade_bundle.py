import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import upgrade_bundle as bundle
import upgrade_manifest as manifest


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'source'
        self.source.mkdir(mode=0o700)
        self.recovery = self.root / 'recovery'
        self.recovery.mkdir(mode=0o700)
        (self.source / 'entry.py').write_text('import dependency\nprint(dependency.VALUE)\n')
        (self.source / 'dependency.py').write_text('VALUE = "frozen recovery"\n')
        (self.source / 'entry.py').chmod(0o600)
        (self.source / 'dependency.py').chmod(0o600)
        self.frozen = manifest.capture(self.source, ['entry.py', 'dependency.py'])

    def test_standalone_archive_survives_broken_prefix_and_ignores_pythonpath(self):
        descriptor = bundle.prepare(self.recovery, self.frozen, 'entry.py')
        path = bundle.verify(descriptor, self.frozen)
        prefix = self.root / 'installed'
        prefix.mkdir(mode=0o700)
        (prefix / 'dependency.py').write_text('raise RuntimeError("partial replacement")\n')
        result = subprocess.run([sys.executable, '-I', str(path)], cwd=prefix,
                                env=dict(os.environ, PYTHONPATH=str(prefix)),
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'frozen recovery\n')
        before = path.stat().st_ino
        self.assertEqual(bundle.prepare(self.recovery, self.frozen, 'entry.py'), descriptor)
        self.assertEqual(path.stat().st_ino, before)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_source_or_bundle_change_refuses_without_replacement(self):
        descriptor = bundle.prepare(self.recovery, self.frozen, 'entry.py')
        path = self.recovery / bundle.ARCHIVE
        original = path.read_bytes()
        (self.source / 'dependency.py').write_text('VALUE = "changed"\n')
        with self.assertRaises(manifest.ManifestError):
            bundle.verify(descriptor, self.frozen)
        self.assertEqual(path.read_bytes(), original)
        (self.source / 'dependency.py').write_text('VALUE = "frozen recovery"\n')
        path.write_bytes(b'corrupted archive')
        with self.assertRaises(manifest.ManifestError):
            bundle.verify(descriptor, self.frozen)
        with self.assertRaises(bundle.BundleError):
            bundle.prepare(self.recovery, self.frozen, 'entry.py')
        self.assertEqual(path.read_bytes(), b'corrupted archive')

    def test_failed_rename_flush_requires_successful_confirmation_on_retry(self):
        with patch.object(bundle.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                bundle.prepare(self.recovery, self.frozen, 'entry.py')
            path = self.recovery / bundle.ARCHIVE
            self.assertTrue(path.exists())
            before = path.stat().st_ino
            with self.assertRaises(OSError):
                bundle.prepare(self.recovery, self.frozen, 'entry.py')
        descriptor = bundle.prepare(self.recovery, self.frozen, 'entry.py')
        self.assertEqual(bundle.verify(descriptor, self.frozen).stat().st_ino, before)

    def test_unsafe_destination_is_preserved(self):
        outside = self.root / 'outside'
        outside.write_bytes(b'untouched')
        path = self.recovery / bundle.ARCHIVE
        path.symlink_to(outside)
        with self.assertRaises(bundle.BundleError):
            bundle.prepare(self.recovery, self.frozen, 'entry.py')
        self.assertTrue(path.is_symlink())
        self.assertEqual(outside.read_bytes(), b'untouched')

    def test_archive_capacity_refuses_before_publication(self):
        with patch.object(bundle, 'MAX_BYTES', 128):
            with self.assertRaises(bundle.BundleError):
                bundle.prepare(self.recovery, self.frozen, 'entry.py')
        self.assertFalse((self.recovery / bundle.ARCHIVE).exists())

    def test_missing_entrypoint_and_reserved_archive_name_refuse(self):
        with self.assertRaises(bundle.BundleError):
            bundle.prepare(self.recovery, self.frozen, 'missing.py')
        (self.source / '__main__.py').write_text('pass\n')
        (self.source / '__main__.py').chmod(0o600)
        frozen = manifest.capture(self.source, ['entry.py', '__main__.py'])
        with self.assertRaises(bundle.BundleError):
            bundle.prepare(self.recovery, frozen, 'entry.py')
        self.assertFalse((self.recovery / bundle.ARCHIVE).exists())

    def test_archive_confirmation_reopens_atomically_replaced_inode(self):
        alias = self.root / 'selected-recovery'
        alias.symlink_to(self.recovery, target_is_directory=True)
        descriptor = bundle.prepare(alias, self.frozen, 'entry.py')
        # Hook the frozen canonical path, not the caller's alias spelling.
        path = bundle.verify(descriptor, self.frozen)
        replacement = path.with_name('replacement')
        replacement.write_bytes(path.read_bytes())
        replacement.chmod(0o600)
        real_open = os.open
        detached = []
        def open_then_replace(selected, flags, *args, **kwargs):
            fd = real_open(selected, flags, *args, **kwargs)
            if Path(selected) == path and flags & os.O_RDWR and replacement.exists():
                os.replace(replacement, path)
                detached.append(os.fstat(fd).st_nlink)
            return fd
        with patch.object(bundle.durable_state.os, 'open', side_effect=open_then_replace):
            self.assertEqual(bundle.prepare(alias, self.frozen, 'entry.py'), descriptor)
        self.assertEqual(detached, [0])
        self.assertFalse(replacement.exists())


if __name__ == '__main__':
    unittest.main()
