import copy
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch

import platform_support
import session_endpoints
from session_supervisor_state import Records, StateError


class OwnedEndpointTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name).resolve()
        self.records = Records(self.home, 'a' * 64, 'b' * 64)
        self.socket_path = platform_support.control_socket_path(self.home)
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.bind(str(self.socket_path))
        self.socket_path.chmod(0o600)
        self.addCleanup(self.sock.close)
        self.owner = self.records.new_owner()
        self.owner.update(phase='failed', exit_status=75, primary_code='session_temporary_failure')
        self.owner['children']['bridge'] = dict(pid=12345, proc_start='synthetic-child', generation='c' * 32,
                                                control_endpoint=session_endpoints.capture(self.home))
        self.records.publish(self.owner)

    def test_only_captured_socket_of_dead_owned_process_is_removed(self):
        self.sock.close()
        with patch('session_supervisor_state.alive_state', return_value='dead'):
            session_endpoints.recover(self.records, self.owner)
        self.assertFalse(self.socket_path.exists())
        self.assertEqual(self.records.read(), self.owner)

    def test_alive_or_unknown_process_preserves_socket(self):
        for state in ('alive', 'unknown'):
            with self.subTest(state=state), patch('session_supervisor_state.alive_state', return_value=state):
                with self.assertRaises(StateError):
                    session_endpoints.recover(self.records, self.owner)
            self.assertTrue(self.socket_path.exists())

    def test_replacement_inode_is_preserved(self):
        # Keep the original open so the kernel cannot recycle its inode.
        self.socket_path.unlink()
        replacement = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.addCleanup(replacement.close)
        replacement.bind(str(self.socket_path))
        self.socket_path.chmod(0o600)
        before = self.socket_path.lstat().st_ino
        with patch('session_supervisor_state.alive_state', return_value='dead'):
            with self.assertRaises(StateError):
                session_endpoints.recover(self.records, self.owner)
        self.assertEqual(self.socket_path.lstat().st_ino, before)

    def test_missing_legacy_capture_refuses_without_overwriting_owner(self):
        del self.owner['children']['bridge']['control_endpoint']
        self.records.publish(self.owner)
        before = self.records.owner_path.read_bytes()
        with patch('session_supervisor_state.alive_state', return_value='dead'):
            with self.assertRaises(StateError):
                session_endpoints.recover(self.records, self.owner)
        self.assertEqual(self.records.owner_path.read_bytes(), before)
        self.assertTrue(self.socket_path.exists())

    def test_foreign_endpoint_path_cannot_enter_owned_record(self):
        self.owner['children']['bridge']['control_endpoint']['path'] = '/synthetic/foreign.sock'
        with self.assertRaises(StateError):
            self.records.publish(self.owner)

    def test_owner_replacement_refuses_before_cleanup(self):
        old = copy.deepcopy(self.owner)
        self.owner['generation'] = 'f' * 32
        self.records.publish(self.owner)
        with patch('session_supervisor_state.alive_state', return_value='dead'):
            with self.assertRaises(StateError):
                session_endpoints.recover(self.records, old)
        self.assertTrue(self.socket_path.exists())
