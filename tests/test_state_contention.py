import contextlib
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import bridge
from koinon import durable_state
import memory_service
from koinon import notification_migration
import session_service
from koinon.session_supervisor_state import Records, StateError


class StateContentionTests(unittest.TestCase):
    def test_session_owner_contention_is_temporary_not_invalid_evidence(self):
        with tempfile.TemporaryDirectory() as raw:
            records = Records(Path(raw), 'a' * 64, 'b' * 64)
            with patch.object(durable_state, 'read', side_effect=durable_state.StateReadBusyError()):
                with self.assertRaises(StateError) as caught:
                    records.read()
            self.assertEqual(caught.exception.code, 'session_temporary_failure')
            self.assertFalse(records.refusal_path.exists())

    def test_memory_boundaries_keep_contention_retryable(self):
        error = durable_state.StateReadBusyError()
        self.assertEqual(memory_service.classify(error).exit_status, 75)
        with self.assertRaises(memory_service.RunnerError) as caught:
            with memory_service.configuration_boundary():
                raise error
        self.assertEqual(caught.exception.exit_status, 75)

    def test_service_entrypoints_report_temporary_status(self):
        with patch.object(bridge, 'cli_main', side_effect=durable_state.StateReadBusyError()), \
                contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as caught:
            bridge.main()
        self.assertEqual(caught.exception.code, 75)
        with patch.object(session_service, 'Selection', side_effect=durable_state.StateReadBusyError()), \
                contextlib.redirect_stdout(io.StringIO()):
            code = session_service.main(['status', '--prefix', '/synthetic', '--state-dir', '/synthetic/state',
                                         '--backend', 'systemd'])
        self.assertEqual(code, 75)

    def test_notification_migration_does_not_turn_contention_into_operator_recovery(self):
        with patch.object(durable_state, 'read', side_effect=durable_state.StateReadBusyError()):
            with self.assertRaises(durable_state.StateReadBusyError):
                notification_migration.read_state('/synthetic/marker.json')
