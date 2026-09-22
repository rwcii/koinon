"""Fault injection for gate 69; all guidance and recovery files are synthetic."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import memory
import notify
from koinon import participant_instructions as guidance
from koinon import platform_support


class GuidanceDurabilityTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.target = self.root / 'guidance.txt'
        self.target.write_text('original\n')
        self.backup = self.root / 'guidance.txt.before-codex-peer-bridge'

    def test_backup_flush_failure_prevents_overwrite_and_retry_reconfirms(self):
        with patch.object(platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                guidance.publish_guidance(self.target, 'original\n', 'replacement\n', 'codex')
        self.assertEqual(self.target.read_text(), 'original\n')
        self.assertEqual(self.backup.read_text(), 'original\n')
        # A visible backup from the failed attempt is not proof of durability.
        with patch.object(platform_support, 'sync_state_file', side_effect=OSError('retry flush')):
            with self.assertRaises(OSError):
                guidance.publish_guidance(self.target, 'original\n', 'replacement\n', 'codex')
        self.assertEqual(self.target.read_text(), 'original\n')
        guidance.publish_guidance(self.target, 'original\n', 'replacement\n', 'codex')
        self.assertEqual(self.target.read_text(), 'replacement\n')
        self.assertEqual(self.backup.read_text(), 'original\n')

    def test_failed_backup_file_flush_does_not_publish_partial_recovery(self):
        with patch.object(platform_support, 'sync_state_file', side_effect=OSError('file flush')):
            with self.assertRaises(OSError):
                guidance.publish_guidance(self.target, 'original\n', 'replacement\n', 'codex')
        self.assertFalse(self.backup.exists())
        self.assertEqual(self.target.read_text(), 'original\n')
        self.assertFalse(list(self.root.glob('*.tmp.*')))

    def test_symlink_backup_refused_without_overwrite(self):
        other = self.root / 'other'
        other.write_text('unrelated')
        self.backup.symlink_to(other)
        with self.assertRaises(OSError):
            guidance.publish_guidance(self.target, 'original\n', 'replacement\n', 'codex')
        self.assertEqual(self.target.read_text(), 'original\n')
        self.assertEqual(other.read_text(), 'unrelated')

    def test_identical_participant_update_reconfirms_published_guidance(self):
        guidance.update(self.root, Path('/synthetic/runtime'))
        with patch.object(platform_support, 'sync_state_directory', side_effect=OSError('retry flush')):
            with self.assertRaises(OSError):
                guidance.update(self.root, Path('/synthetic/runtime'))

    def test_registration_save_reports_directory_flush_failure(self):
        path = self.root / 'session.json'
        with patch.object(platform_support, 'sync_state_directory', side_effect=OSError('directory flush')):
            with self.assertRaises(OSError):
                notify.save(path, {'name': 'synthetic'})
        notify.save(path, {'name': 'synthetic'})
        self.assertIn('synthetic', path.read_text())

    def test_owner_publication_reports_directory_flush_failure(self):
        with patch.object(platform_support, 'sync_state_directory', side_effect=OSError('directory flush')):
            with self.assertRaises(OSError):
                memory.write_owner(self.root, self.root / 'socket', 'synthetic', 'repository')

    def test_standalone_flush_keeps_macos_device_sync_without_runtime_import(self):
        import fcntl
        with patch.object(platform_support, 'DARWIN', True):
            source = platform_support.standalone_state_sync_source()
        namespace = {}
        exec(source, namespace)
        with self.target.open('rb') as stream, \
                patch.object(fcntl, 'F_FULLFSYNC', 12345, create=True), \
                patch.object(fcntl, 'fcntl') as full_sync:
            namespace['sync_state_file'](stream.fileno())
            full_sync.assert_called_once_with(stream.fileno(), 12345)
