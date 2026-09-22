from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from koinon import session_service_config
from koinon import upgrade_bundle
from koinon import upgrade_manifest as manifest
from koinon import upgrade_plan as plans


class PlanTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.prefix, self.source = self.root / 'prefix', self.root / 'source'
        for directory in (self.prefix, self.source, self.prefix / '.upgrade'):
            directory.mkdir(mode=0o700)
        self.operation = self.prefix / '.upgrade' / ('a' * 32)
        self.operation.mkdir(mode=0o700)
        for directory, text in ((self.prefix, 'print("old")\n'), (self.source, 'print("new")\n')):
            path = directory / 'entry.py'
            path.write_text(text)
            path.chmod(0o600)
        self.source_manifest = manifest.capture(self.source, ['entry.py'])
        self.runtime_manifest = manifest.capture(self.prefix, ['entry.py'])
        self.config = dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'),
                           codex=sys.executable, preserved={'opaque': [1, 2]})
        self.bundle = upgrade_bundle.prepare(self.operation, self.source_manifest, 'entry.py')

    def prepare(self, **changes):
        args = dict(source=self.source_manifest, runtime=self.runtime_manifest,
                    installation=self.config, components=[], recovery=self.bundle)
        args.update(changes)
        return plans.prepare(self.operation, self.prefix, **args)

    def test_preparation_and_resume_preserve_data_and_pending_intent(self):
        prepared = self.prepare()
        loaded = plans.load(self.operation, prepared['sha256'])
        self.assertEqual(loaded['documents']['installation'], self.config)
        self.assertEqual(loaded['phase']['step'], 0)
        self.assertEqual(loaded['phase']['receipts'], [])
        self.assertEqual(self.prepare(), prepared)
        self.assertFalse((self.prefix / 'install.json').exists())

    def test_resume_validates_old_manifest_without_requiring_unreplaced_runtime(self):
        prepared = self.prepare()
        (self.prefix / 'entry.py').write_text('partial replacement\n')
        loaded = plans.load(self.operation, prepared['sha256'])
        self.assertEqual(loaded['documents']['runtime'], self.runtime_manifest)
        self.assertEqual((self.prefix / 'entry.py').read_text(), 'partial replacement\n')

    def test_resume_refuses_missing_document_without_recreating_it(self):
        prepared = self.prepare()
        source = self.operation / 'source.json'
        source.unlink()
        with self.assertRaises(ValueError):
            plans.load(self.operation, prepared['sha256'])
        self.assertFalse(source.exists())
        with self.assertRaises(ValueError):
            plans.load(self.operation, 'b' * 64)

    def test_changed_source_refuses_resume(self):
        prepared = self.prepare()
        (self.source / 'entry.py').write_text('changed release\n')
        with self.assertRaises(manifest.ManifestError):
            plans.load(self.operation, prepared['sha256'])

    def test_prefix_alias_cannot_retarget_resume(self):
        alias = self.root / 'selected-prefix'
        alias.symlink_to(self.prefix, target_is_directory=True)
        prepared = plans.prepare(self.operation, alias, source=self.source_manifest,
                                 runtime=self.runtime_manifest, installation=self.config,
                                 components=[], recovery=self.bundle)
        other = self.root / 'other-prefix'
        other.mkdir(mode=0o700)
        alias.unlink()
        alias.symlink_to(other, target_is_directory=True)
        with self.assertRaises(plans.PlanError):
            plans.load(self.operation, prepared['sha256'])
        self.assertEqual(list(other.iterdir()), [])

    def test_capacity_reservation_precedes_plan_document_publication(self):
        with patch.object(plans, 'MAX_DIRECTORY_ENTRIES', 4):
            with self.assertRaises(plans.PlanError):
                self.prepare()
        self.assertFalse((self.operation / 'plan.json').exists())
        self.assertFalse((self.operation / 'source.json').exists())
        self.assertFalse((self.operation / 'phase.json').exists())

    def test_duplicate_or_contradictory_component_observations_refuse(self):
        registration = dict(thread='synthetic', name='synthetic-peer', repo=str(self.root))
        key, _, _ = session_service_config.registration_identity(registration)
        home = Path(self.config['state_root']) / 'sessions' / key
        selection = session_service_config.selection(self.prefix, sys.executable, home,
                                                     self.config, registration, 'systemd')
        component = dict(kind='session', selection=selection, manager=dict(status='absent'),
                         registered=False, running=False, owner=None)
        for values in ([component, component], [dict(component, running=True)]):
            with self.assertRaises(plans.PlanError):
                self.prepare(components=values)
        prepared = self.prepare(components=[component])
        self.assertEqual(plans.load(self.operation, prepared['sha256'])['plan']['component_count'], 1)

    def test_operation_outside_installed_prefix_refuses(self):
        elsewhere = self.root / 'elsewhere'
        elsewhere.mkdir(mode=0o700)
        with self.assertRaises(plans.PlanError):
            plans.prepare(elsewhere, self.prefix, source=self.source_manifest,
                          runtime=self.runtime_manifest, installation=self.config,
                          components=[], recovery=self.bundle)
        self.assertEqual(list(elsewhere.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
