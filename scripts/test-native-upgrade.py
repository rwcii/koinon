#!/usr/bin/env python3
"""Opt-in public upgrade acceptance using one isolated native memory service."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
import install_state
import memory_service
from participant_lock import file_lock
import platform_support
from scripts import install


def session_case(backend):
    spec = importlib.util.spec_from_file_location('native_session_fixture', SOURCE / 'scripts/test-native-session.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    fixture = module.Fixture(backend)
    try:
        # Retain the fixture's private account/registry overrides in both releases.
        # No real participant receives messages and no real peer registry is used.
        source = fixture.root / 'new-source'
        source.mkdir(mode=0o700)
        for name in install.FILES:
            target = source / name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copyfile(fixture.prefix / name, target)
            target.chmod(0o600)
        for root in (source, fixture.prefix):
            for path in root.rglob('*'):
                if path.is_dir():
                    path.chmod(0o700)
        (fixture.prefix / 'LICENSE').write_text('synthetic previous release')
        before = fixture.ensure()
        result = subprocess.run([sys.executable, str(source / 'scripts/upgrade.py'),
            '--prefix', str(fixture.prefix), '--source', str(source)],
            capture_output=True, text=True, timeout=120)
        if result.returncode:
            raise RuntimeError('public session upgrade failed: ' + result.stderr)
        report = json.loads(result.stdout)
        import session_service_manager
        after = session_service_manager.status(fixture.selection)
        if (not report['ok'] or after.get('status') != 'running'
                or before['owner']['generation'] == after['owner']['generation']):
            raise RuntimeError('public session upgrade did not replace the owned pair')
        print(json.dumps(dict(ok=True, backend=backend,
            checks=['public_archive_execution', 'owned_native_pair_stop_restart',
                    'inbox_preservation', 'notifier_gate', 'post_release_readiness'])))
        return 0
    finally:
        import session_service
        import session_service_manager
        import upgrade_exclusion
        active = upgrade_exclusion.read(fixture.prefix)
        selected = session_service.Selection(fixture.prefix, fixture.home, upgrading=active is not None)
        session_service_manager.deactivate(selected)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--backend', choices=('systemd', 'launchd'), required=True)
    parser.add_argument('--kind', choices=('memory', 'session'), default='memory')
    args = parser.parse_args()
    # Python imports can create caches before a child sets its own umask.
    # All fixture subprocesses must inherit private creation permissions.
    os.umask(0o077)
    if args.kind == 'session':
        return session_case(args.backend)
    spec = importlib.util.spec_from_file_location('native_memory_fixture', SOURCE / 'scripts/test-native-memory.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    fixture = module.Fixture(args.backend)
    try:
        # Reuse only the fixture's isolated identity/artifact setup. Remove its
        # crash injection and use the ordinary product entrypoints for this run.
        source = fixture.root / 'new-source'
        source.mkdir(mode=0o700)
        for root in (source, fixture.prefix):
            for name in install.FILES:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                shutil.copyfile(SOURCE / name, target)
                target.chmod(0o600)
            for path in root.rglob('*'):
                if path.is_dir():
                    path.chmod(0o700)
        (fixture.prefix / 'LICENSE').write_text('synthetic previous release')
        ready = memory_service.ensure_managed(fixture.selection)
        if ready.get('status') != 'running' or ready.get('managed') is not True:
            raise RuntimeError('isolated native memory is not ready')
        fixture.memory_cli('--consumer', 'synthetic-upgrade', 'note', 'retained synthetic note', '--type', 'finding')
        before = fixture.memory_cli('status')['result']
        result = subprocess.run([sys.executable, str(source / 'scripts/upgrade.py'),
            '--prefix', str(fixture.prefix), '--source', str(source)],
            capture_output=True, text=True, timeout=120)
        if result.returncode:
            raise RuntimeError('public upgrade failed: ' + result.stderr)
        report = json.loads(result.stdout)
        after = fixture.memory_cli('status')['result']
        if (not report['ok'] or before['store_id'] != after['store_id']
                or before['head'] != after['head'] or before['floor'] != after['floor']):
            raise RuntimeError('public upgrade changed retained memory identity or cursors')
        if before['generation'] == after['generation']:
            raise RuntimeError('public upgrade did not replace the running child')
        print(json.dumps(dict(ok=True, backend=args.backend,
                              checks=['public_archive_execution', 'owned_native_stop_restart',
                                      'memory_preservation', 'post_release_readiness'])))
        return 0
    finally:
        # The exact temporary selection owns these actions; no host service or
        # unrelated unit is discovered, stopped, disabled, or deleted here.
        with install_state.locked(fixture.prefix, validator=lambda value: value):
            with file_lock(fixture.selection.home / 'manager.lock', 'fixture_busy', None):
                import upgrade_exclusion
                active = upgrade_exclusion.read(fixture.prefix)
                selected = memory_service.Selection(fixture.prefix, fixture.repo, upgrading=active is not None)
                memory_service.deactivate_owned(selected)
        # Keep the temporary prefix, store and operation evidence for inspection.


if __name__ == '__main__':
    raise SystemExit(main())
