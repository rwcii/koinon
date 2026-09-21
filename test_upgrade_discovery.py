"""External definitions are reported without adopting, executing or deleting them."""
import hashlib
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import patch

import platform_support
import upgrade_discovery as discovery


class DiscoveryTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.prefix = self.root / 'runtime space $literal %n'
        self.units = self.root / 'outside-selected-state'
        self.units.mkdir()
        self.sources = dict(directories=[str(self.units)], loaded_artifacts=[],
                            loaded_discovery='synthetic_native_inventory')
        self.patch = patch.object(platform_support, 'upgrade_service_sources', return_value=self.sources)
        processes = patch.object(platform_support, 'upgrade_memory_processes', return_value=[])
        self.processes = processes.start()
        self.addCleanup(processes.stop)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    def write(self, path, data):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return path

    def unit(self, path):
        return self.write(path, ('[Service]\nExecStart=/usr/bin/python3 "' + str(self.prefix)
            + '/memory.py" --state-dir /synthetic/external-state serve\n').encode())

    def test_dormant_hand_written_unit_outside_state_is_reported_not_mutated(self):
        path = self.unit(self.units / 'repository-name.service')
        before = path.read_bytes()
        report = discovery.inventory(self.prefix, {}, [])
        self.assertEqual(report['action'], 'refused')
        self.assertEqual(report['findings'][0]['path'], str(path))
        self.assertEqual(report['findings'][0]['ownership'], 'unowned')
        self.assertEqual(path.read_bytes(), before)
        self.assertNotIn('ExecStart', str(report))

    def test_selected_legacy_unit_directory_is_included(self):
        elsewhere = self.root / 'explicit-units'
        path = self.unit(elsewhere / 'custom.service')
        report = discovery.inventory(self.prefix, {'unit_dir': str(elsewhere)}, [])
        self.assertIn(str(path), [item['path'] for item in report['findings']])

    def test_loaded_custom_artifact_and_loader_symlink_are_included(self):
        path = self.unit(self.root / 'custom-loaded' / 'repository.service')
        self.sources['loaded_artifacts'] = [str(path)]
        loader = self.units / 'default.target.wants' / path.name
        loader.parent.mkdir()
        loader.symlink_to(path)
        report = discovery.inventory(self.prefix, {}, [])
        self.assertEqual({item['path'] for item in report['findings']}, {str(path), str(loader)})

    def test_owned_artifact_requires_both_selected_target_and_digest(self):
        path = self.unit(self.units / 'owned.service')
        record = {'artifact': str(path), 'artifact_digest': hashlib.sha256(path.read_bytes()).hexdigest()}
        report = discovery.inventory(self.prefix, {}, [{'selection': record}])
        self.assertEqual(report['action'], 'no_unowned_definition_or_process_found')
        duplicate = self.unit(self.units / 'copied.service')
        report = discovery.inventory(self.prefix, {}, [{'selection': record}])
        self.assertEqual(report['findings'][0]['path'], str(duplicate))
        self.assertEqual(report['findings'][0]['ownership'], 'unowned')
        path.write_bytes(path.read_bytes() + b'# changed\n')
        self.assertTrue(all(item['ownership'] == 'unowned'
            for item in discovery.inventory(self.prefix, {}, [{'selection': record}])['findings']))

    def test_binary_and_xml_plists_use_decoded_program_arguments(self):
        for format in (plistlib.FMT_BINARY, plistlib.FMT_XML):
            with self.subTest(format=format):
                path = self.write(self.units / 'custom.plist', plistlib.dumps(
                    {'ProgramArguments': ['/usr/bin/python3', str(self.prefix / 'memory.py'), 'serve']}, fmt=format))
                self.assertEqual(discovery.inventory(self.prefix, {}, [])['action'], 'refused')
                path.unlink()

    def test_unrelated_runtime_does_not_become_a_selected_service(self):
        self.write(self.units / 'other.service', b'[Service]\nExecStart=/other/runtime/memory.py serve\n')
        self.assertEqual(discovery.inventory(self.prefix, {}, [])['findings'], [])

    def test_changed_file_and_oversize_inventory_refuse(self):
        path = self.unit(self.units / 'changing.service')
        original = discovery._references
        def changed(*args):
            path.write_bytes(path.read_bytes() + b'# racing writer\n')
            return original(*args)
        with patch.object(discovery, '_references', side_effect=changed):
            with self.assertRaisesRegex(discovery.DiscoveryError, 'changed'):
                discovery.inventory(self.prefix, {}, [])
        with patch.object(discovery, 'MAX_FILE_BYTES', 1):
            with self.assertRaises(discovery.DiscoveryError):
                discovery.inventory(self.prefix, {}, [])

    def test_memory_process_requires_exact_saved_pid_and_start_marker(self):
        process = dict(pid=123, proc_start='synthetic-start', kind='memory')
        self.processes.return_value = [process]
        self.assertEqual(discovery.inventory(self.prefix, {}, [])['action'], 'refused')
        component = dict(kind='memory', selection={}, owner=dict(
            pid=122, proc_start='parent-start', child=process))
        self.assertEqual(discovery.inventory(self.prefix, {}, [component])['processes'][0]['ownership'],
                         'saved_selection')
        component['owner']['child'] = dict(process, proc_start='reused-pid-start')
        self.assertEqual(discovery.inventory(self.prefix, {}, [component])['action'], 'refused')

    def test_manager_failure_is_not_reported_as_empty(self):
        with patch.object(platform_support, 'upgrade_service_sources', side_effect=ValueError('unavailable')):
            with self.assertRaisesRegex(ValueError, 'unavailable'):
                discovery.inventory(self.prefix, {}, [])


class NativeDiscoveryShapeTests(unittest.TestCase):
    def test_systemd_tombstone_is_distinct_from_unobserved_loaded_command(self):
        tombstone = 'Id=synthetic.service\nFragmentPath=\nLoadState=not-found\n'
        self.assertEqual(platform_support._upgrade_systemd_definitions(tombstone, '/synthetic/runtime'), ([], []))
        with self.assertRaises(ValueError):
            platform_support._upgrade_systemd_definitions(tombstone.replace('not-found', 'loaded'), '/synthetic/runtime')
        direct = 'Id=synthetic.service\nFragmentPath=\nLoadState=loaded\nExecStart=/synthetic/runtime/memory.py serve\n'
        paths, references = platform_support._upgrade_systemd_definitions(direct, '/synthetic/runtime')
        self.assertEqual(paths, [])
        self.assertEqual(references[0]['identity'], 'synthetic.service')
        self.assertIsNone(references[0]['artifact'])

    def test_launchd_domain_table_is_bounded_and_explicit(self):
        value = 'gui/501 = {\n\tservices = {\n\t\t0 M synthetic.one\n\t\t123 0 synthetic.two\n\t}\n}\n'
        self.assertEqual(platform_support._upgrade_launchd_labels(value, 'gui/501'), ['synthetic.one', 'synthetic.two'])
        for changed in (value.replace('gui/501', 'user/501'), value.replace('services =', 'jobs ='),
                        value.replace('123 0 synthetic.two', 'unknown syntax'),
                        value.replace('synthetic.two', 'synthetic.one')):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                platform_support._upgrade_launchd_labels(changed, 'gui/501')

    def test_unrelated_label_text_is_preserved_rather_than_refusing_every_upgrade(self):
        """One third-party job must not make the whole host unupgradable."""
        value = ('gui/501 = {\n\tservices = {\n\t\t0 M vendor job 3\n'
                 '\t\t123 0 synthetic.two\n\t}\n}\n')
        self.assertEqual(platform_support._upgrade_launchd_labels(value, 'gui/501'),
                         ['synthetic.two', 'vendor job 3'])
        for refused in ('vendor\x01job', 'vendor\x7fjob'):
            with self.subTest(refused=refused), self.assertRaises(ValueError):
                platform_support._upgrade_launchd_labels(
                    value.replace('vendor job 3', refused), 'gui/501')

    def test_malformed_property_list_refuses_and_names_the_file(self):
        """ExpatError is not a ValueError, so it escaped the handler and crashed."""
        malformed = b'<?xml version="1.0 broken"?><plist><dict/></plist>'
        with self.assertRaises(discovery.DiscoveryError) as refused:
            discovery._references(malformed, '.plist', '/synthetic/runtime',
                                  Path('/synthetic/agents/vendor.plist'))
        self.assertIn('vendor.plist', str(refused.exception))

    def test_escaped_percent_is_not_expanded_into_a_runtime_reference(self):
        """systemd renders %%h as the literal text %h and never expands it."""
        prefix = str(platform_support.account_home())
        self.assertFalse(discovery._references(b'ExecStart=/bin/echo %%h/elsewhere\n',
                                               '.service', prefix))
        self.assertTrue(discovery._references(b'ExecStart=/bin/echo %h/elsewhere\n',
                                              '.service', prefix))
