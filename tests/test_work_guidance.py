"""Synthetic guidance publication, recovery and removal; never live agent files."""
import waiting
import copy
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from koinon import install_state
import memory
from koinon import participant_instructions as instructions
from koinon import runtime_names
from koinon import work_guidance as guidance
from koinon import work_policy


class GuidanceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix = self.root/'installed runtime'
        self.prefix.mkdir(mode=0o700)
        for name in ('session.py', 'memory.py', 'koinon/work_policy.py', 'koinon/work_guidance.py'):
            (self.prefix/name).parent.mkdir(parents=True, exist_ok=True)
            (self.prefix/name).write_text('# synthetic installation\n')
            (self.prefix/name).chmod(0o600)
        self.repo = self.new_repo('repository one')
        self.common = memory.repo_common_directory(self.repo)
        self.repo_key = work_policy.repository_key(self.common)
        self.key = self.repo_key+':codex'
        self.target = self.root/'synthetic guidance.txt'
        self.original = b'Outside text\r\nPreserve this exactly: \xc3\xa9\r\n'
        self.target.write_bytes(self.original)
        self.initial = dict(state_root=str(self.root/'private state'), unit_dir=str(self.root/'units'),
                            participants=[], unknown={'preserve': [1, 2]})
        self.save(self.initial)

    def new_repo(self, name):
        repo = self.root/name
        repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(repo)], check=True)
        return repo

    def save(self, config):
        path = self.prefix/'install.json'
        path.write_text(json.dumps(config))
        path.chmod(0o600)

    def config(self):
        return runtime_names.install_config(self.prefix)

    def rule(self):
        return self.config().get('work_items', {'rules': {}})['rules'].get(self.key)

    def enable(self, agent='codex', repo=None, target=None):
        return guidance.configure(self.prefix, repo or self.repo, agent, target or self.target)

    def remove(self, agent='codex', repo=None):
        return guidance.remove(self.prefix, repo or self.repo, agent)

    def query(self, agent='codex'):
        return work_policy.query(self.config(), self.repo, agent)

    def backup(self):
        return guidance.backup_path(self.prefix, self.config(), self.target)

    def test_three_participants_repeat_and_exact_removal(self):
        for agent in work_policy.PARTICIPANTS:
            self.enable(agent)
            first = self.target.read_bytes()
            self.assertTrue(self.query(agent)['enabled'])
            self.enable(agent)
            self.assertEqual(self.target.read_bytes(), first)
        self.assertEqual(self.backup().read_bytes(), self.original)
        self.assertEqual(self.backup().stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.backup().parent.stat().st_mode & 0o777, 0o700)
        for agent in work_policy.PARTICIPANTS:
            self.remove(agent)
            self.remove(agent)
            self.assertFalse(self.query(agent)['enabled'])
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertFalse(self.backup().exists())
        self.assertEqual(self.config()['unknown'], self.initial['unknown'])
        self.assertTrue((self.prefix/install_state.LOCK_NAME).exists())
        self.assertEqual(len(list(self.root.glob('.koinon-work-*.lock'))), 1)

    def test_multiple_repositories_one_file_preserve_other_selection(self):
        other = self.new_repo('repository two')
        self.enable()
        self.enable('claude', other)
        other_key = work_policy.repository_key(memory.repo_common_directory(other))+':claude'
        before = instructions.read_guidance(self.target)
        other_span = guidance.sections(before)[other_key]
        other_section = before[slice(*other_span)]
        self.remove()
        after = instructions.read_guidance(self.target)
        self.assertEqual(after[slice(*guidance.sections(after)[other_key])], other_section)
        self.assertTrue(self.backup().exists())
        self.assertTrue(work_policy.query(self.config(), other, 'claude')['enabled'])
        self.remove('claude', other)
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_render_quotes_real_paths_and_keeps_workflow_conditional(self):
        section = guidance.render(self.prefix, self.common, self.repo_key, 'claude')
        command = section.split('```sh\n')[1].split('\n```')[0]
        self.assertEqual(shlex.split(command), [sys.executable, str(self.prefix/'session.py'),
                        'work-policy', '--repo', str(self.common), '--agent', 'claude'])
        memory_command = section.split('```sh\n')[2].split('\n```')[0]
        self.assertEqual(shlex.split(memory_command), [sys.executable, str(self.prefix/'memory.py'),
                        '--repo-path', str(self.common), '--consumer', '<stable-consumer-key>'])
        self.assertIn('stable participant session key', section)
        self.assertIn('reconcile current work', section)
        self.assertIn('Configuration never grants permission', section)
        self.assertEqual(set(instructions.MARKERS), {'codex', 'deepseek', 'claude'})

    def test_missing_installation_and_explicit_path_fail_before_writes(self):
        (self.prefix/'koinon/work_guidance.py').unlink()
        with self.assertRaises(guidance.GuidanceError):
            self.enable()
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertFalse((self.prefix/install_state.LOCK_NAME).exists())
        self.assertFalse((self.root/'private state').exists())
        result = subprocess.run([sys.executable, 'scripts/install.py', '--configure-work-items',
                                 '--prefix', str(self.prefix), '--repo', str(self.repo),
                                 '--participant', 'codex'], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)

    def test_parser_rejects_malformed_duplicate_nested_and_peer_overlap(self):
        section = guidance.render(self.prefix, self.common, self.repo_key, 'codex')
        peer = instructions.section(self.prefix)
        adjacent = section + guidance.render(self.prefix, self.common, self.repo_key, 'claude').lstrip('\n')
        samples = [section+section, adjacent, section.rstrip('\n'),
                   section.replace('<!-- END', section+'<!-- END', 1),
                   section.replace('KOINON WORK ITEMS', 'KOINON WORK ITEMS malformed', 1),
                   peer.replace('## Koinon\n', section+'## Koinon\n')]
        for text in samples:
            with self.subTest(text=text[:90]), self.assertRaises(ValueError):
                guidance.sections(text)

    def test_tampered_enabled_query_fails_closed_without_writes(self):
        self.enable()
        self.target.write_bytes(self.target.read_bytes().replace(b'explicitly start', b'implicitly start'))
        config = (self.prefix/'install.json').read_bytes()
        result = self.query()
        self.assertEqual((result['enabled'], result['state'], result['reason']),
                         (False, 'disabled', 'guidance_unverified'))
        self.assertEqual((self.prefix/'install.json').read_bytes(), config)
        for state in ('disabled', 'pending'):
            rule = dict(self.rule(), state=state)
            if state == 'pending':
                rule.update(before_digest='a'*64, after_digest='b'*64)
            config = self.config()
            config['work_items']['rules'][self.key] = rule
            self.save(config)
            with patch.object(guidance, 'verify', side_effect=AssertionError('target read')):
                self.assertFalse(self.query()['enabled'])

    def test_backup_retry_reconfirms_before_pending_publication(self):
        with patch.object(guidance.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertIsNone(self.rule())
        self.assertEqual(self.target.read_bytes(), self.original)
        with patch.object(instructions, 'confirm_guidance', side_effect=OSError('retry flush')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertIsNone(self.rule())
        self.enable()
        self.assertEqual(self.backup().read_bytes(), self.original)

    def test_pending_visible_guidance_requires_flush_before_enabling(self):
        confirm = instructions.confirm_guidance
        def fail_target(path, **kwargs):
            if path == self.target:
                raise OSError('target flush')
            return confirm(path, **kwargs)
        with patch.object(instructions, 'confirm_guidance', side_effect=fail_target):
            with self.assertRaises(OSError):
                self.enable()
            self.assertEqual(self.rule()['state'], 'pending')
            with self.assertRaises(OSError):
                self.enable()
            self.assertEqual(self.rule()['state'], 'pending')
        self.enable()
        self.assertEqual(self.rule()['state'], 'enabled')
        self.assertEqual(self.backup().read_bytes(), self.original)

    def test_crash_before_pending_leaves_original_and_retryable_backup(self):
        with patch.object(guidance, 'change_rule', side_effect=OSError('synthetic crash')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertIsNone(self.rule())
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertEqual(self.backup().read_bytes(), self.original)
        self.enable()
        self.assertTrue(self.query()['enabled'])

    def test_pending_before_publication_retries_without_losing_backup(self):
        with patch.object(instructions, 'atomic_guidance', side_effect=OSError('synthetic crash')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertEqual(self.rule()['state'], 'pending')
        self.assertFalse(self.query()['enabled'])
        self.assertEqual(self.target.read_bytes(), self.original)
        self.enable()
        self.assertTrue(self.query()['enabled'])
        self.assertEqual(self.backup().read_bytes(), self.original)

    def test_pending_after_publication_retries_only_configuration(self):
        real_change = guidance.change_rule
        def crash_enable(state, key, rule):
            if rule and rule['state'] == 'enabled':
                raise OSError('synthetic crash')
            return real_change(state, key, rule)
        with patch.object(guidance, 'change_rule', side_effect=crash_enable):
            with self.assertRaises(OSError):
                self.enable()
        self.assertEqual(self.rule()['state'], 'pending')
        published = self.target.read_bytes()
        with patch.object(instructions, 'atomic_guidance', side_effect=AssertionError('second publication')):
            self.enable()
        self.assertTrue(self.query()['enabled'])
        self.assertEqual(self.target.read_bytes(), published)

    def test_pending_refuses_operator_edits_before_and_after_publication(self):
        for after in (False, True):
            with self.subTest(after=after):
                self.save(self.initial)
                self.target.write_bytes(self.original)
                real_change = guidance.change_rule
                def crash(state, key, rule):
                    if rule and rule['state'] == 'enabled':
                        raise OSError('crash')
                    return real_change(state, key, rule)
                target = patch.object(guidance, 'change_rule', side_effect=crash) if after else \
                         patch.object(instructions, 'atomic_guidance', side_effect=OSError('crash'))
                with target, self.assertRaises(OSError):
                    self.enable()
                self.target.write_bytes(self.target.read_bytes()+b'Operator addition\r\n')
                edited = self.target.read_bytes()
                config = (self.prefix/'install.json').read_bytes()
                with self.assertRaises(guidance.GuidanceError):
                    self.enable()
                self.assertEqual(self.target.read_bytes(), edited)
                self.assertEqual((self.prefix/'install.json').read_bytes(), config)

    def test_removal_disables_before_refusing_edited_section(self):
        self.enable()
        self.target.write_bytes(self.target.read_bytes().replace(b'explicitly start', b'operator edit'))
        edited = self.target.read_bytes()
        with self.assertRaises(guidance.GuidanceError):
            self.remove()
        self.assertEqual(self.rule()['state'], 'disabled')
        self.assertEqual(self.target.read_bytes(), edited)
        self.assertTrue(self.backup().exists())

    def test_removal_retries_after_disabling_and_after_file_publication(self):
        self.enable()
        with patch.object(instructions, 'atomic_guidance', side_effect=OSError('crash')):
            with self.assertRaises(OSError):
                self.remove()
        self.assertEqual(self.rule()['state'], 'disabled')
        real_change = guidance.change_rule
        def crash_drop(state, key, rule):
            if rule is None:
                raise OSError('crash')
            return real_change(state, key, rule)
        with patch.object(guidance, 'change_rule', side_effect=crash_drop):
            with self.assertRaises(OSError):
                self.remove()
        self.assertEqual(self.target.read_bytes(), self.original)
        self.remove()
        self.assertIsNone(self.rule())

    def test_pending_upgrade_preimage_can_be_removed_and_retried(self):
        self.enable()
        real_render = guidance.render
        def revised(*args, **kwargs):
            return real_render(*args, **kwargs).replace('Repository work coordination', 'Revised work coordination')
        with patch.object(guidance, 'render', side_effect=revised), \
             patch.object(instructions, 'atomic_guidance', side_effect=OSError('crash')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertEqual(self.rule()['state'], 'pending')
        with patch.object(instructions, 'atomic_guidance', side_effect=OSError('crash')):
            with self.assertRaises(OSError):
                self.remove()
        self.assertEqual(self.rule()['state'], 'disabled')
        self.remove()
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_target_move_or_orphan_section_requires_explicit_repair(self):
        self.enable()
        other = self.root/'other guidance'
        with self.assertRaises(guidance.GuidanceError):
            self.enable(target=other)
        self.assertFalse(other.exists())
        self.save(self.initial)
        with self.assertRaises(guidance.GuidanceError):
            self.enable()

    def test_private_backups_refuse_git_symlink_and_open_permissions(self):
        for location in (self.repo/'state', self.root/'symlink state', self.root/'open state'):
            with self.subTest(location=location):
                if location.name == 'symlink state':
                    location.symlink_to(self.root, target_is_directory=True)
                elif location.name == 'open state':
                    location.mkdir(mode=0o755)
                    location.chmod(0o755)
                config = dict(self.initial, state_root=str(location))
                self.save(config)
                with self.assertRaises(guidance.GuidanceError):
                    self.enable()
                self.assertEqual(self.target.read_bytes(), self.original)
                self.assertIsNone(self.rule())

    def test_cli_configure_remove_and_uninstall_preserve_outside_bytes(self):
        command = [sys.executable, 'scripts/install.py', '--prefix', str(self.prefix),
                   '--repo', str(self.repo), '--participant', 'claude']
        result = subprocess.run(command+['--configure-work-items', '--guidance-file', str(self.target)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        self.assertTrue(self.query('claude')['enabled'])
        result = subprocess.run(command+['--remove-work-items'], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        self.enable()
        self.enable('claude')
        state_marker = Path(self.initial['state_root'])/'preserved-state'
        state_marker.write_text('synthetic state')
        result = subprocess.run([sys.executable, 'scripts/uninstall.py', '--prefix', str(self.prefix)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertTrue(state_marker.exists())
        self.assertFalse((self.prefix/'install.json').exists())
        self.assertTrue((self.prefix/install_state.LOCK_NAME).exists())

    def test_work_writer_waits_for_legacy_guidance_lock(self):
        with instructions.update_locks(self.root):
            with patch.object(install_state, 'LOCK_TIMEOUT', .02):
                with self.assertRaises(runtime_names.NameConflict) as refused:
                    self.enable()
            self.assertEqual(refused.exception.code, 'configuration_busy')
            self.assertEqual(self.target.read_bytes(), self.original)
            self.assertIsNone(self.rule())
        self.enable()
        self.assertTrue(self.query()['enabled'])

    def test_two_process_writers_preserve_both_rules_and_original_backup(self):
        command = [sys.executable, 'scripts/install.py', '--configure-work-items',
                   '--prefix', str(self.prefix), '--repo', str(self.repo),
                   '--guidance-file', str(self.target), '--participant']
        first = subprocess.Popen(command+['codex'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        second = subprocess.Popen(command+['claude'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        for child in (first, second):
            out, err = child.communicate(timeout=waiting.timeout())
            self.assertEqual(child.returncode, 0, out+err)
        self.assertTrue(self.query()['enabled'])
        self.assertTrue(self.query('claude')['enabled'])
        self.assertEqual(self.backup().read_bytes(), self.original)

    def test_renderer_change_while_pending_before_publication_refuses(self):
        with patch.object(instructions, 'atomic_guidance', side_effect=OSError('crash')):
            with self.assertRaises(OSError):
                self.enable()
        render = guidance.render
        with patch.object(guidance, 'render', side_effect=lambda *a, **k: render(*a, **k)+'changed'), \
             self.assertRaises(guidance.GuidanceError):
            self.enable()
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertEqual(self.rule()['state'], 'pending')

    def test_control_characters_in_configuration_paths_refuse_before_writes(self):
        for char in ('\n', '\r', '\t', '\x7f', '\x85'):
            with self.subTest(char=repr(char)), self.assertRaises(ValueError):
                work_policy.absolute_path(str(self.root/('unsafe'+char+'path')))
        bad_repo = self.new_repo('newline\nrepository')
        with self.assertRaises(ValueError):
            self.enable(repo=bad_repo)
        with self.assertRaises(ValueError):
            guidance.configure(self.root/'newline\nprefix', self.repo, 'codex', self.target)
        self.assertEqual(self.target.read_bytes(), self.original)
        self.assertFalse((self.prefix/install_state.LOCK_NAME).exists())

    def test_backup_git_check_failure_is_an_explicit_refusal(self):
        for error in (FileNotFoundError('git unavailable'), subprocess.TimeoutExpired(['git'], 10)):
            with self.subTest(error=type(error).__name__), \
                 patch.object(guidance.subprocess, 'run', side_effect=error), \
                 self.assertRaises(guidance.GuidanceError) as refused:
                guidance.backup_path(self.prefix, self.initial, self.target)
            self.assertIn('cannot verify backup location with Git', str(refused.exception))
        self.assertFalse((self.root/'private state').exists())

    def test_legacy_guidance_updates_preserve_work_sections(self):
        # Existing peer updater chooses AGENTS.md beneath the explicitly supplied home.
        self.target = self.root/'AGENTS.md'
        self.target.write_bytes(self.original)
        for agent in work_policy.PARTICIPANTS:
            self.enable(agent)
        for agent in ('codex', 'deepseek'):
            instructions.update(self.root, self.prefix, agent=agent)
            for selected in work_policy.PARTICIPANTS:
                self.assertTrue(self.query(selected)['enabled'])
            instructions.update(self.root, self.prefix, remove=True, agent=agent)
        for agent in work_policy.PARTICIPANTS:
            self.remove(agent)
        self.assertEqual(self.target.read_bytes(), self.original)


    def test_exact_guidance_repeat_requires_installation_confirmation(self):
        self.enable()
        content = self.target.read_bytes()
        with patch.object(install_state.platform_support, 'sync_state_directory', side_effect=OSError('flush')):
            with self.assertRaises(OSError):
                self.enable()
        self.assertEqual(self.target.read_bytes(), content)
        self.enable()
        self.assertEqual(self.target.read_bytes(), content)
        self.assertTrue(self.query()['enabled'])

if __name__ == '__main__':
    unittest.main()
