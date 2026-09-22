import argparse
import contextlib
import io
import json
import subprocess
import sys
from pathlib import Path
import tempfile
import time
import unittest
from unittest import mock

import memory
from koinon import work_items

# Cases taken from the documented command table, not generated from REQUIRED.
CASES = {
    'create': dict(title='Task', criteria='Review', non_goals='No deployment', key='create', deadline='123'),
    'propose': dict(if_revision='1', proposed_assignee='reviewer'),
    'edit': dict(if_revision='1'),
    'start': dict(if_revision='1', checkpoint='Saved', next_artifact='Patch', progress_deadline='123', key='start', deadline='123'),
    'update': dict(if_revision='1', claim_generation='1', progress='Working', checkpoint='Saved', next_artifact='Patch', progress_deadline='123'),
    'release': dict(if_revision='1', claim_generation='1', checkpoint='Saved'),
    'finish': dict(if_revision='1', claim_generation='1', outcome='withdrawn'),
    'renew': dict(claim_generation='1', if_claim_revision='1'),
}
INTEGERS = {'if_revision', 'claim_generation', 'if_claim_revision'}
TIMES = {'deadline', 'progress_deadline'}


class RequiredWorkArgumentsTests(unittest.TestCase):
    def parser(self):
        p = argparse.ArgumentParser()
        work_items.cli_parsers(p.add_subparsers(dest='op'))
        return p

    def argv(self, name, fields):
        args = ['claim' if name == 'renew' else 'work', name]
        if name != 'create':
            args.append('0' * 32)
        for field, value in fields.items():
            args.extend(['--' + field.replace('_', '-'), value])
        return args

    def test_each_unconditional_cli_requirement_names_the_missing_flag(self):
        p = self.parser()
        for name, fields in CASES.items():
            for absent in fields:
                with self.subTest(command=name, absent=absent):
                    output = io.StringIO()
                    args = self.argv(name, {k: v for k, v in fields.items() if k != absent})
                    with contextlib.redirect_stdout(output), self.assertRaises(SystemExit) as caught:
                        p.parse_args(args)
                    self.assertEqual(caught.exception.code, 1)
                    self.assertIn('--' + absent.replace('_', '-'), output.getvalue())
                    self.assertIn('required', output.getvalue())
                    self.assertEqual(json.loads(output.getvalue())['code'], 'invalid_request')

    def test_missing_argument_cli_json_without_service_or_state_creation(self):
        with tempfile.TemporaryDirectory() as root:
            state = Path(root) / 'state'
            result = subprocess.run([sys.executable, 'memory.py', '--state-dir', str(state),
                                     '--consumer', 'synthetic', 'work', 'create',
                                     '--title', 'Task', '--criteria', 'Review',
                                     '--non-goals', 'No deployment', '--deadline', '123'],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stderr, '')
            refusal = json.loads(result.stdout)
            self.assertEqual(refusal['code'], 'invalid_request')
            self.assertFalse(refusal['ok'])
            self.assertIn('--key', refusal['error'])
            self.assertFalse(state.exists())

    def test_optional_replay_and_clear_proposal_remain_accepted(self):
        p = self.parser()
        for name in ('edit', 'update', 'release', 'finish', 'renew'):
            p.parse_args(self.argv(name, CASES[name]))
        args = self.argv('propose', {'if_revision': '1'}) + ['--clear-assignee']
        parsed = vars(p.parse_args(args))
        work_items.cli_request(parsed.pop('op'), parsed)
        self.assertIsNone(parsed['proposed_assignee'])
        with contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit):
            p.parse_args(args + ['--proposed-assignee', 'reviewer'])

    def test_clear_assignee_reaches_wire_as_explicit_null(self):
        with tempfile.TemporaryDirectory() as root:
            args = ['memory.py', '--state-dir', str(Path(root) / 'state'),
                    '--consumer', 'synthetic', 'work', 'propose', '0' * 32,
                    '--if-revision', '1', '--clear-assignee']
            with mock.patch.object(sys, 'argv', args), \
                 mock.patch.object(memory, 'repo_identity', return_value='0123456789abcdef'), \
                 mock.patch.object(memory, 'request', new_callable=mock.AsyncMock,
                                   return_value={'ok': True, 'result': {}}) as request, \
                 contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit_:
                memory.cli_main()
            self.assertEqual(exit_.exception.code, 0)
            payload = request.await_args.args[1]
            self.assertIn('proposed_assignee', payload)
            self.assertIsNone(payload['proposed_assignee'])
            self.assertNotIn('_clear_assignee', payload)
            store = memory.Store(Path(root) / 'validation.sqlite3', '0123456789abcdef')
            try:
                self.assertFalse(work_items.WorkItems(store, memory.MemoryError_).validate(payload))
            finally:
                store.close()

    def test_present_malformed_cli_numbers_keep_type_diagnostics(self):
        for name, fields in CASES.items():
            for field in fields.keys() & (INTEGERS | TIMES):
                bad = dict(fields, **{field: 'not-a-number'})
                output = io.StringIO()
                with self.subTest(command=name, field=field), contextlib.redirect_stdout(output), self.assertRaises(SystemExit):
                    self.parser().parse_args(self.argv(name, bad))
                self.assertIn('invalid', output.getvalue())
                self.assertNotIn('arguments are required', output.getvalue())

    def test_wire_missing_and_malformed_fields_are_distinct_without_writes(self):
        with tempfile.TemporaryDirectory() as root:
            store = memory.Store(Path(root) / 'memory.sqlite3', '0123456789abcdef')
            self.addCleanup(store.close)
            work = work_items.WorkItems(store, memory.MemoryError_)
            before = tuple(store.db.iterdump())
            for name, fields in CASES.items():
                request = {k: int(v) if k in INTEGERS else float(v) if k in TIMES else v
                           for k, v in fields.items()}
                request.update(op='claim-renew' if name == 'renew' else 'work-' + name,
                               consumer='synthetic-writer')
                if name != 'create':
                    request['work_id'] = '0' * 32
                if name == 'edit':
                    request['title'] = 'Edited'
                if name == 'finish':
                    request['reason'] = 'Test withdrawal'
                for field in fields:
                    for missing in (True, False):
                        altered = dict(request)
                        if missing:
                            altered.pop(field)
                        else:
                            altered[field] = []
                        with self.subTest(command=name, field=field, missing=missing):
                            with self.assertRaises(memory.MemoryError_) as caught:
                                work.command(altered, pid=123, now=time.time())
                            self.assertEqual(caught.exception.code, 'invalid_request')
                            message = str(caught.exception)
                            if missing:
                                self.assertIn('missing required fields:', message)
                                self.assertIn(field, message)
                            else:
                                self.assertNotIn('missing required fields:', message)
                            self.assertEqual(tuple(store.db.iterdump()), before)

    def test_partial_optional_replay_pair_names_the_missing_partner(self):
        with tempfile.TemporaryDirectory() as root:
            store = memory.Store(Path(root) / 'memory.sqlite3', '0123456789abcdef')
            self.addCleanup(store.close)
            work = work_items.WorkItems(store, memory.MemoryError_)
            request = dict(op='work-release', consumer='writer', work_id='0' * 32,
                           if_revision=1, claim_generation=1, checkpoint='Saved')
            for present, value, missing in [('key', 'release', 'deadline'), ('deadline', 123.0, 'key')]:
                with self.assertRaises(memory.MemoryError_) as caught:
                    work.command(dict(request, **{present: value}), pid=123, now=time.time())
                self.assertIn('missing required fields: ' + missing, str(caught.exception))
