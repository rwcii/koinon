from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import durable_state
import upgrade_journal as journal


class JournalTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = journal.Journal(self.root, 'a' * 64)

    def test_concurrent_reader_waits_for_short_metadata_publication(self):
        from concurrent.futures import ThreadPoolExecutor
        from participant_lock import file_lock
        import threading
        import time
        value = self.state.initialize()
        entered = threading.Event()
        def reader():
            entered.set()
            return self.state.read()
        with ThreadPoolExecutor(max_workers=1) as executor:
            with file_lock(self.state.lock_path, 'synthetic_busy', None):
                future = executor.submit(reader)
                self.assertTrue(entered.wait(1))
                time.sleep(.1)
                self.assertFalse(future.done(), 'brief contention must not kill a gate reader')
            self.assertEqual(future.result(timeout=2), value)

    def test_resume_never_recreates_a_missing_journal(self):
        with self.assertRaisesRegex(journal.JournalError, 'missing'):
            self.state.read()
        self.assertFalse(self.state.path.exists())

    def test_all_phases_record_intent_then_completion_and_release_once(self):
        value = self.state.initialize()
        for step in range(journal.LAST_STEP + 1):
            self.assertEqual(value['step'], step)
            description = self.state.describe(value)
            self.assertEqual(description['release_decision_committed'], step >= journal.RELEASE_STEP)
            self.assertEqual(description['finished'], step == journal.LAST_STEP)
            self.assertEqual(self.state.initialize(), value)
            if step < journal.LAST_STEP:
                evidence = f'{step:064x}' if step % 2 == 0 else None
                value = self.state.advance(value, evidence=evidence)
        with self.assertRaisesRegex(journal.JournalError, 'already complete'):
            self.state.advance(value)
        self.assertEqual(len(value['receipts']), len(journal.PHASES))

    def test_lost_publication_reply_resumes_identical_transition(self):
        value = self.state.initialize()
        publish = durable_state.publish

        def publish_then_fail(*args):
            publish(*args)
            raise OSError('simulated lost completion')

        with patch.object(durable_state, 'publish', side_effect=publish_then_fail):
            with self.assertRaises(OSError):
                self.state.advance(value, evidence='b' * 64)
        observed = self.state.read()
        self.assertEqual(observed['step'], 1)
        self.assertEqual(self.state.advance(value, evidence='b' * 64), observed)
        with self.assertRaises(journal.JournalError):
            self.state.advance(value, evidence='c' * 64)

    def test_failure_before_publication_preserves_predecessor(self):
        value = self.state.initialize()
        with patch.object(durable_state, 'publish', side_effect=OSError('disk unavailable')):
            with self.assertRaises(OSError):
                self.state.advance(value, evidence='b' * 64)
        self.assertEqual(self.state.read(), value)
        self.assertEqual(self.state.advance(value, evidence='b' * 64)['step'], 1)

    def test_stale_predecessor_cannot_rewind_or_skip(self):
        original = self.state.initialize()
        completed = self.state.advance(original, evidence='b' * 64)
        current = self.state.advance(completed)
        with self.assertRaises(journal.JournalError):
            self.state.advance(original, evidence='b' * 64)
        self.assertEqual(self.state.read(), current)
        with self.assertRaises(journal.JournalError):
            self.state.advance(dict(current, step=8))

    def test_wrong_plan_and_malformed_state_are_preserved(self):
        self.state.initialize()
        other = journal.Journal(self.root, 'b' * 64)
        with self.assertRaises(journal.JournalError):
            other.read()
        with self.assertRaises(journal.JournalError):
            other.initialize()
        self.state.path.write_text('{broken')
        before = self.state.path.read_bytes()
        with self.assertRaises(ValueError):
            self.state.initialize()
        self.assertEqual(before, self.state.path.read_bytes())

    def test_permanent_lock_inode_survives_transitions(self):
        value = self.state.initialize()
        original = self.state.lock_path.stat().st_ino
        self.state.advance(value, evidence='b' * 64)
        self.assertEqual(self.state.lock_path.stat().st_ino, original)
        self.assertEqual(self.state.path.stat().st_mode & 0o777, 0o600)

    def test_nonprivate_directory_is_not_repaired(self):
        self.root.chmod(0o755)
        with self.assertRaises(journal.JournalError):
            self.state.read()
        self.assertEqual(self.root.stat().st_mode & 0o777, 0o755)

    def test_release_decision_survives_resume_and_cannot_be_undone(self):
        value = self.state.initialize()
        while value['step'] < journal.RELEASE_STEP:
            value = self.state.advance(value, evidence='b' * 64 if value['step'] % 2 == 0 else None)
        reopened = journal.Journal(self.root, 'a' * 64)
        self.assertTrue(reopened.describe(reopened.read())['release_decision_committed'])
        next_value = reopened.advance(value)
        self.assertTrue(reopened.describe(next_value)['release_decision_committed'])
        with self.assertRaises(journal.JournalError):
            reopened.advance(dict(value, step=0, receipts=[]), evidence='b' * 64)


    def test_operation_lock_excludes_second_coordinator_but_not_journal_access(self):
        from participant_lock import OwnershipError
        value = self.state.initialize()
        second = journal.Journal(self.root, 'a' * 64)
        with self.state.operation():
            self.assertEqual(second.read(), value)
            with self.assertRaises(OwnershipError) as caught:
                with second.operation():
                    self.fail('second coordinator admitted')
            self.assertEqual(caught.exception.code, 'upgrade_coordinator_busy')
            value = self.state.advance(value, evidence='b' * 64)
        with second.operation():
            self.assertEqual(second.read(), value)


    def test_initial_alias_is_frozen_before_journal_mutation(self):
        selected = self.root / 'selected'
        selected.mkdir(mode=0o700)
        other = self.root / 'other'
        other.mkdir(mode=0o700)
        alias = self.root / 'alias'
        alias.symlink_to(selected, target_is_directory=True)
        state = journal.Journal(alias, 'a' * 64)
        value = state.initialize()
        self.assertEqual(state.directory, selected.resolve())
        alias.unlink()
        alias.symlink_to(other, target_is_directory=True)
        self.assertEqual(state.read(), value)
        self.assertFalse((other / 'phase.json').exists())

    def test_initialization_retry_requires_successful_durability_confirmation(self):
        with patch.object(durable_state.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                self.state.initialize()
            self.assertEqual(durable_state.read(self.state.path)['step'], 0)
            with self.assertRaises(OSError):
                self.state.initialize()
            with self.assertRaises(OSError):
                self.state.read()
        self.assertEqual(self.state.initialize()['step'], 0)

    def test_release_retry_cannot_accept_visible_but_unconfirmed_record(self):
        value = self.state.initialize()
        while value['step'] < journal.RELEASE_STEP - 1:
            value = self.state.advance(value, evidence='b' * 64 if value['step'] % 2 == 0 else None)
        publish = durable_state.publish
        def fail_after_replace(*args):
            with patch.object(durable_state.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
                publish(*args)
        with patch.object(durable_state, 'publish', side_effect=fail_after_replace):
            with self.assertRaises(OSError):
                self.state.advance(value, evidence='c' * 64)
        self.assertEqual(durable_state.read(self.state.path)['step'], journal.RELEASE_STEP)
        with patch.object(durable_state.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                self.state.advance(value, evidence='c' * 64)
            with self.assertRaises(OSError):
                self.state.read()
        self.assertTrue(self.state.describe(self.state.advance(value, evidence='c' * 64))['release_decision_committed'])


if __name__ == '__main__':
    unittest.main()
