"""Refuse unsafe registration before any systemd enable/start invocation."""
import os
import json
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import memory_service
import memory_service_artifacts as artifacts
import platform_support
from test_memory_service_config import record


class RegistrationPreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.config = self.root / 'config'
        self.config.mkdir(mode=0o700)
        self.selected = record()

    def test_unsafe_config_ancestor_refuses_before_manager_mutation_and_names_path(self):
        self.config.chmod(0o775)
        with patch.object(platform_support, 'systemd_registration_directories',
                          return_value=(self.config / 'systemd/user', self.root / 'systemd/user')), \
                patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run') as run:
            with self.assertRaises(memory_service.RunnerError) as caught:
                with memory_service.configuration_boundary():
                    platform_support.memory_manager_action(self.selected, 'activate')
            self.assertEqual(caught.exception.paths, (str(self.config),))
            self.assertEqual(caught.exception.code, 'configuration_error')
            run.assert_not_called()
        self.assertEqual(list(self.config.iterdir()), [])
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o775)

    def test_safe_missing_suffix_does_not_create_directories(self):
        with patch.object(platform_support, 'systemd_registration_directories',
                          return_value=(self.config / 'systemd/user', self.root / 'systemd/user')):
            paths = platform_support.memory_registration_paths(self.selected)
        artifacts.preflight_registration(self.selected, paths)
        self.assertFalse((self.config / 'systemd').exists())

    def test_conflicting_wants_link_and_symlinked_parent_are_refused(self):
        unit_dir = self.config / 'systemd/user'
        wants = unit_dir / 'default.target.wants'
        (self.config / 'systemd').mkdir(mode=0o700)
        unit_dir.mkdir(mode=0o700)
        wants.mkdir(mode=0o700)
        paths = (unit_dir / Path(self.selected['artifact']).name,
                 wants / Path(self.selected['artifact']).name)
        paths[1].symlink_to('/synthetic/other.service')
        with self.assertRaises(artifacts.RegistrationPathError):
            artifacts.preflight_registration(self.selected, paths)
        paths[1].unlink()
        wants.rmdir()
        wants.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(artifacts.RegistrationPathError):
            artifacts.preflight_registration(self.selected, paths)

    def test_existing_exact_owned_links_are_preserved_for_retry(self):
        unit_dir = self.config / 'systemd/user'
        wants = unit_dir / 'default.target.wants'
        (self.config / 'systemd').mkdir(mode=0o700)
        unit_dir.mkdir(mode=0o700)
        wants.mkdir(mode=0o700)
        paths = (unit_dir / Path(self.selected['artifact']).name,
                 wants / Path(self.selected['artifact']).name)
        for path in paths:
            path.symlink_to(self.selected['artifact'])
        before = [path.lstat().st_ino for path in paths]
        artifacts.preflight_registration(self.selected, paths)
        self.assertEqual([path.lstat().st_ino for path in paths], before)
        self.assertEqual([os.readlink(path) for path in paths], [self.selected['artifact']] * 2)

    def test_runtime_and_persistent_paths_remain_explicit_and_separate(self):
        with patch.object(platform_support, 'systemd_registration_directories',
                          return_value=(self.config / 'systemd/user', self.root / 'systemd/user')), \
                patch.dict(os.environ, XDG_CONFIG_HOME='/unrelated/client', XDG_RUNTIME_DIR='/other/client'):
            persistent = platform_support.memory_registration_paths(self.selected)
            runtime = platform_support.memory_registration_paths(self.selected, runtime=True)
        self.assertEqual(persistent[0].parent, self.config / 'systemd/user')
        self.assertEqual(runtime[0].parent, self.root / 'systemd/user')
        self.assertNotEqual(persistent, runtime)


class ManagerDirectoryObservationTests(unittest.TestCase):
    def response(self, paths):
        return subprocess.CompletedProcess([], 0, json.dumps(dict(type='as', data=paths)), '')

    def paths(self):
        return ['/synthetic/config/systemd/user.control', '/synthetic/runtime/systemd/user.control',
                '/synthetic/runtime/systemd/transient', '/synthetic/runtime/systemd/generator.early',
                '/synthetic/config/systemd/user', '/synthetic/runtime/systemd/user']

    def test_uses_stable_manager_paths_not_client_environment(self):
        with patch.object(platform_support, 'LINUX', True), \
                patch.dict(os.environ, XDG_CONFIG_HOME='/different/client'), \
                patch.object(platform_support.subprocess, 'run', return_value=self.response(self.paths())) as run:
            self.assertEqual(platform_support.systemd_registration_directories(),
                             (Path('/synthetic/config/systemd/user'), Path('/synthetic/runtime/systemd/user')))
            self.assertEqual(run.call_count, 2)
            for call in run.call_args_list:
                self.assertIn('get-property', call.args[0])
                self.assertEqual(call.args[0][-1], 'UnitPath')

    def test_changed_overridden_or_malformed_layout_never_establishes_directories(self):
        original = self.paths()
        variants = [[], original[::-1], original + [original[0]], ['relative'] + original[1:],
                    original[:4] + ['/wrong/persistent', original[5]]]
        for paths in variants:
            with self.subTest(paths=paths), patch.object(platform_support, 'LINUX', True), \
                    patch.object(platform_support.subprocess, 'run', return_value=self.response(paths)):
                with self.assertRaises(OSError):
                    platform_support.systemd_registration_directories()
        changed = [p.replace('/config/', '/other/') for p in original]
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run', side_effect=[
                    self.response(original), self.response(changed)]):
            with self.assertRaises(OSError):
                platform_support.systemd_registration_directories()
