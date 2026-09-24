"""Host process and tmux pane records of a Codex `ensure` (stable-alias chunk 01)."""
from contextlib import redirect_stdout
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import session
from koinon import durable_state, platform_support, tmux_terminal
from scripts.install import FILES
from repo_root import ROOT

CLI = '/synthetic/bin/codex'


def tree(parents, argvs):
    """Patch process_command with a synthetic process table."""
    def command(pid):
        if pid not in parents:
            raise ProcessLookupError(pid)
        return parents[pid], argvs.get(pid, ['/bin/sh'])
    return patch.object(platform_support, 'process_command', side_effect=command)


class HostMatcherTests(unittest.TestCase):
    def setUp(self):
        which = patch('shutil.which', side_effect=lambda value: value)
        which.start()
        self.addCleanup(which.stop)
        resolve = patch.object(Path, 'resolve', lambda path, strict=False: path)
        resolve.start()
        self.addCleanup(resolve.stop)

    def test_shell_child_of_cli_is_not_the_host(self):
        # A shell whose parent names the CLI: codex_process accepts it, codex_host must not.
        with tree({123: 456, 456: 1}, {123: ['bash'], 456: [CLI]}):
            self.assertTrue(platform_support.codex_process(123, CLI))
            self.assertFalse(platform_support.codex_host(123, CLI))
            self.assertTrue(platform_support.codex_host(456, CLI))
            found = platform_support.ancestor_matching(
                123, lambda pid: platform_support.codex_host(pid, CLI))
            self.assertEqual(found, 456)

    def test_launcher_with_native_child_selects_the_child(self):
        # node launcher (400) -> native CLI (500) -> shell (600) -> ensure.
        argvs = {400: ['node', CLI], 500: [CLI, 'resume'], 600: ['bash']}
        with tree({600: 500, 500: 400, 400: 1}, argvs):
            found = platform_support.ancestor_matching(
                600, lambda pid: platform_support.codex_host(pid, CLI))
            self.assertEqual(found, 500)

    def test_prompt_arguments_are_never_searched(self):
        with tree({700: 1}, {700: ['/bin/editor', CLI]}):
            self.assertFalse(platform_support.codex_host(700, CLI))

    def test_walk_is_bounded_and_stops_at_cycles_and_gaps(self):
        with tree({10: 11, 11: 10}, {}):
            self.assertIsNone(platform_support.ancestor_matching(10, lambda pid: False))
        with tree({10: 99}, {}):
            self.assertIsNone(platform_support.ancestor_matching(10, lambda pid: False))
        chain = {pid: pid + 1 for pid in range(2, 200)}
        with tree(chain, {}):
            self.assertIsNone(platform_support.ancestor_matching(2, lambda pid: pid == 150, limit=64))
            self.assertEqual(platform_support.ancestor_matching(2, lambda pid: pid == 50, limit=64), 50)


class ObservationTests(unittest.TestCase):
    def test_host_found_and_not_found(self):
        with patch.object(platform_support, 'ancestor_matching', return_value=os.getpid()):
            host = tmux_terminal.observe_host(CLI, start=os.getpid())
        self.assertEqual((host['state'], host['pid']), ('observed', os.getpid()))
        self.assertEqual(host['proc_start'], platform_support.proc_start(os.getpid()))
        with patch.object(platform_support, 'ancestor_matching', return_value=None):
            host = tmux_terminal.observe_host(CLI, start=os.getpid())
        self.assertEqual((host['state'], host['reason']), ('unknown', 'host_not_found'))

    def test_terminal_outside_tmux_and_without_tmux(self):
        host = dict(state='observed', pid=os.getpid())
        self.assertEqual(tmux_terminal.observe_terminal(host, {})['reason'], 'not_in_tmux')
        self.assertEqual(tmux_terminal.observe_terminal(host, {'TMUX': '/s,1,0'})['reason'], 'not_in_tmux')
        with patch('shutil.which', return_value=None):
            observed = tmux_terminal.observe_terminal(host, {'TMUX': '/s,1,0', 'TMUX_PANE': '%0'})
        self.assertEqual(observed['reason'], 'tmux_unavailable')

    def test_report_marks_missing_records_and_host_liveness(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            self.assertEqual(tmux_terminal.report(state),
                             dict(host=dict(state='unknown', reason='not_recorded'),
                                  terminal=dict(state='unknown', reason='not_recorded')))
            live = dict(state='observed', pid=os.getpid(),
                        proc_start=platform_support.proc_start(os.getpid()), observed_at_ms=1)
            durable_state.publish(state / 'host.json', live)
            self.assertTrue(tmux_terminal.report(state)['host']['live'])
            durable_state.publish(state / 'host.json', dict(live, proc_start='another start'))
            self.assertFalse(tmux_terminal.report(state)['host']['live'])


class PrivateTmuxTests(unittest.TestCase):
    """A private tmux server; never the user's."""

    def setUp(self):
        if shutil.which('tmux') is None:
            if os.environ.get('CI'):
                self.fail('tmux must be installed in CI')
            self.skipTest('tmux is not installed')
        # A short directory keeps the socket under the AF_UNIX path limit on macOS.
        directory = tempfile.mkdtemp(prefix='kt', dir='/tmp')
        self.addCleanup(shutil.rmtree, directory, True)
        self.socket = str(Path(directory) / 's')
        environment = {key: value for key, value in os.environ.items() if key not in ('TMUX', 'TMUX_PANE')}
        subprocess.run(['tmux', '-S', self.socket, '-f', '/dev/null', 'new-session', '-d', '-s', 'agent',
                        sys.executable, '-c', 'import time; time.sleep(60)'],
                       check=True, env=environment, timeout=10)
        self.addCleanup(subprocess.run, ['tmux', '-S', self.socket, 'kill-server'],
                        capture_output=True, timeout=10)
        fields = subprocess.run(['tmux', '-S', self.socket, 'display-message', '-p', '-t', 'agent',
                                 '#{pane_id}\t#{pane_pid}\t#{session_id}'],
                                capture_output=True, text=True, check=True, timeout=10).stdout.split()
        self.pane, self.pane_pid, self.session_id = fields[0], int(fields[1]), fields[2]
        self.environ = {'TMUX': f'{self.socket},1,0', 'TMUX_PANE': self.pane}

    def test_pane_that_contains_the_host_is_recorded(self):
        host = dict(state='observed', pid=self.pane_pid)
        observed = tmux_terminal.observe_terminal(host, self.environ)
        self.assertEqual({key: observed[key] for key in ('state', 'socket', 'pane_id', 'session_id')},
                         dict(state='observed', socket=self.socket, pane_id=self.pane,
                              session_id=self.session_id))

    def test_pane_without_the_host_is_refused(self):
        host = dict(state='observed', pid=os.getpid())
        self.assertEqual(tmux_terminal.observe_terminal(host, self.environ)['reason'], 'pane_not_host')

    def test_unknown_host_and_unreadable_server(self):
        unknown = dict(state='unknown', reason='host_not_found')
        self.assertEqual(tmux_terminal.observe_terminal(unknown, self.environ)['reason'], 'host_not_found')
        missing = dict(self.environ, TMUX=self.socket + '-missing,1,0')
        host = dict(state='observed', pid=self.pane_pid)
        self.assertEqual(tmux_terminal.observe_terminal(host, missing)['reason'], 'tmux_unreadable')


class TerminalNameTests(unittest.TestCase):
    """Naming on a private tmux server; never the user's."""

    AGENT = [sys.executable, '-c', 'import time; time.sleep(60)']
    OTHER = ['sleep', '60']

    def setUp(self):
        if shutil.which('tmux') is None:
            if os.environ.get('CI'):
                self.fail('tmux must be installed in CI')
            self.skipTest('tmux is not installed')
        directory = tempfile.mkdtemp(prefix='kn', dir='/tmp')
        self.addCleanup(shutil.rmtree, directory, True)
        self.socket = str(Path(directory) / 's')
        self.environment = {key: value for key, value in os.environ.items() if key not in ('TMUX', 'TMUX_PANE')}
        self.addCleanup(subprocess.run, ['tmux', '-S', self.socket, 'kill-server'], capture_output=True, timeout=10)

    def tmux(self, *arguments):
        return subprocess.run(['tmux', '-S', self.socket, *arguments], capture_output=True, text=True,
                              env=self.environment, timeout=10).stdout.strip()

    def session(self, name, command):
        self.tmux('-f', '/dev/null', 'new-session', '-d', '-s', name, *command)
        pane, session_id = self.tmux('display-message', '-p', '-t', name, '#{pane_id}\t#{session_id}').split('\t')
        return dict(state='observed', socket=self.socket, pane_id=pane, session_id=session_id)

    def name(self, terminal, target, claude=()):
        return tmux_terminal.name_terminal(terminal, target, sys.executable, set(claude))

    def test_single_agent_pane_renames_its_session(self):
        terminal = self.session('agent', self.AGENT)
        self.assertEqual(self.name(terminal, 'codex-koinon'), dict(result='renamed', name='codex-koinon'))
        self.assertEqual(self.tmux('display-message', '-p', '-t', terminal['pane_id'], '#{session_name}'),
                         'codex-koinon')
        self.assertEqual(self.name(terminal, 'codex-koinon')['result'], 'unchanged')

    def test_a_non_agent_second_pane_does_not_block_the_rename(self):
        terminal = self.session('agent', self.AGENT)
        self.tmux('split-window', '-t', terminal['pane_id'], *self.OTHER)
        self.assertEqual(self.name(terminal, 'codex-koinon')['result'], 'renamed')

    def test_shared_session_titles_only_the_own_pane(self):
        terminal = self.session('shared', self.AGENT)
        self.tmux('split-window', '-t', terminal['pane_id'], *self.OTHER)
        other = [pane for pane in self.tmux('list-panes', '-t', 'shared', '-F', '#{pane_id}\t#{pane_pid}').splitlines()
                 if not pane.startswith(terminal['pane_id'] + '\t')][0].split('\t')
        # The second pane holds a live Claude registry process.
        result = self.name(terminal, 'codex-koinon', claude=[int(other[1])])
        self.assertEqual(result, dict(result='pane_titled', name='codex-koinon', pane_id=terminal['pane_id']))
        self.assertEqual(self.tmux('display-message', '-p', '-t', terminal['pane_id'], '#{session_name}'), 'shared')
        self.assertEqual(self.tmux('display-message', '-p', '-t', terminal['pane_id'], '#{pane_title}'), 'codex-koinon')
        self.assertNotEqual(self.tmux('display-message', '-p', '-t', other[0], '#{pane_title}'), 'codex-koinon')

    def test_a_taken_name_renames_nothing(self):
        terminal = self.session('agent', self.AGENT)
        self.session('codex-koinon', self.OTHER)
        self.assertEqual(self.name(terminal, 'codex-koinon'), dict(result='name_taken', name='codex-koinon'))
        self.assertEqual(self.tmux('display-message', '-p', '-t', terminal['pane_id'], '#{session_name}'), 'agent')

    def test_an_incomplete_process_scan_renames_nothing(self):
        terminal = self.session('agent', self.AGENT)
        self.tmux('split-window', '-t', terminal['pane_id'], *self.AGENT)
        with patch.object(platform_support, 'process_parents', return_value=None):
            self.assertEqual(self.name(terminal, 'codex-koinon'), dict(result='panes_unknown'))
        self.assertEqual(self.tmux('display-message', '-p', '-t', terminal['pane_id'], '#{session_name}'), 'agent')

    def test_no_observed_terminal_is_a_no_op(self):
        for record in (dict(state='unavailable', reason='not_in_tmux'),
                       dict(state='unavailable', reason='tmux_unavailable'),
                       dict(state='unknown', reason='not_recorded')):
            self.assertEqual(self.name(record, 'codex-koinon'), dict(result=record['reason']))


class ProcessSnapshotTests(unittest.TestCase):
    def test_a_failed_or_malformed_ps_is_unavailable(self):
        with patch.object(platform_support, 'LINUX', False):
            with patch.object(platform_support.subprocess, 'run', side_effect=OSError):
                self.assertIsNone(platform_support.process_parents())
            malformed = subprocess.CompletedProcess([], 0, stdout='12 1 501\ngarbage\n')
            with patch.object(platform_support.subprocess, 'run', return_value=malformed):
                self.assertIsNone(platform_support.process_parents())
            good = subprocess.CompletedProcess([], 0, stdout=f'12 1 {os.geteuid()}\n13 12 {os.geteuid()}\n14 1 0\n')
            with patch.object(platform_support.subprocess, 'run', return_value=good):
                self.assertEqual(platform_support.process_parents(), {12: 1, 13: 12})
        with patch.object(platform_support, 'process_parents', return_value=None), self.assertRaises(OSError):
            platform_support.descendants(1)

    def test_this_process_is_in_the_snapshot(self):
        parents = platform_support.process_parents()
        self.assertEqual(parents.get(os.getpid()), os.getppid())
        self.assertIn(os.getpid(), platform_support.descendants(os.getppid(), parents))


class FreshTerminalTests(unittest.TestCase):
    def test_a_saved_record_never_directs_a_rename(self):
        with patch.object(tmux_terminal, 'name_terminal') as naming:
            result = session.name_own_terminal(Path('/unused'), dict(codex=CLI), 'codex-koinon-0a',
                                               dict(name='codex-koinon', held=True), None)
        self.assertEqual(result, dict(result='terminal_refresh_failed'))
        naming.assert_not_called()

    def test_record_attachment_returns_the_fresh_observation_or_none(self):
        fresh = dict(state='observed', socket='/s', pane_id='%1', session_id='$1')
        with patch.object(tmux_terminal, 'record', return_value=dict(host={}, terminal=fresh)):
            self.assertEqual(session.record_attachment(Path('/unused'), dict(codex=CLI)), fresh)
        with patch.object(tmux_terminal, 'record', side_effect=OSError('publish failed')):
            self.assertIsNone(session.record_attachment(Path('/unused'), dict(codex=CLI)))


class EnsureRecordsTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        for name in FILES:
            if name.endswith('.py'):
                target = self.prefix / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
                target.chmod(0o600)
        self.env = dict(os.environ, CODEX_HOME=str(self.root / 'codex'),
                        DSH_HOME=str(self.root / 'deepseek'), CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        for key in ('CODEX_THREAD_ID', 'DSH_SESSION_ID', 'TMUX', 'TMUX_PANE'):
            self.env.pop(key, None)

    def invoke(self, backend, action, agent, thread):
        config = dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'),
                      codex=sys.executable, dsh_url='http://127.0.0.1:9999',
                      dsh_credentials=str(self.root / 'deepseek' / 'credentials.json'))
        if backend is not None:
            config['session_backend'] = backend
        durable_state.publish(self.prefix / 'install.json', config)
        output = io.StringIO()
        args = ['session.py', action, '--agent', agent, '--thread', thread, '--repo', str(self.root / 'repo')]
        with patch.object(session, '__file__', str(self.prefix / 'session.py')), \
                patch.object(sys, 'argv', args), patch.dict(os.environ, self.env, clear=True), \
                patch.object(session, 'peers', return_value=[]), \
                patch.object(platform_support, 'codex_host', return_value=False), \
                patch.object(platform_support, 'session_manager_observation', return_value={'status': 'unknown'}), \
                patch.object(platform_support, 'memory_manager_available', return_value=False), \
                patch.object(platform_support, 'user_service_manager', return_value=subprocess.CompletedProcess([], 1)), \
                redirect_stdout(output):
            try:
                session.main()
            except SystemExit:
                pass
        state, _, _ = session.details(self.prefix, config, thread, str(self.root / 'repo'), agent)
        return json.loads(output.getvalue()), state

    def test_codex_ensure_records_and_reports_both(self):
        for backend in (None, 'manual', 'systemd', 'launchd'):
            with self.subTest(backend=backend):
                thread = f'synthetic-{backend}'
                ensured, state = self.invoke(backend, 'ensure', 'codex', thread)
                self.assertEqual(durable_state.read(state / 'host.json')['reason'], 'host_not_found')
                self.assertEqual(durable_state.read(state / 'terminal.json')['reason'], 'not_in_tmux')
                self.assertEqual(ensured['host']['reason'], 'host_not_found')
                self.assertEqual(ensured['terminal']['reason'], 'not_in_tmux')
                if backend != 'manual':
                    self.assertEqual(ensured.get('tmux', dict(result='not_in_tmux'))['result'], 'not_in_tmux')
                reported, _ = self.invoke(backend, 'status', 'codex', thread)
                self.assertEqual(reported['terminal']['reason'], 'not_in_tmux')
                for path in (state / 'host.json', state / 'terminal.json'):
                    self.assertEqual(path.stat().st_mode & 0o077, 0)
                    text = path.read_text()
                    self.assertNotIn(thread, text)

    def test_deepseek_ensure_records_nothing(self):
        ensured, state = self.invoke(None, 'ensure', 'deepseek', 'synthetic-deepseek')
        self.assertFalse((state / 'host.json').exists())
        self.assertFalse((state / 'terminal.json').exists())
        self.assertNotIn('host', ensured)

    def test_status_before_any_record_reports_not_recorded(self):
        _, state = self.invoke(None, 'ensure', 'codex', 'synthetic-old')
        (state / 'host.json').unlink()
        (state / 'terminal.json').unlink()
        reported, _ = self.invoke(None, 'status', 'codex', 'synthetic-old')
        self.assertEqual(reported['host'], dict(state='unknown', reason='not_recorded'))


if __name__ == '__main__':
    unittest.main()
