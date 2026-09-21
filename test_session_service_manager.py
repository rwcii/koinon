from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

import platform_support
import session_service as service
import session_service_manager as manager
import test_session_service as service_tests


class NativeSessionManagerTests(unittest.TestCase):
    def setUp(self):
        service_tests.NativeSessionServiceTests.setUp(self)
        command = platform_support.session_service_command(self.record)
        self.loaded = dict(status='observed', argv=command, executable=command[0],
                           artifact=self.record['artifact'], pid=0, active_state='inactive')

    def test_inert_registration_is_verified_before_start(self):
        events = []
        def action(record, operation):
            events.append(operation)
        observations = [dict(status='absent'), dict(status='absent'), self.loaded, self.loaded]
        with patch.object(manager, 'observation', side_effect=observations), \
                patch.object(platform_support, 'session_manager_action', side_effect=action), \
                patch.object(manager, 'status', return_value=dict(status='running', managed=True)):
            self.assertEqual(manager.ensure(self.selection)['status'], 'running')
        self.assertEqual(events, ['register', 'start'])

    def test_changed_loaded_registration_cannot_reach_start(self):
        values = [dict(status='absent'), dict(status='absent'), dict(status='unknown')]
        with patch.object(manager, 'observation', side_effect=values), \
                patch.object(platform_support, 'session_manager_action') as action:
            with self.assertRaises(service.ServiceError):
                manager.ensure(self.selection)
        self.assertEqual([call.args[1] for call in action.call_args_list], ['register'])

    def test_timeout_never_becomes_success_or_triggers_second_mutation(self):
        with patch.object(manager, 'observation', return_value=dict(status='absent')), \
                patch.object(platform_support, 'session_manager_action',
                             side_effect=subprocess.TimeoutExpired('synthetic', 1)) as action:
            with self.assertRaises(service.ServiceError) as caught:
                manager.ensure(self.selection)
        self.assertEqual(caught.exception.code, 'session_temporary_failure')
        action.assert_called_once()

    def test_foreign_loaded_arguments_refuse_before_mutation_and_name_path(self):
        wrong = dict(self.loaded, argv=['/foreign'])
        with patch.object(platform_support, 'session_manager_observation', return_value=wrong), \
                patch.object(platform_support, 'session_manager_action') as action:
            with self.assertRaises(service.ServiceError) as caught:
                manager.ensure(self.selection)
        self.assertEqual(caught.exception.paths, (self.record['artifact'],))
        action.assert_not_called()

    def test_permanent_failure_is_not_automatically_retried(self):
        owner = self.records.new_owner()
        owner.update(phase='failed', exit_status=78, primary_code='session_configuration_failure')
        self.records.publish(owner)
        with patch.object(platform_support, 'session_manager_observation') as observe, \
                patch.object(platform_support, 'session_manager_action') as action:
            result = manager.ensure(self.selection)
        self.assertEqual(result['status'], 'refused')
        self.assertEqual(result['exit_status'], 78)
        observe.assert_not_called()
        action.assert_not_called()

    def test_loaded_process_without_owned_runner_is_not_adopted(self):
        with patch.object(manager, 'observation', return_value=dict(self.loaded, pid=12345)), \
                patch.object(platform_support, 'session_manager_action') as action:
            with self.assertRaises(service.ServiceError):
                manager.ensure(self.selection)
        action.assert_not_called()

    def test_higher_precedence_legacy_unit_refuses_before_manager_mutation(self):
        persistent, runtime = self.root / 'persistent', self.root / 'runtime'
        persistent.mkdir(mode=0o700)
        runtime.mkdir(mode=0o700)
        shadow = persistent / Path(self.record['artifact']).name
        shadow.write_bytes(Path(self.record['artifact']).read_bytes())
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support, 'systemd_registration_layout',
                             return_value=(persistent, runtime, (persistent, runtime))), \
                patch.object(platform_support.subprocess, 'run') as run:
            with self.assertRaises(ValueError) as caught:
                platform_support.session_manager_action(self.record, 'register')
        self.assertEqual(caught.exception.paths, (str(shadow),))
        run.assert_not_called()
        self.assertTrue(shadow.is_file())
        self.assertFalse((runtime / shadow.name).exists())

    def test_runtime_registration_never_enables_login_or_forces_replacement(self):
        persistent, runtime = self.root / 'persistent', self.root / 'runtime'
        persistent.mkdir(mode=0o700)
        runtime.mkdir(mode=0o700)
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support, 'systemd_registration_layout',
                             return_value=(persistent, runtime, (persistent, runtime))), \
                patch.object(platform_support.subprocess, 'run') as run:
            platform_support.session_manager_action(self.record, 'register')
        self.assertEqual(run.call_args.args[0], ['systemctl', '--user', '--no-ask-password', '--runtime',
                                                'link', self.record['artifact']])

    def test_stop_checks_manager_runner_identity_before_request(self):
        owner = self.records.new_owner()
        self.records.publish(owner)
        with patch.object(manager, 'observation', return_value=dict(self.loaded, pid=owner['pid'] + 1)), \
                patch.object(service, 'stop_owned') as stop:
            with self.assertRaises(service.ServiceError):
                manager.stop(self.selection)
        stop.assert_not_called()

    def test_deactivation_without_owner_evidence_refuses(self):
        with patch.object(manager, 'observation', return_value=self.loaded), \
                patch.object(platform_support, 'session_manager_deactivate') as deactivate:
            with self.assertRaises(service.ServiceError):
                manager.deactivate(self.selection)
        deactivate.assert_not_called()

    def test_unavailable_manager_reports_manual_command_without_mutation(self):
        with patch.object(manager, 'observation', return_value=dict(status='unknown')), \
                patch.object(platform_support, 'memory_manager_available', return_value=False), \
                patch.object(platform_support, 'session_manager_action') as action:
            result = manager.ensure(self.selection)
        self.assertEqual(result['status'], 'manual_required')
        self.assertFalse(result['running'])
        import shlex
        self.assertEqual(shlex.split(result['start_command']),
                         platform_support.session_service_command(self.record))
        action.assert_not_called()

    def test_unknown_job_with_available_manager_is_temporary_failure(self):
        with patch.object(manager, 'observation', return_value=dict(status='unknown')), \
                patch.object(platform_support, 'memory_manager_available', return_value=True), \
                patch.object(platform_support, 'session_manager_action') as action:
            with self.assertRaises(service.ServiceError) as caught:
                manager.ensure(self.selection)
        self.assertEqual(caught.exception.code, 'session_temporary_failure')
        action.assert_not_called()
