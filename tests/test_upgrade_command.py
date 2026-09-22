"""Public dispatcher executes the retained archive against synthetic installations."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import os

from scripts import install
from koinon import platform_support
from koinon import upgrade_command
from koinon import upgrade_exclusion
from koinon import upgrade_plan
from repo_root import ROOT


class CommandTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix, self.source, self.state = [self.root / name for name in ('prefix', 'source', 'state')]
        project = ROOT
        for root in (self.prefix, self.source):
            root.mkdir(mode=0o700)
            for name in install.FILES:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                shutil.copyfile(project / name, target)
                target.chmod(0o600)
            for path in root.rglob('*'):
                if path.is_dir():
                    path.chmod(0o700)
        self.state.mkdir(mode=0o700)
        # These are synthetic installations. Without controlled sources, discovery read
        # the real host's loaded services, so the result depended on the machine and
        # any unparseable third-party definition on it failed the unit test.
        # Most of these tests run the dispatcher as a subprocess, which a patch in this
        # process cannot reach, so the child is given the same controlled sources through
        # its own import. Memory process detection is deliberately left real: the
        # foreground regression depends on observing an actual process.
        self.units = self.root / 'units'
        sources = dict(directories=[str(self.units)], loaded_artifacts=[],
                       loaded_discovery='synthetic_isolated_inventory')
        isolated = patch.object(platform_support, 'upgrade_service_sources',
                                return_value=sources)
        isolated.start()
        self.addCleanup(isolated.stop)
        harness = self.root / 'harness'
        harness.mkdir(mode=0o700)
        (harness / 'sitecustomize.py').write_text(
            'import sys\n'
            f'sys.path.insert(0, {str(self.source)!r})\n'
            'import koinon.platform_support\n'
            f'koinon.platform_support.upgrade_service_sources = lambda prefix: {sources!r}\n')
        self.env = {**os.environ, 'PYTHONPATH': str(harness)}
        self.config = dict(state_root=str(self.state), unit_dir=str(self.root / 'units'), codex=sys.executable)
        (self.prefix / 'install.json').write_text(json.dumps(self.config))
        (self.prefix / 'install.json').chmod(0o600)
        (self.prefix / 'LICENSE').write_text('synthetic old release license bytes')

    def command(self, *args):
        return subprocess.run([sys.executable, str(self.source / 'scripts/upgrade.py'), *map(str, args)],
                              capture_output=True, text=True, timeout=60, env=self.env)

    def test_subprocess_discovery_reads_the_controlled_sources(self):
        """A patch in this process cannot reach the child, so assert the child's own scope.

        The reported discovery status can only be the synthetic one when the child
        itself used the controlled sources rather than this machine's real services.
        """
        units = Path(self.config['unit_dir'])
        units.mkdir(mode=0o700)
        (units / 'synthetic-isolation.service').write_text(
            '[Service]\nExecStart=/synthetic/python ' + str(self.prefix / 'memory.py') + ' serve\n')
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(result.returncode, 1, result.stdout)
        report = json.loads(result.stderr)
        self.assertEqual(report['service_ownership']['loaded_discovery'],
                         'synthetic_isolated_inventory')

    def test_public_command_executes_archive_and_reports_completed_upgrade(self):
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertTrue(value['ok'])
        self.assertEqual((self.prefix / 'LICENSE').read_bytes(), (self.source / 'LICENSE').read_bytes())
        self.assertEqual(json.loads((self.prefix / 'install.json').read_text()), self.config)
        status = self.command('--status', self.prefix)
        self.assertEqual(status.returncode, 0, status.stderr)
        selected = json.loads(status.stdout)['result']
        self.assertTrue(selected['finished'])
        resume = self.command('--resume', selected['operation'], '--plan', selected['plan'])
        self.assertEqual(resume.returncode, 0, resume.stderr)
        self.assertEqual(json.loads(resume.stdout), value)

    def test_completed_operation_does_not_pin_old_checkout_for_next_upgrade(self):
        first = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(first.returncode, 0, first.stderr)
        selected = json.loads(self.command('--status', self.prefix).stdout)['result']
        (self.source / 'LICENSE').write_text('synthetic subsequent release')
        status = self.command('--status', self.prefix)
        self.assertEqual(status.returncode, 0, status.stderr)
        self.assertTrue(json.loads(status.stdout)['result']['finished'])
        old_resume = self.command('--resume', selected['operation'], '--plan', selected['plan'])
        self.assertEqual(old_resume.returncode, 0, old_resume.stderr)
        second = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual((self.prefix / 'LICENSE').read_text(), 'synthetic subsequent release')

    def test_resume_uses_archive_after_installed_dispatcher_is_missing(self):
        loaded = upgrade_command.prepare(self.prefix, self.source)
        self.assertEqual(loaded['phase']['step'], 0)
        # The installed dispatcher is intentionally unavailable. Recovery runs
        # from the retained archive; it must still refuse an unexpected runtime
        # preimage rather than silently repair the deletion.
        (self.prefix / 'koinon/upgrade_command.py').unlink()
        result = self.command('--resume', loaded['plan']['directory'], '--plan', loaded['sha256'])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('ok', result.stderr)
        self.assertIsNotNone(upgrade_exclusion.read(self.prefix))
        self.assertFalse((self.prefix / 'koinon/upgrade_command.py').exists())

    def test_missing_state_root_has_domain_refusal_without_recovery_mutations(self):
        self.state.rmdir()
        before = (self.prefix / 'install.json').read_bytes()
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(result.returncode, 1, result.stdout)
        report = json.loads(result.stderr)
        self.assertFalse(report['ok'])
        self.assertEqual(report['code'], 'missing_state_root')
        self.assertEqual(report['path'], str(self.state))
        self.assertEqual(report['recovery']['code'], 'missing_state_root')
        self.assertEqual(report['recovery']['phase'], 'refused_before_shutdown')
        self.assertIn('Do not create', report['recovery']['preserve'])
        self.assertNotIn('[Errno', report['error'])
        self.assertFalse(self.state.exists())
        self.assertFalse((self.prefix / '.upgrade').exists())
        self.assertIsNone(upgrade_exclusion.read(self.prefix))
        self.assertEqual((self.prefix / 'install.json').read_bytes(), before)
        self.assertEqual((self.prefix / 'LICENSE').read_text(), 'synthetic old release license bytes')

    def test_non_directory_state_ancestor_has_distinct_domain_refusal(self):
        self.state.rmdir()
        self.state.write_text('preserve this unrelated file')
        configured = self.state / 'nested-state'
        self.config['state_root'] = str(configured)
        (self.prefix / 'install.json').write_text(json.dumps(self.config))
        before = (self.prefix / 'install.json').read_bytes()
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(result.returncode, 1, result.stdout)
        report = json.loads(result.stderr)
        self.assertEqual(report['code'], 'invalid_state_root')
        self.assertEqual(report['path'], str(configured))
        self.assertEqual(report['recovery']['code'], 'invalid_state_root')
        self.assertIn('non-directory component', report['recovery']['next_step'])
        self.assertIn('Do not delete or overwrite the obstructing file', report['recovery']['preserve'])
        self.assertEqual(report['recovery']['phase'], 'refused_before_shutdown')
        self.assertNotIn('[Errno', report['error'])
        self.assertEqual(self.state.read_text(), 'preserve this unrelated file')
        self.assertEqual((self.prefix / 'install.json').read_bytes(), before)
        self.assertFalse((self.prefix / '.upgrade').exists())
        self.assertIsNone(upgrade_exclusion.read(self.prefix))

    def test_unowned_memory_refuses_before_marker_or_runtime_change(self):
        home = self.state / 'memory' / ('a' * 16)
        home.mkdir(parents=True, mode=0o700)
        home.parent.chmod(0o700)
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertNotEqual(result.returncode, 0)
        report = json.loads(result.stderr)
        self.assertEqual(report['memory_ownership']['action'], 'refused')
        self.assertEqual(report['recovery']['code'], 'unowned_memory_requires_inventory')
        self.assertEqual(report['recovery']['phase'], 'refused_before_shutdown')
        self.assertIn('Do not delete', report['recovery']['preserve'])
        self.assertIn('#recovering-from-unowned-memory-refusal', report['recovery']['guide'])
        self.assertEqual(json.loads((self.prefix / 'install.json').read_text()), self.config)
        self.assertFalse((self.prefix / '.upgrade').exists())

    def test_external_unit_outside_state_refuses_with_private_discovery_report(self):
        units = Path(self.config['unit_dir'])
        units.mkdir(mode=0o700)
        definition = units / 'synthetic-repository-memory.service'
        contents = ('[Service]\nExecStart=/synthetic/python ' + str(self.prefix / 'memory.py')
                    + ' --state-dir /synthetic/outside-state serve\n')
        definition.write_text(contents)
        result = self.command('--prefix', self.prefix, '--source', self.source)
        self.assertEqual(result.returncode, 1, result.stdout)
        report = json.loads(result.stderr)
        self.assertEqual(report['service_ownership']['action'], 'refused')
        self.assertEqual(report['service_ownership']['findings'][0]['path'], str(definition))
        self.assertEqual(report['recovery']['code'], 'unowned_service_requires_inventory')
        self.assertEqual(definition.read_text(), contents)
        self.assertFalse((self.prefix / '.upgrade').exists())
        self.assertEqual(json.loads((self.prefix / 'install.json').read_text()), self.config)
        self.assertEqual((self.prefix / 'LICENSE').read_text(), 'synthetic old release license bytes')

    def test_external_foreground_memory_process_refuses_without_signalling_it(self):
        script = self.prefix / 'memory.py'
        script.write_text('import time\ntime.sleep(60)\n')
        job = subprocess.Popen([sys.executable, str(script), 'serve'],
                               stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            result = self.command('--prefix', self.prefix, '--source', self.source)
            self.assertEqual(result.returncode, 1, result.stdout)
            report = json.loads(result.stderr)
            self.assertEqual(report['recovery']['code'], 'unowned_service_requires_inventory')
            self.assertEqual(report['service_ownership']['processes'][0]['pid'], job.pid)
            self.assertEqual(report['service_ownership']['processes'][0]['ownership'], 'unowned')
            self.assertIsNone(job.poll())
            self.assertFalse((self.prefix / '.upgrade').exists())
        finally:
            job.terminate()
            job.wait(timeout=5)

    def test_lost_exclusion_publication_has_discoverable_phase_zero_resume(self):
        activate = upgrade_exclusion.Exclusion.activate_locked
        def interrupted(owner, installed):
            activate(owner, installed)
            raise OSError('synthetic lost marker reply')
        with patch.object(upgrade_exclusion.Exclusion, 'activate_locked', new=interrupted):
            with self.assertRaises(OSError):
                upgrade_command.prepare(self.prefix, self.source)
        status = self.command('--status', self.prefix)
        selected = json.loads(status.stdout)['result']
        self.assertEqual(selected['phase'], 'prepared')
        resumed = self.command('--resume', selected['operation'], '--plan', selected['plan'])
        self.assertEqual(resumed.returncode, 0, resumed.stderr)
