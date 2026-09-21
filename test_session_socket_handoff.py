from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

import platform_support
import session_endpoints
import session_socket_handoff as handoff
import session_supervisor
from session_supervisor_state import Records, StateError


class SocketHandoffTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name).resolve()
        self.records = Records(self.home, 'a' * 64, 'b' * 64)

    def test_bound_parent_descriptor_is_explicitly_assigned_and_consumed(self):
        endpoint, captured = handoff.bind(self.home)
        self.addCleanup(endpoint.close)
        owner = self.records.new_owner()
        owner['control_endpoints']['bridge'] = captured
        owner['spawn_pending'] = 'bridge'
        self.records.publish(owner)
        script = ('import os,sys\nfrom session_socket_handoff import take\n'
                  'fd=int(sys.argv[1])\nsock=take(fd,sys.argv[2],"bridge",sys.argv[3])\n'
                  'assert sock.getsockname()==sys.argv[4]\n'
                  'try: os.fstat(fd)\nexcept OSError: pass\nelse: raise AssertionError("original descriptor leaked")\n'
                  'sock.close()\n')
        result = subprocess.run([sys.executable, '-c', script, str(endpoint.fileno()), str(self.home),
                                 owner['generation'], captured['path']], pass_fds=(endpoint.fileno(),),
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_legacy_owner_read_preserves_version_and_published_evidence(self):
        owner = self.records.new_owner()
        owner['version'] = 1
        del owner['control_endpoints'], owner['endpoint_pending']
        self.records.publish(owner)
        before = self.records.owner_path.read_bytes()
        identity = self.records.owner_path.stat()
        self.assertEqual(self.records.read(), owner)
        self.assertEqual(self.records.owner_path.read_bytes(), before)
        after = self.records.owner_path.stat()
        self.assertEqual((after.st_ino, after.st_mtime_ns), (identity.st_ino, identity.st_mtime_ns))

    def test_wrong_parent_or_generation_never_falls_back_to_bind(self):
        endpoint, captured = handoff.bind(self.home)
        self.addCleanup(endpoint.close)
        owner = self.records.new_owner()
        owner['control_endpoints']['bridge'] = captured
        owner['spawn_pending'] = 'bridge'
        self.records.publish(owner)
        inode = Path(captured['path']).lstat().st_ino
        # This process is the owner, not its child, so the parent-PID check refuses.
        with self.assertRaises(ValueError):
            handoff.take(endpoint.fileno(), self.home, 'bridge', owner['generation'])
        self.assertEqual(Path(captured['path']).lstat().st_ino, inode)
        self.assertGreaterEqual(endpoint.fileno(), 0)

    def test_exclusive_parent_bind_preserves_operator_socket(self):
        endpoint, captured = handoff.bind(self.home)
        self.addCleanup(endpoint.close)
        with self.assertRaises(OSError):
            handoff.bind(self.home)
        self.assertEqual(session_endpoints.capture(self.home), captured)

    def test_parent_intent_failure_prevents_bind_and_spawn(self):
        runner = session_supervisor.Runner(self.records, dict(bridge=['synthetic'], notifier=['synthetic']), threading.Event())
        publish = self.records.publish
        def fail_endpoint_intent(value, **kwargs):
            if value.get('endpoint_pending') is not None:
                raise OSError('synthetic intent failure')
            return publish(value, **kwargs)
        with patch.object(self.records, 'publish', side_effect=fail_endpoint_intent), \
                patch.object(handoff, 'bind') as bind, patch.object(subprocess, 'Popen') as spawn:
            with self.assertRaises(OSError):
                runner.attempt()
        bind.assert_not_called()
        spawn.assert_not_called()

    def test_unrecorded_parent_bind_intent_is_preserved_and_blocks_retry(self):
        owner = self.records.new_owner()
        owner.update(phase='failed', exit_status=78, endpoint_pending='bridge', primary_code='session_configuration_failure')
        self.records.publish(owner)
        with patch('session_supervisor_state.alive_state', return_value='dead'):
            with self.assertRaises(StateError):
                self.records.retry()
            with self.assertRaises(StateError):
                session_endpoints.recover(self.records, owner)
        self.assertEqual(self.records.read(), owner)

    def test_immediate_child_exit_before_handoff_cleans_only_parent_socket(self):
        commands = dict(bridge=[sys.executable, '-c', 'raise SystemExit(75)'], notifier=['unused'])
        for _ in range(2):
            # Repeated attempts happen in this test process, so isolate each owner
            # directory as a native manager would isolate a fresh wrapper lifetime.
            with tempfile.TemporaryDirectory() as raw:
                root = Path(raw).resolve()
                records = Records(root, 'a' * 64, 'b' * 64)
                self.assertEqual(session_supervisor.run(records, commands, 'manual'), 75)
                failed = records.read()
                self.assertIsNotNone(failed['control_endpoints']['bridge'])
                self.assertFalse(Path(failed['control_endpoints']['bridge']['path']).exists())
                self.assertIsNone(failed['endpoint_pending'])
                self.assertIsNone(failed['spawn_pending'])

    def test_invalid_bridge_descriptor_refuses_without_database_or_socket(self):
        result = subprocess.run([sys.executable, str(Path(__file__).parent / 'bridge.py'),
                                 '--state-dir', str(self.home), 'serve', '--supervisor-control-fd', '99',
                                 '--supervisor-generation', 'a' * 32], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 78, result.stderr)
        self.assertFalse((self.home / 'inbox.sqlite3').exists())
        self.assertFalse(platform_support.control_socket_path(self.home).exists())
