import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from koinon import durable_state as state
from koinon import platform_support


class DurableStateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / 'marker.json'

    def test_publication_is_private_and_preserves_complete_values(self):
        state.publish(self.path, dict(state='preparing', through=17))
        self.assertEqual(state.read(self.path), dict(state='preparing', through=17))
        state.publish(self.path, dict(state='ready', through=17))
        self.assertEqual(state.read(self.path), dict(state='ready', through=17))
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(list(self.path.parent.iterdir()), [self.path])

    def test_atomic_replacement_between_open_and_fstat_reopens_current_record(self):
        state.publish(self.path, dict(phase='starting'))
        replacement = self.path.with_name('replacement.json')
        state.publish(replacement, dict(phase='running'))
        real_open = os.open
        captured = []
        def open_then_replace(path, flags, *args, **kwargs):
            fd = real_open(path, flags, *args, **kwargs)
            if Path(path) == self.path and not captured:
                os.replace(replacement, self.path)
                captured.append(os.fstat(fd).st_nlink)
            return fd
        with mock.patch.object(state.os, 'open', side_effect=open_then_replace):
            self.assertEqual(state.read(self.path), dict(phase='running'))
        self.assertEqual(captured, [0])

    def test_replacement_never_accepts_a_new_nonprivate_record(self):
        state.publish(self.path, dict(phase='starting'))
        replacement = self.path.with_name('replacement.json')
        replacement.write_text('{"phase":"running"}')
        replacement.chmod(0o644)
        real_open = os.open
        def open_then_replace(path, flags, *args, **kwargs):
            fd = real_open(path, flags, *args, **kwargs)
            if replacement.exists():
                os.replace(replacement, self.path)
            return fd
        with mock.patch.object(state.os, 'open', side_effect=open_then_replace):
            with self.assertRaises(state.StateFileError):
                state.read(self.path)

    def test_concurrent_record_removal_reports_absence_and_closes_descriptor(self):
        state.publish(self.path, dict(refusal=True))
        real_open = os.open
        opened = []
        def open_then_remove(path, flags, *args, **kwargs):
            fd = real_open(path, flags, *args, **kwargs)
            self.path.unlink()
            opened.append(fd)
            return fd
        with mock.patch.object(state.os, 'open', side_effect=open_then_remove):
            self.assertIsNone(state.read(self.path))
        with self.assertRaises(OSError):
            os.fstat(opened[0])

    def test_repeated_replacement_is_bounded_and_does_not_accept_detached_content(self):
        state.publish(self.path, dict(phase='starting'))
        real_open = os.open
        opened = []
        def open_then_replace(path, flags, *args, **kwargs):
            fd = real_open(path, flags, *args, **kwargs)
            replacement = self.path.with_name('replacement.json')
            replacement.write_text('{"phase":"running"}')
            replacement.chmod(0o600)
            os.replace(replacement, self.path)
            opened.append(fd)
            return fd
        with mock.patch.object(state.os, 'open', side_effect=open_then_replace):
            with self.assertRaises(state.StateReadBusyError):
                state.read(self.path)
        self.assertEqual(len(opened), 3)
        for fd in opened:
            with self.assertRaises(OSError):
                os.fstat(fd)

    def test_publication_orders_file_replace_directory_and_final_flush(self):
        calls = []
        replace = os.replace
        def replacing(source, destination):
            calls.append('replace')
            replace(source, destination)
        with mock.patch.object(platform_support, 'sync_state_file', side_effect=lambda fd: calls.append('file')), \
             mock.patch.object(platform_support, 'sync_state_directory', side_effect=lambda path: calls.append('directory')), \
             mock.patch.object(state.os, 'replace', side_effect=replacing):
            state.publish(self.path, dict(state='ready'))
        self.assertEqual(calls, ['file', 'replace', 'directory', 'file'])

    def test_failed_directory_sync_is_not_reported_as_success(self):
        with mock.patch.object(platform_support, 'sync_state_directory', side_effect=OSError('synthetic')):
            with self.assertRaises(OSError):
                state.publish(self.path, dict(state='ready'))
        # Replacement happened, but its durability was not confirmed.
        self.assertEqual(state.read(self.path), dict(state='ready'))
        self.assertFalse(self.path.with_name(self.path.name + '.tmp').exists())

    def test_oversized_publication_leaves_prior_value_untouched(self):
        state.publish(self.path, dict(state='preparing'))
        with self.assertRaises(state.StateFileError):
            state.publish(self.path, dict(data='x' * state.MAX_BYTES))
        self.assertEqual(state.read(self.path), dict(state='preparing'))

    def test_stale_private_replacement_is_reused_without_file_growth(self):
        temp = self.path.with_name(self.path.name + '.tmp')
        temp.write_text('{"old":true}')
        temp.chmod(0o600)
        state.publish(self.path, dict(state='ready'))
        self.assertEqual(list(self.path.parent.iterdir()), [self.path])

    def test_symlink_and_nonprivate_files_are_refused(self):
        other = self.path.parent / 'other.json'
        other.write_text('{}')
        other.chmod(0o600)
        self.path.symlink_to(other)
        with self.assertRaises((OSError, state.StateFileError)):
            state.read(self.path)
        with self.assertRaises(state.StateFileError):
            state.publish(self.path, {})
        self.assertEqual(other.read_text(), '{}')
        self.path.unlink()
        self.path.write_text('{}')
        self.path.chmod(0o644)
        with self.assertRaises(state.StateFileError):
            state.read(self.path)

    def test_duplicate_fields_are_not_silently_adopted(self):
        self.path.write_text('{"state":"preparing","state":"ready"}')
        self.path.chmod(0o600)
        with self.assertRaises(state.StateFileError):
            state.read(self.path)

    def test_macos_stronger_flush_is_required_without_platform_gating(self):
        import fcntl
        with mock.patch.object(platform_support, 'DARWIN', True), \
             mock.patch.object(platform_support.os, 'fsync') as fsync, \
             mock.patch.object(fcntl, 'F_FULLFSYNC', 51, create=True), \
             mock.patch.object(fcntl, 'fcntl') as flush:
            platform_support.sync_state_file(7)
            fsync.assert_called_once_with(7)
            flush.assert_called_once_with(7, 51)
            flush.side_effect = OSError('synthetic unsupported flush')
            with self.assertRaises(OSError):
                platform_support.sync_state_file(7)

    def test_explicit_document_limit_does_not_expand_default_service_records(self):
        value = dict(manifest='x' * 8192)
        state.publish(self.path, value, max_bytes=16384)
        self.assertEqual(state.read(self.path, max_bytes=16384), value)
        with self.assertRaises(state.StateFileError):
            state.read(self.path)
        before = self.path.read_bytes()
        with self.assertRaises(state.StateFileError):
            state.publish(self.path, dict(replacement=True))
        self.assertEqual(self.path.read_bytes(), before)
        self.assertEqual(state.MAX_BYTES, 4096)

    def test_explicit_document_overflow_preserves_prior_evidence(self):
        state.publish(self.path, dict(phase='prepared'), max_bytes=8192)
        before = self.path.read_bytes()
        with self.assertRaises(state.StateFileError):
            state.publish(self.path, dict(manifest='x' * 8192), max_bytes=8192)
        self.assertEqual(self.path.read_bytes(), before)

    def test_document_limit_is_bounded_and_cannot_relax_privacy(self):
        for limit in (True, 0, -1, state.MAX_DOCUMENT_BYTES + 1):
            with self.subTest(limit=limit), self.assertRaises(ValueError):
                state.publish(self.path, {}, max_bytes=limit)
        self.assertFalse(self.path.exists())
        self.path.write_text('{}')
        self.path.chmod(0o644)
        with self.assertRaises(state.StateFileError):
            state.read(self.path, max_bytes=state.MAX_DOCUMENT_BYTES)

    def test_confirm_repeats_all_flushes_after_ambiguous_publication(self):
        value = dict(phase='released')
        with mock.patch.object(platform_support, 'sync_state_directory', side_effect=OSError('flush failed')):
            with self.assertRaises(OSError):
                state.publish(self.path, value)
            self.assertEqual(state.read(self.path), value)
            with self.assertRaises(OSError):
                state.confirm(self.path, value)
        before = self.path.stat().st_ino
        calls = []
        with mock.patch.object(platform_support, 'sync_state_file', side_effect=lambda fd: calls.append('file')), \
             mock.patch.object(platform_support, 'sync_state_directory', side_effect=lambda path: calls.append('directory')):
            self.assertEqual(state.confirm(self.path, value), value)
        self.assertEqual(calls, ['file', 'directory', 'file'])
        self.assertEqual(self.path.stat().st_ino, before)

    def test_confirm_refuses_missing_changed_and_nonprivate_records(self):
        with self.assertRaises(state.StateFileError):
            state.confirm(self.path, {})
        self.assertFalse(self.path.exists())
        state.publish(self.path, dict(step=1))
        with self.assertRaises(state.StateFileError):
            state.confirm(self.path, dict(step=True))
        self.path.chmod(0o644)
        with self.assertRaises(state.StateFileError):
            state.confirm(self.path, dict(step=1))
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o644)

    def test_confirm_reopens_replaced_inode_before_flushing(self):
        state.publish(self.path, dict(step=0))
        replacement = self.path.with_name('replacement.json')
        state.publish(replacement, dict(step=1))
        real_open = os.open
        def open_then_replace(path, flags, *args, **kwargs):
            fd = real_open(path, flags, *args, **kwargs)
            if Path(path) == self.path and replacement.exists():
                os.replace(replacement, self.path)
            return fd
        with mock.patch.object(state.os, 'open', side_effect=open_then_replace):
            self.assertEqual(state.confirm(self.path, dict(step=1)), dict(step=1))

    def test_confirm_refuses_replacement_during_flush(self):
        state.publish(self.path, dict(step=0))
        replacement = self.path.with_name('replacement.json')
        state.publish(replacement, dict(step=1))
        def replace_during_flush(fd):
            if replacement.exists():
                os.replace(replacement, self.path)
        with mock.patch.object(platform_support, 'sync_state_file', side_effect=replace_during_flush):
            with self.assertRaises(state.StateFileError):
                state.confirm(self.path, dict(step=0))
        self.assertEqual(state.read(self.path), dict(step=1))
