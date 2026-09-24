"""A refusal for an unsafe path must name the condition that failed, not the path alone.

The predicate tests build synthetic stat records rather than real files. The rule
reads only the mode, the owner and the file type, so a synthetic record exercises
it exactly, and the result does not depend on the account running the suite: its
umask, its primary group's membership, a setgid parent directory that would hand
down its own group, or whether the suite runs as root.
"""
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest import mock

from koinon import install_state
from koinon import memory_service_artifacts as artifacts
from koinon import path_permissions
from koinon import runtime_names
from koinon import upgrade_manifest as manifest
from koinon import work_policy
from test_memory_service_config import record

OWNER = 4242
STRANGER = 4243


def stat_record(mode, uid=OWNER, gid=99):
    """A synthetic os.stat_result carrying only the fields the rule reads."""
    return os.stat_result((mode, 1, 1, 1, uid, gid, 0, 0, 0, 0))


class AncestorFaultTests(unittest.TestCase):
    def test_owner_only_directories_are_accepted(self):
        for mode in (0o700, 0o750, 0o755, 0o500):
            with self.subTest(mode=mode):
                info = stat_record(stat.S_IFDIR | mode)
                self.assertIsNone(path_permissions.ancestor_fault('/d', info, OWNER))

    def test_group_and_other_write_are_both_refused(self):
        for mode in (0o775, 0o770, 0o707, 0o777, 0o702, 0o720):
            with self.subTest(mode=mode):
                info = stat_record(stat.S_IFDIR | mode)
                fault = path_permissions.ancestor_fault('/d', info, OWNER)
                self.assertIn('%04o' % mode, fault)
                self.assertIn(path_permissions.REMEDY, fault)

    def test_a_group_that_looks_exclusive_is_still_refused(self):
        """Group membership cannot be proved complete, so an exclusive look is not proof."""
        info = stat_record(stat.S_IFDIR | 0o775, gid=OWNER)
        self.assertIsNotNone(path_permissions.ancestor_fault('/d', info, OWNER))

    def test_a_root_owned_ancestor_is_accepted_and_a_stranger_is_not(self):
        self.assertIsNone(
            path_permissions.ancestor_fault('/d', stat_record(stat.S_IFDIR | 0o755, uid=0), OWNER))
        fault = path_permissions.ancestor_fault(
            '/d', stat_record(stat.S_IFDIR | 0o755, uid=STRANGER), OWNER)
        self.assertIn('owned by uid %d' % STRANGER, fault)

    def test_only_a_root_owned_sticky_directory_may_be_writable(self):
        sticky = stat.S_IFDIR | stat.S_ISVTX | 0o777
        self.assertIsNone(path_permissions.ancestor_fault('/tmp', stat_record(sticky, uid=0), OWNER))
        self.assertIsNotNone(
            path_permissions.ancestor_fault('/tmp', stat_record(sticky), OWNER))

    def test_a_non_directory_ancestor_is_named_as_such_not_as_a_mode(self):
        fault = path_permissions.ancestor_fault('/f', stat_record(stat.S_IFREG | 0o600), OWNER)
        self.assertIn('is not a directory', fault)
        self.assertNotIn(path_permissions.REMEDY, fault)


class TargetFaultTests(unittest.TestCase):
    def test_the_target_may_not_be_owned_by_root_or_a_stranger(self):
        for uid in (0, STRANGER):
            with self.subTest(uid=uid):
                fault = path_permissions.target_fault(
                    '/d', stat_record(stat.S_IFDIR | 0o755, uid=uid), OWNER)
                self.assertIn('not by this user', fault)

    def test_no_temporary_directory_exception_applies_to_the_target(self):
        sticky = stat_record(stat.S_IFDIR | stat.S_ISVTX | 0o777, uid=0)
        self.assertIsNotNone(path_permissions.target_fault('/tmp', sticky, OWNER))

    def test_a_wrong_owner_is_not_described_as_a_mode_to_correct(self):
        fault = path_permissions.target_fault(
            '/d', stat_record(stat.S_IFDIR | 0o755, uid=STRANGER), OWNER)
        self.assertNotIn(path_permissions.REMEDY, fault)
        self.assertNotIn('mode', fault)

    def test_the_description_names_path_mode_and_remedy(self):
        described = path_permissions.describe('/d', stat_record(stat.S_IFDIR | 0o775))
        self.assertIn('/d', described)
        self.assertIn('0775', described)
        self.assertIn(path_permissions.REMEDY, described)

    def test_the_sticky_bit_does_not_hide_the_mode(self):
        described = path_permissions.describe('/d', stat_record(stat.S_IFDIR | stat.S_ISVTX | 0o775))
        self.assertIn('1775', described)


class SandboxOwnerTests(unittest.TestCase):
    """An agent sandbox in a user namespace shows root-owned paths as the unmapped uid."""

    def test_the_unmapped_owner_adds_the_sandbox_remedy_to_both_rules(self):
        record_ = stat_record(stat.S_IFDIR | 0o755, uid=STRANGER)
        with mock.patch.object(path_permissions.platform_support, 'overflow_uid', return_value=STRANGER):
            for fault in (path_permissions.ancestor_fault('/', record_, OWNER),
                          path_permissions.target_fault('/', record_, OWNER)):
                with self.subTest(fault=fault):
                    self.assertIn('owned by uid %d' % STRANGER, fault)
                    self.assertIn('can show this owner', fault)
                    self.assertIn('outside the sandbox', fault)

    def test_another_owner_and_no_mapping_keep_the_plain_refusal(self):
        record_ = stat_record(stat.S_IFDIR | 0o755, uid=STRANGER)
        for unmapped in (None, STRANGER + 1):
            with self.subTest(unmapped=unmapped), \
                    mock.patch.object(path_permissions.platform_support, 'overflow_uid', return_value=unmapped):
                fault = path_permissions.ancestor_fault('/', record_, OWNER)
                self.assertIn('owned by uid %d' % STRANGER, fault)
                self.assertNotIn(path_permissions.SANDBOX_HINT, fault)

    def test_the_hint_never_accepts_the_path(self):
        with mock.patch.object(path_permissions.platform_support, 'overflow_uid', return_value=STRANGER):
            self.assertIsNotNone(path_permissions.ancestor_fault(
                '/', stat_record(stat.S_IFDIR | 0o755, uid=STRANGER), OWNER))


class RefusalMessageTests(unittest.TestCase):
    """Each operator-facing check must report the mode and the remedy, from real paths."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()

    def _widened(self, name):
        path = self.root / name
        path.mkdir(mode=0o700, parents=True)
        path.chmod(0o775)
        return path

    def _assert_names_mode_and_remedy(self, message, path):
        self.assertIn(str(path), message)
        self.assertIn('0775', message)
        self.assertIn(path_permissions.REMEDY, message)

    def test_manifest_selection_refusal_names_the_mode_and_remedy(self):
        source = self._widened('source')
        with self.assertRaises(manifest.ManifestError) as caught:
            manifest.check_root(source)
        # The selected root is checked inside the ancestor walk, so it reports as one.
        self.assertIn('unsafe manifest ancestor', str(caught.exception))
        self._assert_names_mode_and_remedy(str(caught.exception), source)

    def test_manifest_ancestor_refusal_names_the_offending_ancestor(self):
        parent = self._widened('parent')
        child = parent / 'child'
        child.mkdir(mode=0o700)
        with self.assertRaises(manifest.ManifestError) as caught:
            manifest.check_root(child)
        self._assert_names_mode_and_remedy(str(caught.exception), parent)
        self.assertNotIn(str(child), str(caught.exception))

    def test_registration_refusal_names_the_mode_and_the_offending_ancestor(self):
        config = self._widened('config')
        paths = (config / 'systemd/user/koinon-memory.service',)
        with self.assertRaises(artifacts.RegistrationPathError) as caught:
            artifacts.preflight_registration(record(), paths)
        self.assertEqual(caught.exception.paths, (str(config),))
        self._assert_names_mode_and_remedy(str(caught.exception), config)

    def test_guidance_parent_refusal_names_the_mode_and_remedy(self):
        home = self.root / 'home'
        home.mkdir(mode=0o700)
        guidance = home / 'AGENTS.md'
        guidance.write_text('guidance\n')
        guidance.chmod(0o600)
        home.chmod(0o775)
        with self.assertRaises(ValueError) as caught:
            work_policy.guidance_path(str(guidance))
        self._assert_names_mode_and_remedy(str(caught.exception), home)

    def test_installation_prefix_refusal_keeps_the_fixed_error_vocabulary(self):
        """The prefix check reports a code, not a detail; documentation must not claim otherwise."""
        prefix = self._widened('prefix')
        with self.assertRaises(runtime_names.NameConflict) as caught:
            with install_state.locked(prefix):
                pass
        self.assertEqual(caught.exception.code, 'invalid_install_configuration')
        self.assertNotIn(path_permissions.REMEDY, str(caught.exception))


if __name__ == '__main__':
    unittest.main()
