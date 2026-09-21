"""Public dispatcher executes the retained archive against synthetic installations."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts import install
import upgrade_command
import upgrade_exclusion
import upgrade_plan


class CommandTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix, self.source, self.state = [self.root / name for name in ('prefix', 'source', 'state')]
        project = Path(__file__).resolve().parent
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
        self.config = dict(state_root=str(self.state), unit_dir=str(self.root / 'units'), codex=sys.executable)
        (self.prefix / 'install.json').write_text(json.dumps(self.config))
        (self.prefix / 'install.json').chmod(0o600)
        (self.prefix / 'LICENSE').write_text('synthetic old release license bytes')

    def command(self, *args):
        return subprocess.run([sys.executable, str(self.source / 'scripts/upgrade.py'), *map(str, args)],
                              capture_output=True, text=True, timeout=60)

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
        (self.prefix / 'upgrade_command.py').unlink()
        result = self.command('--resume', loaded['plan']['directory'], '--plan', loaded['sha256'])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('ok', result.stderr)
        self.assertIsNotNone(upgrade_exclusion.read(self.prefix))
        self.assertFalse((self.prefix / 'upgrade_command.py').exists())

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
