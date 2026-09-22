import importlib.util
import re
import unittest
from repo_root import ROOT

spec = importlib.util.spec_from_file_location('installer', ROOT/'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

WORKFLOWS = ROOT/'.github/workflows'


def exempt_prefixes():
    """The agent-process prefixes that the required test jobs treat as exempt."""
    text = (WORKFLOWS/'tests.yml').read_text()
    match = re.search(r"grep -vE '\^\(([^)]*)\)'", text)
    return sorted(part.replace('\\.', '.') for part in match.group(1).split('|'))


class CiScopeTests(unittest.TestCase):
    def test_native_workflows_ignore_the_same_paths(self):
        expected = exempt_prefixes()
        native = sorted(WORKFLOWS.glob('native-*.yml'))
        self.assertTrue(native)
        for workflow in native:
            text = workflow.read_text()
            blocks = re.findall(r"paths-ignore:\n((?:\s+- '[^']+'\n)+)", text)
            self.assertEqual(len(blocks), 2, workflow.name)
            for block in blocks:
                paths = sorted(p + '/' for p in re.findall(r"- '([^']+)/\*\*'", block))
                self.assertEqual(paths, expected, workflow.name)

    def test_exempt_paths_are_never_installed(self):
        prefixes = tuple(exempt_prefixes())
        self.assertIn('agents/', prefixes)
        installed = [path for path in installer.FILES if path.startswith(prefixes)]
        self.assertEqual(installed, [])


if __name__ == '__main__':
    unittest.main()
