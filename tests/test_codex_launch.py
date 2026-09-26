"""codex_launch.py: a Codex CLI that names its own process to every command of its session (#160)."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import codex_launch
import waiting
from koinon import tmux_terminal


class ArgumentTests(unittest.TestCase):
    def test_leading_options_are_the_launcher_s_and_the_rest_reaches_codex(self):
        self.assertEqual(codex_launch.split(['resume', '--last']), ({}, ['resume', '--last']))
        self.assertEqual(codex_launch.split(['--tmux-session', 'peer', '--directory', '/d', '--', '-m', 'x']),
                         ({'--tmux-session': 'peer', '--directory': '/d'}, ['-m', 'x']))
        # Only leading options are the launcher's; a later one belongs to Codex.
        self.assertEqual(codex_launch.split(['resume', '--directory', '/d']), ({}, ['resume', '--directory', '/d']))
        with self.assertRaises(ValueError):
            codex_launch.split(['--tmux-session'])

    def test_the_command_names_the_cli_process_through_the_shell_policy(self):
        self.assertEqual(codex_launch.command('/synthetic/codex', 42, ['resume', 'x']),
                         ['/synthetic/codex', '-c', 'shell_environment_policy.set.KOINON_CODEX_HOST="42"',
                          'resume', 'x'])


class TmuxStartTests(unittest.TestCase):
    """The real launcher on a private tmux server, with a synthetic `codex` on PATH."""

    def setUp(self):
        if shutil.which('tmux') is None:
            if os.environ.get('CI'):
                self.fail('tmux must be installed in CI')
            self.skipTest('tmux is not installed')
        # A short directory keeps the socket under the AF_UNIX path limit on macOS.
        directory = Path(tempfile.mkdtemp(prefix='kl', dir='/tmp'))
        self.addCleanup(shutil.rmtree, directory, True)
        self.socket = str(directory / 's')
        self.addCleanup(subprocess.run, ['tmux', '-S', self.socket, 'kill-server'], capture_output=True, timeout=10)
        self.out = directory / 'argv'
        bin_dir = directory / 'bin'
        bin_dir.mkdir()
        fake = bin_dir / 'codex'
        fake.write_text(f'#!/bin/sh\necho "$$ $*" > {self.out}\nexec sleep 60\n')
        fake.chmod(0o700)
        self.work = directory / 'work'
        self.work.mkdir()
        environment = {key: value for key, value in os.environ.items() if key not in ('TMUX', 'TMUX_PANE')}
        environment['PATH'] = f'{bin_dir}{os.pathsep}{environment.get("PATH", "")}'
        for patcher in (patch.dict(os.environ, environment, clear=True),
                        patch.object(tmux_terminal, 'default_socket', return_value=self.socket)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def test_a_new_session_runs_codex_with_its_own_pid(self):
        started = codex_launch.start_in_tmux('peer', self.work, ['resume', 'synthetic-thread'])
        self.assertTrue(started['ok'], started)
        self.assertEqual(started['session'], 'peer')
        waiting.wait_until_sync(self.out.exists, 'the synthetic codex', seconds=30)
        pid, *argv = self.out.read_text().split()
        pane_pid = subprocess.run(['tmux', '-S', self.socket, 'display-message', '-p', '-t', started['pane_id'],
                                   '#{pane_pid}\t#{pane_current_path}'],
                                  capture_output=True, text=True, timeout=10).stdout.strip().split('\t')
        # exec keeps the pane's process: the launcher, then Codex, have one pid.
        self.assertEqual(pid, pane_pid[0])
        self.assertEqual(Path(pane_pid[1]).resolve(), self.work.resolve())
        self.assertEqual(argv, ['-c', f'shell_environment_policy.set.KOINON_CODEX_HOST="{pid}"',
                                'resume', 'synthetic-thread'])
        self.assertEqual(codex_launch.start_in_tmux('peer', self.work, []),
                         dict(ok=False, code='name_taken', session='peer'))


if __name__ == '__main__':
    unittest.main()
