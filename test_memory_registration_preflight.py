"""Refuse unsafe registration before any systemd enable/start invocation."""
import os
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
        with patch.dict(os.environ, XDG_CONFIG_HOME=str(self.config)), \
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
        with patch.dict(os.environ, XDG_CONFIG_HOME=str(self.config)):
            paths = platform_support.memory_registration_paths(self.selected)
        artifacts.preflight_registration(self.selected, paths)
        self.assertFalse((self.config / 'systemd').exists())

    def test_conflicting_wants_link_and_symlinked_parent_are_refused(self):
        unit_dir = self.config / 'systemd/user'
        wants = unit_dir / 'default.target.wants'
        wants.mkdir(parents=True, mode=0o700)
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

    def test_runtime_and_persistent_paths_remain_explicit_and_separate(self):
        with patch.dict(os.environ, XDG_CONFIG_HOME=str(self.config), XDG_RUNTIME_DIR=str(self.root)):
            persistent = platform_support.memory_registration_paths(self.selected)
            runtime = platform_support.memory_registration_paths(self.selected, runtime=True)
        self.assertEqual(persistent[0].parent, self.config / 'systemd/user')
        self.assertEqual(runtime[0].parent, self.root / 'systemd/user')
        self.assertNotEqual(persistent, runtime)
