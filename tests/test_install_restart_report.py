import contextlib
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from test_install import installer


class RestartReportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.prefix = self.root / 'runtime'
        self.units = self.root / 'units'
        self.units.mkdir()

    def unit(self, digit='a', *, prefix=None, legacy=False):
        rendered = installer.units(prefix or self.prefix, self.root / 'state',
                                   'synthetic-thread', 'synthetic-peer', '/synthetic-repo',
                                   '/usr/bin/python3', '/synthetic/codex',
                                   instance=digit * 16, legacy=legacy)
        name, body = next(iter(rendered.items()))
        path = self.units / name
        path.write_text(body)
        return path

    def observation(self, path, state, *, fragment=None):
        return f'Id={path.name}\nActiveState={state}\nFragmentPath={fragment or path}\n'

    def report(self, *, output='', no_start=False, manager='systemd', effect=None, code=0):
        stream = io.StringIO()
        result = subprocess.CompletedProcess([], code, output, '')
        with mock.patch.object(installer.platform_support, 'SERVICE_MANAGER', manager), \
             mock.patch.object(installer.subprocess, 'run', return_value=result,
                               side_effect=effect) as run, contextlib.redirect_stdout(stream):
            installer.report_session_restarts(self.prefix, self.units, no_start=no_start)
        return stream.getvalue(), run

    def test_active_and_inactive_owned_units_only_and_no_runtime_mutation(self):
        active = self.unit()
        stopped = self.unit('b', legacy=True)
        foreign = self.unit('c', prefix=self.root / 'other')
        before = {p: p.read_bytes() for p in (active, stopped, foreign)}
        text, run = self.report(output=self.observation(active, 'active') + '\n' +
                               self.observation(stopped, 'inactive'))
        self.assertIn('explicit restart required: ' + active.name, text)
        self.assertIn('inactive at observation: ' + stopped.name, text)
        self.assertNotIn(foreign.name, text)
        argv = run.call_args.args[0]
        self.assertEqual(argv, ['systemctl', '--user', 'show', '--property=Id',
                                '--property=ActiveState', '--property=FragmentPath',
                                *sorted((active.name, stopped.name))])
        self.assertNotIn(foreign.name, argv)
        self.assertEqual(run.call_count, 1)
        self.assertEqual(run.call_args.kwargs, dict(capture_output=True, text=True, timeout=5))
        self.assertEqual(before, {p: p.read_bytes() for p in before})

    def test_no_start_does_not_query_manager(self):
        path = self.unit()
        text, run = self.report(no_start=True)
        run.assert_not_called()
        self.assertIn(path.name, text)
        self.assertIn('service query disabled by --no-start', text)
        self.assertNotIn('restart required:', text)

    def test_manual_platform_has_no_service_manager_calls(self):
        self.unit()
        text, run = self.report(manager=None)
        run.assert_not_called()
        self.assertIn('manually managed supervisors explicitly', text)
        self.assertIn('no supported service manager', text)

    def test_timeout_missing_manager_and_nonzero_are_unknown(self):
        path = self.unit()
        for effect in (FileNotFoundError(), subprocess.TimeoutExpired('systemctl', 5)):
            with self.subTest(effect=type(effect).__name__):
                text, _ = self.report(effect=effect)
                self.assertIn('state unverified: ' + path.name, text)
                self.assertNotIn('restart required:', text)
        text, _ = self.report(code=1)
        self.assertIn('service manager query failed', text)

    def test_foreign_and_symlinked_units_are_not_queried(self):
        unsafe = self.unit()
        unsafe.write_text('[Service]\nExecStart=/unrelated\n')
        link = self.unit('b')
        link.unlink()
        link.symlink_to(unsafe)
        text, run = self.report()
        run.assert_not_called()
        self.assertEqual(text.count('ownership unverified:'), 2)

    def test_wrong_fragment_missing_duplicate_and_unknown_states_are_unknown(self):
        path = self.unit()
        valid = self.observation(path, 'active')
        for output in (self.observation(path, 'active', fragment='/other/unit'), '',
                       valid + '\n' + valid, self.observation(path, 'novel')):
            with self.subTest(output=output):
                text, _ = self.report(output=output)
                self.assertIn('state unverified: ' + path.name, text)
                self.assertNotIn('restart required:', text)

    def test_ownership_is_rechecked_after_query(self):
        path = self.unit()
        response = self.observation(path, 'active')
        def replace(*args, **kwargs):
            path.write_text('[Service]\nExecStart=/foreign\n')
            return subprocess.CompletedProcess([], 0, response, '')
        text, _ = self.report(effect=replace)
        self.assertIn('ownership or fragment changed', text)
        self.assertNotIn('restart required:', text)

    def test_missing_unit_directory_still_explains_manual_restart(self):
        self.units.rmdir()
        text, run = self.report()
        run.assert_not_called()
        self.assertIn('does not reload', text)
        self.assertIn('newly started by this installation load the installed files', text)

    def test_repeated_configure_reports_existing_session_without_changing_it(self):
        path = self.unit()
        before = path.read_bytes()
        command = [sys.executable, 'scripts/install.py', '--configure-codex',
                   '--codex', sys.executable, '--codex-home', str(self.root / 'codex'),
                   '--prefix', str(self.prefix), '--state-dir', str(self.root / 'state'),
                   '--unit-dir', str(self.units), '--no-start']
        for _ in range(2):
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            # Linux inspects units without querying; macOS gives manual guidance.
            self.assertIn('does not reload existing session supervisors', result.stdout)
            self.assertIn('manually managed supervisors explicitly', result.stdout)
            self.assertEqual(path.read_bytes(), before)

    def test_inventory_limit_does_not_query_partial_inventory(self):
        for i in range(129):
            (self.units / f'koinon-session-{i:016x}.service').write_text('unused')
        text, run = self.report()
        run.assert_not_called()
        self.assertIn('inventory incomplete', text)


if __name__ == '__main__':
    unittest.main()
