from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import durable_state
import platform_support
import session_service_artifacts as artifacts
import session_service_config as configuration


class NativeSessionArtifactsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        self.config = dict(state_root=str(self.root / 'state'), codex='/synthetic/codex', unit_dir=str(self.root / 'units'))
        self.registration = dict(thread='synthetic-session', name='synthetic-peer', repo=str(self.root))
        key, _, _ = configuration.registration_identity(self.registration)
        self.home = self.root / 'state' / 'sessions' / key
        for path in (self.root / 'state', self.root / 'state' / 'sessions', self.home,
                     self.home / 'native-service'):
            path.mkdir(mode=0o700)
        durable_state.publish(self.prefix / 'install.json', self.config)
        durable_state.publish(self.home / 'session.json', self.registration)
        self.record = configuration.selection(self.prefix, '/synthetic/python', self.home,
                                               self.config, self.registration, 'systemd')
        self.path = Path(self.record['artifact'])

    def test_publication_is_inert_repeat_preserves_artifact_inode(self):
        with patch.object(platform_support.subprocess, 'run') as manager:
            artifacts.publish(self.record)
            inode = self.path.stat().st_ino
            artifacts.publish(self.record)
            artifacts.verify_owned(self.record)
            self.assertEqual(inode, self.path.stat().st_ino)
            manager.assert_not_called()
        self.assertEqual(artifacts.load(self.home), self.record)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)

    def test_unregistered_identical_artifact_is_never_adopted(self):
        self.path.write_bytes(artifacts.expected(self.record))
        self.path.chmod(0o600)
        with self.assertRaises(ValueError):
            artifacts.publish(self.record)
        self.assertIsNone(artifacts.load(self.home))

    def test_crash_before_artifact_write_resumes_only_saved_intent(self):
        with patch.object(artifacts.files, '_replace', side_effect=OSError('synthetic crash')):
            with self.assertRaises(OSError):
                artifacts.publish(self.record)
        self.assertEqual(artifacts.load(self.home)['state'], 'pending')
        self.assertFalse(self.path.exists())
        artifacts.publish(self.record)
        artifacts.verify_owned(self.record)

    def test_crash_after_artifact_rename_resumes_without_replacing_it(self):
        publish = durable_state.publish
        def fail_completion(path, value):
            if value.get('state') == 'installed':
                raise OSError('synthetic completion failure')
            return publish(path, value)
        with patch.object(durable_state, 'publish', side_effect=fail_completion):
            with self.assertRaises(OSError):
                artifacts.publish(self.record)
        self.assertEqual(artifacts.load(self.home)['state'], 'pending')
        inode = self.path.stat().st_ino
        artifacts.publish(self.record)
        self.assertEqual(self.path.stat().st_ino, inode)
        artifacts.verify_owned(self.record)

    def test_unknown_pending_bytes_are_preserved_for_reconciliation(self):
        pending = dict(self.record, state='pending', before_digest=None,
                       after_digest=self.record['artifact_digest'])
        durable_state.publish(self.home / 'native-service.json', pending)
        self.path.write_bytes(b'foreign')
        self.path.chmod(0o600)
        with self.assertRaises(ValueError):
            artifacts.publish(self.record)
        self.assertEqual(self.path.read_bytes(), b'foreign')
        self.assertEqual(artifacts.load(self.home), pending)

    def test_changed_registration_and_other_prefix_refuse_exact_repeat(self):
        artifacts.publish(self.record)
        original = self.path.read_bytes()
        durable_state.publish(self.home / 'session.json', dict(self.registration, name='changed'))
        with self.assertRaises(ValueError):
            artifacts.publish(self.record)
        self.assertEqual(self.path.read_bytes(), original)
        durable_state.publish(self.home / 'session.json', self.registration)
        other = self.root / 'other'
        other.mkdir(mode=0o700)
        durable_state.publish(other / 'install.json', self.config)
        desired = configuration.selection(other, '/synthetic/python', self.home,
                                           self.config, self.registration, 'systemd')
        with self.assertRaises(ValueError):
            artifacts.publish(desired)
        self.assertEqual(artifacts.load(self.home), self.record)

    def test_loaded_path_accepts_only_single_owned_literal_link(self):
        artifacts.publish(self.record)
        loader = self.root / 'loader'
        loader.mkdir(mode=0o700)
        link = loader / self.path.name
        link.symlink_to(self.path)
        artifacts.verify_loaded(self.record, link)
        link.unlink()
        link.write_bytes(self.path.read_bytes())
        link.chmod(0o600)
        with self.assertRaises(ValueError) as caught:
            artifacts.verify_loaded(self.record, link)
        self.assertIn(str(link), caught.exception.paths)
        link.unlink()
        intermediate = self.root / 'alias'
        intermediate.symlink_to(self.path)
        link.symlink_to(intermediate)
        with self.assertRaises(ValueError) as caught:
            artifacts.verify_loaded(self.record, link)
        self.assertIn(str(link), caught.exception.paths)

    def test_unsafe_directory_or_legacy_lock_preserved_and_refused(self):
        self.home.chmod(0o770)
        with self.assertRaises(ValueError):
            artifacts.publish(self.record)
        self.assertEqual(self.home.stat().st_mode & 0o777, 0o770)
        self.home.chmod(0o700)
        lock = self.home / 'supervisor.lock'
        lock.touch(mode=0o600)
        lock.chmod(0o664)
        with self.assertRaisesRegex(ValueError, 'unsafe_lock_file'):
            artifacts.publish(self.record)
        self.assertEqual(lock.stat().st_mode & 0o777, 0o664)
        self.assertIsNone(artifacts.load(self.home))

    def test_registration_rechecked_under_locks_before_intent(self):
        original = artifacts.prepare
        count = 0
        def mutate(desired):
            nonlocal count
            count += 1
            if count == 2:
                durable_state.publish(self.home / 'session.json', dict(self.registration, name='changed'))
            return original(desired)
        with patch.object(artifacts, 'prepare', side_effect=mutate):
            with self.assertRaises(ValueError):
                artifacts.publish(self.record)
        self.assertIsNone(artifacts.load(self.home))
        self.assertFalse(self.path.exists())
