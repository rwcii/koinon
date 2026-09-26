import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

from koinon import claude_guidance as claude
from koinon import claude_statusline, participant_instructions, runtime_names
from repo_root import ROOT
import waiting

PYTHON = sys.executable
USER_HOOK = dict(matcher='startup', hooks=[dict(type='command', command='echo user-hook')])
USER_SETTINGS = dict(model='synthetic', statusLine=dict(type='command', command='echo line'),
                     hooks=dict(SessionStart=[USER_HOOK], Stop=[dict(hooks=[dict(type='command', command='true')])]))


class State:
    """The part of install_state's configuration that the guidance module uses."""
    def __init__(self, config=None):
        self.config = dict(config or {})

    def merge(self, updates):
        self.config.update(updates)


class Fixture(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.config = self.root / 'claude config'
        self.config.mkdir(mode=0o700)
        self.settings = self.config / 'settings.json'
        self.claude_md = self.config / 'CLAUDE.md'
        self.prefix = ROOT
        self.state = State()

    def write(self, value):
        self.settings.write_text(json.dumps(value, indent=2) + '\n')
        self.settings.chmod(0o600)

    def read(self):
        return json.loads(self.settings.read_text())

    def set_up(self, **options):
        return claude.set_up(self.state, self.prefix, PYTHON, directory=self.config, **options)

    def remove(self):
        return claude.remove(self.state, self.prefix, directory=self.config, python=PYTHON)

    def hooks(self):
        return [hook for group in self.read().get('hooks', {}).get('SessionStart', [])
                for hook in group.get('hooks', []) if hook.get('command') == claude.hook_command(self.prefix, PYTHON)]

    def without_koinon(self, value):
        """The settings with Koinon's hook group taken out, to compare everything else."""
        value = json.loads(json.dumps(value))
        groups = value.get('hooks', {}).get('SessionStart', [])
        value.get('hooks', {})['SessionStart'] = [
            group for group in groups if group != dict(hooks=[claude.hook_entry(self.prefix, PYTHON)])]
        return value


class SetUpTests(Fixture):
    def test_set_up_adds_one_block_and_one_hook_and_keeps_everything_else(self):
        for name, settings, text in (('empty', None, None),
                                     ('existing', USER_SETTINGS, '# My rules\n\nKeep this.\n')):
            with self.subTest(name):
                self.settings.unlink(missing_ok=True)
                self.claude_md.unlink(missing_ok=True)
                self.state = State()
                if settings is not None:
                    self.write(settings)
                if text is not None:
                    self.claude_md.write_text(text)
                result = self.set_up()
                self.assertEqual(result['outcome'], 'set_up')
                self.assertEqual(self.hooks(), [claude.hook_entry(self.prefix, PYTHON)])
                content = self.claude_md.read_text()
                self.assertEqual(content.count('<!-- BEGIN KOINON CLAUDE -->'), 1)
                self.assertIn(participant_instructions.section(self.prefix, 'claude', PYTHON), content)
                if text is not None:
                    self.assertTrue(content.startswith(text))
                    after = self.without_koinon(self.read())
                    self.assertEqual(after, USER_SETTINGS)
                # A repeated installation or upgrade adds no duplicate.
                before = (self.settings.read_bytes(), self.claude_md.read_bytes())
                self.assertEqual(self.set_up()['outcome'], 'unchanged')
                self.assertEqual((self.settings.read_bytes(), self.claude_md.read_bytes()), before)
                # Removal restores both files to what they were.
                self.assertEqual(self.remove()['outcome'], 'removed')
                if settings is None:
                    self.assertEqual(self.read(), {})
                    self.assertFalse(self.claude_md.read_text())
                else:
                    self.assertEqual(self.read(), USER_SETTINGS)
                    self.assertEqual(self.claude_md.read_text(), text)
                self.assertEqual(self.state.config[claude.KEY]['state'], 'declined')
                self.assertNotIn('claude', self.state.config[participant_instructions.RECORD_KEY]['blocks'])

    def test_the_hook_runs_the_brief_guide_and_reports_a_removed_runtime(self):
        self.set_up()
        command = self.read()['hooks']['SessionStart'][0]['hooks'][0]['command']
        self.assertEqual(self.read()['hooks']['SessionStart'][0]['hooks'][0]['timeout'], 10)
        gone = self.root / 'removed prefix'
        missing = command.replace(str(self.prefix), str(gone))
        started = time.monotonic()
        result = subprocess.run(['/bin/sh', '-c', missing], capture_output=True, text=True,
                                timeout=claude.TIMEOUT, stdin=subprocess.DEVNULL)
        self.assertLess(time.monotonic() - started, claude.TIMEOUT)
        self.assertEqual((result.returncode, result.stdout),
                         (0, f'Koinon guidance unavailable: {gone}/session.py is missing\n'))
        result = subprocess.run(['/bin/sh', '-c', command], capture_output=True, text=True,
                                input=json.dumps(dict(session_id='synthetic-session')),
                                env=dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)),
                                timeout=waiting.timeout())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(result.stdout.startswith('Koinon guidance for claude'))
        self.assertLessEqual(len(result.stdout.splitlines()), 40)

    def test_a_saved_decline_is_kept_until_an_explicit_set_up(self):
        self.write(USER_SETTINGS)
        self.assertEqual(claude.decline(self.state, self.prefix, directory=self.config)['outcome'], 'declined')
        self.assertEqual(self.set_up()['outcome'], 'declined')
        self.assertEqual(self.read(), USER_SETTINGS)
        self.assertFalse(self.claude_md.exists())
        self.assertEqual(self.set_up(explicit=True)['outcome'], 'set_up')
        self.assertEqual(len(self.hooks()), 1)
        result = claude.decline(self.state, self.prefix, directory=self.config)
        self.assertEqual((result['action'], result['outcome']), ('decline', 'removed'))
        self.assertEqual(self.read(), USER_SETTINGS)

    def test_edited_entries_are_kept_and_reported(self):
        self.write(USER_SETTINGS)
        self.set_up()
        settings = self.read()
        settings['hooks']['SessionStart'][1]['hooks'][0]['timeout'] = 30
        self.write(settings)
        self.claude_md.write_text(self.claude_md.read_text().replace('## Koinon', '## Koinon (mine)'))
        result = self.set_up()
        self.assertEqual((result['outcome'], result['reason']), ('changed', 'hook_changed'))
        self.assertIn('--claude-guidance', result['repair'])
        self.assertEqual([entry['state'] for entry in result['block']], ['edited'])
        self.assertEqual(self.read(), settings)
        edited = self.claude_md.read_bytes()
        result = self.remove()
        self.assertEqual((result['outcome'], result['reason']), ('changed', 'hook_changed'))
        self.assertEqual([(entry['state'], entry['action']) for entry in result['block']], [('edited', 'kept')])
        self.assertEqual(self.read(), settings)
        self.assertEqual(self.claude_md.read_bytes(), edited)

    def test_a_removed_hook_is_not_recreated_unless_asked(self):
        self.write(USER_SETTINGS)
        self.set_up()
        self.write(USER_SETTINGS)
        self.claude_md.write_text('')
        result = self.set_up()
        self.assertEqual(result['outcome'], 'changed')
        self.assertEqual([entry['state'] for entry in result['block']], ['missing'])
        self.assertEqual((self.read(), self.claude_md.read_text()), (USER_SETTINGS, ''))
        result = self.set_up(explicit=True)
        self.assertEqual(result['outcome'], 'set_up')
        self.assertEqual(len(self.hooks()), 1)
        self.assertEqual(self.claude_md.read_text().count('BEGIN KOINON CLAUDE'), 1)

    def test_a_new_runtime_path_refreshes_the_hook_in_place(self):
        self.write(USER_SETTINGS)
        self.set_up()
        moved = self.root / 'other prefix'
        result = claude.set_up(self.state, moved, PYTHON, directory=self.config)
        self.assertEqual(result['outcome'], 'set_up')
        groups = self.read()['hooks']['SessionStart']
        self.assertEqual(groups, [USER_HOOK, dict(hooks=[claude.hook_entry(moved, PYTHON)])])
        self.assertIn(str(moved / 'session.py'), self.claude_md.read_text())
        self.assertNotIn(str(self.prefix / 'session.py'), self.claude_md.read_text())

    def test_other_hooks_in_koinon_group_survive_removal(self):
        self.write(USER_SETTINGS)
        self.set_up()
        settings = self.read()
        settings['hooks']['SessionStart'][1]['hooks'].append(dict(type='command', command='echo added'))
        self.write(settings)
        self.assertEqual(self.remove()['outcome'], 'removed')
        self.assertEqual(self.read()['hooks']['SessionStart'],
                         [USER_HOOK, dict(hooks=[dict(type='command', command='echo added')])])

    def test_settings_edits_reuse_the_status_line_conflict_detection(self):
        self.write(USER_SETTINGS)
        before = self.settings.read_bytes()
        real = claude_statusline._current
        with mock.patch.object(claude_statusline, '_current',
                               side_effect=lambda path: b'{}' if path == self.settings else real(path)):
            with self.assertRaises(claude_statusline.SettingsError) as caught:
                self.set_up()
        self.assertEqual(caught.exception.code, 'settings_conflict')
        self.assertEqual(self.settings.read_bytes(), before)

    def test_unsafe_settings_shapes_are_refused_unchanged(self):
        for name, value in (('hooks', dict(hooks=[])), ('event', dict(hooks=dict(SessionStart={})))):
            with self.subTest(name):
                self.state = State()
                self.write(value)
                with self.assertRaises(claude_statusline.SettingsError) as caught:
                    self.set_up()
                self.assertEqual(caught.exception.code, 'settings_invalid')
                self.assertEqual(self.read(), value)

    def test_no_claude_configuration_means_nothing_to_set_up(self):
        result = claude.set_up(self.state, self.prefix, PYTHON, directory=self.root / 'absent')
        self.assertEqual((result['outcome'], result['reason']), ('skipped', 'claude_config_missing'))
        self.assertFalse((self.root / 'absent').exists())

    def test_the_configuration_directory_is_one_for_block_hook_and_registry(self):
        from koinon import participant_status
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            result = claude.set_up(self.state, self.prefix, PYTHON)
            self.assertEqual(participant_status.registry_directory(), self.config / 'sessions')
        self.assertEqual(result['settings_file'], str(self.settings))
        self.assertEqual([entry['path'] for entry in result['block']], [str(self.claude_md)])
        self.assertEqual(len(self.hooks()), 1)

    def test_the_saved_selection_is_validated(self):
        self.set_up()
        base = dict(state_root='/state', unit_dir='/units')
        runtime_names.validate_install_config(dict(base, **self.state.config))
        for broken in (dict(self.state.config[claude.KEY], state='on'),
                       dict(self.state.config[claude.KEY], settings_file='relative'),
                       dict(self.state.config[claude.KEY], hook=None),
                       dict(self.state.config[claude.KEY], created=['other'])):
            with self.assertRaises(ValueError):
                runtime_names.validate_install_config(dict(base, claude_guidance=broken))


class UpgradeTests(Fixture):
    def setUp(self):
        super().setUp()
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        self.install = self.prefix / 'install.json'
        self.install.write_text(json.dumps(dict(state_root=str(self.root / 'state'),
                                                unit_dir=str(self.root / 'units'))))
        self.install.chmod(0o600)
        self.write(USER_SETTINGS)

    def plan(self):
        return claude.plan(json.loads(self.install.read_text()), self.config, prefix=self.prefix)

    def test_an_upgrade_sets_up_both_after_the_status_line_step(self):
        planned = self.plan()
        self.assertEqual(planned['action'], 'set_up')
        # The status-line step of the same upgrade runs first and changes statusLine.
        self.write(dict(USER_SETTINGS, statusLine=dict(type='command', command='wrapped')))
        result = claude.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual(result['outcome'], 'set_up')
        self.assertEqual(len(self.hooks()), 1)
        saved = json.loads(self.install.read_text())
        self.assertEqual(saved[claude.KEY]['state'], 'enabled')
        self.assertEqual(saved[participant_instructions.RECORD_KEY]['blocks']['claude']['state'], 'current')
        # The same plan again, as after a crash before its outcome was kept.
        self.assertEqual(claude.apply_planned(self.prefix, planned, PYTHON)['outcome'], 'unchanged')
        self.assertEqual(claude.apply_planned(self.prefix, self.plan(), PYTHON)['outcome'], 'unchanged')
        self.assertEqual(len(self.hooks()), 1)
        # An independent edit after the interrupted write is still a conflict on resume.
        changed = dict(self.read(), model='changed after the write')
        self.write(changed)
        result = claude.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual((result['outcome'], result['code']), ('conflict', 'settings_conflict'))
        self.assertEqual(self.read(), changed)

    def test_a_settings_change_during_the_upgrade_is_a_conflict(self):
        planned = self.plan()
        self.write(dict(USER_SETTINGS, model='changed'))
        before = self.settings.read_bytes()
        result = claude.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual((result['outcome'], result['code']), ('conflict', 'settings_conflict'))
        self.assertIn('--claude-guidance', result['repair'])
        self.assertEqual(self.settings.read_bytes(), before)
        self.assertFalse(self.claude_md.exists())

    def test_an_existing_hook_is_no_excuse_for_a_change_after_preflight(self):
        for name, old, new in (('same interpreter', PYTHON, PYTHON),
                               ('new interpreter', '/old/python3', PYTHON)):
            with self.subTest(name):
                self.write(USER_SETTINGS)
                self.claude_md.unlink(missing_ok=True)
                state = State(dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units')))
                claude.set_up(state, self.prefix, old, directory=self.config)
                self.install.write_text(json.dumps(state.config))
                planned = claude.plan(state.config, self.config, prefix=self.prefix, python=new)
                changed = dict(self.read(), model='changed after preflight')
                self.write(changed)
                result = claude.apply_planned(self.prefix, planned, new)
                self.assertEqual((result['outcome'], result['code']), ('conflict', 'settings_conflict'))
                self.assertEqual(self.read(), changed)

    def test_a_saved_decline_survives_an_upgrade(self):
        config = json.loads(self.install.read_text())
        config[claude.KEY] = dict(state='declined', settings_file=str(self.settings), hook=None, created=[])
        self.install.write_text(json.dumps(config))
        planned = self.plan()
        self.assertEqual(planned['action'], 'declined')
        self.assertEqual(claude.apply_planned(self.prefix, planned, PYTHON), planned)
        self.assertEqual(self.read(), USER_SETTINGS)
        self.assertFalse(self.claude_md.exists())

    def test_an_edited_block_changed_during_the_upgrade_is_a_conflict(self):
        state = State(json.loads(self.install.read_text()))
        claude.set_up(state, self.prefix, PYTHON, directory=self.config)
        self.install.write_text(json.dumps(state.config))
        planned = self.plan()
        edited = self.claude_md.read_text().replace('## Koinon', '## Koinon (mine)')
        self.claude_md.write_text(edited)
        result = claude.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual([entry['action'] for entry in result['block']], ['conflict'])
        self.assertEqual(self.claude_md.read_text(), edited)

    def test_unreadable_settings_are_skipped(self):
        self.settings.write_text('{not json')
        planned = self.plan()
        self.assertEqual((planned['action'], planned['reason']), ('skipped', 'settings_invalid'))
        self.assertEqual(claude.apply_planned(self.prefix, planned, PYTHON), planned)


class InstallerTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.config = self.root / 'claude'
        self.config.mkdir(mode=0o700)
        self.settings = self.config / 'settings.json'
        self.settings.write_text(json.dumps(USER_SETTINGS))
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        (self.prefix / 'koinon').symlink_to(ROOT / 'koinon')
        (self.prefix / 'statusline.py').write_text('')
        (self.prefix / 'install.json').write_text(json.dumps(dict(state_root=str(self.root / 'state'),
                                                                  unit_dir=str(self.root / 'units'))))
        (self.prefix / 'install.json').chmod(0o600)
        self.env = dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config))

    def installer(self, *arguments):
        return subprocess.run([sys.executable, str(ROOT / 'scripts' / 'install.py'), '--prefix', str(self.prefix),
                               *arguments], capture_output=True, text=True, env=self.env, timeout=waiting.timeout())

    def test_modes_set_up_and_remove_the_guidance(self):
        result = self.installer('--claude-guidance')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['result']['outcome'], 'set_up')
        self.assertIn('BEGIN KOINON CLAUDE', (self.config / 'CLAUDE.md').read_text())
        result = self.installer('--remove-claude-guidance')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['result']['outcome'], 'removed')
        self.assertEqual(json.loads(self.settings.read_text()), USER_SETTINGS)
        self.assertEqual(json.loads((self.prefix / 'install.json').read_text())[claude.KEY]['state'], 'declined')

    def test_modes_refuse_other_options_and_an_older_runtime(self):
        result = self.installer('--claude-guidance', '--repo', str(self.root))
        self.assertEqual(result.returncode, 2)
        self.assertIn('take only --prefix', result.stderr)
        result = self.installer('--remove-claude-guidance', '--replace-guidance')
        self.assertEqual(result.returncode, 2)
        (self.prefix / 'koinon').unlink()
        (self.prefix / 'koinon').mkdir()
        result = self.installer('--claude-guidance')
        self.assertEqual(result.returncode, 2)
        self.assertIn('predates Claude guidance', result.stderr)

    def test_a_malformed_block_is_reported_and_kept(self):
        malformed = '<!-- BEGIN KOINON CLAUDE -->\nno end\n'
        (self.config / 'CLAUDE.md').write_text(malformed)
        result = self.installer('--claude-guidance')
        self.assertEqual(result.returncode, 0, result.stderr)
        block = json.loads(result.stdout)['result']['block']
        self.assertEqual([(entry['state'], entry['action']) for entry in block], [('malformed', 'kept')])
        self.assertEqual((self.config / 'CLAUDE.md').read_text(), malformed)

    def test_installation_declines_or_skips_without_touching_settings(self):
        sys.path.insert(0, str(ROOT / 'scripts'))
        try:
            import install
        finally:
            sys.path.remove(str(ROOT / 'scripts'))
        before = self.settings.read_bytes()
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            staged = install.claude_guidance_setup(
                argparse.Namespace(no_start=True, no_claude_guidance=False, replace_guidance=False,
                                   prefix=self.prefix), State())
            state = State()
            declined = install.claude_guidance_setup(
                argparse.Namespace(no_start=False, no_claude_guidance=True, replace_guidance=False,
                                   prefix=self.prefix), state)
        self.assertEqual((staged['outcome'], staged['reason']), ('skipped', 'no_start'))
        self.assertEqual(declined['outcome'], 'declined')
        self.assertEqual(state.config[claude.KEY]['state'], 'declined')
        self.assertEqual(self.settings.read_bytes(), before)
        self.assertFalse((self.config / 'CLAUDE.md').exists())


class GuideTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        (self.root / 'claude' / 'sessions').mkdir(parents=True)
        (self.root / 'claude' / 'sessions' / '4242.json').write_text(
            json.dumps(dict(pid=4242, sessionId='hook-session', name='synthetic-claude-peer')))
        self.env = dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        self.env.pop('CLAUDE_CODE_SESSION_ID', None)

    def guide(self, **options):
        return subprocess.run([sys.executable, str(ROOT / 'session.py'), 'guide', '--agent', 'claude', '--brief',
                               '--json'], capture_output=True, text=True, env=self.env,
                              timeout=waiting.timeout(), **options)

    def test_the_brief_names_the_peer_from_the_hook_input(self):
        result = self.guide(input=json.dumps(dict(session_id='hook-session', hook_event_name='SessionStart')))
        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual(value['observations']['registration'],
                         dict(state='observed', name='synthetic-claude-peer', source='claude_registry'))
        self.assertEqual([topic['topic'] for topic in value['topics']],
                         ['overview', 'peers', 'reconnect', 'messages'])
        text = ' '.join(filter(None, (entry for topic in value['topics'] for entry in (topic['text'], topic['view']))))
        for fact in ('agent listing', '/clear', 'CLAUDE_CODE_SESSION_ID', "user's approval"):
            self.assertIn(fact, text)
        self.assertEqual([recipe['id'] for topic in value['topics'] for recipe in topic['recipes']], ['launch_codex', 'peers'])

    def test_an_open_stdin_without_input_does_not_hold_the_guide(self):
        process = subprocess.Popen([sys.executable, str(ROOT / 'session.py'), 'guide', '--agent', 'claude',
                                    '--brief', '--json'], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True, env=self.env)
        try:
            out, err = process.communicate(timeout=claude.TIMEOUT)
        finally:
            process.kill()
        self.assertEqual(process.returncode, 0, err)
        self.assertEqual(json.loads(out)['observations']['registration']['reason'], 'session_id_missing')


class UninstallTests(unittest.TestCase):
    def test_uninstall_releases_the_guidance_before_the_status_line(self):
        source = (ROOT / 'scripts' / 'uninstall.py').read_text()
        self.assertLess(source.index('claude_guidance.release(state, prefix)'),
                        source.index('claude_statusline.release(state, prefix)'))
        self.assertLess(source.index('claude_statusline.release(state, prefix)'),
                        source.index('uninstall_finalize.prepare(prefix, FILES)'))

    def test_release_removes_owned_entries_and_does_nothing_when_never_set_up(self):
        with tempfile.TemporaryDirectory() as temporary:
            config = Path(temporary) / 'claude'
            config.mkdir()
            (config / 'settings.json').write_text(json.dumps(USER_SETTINGS))
            state = State()
            with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(config)):
                self.assertIsNone(claude.release(state, ROOT))
                claude.set_up(state, ROOT, PYTHON)
                result = claude.release(state, ROOT)
            self.assertEqual(result['outcome'], 'removed')
            self.assertEqual(json.loads((config / 'settings.json').read_text()), USER_SETTINGS)
            self.assertNotIn('KOINON', (config / 'CLAUDE.md').read_text())


if __name__ == '__main__':
    unittest.main()
