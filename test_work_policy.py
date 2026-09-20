"""Synthetic work-policy records and isolated installation concurrency."""
import copy
from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import install_state
import memory
import runtime_names
import session
import work_policy


class PolicyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix = self.root / 'app'
        self.prefix.mkdir(mode=0o700)
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.common = memory.repo_common_directory(self.repo)
        self.key = memory.repo_identity(self.repo)
        self.guidance = self.root / 'synthetic guidance.txt'
        self.guidance.write_bytes(b'Synthetic outside bytes\r\n')
        self.rule = dict(common_directory=str(self.common), guidance_file=str(self.guidance),
                         state='enabled', digest='a' * 64)
        self.config = dict(state_root=str(self.root/'state'), unit_dir=str(self.root/'units'),
                           codex=sys.executable, participants=[], opaque={'keep': [1, 2]},
                           work_items=dict(version=1, rules={self.key + ':codex': self.rule}))

    def save(self, config=None):
        (self.prefix/'install.json').write_text(json.dumps(self.config if config is None else config))
        (self.prefix/'install.json').chmod(0o600)

    def test_repository_identity_literal_is_unchanged(self):
        answer = subprocess.CompletedProcess([], 0, '/synthetic/repository/.git\n', '')
        with patch('memory.subprocess.run', return_value=answer):
            self.assertEqual(memory.repo_identity('/unused'), '5e5b7ae4dfe9fc28')
            self.assertEqual(work_policy.repository_key(memory.repo_common_directory('/unused')),
                             '5e5b7ae4dfe9fc28')

    def test_other_actions_refuse_claude_before_config_or_registration(self):
        for action in ('ensure', 'run', 'status', 'stop', 'rename'):
            with self.subTest(action=action), patch('sys.argv', ['session.py', action, '--agent', 'claude']), \
                    patch.object(session, 'read_config', side_effect=AssertionError('configuration read')), \
                    redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as refused:
                    session.main()
                self.assertEqual(refused.exception.code, 2)

    def test_all_participants_and_pending_disabled_semantics(self):
        for agent in work_policy.PARTICIPANTS:
            for state in ('enabled', 'pending', 'disabled'):
                rule = dict(self.rule, state=state)
                if state == 'pending':
                    rule.update(before_digest='b'*64, after_digest='c'*64)
                config = dict(self.config, work_items=dict(version=1, rules={self.key+':'+agent:rule}))
                result = work_policy.query(config, self.repo, agent)
                self.assertEqual(result['enabled'], state == 'enabled')
                self.assertEqual(result['state'], 'enabled' if state == 'enabled' else 'disabled')
                self.assertEqual(result['digest'], 'a'*64)
                self.assertEqual(result['common_directory'], str(self.common))
        self.assertFalse(work_policy.query({}, self.repo, 'codex')['enabled'])
        self.assertFalse(work_policy.query(self.config, self.repo, 'claude')['enabled'])

    def test_worktree_and_subdirectory_share_selection(self):
        subprocess.run(['git', '-C', str(self.repo), '-c', 'user.name=Synthetic',
                        '-c', 'user.email=synthetic@example.invalid', '-c', 'commit.gpgsign=false',
                        'commit', '--allow-empty', '-qm', 'synthetic'], check=True)
        tree = self.root/'tree'
        subprocess.run(['git', '-C', str(self.repo), 'worktree', 'add', '--detach', str(tree)],
                       check=True, capture_output=True)
        sub = tree/'nested'
        sub.mkdir()
        self.assertEqual(work_policy.query(self.config, self.repo, 'codex'),
                         work_policy.query(self.config, sub, 'codex'))

    def test_malformed_rules_never_become_defaults(self):
        bad = [None, [], {}, {'version':True,'rules':{}}, {'version':2,'rules':{}},
               {'version':1,'rules':[]}, {'version':1,'rules':{},'unknown':1}]
        for field, value in [('state','invalid'), ('digest','x'*64), ('common_directory','relative'),
                             ('guidance_file','relative'), ('guidance_file','/a/../b')]:
            bad.append(dict(version=1, rules={self.key+':codex':dict(self.rule, **{field:value})}))
        bad += [dict(version=1,rules={'wrong:codex':self.rule}),
                dict(version=1,rules={self.key+':codex':dict(self.rule,state='pending')}),
                dict(version=1,rules={self.key+':codex':dict(self.rule,before_digest='b'*64)})]
        for invalid in bad:
            with self.subTest(invalid=invalid):
                config = dict(self.config, work_items=invalid)
                self.save(config)
                before = (self.prefix/'install.json').read_bytes()
                with self.assertRaises(runtime_names.NameConflict):
                    runtime_names.install_config(self.prefix)
                with self.assertRaises(runtime_names.NameConflict):
                    with install_state.locked(self.prefix):
                        self.fail('invalid configuration accepted')
                self.assertEqual((self.prefix/'install.json').read_bytes(), before)

    def test_exact_rule_bound(self):
        rules = {}
        for n in range(65):
            common = str(self.root / ('repo-'+str(n)))
            rules[work_policy.repository_key(common)+':codex'] = dict(self.rule, common_directory=common)
            if n == 63:
                work_policy.validate(dict(version=1, rules=rules))
        with self.assertRaises(ValueError):
            work_policy.validate(dict(version=1, rules=rules))

    def test_guidance_path_refuses_links_permissions_and_wrong_types(self):
        target = self.root/'new file'
        self.assertEqual(work_policy.guidance_path(str(target)), target)
        self.assertFalse(target.exists())
        linked = self.root/'linked'
        linked.symlink_to(self.guidance)
        directory_link = self.root/'linked-parent'
        directory_link.symlink_to(self.root, target_is_directory=True)
        unsafe = self.root/'unsafe'
        unsafe.mkdir()
        unsafe.chmod(0o777)
        for value in (str(linked), str(directory_link/'new'), str(unsafe/'new'),
                      str(self.root/'absent'/'new'), str(self.root), 'relative'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                work_policy.guidance_path(value)
        with patch('work_policy.os.getuid', return_value=os.getuid()+1), self.assertRaises(ValueError):
            work_policy.guidance_path(str(self.guidance))
        self.assertFalse((self.root/'absent').exists())

    def test_atomic_merge_preserves_unknown_and_work_fields(self):
        self.save()
        before = copy.deepcopy(self.config)
        with install_state.locked(self.prefix) as locked:
            result = locked.merge({'codex_home':str(self.root/'codex')})
        self.assertEqual({key:result[key] for key in before}, before)
        self.assertEqual(runtime_names.install_config(self.prefix), result)
        lock = self.prefix/install_state.LOCK_NAME
        inode = lock.stat().st_ino
        with install_state.locked(self.prefix) as locked:
            locked.merge({'another_unknown':True})
        self.assertEqual(lock.stat().st_ino, inode)
        self.assertEqual((self.prefix/'install.json').stat().st_mode & 0o777, 0o600)

    def test_failed_publication_preserves_previous_config(self):
        self.save()
        before = (self.prefix/'install.json').read_bytes()
        with install_state.locked(self.prefix) as locked:
            with patch('install_state.os.replace', side_effect=OSError('injected')):
                with self.assertRaises(OSError):
                    locked.merge({'opaque':False})
        self.assertEqual((self.prefix/'install.json').read_bytes(), before)
        self.assertEqual(list(self.prefix.glob('.install-*')), [])

    def test_unsafe_lock_or_config_refuses_without_repair(self):
        self.save()
        lock = self.prefix/install_state.LOCK_NAME
        lock.symlink_to(self.guidance)
        before = self.guidance.read_bytes()
        with self.assertRaises(runtime_names.NameConflict):
            with install_state.locked(self.prefix):
                pass
        self.assertEqual(self.guidance.read_bytes(), before)
        lock.unlink()
        lock.write_text('')
        lock.chmod(0o666)
        with self.assertRaises(runtime_names.NameConflict):
            with install_state.locked(self.prefix):
                pass
        lock.unlink()
        (self.prefix/'install.json').chmod(0o666)
        with self.assertRaises(runtime_names.NameConflict):
            with install_state.locked(self.prefix):
                pass

    def test_lock_serializes_read_modify_write_and_preserves_both_updates(self):
        self.save()
        code = '''import install_state,sys
print('started',flush=True)
with install_state.locked(sys.argv[1]) as state:
 state.merge({'second_writer':2})
'''
        with install_state.locked(self.prefix) as state:
            process = subprocess.Popen([sys.executable, '-c', code, str(self.prefix)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            self.assertEqual(process.stdout.readline().strip(), 'started')
            self.assertIsNone(process.poll())
            state.merge({'first_writer':1})
        out, error = process.communicate(timeout=10)
        self.assertEqual(process.returncode, 0, error)
        result = runtime_names.install_config(self.prefix)
        self.assertEqual((result['first_writer'],result['second_writer']), (1,2))
        self.assertEqual(result['work_items'], self.config['work_items'])

    def install(self, *mode):
        return subprocess.run([sys.executable, 'scripts/install.py', *mode,
            '--prefix', str(self.prefix), '--no-start', '--codex', sys.executable,
            '--codex-home', str(self.root/'codex'), '--dsh-home', str(self.root/'deepseek')],
            text=True, capture_output=True)

    def test_all_install_modes_preserve_rules_and_opaque_fields(self):
        self.save()
        for mode in (('--thread','synthetic'), ('--configure-codex',), ('--configure-deepseek',)):
            result = self.install(*mode)
            self.assertEqual(result.returncode, 0, result.stderr)
            config = runtime_names.install_config(self.prefix)
            self.assertEqual(config['work_items'], self.config['work_items'])
            self.assertEqual(config['opaque'], self.config['opaque'])
        self.assertEqual(self.guidance.read_bytes(), b'Synthetic outside bytes\r\n')

    def test_install_reloads_configuration_after_waiting_for_lock(self):
        self.save()
        code = """import contextlib,install_state,runpy,sys
real=install_state.locked
@contextlib.contextmanager
def waiting(prefix):
 print('waiting',flush=True)
 with real(prefix) as state:
  yield state
install_state.locked=waiting
sys.argv=['scripts/install.py',*sys.argv[1:]]
runpy.run_path('scripts/install.py',run_name='__main__')
"""
        args = ['--configure-codex', '--prefix', str(self.prefix), '--no-start',
                '--codex', sys.executable, '--codex-home', str(self.root/'codex')]
        with install_state.locked(self.prefix) as state:
            process = subprocess.Popen([sys.executable, '-c', code, *args],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            self.assertEqual(process.stdout.readline().strip(), 'waiting')
            changed = copy.deepcopy(self.config['work_items'])
            changed['rules'][self.key+':codex']['digest'] = 'f'*64
            state.merge({'work_items':changed,'concurrent_value':True})
        out, error = process.communicate(timeout=15)
        self.assertEqual(process.returncode, 0, error)
        saved = runtime_names.install_config(self.prefix)
        self.assertEqual(saved['work_items'], changed)
        self.assertTrue(saved['concurrent_value'])
        self.assertEqual(saved['participants'], ['codex'])

    def test_installed_cli_query_is_read_only_and_needs_no_thread(self):
        self.save()
        installed = self.install('--configure-deepseek')
        self.assertEqual(installed.returncode, 0, installed.stderr)
        env = dict(os.environ)
        env.pop('CODEX_THREAD_ID', None)
        env.pop('DSH_SESSION_ID', None)
        before = {str(p):p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        result = subprocess.run([sys.executable, '-B', str(self.prefix/'session.py'),
            'work-policy', '--repo', str(self.repo), '--agent', 'codex'],
            env=env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)['enabled'])
        after = {str(p):p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(before, after)
        self.assertFalse((self.root/'state').exists())
        bad = subprocess.run([sys.executable, '-B', str(self.prefix/'session.py'),
            'work-policy', '--repo', str(self.repo)], env=env, text=True, capture_output=True)
        self.assertNotEqual(bad.returncode, 0)
        self.assertFalse((self.root/'state').exists())
