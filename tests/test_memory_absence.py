"""Client probes must preserve the absence of an uninitialized service."""
import waiting
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import memory
from repo_root import ROOT


class MemoryAbsenceTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.repo = self.root / 'repo'
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)

    def cli(self, option, path, *command):
        return subprocess.run([sys.executable, str(ROOT / 'memory.py'),
                               '--repo-path', str(self.repo), option, str(path),
                               *command], capture_output=True, text=True, timeout=waiting.timeout())

    def assert_absent(self, option, result):
        if option == '--service-dir':
            self.assertEqual(json.loads(result.stdout)['code'], 'service_unavailable')
        else:
            self.assertIn('no memory service is running', result.stderr)

    def test_absent_clients_do_not_initialize_state(self):
        for option in ('--state-dir', '--service-dir'):
            for command in (('status',), ('recall', 'synthetic'), ('work', 'list'), ('stop',)):
                with self.subTest(option=option, command=command):
                    path = self.root / 'absent' / 'state'
                    result = self.cli(option, path, *command)
                    if command == ('stop',):
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertEqual(json.loads(result.stdout)['status'], 'not_running')
                    else:
                        self.assertNotEqual(result.returncode, 0)
                        self.assert_absent(option, result)
                    self.assertFalse((self.root / 'absent').exists())

    def test_existing_root_remains_empty(self):
        path = self.root / 'state'
        path.mkdir(mode=0o700)
        result = self.cli('--state-dir', path, 'status')
        self.assert_absent('--state-dir', result)
        self.assertEqual(list(path.iterdir()), [])

    def test_read_only_parent_is_not_written_by_a_probe(self):
        parent = self.root / 'read-only'
        parent.mkdir(mode=0o500)
        self.addCleanup(parent.chmod, 0o700)
        for option in ('--state-dir', '--service-dir'):
            result = self.cli(option, parent / 'absent', 'status')
            self.assert_absent(option, result)
            self.assertEqual(list(parent.iterdir()), [])

    def test_existing_unsafe_paths_are_still_refused(self):
        unsafe = self.root / 'unsafe'
        unsafe.mkdir(mode=0o700)
        unsafe.chmod(0o755)
        for option in ('--state-dir', '--service-dir'):
            result = self.cli(option, unsafe, 'status')
            self.assertEqual(json.loads(result.stdout)['code'], 'unsafe_state_directory')
            self.assertEqual(list(unsafe.iterdir()), [])
        link = self.root / 'link'
        link.symlink_to(unsafe, target_is_directory=True)
        result = self.cli('--service-dir', link, 'status')
        self.assertEqual(json.loads(result.stdout)['code'], 'unsafe_state_directory')

    def test_serve_still_initializes_private_directories(self):
        for option in ('--state-dir', '--service-dir'):
            path = self.root / option.removeprefix('--') / 'state'
            argv = ['memory.py', '--repo-path', str(self.repo), option, str(path), 'serve']
            with mock.patch.object(sys, 'argv', argv), \
                 mock.patch.object(memory, 'serve', return_value={'status': 'synthetic'}) as serve, \
                 mock.patch('builtins.print'):
                memory.cli_main()
            home = serve.call_args.args[0]
            self.assertTrue(home.is_dir())
            self.assertEqual(home.stat().st_mode & 0o777, 0o700)
            self.assertEqual(list(home.iterdir()), [])
