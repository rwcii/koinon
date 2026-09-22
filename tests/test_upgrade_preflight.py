"""Synthetic preflight refuses ambiguous runtime and unowned stopped state."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from koinon import upgrade_preflight as preflight
from koinon import upgrade_manifest as manifest


class PreflightTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.old, self.new, self.state = [self.root / name for name in ('old', 'new', 'state')]
        for root in (self.old, self.new, self.state):
            root.mkdir(mode=0o700)
        for root in (self.old, self.new):
            (root / 'scripts').mkdir(mode=0o700)
            (root / 'entry.py').write_text('print("synthetic")\n')
            (root / 'scripts/install.py').write_text(
                'raise RuntimeError("must never execute")\n'
                'FILES = ("scripts/install.py", "entry.py")\n')
            (root / "entry.py").chmod(0o600)
            (root / "scripts/install.py").chmod(0o600)
        self.config = dict(state_root=str(self.state))

    def test_literal_manifest_does_not_execute_installer(self):
        pair = preflight.runtime_pair(self.old, self.new)
        self.assertEqual(set(pair['source']['files']), {'scripts/install.py', 'entry.py'})
        self.assertEqual(pair['runtime']['root'], str(self.old))

    def test_dynamic_duplicate_and_self_omitting_file_lists_refuse(self):
        for code in ('FILES = tuple(["entry.py"])',
                     'FILES = ("entry.py",)\nFILES = ("scripts/install.py",)',
                     'FILES = ("entry.py",)',
                     'FILES = ("scripts/install.py", "../escape")'):
            with self.subTest(code=code):
                (self.new / 'scripts/install.py').write_text(code)
                with self.assertRaises(ValueError):
                    preflight.runtime_pair(self.old, self.new)

    def test_overlap_and_removal_refuse(self):
        with self.assertRaisesRegex(ValueError, 'disjoint'):
            preflight.runtime_pair(self.old, self.old)
        (self.new / 'scripts/install.py').write_text('FILES = ("scripts/install.py",)')
        with self.assertRaises(ValueError):
            preflight.runtime_pair(self.old, self.new)

    def test_changed_list_between_parse_and_capture_refuses(self):
        capture = manifest.capture
        def changed(root, names):
            (root / 'scripts/install.py').write_text('FILES = ("scripts/install.py",)')
            return capture(root, names)
        with patch.object(manifest, 'capture', side_effect=changed):
            with self.assertRaisesRegex(ValueError, 'changed'):
                preflight.runtime_manifest(self.new)

    def test_missing_recorded_roots_refuse_before_service_observation(self):
        missing = self.root / 'missing-state'
        for config in (
                dict(state_root=str(missing)),
                dict(state_root=str(self.state), memory_services=dict(repositories={
                    'b' * 16: dict(state_root=str(missing))}))):
            with self.subTest(config=config):
                installed = type('Installed', (), dict(config=config))()
                with patch('koinon.upgrade_observation.installation_locked') as observation:
                    with self.assertRaises(preflight.MissingStateRootError) as error:
                        preflight.observe_locked(self.old, installed)
                    observation.assert_not_called()
                self.assertEqual(error.exception.path, str(missing))
                self.assertFalse(missing.exists())

    def test_no_memory_directory_is_reported_without_creation(self):
        report = preflight.memory_ownership(self.config)
        self.assertEqual(report['action'], 'no_unowned_state_found')
        self.assertFalse((self.state / 'memory').exists())

    def test_dormant_unowned_state_is_reported_and_refused_before_observation(self):
        home = self.state / 'memory' / ('a' * 16)
        home.mkdir(parents=True, mode=0o700)
        home.parent.chmod(0o700)
        sentinel = home / 'memory.sqlite3'
        sentinel.write_bytes(b'not opened or modified')
        installed = type('Installed', (), dict(config=self.config))()
        with patch('koinon.upgrade_observation.installation_locked') as observation:
            with self.assertRaises(preflight.UnownedMemoryError) as error:
                preflight.observe_locked(self.old, installed)
            observation.assert_not_called()
        self.assertEqual(error.exception.report['unowned'][0]['service_directory'], str(home))
        self.assertEqual(sentinel.read_bytes(), b'not opened or modified')

    def test_saved_roots_are_included_and_exact_owned_state_is_not_external(self):
        other = self.root / 'other-state'
        home = other / 'memory' / ('b' * 16)
        home.mkdir(parents=True, mode=0o700)
        other.chmod(0o700)
        home.parent.chmod(0o700)
        self.config['memory_services'] = dict(repositories={
            'b' * 16: dict(state_root=str(other), service_directory=str(home))})
        report = preflight.memory_ownership(self.config)
        self.assertEqual(report['unowned'], [])
        self.assertEqual(len(report['roots']), 2)

    def test_symlinked_or_unknown_inventory_entry_refuses(self):
        directory = self.state / 'memory'
        directory.mkdir(mode=0o700)
        path = directory / ('c' * 16)
        path.symlink_to(self.new, target_is_directory=True)
        with self.assertRaises(ValueError):
            preflight.memory_ownership(self.config)
        path.unlink()
        (directory / 'unknown').mkdir(mode=0o700)
        with self.assertRaises(ValueError):
            preflight.memory_ownership(self.config)


class OnlineDatabasePreflightTests(unittest.TestCase):
    def test_live_memory_snapshot_includes_wal_and_leaves_original_schema_unchanged(self):
        import memory
        import sqlite3
        import test_work_foundation as released
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            home, workspace = root / 'state', root / 'workspace'
            home.mkdir(mode=0o700)
            workspace.mkdir(mode=0o700)
            path = home / 'memory.sqlite3'
            db = sqlite3.connect(path, isolation_level=None)
            path.chmod(0o600)
            try:
                db.execute('PRAGMA auto_vacuum=INCREMENTAL')
                db.execute('PRAGMA journal_mode=WAL')
                released.legacy(db, 4)
                db.execute("INSERT INTO cursors VALUES ('synthetic-reader', 0, 0, NULL, 1, 0, 1.0)")
                result = preflight.database_check(home, 'memory.sqlite3', workspace,
                                                 kind='memory', repo=released.REPO)
                self.assertEqual((result['source_schema'], result['target_schema']), (4, 5))
                self.assertEqual(dict(db.execute('SELECT key,value FROM meta'))['schema'], '4')
                self.assertEqual(db.execute('SELECT consumer FROM cursors').fetchone()[0], 'synthetic-reader')
                self.assertEqual(list(workspace.iterdir()), [])
                with patch.object(memory, 'ORDINARY_MAX_PAGES', 1):
                    with self.assertRaisesRegex(preflight.PreflightError, 'near-full'):
                        preflight.database_check(home, 'memory.sqlite3', workspace,
                                                 kind='memory', repo=released.REPO)
            finally:
                db.close()

    def test_current_inbox_preflight_does_not_create_selected_absent_database(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            home, workspace = root / 'state', root / 'workspace'
            home.mkdir(mode=0o700)
            workspace.mkdir(mode=0o700)
            with self.assertRaisesRegex(preflight.PreflightError, 'absent'):
                preflight.database_check(home, 'inbox.sqlite3', workspace, kind='session')
            self.assertEqual(list(home.iterdir()), [])
