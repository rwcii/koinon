import os
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch

from koinon import durable_state
from koinon import platform_support
from koinon import session_endpoints
from koinon import upgrade_reservation as reservations


class ReservationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name).resolve()
        self.root = self.directory / 'state'
        self.root.mkdir(mode=0o700)
        self.plan = 'a' * 64

    def test_nonlistening_reservation_excludes_bind_and_releases_only_owned_path(self):
        with reservations.hold(self.directory, self.root, self.plan) as captured:
            self.assertEqual(session_endpoints.capture(self.root), captured)
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as competitor:
                with self.assertRaises(OSError):
                    competitor.bind(captured['path'])
                with self.assertRaises(ConnectionRefusedError):
                    competitor.connect(captured['path'])
        self.assertFalse(Path(captured['path']).exists())
        with reservations.hold(self.directory, self.root, self.plan):
            pass

    def test_recorded_reservation_recovers_only_after_actual_owner_exit(self):
        import subprocess
        import sys
        program = """import os, sys
from pathlib import Path
from koinon import upgrade_reservation
with upgrade_reservation.hold(Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]):
    os._exit(0)
"""
        subprocess.run([sys.executable, '-c', program, str(self.directory), str(self.root), self.plan],
                       check=True, timeout=10)
        previous = session_endpoints.capture(self.root)
        with reservations.hold(self.directory, self.root, self.plan) as current:
            self.assertEqual(current['path'], previous['path'])
            self.assertEqual(session_endpoints.capture(self.root), current)
        self.assertFalse(Path(previous['path']).exists())

    def test_foreign_endpoint_is_preserved(self):
        path = platform_support.control_socket_path(self.root)
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as foreign:
            foreign.bind(str(path))
            path.chmod(0o600)
            inode = path.stat().st_ino
            with self.assertRaises(reservations.ReservationError):
                with reservations.hold(self.directory, self.root, self.plan):
                    self.fail('adopted foreign endpoint')
            self.assertEqual(path.stat().st_ino, inode)

    def test_interrupted_unrecorded_bind_is_preserved(self):
        path = self.directory / ('reservation-' + reservations.upgrade_manifest.fingerprint(str(self.root))[:32] + '.json')
        durable_state.publish(path, dict(version=1, plan=self.plan, root=str(self.root), pid=os.getpid(),
                                        proc_start='synthetic-dead-owner', state='pending', endpoint=None))
        endpoint = platform_support.control_socket_path(self.root)
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as foreign:
            foreign.bind(str(endpoint))
            endpoint.chmod(0o600)
            with patch.object(platform_support, 'process_state', return_value='dead'):
                with self.assertRaisesRegex(reservations.ReservationError, 'no captured inode'):
                    with reservations.hold(self.directory, self.root, self.plan):
                        self.fail('removed ambiguous endpoint')
            self.assertTrue(endpoint.exists())
