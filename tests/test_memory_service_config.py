"""Staged memory-service configuration uses only synthetic repositories and paths."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from koinon import install_state
import memory
from koinon import memory_service_config as config
from koinon import runtime_names


def record(common='/synthetic/repo/.git', backend='systemd'):
    key, digest = config.identity(common)
    name = config.artifact_name(key, backend)
    return dict(common_directory=common, identity_digest=digest,
                state_root='/synthetic/state', service_directory='/synthetic/state/memory/' + key,
                backend=backend, artifact='/synthetic/units/' + name if name else None,
                artifact_digest='b' * 64 if name else None, state='installed')


def inventory(*records):
    return dict(version=1, repositories={config.identity(r['common_directory'])[0]: r for r in records})


class MemoryServiceConfigurationTests(unittest.TestCase):
    def test_main_worktree_subdirectory_and_bare_identity_without_state_creation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            repo, linked, bare, state = (root / name for name in ('repo', 'linked', 'bare.git', 'state'))
            def git(*args):
                subprocess.run(['git', *map(str, args)], check=True, capture_output=True)
            git('init', repo)
            git('-C', repo, '-c', 'user.name=Synthetic', '-c', 'user.email=synthetic@example.invalid',
                '-c', 'commit.gpgsign=false', 'commit', '--allow-empty', '-m', 'synthetic')
            git('-C', repo, 'worktree', 'add', '--detach', linked)
            nested = linked / 'nested'
            nested.mkdir()
            expected = config.selection(repo, state)
            for path in (linked, nested, memory.repo_common_directory(repo)):
                self.assertEqual(config.selection(path, state), expected)
            self.assertEqual(expected[0], memory.repo_identity(repo))
            git('init', '--bare', bare)
            self.assertNotEqual(config.selection(bare, state)[0], expected[0])
            self.assertFalse(state.exists())

    def test_filesystem_verification_refuses_consistent_alias_without_creating_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            repo = root / 'repo'
            subprocess.run(['git', 'init', '-q', str(repo)], check=True)
            common = memory.repo_common_directory(repo)
            canonical = record(str(common))
            canonical.update(state_root=str(root / 'state'),
                             service_directory=str(root / 'state' / 'memory' / config.identity(common)[0]))
            self.assertEqual(config.verify_selection(canonical)[0], memory.repo_identity(repo))
            alias = root / 'common-alias'
            alias.symlink_to(common, target_is_directory=True)
            retained = record(str(alias))
            retained.update(state_root=str(root / 'state'),
                            service_directory=str(root / 'state' / 'memory' / config.identity(alias)[0]))
            config.validate(inventory(retained))  # Internal consistency is not resolution.
            with self.assertRaises(ValueError):
                config.verify_selection(retained)
            self.assertFalse((root / 'state').exists())

    def test_all_backends_and_durable_publication_states(self):
        for backend in config.BACKENDS:
            r = record(backend=backend)
            self.assertEqual(config.validate(inventory(r)), inventory(r))
            if backend == 'manual':
                continue
            for before in (None, 'a' * 64):
                pending = dict(r, state='pending', before_digest=before, after_digest=r['artifact_digest'])
                config.validate(inventory(pending))
            config.validate(inventory(dict(r, state='removing', before_digest=r['artifact_digest'],
                                           after_digest=None)))

    def test_launchd_domain_is_explicit_canonical_and_preserved(self):
        old = record(backend='launchd')
        selected = dict(old, manager_domain='gui/501')
        self.assertEqual(config.validate(inventory(selected)), inventory(selected))
        self.assertEqual(config.admit(inventory(selected), selected), inventory(selected))
        # An older staged record is still readable, but never silently acquires
        # the domain of whichever session happens to read it.
        self.assertEqual(config.validate(inventory(old)), inventory(old))
        with self.assertRaises(ValueError):
            config.admit(inventory(old), selected)
        for domain in ('system', 'user/501', 'gui/0501', 'gui/-1', 'gui/4294967296', None):
            with self.subTest(domain=domain), self.assertRaises(ValueError):
                config.validate(inventory(dict(old, manager_domain=domain)))
        with self.assertRaises(ValueError):
            config.validate(inventory(dict(record(), manager_domain='gui/501')))

    def test_template_version_is_explicit_and_backend_specific(self):
        for version in (1, 2):
            selected = dict(record(), template_version=version)
            self.assertEqual(config.validate(inventory(selected)), inventory(selected))
        for version in (True, 0, 3, '2'):
            with self.subTest(version=version), self.assertRaises(ValueError):
                config.validate(inventory(dict(record(), template_version=version)))
        with self.assertRaises(ValueError):
            config.validate(inventory(dict(record(backend='launchd'), template_version=2)))

    def test_cap_refuses_new_selection_but_allows_identical_repeat_without_aliases(self):
        records = [record('/synthetic/repo%d/.git' % n) for n in range(config.MAX_SERVICES)]
        full = inventory(*records)
        before = copy.deepcopy(full)
        repeated = config.admit(full, records[0])
        repeated['repositories'].clear()
        self.assertEqual(full, before)
        with self.assertRaises(runtime_names.NameConflict) as raised:
            config.admit(full, record('/synthetic/one-more/.git'))
        self.assertEqual(raised.exception.code, 'memory_service_limit')
        self.assertEqual(full, before)
        # The protocol must not accidentally inherit installer admission errors.
        self.assertIn('memory_service_limit', runtime_names.CONFIGURATION_CODES)
        self.assertNotIn('memory_service_limit', memory.ERROR_EXIT_CLASSES['configuration'])

    def test_admission_refuses_retarget_or_lifecycle_transition(self):
        original = record()
        saved = inventory(original)
        for changed in (dict(original, state_root='/other',
                             service_directory='/other/memory/' + config.identity(original['common_directory'])[0]),
                        dict(original, artifact='/other/' + Path(original['artifact']).name),
                        dict(original, state='pending', before_digest='a' * 64, after_digest='b' * 64),
                        record(backend='launchd')):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                config.admit(saved, changed)
        self.assertEqual(saved, inventory(original))

    def test_full_identity_prevents_short_key_collision_adoption(self):
        def collision(common):
            digest = 'a' * 16 + hashlib.sha256(str(common).encode()).hexdigest()[16:]
            return digest[:16], digest
        with patch.object(config, 'identity', side_effect=collision):
            first, second = record('/synthetic/first/.git'), record('/synthetic/second/.git')
            self.assertEqual(config.identity(first['common_directory'])[0],
                             config.identity(second['common_directory'])[0])
            with self.assertRaises(ValueError):
                config.admit(inventory(first), second)

    def test_malformed_retained_selection_refuses_without_writes(self):
        original = record()
        cases = [None, [], {}, dict(version=True, repositories={}), dict(version=2, repositories={}),
                 dict(version=1, repositories=[]), dict(version=1, repositories={}, extra=True),
                 inventory(*[record('/synthetic/r%d/.git' % n) for n in range(65)])]
        for field, bad in [('common_directory', 'relative'), ('common_directory', '/a/../b'),
                           ('identity_digest', 'f' * 64), ('state_root', '/a\nb'),
                           ('service_directory', '/other'), ('backend', 'unknown'), ('state', 'ready'),
                           ('artifact', '/synthetic/units/unrelated.service'), ('artifact_digest', None)]:
            item = dict(original, **{field: bad})
            cases.append(dict(version=1, repositories={config.identity(original['common_directory'])[0]: item}))
        cases += [inventory(dict(original, extra=1)), inventory(dict(original, state='pending')),
                  inventory(dict(original, state='pending', before_digest='z', after_digest='b' * 64)),
                  inventory(dict(original, state='pending', before_digest=None, after_digest='c' * 64)),
                  inventory(dict(original, state='removing', before_digest='a' * 64, after_digest=None)),
                  inventory(dict(record(backend='manual'), state='pending', before_digest=None, after_digest=None))]
        with tempfile.TemporaryDirectory() as temporary:
            prefix = Path(temporary)
            target = prefix / 'install.json'
            for bad in cases:
                retained = dict(state_root='/synthetic/state', unit_dir='/synthetic/units', memory_services=bad)
                content = json.dumps(retained).encode()
                target.write_bytes(content)
                with self.subTest(bad=bad), self.assertRaises(runtime_names.NameConflict) as raised:
                    runtime_names.install_config(prefix)
                self.assertEqual(raised.exception.code, 'invalid_install_configuration')
                self.assertEqual(target.read_bytes(), content)
                self.assertFalse((prefix / install_state.LOCK_NAME).exists())

    def test_locked_merge_preserves_unrelated_configuration_and_saved_selection(self):
        with tempfile.TemporaryDirectory() as temporary:
            prefix = Path(temporary)
            retained = dict(state_root='/synthetic/state', unit_dir='/synthetic/units',
                            opaque={'retain': [1, 2]}, work_items={'version': 1, 'rules': {}},
                            memory_services=inventory(record()))
            with install_state.locked(prefix) as state:
                state.merge(retained)
                state.merge(dict(participants=['deepseek']))
            restored = runtime_names.install_config(prefix)
            for key, value in retained.items():
                self.assertEqual(restored[key], value)
            self.assertEqual(restored['participants'], ['deepseek'])
            self.assertEqual(set(prefix.iterdir()), {prefix / 'install.json', prefix / install_state.LOCK_NAME})

    def test_new_admission_is_pure_and_does_not_alias_caller_record(self):
        current, candidate = inventory(), record()
        result = config.admit(current, candidate)
        candidate['state_root'] = '/changed'
        self.assertEqual(current, inventory())
        config.validate(result)
        self.assertEqual(len(result['repositories']), 1)


if __name__ == '__main__':
    unittest.main()
