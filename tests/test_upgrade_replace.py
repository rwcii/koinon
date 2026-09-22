"""Frozen source replacement with real private backups and synthetic managers."""
from pathlib import Path
import unittest
from unittest.mock import patch

import test_upgrade_capture as fixtures
from koinon import upgrade_capture
from koinon.upgrade_documents import Documents
from koinon import durable_state
from koinon import upgrade_layout
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
                with self.assertRaisesRegex(ValueError, 'untrusted_cache_contents: unsafe Python bytecode'):
                    upgrade_replace.replace(guard)
        self.assertEqual((self.prefix / 'entry.py').read_bytes(), old)
        self.assertTrue(cached.is_symlink())
        self.assertEqual(unrelated.read_text(), 'do not remove')

    def untrusted_cache(self, extra=None):
        """A group-writable cache left by an operator command run under umask 002."""
        import importlib._bootstrap_external as external
        source = self.prefix / 'entry.py'
        cached = Path(external.cache_from_source(str(source)))
        cached.parent.mkdir(mode=0o700)
        info = source.stat()
        cached.write_bytes(bytes(external._code_to_timestamp_pyc(
            compile(source.read_text(), str(source), 'exec'), info.st_mtime, info.st_size)))
        cached.chmod(0o600)
        if extra is not None:
            (cached.parent / extra).write_text('operator data')
        cached.parent.chmod(0o775)
        identity = cached.parent.lstat()
        return cached.parent, (identity.st_dev, identity.st_ino)

    def test_untrusted_cache_is_quarantined_intact_and_never_chmodded(self):
        directory, identity = self.untrusted_cache()
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                result = upgrade_replace.replace(guard)
                moved = guard.exclusion.journal.directory / 'untrusted-cache' / '000'
        self.assertEqual(result['quarantined'], [str(directory)])
        self.assertFalse(directory.exists())
        info = moved.lstat()
        self.assertEqual((info.st_dev, info.st_ino), identity)
        self.assertEqual(info.st_mode & 0o777, 0o775)
        self.assertEqual(len(list(moved.iterdir())), 1)

    def test_quarantine_resumes_on_either_side_of_the_rename(self):
        for failure in ('rename', 'completion'):
            with self.subTest(failure=failure):
                self.setUp()
                directory, identity = self.untrusted_cache()
                rename, publish = upgrade_replace.os.rename, upgrade_replace.durable_state.publish
                def failing_rename(*args):
                    raise OSError('synthetic interruption before the rename')
                def failing_publish(path, value, **kwargs):
                    if Path(path).name == 'cache-quarantine.json' and value['completed'] == 1:
                        raise OSError('synthetic interruption after the rename')
                    return publish(path, value, **kwargs)
                interrupted = (patch.object(upgrade_replace.os, 'rename', side_effect=failing_rename)
                               if failure == 'rename' else
                               patch.object(upgrade_replace.durable_state, 'publish', side_effect=failing_publish))
                with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
                    with upgrade_capture.hold(owner) as guard:
                        self.prepare_backups(owner, guard)
                        with interrupted, self.assertRaises(OSError):
                            upgrade_replace.replace(guard)
                        self.assertEqual(directory.exists(), failure == 'rename')
                        result = upgrade_replace.replace(guard)
                        moved = guard.exclusion.journal.directory / 'untrusted-cache' / '000'
                self.assertEqual(result['quarantined'], [str(directory)])
                self.assertEqual((moved.lstat().st_dev, moved.lstat().st_ino), identity)
                self.assertFalse((moved.parent / '001').exists())

    def test_untrusted_cache_holding_other_data_refuses_before_publication(self):
        directory, _ = self.untrusted_cache(extra='notes.txt')
        old = (self.prefix / 'entry.py').read_bytes()
        with self.assertRaisesRegex(ValueError, 'untrusted_cache_contents: .*notes.txt'):
            upgrade_replace.untrusted_caches(self.prefix, ['entry.py'])
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with self.assertRaisesRegex(ValueError, 'untrusted_cache_contents'):
                    upgrade_replace.replace(guard)
        self.assertEqual((self.prefix / 'entry.py').read_bytes(), old)
        self.assertEqual((directory / 'notes.txt').read_text(), 'operator data')

    def test_preflight_reports_the_cache_it_will_quarantine(self):
        directory, _ = self.untrusted_cache()
        self.assertEqual(upgrade_replace.untrusted_caches(self.prefix, ['entry.py']), [str(directory)])

    def test_cache_selection_ignores_a_private_pycache_prefix(self):
        import sys
        directory, _ = self.untrusted_cache()
        with patch.object(sys, 'pycache_prefix', str(self.root / 'private-cache')):
            self.assertEqual(upgrade_replace.untrusted_caches(self.prefix, ['entry.py']), [str(directory)])

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


class RetirementTests(unittest.TestCase):
    """A cross-layout upgrade: the release publishes a path the runtime holds elsewhere."""
    legacy = (('legacy/old.py', 'koinon/legacy.py'),)

    def setUp(self):
        ReplacementTests.setUp(self)

    def owner(self):
        return ReplacementTests.owner(self)

    def prepare_backups(self, owner, guard):
        ReplacementTests.prepare_backups(self, owner, guard)

    def held(self, moves):
        return (self.owner(),
                patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')),
                patch.dict(upgrade_layout.MOVES, moves, clear=True))

    def test_declared_move_publishes_then_retires_and_keeps_the_old_bytes_in_backup(self):
        retired_bytes = (self.prefix / 'legacy/old.py').read_bytes()
        owner_context, observation, declaration = self.held({'legacy/old.py': 'koinon/legacy.py'})
        with owner_context as owner, observation, declaration:
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                result = upgrade_replace.replace(guard)
                self.assertEqual((self.prefix / 'koinon/legacy.py').read_bytes(),
                                 (self.source / 'koinon/legacy.py').read_bytes())
                self.assertFalse((self.prefix / 'legacy/old.py').exists())
                # The frozen backup still holds the retired path, so recovery stays possible.
                self.assertEqual((self.runtime_destination / 'legacy/old.py').read_bytes(), retired_bytes)
                self.assertEqual(result['retired'], ['legacy/old.py'])
                # Repeating the step must not reconstruct the retired path.
                self.assertEqual(upgrade_replace.replace(guard), result)
                self.assertFalse((self.prefix / 'legacy/old.py').exists())

    def test_undeclared_removal_refuses_before_publishing_or_retiring_anything(self):
        published = (self.prefix / 'entry.py').read_bytes()
        owner_context, observation, declaration = self.held({})
        with owner_context as owner, observation, declaration:
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with self.assertRaises(upgrade_replace.ReplacementError) as refusal:
                    upgrade_replace.replace(guard)
        self.assertIn('undeclared runtime file removal: legacy/old.py', str(refusal.exception))
        self.assertTrue((self.prefix / 'legacy/old.py').exists())
        self.assertEqual((self.prefix / 'entry.py').read_bytes(), published)

    def test_declared_move_without_a_published_destination_refuses(self):
        owner_context, observation, declaration = self.held({'legacy/old.py': 'koinon/absent.py'})
        with owner_context as owner, observation, declaration:
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with self.assertRaises(upgrade_replace.ReplacementError) as refusal:
                    upgrade_replace.replace(guard)
        self.assertIn('no published destination', str(refusal.exception))
        self.assertTrue((self.prefix / 'legacy/old.py').exists())

    def test_operator_change_to_a_retiring_path_refuses_instead_of_deleting_it(self):
        owner_context, observation, declaration = self.held({'legacy/old.py': 'koinon/legacy.py'})
        with owner_context as owner, observation, declaration:
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                (self.prefix / 'legacy/old.py').write_text('operator change\n')
                with self.assertRaises(upgrade_replace.ReplacementError) as refusal:
                    upgrade_replace.replace(guard)
        self.assertIn('retired runtime path changed before removal', str(refusal.exception))
        self.assertEqual((self.prefix / 'legacy/old.py').read_text(), 'operator change\n')

    def test_a_failed_directory_flush_leaves_the_retirement_uncheckpointed(self):
        """Unlink then a failed flush must not be recorded as a completed retirement.

        A checkpoint claims the path is gone durably. If the flush fails after the
        unlink, the resumed attempt sees the path already absent, so without
        re-establishing durability it would record a removal that never reached
        storage. The fault fires only once the path is gone, which is exactly the
        window between the unlink and its checkpoint.
        """
        real = upgrade_replace.platform_support.sync_state_directory
        faults, flushes = {'left': 1}, []

        def flush(path):
            retired_gone = not (self.prefix / 'legacy/old.py').exists()
            if Path(path) == self.prefix / 'legacy' and retired_gone:
                flushes.append(faults['left'])
                if faults['left']:
                    faults['left'] -= 1
                    raise OSError('injected directory flush failure')
            return real(path)

        owner_context, observation, declaration = self.held({'legacy/old.py': 'koinon/legacy.py'})
        progress = self.operation / 'replacement-progress.json'
        with owner_context as owner, observation, declaration:
            with upgrade_capture.hold(owner) as guard:
                self.prepare_backups(owner, guard)
                with patch.object(upgrade_replace.platform_support, 'sync_state_directory', flush):
                    with self.assertRaises(OSError):
                        upgrade_replace.replace(guard)
                    # The unlink happened; the flush did not. Nothing may be recorded.
                    self.assertFalse((self.prefix / 'legacy/old.py').exists())
                    self.assertEqual(faults['left'], 0)
                    self.assertEqual(durable_state.read(progress)['retired'], 0)
                    flushes.clear()
                    result = upgrade_replace.replace(guard)
                # The resumed attempt must flush the directory again, although the
                # path was already gone when it started.
                self.assertTrue(flushes)
                self.assertEqual(result['retired'], ['legacy/old.py'])
                self.assertFalse((self.prefix / 'legacy/old.py').exists())
