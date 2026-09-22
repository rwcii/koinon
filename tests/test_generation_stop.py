import ast
from pathlib import Path
import unittest
from unittest.mock import AsyncMock, patch

import generation_stop as guard


class StopProtocolTests(unittest.TestCase):
    def test_closed_codes_and_exact_versioned_request(self):
        self.assertEqual(guard.CODES, {'invalid_request', 'not_this_instance',
                                     'guarded_stop_unsupported', 'guarded_stop_unconfirmed'})
        with self.assertRaises(ValueError):
            guard.StopError('typo')
        tree = ast.parse(Path(guard.__file__).read_text())
        codes = {node.args[0].value for node in ast.walk(tree)
                 if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                 and node.func.id == 'StopError' and isinstance(node.args[0], ast.Constant)}
        self.assertEqual(codes, guard.CODES)
        valid = dict(op=guard.OPERATION, protocol=1, generation='a' * 32)
        guard.validate(valid, 'a' * 32)
        for request in (dict(valid, protocol=True), dict(valid, protocol=2),
                        dict(valid, extra=True), dict(valid, generation='A' * 32),
                        dict(op=guard.OPERATION, generation='a' * 32), None):
            with self.subTest(request=request), self.assertRaises(guard.StopError) as caught:
                guard.validate(request, 'a' * 32)
            self.assertEqual(caught.exception.code, 'invalid_request')
        with self.assertRaises(guard.StopError) as caught:
            guard.validate(valid, 'b' * 32)
        self.assertEqual(caught.exception.code, 'not_this_instance')


class StopClientTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        patcher = patch.object(guard.platform_support, 'process_state', return_value='alive')
        self.process = patcher.start()
        self.addCleanup(patcher.stop)

    def status(self, **changes):
        value = dict(pid=123, generation='a' * 32, control_capabilities=[guard.CAPABILITY])
        value.update(changes)
        return dict(ok=True, result=value), 123

    async def test_absent_capability_or_changed_identity_never_sends_stop(self):
        cases = [(self.status(control_capabilities=[]), 'guarded_stop_unsupported'),
                 (self.status(control_capabilities=guard.CAPABILITY), 'guarded_stop_unsupported'),
                 (self.status(generation='b' * 32), 'not_this_instance'),
                 (self.status(pid=124), 'not_this_instance'),
                 ((self.status()[0], 124), 'not_this_instance')]
        for status, code in cases:
            with self.subTest(code=code, status=status), patch.object(
                    guard, 'control_exchange', new=AsyncMock(return_value=status)) as exchange:
                with self.assertRaises(guard.StopError) as caught:
                    await guard.request_stop('/synthetic', 'a' * 32, 123, 'synthetic-start')
                self.assertEqual(caught.exception.code, code)
                self.assertEqual(exchange.await_count, 1)
                self.assertEqual(exchange.await_args.args[1], dict(op='status'))

    async def test_reused_pid_before_send_never_receives_stop(self):
        self.process.side_effect = ['alive', 'dead']
        with patch.object(guard, 'control_exchange', new=AsyncMock(return_value=self.status())) as exchange:
            with self.assertRaises(guard.StopError) as caught:
                await guard.request_stop('/synthetic', 'a' * 32, 123, 'synthetic-start')
            self.assertEqual(caught.exception.code, 'not_this_instance')
            self.assertEqual(exchange.await_count, 1)

    async def test_old_replacement_rejects_new_opcode_and_never_gets_legacy_stop(self):
        calls = []
        stopped = False
        async def exchange(root, request, **kwargs):
            nonlocal stopped
            calls.append(request)
            if len(calls) == 1:
                return self.status()
            # The legacy bridge ignores unknown fields on stop, but rejects an
            # unknown operation. Model replacement after capability observation.
            if request['op'] == 'stop':
                stopped = True
                return dict(ok=True, result='stopping'), 124
            return dict(ok=False, code='rejected'), 124
        with patch.object(guard, 'control_exchange', side_effect=exchange):
            with self.assertRaises(guard.StopError) as caught:
                await guard.request_stop('/synthetic', 'a' * 32, 123, 'synthetic-start')
        self.assertEqual(caught.exception.code, 'guarded_stop_unconfirmed')
        self.assertFalse(stopped)
        self.assertEqual([r['op'] for r in calls], ['status', guard.OPERATION])

    async def test_exact_reply_and_lost_reply_have_no_fallback(self):
        result = dict(stopping=True, generation='a' * 32, protocol=1)
        responses = ((dict(ok=True, result=result), 123),
                     (dict(ok=True, result=dict(result, protocol=True)), 123),
                     (dict(ok=True, result=dict(result, stopping=1)), 123),
                     TimeoutError('lost reply'))
        for index, response in enumerate(responses):
            with self.subTest(response=response), patch.object(
                    guard, 'control_exchange', new=AsyncMock(side_effect=[self.status(), response])) as exchange:
                if index == 0:
                    self.assertEqual(await guard.request_stop('/synthetic', 'a' * 32, 123, 'synthetic-start'), result)
                else:
                    with self.assertRaises((guard.StopError, TimeoutError)):
                        await guard.request_stop('/synthetic', 'a' * 32, 123, 'synthetic-start')
                self.assertEqual(exchange.await_count, 2)
