import copy
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from koinon import durable_state
from koinon import session_supervisor_state as state


class SessionStateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.records = state.Records(self.home, 'a' * 64, 'b' * 64)
        self.owner = self.records.new_owner()

    def test_failure_codes_are_closed_and_all_have_exit_classification(self):
        from koinon import session_supervisor
        self.assertEqual(set(session_supervisor.STATUSES), state.CODES)
        with self.assertRaises(ValueError):
            state.StateError('unclassified')
        with state.state_lock(self.home / 'supervisor.lock', 'session_supervisor_in_use'):
            with self.assertRaises(state.StateError) as caught:
                self.records.retry()
        self.assertEqual(caught.exception.code, 'session_supervisor_in_use')

    def test_running_requires_both_captured_child_generations(self):
        owner = copy.deepcopy(self.owner)
        owner['phase'] = 'running'
        for index, key in enumerate(state.CHILDREN):
            with self.assertRaises(state.StateError):
                self.records.publish(owner)
            owner['children'][key] = dict(pid=100 + index, proc_start='synthetic-start', generation='c' * 32)
        self.records.publish(owner)
        self.assertEqual(self.records.read(), owner)
        self.assertEqual(self.records.owner_path.stat().st_mode & 0o777, 0o600)

    def test_stop_targets_captured_generation_and_stale_request_is_inert(self):
        self.records.publish(self.owner)
        captured = self.records.request_stop()
        self.assertEqual(captured, self.owner)
        self.assertTrue(self.records.stop_requested(captured['generation']))
        self.assertFalse(self.records.stop_requested('f' * 32))
        value = durable_state.read(self.records.stop_path)
        value['installation'] = 'c' * 64
        durable_state.publish(self.records.stop_path, value)
        with self.assertRaises(state.StateError):
            self.records.stop_requested(captured['generation'])

    def test_changed_configuration_or_owner_refuses_before_stop_publication(self):
        self.records.publish(self.owner)
        other = state.Records(self.home, 'a' * 64, 'c' * 64)
        with self.assertRaises(state.StateError):
            other.request_stop()
        self.assertFalse(self.records.stop_path.exists())
        successor = dict(self.owner, generation='d' * 32)
        with patch.object(self.records, 'read', side_effect=[self.owner, successor]):
            with self.assertRaises(state.StateError):
                self.records.request_stop()
        self.assertFalse(self.records.stop_path.exists())

    def test_shutdown_requires_terminal_record_and_both_children_dead(self):
        owner = copy.deepcopy(self.owner)
        owner.update(phase='stopped', exit_status=0)
        owner['children']['notifier'] = dict(pid=100, proc_start='synthetic', generation='c' * 32)
        self.records.publish(owner)
        with patch.object(state, 'alive_state', side_effect=lambda row: 'alive' if row['pid'] == 100 else 'dead'):
            with self.assertRaises(state.StateError) as caught:
                self.records.wait_stopped(owner, timeout=0)
            self.assertEqual(caught.exception.code, 'session_shutdown_unconfirmed')
        with patch.object(state, 'alive_state', return_value='dead'):
            self.assertEqual(self.records.wait_stopped(owner, timeout=0)['status'], 'stopped')
            interrupted = dict(self.owner, spawn_pending='bridge')
            self.records.publish(interrupted)
            with self.assertRaises(state.StateError):
                self.records.wait_stopped(interrupted, timeout=0)

    def test_retry_preserves_refusal_until_every_recorded_process_is_dead(self):
        failure = dict(self.owner, phase='failed', exit_status=78,
                       primary_code='session_configuration_failure')
        self.records.publish(failure)
        self.records.publish(failure, refusal=True)
        before = self.records.refusal_path.read_bytes()
        for observed in ('alive', 'unknown'):
            with patch.object(state, 'alive_state', return_value=observed):
                with self.assertRaises(state.StateError):
                    self.records.retry()
                self.assertEqual(self.records.refusal_path.read_bytes(), before)
        with patch.object(state, 'alive_state', return_value='dead'):
            self.records.retry()
        self.assertFalse(self.records.refusal_path.exists())
        self.assertEqual(self.records.read(), failure)
        self.assertTrue(self.records.retry_requested(failure['generation']))
        self.assertFalse(self.records.retry_requested('f' * 32))

    def test_unresolved_spawn_blocks_retry_even_when_terminal_and_parent_dead(self):
        failure = dict(self.owner, phase='failed', exit_status=78, spawn_pending='notifier',
                       primary_code='session_configuration_failure', shutdown_code='session_shutdown_unconfirmed')
        self.records.publish(failure)
        self.records.publish(failure, refusal=True)
        with patch.object(state, 'alive_state', return_value='dead'):
            with self.assertRaises(state.StateError):
                self.records.retry()
            with self.assertRaises(state.StateError):
                self.records.wait_stopped(failure, timeout=0)
        self.assertEqual(self.records.read(refusal=True), failure)
        self.assertFalse(self.records.retry_path.exists())

    def test_dead_pair_with_no_unresolved_spawn_can_be_observed_after_runner_crash(self):
        interrupted = dict(self.owner, phase='starting')
        self.records.publish(interrupted)
        with patch.object(state, 'alive_state', return_value='dead'):
            self.assertEqual(self.records.wait_stopped(interrupted, timeout=0)['status'], 'stopped')
            self.records.retry()
        self.assertTrue(self.records.retry_requested(interrupted['generation']))

    def test_explicit_spawn_recovery_retains_evidence_without_claiming_observed_exit(self):
        failure = dict(self.owner, phase='failed', exit_status=78, spawn_pending='bridge',
                       primary_code='session_configuration_failure', shutdown_code='session_shutdown_unconfirmed')
        self.records.publish(failure)
        self.records.publish(failure, refusal=True)
        with self.assertRaises(state.StateError):
            self.records.recover_spawn(failure['generation'])
        with patch.object(state, 'alive_state', return_value='alive'):
            with self.assertRaises(state.StateError):
                self.records.recover_spawn(failure['generation'], assert_no_unrecorded_child=True)
        with patch.object(state, 'alive_state', return_value='dead'):
            with self.assertRaises(state.StateError):
                self.records.recover_spawn('f' * 32, assert_no_unrecorded_child=True)
            result = self.records.recover_spawn(failure['generation'], assert_no_unrecorded_child=True)
            self.assertEqual(result['status'], 'operator_assertion_recorded')
            evidence = durable_state.read(self.records.recovery_path)
            self.assertEqual(evidence['basis'], 'operator_assertion')
            self.assertEqual(evidence['owner'], failure)
            self.assertEqual(evidence['refusal'], failure)
            self.assertEqual(self.records.read(), failure)
            self.assertEqual(self.records.read(refusal=True), failure)
            self.assertFalse(self.records.retry_requested(failure['generation']))
            with self.assertRaises(state.StateError):
                self.records.wait_stopped(failure, timeout=0)
            self.records.retry()
        self.assertTrue(self.records.retry_requested(failure['generation']))
        self.assertFalse(self.records.spawn_recovered(dict(failure, generation='f' * 32)))
        self.assertEqual(durable_state.read(self.records.recovery_path), evidence)

    def test_legacy_nonprivate_lock_refuses_without_changing_permissions(self):
        lock = self.home / 'supervisor.lock'
        lock.touch(mode=0o600)
        lock.chmod(0o664)
        with self.assertRaises(state.StateError) as caught:
            self.records.retry()
        self.assertEqual(caught.exception.code, 'invalid_session_state')
        self.assertEqual(lock.stat().st_mode & 0o777, 0o664)

    def test_foreign_or_unsafe_evidence_is_never_repaired(self):
        self.records.publish(self.owner)
        self.records.owner_path.chmod(0o644)
        with self.assertRaises(state.StateError):
            self.records.read()
        self.assertEqual(self.records.owner_path.stat().st_mode & 0o777, 0o644)
        self.records.owner_path.chmod(0o600)
        foreign = dict(self.owner, installation='c' * 64)
        durable_state.publish(self.records.owner_path, foreign)
        with self.assertRaises(state.StateError):
            self.records.retry()
        self.assertEqual(durable_state.read(self.records.owner_path), foreign)
