import json
from pathlib import Path
import socket
import subprocess
import sys
import unittest
from unittest.mock import patch

from koinon.participant_lock import file_lock, OwnershipError
import test_upgrade_gate as fixtures
from koinon import upgrade_backup
from koinon import upgrade_capture
from koinon import upgrade_exclusion


class CaptureTests(unittest.TestCase):
    def setUp(self):
        fixtures.GateTests.setUp(self)
        fixtures.GateTests.advance(self, 6)
        self.destination = self.operation / 'component-backup'
        self.destination.mkdir(mode=0o700)

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def test_capture_holds_writer_exclusion_and_records_sidecar_absence(self):
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                with self.assertRaises(OwnershipError):
                    with file_lock(self.home / 'supervisor.lock', 'busy', None):
                        self.fail('writer lifetime lock was not held')
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as writer:
                    with self.assertRaises(OSError):
                        writer.bind(guard.endpoints[0][1]['path'])
                result = upgrade_capture.copy_components(guard, [self.destination])
                entry = result['components'][0]
                for database in ('inbox.sqlite3', 'notify-journal.sqlite3'):
                    for suffix in ('', '-wal', '-shm', '-journal'):
                        self.assertIsNone(entry['source']['files'][database + suffix])
                self.assertEqual(json.loads((self.destination / 'session.json').read_text())['thread'], 'synthetic')
                upgrade_backup.verify(entry['backup']['destination'])
            self.assertFalse(Path(guard.endpoints[0][1]['path']).exists())
            with upgrade_capture.hold(owner) as retry:
                self.assertEqual(upgrade_capture.copy_components(retry, [self.destination]), result)

    def test_runtime_backup_preserves_frozen_old_bytes_separately(self):
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                result = upgrade_capture.copy_runtime(guard, self.destination)
                self.assertEqual((self.destination / 'entry.py').read_bytes(), (self.prefix / 'entry.py').read_bytes())
                self.assertEqual(set(result['source']['files']), {'entry.py'})
                self.assertEqual(upgrade_capture.copy_runtime(guard, self.destination), result)

    def test_nested_runtime_backup_cannot_include_its_own_destination(self):
        retained = self.destination / 'retained.py'
        retained.write_text('synthetic retained backup')
        retained.chmod(0o600)
        snapshot = upgrade_backup.capture(self.prefix, [str(retained.relative_to(self.prefix))])
        with self.assertRaises(upgrade_backup.BackupError):
            upgrade_backup.copy(snapshot, self.destination, allow_nested_destination=True)
        self.assertEqual(retained.read_text(), 'synthetic retained backup')

    def test_changed_runtime_refuses_before_backup(self):
        (self.prefix / 'entry.py').write_text('changed old runtime')
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                with self.assertRaises(ValueError):
                    upgrade_capture.copy_runtime(guard, self.destination)
        self.assertEqual(list(self.destination.iterdir()), [])

    def test_unknown_state_socket_refuses_without_copying_partial_inventory(self):
        unknown = self.home / 'unknown.sock'
        # A relative bind avoids macOS's short sockaddr_un path limit even
        # when the test state lives beneath a long temporary-directory path.
        subprocess.run([sys.executable, '-c',
                        "import socket; s = socket.socket(socket.AF_UNIX); "
                        "s.bind('unknown.sock'); s.close()"],
                       cwd=self.home, check=True)
        unknown.chmod(0o600)
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                with self.assertRaises(upgrade_capture.CaptureError):
                    upgrade_capture.copy_components(guard, [self.destination])
        self.assertEqual(list(self.destination.iterdir()), [])

    def test_retry_cannot_omit_a_removed_business_file(self):
        retained = self.home / 'retained.json'
        retained.write_text('{"synthetic": "must be retained"}')
        retained.chmod(0o600)
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                upgrade_capture.copy_components(guard, [self.destination])
            retained.unlink()
            with upgrade_capture.hold(owner) as guard:
                with self.assertRaises(ValueError):
                    upgrade_capture.copy_components(guard, [self.destination])
        self.assertTrue((self.destination / 'retained.json').exists())

    def test_manager_reappearance_invalidates_guard(self):
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            with upgrade_capture.hold(owner) as guard:
                with patch('koinon.platform_support.session_manager_observation', return_value=dict(status='unknown')):
                    with self.assertRaises(ValueError):
                        guard.verify()


class MemoryCaptureTests(unittest.TestCase):
    def setUp(self):
        from koinon import durable_state
        import test_memory_service_artifacts as artifacts
        from koinon import upgrade_bundle
        from koinon import upgrade_manifest
        from koinon import upgrade_plan
        artifacts.MemoryArtifactTests.setUp(self)
        self.key, self.record, content = artifacts.MemoryArtifactTests.desired(self)
        self.config = dict(self.initial, memory_services=dict(version=1, repositories={self.key: self.record}))
        durable_state.publish(self.config_path, self.config)
        Path(self.record['artifact']).write_bytes(content)
        Path(self.record['artifact']).chmod(0o600)
        self.home = Path(self.record['service_directory'])
        for path in (self.root / 'state', self.root / 'state' / 'memory', self.home):
            path.mkdir(mode=0o700)
        self.source = self.root / 'source'
        self.source.mkdir(mode=0o700)
        for directory in (self.prefix, self.source):
            (directory / 'entry.py').write_text('print("synthetic")\n')
            (directory / 'entry.py').chmod(0o600)
        (self.prefix / '.upgrade').mkdir(mode=0o700)
        self.operation = self.prefix / '.upgrade' / ('a' * 32)
        self.operation.mkdir(mode=0o700)
        source = upgrade_manifest.capture(self.source, ['entry.py'])
        bundle = upgrade_bundle.prepare(self.operation, source, 'entry.py')
        component = dict(kind='memory', selection=self.record, manager=dict(status='absent'),
                         running=False, registered=False, owner=None)
        self.prepared = upgrade_plan.prepare(self.operation, self.prefix, source=source,
            runtime=upgrade_manifest.capture(self.prefix, ['entry.py']), installation=self.config,
            components=[component], recovery=bundle)
        self.destination = self.operation / 'memory-backup'
        self.destination.mkdir(mode=0o700)
        with self.owner() as owner:
            owner.activate()

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def advance(self, owner, step):
        while owner.journal.read()['step'] < step:
            current = owner.journal.read()
            owner.journal.advance(current, evidence='a' * 64 if current['step'] % 2 == 0 else None)

    def test_memory_shutdown_and_capture_hold_start_lock_and_preserve_store(self):
        import sqlite3
        from koinon import upgrade_quiescence
        with sqlite3.connect(self.home / 'memory.sqlite3') as database:
            database.execute('CREATE TABLE retained (value TEXT)')
            database.execute('INSERT INTO retained VALUES (?)', ('synthetic retained content',))
        database.close()
        before = (self.home / 'memory.sqlite3').read_bytes()
        with self.owner() as owner, patch('koinon.platform_support.systemd_service_observation', return_value=dict(status='absent')):
            self.advance(owner, 4)
            result = upgrade_quiescence.stop_phase(owner, 'memory')
            self.assertFalse(result['components'][0]['running'])
            self.advance(owner, 6)
            with upgrade_capture.hold(owner) as guard:
                with self.assertRaises(OwnershipError):
                    with file_lock(self.home / 'start.lock', 'busy', None):
                        self.fail('memory startup was not excluded')
                result = upgrade_capture.copy_components(guard, [self.destination])
                self.assertEqual((self.destination / 'memory.sqlite3').read_bytes(), before)
                self.assertIsNone(result['components'][0]['source']['files']['memory.sqlite3-wal'])
        self.assertEqual((self.home / 'memory.sqlite3').read_bytes(), before)

    def test_memory_deregistration_is_observed_and_repeat_has_no_manager_action(self):
        import memory_service
        from koinon import upgrade_quiescence
        observed = dict(status='observed', pid=0)
        def deregister(record):
            self.assertEqual(record, self.record)
            observed.clear()
            observed.update(status='absent')
        with self.owner() as owner, \
                patch.object(memory_service, 'manager_observation', side_effect=lambda selection: dict(observed)), \
                patch('koinon.platform_support.memory_manager_deregister', side_effect=deregister) as action:
            self.advance(owner, 4)
            result = upgrade_quiescence.stop_phase(owner, 'memory')
            self.assertFalse(result['components'][0]['registered'])
            self.assertFalse(result['components'][0]['running'])
            self.assertEqual(upgrade_quiescence.stop_phase(owner, 'memory'), result)
            action.assert_called_once_with(self.record)
            self.assertEqual(owner.journal.read()['step'], 4)
