"""Declared layout migrations, and the shipped declaration's own consistency."""
import ast
import importlib.util
import subprocess
import unittest
from unittest import mock

from koinon import upgrade_layout
from scripts.install import FILES
from repo_root import ROOT

_spec = importlib.util.spec_from_file_location(
    'native_upgrade_fixture', ROOT / 'scripts/test-native-upgrade.py')
native = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(native)


class DeclarationTests(unittest.TestCase):
    def test_every_declared_destination_ships_and_no_declared_source_still_ships(self):
        """The invariant that catches a move nobody declared.

        A release that moves a shipped file must add it here. If the declaration and
        the manifest disagree, an installed runtime either loses a path with nowhere
        to go, or keeps a path the release believes it retired.
        """
        shipped = set(FILES)
        for retired, destination in upgrade_layout.MOVES.items():
            with self.subTest(retired=retired):
                self.assertNotIn(retired, shipped)
                self.assertIn(destination, shipped)

    def test_undeclared_removal_refuses_and_names_the_path(self):
        with self.assertRaises(upgrade_layout.LayoutError) as refusal:
            upgrade_layout.declared({'vanished.py'}, {'koinon/vanished.py': {}})
        self.assertIn('undeclared runtime file removal: vanished.py', str(refusal.exception))

    def test_declared_destination_must_be_published_by_this_release(self):
        with mock.patch.dict(upgrade_layout.MOVES, {'old.py': 'koinon/old.py'}, clear=True):
            with self.assertRaises(upgrade_layout.LayoutError) as refusal:
                upgrade_layout.declared({'old.py'}, {'entry.py': {}})
        self.assertIn('no published destination: old.py', str(refusal.exception))

    def test_declared_moves_are_paired_in_a_stable_order(self):
        moves = {'b.py': 'koinon/b.py', 'a.py': 'koinon/a.py'}
        with mock.patch.dict(upgrade_layout.MOVES, moves, clear=True):
            self.assertEqual(upgrade_layout.declared(set(moves), {v: {} for v in moves.values()}),
                             (('a.py', 'koinon/a.py'), ('b.py', 'koinon/b.py')))

    def test_nothing_removed_declares_nothing(self):
        self.assertEqual(upgrade_layout.declared(set(), {'entry.py': {}}), ())

    def test_a_noncanonical_declared_path_refuses(self):
        for moves in ({'../escape.py': 'koinon/escape.py'}, {'ok.py': '/absolute.py'}):
            with self.subTest(moves=moves):
                with mock.patch.dict(upgrade_layout.MOVES, moves, clear=True):
                    with self.assertRaises(ValueError):
                        upgrade_layout.declared(set(moves), {v: {} for v in moves.values()})


if __name__ == '__main__':
    unittest.main()


class PinnedReleaseTests(unittest.TestCase):
    """Check the declaration against the release it claims to migrate from.

    Comparing the declaration only with this release's manifest cannot detect an
    entry somebody deleted: the entries that remain still agree. Only the previous
    release's own file list says what actually has to be accounted for.
    """
    def setUp(self):
        self.previous = self.released_files(native.PREVIOUS_RELEASE)

    def released_files(self, ref):
        shown = subprocess.run(['git', '-C', str(ROOT), 'show', f'{ref}:scripts/install.py'],
                               capture_output=True, text=True)
        if shown.returncode:
            self.fail(f'pinned release {ref} is unavailable, so the declaration cannot be '
                      f'checked against what it migrates from. Fetch full history: {shown.stderr}')
        for node in ast.walk(ast.parse(shown.stdout)):
            if (isinstance(node, ast.Assign) and len(node.targets) == 1
                    and getattr(node.targets[0], 'id', None) == 'FILES'):
                return set(ast.literal_eval(node.value))
        self.fail(f'the pinned release {ref} has no FILES manifest')

    def test_every_path_the_previous_release_shipped_is_still_accounted_for(self):
        unaccounted = self.previous - set(FILES) - set(upgrade_layout.MOVES)
        self.assertEqual(unaccounted, set(),
                         'these paths the previous release shipped are neither published nor '
                         'declared as moved, so an upgrade from it would refuse')

    def test_no_declaration_names_a_path_the_previous_release_did_not_ship(self):
        stale = set(upgrade_layout.MOVES) - self.previous
        self.assertEqual(stale, set(), 'these declared moves name paths no pinned release shipped')
