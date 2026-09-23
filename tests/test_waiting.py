import asyncio
import os
import subprocess
import sys
import time
import unittest
from unittest import mock

import waiting


class ScaleTests(unittest.TestCase):
    def test_unset_scale_is_one(self):
        self.assertEqual(waiting.read_scale(None), 1)
        self.assertEqual(waiting.read_scale(''), 1)

    def test_scale_multiplies_default_and_explicit_budgets(self):
        with mock.patch.object(waiting, 'SCALE', 3.0):
            self.assertEqual(waiting.timeout(), 90)
            self.assertEqual(waiting.timeout(40), 120)

    def test_invalid_scale_names_the_variable(self):
        for value in ('fast', '0.5', '0', '-2', 'nan', 'inf', '1e309'):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, waiting.SCALE_VARIABLE):
                waiting.read_scale(value)

    def test_invalid_scale_fails_at_import(self):
        env = dict(os.environ, **{waiting.SCALE_VARIABLE: 'fast'})
        result = subprocess.run([sys.executable, '-c', 'import waiting'], cwd=os.path.dirname(waiting.__file__),
                                env=env, capture_output=True, text=True, timeout=waiting.timeout())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(waiting.SCALE_VARIABLE, result.stderr)


class AsyncWaitTests(unittest.IsolatedAsyncioTestCase):
    async def test_returns_the_condition_value_as_soon_as_it_holds(self):
        values = iter([0, 0, 'ready'])
        started = time.monotonic()
        self.assertEqual(await waiting.wait_until(lambda: next(values), 'readiness'), 'ready')
        self.assertLess(time.monotonic() - started, 1)

    async def test_timeout_names_the_condition_and_last_observed_state(self):
        with self.assertRaises(AssertionError) as caught:
            await waiting.wait_until(lambda: False, 'the notifier to publish', seconds=.05,
                                     observe=lambda: {'state': 'degraded'})
        message = str(caught.exception)
        self.assertIn('the notifier to publish', message)
        self.assertIn("'degraded'", message)

    async def test_a_task_that_already_failed_is_reported_not_timed_out(self):
        async def fail():
            raise RuntimeError('service stopped: synthetic fault')
        task = asyncio.create_task(fail())
        await asyncio.sleep(0)
        started = time.monotonic()
        with self.assertRaisesRegex(RuntimeError, 'synthetic fault'):
            await waiting.wait_until(lambda: False, 'readiness', task=task)
        self.assertLess(time.monotonic() - started, 1)

    async def test_a_task_that_fails_during_the_wait_is_reported(self):
        async def fail_later():
            await asyncio.sleep(.05)
            raise RuntimeError('synthetic late fault')
        task = asyncio.create_task(fail_later())
        with self.assertRaisesRegex(RuntimeError, 'synthetic late fault'):
            await waiting.wait_until(lambda: False, 'readiness', task=task)

    async def test_condition_true_at_the_deadline_succeeds(self):
        loop = asyncio.get_running_loop()
        ready_at = loop.time() + .05
        self.assertTrue(await waiting.wait_until(lambda: loop.time() >= ready_at, 'late condition',
                                                 seconds=.05, interval=.1))

    async def test_patched_asyncio_timeout_does_not_change_the_budget(self):
        with mock.patch.object(asyncio, 'timeout', side_effect=AssertionError('patched')):
            self.assertTrue(await waiting.wait_until(lambda: True, 'ready'))

    async def test_settle_returns_the_result_and_bounds_a_stalled_awaitable(self):
        async def value():
            return 7
        self.assertEqual(await waiting.settle(value(), 'the value'), 7)
        stalled = asyncio.Event()
        with self.assertRaisesRegex(AssertionError, 'worker shutdown'):
            await waiting.settle(stalled.wait(), 'worker shutdown', seconds=.05)


class SyncWaitTests(unittest.TestCase):
    def test_expected_process_exit_is_success(self):
        child = subprocess.Popen([sys.executable, '-c', 'pass'])
        self.addCleanup(child.wait)
        self.assertEqual(waiting.wait_until_sync(lambda: child.poll() is not None and 'exited',
                                                 'the child to exit', process=child), 'exited')

    def test_a_process_that_already_failed_is_reported_not_timed_out(self):
        child = subprocess.Popen([sys.executable, '-c', 'raise SystemExit(4)'])
        self.addCleanup(child.wait)
        started = time.monotonic()
        with self.assertRaisesRegex(AssertionError, 'status 4.*readiness file'):
            waiting.wait_until_sync(lambda: False, 'readiness file', process=child)
        self.assertLess(time.monotonic() - started, waiting.timeout(5))

    def test_timeout_names_the_condition_and_last_observed_state(self):
        with self.assertRaises(AssertionError) as caught:
            waiting.wait_until_sync(lambda: False, 'session failed to start', seconds=.05,
                                    observe=lambda: 'bridge: starting')
        self.assertIn('session failed to start', str(caught.exception))
        self.assertIn('bridge: starting', str(caught.exception))


if __name__ == '__main__':
    unittest.main()
