"""Revision identity, explicit processing acknowledgement and durable notice deduplication."""
import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from koinon import durable_state, guidance, revisions, participant_status
from koinon.notification_notices import notify_guidance
import test_participant_guidance as guide_tests
import test_upgrade_complete as completion_tests
from koinon import upgrade_complete, upgrade_exclusion
from koinon.upgrade_documents import Documents


class RevisionTests(unittest.TestCase):
    def test_runtime_digest_uses_only_canonical_listed_bytes_and_names(self):
        import zipfile
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root/'scripts').mkdir()
            (root/'scripts/install.py').write_text("FILES = ('session.py', 'scripts/install.py')\n")
            (root/'session.py').write_text('original runtime\n')
            first = revisions.runtime_revision(root)
            import hashlib
            manifest = dict(files={name: dict(sha256=hashlib.sha256((root/name).read_bytes()).hexdigest())
                                  for name in ('session.py', 'scripts/install.py')})
            self.assertEqual(revisions.runtime_revision(manifest=manifest), first)
            (root/'unlisted.py').write_text('unrelated runtime\n')
            self.assertEqual(revisions.runtime_revision(root), first)
            archive = root/'runtime.zip'
            with zipfile.ZipFile(archive, 'w') as output:
                for name in ('session.py', 'scripts/install.py'):
                    output.write(root/name, name)
            with zipfile.ZipFile(archive) as source:
                self.assertEqual(revisions.runtime_revision(zipfile.Path(source)), first)
            (root/'session.py').write_text('changed runtime\n')
            self.assertNotEqual(revisions.runtime_revision(root), first)


    def test_catalog_and_runtime_are_distinct_and_snapshot_is_cached(self):
        captured = revisions.loaded_runtime_revision()
        with patch.object(revisions, 'runtime_revision', return_value='f' * 64):
            self.assertEqual(revisions.loaded_runtime_revision(), captured)
        self.assertNotEqual(captured, guidance.revision())
        self.assertEqual(revisions.observe_runtime({'runtime_revision': 'a'*64}, 'b'*64)['reason'], 'mismatch')
        self.assertEqual(revisions.observe_runtime({'runtime_revision': 'a'*64})['state'], 'unknown')
        self.assertEqual(revisions.observe_runtime({}, upgrade=True)['reason'], 'upgrade_incomplete')

    def test_peer_guidance_fields_are_allowlisted_and_expire(self):
        value = dict(guide_revision='a'*64, guide_stale=True, runtime_revision='b'*64, recorded_at_ms=1000)
        self.assertEqual(participant_status.validate_guidance(value), value)
        with self.assertRaises(ValueError):
            participant_status.validate_guidance(dict(value, body='not allowed'))
        self.assertTrue(participant_status.guidance_view(dict(reason=None, guidance=value), 1001)['guide_stale'])
        self.assertIsNone(participant_status.guidance_view(dict(reason=None, guidance=value), 100000)['guide_stale'])


class AckCommandTests(unittest.TestCase):
    guide = guide_tests.GuideCommandTests.guide
    def setUp(self):
        guide_tests.GuideCommandTests.setUp(self)
        self.config = json.loads((self.prefix/'install.json').read_text())
        self.config.update(revisions.installed_fields())
        durable_state.publish(self.prefix/'install.json', self.config)

    def test_guide_never_acknowledges_and_ack_is_scoped_and_validated(self):
        import subprocess
        first = self.guide('--agent', 'codex', '--thread', 'synthetic-one', '--json')
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertTrue(json.loads(first.stdout)['guide_stale'])
        base = [sys.executable, str(self.prefix/'session.py'), 'guide-ack']
        rejected = subprocess.run(base + ['0'*64, '--agent', 'codex', '--thread', 'synthetic-one'],
                                  env=self.env, capture_output=True, text=True)
        self.assertEqual(rejected.returncode, 78)
        import session
        state = session.details(self.prefix, self.config, 'synthetic-one', '/unused')[0]
        self.assertFalse(revisions.ack_path(state).exists())
        done = subprocess.run(base + [guidance.revision(), '--agent', 'codex', '--thread', 'synthetic-one'],
                              env=self.env, capture_output=True, text=True)
        self.assertEqual(done.returncode, 0, done.stderr+done.stdout)
        self.assertEqual(revisions.ack_path(state).stat().st_mode & 0o777, 0o600)
        for thread, stale in (('synthetic-one', False), ('synthetic-two', True)):
            value = json.loads(self.guide('--agent', 'codex', '--thread', thread, '--json').stdout)
            self.assertIs(value['guide_stale'], stale)
        self.assertFalse((state/'session.json').exists())

    def test_guide_reports_an_upgrade_without_registering_or_writing(self):
        from test_participant_guidance import tree_digest
        config = dict(self.config, installation_state='upgrading',
                      upgrade=dict(version=1, operation=str(self.root/'operation'), plan='a'*64))
        durable_state.publish(self.prefix/'install.json', config)
        before = tree_digest(self.root)
        result = self.guide('--agent', 'codex', '--thread', 'synthetic-one', '--json')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['runtime']['reason'], 'upgrade_incomplete')
        self.assertEqual(tree_digest(self.root), before)

    def test_claude_acknowledgements_do_not_share_a_file(self):
        registry = self.root/'claude'/'sessions'
        first = revisions.ack_path(family='claude', session_id='synthetic-one', registry=registry)
        second = revisions.ack_path(family='claude', session_id='synthetic-two', registry=registry)
        revisions.acknowledge(self.prefix, guidance.revision(), first)
        self.assertFalse(revisions.guidance_fields(self.config, first)['guide_stale'])
        self.assertTrue(revisions.guidance_fields(self.config, second)['guide_stale'])


class NoticeTests(unittest.IsolatedAsyncioTestCase):
    async def test_once_per_revision_and_session_across_restarts_and_ack(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); prefix=root/'prefix'; prefix.mkdir(mode=0o700)
            config=dict(state_root=str(root/'state'), unit_dir=str(root/'units'),
                        runtime_revision='c'*64, guidance_revision='a'*64)
            durable_state.publish(prefix/'install.json', config)
            delivered=[]
            async def deliver(text):
                delivered.append(text)
                return 'delivered'
            for family in ('codex','deepseek'):
                state=root/family
                for _ in range(3):
                    await notify_guidance(prefix, state, family, deliver)
                self.assertTrue(revisions.guidance_fields(config,revisions.ack_path(state))['guide_stale'])
            self.assertEqual(len(delivered), 2)
            self.assertNotIn(guidance.CATALOG['overview']['text'], delivered[0])
            self.assertIn('guide-ack', delivered[0])
            config['guidance_revision']='b'*64
            durable_state.publish(prefix/'install.json',config)
            await notify_guidance(prefix,root/'codex','codex',deliver)
            revisions.acknowledge(prefix,'b'*64,revisions.ack_path(root/'deepseek'))
            await notify_guidance(prefix,root/'deepseek','deepseek',deliver)
            self.assertEqual(len(delivered),3)
            # Even returning to a previous revision does not resend it.
            config['guidance_revision']='a'*64
            durable_state.publish(prefix/'install.json',config)
            await notify_guidance(prefix,root/'codex','codex',deliver)
            self.assertEqual(len(delivered),3)

    async def test_reserved_uncertain_delivery_is_not_repeated(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); prefix=root/'prefix'; prefix.mkdir(mode=0o700)
            durable_state.publish(prefix/'install.json', dict(state_root=str(root/'state'),
                                  unit_dir=str(root/'units'), guidance_revision='a'*64))
            async def interrupted(text):
                raise asyncio.CancelledError()
            with self.assertRaises(asyncio.CancelledError):
                await notify_guidance(prefix,root/'session','codex',interrupted)
            async def forbidden(text):
                self.fail('an uncertain reserved notice was repeated')
            result=await notify_guidance(prefix,root/'session','codex',forbidden)
            self.assertEqual(result['outcome'],'unknown')


class RevisionCompletionTests(unittest.TestCase):
    setUp = completion_tests.EmptyOperationCompletionTests.setUp
    owner = completion_tests.EmptyOperationCompletionTests.owner
    def checks(self):
        self.target = dict(runtime_revision='a'*64, guidance_revision='b'*64)
        return dict(version=1, plan=self.prepared['sha256'], runtime_revisions=self.target)

    def test_resume_after_finish_does_not_claim_a_completed_revision_publication(self):
        (self.prefix/'.upgrade').mkdir(mode=0o700, exist_ok=True)
        durable_state.publish(self.prefix/'.upgrade/current.json',dict(version=1,
                              operation=str(self.operation),plan=self.prepared['sha256']))
        original=upgrade_complete._runtime_revisions
        with self.owner() as owner:
            with patch.object(upgrade_complete,'_runtime_revisions',side_effect=OSError('interrupted after finish')):
                with self.assertRaises(OSError):
                    upgrade_complete.run(owner)
            self.assertIsNone(upgrade_exclusion.read(self.prefix))
            self.assertTrue(revisions.upgrade_incomplete(self.prefix))
            result=original(owner,Documents(self.operation),owner.journal.read(),{})
        self.assertEqual(result['runtime_revisions'],self.target)
        self.assertFalse(revisions.upgrade_incomplete(self.prefix))
        self.assertEqual(json.loads((self.prefix/'install.json').read_text())['runtime_revision'],'a'*64)
        with self.owner() as owner:
            self.assertEqual(upgrade_complete.run(owner)['runtime_revisions'],self.target)
