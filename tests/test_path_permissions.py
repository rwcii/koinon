"""A refusal for a writable path must name the mode and the remedy, not the path alone."""
import grp
import os
from pathlib import Path
import pwd
import stat
import tempfile
import unittest

from koinon import memory_service_artifacts as artifacts
from koinon import path_permissions
from koinon import upgrade_manifest as manifest
from koinon import work_policy
from test_memory_service_config import record


class WritableByOthersTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()

    def _info(self, mode):
        path = self.root / ('mode%04o' % mode)
        path.mkdir(mode=0o700)
        path.chmod(mode)
        return path, path.lstat()

    def test_owner_only_modes_are_accepted(self):
        for mode in (0o700, 0o750, 0o755, 0o500):
            with self.subTest(mode=mode):
                self.assertFalse(path_permissions.writable_by_others(self._info(mode)[1]))

    def test_group_and_other_write_are_both_refused(self):
        for mode in (0o775, 0o770, 0o707, 0o777, 0o702, 0o720):
            with self.subTest(mode=mode):
                self.assertTrue(path_permissions.writable_by_others(self._info(mode)[1]))

    def test_an_apparently_private_group_is_still_refused(self):
        """Group membership cannot be proved complete, so an exclusive look is not proof."""
        owner = pwd.getpwuid(os.geteuid())
        group = grp.getgrgid(owner.pw_gid)
        self.assertEqual([member for member in group.gr_mem if member != owner.pw_name], [])
        path, _ = self._info(0o775)
        self.assertEqual(path.lstat().st_gid, owner.pw_gid)
        self.assertTrue(path_permissions.writable_by_others(path.lstat()))

    def test_description_names_path_mode_and_remedy(self):
        path, info = self._info(0o775)
        described = path_permissions.describe(path, info)
        self.assertIn(str(path), described)
        self.assertIn('0775', described)
        self.assertIn(path_permissions.REMEDY, described)

    def test_setuid_and_sticky_bits_do_not_hide_the_mode(self):
        path, _ = self._info(0o1775)
        info = path.lstat()
        self.assertTrue(info.st_mode & stat.S_ISVTX)
        self.assertIn('1775', path_permissions.describe(path, info))

    def test_only_a_root_owned_sticky_directory_is_a_temporary_root(self):
        _, info = self._info(0o1777)
        self.assertFalse(path_permissions.temporary_root(info))  # Owned by this user, not root.


class RefusalMessageTests(unittest.TestCase):
    """Each operator-facing check must report the mode, not only the path."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()

    def test_manifest_root_refusal_names_the_mode_and_remedy(self):
        source = self.root / 'source'
        source.mkdir(mode=0o700)
        source.chmod(0o775)
        with self.assertRaises(manifest.ManifestError) as caught:
            manifest.check_root(source)
        message = str(caught.exception)
        # The selected root is checked inside the ancestor walk, so it reports as one.
        self.assertIn('unsafe manifest ancestor', message)
        self.assertIn(str(source), message)
        self.assertIn('0775', message)
        self.assertIn(path_permissions.REMEDY, message)

    def test_manifest_ancestor_refusal_names_the_offending_ancestor(self):
        parent = self.root / 'parent'
        child = parent / 'child'
        parent.mkdir(mode=0o700)
        child.mkdir(mode=0o700)
        parent.chmod(0o775)
        with self.assertRaises(manifest.ManifestError) as caught:
            manifest.check_root(child)
        message = str(caught.exception)
        self.assertIn('unsafe manifest ancestor', message)
        self.assertIn(str(parent), message)
        self.assertIn('0775', message)

    def test_registration_refusal_names_the_mode_and_the_offending_ancestor(self):
        config = self.root / 'config'
        config.mkdir(mode=0o700)
        config.chmod(0o775)
        paths = (config / 'systemd/user/koinon-memory.service',)
        with self.assertRaises(artifacts.RegistrationPathError) as caught:
            artifacts.preflight_registration(record(), paths)
        message = str(caught.exception)
        self.assertEqual(caught.exception.paths, (str(config),))
        self.assertIn(str(config), message)
        self.assertIn('0775', message)
        self.assertIn(path_permissions.REMEDY, message)

    def test_guidance_parent_refusal_names_the_mode_and_remedy(self):
        home = self.root / 'home'
        home.mkdir(mode=0o700)
        guidance = home / 'AGENTS.md'
        guidance.write_text('guidance\n')
        guidance.chmod(0o600)
        home.chmod(0o775)
        with self.assertRaises(ValueError) as caught:
            work_policy.guidance_path(str(guidance))
        message = str(caught.exception)
        self.assertIn(str(home), message)
        self.assertIn('0775', message)
        self.assertIn(path_permissions.REMEDY, message)


if __name__ == '__main__':
    unittest.main()
