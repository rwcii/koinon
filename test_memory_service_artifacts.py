"""Artifact tests never load a manager or touch a real repository's state."""
import json
import os
from pathlib import Path
import plistlib
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import install_state
import memory_service_artifacts as artifacts
import memory_service_config as configuration
from participant_lock import file_lock
import platform_support
import runtime_names


class MemoryArtifactTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix, self.units, self.repo = (self.root / name for name in ('prefix', 'units', 'repo'))
        self.prefix.mkdir(mode=0o700)
        self.units.mkdir(mode=0o700)
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.python = Path(sys.executable).absolute()
        self.initial = dict(state_root=str(self.root / 'state'), unit_dir=str(self.units),
                            unrelated={'keep': ['exactly']}, work_items=dict(version=1, rules={}))
        self.config_path = self.prefix / 'install.json'
        self.config_path.write_text(json.dumps(self.initial))
        self.config_path.chmod(0o600)

    def desired(self, backend='systemd', prefix=None):
        key, record = configuration.selection(self.repo, self.root / 'state')
        record.update(backend=backend, artifact=str(self.units / configuration.artifact_name(key, backend)),
                      state='installed', artifact_digest='0' * 64)
        content = platform_support.memory_service_artifact(prefix or self.prefix, self.python, key, record)
        record['artifact_digest'] = artifacts.digest(content)
        return key, record, content

    def retained(self):
        return runtime_names.install_config(self.prefix)

    def seed(self, record, content=None):
        key = configuration.identity(record['common_directory'])[0]
        with install_state.locked(self.prefix) as state:
            state.merge({'memory_services': dict(version=1, repositories={key: record})})
        if content is not None:
            path = Path(record['artifact'])
            path.write_bytes(content)
            path.chmod(0o600)

    def refused(self, desired):
        before = self.config_path.read_bytes()
        with self.assertRaises(runtime_names.NameConflict) as caught:
            artifacts.publish(self.prefix, self.python, desired)
        self.assertEqual(caught.exception.code, 'invalid_install_configuration')
        self.assertEqual(self.config_path.read_bytes(), before)
        return caught.exception

    def test_new_and_repeat_preserve_configuration_no_state_or_manager(self):
        for backend in ('systemd', 'launchd'):
            with self.subTest(backend=backend):
                key, record, content = self.desired(backend)
                # Each backend is a separate synthetic installation, not a migration.
                self.config_path.write_text(json.dumps(self.initial))
                unrelated = self.units / 'operator.service'
                unrelated.write_bytes(b'untouched')
                with patch.object(platform_support.subprocess, 'run', wraps=subprocess.run) as run:
                    self.assertEqual(artifacts.publish(self.prefix, self.python, record), record)
                    self.assertTrue(all(call.args[0][0] == 'git' for call in run.call_args_list))
                self.assertEqual(Path(record['artifact']).read_bytes(), content)
                inode = Path(record['artifact']).stat().st_ino
                with patch.object(artifacts, '_replace', side_effect=AssertionError('repeat rewrote artifact')):
                    artifacts.publish(self.prefix, self.python, record)
                self.assertEqual(Path(record['artifact']).stat().st_ino, inode)
                self.assertEqual(stat.S_IMODE(Path(record['artifact']).stat().st_mode), 0o600)
                for name, value in self.initial.items():
                    self.assertEqual(self.retained()[name], value)
                self.assertEqual(unrelated.read_bytes(), b'untouched')
                self.assertFalse((self.root / 'state').exists())
                self.assertEqual(self.retained()['memory_services']['repositories'][key], record)
                artifacts.verify_owned(self.prefix, self.python, record, observed_artifact=record['artifact'])
                self.assertNotEqual(artifacts._lock_path(Path(record['artifact'])).parent, self.units)

    def test_repeat_publication_preserves_explicit_launchd_domain(self):
        key, record, content = self.desired('launchd')
        record['manager_domain'] = 'gui/501'
        artifacts.publish(self.prefix, self.python, record)
        artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(self.retained()['memory_services']['repositories'][key]['manager_domain'], 'gui/501')
        self.assertEqual(Path(record['artifact']).read_bytes(), content)
        self.refused(dict(record, manager_domain='gui/502'))

    def test_loaded_systemd_link_requires_one_owned_literal_hop(self):
        key, record, content = self.desired()
        artifacts.publish(self.prefix, self.python, record)
        loader = self.root / 'loader'
        loader.mkdir(mode=0o700)
        link = loader / Path(record['artifact']).name
        link.symlink_to(record['artifact'])
        self.assertEqual(artifacts.verify_loaded(self.prefix, self.python, record, link), record)
        link.unlink()
        indirect = self.root / 'indirect'
        indirect.symlink_to(record['artifact'])
        link.symlink_to(indirect)
        with self.assertRaises(runtime_names.NameConflict):
            artifacts.verify_loaded(self.prefix, self.python, record, link)
        link.unlink()
        link.symlink_to(record['artifact'])
        loader.chmod(0o775)
        with self.assertRaises(runtime_names.NameConflict) as caught:
            artifacts.verify_loaded(self.prefix, self.python, record, link)
        self.assertIn(str(loader), caught.exception.paths)

    def test_shadowing_regular_artifact_is_never_adopted_even_with_identical_bytes(self):
        _, record, content = self.desired()
        artifacts.publish(self.prefix, self.python, record)
        shadow_dir = self.root / 'shadow'
        shadow_dir.mkdir(mode=0o700)
        shadow = shadow_dir / Path(record['artifact']).name
        shadow.write_bytes(content)
        shadow.chmod(0o600)
        with self.assertRaises(runtime_names.NameConflict):
            artifacts.verify_loaded(self.prefix, self.python, record, shadow)
        self.assertEqual(shadow.read_bytes(), content)
        self.assertEqual(Path(record['artifact']).read_bytes(), content)

    def test_new_literal_systemd_template_does_not_replace_existing_template(self):
        key, old, old_content = self.desired()
        new = dict(old, template_version=2)
        new_content = platform_support.memory_service_artifact(self.prefix, self.python, key, new)
        self.assertIn(b'ExecStart=:', new_content)
        self.assertNotEqual(new_content, old_content)
        new['artifact_digest'] = artifacts.digest(new_content)
        artifacts.publish(self.prefix, self.python, old)
        self.refused(new)
        self.assertEqual(Path(old['artifact']).read_bytes(), old_content)

    def test_pending_before_write_and_after_rename_recover_without_rewriting_postimage(self):
        key, record, content = self.desired()
        pending = dict(record, state='pending', before_digest=None, after_digest=record['artifact_digest'])
        self.seed(pending)
        artifacts.publish(self.prefix, self.python, record)
        self.seed(pending, content)
        with patch.object(artifacts, '_replace', side_effect=AssertionError('postimage rewritten')):
            with patch.object(artifacts, '_fsync_directory', wraps=artifacts._fsync_directory) as sync:
                artifacts.publish(self.prefix, self.python, record)
                sync.assert_called_once_with(self.units)
        self.assertEqual(self.retained()['memory_services']['repositories'][key], record)

    def test_pending_neither_preimage_nor_postimage_refuses_and_preserves_foreign_bytes(self):
        _, record, _ = self.desired()
        pending = dict(record, state='pending', before_digest=None, after_digest=record['artifact_digest'])
        self.seed(pending, b'operator changed this')
        self.refused(record)
        self.assertEqual(Path(record['artifact']).read_bytes(), b'operator changed this')

    def test_installed_missing_or_modified_artifact_refuses_without_repair(self):
        _, record, content = self.desired()
        for observed in (None, b'changed'):
            self.seed(record, observed)
            if observed is None:
                Path(record['artifact']).unlink(missing_ok=True)
            self.refused(record)
        self.seed(record, content)
        artifacts.verify_owned(self.prefix, self.python, record)
        with self.assertRaises(runtime_names.NameConflict):
            artifacts.verify_owned(self.prefix, self.python, record, observed_artifact=str(self.root / 'other'))

    def test_unregistered_even_identical_artifact_is_not_adopted(self):
        _, record, content = self.desired()
        path = Path(record['artifact'])
        path.write_bytes(content)
        path.chmod(0o600)
        self.refused(record)
        self.assertFalse((self.prefix / install_state.LOCK_NAME).exists())
        self.assertEqual(path.read_bytes(), content)

    def test_failed_artifact_write_and_directory_fsync_leave_pending_for_retry(self):
        key, record, content = self.desired()
        with patch.object(artifacts, '_replace', side_effect=OSError('disk full')):
            with self.assertRaises(runtime_names.NameConflict):
                artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(self.retained()['memory_services']['repositories'][key]['state'], 'pending')
        self.assertFalse(Path(record['artifact']).exists())
        with patch.object(artifacts, '_fsync_directory', side_effect=OSError('fsync failed')):
            with self.assertRaises(runtime_names.NameConflict):
                artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(Path(record['artifact']).read_bytes(), content)
        self.assertEqual(self.retained()['memory_services']['repositories'][key]['state'], 'pending')
        self.assertEqual(list(self.units.glob('.memory-artifact-*')), [])
        artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(self.retained()['memory_services']['repositories'][key], record)

    def test_crash_before_config_completion_is_recoverable(self):
        key, record, content = self.desired()
        save = artifacts._save
        def fail_completion(state, key, value):
            if value['state'] == 'installed':
                raise OSError('completion failed')
            return save(state, key, value)
        with patch.object(artifacts, '_save', side_effect=fail_completion):
            with self.assertRaises(runtime_names.NameConflict):
                artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(Path(record['artifact']).read_bytes(), content)
        self.assertEqual(self.retained()['memory_services']['repositories'][key]['state'], 'pending')
        artifacts.publish(self.prefix, self.python, record)

    def test_symlink_hardlink_fifo_directory_and_writable_artifact_refuse(self):
        _, record, content = self.desired()
        path = Path(record['artifact'])
        target = self.root / 'operator'
        target.write_bytes(content)
        target.chmod(0o600)
        constructors = [lambda: path.symlink_to(target), lambda: os.link(target, path),
                        lambda: os.mkfifo(path, 0o600), lambda: path.mkdir()]
        for make in constructors:
            make()
            self.refused(record)
            path.rmdir() if path.is_dir() else path.unlink()
        self.seed(record, content)
        path.chmod(0o666)
        self.refused(record)
        self.assertEqual(target.read_bytes(), content)

    def test_missing_directory_and_nonexecutable_interpreters_refuse_before_publication(self):
        key, record, _ = self.desired()
        missing, directory, no_execute = (self.root / name for name in
                                          ('missing-python', 'directory-python', 'not-executable'))
        directory.mkdir()
        no_execute.write_text('synthetic non-executable')
        no_execute.chmod(0o600)
        before = self.config_path.read_bytes()
        for python in (missing, directory, no_execute):
            candidate = dict(record)
            candidate['artifact_digest'] = artifacts.digest(
                platform_support.memory_service_artifact(self.prefix, python, key, candidate))
            with self.subTest(python=python), self.assertRaises(runtime_names.NameConflict) as caught:
                artifacts.publish(self.prefix, python, candidate)
            self.assertEqual(caught.exception.code, 'invalid_install_configuration')
            self.assertEqual(self.config_path.read_bytes(), before)
            self.assertFalse((self.prefix / install_state.LOCK_NAME).exists())
            self.assertFalse(Path(candidate['artifact']).exists())

    def test_parent_symlink_missing_and_writable_refuse_before_lock_creation(self):
        _, record, _ = self.desired()
        for mode in (0o777, 0o775):
            self.units.chmod(mode)
            self.refused(record)
            self.assertFalse((self.prefix / install_state.LOCK_NAME).exists())
        self.units.chmod(0o700)
        self.units.rmdir()
        self.refused(record)
        other = self.root / 'other-units'
        other.mkdir(mode=0o700)
        self.units.symlink_to(other, target_is_directory=True)
        self.refused(record)
        self.assertEqual(list(other.iterdir()), [])

    def test_repo_unresolved_and_structural_errors_translate_before_mutation(self):
        _, record, _ = self.desired()
        shutil.rmtree(self.repo)
        exc = self.refused(record)
        self.assertEqual(getattr(exc.__cause__, 'code', None), 'repo_unresolved')
        self.assertFalse((self.prefix / install_state.LOCK_NAME).exists())
        invalid = dict(record, state_root='relative')
        self.refused(invalid)
        self.assertEqual(platform_support.CONFIGURATION_EXIT_STATUS, 78)

    def test_removing_retargeted_and_distinct_template_preimage_are_refused(self):
        _, record, content = self.desired()
        self.seed(dict(record, state='removing', before_digest=record['artifact_digest'], after_digest=None), content)
        self.refused(record)
        self.seed(record, content)
        changed = dict(record, artifact=str(self.root / Path(record['artifact']).name))
        self.refused(changed)
        self.seed(dict(record, state='pending', before_digest=artifacts.digest(b'old template'),
                       after_digest=record['artifact_digest']), b'old template')
        self.refused(record)

    def test_cross_prefix_refuses_and_shares_permanent_namespace_lock(self):
        _, record, _ = self.desired()
        artifacts.publish(self.prefix, self.python, record)
        other = self.root / 'second-prefix'
        other.mkdir(mode=0o700)
        (other / 'install.json').write_text(json.dumps(self.initial))
        _, second, _ = self.desired(prefix=other)
        with self.assertRaises(runtime_names.NameConflict):
            artifacts.publish(other, self.python, second)
        lock = artifacts._lock_path(Path(record['artifact']))
        self.assertEqual(lock, artifacts._lock_path(Path(second['artifact'])))
        inode = lock.stat().st_ino
        artifacts.publish(self.prefix, self.python, record)
        self.assertEqual(lock.stat().st_ino, inode)
        with file_lock(lock, 'synthetic', None), patch.object(install_state, 'LOCK_TIMEOUT', 0):
            with self.assertRaises(runtime_names.NameConflict) as caught:
                artifacts.publish(self.prefix, self.python, record)
            self.assertEqual(caught.exception.code, 'configuration_busy')
        self.assertEqual(lock.stat().st_ino, inode)

    def test_templates_bind_exact_prefix_repo_state_and_literal_special_paths(self):
        prefix = self.root / 'space $HOME %n "quote"'
        prefix.mkdir(mode=0o700)
        for backend in ('systemd', 'launchd'):
            _, record, content = self.desired(backend, prefix)
            if backend == 'systemd':
                self.assertIn(b'# Memory service template v1', content)
                self.assertIn(b'$$HOME %%n', content)
                self.assertIn(b'RestartPreventExitStatus=70 78', content)
            else:
                decoded = plistlib.loads(content)
                self.assertEqual(decoded['ProgramArguments'][1], str(prefix / 'memory_service.py'))
                self.assertEqual(decoded['KeepAlive'], {'SuccessfulExit': False})
                self.assertEqual(decoded['Umask'], 63)
                self.assertEqual(decoded['KoinonManaged'], 'memory-service-v1')
            self.seed(record, content)
            (prefix / 'install.json').write_bytes(self.config_path.read_bytes())
            (prefix / 'install.json').chmod(0o600)
            artifacts.verify_owned(prefix, self.python, record)
            Path(record['artifact']).write_bytes(content + b'\n')
            with self.assertRaises(runtime_names.NameConflict):
                artifacts.verify_owned(prefix, self.python, record)


if __name__ == '__main__':
    unittest.main()
