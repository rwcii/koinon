"""Typed native manager observations never substitute labels for ownership."""
import json
import os
import subprocess
import unittest
from unittest.mock import patch

import platform_support


class SystemdObservationTests(unittest.TestCase):
    name = 'koinon-memory-0123456789abcdef.service'
    path = '/org/freedesktop/systemd1/unit/synthetic'

    def response(self, *values):
        return subprocess.CompletedProcess([], 0, '\n'.join(json.dumps(dict(type=kind, data=data))
                                                            for kind, data in values), '')

    def fixture(self):
        listed = self.response(('a(ssssssouso)', [[[self.name, 'synthetic', 'loaded', 'active',
                                                  'running', '', self.path, 0, '', '/']]]))
        unit = self.response(('s', self.name), ('s', '/synthetic/owned.service'),
                             ('s', 'loaded'), ('s', 'active'), ('ay', list(range(16))))
        service = self.response(('a(sasbttttuii)', [['/synthetic/python',
                               ['/synthetic/python', '/synthetic/prefix/memory_service.py', 'run'],
                               False, 1, 2, 0, 0, 42, 0, 0]]), ('u', 42))
        return [listed, unit, service, unit, service]

    def test_reads_exact_arguments_path_and_pid_without_activation(self):
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run', side_effect=self.fixture()) as run:
            observed = platform_support.systemd_service_observation(self.name)
        self.assertEqual(observed['status'], 'observed')
        self.assertEqual(observed['artifact'], '/synthetic/owned.service')
        self.assertEqual(observed['argv'], ['/synthetic/python', '/synthetic/prefix/memory_service.py', 'run'])
        self.assertEqual(observed['pid'], 42)
        for call in run.call_args_list:
            args = call.args[0]
            self.assertEqual(args[:2], ['busctl', '--user'])
            self.assertIn('--auto-start=no', args)
            self.assertIn('--allow-interactive-authorization=no', args)
            self.assertFalse(any(x in args for x in ('StartUnit', 'StopUnit', 'EnableUnitFiles')))

    def test_changed_manager_evidence_stays_unknown(self):
        results = self.fixture()
        results[-1] = self.response(('a(sasbttttuii)', [['/different', ['/different'], False, 1, 2, 0, 0, 99, 0, 0]]), ('u', 99))
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run', side_effect=results):
            self.assertEqual(platform_support.systemd_service_observation(self.name),
                             dict(status='unknown', reason='observation_changed'))

    def test_unavailable_or_malformed_response_never_means_absent(self):
        for result in (OSError(), subprocess.TimeoutExpired('busctl', 5),
                       subprocess.CompletedProcess([], 0, '{"type":"s","type":"s","data":"x"}', ''),
                       subprocess.CompletedProcess([], 0, '{}', '')):
            with self.subTest(result=result), patch.object(platform_support, 'LINUX', True), \
                    patch.object(platform_support.subprocess, 'run') as run:
                if isinstance(result, Exception):
                    run.side_effect = result
                else:
                    run.return_value = result
                self.assertEqual(platform_support.systemd_service_observation(self.name)['status'], 'unknown')

    def test_structured_not_found_result_is_absent(self):
        missing = self.response(('a(ssssssouso)', [[[self.name, self.name, 'not-found', 'inactive',
                                                   'dead', '', self.path, 0, '', '/']]]))
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run', return_value=missing):
            self.assertEqual(platform_support.systemd_service_observation(self.name), dict(status='absent'))

    def test_invalid_names_and_other_platforms_do_not_query_manager(self):
        with patch.object(platform_support.subprocess, 'run') as run:
            for name in ('--system', 'bad/name.service', 'thing.service\n', None):
                with self.assertRaises(ValueError):
                    platform_support.systemd_service_observation(name)
            with patch.object(platform_support, 'LINUX', False):
                self.assertEqual(platform_support.systemd_service_observation(self.name)['status'], 'unknown')
            run.assert_not_called()


class LaunchdObservationTests(unittest.TestCase):
    label = 'io.github.rwcii.koinon.memory.0123456789abcdef'

    def setUp(self):
        self.domain = f'gui/{os.geteuid()}'
        self.target = self.domain + '/' + self.label
        self.argv = ['/synthetic/python', '/synthetic/prefix/memory_service.py',
                     'argument with spaces', 'quote"literal', r'backslash\literal', 'Ünicode', ' trailing ']
        self.text = (self.target + ' = {\n\tpath = /synthetic/owned.plist\n'
                     '\tstate = running\n\tprogram = /synthetic/python\n\targuments = {\n' +
                     ''.join('\t\t' + arg + '\n' for arg in self.argv) +
                     '\t}\n\tpid = 42\n\tenvironment = {\n\t\tprogram = ignored\n\t}\n}\n')

    def result(self, text=None, code=0):
        return subprocess.CompletedProcess([], code, self.text if text is None else text, '')

    def observe(self, results):
        with patch.object(platform_support, 'DARWIN', True), \
                patch.object(platform_support.subprocess, 'run', side_effect=results):
            return platform_support.launchd_service_observation(self.domain, self.label)

    def test_absence_probe_discards_unrelated_domain_inventory(self):
        with patch.object(platform_support, 'DARWIN', True), \
                patch.object(platform_support.subprocess, 'run', side_effect=[
                    self.result('', 113), self.result('unrelated inventory' * 10000)]) as run:
            self.assertEqual(platform_support.launchd_service_observation(self.domain, self.label),
                             dict(status='absent'))
        probe = run.call_args_list[-1]
        self.assertEqual(probe.args[0], ['launchctl', 'print', self.domain])
        self.assertEqual(probe.kwargs['stdout'], subprocess.DEVNULL)
        self.assertEqual(probe.kwargs['stderr'], subprocess.DEVNULL)

    def test_preserves_exact_argument_boundaries_and_ignores_environment(self):
        observed = self.observe([self.result(), self.result()])
        self.assertEqual(observed['status'], 'observed')
        self.assertEqual(observed['argv'], self.argv)
        self.assertEqual(observed['artifact'], '/synthetic/owned.plist')
        self.assertEqual(observed['pid'], 42)

    def test_shape_changes_duplicates_and_incomplete_identity_stay_unknown(self):
        variants = [self.text.replace('\targuments = {', '\targuments: ['),
                    self.text.replace('\tpid = 42', '\tpid = 42\n\tpid = 99'),
                    self.text.replace('\tpath = /synthetic/owned.plist\n', ''),
                    self.text.replace('\t\targument with spaces', '\t\t\tambiguous'),
                    self.text.replace(self.target, 'gui/999/foreign'),
                    self.text.replace('\tpid = 42\n', '')]
        for variant in variants:
            with self.subTest(variant=variant):
                self.assertEqual(self.observe([self.result(variant)])['status'], 'unknown')

    def test_changed_pid_is_unknown(self):
        observed = self.observe([self.result(), self.result(self.text.replace('pid = 42', 'pid = 43'))])
        self.assertEqual(observed, dict(status='unknown', reason='observation_changed'))

    def test_missing_job_requires_a_live_selected_domain(self):
        self.assertEqual(self.observe([self.result('', 113), self.result('', 0)]), dict(status='absent'))
        self.assertEqual(self.observe([self.result('', 113), self.result('', 113)])['status'], 'unknown')
        self.assertEqual(self.observe([self.result('', 1)])['status'], 'unknown')

    def test_foreign_domains_and_invalid_labels_never_query_manager(self):
        with patch.object(platform_support.subprocess, 'run') as run:
            for domain, label in [('system', self.label), (self.domain, 'bad/name'),
                                  (self.domain, '--system'), ('gui/9999999', self.label)]:
                with self.subTest(domain=domain, label=label):
                    with self.assertRaises(ValueError):
                        platform_support.launchd_service_observation(domain, label)
            run.assert_not_called()



class NativeMemoryActionTests(unittest.TestCase):
    def test_actions_use_only_selected_user_backend_and_exact_artifact(self):
        from test_memory_service_config import record
        selected = record()
        with patch.object(platform_support, 'LINUX', True), \
                patch.object(platform_support.subprocess, 'run') as run:
            platform_support.memory_manager_action(selected, 'activate')
        self.assertEqual(run.call_args.args[0], ['systemctl', '--user', '--no-ask-password',
                                               'enable', '--now', selected['artifact']])
        selected = dict(record(backend='launchd'), manager_domain=f'gui/{os.geteuid()}')
        with patch.object(platform_support, 'DARWIN', True), \
                patch.object(platform_support.subprocess, 'run') as run:
            platform_support.memory_manager_action(selected, 'activate')
            self.assertEqual(run.call_args.args[0], ['launchctl', 'bootstrap', selected['manager_domain'],
                                                   selected['artifact']])
            platform_support.memory_manager_action(selected, 'restart')
            self.assertEqual(run.call_args.args[0], ['launchctl', 'kickstart',
                selected['manager_domain'] + '/' + selected['artifact'].rsplit('/', 1)[1][:-6]])
            self.assertNotIn('-k', run.call_args.args[0])

    def test_missing_or_foreign_launchd_domain_never_invokes_manager(self):
        from test_memory_service_config import record
        with patch.object(platform_support.subprocess, 'run') as run:
            for domain in (None, 'system', f'gui/{os.geteuid() + 1}'):
                selected = record(backend='launchd')
                if domain is not None:
                    selected['manager_domain'] = domain
                with self.assertRaises(ValueError):
                    platform_support.memory_manager_action(selected, 'activate')
            run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
