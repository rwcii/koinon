import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import time
import unittest

import run
import waiting

RUNNER = Path(__file__).resolve().parent / 'run.py'
BOUND = 2
# Interpreter start, test loading and the dump itself, beyond the bound.
MARGIN = 20

BLOCK = 'threading.Event().wait()'
SELECT = textwrap.dedent('''\
    read, write = os.pipe()
    select.select([read], [], [])
''')

# Each case: the synthetic module's body, and what the watchdog must name.
CASES = {
    'test body': ('''
        class Blocked(unittest.TestCase):
            def test_blocks(self):
                BLOCK
        ''', 'running test: synthetic_block.Blocked.test_blocks'),
    'setUp': ('''
        class Blocked(unittest.TestCase):
            def setUp(self):
                BLOCK
            def test_never_runs(self):
                pass
        ''', 'running test: synthetic_block.Blocked.test_never_runs'),
    'tearDown': ('''
        class Blocked(unittest.TestCase):
            def tearDown(self):
                BLOCK
            def test_passes(self):
                pass
        ''', 'running test: synthetic_block.Blocked.test_passes'),
    'setUpClass': ('''
        class Blocked(unittest.TestCase):
            @classmethod
            def setUpClass(cls):
                BLOCK
            def test_never_runs(self):
                pass
        ''', 'running fixture: synthetic_block.Blocked.setUpClass'),
    'tearDownClass': ('''
        class Blocked(unittest.TestCase):
            @classmethod
            def tearDownClass(cls):
                BLOCK
            def test_passes(self):
                pass
        ''', 'running fixture: synthetic_block.Blocked.tearDownClass'),
    'setUpModule': ('''
        def setUpModule():
            BLOCK
        class Blocked(unittest.TestCase):
            def test_never_runs(self):
                pass
        ''', 'running fixture: synthetic_block.setUpModule'),
    'tearDownModule': ('''
        def tearDownModule():
            BLOCK
        class Blocked(unittest.TestCase):
            def test_passes(self):
                pass
        ''', 'running fixture: synthetic_block.tearDownModule'),
    'select on an unwritten pipe': ('''
        class Blocked(unittest.TestCase):
            def test_waits_in_select(self):
                SELECT
        ''', 'running test: synthetic_block.Blocked.test_waits_in_select'),
    'stalled worker': ('''
        class Stalled(unittest.IsolatedAsyncioTestCase):
            async def asyncSetUp(self):
                self.worker = asyncio.create_task(asyncio.Event().wait())
            async def asyncTearDown(self):
                await asyncio.gather(self.worker)
            async def test_passes(self):
                pass
        ''', 'running test: synthetic_block.Stalled.test_passes'),
}


def module_source(body):
    body = textwrap.dedent(body)
    body = body.replace('BLOCK', BLOCK)
    body = body.replace('SELECT', textwrap.indent(SELECT, '        ').strip())
    return 'import asyncio, os, select, threading, unittest\n' + body


class WatchdogTests(unittest.TestCase):
    def test_a_run_without_progress_ends_bounded_and_names_what_was_running(self):
        env = dict(os.environ, KOINON_TEST_WATCHDOG_SECONDS=str(BOUND))
        env.pop(waiting.SCALE_VARIABLE, None)
        children = {}
        with tempfile.TemporaryDirectory() as tmp:
            for case, (body, _) in CASES.items():
                directory = Path(tmp) / case.replace(' ', '_')
                directory.mkdir()
                (directory / 'synthetic_block.py').write_text(module_source(body))
                children[case] = (time.monotonic(), subprocess.Popen(
                    [sys.executable, str(RUNNER), '-v', 'synthetic_block'], cwd=directory, env=env,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True))
            for case, (started, child) in children.items():
                with self.subTest(case=case):
                    try:
                        output, _ = child.communicate(timeout=BOUND + MARGIN)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.communicate()
                        self.fail(f'{case}: the run did not end within {BOUND + MARGIN}s')
                    elapsed = time.monotonic() - started
                    self.assertEqual(child.returncode, 3, output)
                    self.assertLess(elapsed, BOUND + MARGIN)
                    self.assertIn('WATCHDOG: no test progress', output)
                    self.assertIn(CASES[case][1], output)
                    self.assertIn('Thread 0x', output, 'every thread is dumped')
                    self.assertIn('synthetic_block.py', output, 'the stack reaches the blocked code')
                    if case == 'stalled worker':
                        self.assertIn('asyncio tasks of the main thread', output)
                        self.assertIn('in asyncTearDown', output)

    def test_invalid_bound_is_a_named_configuration_error(self):
        for value in ('soon', '0', '-5', 'nan', 'inf', '1e309'):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, run.WATCHDOG_VARIABLE):
                run.read_bound(value)
        self.assertEqual(run.read_bound('7.5'), 7.5)

    def test_a_healthy_run_is_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / 'synthetic_pass.py').write_text(
                'import unittest\nclass Passing(unittest.TestCase):\n    def test_passes(self):\n        pass\n')
            result = subprocess.run([sys.executable, str(RUNNER), 'synthetic_pass'], cwd=tmp,
                                    capture_output=True, text=True, timeout=waiting.timeout())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('OK', result.stderr)
        self.assertNotIn('WATCHDOG', result.stderr)


if __name__ == '__main__':
    unittest.main()
