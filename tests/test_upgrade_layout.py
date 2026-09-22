"""Declared layout migrations, and the shipped declaration's own consistency."""
import unittest
from unittest import mock

from koinon import upgrade_layout
from scripts.install import FILES


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
