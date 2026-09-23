"""Synthetic rollout and process evidence for Codex participant status."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from koinon import codex_status, platform_support
from waiting import timeout, wait_until_sync


def event(kind, **payload):
    return dict(type=kind, timestamp='2026-01-01T00:00:00Z', payload=payload)


class ReaderTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.home = Path(self.temp.name)
        (self.home / 'sessions').mkdir()
        self.path = self.home / 'sessions' / 'rollout-synthetic-thread.jsonl'
        self.path.write_text(json.dumps(event('session_meta', id='synthetic-thread')) + '\n')
        self.reader = codex_status.Reader('synthetic-thread', sys.executable, self.home)
        self.patches = [mock.patch.object(platform_support, 'open_file_holders', return_value=[os.getpid()]),
                        mock.patch.object(platform_support, 'holds_open', return_value=True),
                        mock.patch.object(platform_support, 'codex_process', return_value=True)]
        for patch in self.patches:
            patch.start()

    def tearDown(self):
        for patch in reversed(self.patches):
            patch.stop()
        self.temp.cleanup()

    def append(self, *records):
        with self.path.open('a') as stream:
            for record in records:
                stream.write(json.dumps(record) + '\n')

    def test_turns_usage_and_no_content_retention(self):
        self.append(event('turn_context', model='synthetic-model', prompt='PRIVATE'),
                    event('event_msg', type='task_started', turn_id='turn-a'),
                    event('event_msg', type='token_count', info=dict(
                        last_token_usage=dict(input_tokens=120), total_token_usage=dict(input_tokens=9000),
                        model_context_window=1000)))
        participant, groups = self.reader.sample()
        self.assertEqual(participant['pid'], os.getpid())
        self.assertEqual(groups['activity']['state'], 'busy')
        self.assertEqual(groups['context']['used_tokens'], 120)
        self.assertEqual(groups['model']['id'], 'synthetic-model')
        self.assertNotIn('PRIVATE', repr(vars(self.reader)))
        self.append(event('event_msg', type='task_complete', turn_id='other', last_agent_message='PRIVATE'))
        self.assertEqual(self.reader.sample()[1]['activity']['state'], 'busy')
        self.append(event('event_msg', type='turn_aborted', turn_id='turn-a'))
        self.assertEqual(self.reader.sample()[1]['activity']['state'], 'idle')
        self.assertNotIn('PRIVATE', repr(vars(self.reader)))

    def test_partial_line_waits_then_observes_completion(self):
        self.append(event('event_msg', type='task_started', turn_id='a'))
        self.reader.sample()
        raw = json.dumps(event('event_msg', type='task_complete', turn_id='a'))
        with self.path.open('a') as stream:
            stream.write(raw[:20])
        self.assertEqual(self.reader.sample()[1]['activity']['reason'], 'source_unrecognized')
        with self.path.open('a') as stream:
            stream.write(raw[20:] + '\n')
        self.assertEqual(self.reader.sample()[1]['activity']['state'], 'idle')

    def test_lost_association_invalidates_busy(self):
        self.append(event('event_msg', type='task_started', turn_id='a'))
        self.reader.sample()
        with mock.patch.object(platform_support, 'holds_open', return_value=False), \
                mock.patch.object(platform_support, 'open_file_holders', return_value=[]):
            owner, groups = self.reader.sample()
        self.assertIsNone(owner)
        self.assertEqual(groups['activity']['reason'], 'participant_not_associated')

    def test_ambiguous_and_non_codex_holders_are_unknown(self):
        for holders in ([], [1, 2]):
            with mock.patch.object(platform_support, 'open_file_holders', return_value=holders):
                self.assertEqual(self.reader.sample()[1]['activity']['reason'], 'participant_not_associated')
        with mock.patch.object(platform_support, 'codex_process', return_value=False):
            self.assertIsNone(self.reader.sample()[0])

    def test_bad_usage_and_unreadable_log_do_not_reuse_values(self):
        self.append(event('event_msg', type='token_count', info=dict(
            last_token_usage=dict(input_tokens=10**400), model_context_window=1000)))
        self.assertEqual(self.reader.sample()[1]['context']['reason'], 'source_unrecognized')
        self.path.unlink()
        self.assertEqual(self.reader.sample()[1]['context']['reason'], 'source_unrecognized')

    def test_unavailable_usage_and_identity_mismatch(self):
        self.append(event('event_msg', type='token_count', info=None))
        self.assertEqual(self.reader.sample()[1]['context']['reason'], 'no_token_usage')
        self.path.write_text(json.dumps(event('session_meta', id='other')) + '\n')
        reader = codex_status.Reader('synthetic-thread', sys.executable, self.home)
        self.assertEqual(reader.sample()[1]['context']['reason'], 'source_unrecognized')

    def test_excessively_nested_record_is_unknown_not_a_notifier_failure(self):
        with self.path.open('a') as stream:
            stream.write('[' * 1100 + '0' + ']' * 1100 + '\n')
        self.assertEqual(self.reader.sample()[1]['context']['reason'], 'source_unrecognized')

    def test_truncation_resets_previous_turn(self):
        self.append(event('event_msg', type='task_started', turn_id='a'))
        self.reader.sample()
        self.path.write_text(json.dumps(event('session_meta', id='synthetic-thread')) + '\n')
        self.assertNotIn('state', self.reader.sample()[1]['activity'])


class ProcessTests(unittest.TestCase):
    def test_open_file_owner_and_closed_descriptor(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'synthetic.log'
            path.write_text('')
            code = 'import sys; f=open(sys.argv[1]); print("ready",flush=True); sys.stdin.readline(); f.close(); print("closed",flush=True); sys.stdin.readline()'
            child = subprocess.Popen([sys.executable, '-c', code, str(path)], stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, text=True)
            try:
                wait_until_sync(lambda: platform_support.holds_open(child.pid, path), 'log opened', process=child)
                self.assertTrue(platform_support.holds_open(child.pid, path))
                self.assertIn(child.pid, platform_support.open_file_holders(path))
                child.stdin.write('close\n'); child.stdin.flush()
                wait_until_sync(lambda: not platform_support.holds_open(child.pid, path), 'log closed', process=child)
                self.assertFalse(platform_support.holds_open(child.pid, path))
            finally:
                child.communicate('\n', timeout=timeout())

    def test_cli_and_wrapper_child_match_without_prompt_arguments(self):
        with mock.patch.object(platform_support, '_process_command') as command:
            command.side_effect = [(7, ['/vendor/codex']), (1, ['/usr/bin/node', '/synthetic/codex'])]
            with mock.patch('shutil.which', return_value='/synthetic/codex'):
                self.assertTrue(platform_support.codex_process(8, '/synthetic/codex'))
            command.side_effect = [(7, ['/bin/cat', '/synthetic/codex']), (1, ['/bin/bash'])]
            with mock.patch('shutil.which', return_value='/synthetic/codex'):
                self.assertFalse(platform_support.codex_process(8, '/synthetic/codex'))
