"""Native completion phase boundaries and crash retries, using synthetic evidence."""
import unittest
from unittest.mock import patch

import test_upgrade_coordinator as pipeline
from koinon import upgrade_complete
from koinon.upgrade_documents import Documents
from koinon import upgrade_exclusion
from koinon import upgrade_plan


class EmptyOperationCompletionTests(unittest.TestCase):
    def setUp(self):
        import json
        import test_upgrade_plan
        test_upgrade_plan.PlanTests.setUp(self)
        self.prepared = test_upgrade_plan.PlanTests.prepare(self)
        (self.prefix / 'install.json').write_text(json.dumps(self.config))
        (self.prefix / 'install.json').chmod(0o600)
        (self.operation / 'runtime-backup').mkdir(mode=0o700)
        (self.operation / 'inventory-workspace').mkdir(mode=0o700)
        with self.owner() as owner:
            owner.activate()
            digest = Documents(self.operation).put('prepared-checks', self.checks())
            owner.journal.advance(owner.journal.read(), evidence=digest)
            owner.journal.advance(owner.journal.read())
            pipeline.upgrade_coordinator.through_replacement(owner)
            owner.journal.advance(owner.journal.read())

    def checks(self):
        return dict(version=1)

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def test_empty_runtime_operation_finishes_and_reopens_completion(self):
        with self.owner() as owner:
            result = upgrade_complete.run(owner)
            self.assertEqual(owner.journal.read()['step'], 19)
            self.assertIsNone(upgrade_exclusion.read(self.prefix))
            self.assertEqual(result['preservation']['components'], [])
        with self.owner() as owner:
            self.assertEqual(upgrade_complete.run(owner), result)

    def test_every_completion_document_can_resume_after_lost_publication(self):
        put = Documents.put
        for name in ('migration', 'starting', 'verification', 'release', 'complete'):
            with self.subTest(name=name), self.owner() as owner:
                def interrupted(documents, actual, value):
                    result = put(documents, actual, value)
                    if actual == name:
                        raise OSError('synthetic lost completion reply')
                    return result
                with patch.object(Documents, 'put', new=interrupted):
                    with self.assertRaises(OSError):
                        upgrade_complete.run(owner)
                self.assertIsNotNone(upgrade_exclusion.read(self.prefix))
        with self.owner() as owner:
            upgrade_complete.run(owner)
            self.assertEqual(owner.journal.read()['step'], 19)

    def test_post_release_resume_never_repeats_inventory_comparison(self):
        put = Documents.put
        def interrupted(documents, name, value):
            if name == 'complete':
                raise OSError('synthetic readiness interruption')
            return put(documents, name, value)
        with self.owner() as owner:
            with patch.object(Documents, 'put', new=interrupted):
                with self.assertRaises(OSError):
                    upgrade_complete.run(owner)
            self.assertEqual(owner.journal.read()['step'], 18)
        with self.owner() as owner, patch.object(upgrade_complete, '_compare') as compare, \
                patch.object(upgrade_complete, '_backups') as backups:
            upgrade_complete.run(owner)
            compare.assert_not_called()
            backups.assert_not_called()

    def test_inactive_adapter_requirement_is_explicit(self):
        upgrade_complete.supported([dict(kind='memory', running=False, selection=dict(backend='systemd'))])
        upgrade_complete.supported([dict(kind='memory', running=True, selection=dict(backend='manual'))])
        with self.assertRaisesRegex(upgrade_complete.CompletionError, 'adapter'):
            upgrade_complete.supported([dict(kind='memory', running=True, selection=dict(backend='unknown'))])


class ClaudeStatusLineCompletionTests(EmptyOperationCompletionTests):
    """The same completion with a preflight plan to set up the Claude status line."""
    def checks(self):
        import json
        import tempfile
        from pathlib import Path
        from koinon import claude_statusline
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.claude = Path(temporary.name)
        self.original = dict(type='command', command='echo synthetic')
        (self.claude / 'settings.json').write_text(json.dumps(dict(statusLine=self.original)))
        return dict(version=1, claude_statusline=claude_statusline.plan(self.config, self.claude))

    def test_completion_sets_up_the_status_line_after_release_and_reports_it_once(self):
        import json
        from koinon import claude_statusline
        with self.owner() as owner:
            result = upgrade_complete.run(owner)
        self.assertEqual(result['claude_statusline']['outcome'], 'set_up')
        line = json.loads((self.claude / 'settings.json').read_text())['statusLine']
        self.assertTrue(claude_statusline.is_wrapper(line, self.prefix))
        record = json.loads((self.prefix / 'install.json').read_text())[claude_statusline.KEY]
        self.assertEqual((record['state'], record['original']), ('enabled', self.original))
        with self.owner() as owner:
            self.assertEqual(upgrade_complete.run(owner), result)

    def test_an_interrupted_outcome_publication_resumes_without_a_false_conflict(self):
        import json
        from koinon import claude_statusline
        put = Documents.put

        def interrupted(documents, name, value):
            if name == 'claude-statusline':
                raise OSError('synthetic lost status-line outcome')
            return put(documents, name, value)
        with self.owner() as owner, patch.object(Documents, 'put', new=interrupted):
            with self.assertRaises(OSError):
                upgrade_complete.run(owner)
        line = json.loads((self.claude / 'settings.json').read_text())['statusLine']
        self.assertTrue(claude_statusline.is_wrapper(line, self.prefix))
        with self.owner() as owner:
            result = upgrade_complete.run(owner)
        self.assertEqual(result['claude_statusline']['outcome'], 'unchanged')
        self.assertEqual(json.loads((self.claude / 'settings.json').read_text())['statusLine'], line)


class ParticipantGuidanceCompletionTests(EmptyOperationCompletionTests):
    """The same completion with a pre-sprint guidance block to migrate."""
    def checks(self):
        import tempfile
        from pathlib import Path
        from koinon import participant_instructions
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.home = Path(temporary.name)
        self.target = self.home / 'AGENTS.md'
        self.target.write_text('Personal.\n' + participant_instructions.legacy_section(self.prefix, 'codex', '/old/python3'))
        config = dict(self.config, codex_home=str(self.home), participants=['codex'])
        return dict(version=1, participant_guidance=participant_instructions.plan(config, self.prefix))

    def test_completion_migrates_the_block_once_and_resumes_without_a_false_conflict(self):
        import json
        import sys
        from koinon import participant_instructions
        put = Documents.put

        def interrupted(documents, name, value):
            if name == 'participant-guidance':
                raise OSError('synthetic lost guidance outcome')
            return put(documents, name, value)
        with self.owner() as owner, patch.object(Documents, 'put', new=interrupted):
            with self.assertRaises(OSError):
                upgrade_complete.run(owner)
        migrated = self.target.read_text()
        self.assertEqual(migrated, 'Personal.\n' + participant_instructions.section(self.prefix, 'codex', sys.executable))
        with self.owner() as owner:
            result = upgrade_complete.run(owner)
        entries = result['participant_guidance']['codex']['entries']
        self.assertEqual([(e['state'], e['action']) for e in entries], [('current', 'unchanged')])
        self.assertEqual(self.target.read_text(), migrated)
        record = json.loads((self.prefix / 'install.json').read_text())[participant_instructions.RECORD_KEY]
        self.assertEqual(record['blocks']['codex']['state'], 'current')
        with self.owner() as owner:
            self.assertEqual(upgrade_complete.run(owner), result)
