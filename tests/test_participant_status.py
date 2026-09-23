import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import bridge
from koinon import participant_status as status
from koinon import platform_support
from koinon import runtime_names


def me():
    return dict(pid=os.getpid(), proc_start=platform_support.proc_start(os.getpid()))


def ended():
    child = subprocess.Popen([sys.executable, '-c', 'pass'])
    start = platform_support.proc_start(child.pid)
    child.wait()
    return dict(pid=child.pid, proc_start=start)


def observed(**fields):
    return dict(source='synthetic_source', recorded_at_ms=1000, reason=None, **fields)


class StatusRecordTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.config = Path(self.temp.name) / 'claude'
        self.registry = self.config / 'sessions'
        self.env = mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config))
        self.env.start()

    def tearDown(self):
        self.env.stop()
        self.temp.cleanup()

    def claude(self, participant=None, **groups):
        return status.write('claude', 'synthetic-session', participant=participant or me(), groups=groups)

    def test_directory_sits_beside_the_registry_and_follows_the_configuration(self):
        self.assertEqual(status.directory(), self.config / 'koinon-status')
        self.assertEqual(status.directory(Path('/elsewhere/sessions')), Path('/elsewhere/koinon-status'))

    def test_record_and_directory_are_owner_only(self):
        path = self.claude(model=observed(id='synthetic-model'))
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)
        self.assertFalse(path.with_name(path.name + '.tmp').exists())

    def test_text_outside_the_allowlist_is_never_stored_or_reported(self):
        secret = 'synthetic prompt text that must not leave the source'
        path = self.claude(
            model=observed(id='synthetic-model', display_name=secret),
            context=observed(limit_tokens=200000, used_tokens=50000, usage_available=True,
                             workspace=secret, transcript_path=secret),
            session_name=dict(source='synthetic_source', recorded_at_ms=1, reason=None, text=secret))
        self.assertNotIn(secret, path.read_text())
        # A record edited to carry text is filtered again when it is read.
        record = json.loads(path.read_text())
        record['groups']['model']['last_agent_message'] = secret
        record['extra'] = secret
        path.write_text(json.dumps(record))
        result = status.read('claude', 'synthetic-session', participant=me())
        self.assertNotIn(secret, json.dumps(status.views(result)))

    def test_observed_values_need_an_associated_participant(self):
        with self.assertRaisesRegex(ValueError, 'associated participant'):
            status.write('bridge', 1, participant=None, groups=dict(model=observed(id='m')),
                         identity=dict(proc_start='1', generation='g'), provider='codex')

    def test_fill_uses_tokens_in_context_and_keeps_the_source_time(self):
        self.claude(model=observed(id='synthetic-model'),
                    context=observed(limit_tokens=200000, used_tokens=50000, usage_available=True))
        now = int(time.time() * 1000)
        views = status.views(status.read('claude', 'synthetic-session', participant=me()), now)
        self.assertEqual(views['model']['id'], 'synthetic-model')
        context = views['context']
        self.assertEqual((context['state'], context['fill']), ('observed', 0.25))
        self.assertEqual(context['recorded_at_ms'], 1000)
        self.assertEqual(context['observed_at_ms'], now)
        self.assertEqual(context['freshness_ms'], 15000)

    def test_out_of_range_integers_are_refused_not_divided(self):
        for fields in (dict(limit_tokens=1, used_tokens=10 ** 400), dict(limit_tokens=2 ** 53, used_tokens=1)):
            with self.subTest(fields):
                with self.assertRaises(ValueError):
                    self.claude(context=observed(usage_available=True, **fields))
                path = self.claude(context=observed(limit_tokens=1, used_tokens=1, usage_available=True))
                record = json.loads(path.read_text())
                record['groups']['context'].update(fields)
                path.write_text(json.dumps(record))
                result = status.read('claude', 'synthetic-session', participant=me())
                self.assertEqual(status.views(result)['context']['reason'], 'status_record_invalid')

    def test_a_source_time_after_the_read_time_is_not_observed(self):
        future = int(time.time() * 1000) + 60_000
        self.claude(model=dict(observed(id='synthetic-model'), recorded_at_ms=future),
                    activity=dict(observed(state='busy'), recorded_at_ms=future))
        result = status.read('claude', 'synthetic-session', participant=me())
        now = int(time.time() * 1000)
        self.assertEqual(status.views(result, now)['model']['reason'], 'status_record_invalid')
        self.assertEqual(status.activity(result, now)['reason'], 'status_record_invalid')

    def test_unavailable_usage_is_unknown_not_zero(self):
        self.claude(context=observed(limit_tokens=200000, used_tokens=0, usage_available=False))
        context = status.views(status.read('claude', 'synthetic-session', participant=me()))['context']
        self.assertEqual((context['state'], context['reason'], context['fill']),
                         ('unknown', 'no_token_usage', None))

    def test_an_ended_or_recycled_participant_reports_nothing(self):
        gone = ended()
        self.claude(participant=gone, model=observed(id='synthetic-model'))
        result = status.read('claude', 'synthetic-session')
        self.assertEqual((result['groups'], result['reason']), ({}, 'participant_not_live'))
        recycled = dict(pid=os.getpid(), proc_start='0')
        self.claude(participant=recycled, model=observed(id='synthetic-model'))
        self.assertEqual(status.read('claude', 'synthetic-session')['reason'], 'participant_not_live')

    def test_a_record_for_another_claude_process_is_not_used(self):
        self.claude(participant=ended(), model=observed(id='synthetic-model'))
        result = status.read('claude', 'synthetic-session', participant=me())
        self.assertEqual(result['reason'], 'no_status_record')

    def test_unsafe_or_malformed_records_are_refused(self):
        path = self.claude(model=observed(id='synthetic-model'))
        cases = {
            'malformed': lambda: path.write_text('{not json'),
            'oversized': lambda: path.write_text(json.dumps(dict(pad='x' * (status.MAX_BYTES + 1)))),
            'shared': lambda: path.chmod(0o644),
        }
        for name, damage in cases.items():
            with self.subTest(name):
                self.claude(model=observed(id='synthetic-model'))
                damage()
                self.assertEqual(status.read('claude', 'synthetic-session')['reason'],
                                 'status_record_invalid')
        target = path.with_name('target.json')
        path.replace(target)
        path.symlink_to(target)
        self.assertEqual(status.read('claude', 'synthetic-session')['reason'], 'status_record_invalid')

    def test_invalid_keys_never_become_paths(self):
        for key in ('../escape', '', '.hidden', 'a/b', 'x' * 200):
            with self.subTest(key):
                self.assertEqual(status.read('claude', key)['reason'], 'status_record_invalid')
                with self.assertRaises(ValueError):
                    status.write('claude', key, participant=me(), groups={})

    def test_bridge_record_matches_only_its_bridge_and_generation(self):
        identity = dict(proc_start=platform_support.proc_start(os.getpid()), generation='generation-a')
        status.write('bridge', os.getpid(), participant=me(), provider='codex', identity=identity,
                     groups=dict(activity=observed(state='busy')))
        self.assertIsNone(status.read('bridge', os.getpid(), identity=identity)['reason'])
        other = dict(identity, generation='generation-b')
        self.assertEqual(status.read('bridge', os.getpid(), identity=other)['reason'], 'no_status_record')
        self.assertFalse(status.remove('bridge', os.getpid(), generation='generation-b'))
        self.assertTrue(status.remove('bridge', os.getpid(), generation='generation-a'))
        self.assertEqual(status.read('bridge', os.getpid())['reason'], 'no_status_record')

    def test_unassociated_bridge_keeps_each_group_reason(self):
        identity = dict(proc_start='1', generation='g')
        for provider, reason in (('codex', 'participant_not_associated'), ('deepseek', 'provider_unsupported')):
            with self.subTest(provider):
                status.write('bridge', 7, participant=None, provider=provider, identity=identity,
                             groups={name: dict(source='koinon_notifier', recorded_at_ms=5, reason=reason)
                                     for name in status.FIELDS})
                result = status.read('bridge', 7, identity=identity)
                views = status.views(result)
                self.assertEqual({views['model']['reason'], views['context']['reason']}, {reason})
                self.assertEqual(status.activity(result, int(time.time() * 1000))['reason'], reason)

    def test_work_is_unknown_until_it_is_associated(self):
        self.assertEqual(status.work_view(0)['reason'], 'work_association_missing')
        self.assertEqual(status.work_view(0)['claims'], [])


class PeerListingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.config = Path(self.temp.name) / 'claude'
        self.registry = self.config / 'sessions'
        self.registry.mkdir(parents=True, mode=0o700)
        self.env = mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config))
        self.env.start()
        self.address = mock.patch('bridge.target_path', return_value=None)
        self.address.start()

    def tearDown(self):
        self.address.stop()
        self.env.stop()
        self.temp.cleanup()

    def register(self, **fields):
        record = dict(pid=os.getpid(), procStart=platform_support.proc_start(os.getpid()),
                      messagingSocketPath='/tmp/cc-socks/synthetic.sock', name='synthetic-peer',
                      peerProtocol=1, **fields)
        (self.registry / f'{os.getpid()}.json').write_text(json.dumps(record))

    def test_claude_peer_reports_its_model_and_context(self):
        self.register(entrypoint='cli', sessionId='synthetic-session', status='idle',
                      statusUpdatedAt=1)
        status.write('claude', 'synthetic-session', participant=me(),
                     groups=dict(model=observed(id='synthetic-model'),
                                 context=observed(limit_tokens=1000000, used_tokens=100000,
                                                  usage_available=True)))
        [peer] = bridge.peers()
        self.assertEqual(peer['model']['id'], 'synthetic-model')
        self.assertEqual(peer['context']['fill'], 0.1)
        self.assertEqual(peer['work']['reason'], 'work_association_missing')
        self.assertEqual(peer['presence']['model_activity']['source'], 'claude_registry')

    def test_claude_peer_without_a_record_is_unknown(self):
        self.register(entrypoint='cli', sessionId='synthetic-session')
        [peer] = bridge.peers()
        self.assertEqual({peer[k]['state'] for k in ('model', 'context', 'work')}, {'unknown'})
        self.assertEqual(peer['context']['reason'], 'no_status_record')

    def test_koinon_participant_reports_its_record_activity(self):
        start = platform_support.proc_start(os.getpid())
        self.register(entrypoint=runtime_names.REGISTRY_ENTRYPOINT, bridgeOwner='generation-a')
        status.write('bridge', os.getpid(), participant=me(), provider='codex',
                     identity=dict(proc_start=start, generation='generation-a'),
                     groups=dict(activity=observed(state='busy'), model=observed(id='synthetic-codex')))
        [peer] = bridge.peers()
        self.assertEqual(peer['status'], 'busy')
        self.assertEqual(peer['presence']['model_activity']['source'], 'synthetic_source')
        self.assertEqual(peer['model']['id'], 'synthetic-codex')

    def test_both_families_read_the_same_listing(self):
        self.register(entrypoint='cli', sessionId='synthetic-session')
        claude = bridge.peers()
        with mock.patch.dict(os.environ, CODEX_THREAD_ID='synthetic-thread', CLAUDE_CODE_SESSION_ID=''):
            codex = bridge.peers()
        for peer in claude + codex:
            for view in ('model', 'context', 'work'):
                peer[view].pop('observed_at_ms')
            peer['presence']['service'].pop('observed_at_ms')
            peer['presence']['model_activity'].pop('observed_at_ms', None)
        self.assertEqual(claude, codex)
