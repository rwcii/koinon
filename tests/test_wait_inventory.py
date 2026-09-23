import ast
from collections import Counter
from pathlib import Path
import re
import unittest

import wait_inventory

TESTS = Path(__file__).resolve().parent
# The deadline patterns the guard recognizes. Polling loops and subprocess waits are listed in
# the inventory but are not recognized here; the inventory review establishes those.
PATTERN = re.compile(r'asyncio\.timeout\(|asyncio\.wait_for\(|(?:monotonic|time)\(\)\s*\+')
STATUSES = {'pending-02', 'pending-03', 'product-deadline'}


def owners(source):
    """Map each line number to the qualified name of the innermost function holding it."""
    names = {}

    def visit(node, prefix):
        for child in ast.iter_child_nodes(node):
            if isinstance(child, ast.ClassDef):
                visit(child, prefix + child.name + '.')
            elif isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
                name = prefix + child.name
                for line in range(child.lineno, child.end_lineno + 1):
                    names[line] = name
                visit(child, name + '.')
            else:
                visit(child, prefix)

    visit(ast.parse(source), '')
    return names


def source_lines():
    """Every stripped source line of the test files, keyed by (file, function)."""
    found = Counter()
    for path in sorted(TESTS.glob('test_*.py')):
        source = path.read_text()
        names = owners(source)
        for number, line in enumerate(source.splitlines(), 1):
            found[(path.name, names.get(number, '<module>'), line.strip())] += 1
    return found


class WaitInventoryTests(unittest.TestCase):
    def test_entries_are_well_formed(self):
        for entry in wait_inventory.ENTRIES:
            with self.subTest(entry=entry):
                file, function, line, status, reason = entry
                self.assertIn(status, STATUSES)
                if status == 'product-deadline':
                    self.assertTrue(reason, 'a product-deadline entry states its reason')

    def test_every_entry_still_matches_its_test(self):
        found = source_lines()
        listed = Counter((file, function, line) for file, function, line, _, _ in wait_inventory.ENTRIES)
        for key, count in listed.items():
            with self.subTest(entry=key):
                self.assertGreaterEqual(found[key], count, 'the listed wait is no longer in its test')

    def test_known_deadline_patterns_occur_only_as_inventory_entries(self):
        listed = Counter((file, function, line) for file, function, line, _, _ in wait_inventory.ENTRIES)
        for key, count in source_lines().items():
            if PATTERN.search(key[2]):
                with self.subTest(wait=key):
                    self.assertLessEqual(count, listed[key],
                                         'use tests/waiting.py, or list a product deadline with its reason')


if __name__ == '__main__':
    unittest.main()
