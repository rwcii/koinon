import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / 'scripts'))

from install import FILES
from koinon import durable_state, guidance
from koinon import participant_instructions as instructions


def tree_digest(root):
    digest = hashlib.sha256()
    for path in sorted(Path(root).rglob('*')):
        info = path.lstat()
        digest.update(str(path.relative_to(root)).encode())
        digest.update(str(stat.S_IMODE(info.st_mode)).encode())
        digest.update(path.read_bytes() if path.is_file() else b'directory')
    return digest.hexdigest()


class GuideCommandTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix = self.root / 'installed runtime'
        self.prefix.mkdir(mode=0o700)
        for name in FILES:
            if name.endswith('.py'):
                target = self.prefix / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
        durable_state.publish(self.prefix / 'install.json',
                              dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'),
                                   codex=sys.executable, session_backend='systemd'))
        for name in ('codex', 'deepseek', 'claude/sessions', 'state'):
            (self.root / name).mkdir(parents=True, exist_ok=True)
        self.env = dict(os.environ, CODEX_HOME=str(self.root / 'codex'), DSH_HOME=str(self.root / 'deepseek'),
                        CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        for key in ('CODEX_THREAD_ID', 'DSH_SESSION_ID', 'CLAUDE_CODE_SESSION_ID',
                    'PYTHONDONTWRITEBYTECODE', 'PYTHONPYCACHEPREFIX'):
            self.env.pop(key, None)

    def guide(self, *args, env=None):
        return subprocess.run([sys.executable, str(self.prefix / 'session.py'), 'guide', *args],
                              capture_output=True, text=True, env=env or self.env, timeout=30)

    def test_guide_is_complete_and_writes_nothing_without_registration_or_services(self):
        cases = (('codex', dict(CODEX_THREAD_ID='synthetic-thread')),
                 ('deepseek', dict(DSH_SESSION_ID='synthetic-session')),
                 ('claude', dict(CLAUDE_CODE_SESSION_ID='synthetic-claude')))
        for family, extra in cases:
            with self.subTest(family=family):
                before = tree_digest(self.root)
                result = self.guide('--agent', family, '--json', env=dict(self.env, **extra))
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                self.assertEqual(tree_digest(self.root), before)
                value = json.loads(result.stdout)
                self.assertEqual([topic['topic'] for topic in value['topics']], list(guidance.TOPICS))
                for observation in value['observations'].values():
                    self.assertIn(observation['state'], ('observed', 'unavailable', 'unknown'))
                if family != 'claude':
                    self.assertIs(value['observations']['registration']['registered'], False)
                    self.assertEqual(value['next_action']['recipe']['id'], 'ensure')

    def test_text_and_brief_forms_and_typed_errors(self):
        result = self.guide('--agent', 'codex', '--brief', '--thread', 'synthetic')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('## overview', result.stdout)
        self.assertNotIn('## register', result.stdout)
        result = self.guide('--agent', 'codex', '--topic', 'nonexistent')
        self.assertEqual(result.returncode, 64)
        self.assertEqual(json.loads(result.stdout)['code'], 'unknown_topic')
        result = self.guide()
        self.assertEqual(result.returncode, 64)
        self.assertEqual(json.loads(result.stdout)['code'], 'family_required')

    def test_missing_installation_is_reported_not_raised(self):
        (self.prefix / 'install.json').unlink()
        result = self.guide('--agent', 'codex', '--json', '--thread', 'synthetic')
        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual(value['observations']['installation']['state'], 'unavailable')
        self.assertIn('not installed', value['next_action']['text'])


class CatalogTests(unittest.TestCase):
    def test_every_topic_renders_for_every_family_with_installed_recipes(self):
        prefix = Path('/synthetic prefix')
        for family in guidance.FAMILIES:
            for topic in guidance.TOPICS:
                with self.subTest(family=family, topic=topic):
                    value = guidance.render(family, topic, python='/usr/bin/python3', prefix=prefix,
                                            observations=dict(registration=dict(state='observed', state_dir='/s')))
                    self.assertEqual(len(value['topics']), 1)
                    for recipe in value['topics'][0]['recipes']:
                        self.assertIsInstance(recipe['argv'], list)
                        self.assertEqual(recipe['argv'][0], '/usr/bin/python3')
                        self.assertTrue(recipe['argv'][1].startswith(str(prefix) + '/'))
                        self.assertIsInstance(recipe['needs_approval'], bool)
        with self.assertRaises(guidance.GuidanceError):
            guidance.render('other', python='p', prefix=prefix)
        with self.assertRaises(guidance.GuidanceError):
            guidance.render('codex', 'other', python='p', prefix=prefix)

    def test_revision_follows_content_not_observations(self):
        first = guidance.render('codex', python='p', prefix='/a', observations=dict(x=dict(state='observed')))
        second = guidance.render('codex', python='p', prefix='/a', observations=dict(x=dict(state='unknown')))
        self.assertEqual(first['guide_revision'], second['guide_revision'])
        changed = json.loads(json.dumps(guidance.CATALOG))
        changed['overview']['text'] += ' Changed.'
        with patch.object(guidance, 'CATALOG', changed):
            self.assertNotEqual(guidance.revision(), first['guide_revision'])
            # The managed block never depends on the catalog.
            self.assertEqual(instructions.section('/a', 'codex', 'p'),
                             instructions.section('/a', 'codex', 'p'))
        self.assertNotIn(guidance.CATALOG['overview']['text'], instructions.section('/a', 'codex', 'p'))


class ReconcileTests(unittest.TestCase):
    PYTHON = '/synthetic/python3'

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.home = Path(temporary.name)
        self.prefix = Path('/synthetic prefix')
        self.user = 'Personal instructions.\n'

    def reconcile(self, agent='codex', record=None, **options):
        return instructions.reconcile(self.home, self.prefix, agent, record, python=self.PYTHON, **options)

    def states(self, report):
        return {Path(entry['path']).name: (entry['state'], entry['action']) for entry in report}

    def block(self, agent='codex'):
        return instructions.section(self.prefix, agent, self.PYTHON)

    def test_install_preserves_user_text_and_repeat_changes_nothing(self):
        (self.home / 'AGENTS.md').write_text(self.user)
        report, record = self.reconcile()
        self.assertEqual(self.states(report), {'AGENTS.md': ('absent', 'written')})
        text = (self.home / 'AGENTS.md').read_text()
        self.assertTrue(text.startswith(self.user))
        self.assertIn("session.py' guide --agent codex", text)
        report, again = self.reconcile(record=record)
        self.assertEqual(self.states(report), {'AGENTS.md': ('current', 'unchanged')})
        self.assertEqual((self.home / 'AGENTS.md').read_text(), text)
        self.assertEqual(again, record)

    def test_edited_block_is_kept_until_explicit_replacement(self):
        (self.home / 'AGENTS.md').write_text(self.user)
        _, record = self.reconcile()
        path = self.home / 'AGENTS.md'
        path.write_text(path.read_text().replace('## Koinon', '## Koinon, edited by the user'))
        edited = path.read_text()
        report, kept = self.reconcile(record=record)
        self.assertEqual(self.states(report), {'AGENTS.md': ('edited', 'kept')})
        self.assertEqual(path.read_text(), edited)
        self.assertEqual(kept['state'], 'edited')
        report, replaced = self.reconcile(record=kept, replace=True)
        self.assertEqual(self.states(report), {'AGENTS.md': ('edited', 'written')})
        self.assertEqual(path.read_text(), self.user + self.block())
        self.assertEqual(replaced['state'], 'current')
        self.assertTrue((self.home / 'AGENTS.md.before-codex-peer-bridge').exists())

    def test_missing_block_is_reported_and_not_recreated(self):
        (self.home / 'AGENTS.md').write_text(self.user)
        _, record = self.reconcile()
        (self.home / 'AGENTS.md').write_text(self.user)
        report, kept = self.reconcile(record=record)
        self.assertEqual(self.states(report), {'AGENTS.md': ('missing', 'kept')})
        self.assertEqual((self.home / 'AGENTS.md').read_text(), self.user)
        self.assertEqual(kept['state'], 'missing')

    def test_target_file_for_each_home_shape(self):
        # Codex with only the user's AGENTS.md: no override is ever created.
        (self.home / 'AGENTS.md').write_text(self.user)
        self.reconcile()
        self.reconcile()
        self.assertFalse((self.home / 'AGENTS.override.md').exists())
        # An existing override is the target; a current block in AGENTS.md is removed.
        (self.home / 'AGENTS.override.md').write_text('Override guidance.\n')
        report, record = self.reconcile()
        self.assertEqual(self.states(report), {'AGENTS.md': ('current', 'removed'),
                                               'AGENTS.override.md': ('absent', 'written')})
        self.assertEqual((self.home / 'AGENTS.md').read_text(), self.user)
        self.assertTrue((self.home / 'AGENTS.override.md').read_text().startswith('Override guidance.\n'))
        self.assertEqual(record['path'], str(self.home / 'AGENTS.override.md'))
        # DeepSeek reads only AGENTS.md, even beside a Codex override.
        report, _ = self.reconcile('deepseek')
        self.assertEqual(self.states(report), {'AGENTS.md': ('absent', 'written')})
        self.assertIn('--agent deepseek', (self.home / 'AGENTS.md').read_text())

    def test_changed_file_since_plan_is_a_conflict(self):
        (self.home / 'AGENTS.md').write_text(self.user)
        report, _ = self.reconcile(expected={str(self.home / 'AGENTS.md'): 'f' * 64})
        self.assertEqual(self.states(report), {'AGENTS.md': ('absent', 'conflict')})
        self.assertEqual((self.home / 'AGENTS.md').read_text(), self.user)


class MigrationTests(unittest.TestCase):
    """An installation written before the runtime guide: no block records at all."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.prefix = self.root / 'app'
        self.prefix.mkdir(mode=0o700)
        self.codex = self.root / 'codex'
        self.dsh = self.root / 'dsh'
        self.codex.mkdir()
        self.dsh.mkdir()
        self.old_python = '/old/python3'
        durable_state.publish(self.prefix / 'install.json', dict(
            state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'), codex=sys.executable,
            codex_home=str(self.codex), dsh_home=str(self.dsh), participants=['codex', 'deepseek']))

    def legacy(self, agent):
        return instructions.legacy_section(self.prefix, agent, self.old_python)

    def upgrade(self):
        config = json.loads((self.prefix / 'install.json').read_text())
        planned = instructions.plan(config, self.prefix)
        return planned, instructions.apply_planned(self.prefix, planned, sys.executable)

    def test_exact_prior_rendering_is_replaced_and_recorded(self):
        for override in (False, True):
            with self.subTest(override=override):
                for path in (self.codex / 'AGENTS.md', self.codex / 'AGENTS.override.md', self.dsh / 'AGENTS.md'):
                    path.unlink(missing_ok=True)
                config = json.loads((self.prefix / 'install.json').read_text())
                config.pop(instructions.RECORD_KEY, None)
                durable_state.publish(self.prefix / 'install.json', config)
                codex = self.codex / ('AGENTS.override.md' if override else 'AGENTS.md')
                codex.write_text('Personal Codex guidance.\n' + self.legacy('codex'))
                (self.dsh / 'AGENTS.md').write_text('Personal harness guidance.\n' + self.legacy('deepseek'))
                planned, outcome = self.upgrade()
                self.assertEqual({e['state'] for e in planned['homes']['codex']['entries']
                                  if e['role'] == 'target'}, {'current'})
                self.assertEqual(codex.read_text(), 'Personal Codex guidance.\n'
                                 + instructions.section(self.prefix, 'codex'))
                self.assertIn('guide --agent deepseek', (self.dsh / 'AGENTS.md').read_text())
                self.assertNotIn('Local peer messaging', (self.dsh / 'AGENTS.md').read_text())
                if not override:
                    self.assertFalse((self.codex / 'AGENTS.override.md').exists())
                record = json.loads((self.prefix / 'install.json').read_text())[instructions.RECORD_KEY]
                self.assertEqual(record['blocks']['codex']['path'], str(codex))
                self.assertEqual(record['blocks']['codex']['state'], 'current')
                self.assertEqual(outcome['codex']['entries'][-1]['action'] if override else
                                 outcome['codex']['entries'][0]['action'], 'written')

    def test_edited_prior_block_is_kept_and_reported(self):
        edited = self.legacy('codex').replace('Local peer messaging', 'My peer messaging')
        (self.codex / 'AGENTS.md').write_text(edited)
        _, outcome = self.upgrade()
        self.assertEqual((self.codex / 'AGENTS.md').read_text(), edited)
        self.assertEqual([(e['state'], e['action']) for e in outcome['codex']['entries']], [('edited', 'kept')])


class InstallCommandTests(unittest.TestCase):
    def install(self, root, *flags):
        env = dict(os.environ, CLAUDE_CONFIG_DIR=str(root / 'claude'))
        for name in ('DSH_SESSION_ID', 'CODEX_THREAD_ID'):
            env.pop(name, None)
        return subprocess.run([sys.executable, str(ROOT / 'scripts/install.py'), '--configure-codex', *flags,
                               '--no-start', '--codex', sys.executable, '--prefix', str(root / 'app'),
                               '--state-dir', str(root / 'state'), '--unit-dir', str(root / 'units'),
                               '--codex-home', str(root / 'codex')],
                              check=True, capture_output=True, text=True, env=env)

    def test_repeat_install_keeps_an_edit_and_replace_restores_the_block(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'codex').mkdir()
            target = root / 'codex' / 'AGENTS.md'
            target.write_text('Personal guidance.\n')
            self.install(root)
            self.assertFalse((root / 'codex' / 'AGENTS.override.md').exists())
            target.write_text(target.read_text().replace('## Koinon', '## Koinon (mine)'))
            edited = target.read_text()
            output = self.install(root).stdout
            self.assertEqual(target.read_text(), edited)
            self.assertIn('"state": "edited"', output)
            self.install(root, '--replace-guidance')
            self.assertNotIn('(mine)', target.read_text())
            record = json.loads((root / 'app' / 'install.json').read_text())[instructions.RECORD_KEY]
            self.assertEqual(record['blocks']['codex']['state'], 'current')


if __name__ == '__main__':
    unittest.main()
