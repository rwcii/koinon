"""How the native fixtures build a release tree, without starting any service.

Those fixtures otherwise run only under CI, because their jobs start real user
services, so a mistake in them costs a full CI round. Building a release tree
touches no service manager, so it is checked here: the pinned release extracts
with its own layout and manifest, an unreachable pin refuses, and every isolation
override reaches both layouts.

What this does not cover: the fixtures' own call sites, and the session fixture's
constructor, which queries a service manager. Those remain CI-only. The call sites
are kept honest structurally instead, by sharing one `apply_overrides` rather than
repeating the overrides inline, which is how one of them came to omit a lock
directory override.
"""
import importlib.util
import tempfile
import unittest
from pathlib import Path

from repo_root import ROOT

_spec = importlib.util.spec_from_file_location('native_upgrade', ROOT / 'scripts/test-native-upgrade.py')
native = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(native)

_install_spec = importlib.util.spec_from_file_location('native_install', ROOT / 'scripts/test-native-install.py')
install_fixture = importlib.util.module_from_spec(_install_spec)
_install_spec.loader.exec_module(install_fixture)


class PinnedReleaseFixtureTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.staging = Path(temporary.name)

    def test_the_pinned_release_extracts_with_its_own_layout_and_manifest(self):
        previous = native.materialize_release(self.staging)
        self.assertTrue((previous / 'platform_support.py').is_file(),
                        'the pinned release predates the package, so its modules are at its root')
        self.assertFalse((previous / 'koinon').exists())
        manifest = install_fixture.released_manifest(previous)
        self.assertIn('platform_support.py', manifest)
        self.assertNotIn('koinon/platform_support.py', manifest)

    def test_an_unavailable_pin_refuses_rather_than_upgrading_a_release_to_itself(self):
        with self.assertRaises(RuntimeError) as refusal:
            native.materialize_release(self.staging, ref='0' * 40)
        self.assertIn('unavailable', str(refusal.exception))

    def test_both_release_layouts_receive_every_isolation_override(self):
        """A missing override moves that state to the host default after replacement."""
        previous = native.materialize_release(self.staging)
        for release, module in ((previous, 'platform_support.py'), (None, 'koinon/platform_support.py')):
            with self.subTest(release='pinned' if release else 'checkout'):
                fixture = install_fixture.Fixture('systemd', session=True, release=release)
                self.addCleanup(lambda root=fixture.root: __import__('shutil').rmtree(root, ignore_errors=True))
                self.assertTrue((fixture.source / module).is_file())
                text = (fixture.source / module).read_text()
                self.assertIn('CLAUDE_CONFIG_DIR', text)
                self.assertIn('def participant_lock_dir():', text)
                upgraded = fixture.root / 'upgraded'
                upgraded.mkdir()
                for name in install_fixture.FILES:
                    target = upgraded / name
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes((ROOT / name).read_bytes())
                fixture.apply_overrides(upgraded)
                after = (upgraded / 'koinon/platform_support.py').read_text()
                self.assertIn('CLAUDE_CONFIG_DIR', after)
                self.assertIn('def participant_lock_dir():', after)


if __name__ == '__main__':
    unittest.main()
