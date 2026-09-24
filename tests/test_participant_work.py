"""Claimed-work observations use synthetic repositories, sessions and memory services."""
import asyncio
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import memory
from koinon import memory_service_config, participant_status, participant_work, platform_support
import waiting


class ClaimedWorkTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.env = patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude'),
                              CODEX_HOME=str(self.root / 'codex'))
        self.env.start()
        self.addCleanup(self.env.stop)
        self.repo = self.root / 'repo'
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.prefix = self.root / 'writer-install'
        self.prefix.mkdir()
        self.state = self.root / 'writer-state'
        self.key, selection = memory_service_config.selection(self.repo, self.state)
        selection.update(backend='manual', artifact=None, artifact_digest=None, state='installed')
        (self.prefix / 'install.json').write_text(json.dumps(dict(
            state_root=str(self.state), unit_dir=str(self.root / 'units'),
            memory_services=dict(version=1, repositories={self.key: selection}))))
        (self.prefix / 'install.json').chmod(0o600)
        self.home = self.state / 'memory' / self.key
        self.home.mkdir(parents=True, mode=0o700)
        self.state.chmod(0o700)
        self.home.parent.chmod(0o700)
        self.proc = subprocess.Popen([sys.executable, 'memory.py', '--service-dir', str(self.home),
                                      '--repo-path', str(self.repo), 'serve'],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(self.stop)
        ready, _, _ = select.select([self.proc.stdout], [], [], waiting.timeout())
        self.assertTrue(ready, 'memory service startup')
        line = self.proc.stdout.readline()
        self.assertTrue(line, self.proc.stderr.read() if self.proc.poll() is not None else 'no startup')
        self.consumer = 'synthetic-session'
        self.counter = 0

    def stop(self):
        if self.proc.poll() is None:
            self.proc.terminate()
        self.proc.communicate(timeout=waiting.timeout())

    def request(self, op, **fields):
        self.counter += 1
        request = dict(op=op, consumer=self.consumer, key=str(self.counter), deadline=time.time()+300, **fields)
        reply = asyncio.run(memory.request_bound(self.home, self.key, request))
        self.assertTrue(reply['ok'], reply)
        return reply['result']

    def claim(self):
        item = self.request('work-create', title='Synthetic change', criteria='Checks pass', non_goals='No deploy')
        return self.request('work-start', work_id=item['work_id'], if_revision=item['revision'],
                            checkpoint='Implementation underway', next_artifact='Patch',
                            progress_deadline=time.time()+300)

    def association(self, agent='codex'):
        return participant_work.association(agent, 'synthetic-session', self.repo, prefix=self.prefix)

    def observe(self, value=None):
        return participant_work.observe(self.association() if value is None else value, int(time.time()*1000))

    def test_active_claim_empty_and_unavailable_are_distinct(self):
        self.assertEqual(self.observe()['claims'], [])
        self.assertEqual(self.observe()['state'], 'observed')
        item = self.claim()
        view = self.observe()
        self.assertEqual(view['claims'], [dict(work_id=item['work_id'], title='Synthetic change',
                                              checkpoint='Implementation underway')])
        self.assertEqual(view['source'], 'memory_work_list')
        self.assertEqual(view['freshness_ms'], 15000)
        self.stop()
        self.assertEqual(self.observe()['reason'], 'memory_unavailable')
        self.assertEqual(self.observe()['claims'], [])
        self.assertEqual(participant_work.observe(None, 1)['reason'], 'work_association_missing')

    def test_custom_key_excludes_native_claim_and_survives_record_rewrite(self):
        self.claim()
        participant_work.set_key('codex', 'synthetic-session', 'custom-key')
        self.assertEqual(self.association()['consumer'], 'custom-key')
        self.assertEqual(self.observe()['claims'], [])
        self.consumer = 'custom-key'
        self.counter = 0
        item = self.claim()
        self.assertEqual(self.observe()['claims'][0]['work_id'], item['work_id'])
        process = dict(pid=os.getpid(), proc_start=platform_support.proc_start(os.getpid()))
        for _ in range(2):
            participant_status.write('bridge', os.getpid(), participant=process, groups={},
                identity=dict(proc_start=process['proc_start'], generation='synthetic'), provider='codex',
                work=self.association())
        read = participant_status.read('bridge', os.getpid())
        self.assertEqual(participant_status.views(read)['work']['claims'][0]['work_id'], item['work_id'])

    def test_claude_and_codex_records_use_writer_store(self):
        item = self.claim()
        process = dict(pid=os.getpid(), proc_start=platform_support.proc_start(os.getpid()))
        participant_status.write('claude', 'synthetic-session', participant=process, groups={},
                                 work=self.association('claude'))
        value = participant_status.read('claude', 'synthetic-session', participant=process)
        self.assertEqual(value['work']['state_root'], str(self.state))
        self.assertEqual(participant_status.views(value)['work']['claims'][0]['work_id'], item['work_id'])
        self.assertIsNone(participant_work.association('codex', 'synthetic-session', self.repo,
                                                      prefix=self.root / 'other-install'))

    def test_expired_lease_listing_does_not_mutate_item(self):
        # Exercise the same read-only listing at a future clock, without waiting for a lease.
        self.stop()
        store = memory.Store(self.home / 'memory.sqlite3', self.key)
        self.addCleanup(store.close)
        from koinon.work_items import WorkItems
        work = WorkItems(store, memory.MemoryError_)
        now = time.time()
        item = work.command(dict(op='work-create', consumer='writer', title='Expired claim',
                                 criteria='Test', non_goals='None', key='create', deadline=now+300), now=now)
        work.command(dict(op='work-start', consumer='writer', work_id=item['work_id'],
                          if_revision=item['revision'], checkpoint='Saved checkpoint', next_artifact='Patch',
                          progress_deadline=now+300, key='start', deadline=now+300), now=now)
        before = tuple(store.db.iterdump())
        self.assertEqual(work.listing(dict(owner='writer'), now+7200)['items'], [])
        self.assertEqual(tuple(store.db.iterdump()), before)

    def test_work_key_cli_and_deepseek_work_do_not_invent_model_context(self):
        env = dict(os.environ, DSH_SESSION_ID='synthetic-session')
        result = subprocess.run([sys.executable, 'session.py', 'work-key', '--agent', 'deepseek',
                                 '--key', 'deepseek-custom'], env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.consumer = 'deepseek-custom'
        item = self.claim()
        association = self.association('deepseek')
        self.assertEqual(association['consumer'], self.consumer)
        groups = {name: dict(source='koinon_notifier', recorded_at_ms=1,
                             reason='provider_unsupported') for name in ('model', 'context')}
        participant_status.write('bridge', os.getpid(), participant=None, groups=groups,
            identity=dict(proc_start=platform_support.proc_start(os.getpid()), generation='synthetic'),
            provider='deepseek', work=association)
        views = participant_status.views(participant_status.read('bridge', os.getpid()))
        for name in ('model', 'context'):
            self.assertEqual(views[name]['reason'], 'provider_unsupported')
        self.assertEqual(views['work']['claims'][0]['work_id'], item['work_id'])

    def test_custom_key_does_not_cross_sessions_or_providers(self):
        participant_work.set_key('codex', 'synthetic-session', 'custom')
        self.assertEqual(self.association('claude')['consumer'], 'synthetic-session')
        other = participant_work.association('codex', 'second-session', self.repo, prefix=self.prefix)
        self.assertEqual(other['consumer'], 'second-session')
        self.assertIsNone(participant_work.association('codex', 'synthetic-session', None,
                                                      prefix=self.prefix))

    def test_repository_mismatch_does_not_read_another_repositorys_claims(self):
        self.claim()
        other = self.root / 'other-repo'
        subprocess.run(['git', 'init', '-q', str(other)], check=True)
        self.assertIsNone(participant_work.association('codex', 'synthetic-session', other,
                                                      prefix=self.prefix))
        wrong = dict(self.association(), repository=str(other))
        self.assertEqual(self.observe(wrong)['reason'], 'memory_unavailable')
        self.assertEqual(self.observe(wrong)['claims'], [])
