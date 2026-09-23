import argparse
import json
import os
from pathlib import Path
import shlex
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from koinon import claude_statusline as settings
from repo_root import ROOT
import waiting

PYTHON = sys.executable
USER = dict(type='command', command='bash $HOME/.claude/line.sh "two words"', padding=1)


class State:
    """The part of install_state's configuration that the settings module uses."""
    def __init__(self, config=None):
        self.config = dict(config or {})

    def merge(self, updates):
        self.config.update(updates)


class SettingsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.config = self.root / 'claude'
        self.config.mkdir()
        self.path = self.config / 'settings.json'
        self.prefix = ROOT
        self.state = State()

    def tearDown(self):
        self.temp.cleanup()

    def write(self, value, mode=0o644):
        self.path.write_text(json.dumps(value, indent=2) + '\n')
        self.path.chmod(mode)

    def read(self):
        return json.loads(self.path.read_text())

    def set_up(self, **options):
        return settings.set_up(self.state, self.prefix, PYTHON, directory=self.config, **options)

    def test_set_up_wraps_the_existing_command_and_keeps_everything_else(self):
        self.write(dict(model='synthetic', statusLine=USER, hooks=dict(Stop=[])))
        self.assertEqual(self.set_up()['outcome'], 'set_up')
        after = self.read()
        self.assertEqual((after['model'], after['hooks']), ('synthetic', dict(Stop=[])))
        self.assertEqual(after['statusLine']['padding'], 1)
        self.assertEqual(shlex.split(after['statusLine']['command']),
                         [PYTHON, str(self.prefix / 'statusline.py'), '--command', USER['command']])
        self.assertEqual(self.state.config[settings.KEY]['original'], USER)
        self.assertEqual(self.state.config[settings.KEY]['state'], 'enabled')
        self.assertEqual(stat.S_IMODE(self.path.stat().st_mode), 0o644)

    def test_set_up_without_a_status_line_adds_the_wrapper_alone(self):
        self.write(dict(model='synthetic'))
        self.set_up()
        self.assertEqual(shlex.split(self.read()['statusLine']['command']),
                         [PYTHON, str(self.prefix / 'statusline.py')])
        self.assertIsNone(self.state.config[settings.KEY]['original'])

    def test_repeating_set_up_never_nests_or_replaces_the_original(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        first = self.read()
        for explicit in (False, True):
            with self.subTest(explicit=explicit):
                self.assertEqual(self.set_up(explicit=explicit)['outcome'], 'unchanged')
                self.assertEqual(self.read(), first)
                self.assertEqual(self.state.config[settings.KEY]['original'], USER)

    def test_an_interrupted_set_up_keeps_the_saved_original(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        self.state.config[settings.KEY]['state'] = 'pending'
        self.assertEqual(self.set_up()['outcome'], 'unchanged')
        self.assertEqual(self.state.config[settings.KEY]['original'], USER)
        self.assertEqual(self.state.config[settings.KEY]['state'], 'enabled')

    def test_a_decline_is_kept_until_the_user_reverses_it(self):
        self.write(dict(statusLine=USER))
        self.assertEqual(settings.decline(self.state, self.prefix, directory=self.config)['outcome'], 'declined')
        self.assertEqual(self.set_up()['outcome'], 'declined')
        self.assertEqual(self.read()['statusLine'], USER)
        self.assertEqual(self.set_up(explicit=True)['outcome'], 'set_up')
        self.assertTrue(settings.is_wrapper(self.read()['statusLine'], self.prefix))

    def test_declining_an_active_wrapper_removes_it(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        self.assertEqual(settings.decline(self.state, self.prefix, directory=self.config)['outcome'], 'restored')
        self.assertEqual(self.read()['statusLine'], USER)

    def test_removal_restores_the_original_exactly(self):
        for original in (USER, None):
            with self.subTest(original=original):
                self.state = State()
                self.write(dict(model='synthetic', **({} if original is None else dict(statusLine=original))))
                before = self.read()
                self.set_up()
                self.assertEqual(settings.remove(self.state, self.prefix, directory=self.config)['outcome'],
                                 'restored')
                self.assertEqual(self.read(), before)
                self.assertEqual(self.state.config[settings.KEY]['state'], 'declined')

    def test_a_status_line_changed_after_set_up_is_kept_and_reported(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        mine = dict(type='command', command='echo mine')
        self.write(dict(statusLine=mine))
        result = self.set_up()
        self.assertEqual((result['outcome'], result['reason']), ('changed', 'statusline_changed'))
        self.assertIn('--claude-statusline', result['repair'])
        self.assertEqual(self.read()['statusLine'], mine)
        self.assertEqual(settings.remove(self.state, self.prefix, directory=self.config)['outcome'], 'changed')
        self.assertEqual(self.read()['statusLine'], mine)

    def test_a_deleted_status_line_stays_deleted(self):
        self.write(dict(model='synthetic'))
        self.set_up()
        self.write(dict(model='synthetic'))
        self.assertEqual(self.set_up()['outcome'], 'changed')
        self.assertNotIn('statusLine', self.read())
        self.assertEqual(settings.remove(self.state, self.prefix, directory=self.config)['outcome'], 'changed')
        self.assertNotIn('statusLine', self.read())

    def test_an_edited_wrapper_is_kept_by_set_up_and_removal(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        wrapped = self.read()['statusLine']
        for edited in (dict(wrapped, padding=5),
                       settings.wrapper_entry(dict(USER, command='echo edited'), self.prefix, PYTHON)):
            with self.subTest(edited=edited):
                self.write(dict(statusLine=edited))
                for explicit in (False, True):
                    self.assertEqual(self.set_up(explicit=explicit)['outcome'], 'changed')
                    self.assertEqual(self.read()['statusLine'], edited)
        self.assertEqual(settings.remove(self.state, self.prefix, directory=self.config)['outcome'], 'changed')
        self.assertEqual(self.read()['statusLine']['command'],
                         settings.wrapper_entry(dict(USER, command='echo edited'), self.prefix, PYTHON)['command'])

    def test_an_interrupted_set_up_before_the_write_finishes_with_the_original(self):
        self.write(dict(statusLine=USER))
        self.state.config[settings.KEY] = dict(state='pending', settings_file=str(self.path), original=USER,
                                               wrapper=settings.wrapper_entry(USER, self.prefix, PYTHON))
        self.assertEqual(self.set_up()['outcome'], 'set_up')
        self.assertTrue(settings.is_wrapper(self.read()['statusLine'], self.prefix))

    def test_a_wrapper_without_a_record_is_recovered_not_wrapped_again(self):
        self.write(dict(statusLine=settings.wrapper_entry(USER, self.prefix, PYTHON)))
        self.assertEqual(self.set_up()['outcome'], 'unchanged')
        self.assertEqual(self.state.config[settings.KEY]['original'], USER)

    def test_uninstall_refuses_to_delete_a_wrapper_an_edited_entry_still_runs(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        edited = dict(self.read()['statusLine'], padding=5)
        self.write(dict(statusLine=edited))
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            for attempt in ('first', 'retry after the record was declined'):
                with self.subTest(attempt):
                    with self.assertRaises(settings.SettingsError) as caught:
                        settings.release(self.state, self.prefix)
                    self.assertEqual(caught.exception.code, 'statusline_references_runtime')
                    self.assertEqual(self.read()['statusLine'], edited)
            self.write(dict(statusLine=dict(type='command', command='echo unrelated')))
            self.assertIsNone(settings.release(self.state, self.prefix))

    def test_uninstall_recognises_an_escaped_non_ascii_wrapper_path(self):
        prefix = self.root / 'café prefix'
        self.prefix = prefix
        self.write(dict(statusLine=USER))
        self.set_up()
        self.assertIn(b'\\u00e9', self.path.read_bytes())
        edited = dict(self.read()['statusLine'], padding=5)
        self.write(dict(statusLine=edited))
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            with self.assertRaises(settings.SettingsError) as caught:
                settings.release(self.state, prefix)
        self.assertEqual(caught.exception.code, 'statusline_references_runtime')
        self.path.write_text('{"statusLine": ' + json.dumps(edited)[:-1])
        self.assertTrue(settings.references(prefix, self.path))

    def test_text_outside_the_status_line_does_not_block_uninstall(self):
        self.write(dict(statusLine=dict(type='command', command='echo mine'),
                        note=str(self.prefix / 'statusline.py')))
        self.assertFalse(settings.references(self.prefix, self.path))

    def test_uninstall_release_restores_an_untouched_entry(self):
        self.write(dict(statusLine=USER))
        self.set_up()
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            self.assertEqual(settings.release(self.state, self.prefix)['outcome'], 'restored')
        self.assertEqual(self.read()['statusLine'], USER)

    def test_a_concurrent_change_is_a_conflict_that_keeps_the_other_content(self):
        self.write(dict(statusLine=USER))
        other = b'{"statusLine": {"type": "command", "command": "echo other"}}\n'
        real = settings._current

        def racing(path):
            self.path.write_bytes(other)
            return real(path)
        with mock.patch.object(settings, '_current', side_effect=racing):
            with self.assertRaises(settings.SettingsError) as caught:
                self.set_up()
        self.assertEqual(caught.exception.code, 'settings_conflict')
        self.assertEqual(self.path.read_bytes(), other)
        self.assertFalse((self.config / '.settings.json.koinon').exists())

    def test_unsafe_or_malformed_settings_are_never_written(self):
        for name, prepare, code in (
                ('malformed', lambda: self.path.write_text('{not json'), 'settings_invalid'),
                ('not an object', lambda: self.path.write_text('[]'), 'settings_invalid'),
                ('symlink', lambda: (self.root.joinpath('real.json').write_text('{}'),
                                     self.path.symlink_to(self.root / 'real.json')), 'settings_symlink')):
            with self.subTest(name):
                self.path.unlink(missing_ok=True)
                prepare()
                before = self.path.read_bytes()
                with self.assertRaises(settings.SettingsError) as caught:
                    self.set_up()
                self.assertEqual(caught.exception.code, code)
                self.assertEqual(self.path.read_bytes(), before)

    def test_no_claude_configuration_means_nothing_to_set_up(self):
        result = settings.set_up(self.state, self.prefix, PYTHON, directory=self.root / 'absent')
        self.assertEqual((result['outcome'], result['reason']), ('skipped', 'claude_config_missing'))
        self.assertFalse((self.root / 'absent').exists())

    def test_the_wrapper_entry_runs_the_original_with_the_same_result(self):
        (self.root / 'line.sh').write_text('printf "%s|%s\\n" "$1" "$(cat | wc -c | tr -d " ")"\n')
        env = dict(os.environ, HOME=str(self.root), CLAUDE_CONFIG_DIR=str(self.config))
        for command in ('bash $HOME/line.sh "two words"',
                        'cat | sh "$HOME/line.sh" \'single quoted\' | tr a-z A-Z'):
            with self.subTest(command):
                entry = settings.wrapper_entry(dict(type='command', command=command), self.prefix, PYTHON)
                results = [subprocess.run(['/bin/sh', '-c', line], input=b'{"session_id": "s"}',
                                          capture_output=True, env=env, timeout=waiting.timeout())
                           for line in (command, entry['command'])]
                self.assertEqual([(r.returncode, r.stdout, r.stderr) for r in results][0],
                                 [(r.returncode, r.stdout, r.stderr) for r in results][1])


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.config = self.root / 'claude'
        self.config.mkdir(mode=0o700)
        (self.config / 'settings.json').write_text(json.dumps(dict(statusLine=USER)))
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        (self.prefix / 'koinon').symlink_to(ROOT / 'koinon')
        (self.prefix / 'statusline.py').write_text('')
        (self.prefix / 'install.json').write_text(json.dumps(dict(state_root=str(self.root / 'state'),
                                                                  unit_dir=str(self.root / 'units'))))
        (self.prefix / 'install.json').chmod(0o600)
        self.env = dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config))

    def tearDown(self):
        self.temp.cleanup()

    def installer(self, *arguments):
        return subprocess.run([sys.executable, str(ROOT / 'scripts' / 'install.py'), '--prefix', str(self.prefix),
                               *arguments], capture_output=True, text=True, env=self.env, timeout=waiting.timeout())

    def test_modes_set_up_and_remove_the_wrapper(self):
        result = self.installer('--claude-statusline')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['result']['outcome'], 'set_up')
        line = json.loads((self.config / 'settings.json').read_text())['statusLine']
        self.assertTrue(settings.is_wrapper(line, self.prefix.resolve()))
        result = self.installer('--remove-claude-statusline')
        self.assertEqual(json.loads(result.stdout)['result']['outcome'], 'restored')
        self.assertEqual(json.loads((self.config / 'settings.json').read_text())['statusLine'], USER)
        record = json.loads((self.prefix / 'install.json').read_text())[settings.KEY]
        self.assertEqual(record['state'], 'declined')

    def test_an_invalid_saved_selection_is_refused(self):
        config = json.loads((self.prefix / 'install.json').read_text())
        config[settings.KEY] = dict(state='enabled', settings_file='relative', original=None, wrapper={})
        (self.prefix / 'install.json').write_text(json.dumps(config))
        result = self.installer('--claude-statusline')
        self.assertEqual(json.loads(result.stdout)['code'], 'invalid_install_configuration')

    def test_modes_refuse_other_options(self):
        result = self.installer('--claude-statusline', '--repo', str(self.root))
        self.assertEqual(result.returncode, 2)
        self.assertIn('take only --prefix', result.stderr)

    def test_a_staged_installation_leaves_claude_settings_alone(self):
        sys.path.insert(0, str(ROOT / 'scripts'))
        try:
            import install
        finally:
            sys.path.remove(str(ROOT / 'scripts'))
        before = (self.config / 'settings.json').read_bytes()
        options = argparse.Namespace(no_start=True, no_claude_statusline=False, prefix=self.prefix)
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.config)):
            result = install.claude_status_line(options, State())
        self.assertEqual((result['outcome'], result['reason']), ('skipped', 'no_start'))
        self.assertEqual((self.config / 'settings.json').read_bytes(), before)


class UpgradeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.config = self.root / 'claude'
        self.config.mkdir(mode=0o700)
        self.path = self.config / 'settings.json'
        self.path.write_text(json.dumps(dict(statusLine=USER)))
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        self.install = self.prefix / 'install.json'
        self.install.write_text(json.dumps(dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'))))
        self.install.chmod(0o600)

    def tearDown(self):
        self.temp.cleanup()

    def plan(self):
        return settings.plan(json.loads(self.install.read_text()), self.config)

    def record(self):
        return json.loads(self.install.read_text()).get(settings.KEY)

    def test_an_upgrade_sets_up_the_wrapper_and_saves_the_original(self):
        planned = self.plan()
        self.assertEqual(planned['action'], 'set_up')
        result = settings.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual(result['outcome'], 'set_up')
        self.assertTrue(settings.is_wrapper(json.loads(self.path.read_text())['statusLine'], self.prefix))
        self.assertEqual(self.record()['original'], USER)
        # The same plan again, as after a crash before its outcome was kept: Koinon's own
        # write is recognised, not reported as a conflict.
        self.assertEqual(settings.apply_planned(self.prefix, planned, PYTHON)['outcome'], 'unchanged')
        self.assertEqual(settings.apply_planned(self.prefix, self.plan(), PYTHON)['outcome'], 'unchanged')
        edited = json.loads(self.path.read_text())
        edited['statusLine'] = dict(edited['statusLine'], padding=9)
        self.path.write_text(json.dumps(edited))
        self.assertEqual(settings.apply_planned(self.prefix, planned, PYTHON)['outcome'], 'conflict')

    def test_a_settings_change_during_the_upgrade_is_a_conflict_that_writes_nothing(self):
        planned = self.plan()
        self.path.write_text(json.dumps(dict(statusLine=USER, model='changed')))
        before = self.path.read_bytes()
        result = settings.apply_planned(self.prefix, planned, PYTHON)
        self.assertEqual((result['outcome'], result['code']), ('conflict', 'settings_conflict'))
        self.assertIn('--claude-statusline', result['repair'])
        self.assertEqual(self.path.read_bytes(), before)
        self.assertIsNone(self.record())

    def test_a_saved_decline_survives_an_upgrade(self):
        config = json.loads(self.install.read_text())
        config[settings.KEY] = dict(state='declined', settings_file=str(self.path), original=None, wrapper=None)
        self.install.write_text(json.dumps(config))
        planned = self.plan()
        self.assertEqual(planned['action'], 'declined')
        self.assertEqual(settings.apply_planned(self.prefix, planned, PYTHON), planned)
        self.assertEqual(json.loads(self.path.read_text())['statusLine'], USER)

    def test_unreadable_settings_are_skipped_not_a_failed_upgrade(self):
        self.path.write_text('{not json')
        planned = self.plan()
        self.assertEqual((planned['action'], planned['reason']), ('skipped', 'settings_invalid'))
        self.assertEqual(settings.apply_planned(self.prefix, planned, PYTHON), planned)
        missing = settings.plan({}, self.root / 'absent')
        self.assertEqual((missing['action'], missing['reason']), ('skipped', 'claude_config_missing'))
