import waiting
import contextlib
import io
import json
import os
import shutil
import subprocess
import time
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

from koinon import durable_state
from koinon import platform_support
import session_service as service
from koinon import session_service_artifacts as artifacts
from koinon import session_service_config as configuration
from koinon.session_supervisor_state import StateError
import test_session_service_artifacts as artifact_tests
from scripts.install import FILES
from test_session import isolate_account_home
from repo_root import ROOT


class NativeSessionServiceTests(unittest.TestCase):
    def setUp(self):
        artifact_tests.NativeSessionArtifactsTests.setUp(self)
        self.config['codex'] = sys.executable
        durable_state.publish(self.prefix / 'install.json', self.config)
        self.record = configuration.selection(self.prefix, sys.executable, self.home,
                                               self.config, self.registration, 'systemd')
        artifacts.publish(self.record)
        for name in ('session_service.py', 'bridge.py', 'notify.py'):
            path = self.prefix / name
            path.write_text('# synthetic entrypoint\n')
            path.chmod(0o600)
        self.selection = service.Selection(self.prefix, self.home, backend='systemd')
        self.records = self.selection.records

    def pending(self):
        owner = self.records.new_owner()
        owner.update(spawn_pending='bridge', phase='failed', exit_status=78,
                     primary_code='session_shutdown_unconfirmed', shutdown_code='session_shutdown_unconfirmed')
        self.records.publish(owner)
        self.records.publish(owner, refusal=True)
        return owner

    def test_saved_selection_does_not_accept_other_prefix_backend_or_interpreter(self):
        for values in (dict(prefix=self.root / 'other'), dict(backend='launchd'), dict(python='/other/python')):
            options = dict(prefix=self.prefix, home=self.home, backend='systemd')
            options.update(values)
            with self.subTest(values=values), self.assertRaises(ValueError):
                service.Selection(**options)

    def test_recovery_basis_is_mandatory_and_never_turns_assertion_into_exit(self):
        owner = self.pending()
        before = self.records.owner_path.read_bytes(), self.records.refusal_path.read_bytes()
        with patch('koinon.session_supervisor_state.alive_state', return_value='dead'), \
                patch.object(service, 'alive_state', return_value='dead'):
            result = service.execute('recover-spawn', self.selection,
                                     generation=owner['generation'], assertion=True)
            self.assertEqual(result['recovery']['basis'], 'operator_assertion')
            observed = service.status(self.selection)
            self.assertEqual(observed['status'], 'unresolved')
            self.assertEqual(observed['basis'], 'incomplete_observation')
            self.assertEqual(observed['recovery']['basis'], 'operator_assertion')
            self.assertEqual(before, (self.records.owner_path.read_bytes(), self.records.refusal_path.read_bytes()))
            result = service.execute('retry', self.selection)
            self.assertEqual(result['recovery']['basis'], 'operator_assertion')
            raw = durable_state.read(self.records.recovery_path)
            del raw['basis']
            durable_state.publish(self.records.recovery_path, raw)
            with self.assertRaises(StateError):
                service.status(self.selection)
            with patch.object(self.records, 'retry') as retry:
                with self.assertRaises(StateError):
                    service.execute('retry', self.selection)
                retry.assert_not_called()

    def test_recovery_requires_explicit_assertion_and_matching_generation(self):
        owner = self.pending()
        for generation, assertion in ((owner['generation'], False), ('f' * 32, True)):
            with self.subTest(generation=generation, assertion=assertion), self.assertRaises(StateError):
                service.execute('recover-spawn', self.selection, generation=generation, assertion=assertion)
        self.assertFalse(self.records.recovery_path.exists())

    def test_public_run_reports_unsafe_lock_path_without_changing_it(self):
        lock = self.home / 'supervisor.lock'
        lock.chmod(0o664)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), patch.object(service.session_supervisor, 'run') as run:
            status = service.main(['run', '--prefix', str(self.prefix), '--state-dir', str(self.home),
                                   '--backend', 'systemd'])
            run.assert_not_called()
        self.assertEqual(status, 78)
        self.assertIn(str(lock), json.loads(output.getvalue())['paths'])
        self.assertEqual(lock.stat().st_mode & 0o777, 0o664)

    def test_launchd_early_configuration_failure_is_permanent_without_restart_loop(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            result = service.main(['run', '--prefix', str(self.prefix), '--state-dir', str(self.home),
                                   '--backend', 'launchd'])
        self.assertEqual(result, 0)
        self.assertEqual(json.loads(output.getvalue())['exit_status'], 78)

    def test_saved_running_phase_without_live_pair_does_not_claim_readiness(self):
        owner = self.records.new_owner()
        owner.update(phase='running', children=dict(
            bridge=dict(pid=12345, proc_start='synthetic-bridge', generation='b' * 32),
            notifier=dict(pid=12346, proc_start='synthetic-notifier', generation='c' * 32)))
        self.records.publish(owner)
        with patch.object(service, 'alive_state', return_value='alive'), \
                patch.object(service, 'control_exchange', side_effect=OSError('synthetic unreachable')):
            observed = service.status(self.selection)
        self.assertEqual(observed['status'], 'unavailable')
        self.assertEqual(observed['basis'], 'incomplete_observation')

    def test_old_configuration_owner_is_not_current_selection_readiness(self):
        owner = self.records.new_owner()
        owner['configuration'] = 'f' * 64
        durable_state.publish(self.records.owner_path, owner)
        with self.assertRaises(StateError):
            service.status(self.selection)

    def test_real_saved_selection_runs_pair_and_reports_guarded_shutdown(self):
        for filename in FILES:
            target = self.prefix / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / filename, target)
        isolate_account_home(self.prefix, self.root / 'account')
        record, selection = self.record, self.selection
        log = (self.root / 'session.log').open('w')
        self.addCleanup(log.close)
        child = subprocess.Popen(platform_support.session_service_command(record), stdout=log, stderr=log,
                                 env=dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude')))
        def cleanup():
            if child.poll() is None:
                child.terminate()
            # Allow two sequential 20s shutdown stages, plus process overhead.
            child.wait(timeout=waiting.timeout(45))
        self.addCleanup(cleanup)
        observed = None
        def running():
            nonlocal observed
            observed = service.status(selection)
            return observed if observed['status'] == 'running' else False
        observed = waiting.wait_until_sync(running, 'session service running', process=child,
                                          observe=lambda: {'status': observed,
                                              'log': (self.root / 'session.log').read_text()})
        self.assertEqual(observed['basis'], 'live_pair_identity')
        captured = selection.records.request_stop()
        # Allow two sequential 20s shutdown stages, plus process overhead.
        self.assertEqual(child.wait(timeout=waiting.timeout(45)), 0, (self.root / 'session.log').read_text())
        selection.records.wait_stopped(captured, timeout=0)
        observed = service.status(selection)
        self.assertEqual(observed['status'], 'stopped')
        self.assertEqual(observed['basis'], 'kernel_process_start')
        self.assertTrue((self.home / 'inbox.sqlite3').exists())
        self.assertEqual(artifacts.load(self.home), record)

    def test_cli_names_missing_or_changed_owned_artifact(self):
        artifact = Path(self.record['artifact'])
        original = artifact.read_bytes()
        for missing in (True, False):
            with self.subTest(missing=missing):
                if missing:
                    artifact.unlink()
                else:
                    artifact.write_bytes(b'changed artifact')
                    artifact.chmod(0o600)
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    result = service.main(['status', '--prefix', str(self.prefix),
                                           '--state-dir', str(self.home), '--backend', 'systemd'])
                self.assertEqual(result, 78)
                failure = json.loads(output.getvalue())
                self.assertIn(str(artifact), failure['paths'])
                self.assertIn(str(self.home / 'native-service.json'), failure['paths'])
                artifact.write_bytes(original)
                artifact.chmod(0o600)
