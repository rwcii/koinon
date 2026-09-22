from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from koinon import durable_state
from koinon import upgrade_documents as documents


class DocumentsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.documents = documents.Documents(self.root)

    def test_concurrent_reader_waits_for_short_metadata_publication(self):
        from concurrent.futures import ThreadPoolExecutor
        from koinon.participant_lock import file_lock
        import threading
        import time
        value = dict(version=1)
        digest = self.documents.put('source', value)
        entered = threading.Event()
        def reader():
            entered.set()
            return self.documents.read('source', digest)
        with ThreadPoolExecutor(max_workers=1) as executor:
            with file_lock(self.root / 'documents.lock', 'synthetic_busy', None):
                future = executor.submit(reader)
                self.assertTrue(entered.wait(1))
                time.sleep(.1)
                self.assertFalse(future.done(), 'brief contention must not kill a gate reader')
            self.assertEqual(future.result(timeout=2), value)

    def test_large_manifest_roundtrip_is_immutable_and_private(self):
        value = dict(files={f'module_{i}.py': dict(sha256=f'{i:064x}', bytes=i)
                            for i in range(128)})
        digest = self.documents.put('source', value)
        path = self.documents.path('source')
        self.assertGreater(path.stat().st_size, durable_state.MAX_BYTES)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.documents.read('source', digest), value)
        before = path.stat().st_ino
        self.assertEqual(self.documents.put('source', value), digest)
        self.assertEqual(path.stat().st_ino, before)
        with self.assertRaises(documents.DocumentError):
            self.documents.put('source', dict(files={}))
        self.assertEqual(self.documents.read('source', digest), value)

    def test_lost_reply_does_not_overwrite_committed_document(self):
        publish = durable_state.publish

        def commit_then_fail(*args, **kwargs):
            publish(*args, **kwargs)
            raise OSError('lost completion')

        value = dict(source='synthetic')
        with patch.object(durable_state, 'publish', side_effect=commit_then_fail):
            with self.assertRaises(OSError):
                self.documents.put('source', value)
        before = self.documents.path('source').stat().st_ino
        digest = self.documents.put('source', value)
        self.assertEqual(self.documents.read('source', digest), value)
        self.assertEqual(self.documents.path('source').stat().st_ino, before)

    def test_missing_or_substituted_document_refuses_without_recreation(self):
        with self.assertRaises(documents.DocumentError):
            self.documents.read('source', 'a' * 64)
        self.assertFalse(self.documents.path('source').exists())
        digest = self.documents.put('source', dict(source='first'))
        self.documents.path('source').write_text('{"source":"second"}')
        with self.assertRaises(documents.DocumentError):
            self.documents.read('source', digest)
        self.assertEqual(self.documents.path('source').read_text(), '{"source":"second"}')

    def test_document_names_cannot_escape_or_alias_phase_files(self):
        for name in ('../source', '/source', 'source.json', '.', '', 'a' * 65, 'phase'):
            with self.subTest(name=name), self.assertRaises(documents.DocumentError):
                self.documents.put(name, {})

    def test_capacity_refusal_does_not_publish_partial_document(self):
        with patch.object(documents, 'MAX_BYTES', 64):
            with self.assertRaises(durable_state.StateFileError):
                self.documents.put('source', dict(payload='x' * 64))
        self.assertFalse(self.documents.path('source').exists())
        with patch.object(documents, 'MAX_DIRECTORY_ENTRIES', 0):
            with self.assertRaises(documents.DocumentError):
                self.documents.put('source', {})

    def test_exact_directory_capacity_preserves_existing_documents(self):
        digest = self.documents.put('source', {})
        self.assertEqual(len(list(self.root.iterdir())), 2)
        with patch.object(documents, 'MAX_DIRECTORY_ENTRIES', 3):
            self.documents.put('runtime', {})
            with self.assertRaises(documents.DocumentError):
                self.documents.put('backup', {})
            self.assertEqual(self.documents.put('source', {}), digest)
            self.assertEqual(self.documents.read('source', digest), {})
        self.assertFalse(self.documents.path('backup').exists())

    def test_symlink_destination_is_never_followed_or_replaced(self):
        other = self.root / 'other'
        other.write_text('{}')
        other.chmod(0o600)
        self.documents.path('source').symlink_to(other)
        with self.assertRaises((OSError, durable_state.StateFileError)):
            self.documents.put('source', {})
        self.assertEqual(other.read_text(), '{}')
        self.assertTrue(self.documents.path('source').is_symlink())

    def test_repeat_and_read_cannot_accept_unconfirmed_document(self):
        value = dict(source='synthetic')
        digest = documents.fingerprint(value)
        with patch.object(durable_state.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                self.documents.put('source', value)
            self.assertEqual(durable_state.read(self.documents.path('source')), value)
            with self.assertRaises(OSError):
                self.documents.put('source', value)
            with self.assertRaises(OSError):
                self.documents.read('source', digest)
        self.assertEqual(self.documents.put('source', value), digest)
        self.assertEqual(self.documents.read('source', digest), value)


if __name__ == '__main__':
    unittest.main()
