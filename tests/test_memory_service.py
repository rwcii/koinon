"""Portable supervisor acceptance uses only temporary repositories and child processes."""
import asyncio
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

from koinon import durable_state
import memory
import memory_service
from koinon import memory_service_artifacts
from koinon import memory_service_config
from koinon import platform_support
from test_install import installer


class MemorySupervisorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix, self.repo, self.state = (self.root / name for name in ('prefix', 'repo', 'state'))
        self.prefix.mkdir(mode=0o700)
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        for filename in installer.FILES:
            target = self.prefix / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(filename, target)
        self.key, self.record = memory_service_config.selection(self.repo, self.state)
        self.record.update(backend='manual', artifact=None, artifact_digest=None, state='installed')
        self.config = dict(state_root=str(self.state), unit_dir=str(self.root / 'units'),
                           memory_services=dict(version=1, repositories={self.key: self.record}))
        self.save_config()
        self.selection = memory_service.Selection(self.prefix, self.repo)
        self.processes = []
        self.addCleanup(self.cleanup_processes)

    def save_config(self):
        path = self.prefix / 'install.json'
        path.write_text(json.dumps(self.config))
        path.chmod(0o600)

    def command(self, action, *args):
        return [sys.executable, str(self.prefix / 'memory_service.py'), action,
                '--prefix', str(self.prefix), '--repo', str(self.repo), *args]

    def spawn(self, command=None):
        process = subprocess.Popen(command or self.command('run'), stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True)
        self.processes.append(process)
        return process

    def cleanup_processes(self):
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=45)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=5)

    def wait_for(self, predicate, description, process=None, timeout=25):
        deadline = time.monotonic() + timeout
        last = None
        while time.monotonic() < deadline:
            try:
                last = predicate()
                if last:
                    return last
            except memory_service.RunnerError as exc:
                last = exc.code
            if process is not None and process.poll() is not None:
                out, err = process.communicate()
                self.fail(f'{description}: runner exited {process.returncode}; stdout={out}; stderr={err}')
            time.sleep(.05)
        self.fail(f'{description}: deadline exceeded; last observation={last!r}')

    def ready(self, process):
        return self.wait_for(lambda: memory_service.observation(self.selection)['running'],
                             'verified supervisor and memory readiness', process)

    def test_real_child_readiness_stop_and_store_preservation(self):
        process = self.spawn()
        self.ready(process)
        owner = memory_service.read_record(self.selection, self.selection.owner_path)
        self.assertEqual(owner['pid'], process.pid)
        self.assertNotEqual(owner['child']['pid'], process.pid)
        live = asyncio.run(memory.verify_running(self.selection.home, self.key))
        self.assertEqual(live['pid'], owner['child']['pid'])
        stopped = subprocess.run(self.command('stop'), capture_output=True, text=True, timeout=50)
        self.assertEqual(stopped.returncode, 0, stopped.stdout + stopped.stderr)
        process.communicate(timeout=10)
        self.assertEqual(process.returncode, 0)
        self.assertEqual(memory_service.observation(self.selection)['status'], 'stopped')
        self.assertTrue((self.selection.home / 'memory.sqlite3').exists())

    def test_external_memory_is_refused_without_stopping_or_adopting_it(self):
        external = self.spawn(self.selection.command())
        self.wait_for(lambda: asyncio.run(memory.verify_running(self.selection.home, self.key)),
                      'external synthetic memory readiness', external)
        runner = self.spawn()
        out, err = runner.communicate(timeout=25)
        self.assertEqual(runner.returncode, 78, out + err)
        self.assertIsNone(external.poll())
        self.assertEqual(memory_service.observation(self.selection)['status'], 'refused')
        owner = memory.read_owner(self.selection.home)
        self.assertEqual(owner['pid'], external.pid)
        memory.stop_service(self.selection.home, self.key, expected_generation=owner['generation'])
        external.communicate(timeout=10)

    def test_second_supervisor_refuses_while_first_keeps_its_child(self):
        first = self.spawn()
        self.ready(first)
        owner = memory_service.read_record(self.selection, self.selection.owner_path)
        second = self.spawn()
        out, err = second.communicate(timeout=15)
        self.assertEqual(second.returncode, 78, out + err)
        self.assertIn('supervisor_in_use', out)
        self.assertIsNone(first.poll())
        self.assertEqual(memory_service.read_record(self.selection, self.selection.owner_path), owner)
        self.assertFalse(self.selection.refusal_path.exists())

    def fail_child(self, code):
        path = self.prefix / 'memory.py'
        original = path.read_text()
        # Test-only installed copy. Imports still expose the actual memory module;
        # only the child CLI is made to report a deterministic startup failure.
        path.write_text(f'if __name__ == "__main__":\n    raise SystemExit({code})\n' + original)
        return original

    def test_permanent_refusal_survives_restart_until_explicit_retry(self):
        original = self.fail_child(78)
        first = self.spawn()
        out, err = first.communicate(timeout=25)
        self.assertEqual(first.returncode, 78, out + err)
        refusal = self.selection.refusal_path.read_bytes()
        second = self.spawn()
        out, err = second.communicate(timeout=15)
        self.assertEqual(second.returncode, 78, out + err)
        self.assertIn('recorded_refusal', out)
        self.assertEqual(self.selection.refusal_path.read_bytes(), refusal)
        (self.prefix / 'memory.py').write_text(original)
        result = subprocess.run(self.command('ensure', '--retry'), capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.selection.refusal_path.exists())
        self.assertEqual(json.loads(result.stdout)['status'], 'manual_required')
        third = self.spawn()
        self.ready(third)

    def select_launchd(self, backend='launchd'):
        units = self.root / 'units'
        units.mkdir(mode=0o700)
        self.record.update(backend=backend, artifact=str(units / memory_service_config.artifact_name(self.key, backend)),
                           artifact_digest='0' * 64)
        if backend == 'launchd':
            self.record['manager_domain'] = f'gui/{os.geteuid()}'
        content = platform_support.memory_service_artifact(self.prefix, sys.executable, self.key, self.record)
        self.record['artifact_digest'] = memory_service_artifacts.digest(content)
        Path(self.record['artifact']).write_bytes(content)
        Path(self.record['artifact']).chmod(0o600)
        self.save_config()
        self.selection = memory_service.Selection(self.prefix, self.repo)

    def test_launchd_boundary_maps_permanent_failure_but_status_retains_it(self):
        self.select_launchd()
        self.fail_child(70)
        runner = self.spawn()
        out, err = runner.communicate(timeout=25)
        self.assertEqual(runner.returncode, 0, out + err)
        status = subprocess.run(self.command('status'), capture_output=True, text=True, timeout=15)
        self.assertEqual(status.returncode, 70, status.stdout + status.stderr)
        result = json.loads(status.stdout)
        self.assertFalse(result['running'])
        self.assertEqual(result['status'], 'refused')
        self.assertEqual(result['exit_status'], 70)

    def test_unwritable_refusal_still_maps_launchd_failure_to_clean_exit(self):
        self.select_launchd()
        path = self.prefix / 'memory.py'
        original = path.read_text()
        path.write_text('if __name__ == "__main__":\n'
                        '    from pathlib import Path\n'
                        f'    Path({str(self.selection.refusal_path)!r}).mkdir()\n'
                        '    raise SystemExit(70)\n' + original)
        runner = self.spawn()
        out, err = runner.communicate(timeout=25)
        self.assertEqual(runner.returncode, 0, out + err)
        self.assertIn('"refusal_recorded": false', err)
        diagnostic = self.selection.home / 'supervisor-diagnostic.json'
        self.assertEqual(diagnostic.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads(diagnostic.read_text())['primary_code'], 'memory_software_failure')
        status = subprocess.run(self.command('status'), capture_output=True, text=True, timeout=15)
        self.assertNotEqual(status.returncode, 0)
        self.assertEqual(json.loads(status.stdout)['status'], 'unavailable')
        # A later invocation must refuse the unreadable marker before spawning.
        second = self.spawn()
        out, err = second.communicate(timeout=15)
        self.assertEqual(second.returncode, 0, out + err)
        self.assertNotIn('FileExistsError', err)

    def test_foreground_retries_temporary_child_failure_after_backoff(self):
        path = self.prefix / 'memory.py'
        original = path.read_text()
        attempted = self.root / 'attempted'
        path.write_text('if __name__ == "__main__":\n'
                        '    from pathlib import Path\n'
                        f'    marker = Path({str(attempted)!r})\n'
                        '    if not marker.exists():\n'
                        '        marker.touch()\n'
                        '        raise SystemExit(75)\n' + original)
        runner = self.spawn()
        self.wait_for(lambda: self.selection.owner_path.exists() and
                      memory_service.read_record(self.selection, self.selection.owner_path)['phase'] == 'backoff',
                      'temporary child failure enters backoff', runner)
        started = time.monotonic()
        self.ready(runner)
        self.assertGreater(time.monotonic() - started, 8)
        self.assertFalse(self.selection.refusal_path.exists())

    def test_shutdown_uncertainty_retains_primary_failure_and_blocks_retry(self):
        for code in ('memory_temporary_failure', 'memory_configuration_failure'):
            with self.subTest(code=code):
                supervisor = memory_service.Supervisor(self.selection, Mock(is_set=lambda: False))
                child = Mock(pid=123, poll=lambda: None)
                with patch.object(memory_service, 'verify_memory', side_effect=[None, memory_service.RunnerError(code)]), \
                        patch.object(supervisor, 'publish'), \
                        patch.object(memory_service.subprocess, 'Popen', return_value=child), \
                        patch.object(platform_support, 'proc_start', return_value='123'), \
                        patch.object(supervisor, 'shutdown', side_effect=memory_service.RunnerError('shutdown_unconfirmed')):
                    with self.assertRaises(memory_service.RunnerError) as caught:
                        supervisor.attempt()
                self.assertEqual(caught.exception.exit_status, 78)
                self.assertEqual(caught.exception.primary_code, code)
                self.assertEqual(caught.exception.shutdown_code, 'shutdown_unconfirmed')

    def test_stop_during_backoff_prevents_another_child_start(self):
        self.fail_child(75)
        runner = self.spawn()
        self.wait_for(lambda: self.selection.owner_path.exists() and
                      memory_service.read_record(self.selection, self.selection.owner_path)['phase'] == 'backoff',
                      'temporary failure reaches backoff before stop', runner)
        result = subprocess.run(self.command('stop'), capture_output=True, text=True, timeout=25)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        runner.communicate(timeout=10)
        self.assertEqual(runner.returncode, 0)
        self.assertEqual(memory_service.observation(self.selection)['status'], 'stopped')

    def test_stale_stop_request_does_not_stop_replacement_supervisor(self):
        first = self.spawn()
        self.ready(first)
        result = subprocess.run(self.command('stop'), capture_output=True, text=True, timeout=25)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        first.communicate(timeout=10)
        stale = self.selection.stop_path.read_bytes()
        second = self.spawn()
        self.ready(second)
        self.assertEqual(self.selection.stop_path.read_bytes(), stale)
        self.assertIsNone(second.poll())

    def test_zero_exit_without_child_readiness_is_not_adopted(self):
        self.fail_child(0)
        runner = self.spawn()
        out, err = runner.communicate(timeout=25)
        self.assertEqual(runner.returncode, 78, out + err)
        self.assertNotIn('"running": true', out)
        self.assertEqual(memory_service.observation(self.selection)['status'], 'refused')

    def loaded_report(self, pid=0):
        argv = platform_support.memory_service_command(self.prefix, sys.executable, self.key, self.record)
        return dict(status='observed', artifact=self.record['artifact'], executable=argv[0],
                    argv=argv, pid=pid, active_state='active' if pid else 'inactive', invocation='a' * 32)

    def test_managed_ensure_requires_loaded_identity_and_real_child_readiness(self):
        self.select_launchd('systemd')
        registered, started = [], []
        def observe(_name):
            return (self.loaded_report(started[0].pid if started else 0)
                    if registered else dict(status='absent'))
        def activate(record, operation):
            self.assertEqual(record, self.record)
            if operation == 'register':
                registered.append(True)
            elif operation == 'restart':
                started.append(self.spawn())
        with patch.object(platform_support, 'memory_manager_available', return_value=True), \
                patch.object(platform_support, 'systemd_service_observation', side_effect=observe), \
                patch.object(platform_support, 'memory_manager_action', side_effect=activate) as action:
            result = memory_service.ensure_managed(self.selection)
            self.assertTrue(result['running'])
            self.assertTrue(result['managed'])
            self.assertTrue(memory_service.ensure_managed(self.selection)['managed'])
            self.assertEqual([call.args[1] for call in action.call_args_list], ['register', 'activate', 'restart'])
            with patch.object(platform_support, 'systemd_service_observation', return_value=self.loaded_report(9999999)):
                self.assertFalse(memory_service.managed_status(self.selection)['running'])

    def test_managed_ensure_never_activates_foreign_or_unknown_job(self):
        self.select_launchd('systemd')
        foreign = self.loaded_report()
        foreign['argv'] = ['/synthetic/foreign']
        for report, code in [(foreign, 'manager_ownership_conflict'),
                             (dict(status='unknown'), 'manager_observation_unknown')]:
            with self.subTest(code=code), \
                    patch.object(platform_support, 'memory_manager_available', return_value=True), \
                    patch.object(platform_support, 'systemd_service_observation', return_value=report), \
                    patch.object(platform_support, 'memory_manager_action') as action:
                with self.assertRaises(memory_service.RunnerError) as caught:
                    memory_service.ensure_managed(self.selection)
                self.assertEqual(caught.exception.code, code)
                action.assert_not_called()

    def test_systemd_registration_is_verified_before_start(self):
        self.select_launchd('systemd')
        registered, started = [], []
        def observe(_name):
            return (self.loaded_report(started[0].pid if started else 0)
                    if registered else dict(status='absent'))
        def action(_record, operation):
            if operation == 'register':
                registered.append(True)
            elif operation == 'activate':
                self.assertTrue(registered)
                self.assertFalse(started)
            elif operation == 'restart':
                started.append(self.spawn())
            else:
                self.fail('unexpected manager operation')
        with patch.object(platform_support, 'memory_manager_available', return_value=True), \
                patch.object(platform_support, 'systemd_service_observation', side_effect=observe), \
                patch.object(platform_support, 'memory_manager_action', side_effect=action) as operations:
            self.assertTrue(memory_service.ensure_managed(self.selection)['managed'])
            self.assertEqual([call.args[1] for call in operations.call_args_list], ['register', 'activate', 'restart'])

    def test_systemd_changed_loaded_command_after_registration_never_starts(self):
        self.select_launchd('systemd')
        foreign = self.loaded_report(0)
        foreign['argv'] = ['/synthetic/foreign']
        with patch.object(platform_support, 'memory_manager_available', return_value=True), \
                patch.object(platform_support, 'systemd_service_observation', side_effect=[
                    dict(status='absent'), dict(status='absent'), foreign]), \
                patch.object(platform_support, 'memory_manager_action') as action:
            with self.assertRaises(memory_service.RunnerError) as caught:
                memory_service.ensure_managed(self.selection)
            self.assertEqual(caught.exception.code, 'manager_ownership_conflict')
            self.assertEqual([call.args[1] for call in action.call_args_list], ['register'])

    def test_unavailable_selected_manager_reports_manual_without_creating_state(self):
        self.select_launchd('systemd')
        with patch.object(platform_support, 'memory_manager_available', return_value=False), \
                patch.object(platform_support, 'memory_manager_action') as action:
            result = memory_service.ensure_managed(self.selection)
        self.assertEqual(result['status'], 'manual_required')
        self.assertFalse(result['running'])
        self.assertFalse(self.state.exists())
        action.assert_not_called()

    def test_ensure_is_read_only_and_never_reports_unstarted_service_running(self):
        result = subprocess.run(self.command('ensure'), capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        data = json.loads(result.stdout)
        self.assertEqual(data['status'], 'manual_required')
        self.assertFalse(data['running'])
        self.assertIn('--foreground', data['start_command'])
        self.assertFalse(self.state.exists())

    def test_unknown_process_observation_cannot_claim_verified_readiness(self):
        process = self.spawn()
        self.ready(process)
        with patch.object(memory_service, 'process_state', return_value='unknown'):
            result = memory_service.observation(self.selection)
        self.assertEqual(result, dict(status='unavailable', running=False))


if __name__ == '__main__':
    unittest.main()
