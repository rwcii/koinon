"""Frozen source replacement with real private backups and synthetic managers."""
from pathlib import Path
import unittest
from unittest.mock import patch

import test_upgrade_capture as fixtures
from koinon import upgrade_capture
from koinon.upgrade_documents import Documents
from koinon import upgrade_replace


class ReplacementTests(unittest.TestCase):
    def setUp(self):
        fixtures.CaptureTests.setUp(self)
        self.runtime_destination = self.operation / 'runtime-backup'
        self.runtime_destination.mkdir(mode=0o700)

    def owner(self):
        return fixtures.CaptureTests.owner(self)

    def prepare_backups(self, owner, guard):
        components = upgrade_capture.copy_components(guard, [self.destination])
        runtime = upgrade_capture.copy_runtime(guard, self.runtime_destination)
        digest = Documents(self.operation).put('backups', dict(version=1, runtime=runtime, components=components))
        owner.journal.advance(owner.journal.read(), evidence=digest)
        owner.journal.advance(owner.journal.read())

    def test_replace_and_repeat_preserve_backups_and_confirm_all_source_bytes(self):
        old = (self.prefix / 'entry.py').read_bytes()
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                result = upgrade_replace.replace(guard)
                self.assertEqual((self.prefix / 'entry.py').read_bytes(), (self.source / 'entry.py').read_bytes())
                self.assertEqual((self.runtime_destination / 'entry.py').read_bytes(), old)
                self.assertEqual(upgrade_replace.replace(guard), result)
                self.assertEqual(owner.journal.read()['step'], 8)

    def test_unchecked_hash_bytecode_cannot_execute_the_old_runtime(self):
        import importlib.util
        import py_compile
        import subprocess
        import sys
        cached = Path(importlib.util.cache_from_source(str(self.prefix / 'entry.py')))
        py_compile.compile(str(self.prefix / 'entry.py'),
                           invalidation_mode=py_compile.PycInvalidationMode.UNCHECKED_HASH)
        cached.parent.chmod(0o700)
        cached.chmod(0o600)
        untouched = cached.parent / 'unrelated.keep'
        untouched.write_text('retained unrelated file')
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                upgrade_replace.replace(guard)
                self.assertFalse(cached.exists())
        result = subprocess.run([sys.executable, '-I', '-c',
            'import sys; sys.path.insert(0, sys.argv[1]); import entry', str(self.prefix)],
            capture_output=True, text=True, check=True)
        self.assertEqual(result.stdout.strip(), 'new')
        self.assertEqual(untouched.read_text(), 'retained unrelated file')

    def test_symlinked_cache_refuses_before_replacing_runtime(self):
        import importlib.util
        cached = Path(importlib.util.cache_from_source(str(self.prefix / 'entry.py')))
        cached.parent.mkdir(mode=0o700)
        unrelated = self.root / 'unrelated-data'
        unrelated.write_text('do not remove')
        cached.symlink_to(unrelated)
        old = (self.prefix / 'entry.py').read_bytes()
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with self.assertRaisesRegex(ValueError, 'bytecode'):
                    upgrade_replace.replace(guard)
        self.assertEqual((self.prefix / 'entry.py').read_bytes(), old)
        self.assertTrue(cached.is_symlink())
        self.assertEqual(unrelated.read_text(), 'do not remove')

    def test_lost_completion_reconfirms_postimage_without_rewriting(self):
        publish = upgrade_replace.durable_state.publish
        def interrupt(path, value, **kwargs):
            if Path(path).name == 'replacement-progress.json' and value['index'] == 1:
                raise OSError('synthetic completion interruption')
            return publish(path, value, **kwargs)
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with patch.object(upgrade_replace.durable_state, 'publish', side_effect=interrupt):
                    with self.assertRaises(OSError):
                        upgrade_replace.replace(guard)
                inode = (self.prefix / 'entry.py').stat().st_ino
                upgrade_replace.replace(guard)
                self.assertEqual((self.prefix / 'entry.py').stat().st_ino, inode)

    def test_missing_backup_receipt_prevents_runtime_mutation(self):
        before = (self.prefix / 'entry.py').read_bytes()
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                owner.journal.advance(owner.journal.read(), evidence='a' * 64)
                owner.journal.advance(owner.journal.read())
                with self.assertRaises(ValueError):
                    upgrade_replace.replace(guard)
        self.assertEqual((self.prefix / 'entry.py').read_bytes(), before)

    def test_substituted_completed_file_is_never_repaired_implicitly(self):
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                upgrade_replace.replace(guard)
                (self.prefix / 'entry.py').write_text('operator changed this file')
                with self.assertRaises(ValueError):
                    upgrade_replace.replace(guard)
                self.assertEqual((self.prefix / 'entry.py').read_text(), 'operator changed this file')
