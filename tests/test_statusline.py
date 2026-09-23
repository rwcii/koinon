import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import unittest

from koinon import participant_status as status
from koinon import platform_support
from repo_root import ROOT
import waiting

SESSION = 'synthetic-session'
SECRET = 'synthetic workspace text that must stay out of the record'


def payload(**changes):
    value = dict(session_id=SESSION, transcript_path=SECRET, session_name=SECRET,
                 model=dict(id='synthetic-model', display_name=SECRET),
                 workspace=dict(current_dir=SECRET, project_dir=SECRET),
                 worktree=dict(name=SECRET), pr=dict(url=SECRET),
                 context_window=dict(context_window_size=200000, total_input_tokens=50000,
                                     used_percentage=25, current_usage=dict(input_tokens=10)))
    value.update(changes)
    return json.dumps(value).encode()


class StatusLineTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.config = self.root / 'claude'
        self.registry = self.config / 'sessions'
        self.registry.mkdir(parents=True, mode=0o700)
        self.env = dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config), HOME=str(self.root))
        record = dict(pid=os.getpid(), procStart=platform_support.proc_start(os.getpid()),
                      sessionId=SESSION, entrypoint='cli')
        path = self.registry / f'{os.getpid()}.json'
        path.write_text(json.dumps(record))
        path.chmod(0o600)
        self.record = self.config / 'koinon-status' / f'claude-{SESSION}.json'

    def tearDown(self):
        self.temp.cleanup()

    def direct(self, command, data):
        return subprocess.run(['/bin/sh', '-c', command], input=data, capture_output=True,
                              env=self.env, timeout=waiting.timeout())

    def wrapped(self, command, data):
        argv = [sys.executable, str(ROOT / 'statusline.py')]
        if command is not None:
            argv += ['--command', command]
        return subprocess.run(argv, input=data, capture_output=True, env=self.env,
                              timeout=waiting.timeout())

    def assertSameResult(self, command, data):
        before = self.direct(command, data)
        after = self.wrapped(command, data)
        self.assertEqual((after.returncode, after.stdout, after.stderr),
                         (before.returncode, before.stdout, before.stderr))
        return after

    def test_input_output_and_status_are_unchanged(self):
        seen = self.root / 'seen'
        command = f'cat > {shlex.quote(str(seen))}; printf "%s\\n" status-line; exit 4'
        data = payload()
        self.direct(command, data)
        direct = seen.read_bytes()
        self.assertSameResult(command, data)
        self.assertEqual(seen.read_bytes(), direct)
        self.assertEqual(direct, data)

    def test_shell_expansion_pipes_and_quoting_keep_their_meaning(self):
        (self.root / 'line.sh').write_text('printf "%s|%s\\n" "$1" "$(cat | wc -c | tr -d " ")"\n')
        for command in ('bash $HOME/line.sh "two words"',
                        'cat | sh "$HOME/line.sh" \'single quoted\' | tr a-z A-Z',
                        'printf "%s\\n" "$HOME" >&2; cat >/dev/null'):
            with self.subTest(command):
                self.assertSameResult(command, payload())

    def test_a_failing_user_command_fails_the_same_way(self):
        result = self.assertSameResult('cat >/dev/null; exit 3', payload())
        self.assertEqual((result.returncode, result.stdout), (3, b''))

    def test_the_user_command_runs_when_recording_fails(self):
        (self.config / 'koinon-status').write_text('not a directory')
        for data in (payload(), b'{not json', b''):
            with self.subTest(data[:10]):
                result = self.assertSameResult('wc -c | tr -d " "', data)
                self.assertEqual(result.stdout.strip(), str(len(data)).encode())

    def test_oversized_input_is_forwarded_whole_and_not_parsed(self):
        data = payload(padding='x' * (2 * 1024 * 1024))
        result = self.assertSameResult('wc -c | tr -d " "', data)
        self.assertEqual(result.stdout.strip(), str(len(data)).encode())
        self.assertFalse(self.record.exists())

    def test_without_a_user_command_it_prints_nothing_and_records(self):
        result = self.wrapped(None, payload())
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, b'', b''))
        views = status.views(status.read('claude', SESSION, registry=self.registry))
        self.assertEqual(views['model']['id'], 'synthetic-model')
        self.assertEqual((views['context']['limit_tokens'], views['context']['used_tokens']), (200000, 50000))
        self.assertEqual(views['context']['fill'], 0.25)
        self.assertEqual(views['context']['source'], 'claude_statusline')

    def test_only_allowlisted_values_are_stored(self):
        self.wrapped(None, payload())
        self.assertNotIn(SECRET, self.record.read_text())

    def test_usage_before_the_first_response_is_unknown_not_zero(self):
        window = dict(context_window_size=200000, total_input_tokens=0, used_percentage=None,
                      current_usage=None)
        self.wrapped(None, payload(context_window=window))
        context = status.views(status.read('claude', SESSION, registry=self.registry))['context']
        self.assertEqual((context['state'], context['reason']), ('unknown', 'no_token_usage'))

    def test_a_session_absent_from_the_registry_is_not_recorded(self):
        self.wrapped(None, payload(session_id='another-session'))
        self.assertFalse((self.config / 'koinon-status' / 'claude-another-session.json').exists())

    def test_a_cancelled_wrapper_stops_the_user_command(self):
        marker = self.root / 'started'
        command = f'touch {shlex.quote(str(marker))}; exec sleep 60'
        process = subprocess.Popen([sys.executable, str(ROOT / 'statusline.py'), '--command', command],
                                   stdin=subprocess.PIPE, env=self.env)
        process.stdin.write(payload())
        process.stdin.close()
        waiting.wait_until_sync(marker.exists, 'the user command to start', process=process)
        process.send_signal(signal.SIGTERM)
        self.assertEqual(process.wait(timeout=waiting.timeout()), 128 + signal.SIGTERM)


class ClaudeProcessTests(unittest.TestCase):
    def test_registry_lookup_matches_only_a_cli_record_for_the_session(self):
        with tempfile.TemporaryDirectory() as temp:
            registry = Path(temp)
            for pid, fields in ((101, dict(sessionId='s-1', entrypoint='cli', procStart='7')),
                                (102, dict(sessionId='s-2', entrypoint='codex-peer-bridge', procStart='8'))):
                path = registry / f'{pid}.json'
                path.write_text(json.dumps(fields))
                path.chmod(0o600)
            self.assertEqual(status.claude_process('s-1', registry), dict(pid=101, proc_start='7'))
            self.assertIsNone(status.claude_process('s-2', registry))
            self.assertIsNone(status.claude_process('../s-1', registry))
