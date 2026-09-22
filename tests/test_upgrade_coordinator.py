"""Interrupted synthetic shutdown/backup/replacement through the phase driver."""
import unittest
from unittest.mock import patch

import test_upgrade_gate as fixtures
from koinon import upgrade_coordinator
from koinon.upgrade_documents import Documents
from koinon import upgrade_exclusion


class CoordinatorTests(unittest.TestCase):
    def setUp(self):
        fixtures.GateTests.setUp(self)
        fixtures.GateTests.advance(self, 2)
        for name in ('runtime-backup', 'component-backup-000'):
            (self.operation / name).mkdir(mode=0o700)

    def owner(self):
        return upgrade_exclusion.operation(self.operation, self.prepared['sha256'])

    def test_prepared_pipeline_stops_at_durable_replacement_with_gate_closed(self):
        old = (self.prefix / 'entry.py').read_bytes()
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            phase = upgrade_coordinator.through_replacement(owner)
            self.assertEqual(phase['step'], 9)
            self.assertEqual((self.prefix / 'entry.py').read_bytes(), (self.source / 'entry.py').read_bytes())
            self.assertEqual((self.operation / 'runtime-backup' / 'entry.py').read_bytes(), old)
            self.assertEqual(upgrade_coordinator.through_replacement(owner), phase)
            self.assertIsNotNone(upgrade_exclusion.read(self.prefix))

    def test_missing_prepared_destination_refuses_before_shutdown(self):
        (self.operation / 'runtime-backup').rmdir()
        with self.owner() as owner, patch('koinon.upgrade_quiescence.stop_phase') as stop:
            with self.assertRaises((ValueError, OSError)):
                upgrade_coordinator.through_replacement(owner)
            stop.assert_not_called()
            self.assertEqual(owner.journal.read()['step'], 2)

    def test_lost_backup_completion_resumes_retained_capture_before_replacing(self):
        put = Documents.put
        def interrupt(documents, name, value):
            digest = put(documents, name, value)
            if name == 'backups':
                raise OSError('synthetic lost backup completion')
            return digest
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            old = (self.prefix / 'entry.py').read_bytes()
            with patch.object(Documents, 'put', new=interrupt):
                with self.assertRaises(OSError):
                    upgrade_coordinator.through_replacement(owner)
            self.assertEqual(owner.journal.read()['step'], 6)
            self.assertEqual((self.prefix / 'entry.py').read_bytes(), old)
            self.assertEqual(upgrade_coordinator.through_replacement(owner)['step'], 9)

    def test_completed_replacement_does_not_hide_a_substituted_runtime(self):
        with self.owner() as owner, patch('koinon.platform_support.session_manager_observation', return_value=dict(status='absent')):
            upgrade_coordinator.through_replacement(owner)
            (self.prefix / 'entry.py').write_text('unexpected operator edit')
            with self.assertRaises(ValueError):
                upgrade_coordinator.through_replacement(owner)
            self.assertEqual((self.prefix / 'entry.py').read_text(), 'unexpected operator edit')


class NestedRuntimePipelineTests(unittest.TestCase):
    def test_literal_manifest_drives_nested_replacement_and_interrupted_resume(self):
        import json
        from pathlib import Path
        import sys
        import tempfile
        from koinon import upgrade_bundle
        from koinon import upgrade_plan
        from koinon import upgrade_preflight
        from koinon import upgrade_replace
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            prefix, source = root / 'prefix', root / 'source'
            names = ('entry.py', 'pkg/worker.py', 'pkg/nested/data.txt', 'scripts/install.py')
            for directory, label in ((prefix, 'old'), (source, 'new')):
                directory.mkdir(mode=0o700)
                for name in names:
                    path = directory / name
                    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                    text = ('FILES = ' + repr(names) if name == 'scripts/install.py'
                            else 'print(' + repr(label) + ')\n')
                    path.write_text(text)
                    path.chmod(0o600)
                for path in directory.rglob('*'):
                    if path.is_dir():
                        path.chmod(0o700)
            pair = upgrade_preflight.runtime_pair(prefix, source)
            parent = prefix / '.upgrade'
            parent.mkdir(mode=0o700)
            operation = parent / ('a' * 32)
            operation.mkdir(mode=0o700)
            (operation / 'runtime-backup').mkdir(mode=0o700)
            state = root / 'state'
            state.mkdir(mode=0o700)
            config = dict(state_root=str(state), unit_dir=str(root / 'units'), codex=sys.executable)
            (prefix / 'install.json').write_text(json.dumps(config))
            (prefix / 'install.json').chmod(0o600)
            bundle = upgrade_bundle.prepare(operation, pair['source'], 'entry.py')
            prepared = upgrade_plan.prepare(operation, prefix, **pair,
                installation=config, components=[], recovery=bundle)
            with upgrade_exclusion.operation(operation, prepared['sha256']) as owner:
                owner.activate()
                phase = owner.journal.read()
                digest = Documents(operation).put('prepared-checks', dict(version=1))
                owner.journal.advance(phase, evidence=digest)
                owner.journal.advance(owner.journal.read())
                publish = upgrade_replace.durable_state.publish
                def interrupt(path, value, **kwargs):
                    if Path(path).name == 'replacement-progress.json' and value['index'] == 2:
                        raise OSError('synthetic lost nested completion')
                    return publish(path, value, **kwargs)
                with patch.object(upgrade_replace.durable_state, 'publish', side_effect=interrupt):
                    with self.assertRaises(OSError):
                        upgrade_coordinator.through_replacement(owner)
                self.assertEqual(owner.journal.read()['step'], 8)
            # Resume under a fresh operation owner and retained recovery evidence.
            with upgrade_exclusion.operation(operation, prepared['sha256']) as owner:
                phase = upgrade_coordinator.through_replacement(owner)
                self.assertEqual(phase['step'], 9)
                for name in names:
                    self.assertEqual((prefix / name).read_bytes(), (source / name).read_bytes())
                self.assertIn('old', (operation / 'runtime-backup/pkg/worker.py').read_text())
                self.assertEqual(upgrade_coordinator.through_replacement(owner), phase)
