from pathlib import Path
import unittest
from unittest.mock import patch

import durable_state
import platform_support
import session_install
import session_service_artifacts as artifacts
import test_session_service_artifacts as artifact_tests


class SessionInstallTests(unittest.TestCase):
    def setUp(self):
        artifact_tests.NativeSessionArtifactsTests.setUp(self)
        self.config['session_backend'] = 'systemd'
        durable_state.publish(self.prefix / 'install.json', self.config)

    def register(self, state, state_root, thread, repo, **kwargs):
        durable_state.publish(state / 'session.json', self.registration)
        return self.registration

    def stage(self, register=None):
        return session_install.stage(self.prefix, self.config, self.home,
                                     self.registration['thread'], self.registration['repo'],
                                     'codex', None, register or self.register)

    def test_new_registration_and_repeat_publish_without_manager_calls(self):
        (self.home / 'session.json').unlink()
        with patch.object(platform_support.subprocess, 'run') as manager:
            record = self.stage()
            inode = Path(record['artifact']).stat().st_ino
            with patch.object(self, 'register', side_effect=AssertionError('repeat registered again')):
                self.assertEqual(self.stage(), record)
            manager.assert_not_called()
        self.assertEqual(Path(record['artifact']).stat().st_ino, inode)
        artifacts.verify_owned(record)

    def test_legacy_registration_is_preserved_without_adoption(self):
        before = (self.home / 'session.json').read_bytes()
        with self.assertRaisesRegex(ValueError, 'explicit upgrade'):
            self.stage()
        self.assertEqual((self.home / 'session.json').read_bytes(), before)
        self.assertFalse((self.home / 'native-service.json').exists())
        self.assertFalse((self.prefix / '.install.lock').exists())

    def test_registration_crash_resumes_only_own_durable_intent(self):
        (self.home / 'session.json').unlink()
        def interrupted(*args, **kwargs):
            self.register(*args, **kwargs)
            raise OSError('synthetic crash after registration')
        with self.assertRaises(OSError):
            self.stage(interrupted)
        self.assertTrue((self.home / 'native-install-intent.json').exists())
        self.assertFalse((self.home / 'native-service.json').exists())
        record = self.stage()
        artifacts.verify_owned(record)

    def test_completed_removal_allows_reinstall_with_retained_registration(self):
        (self.home / 'session.json').unlink()
        record = self.stage()
        before = (self.home / 'session.json').read_bytes()
        inbox = self.home / 'synthetic-retained-inbox'
        inbox.write_text('retained')
        removing = dict(record, state='removing', before_digest=record['artifact_digest'], after_digest=None)
        durable_state.publish(self.home / 'native-service.json', removing)
        Path(record['artifact']).unlink()
        artifacts.archive_removed(removing)
        self.assertFalse((self.home / 'native-service.json').exists())
        with patch.object(self, 'register', side_effect=AssertionError('reinstall replaced registration')):
            repeated = self.stage()
        self.assertEqual(repeated, record)
        self.assertEqual((self.home / 'session.json').read_bytes(), before)
        self.assertEqual(inbox.read_text(), 'retained')
        artifacts.verify_owned(repeated)

    def test_exact_repeat_does_not_take_running_supervisors_lifetime_lock(self):
        (self.home / 'session.json').unlink()
        record = self.stage()
        with artifacts.locked(self.home / 'supervisor.lock'):
            self.assertEqual(self.stage(), record)
