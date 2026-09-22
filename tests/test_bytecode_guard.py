"""The entrypoint bytecode guard: identical text, private caches, untrusted caches unread."""
import ast
import importlib._bootstrap_external as external
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINTS = ('bridge.py', 'notify.py', 'session.py', 'memory.py', 'memory_service.py',
               'session_service.py', 'usage_report.py', 'scripts/install.py',
               'scripts/uninstall.py', 'scripts/upgrade.py')
START = '# Bytecode guard (docs/LEGACY-ADOPTION-DESIGN.md, G1-G3).'
END = '# End of bytecode guard.\n'


def guard(path):
    text = (ROOT / path).read_text()
    return text[text.index(START):text.index(END) + len(END)]


class GuardTextTests(unittest.TestCase):
    def test_every_entrypoint_carries_the_same_guard_before_any_import(self):
        canonical = guard('bridge.py')
        for path in ENTRYPOINTS:
            with self.subTest(path=path):
                self.assertEqual(guard(path), canonical)
                tree = ast.parse((ROOT / path).read_text())
                first = next(node for node in tree.body
                             if isinstance(node, (ast.Import, ast.ImportFrom, ast.If)))
                self.assertIsInstance(first, ast.If, 'the guard must precede every import')
                self.assertIn("__name__ == '__main__'", ast.unparse(first.test))


class GuardBehaviourTests(unittest.TestCase):
    """Run real entrypoints from a private copy of the runtime, as an installed prefix."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix='koinon-guard-'))
        self.addCleanup(shutil.rmtree, self.root)
        self.root.chmod(0o700)
        self.prefix = self.root / 'prefix'
        sys.path.insert(0, str(ROOT))
        from scripts import install
        for name in install.FILES:
            target = self.prefix / name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copyfile(ROOT / name, target)
            target.chmod(0o600)
        for directory in [self.prefix, *self.prefix.rglob('*')]:
            if directory.is_dir():
                directory.chmod(0o700)
        self.sentinel = self.root / 'poisoned-bytecode-ran'

    def run_entrypoint(self, name, umask=0o002):
        return subprocess.run([sys.executable, str(self.prefix / name), '--help'],
                              capture_output=True, text=True, cwd=self.root,
                              env={key: value for key, value in os.environ.items()
                                   if not key.startswith('PYTHON')},
                              preexec_fn=lambda: os.umask(umask))

    def poison(self, module, directory_mode):
        """Write a valid timestamp .pyc that runs the real module and marks the sentinel."""
        source = self.prefix / module
        info = source.stat()
        code = compile(source.read_text() + '\nopen(%r, "w").write("ran")\n' % str(self.sentinel),
                       str(source), 'exec')
        cache = Path(external.cache_from_source(str(source)))
        cache.parent.mkdir(mode=0o700, exist_ok=True)
        cache.write_bytes(bytes(external._code_to_timestamp_pyc(code, info.st_mtime, info.st_size)))
        cache.chmod(0o600)
        cache.parent.chmod(directory_mode)
        return cache

    def test_g2_caches_created_under_umask_002_are_private(self):
        for name in ('bridge.py', 'memory.py', 'notify.py', 'usage_report.py'):
            with self.subTest(name=name):
                result = self.run_entrypoint(name)
                self.assertEqual(result.returncode, 0, result.stderr)
        created = [path for path in self.prefix.rglob('__pycache__')]
        self.assertTrue(created, 'the entrypoints wrote no bytecode, so nothing was tested')
        for path in created:
            self.assertEqual(path.stat().st_mode & 0o777, 0o700, str(path))

    def test_control_poisoned_bytecode_in_a_trusted_cache_does_run(self):
        # Proves the planted file is valid bytecode that an import would execute,
        # so the refusals below are the guard's doing, not a malformed cache.
        self.poison('koinon/platform_support.py', 0o700)
        result = self.run_entrypoint('bridge.py')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.sentinel.exists())

    def test_g3_group_writable_cache_is_never_read(self):
        cache = self.poison('koinon/platform_support.py', 0o775)
        for name in ('bridge.py', 'memory.py', 'notify.py', 'session.py', 'memory_service.py',
                     'session_service.py'):
            with self.subTest(name=name):
                result = self.run_entrypoint(name)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertFalse(self.sentinel.exists(), name + ' executed untrusted bytecode')
        self.assertTrue(cache.exists(), 'the guard must not remove or repair the cache')
        self.assertEqual(cache.parent.stat().st_mode & 0o777, 0o775)

    def test_g3_writable_cache_file_is_never_read(self):
        cache = self.poison('koinon/platform_support.py', 0o700)
        cache.chmod(0o620)
        result = self.run_entrypoint('bridge.py')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.sentinel.exists())

    def test_g1_scripts_never_read_any_cache_even_a_trusted_one(self):
        self.poison('koinon/platform_support.py', 0o700)
        for name in ('scripts/install.py', 'scripts/uninstall.py', 'scripts/upgrade.py'):
            with self.subTest(name=name):
                result = self.run_entrypoint(name)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertFalse(self.sentinel.exists(), name + ' read a cache')

    def test_private_cache_directory_does_not_accumulate(self):
        self.poison('koinon/platform_support.py', 0o775)
        temporary = Path(tempfile.gettempdir())
        before = set(temporary.glob('koinon-pycache-*'))
        self.assertEqual(self.run_entrypoint('bridge.py').returncode, 0)
        self.assertEqual(set(temporary.glob('koinon-pycache-*')), before)

    def test_ordinary_caches_of_every_entrypoint_and_optimization_are_movable(self):
        from koinon import upgrade_replace
        from scripts import install
        for name in ENTRYPOINTS:
            for flags in ([], ['-O']):
                result = subprocess.run([sys.executable, *flags, str(self.prefix / name), '--help'],
                                        capture_output=True, text=True, cwd=self.root,
                                        preexec_fn=lambda: os.umask(0o077))
                self.assertEqual(result.returncode, 0, result.stderr)
        package = self.prefix / 'koinon' / '__pycache__'
        count = len(list(package.iterdir()))
        self.assertGreater(count, 64, 'too few caches to exercise the bound')
        package.chmod(0o775)
        self.assertEqual(upgrade_replace.untrusted_caches(self.prefix, install.FILES), [str(package)])

    def test_importing_an_entrypoint_as_a_module_has_no_side_effects(self):
        result = subprocess.run([sys.executable, '-c',
            'import os, sys; sys.path.insert(0, sys.argv[1]); os.umask(0o022); '
            'import memory; print(oct(os.umask(0)))', str(self.prefix)],
            capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), '0o22')


if __name__ == '__main__':
    unittest.main()
