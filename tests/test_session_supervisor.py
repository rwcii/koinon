import waiting
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch

from scripts.install import FILES
from koinon import session_supervisor as supervisor
from koinon.session_supervisor_state import Records, StateError
from test_session import isolate_account_home
from repo_root import ROOT


class FailureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.records = Records(Path(self.temp.name), 'a' * 64, 'b' * 64)
        self.commands = dict(bridge=['synthetic-bridge'], notifier=['synthetic-notifier'])

    def test_original_permanent_failure_survives_unconfirmed_shutdown(self):
        runner = supervisor.Runner(self.records, self.commands, threading.Event())
        child = Mock()
        child.poll.return_value = child.returncode = 70
        with patch.object(supervisor.subprocess, 'Popen', return_value=child), \
                patch.object(runner, 'shutdown', side_effect=StateError('session_shutdown_unconfirmed')):
            with self.assertRaises(StateError) as caught:
                runner.attempt()
        self.assertEqual(caught.exception.code, 'session_shutdown_unconfirmed')
        self.assertEqual(caught.exception.primary_code, 'session_software_failure')

    def test_guard_refusal_never_falls_back_to_signal_or_force_kill(self):
        runner = supervisor.Runner(self.records, self.commands, threading.Event())
        child = Mock()
        child.poll.return_value = None
        child.wait.side_effect = subprocess.TimeoutExpired('synthetic-child', 1)
        runner.children['bridge'] = child
        runner.owner['children']['bridge'] = dict(pid=123, proc_start='synthetic-start', generation='c' * 32)
        with patch.object(supervisor.generation_stop, 'request_stop', side_effect=StateError('session_ownership_unknown')):
            with self.assertRaises(StateError) as caught:
                runner.shutdown()
        self.assertEqual(caught.exception.code, 'session_shutdown_unconfirmed')
        child.terminate.assert_not_called()
        child.kill.assert_not_called()

    def test_failed_identity_capture_retains_spawn_intent_until_direct_child_reaped(self):
        for reaped in (False, True):
            with self.subTest(reaped=reaped), tempfile.TemporaryDirectory() as directory:
                records = Records(Path(directory), 'a' * 64, 'b' * 64)
                child = Mock(pid=123)
                child.poll.return_value = None
                if not reaped:
                    child.wait.side_effect = subprocess.TimeoutExpired('synthetic-child', 1)
                with patch.object(supervisor.subprocess, 'Popen', return_value=child), \
                        patch.object(supervisor.platform_support, 'proc_start',
                                     side_effect=['synthetic-parent', OSError('identity unavailable')]):
                    self.assertEqual(supervisor.run(records, self.commands, 'manual'), 78)
                refusal = records.read(refusal=True)
                self.assertEqual(refusal['spawn_pending'], None if reaped else 'bridge')
                self.assertEqual(refusal['primary_code'], 'session_configuration_failure')
                with patch('koinon.session_supervisor_state.alive_state', return_value='dead'):
                    if reaped:
                        records.retry()
                    else:
                        with self.assertRaises(StateError):
                            records.retry()
                child.terminate.assert_called_once()
                child.kill.assert_not_called()

    def test_owner_permanent_failure_cannot_bypass_explicit_retry_when_marker_missing(self):
        failure = dict(self.records.new_owner(), phase='failed', exit_status=78,
                       primary_code='session_configuration_failure')
        self.records.publish(failure)
        with patch.object(supervisor, 'alive_state', return_value='dead'), \
                patch('koinon.session_supervisor_state.alive_state', return_value='dead'), \
                patch.object(supervisor.Runner, 'attempt') as attempt:
            self.assertEqual(supervisor.run(self.records, self.commands, 'manual'), 78)
            attempt.assert_not_called()
            with patch('koinon.session_supervisor_state.alive_state', return_value='dead'):
                self.records.retry()
            self.assertEqual(supervisor.run(self.records, self.commands, 'manual'), 0)
            attempt.assert_called_once()

    def test_spawn_never_runs_when_durable_intent_publication_fails(self):
        runner = supervisor.Runner(self.records, self.commands, threading.Event())
        publish = self.records.publish
        def fail_intent(value, **kwargs):
            if value['phase'] == 'starting' and value['spawn_pending'] is not None:
                raise OSError('intent sync failed')
            return publish(value, **kwargs)
        with patch.object(self.records, 'publish', side_effect=fail_intent), \
                patch.object(supervisor.subprocess, 'Popen') as spawn:
            with self.assertRaises(OSError):
                runner.attempt()
            spawn.assert_not_called()

    def test_launchd_permanent_exit_stays_nonrestarting_when_refusal_writes_fail(self):
        with patch.object(supervisor.Runner, 'attempt', side_effect=StateError('session_software_failure')), \
                patch.object(self.records, 'publish', side_effect=OSError('synthetic-full')):
            self.assertEqual(supervisor.run(self.records, self.commands, 'launchd'), 0)
        diagnostic = json.loads((self.records.directory / 'session-supervisor-diagnostic.json').read_text())
        self.assertEqual(diagnostic['exit_status'], 70)
        self.assertEqual(diagnostic['code'], 'session_software_failure')


class NativeChildrenTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.app = self.root / 'app'
        self.app.mkdir(mode=0o700)
        for filename in FILES:
            target = self.app / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / filename, target)
        isolate_account_home(self.app, self.root / 'account')
        self.home = self.root / 'state'
        self.home.mkdir(mode=0o700)
        self.records = Records(self.home, 'a' * 64, 'b' * 64)
        self.commands = dict(
            bridge=[sys.executable, str(self.app / 'bridge.py'), '--state-dir', str(self.home), 'serve'],
            notifier=[sys.executable, str(self.app / 'notify.py'), '--state-dir', str(self.home),
                      '--thread', 'synthetic-native-runner', '--name', 'synthetic-runner',
                      '--repo', str(self.root), '--codex', sys.executable])
        self.processes = []
        self.addCleanup(self.cleanup_children)

    def cleanup_children(self):
        for child in self.processes:
            if child.poll() is None:
                child.terminate()
            # Allow two sequential 20s shutdown stages, plus process overhead.
            child.wait(timeout=waiting.timeout(45))

    def start(self, commands=None):
        wrapper = self.app / 'synthetic-runner.py'
        wrapper.write_text('from koinon.session_supervisor import run\n'
                           'from koinon.session_supervisor_state import Records\n'
                           + 'raise SystemExit(run(Records(' + repr(str(self.home)) + ", 'a'*64, 'b'*64), "
                           + repr(commands or self.commands) + ", 'manual'))\n")
        log = (self.root / 'runner.log').open('w')
        self.addCleanup(log.close)
        child = subprocess.Popen([sys.executable, str(wrapper)], stdout=log, stderr=log,
                                 env=dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude')))
        self.processes.append(child)
        return child

    def wait_phase(self, child, phase):
        owner = None
        def reached():
            nonlocal owner
            owner = self.records.read()
            return owner if owner and owner['phase'] == phase else False
        return waiting.wait_until_sync(reached, f'supervisor phase {phase}', process=child,
                                       observe=lambda: {'owner': owner,
                                                        'log': (self.root / 'runner.log').read_text()})

    def test_real_pair_readiness_and_generation_stop_preserve_inbox(self):
        child = self.start()
        owner = self.wait_phase(child, 'running')
        self.assertEqual(set(owner['children']), {'bridge', 'notifier'})
        self.assertTrue(all(record['generation'] for record in owner['children'].values()))
        waiting.wait_until_sync((self.home / 'notify-journal.sqlite3').exists,
                               'notifier journal creation', process=child,
                               observe=lambda: (self.root / 'runner.log').read_text())
        captured = self.records.request_stop()
        # Allow two sequential 20s shutdown stages, plus process overhead.
        self.assertEqual(child.wait(timeout=waiting.timeout(45)), 0, (self.root / 'runner.log').read_text())
        self.assertEqual(self.records.wait_stopped(captured, timeout=0)['status'], 'stopped')
        self.assertTrue((self.home / 'inbox.sqlite3').exists())
        self.assertTrue((self.home / 'notify-journal.sqlite3').exists())
        self.assertFalse((self.home / 'notify-ready.json').exists())
        self.assertIsNone(self.records.read(refusal=True))

    def test_notifier_permanent_runtime_failure_stops_bridge_and_records_refusal(self):
        trigger = self.root / 'fail-notifier'
        module = self.app / 'koinon/notification_runtime.py'
        with module.open('a') as stream:
            stream.write("\n_original_run = Runtime.run\n"
                         "async def _synthetic_run(self):\n"
                         "    async def fail_when_requested():\n"
                         "        while not Path(" + repr(str(trigger)) + ").exists():\n"
                         "            await asyncio.sleep(.01)\n"
                         "        self.stop.set()\n"
                         "    trigger = asyncio.create_task(fail_when_requested())\n"
                         "    try:\n"
                         "        await _original_run(self)\n"
                         "    finally:\n"
                         "        trigger.cancel()\n"
                         "        await asyncio.gather(trigger, return_exceptions=True)\n"
                         "    return 70\n"
                         "Runtime.run = _synthetic_run\n")
        child = self.start()
        owner = self.wait_phase(child, 'running')
        trigger.touch()
        # Allow two sequential 20s shutdown stages, plus process overhead.
        self.assertEqual(child.wait(timeout=waiting.timeout(45)), 70, (self.root / 'runner.log').read_text())
        refusal = self.records.read(refusal=True)
        self.assertEqual(refusal['primary_code'], 'session_software_failure')
        self.assertIsNone(refusal['shutdown_code'])
        self.assertEqual(self.records.wait_stopped(owner, timeout=0)['status'], 'stopped')
        self.assertTrue((self.home / 'inbox.sqlite3').exists())
        self.records.retry()
        self.assertIsNone(self.records.read(refusal=True))

    def test_permanent_bridge_startup_failure_is_retained_until_explicit_retry(self):
        commands = copy.deepcopy(self.commands)
        commands['bridge'] = [sys.executable, '-c', 'raise SystemExit(78)']
        child = self.start(commands)
        self.assertEqual(child.wait(timeout=waiting.timeout()), 78, (self.root / 'runner.log').read_text())
        refusal = self.records.read(refusal=True)
        self.assertEqual(refusal['primary_code'], 'session_configuration_failure')
        self.assertIsNone(refusal['children']['notifier'])
        self.records.retry()
        self.assertIsNone(self.records.read(refusal=True))
