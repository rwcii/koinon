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
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

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
        paths, modules = list(sys.path), dict(sys.modules)
        manifest = install_fixture.released_manifest(previous)
        self.assertEqual(sys.path, paths)
        self.assertEqual(sys.modules, modules)
        self.assertIn('platform_support.py', manifest)
        self.assertNotIn('koinon/platform_support.py', manifest)

    def test_manifest_read_does_not_execute_installer_code(self):
        scripts = self.staging / 'scripts'
        scripts.mkdir()
        installer = scripts / 'install.py'
        installer.write_text("raise RuntimeError('installer must not execute')\nFILES = ('session.py',)\n")
        self.assertEqual(install_fixture.released_manifest(self.staging), ('session.py',))
        installer.write_text("FILES = tuple(['session.py'])\n")
        with self.assertRaises(ValueError):
            install_fixture.released_manifest(self.staging)

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


class InitialBindingRefreshTests(unittest.TestCase):
    def test_only_concurrent_observation_refusals_retry_with_a_bound(self):
        def reply(value, code=1):
            return subprocess.CompletedProcess([], code, json.dumps(value), '')
        retry = reply(dict(ok=False, code='binding_observation_changed', recovery='retry'))
        success = reply(dict(ok=True), 0)
        denied = reply(dict(ok=False, code='binding_changed', recovery='retry'))
        for responses, succeeds, count in (([retry, success], True, 2),
                                            ([denied, success], False, 1),
                                            ([retry] * 3, False, 3)):
            with self.subTest(succeeds=succeeds, attempts=count), \
                    patch.object(native.subprocess, 'run', side_effect=responses) as run:
                if succeeds:
                    native.refresh_binding(SimpleNamespace(env={}), ['python', 'bridge.py'], 'a' * 64)
                else:
                    with self.assertRaises(RuntimeError) as failure:
                        native.refresh_binding(SimpleNamespace(env={}), ['python', 'bridge.py'], 'a' * 64)
                    self.assertEqual(str(failure.exception).count('returncode'), count)
                self.assertEqual(run.call_count, count)



class CoordinatorTimeoutEvidenceTests(unittest.TestCase):
    """A coordinator that exceeds its timeout leaves evidence of where it stopped (#158)."""

    def test_timeout_names_the_phase_status_processes_and_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            prefix, operation = root / 'prefix', root / 'operation'
            (prefix / '.upgrade').mkdir(parents=True)
            operation.mkdir()
            (prefix / '.upgrade/current.json').write_text(json.dumps(dict(operation=str(operation), plan='0' * 64)))
            (operation / 'phase.json').write_text(json.dumps(dict(step=7)))
            coordinator = root / 'upgrade.py'
            coordinator.write_text(
                'import sys, time\n'
                "if '--status' in sys.argv:\n"
                "    print('{\"synthetic\": \"status\"}')\n"
                '    raise SystemExit(0)\n'
                "print('partial output', flush=True)\n"
                'time.sleep(60)\n')
            command = [sys.executable, str(coordinator), '--resume', str(operation), '--plan', str(prefix)]
            with patch.object(native, 'COORDINATOR_TIMEOUT', 1):
                with self.assertRaises(RuntimeError) as caught:
                    native.run_coordinator(prefix, command, None, [3])
        message = str(caught.exception)
        self.assertTrue(message.startswith('public coordinator timed out: '), message)
        evidence = json.loads(message.split(': ', 1)[1])
        self.assertEqual((evidence['command'], evidence['timeout'], evidence['interrupted'], evidence['phase']),
                         ('resume', 1, [3], dict(step=7)))
        self.assertEqual((evidence['status']['exit_status'], evidence['status']['stdout'].strip()),
                         (0, '{"synthetic": "status"}'))
        self.assertTrue(any(str(coordinator) in line and '--status' not in line for line in evidence['processes']),
                        evidence['processes'])
        self.assertEqual(evidence['stdout'], 'partial output\n')

    def test_a_finished_coordinator_returns_its_result(self):
        command = [sys.executable, '-c', 'import sys; print("done"); sys.exit(3)']
        result = native.run_coordinator(Path('/synthetic/prefix'), command, None, [])
        self.assertEqual((result.returncode, result.stdout), (3, 'done\n'))


if __name__ == '__main__':
    unittest.main()
