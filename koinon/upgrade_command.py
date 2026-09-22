"""Public upgrade dispatcher and retained recovery entrypoint.

Native and manual memory selections and native sessions are executable.
Unsupported selections refuse before shutdown; never infer adoption or rollback.
"""
import argparse
import json
import os
from pathlib import Path
import sys
import uuid

from koinon import durable_state
from koinon import install_state
from koinon import platform_support
from koinon import upgrade_bundle
from koinon import upgrade_complete
from koinon import upgrade_coordinator
from koinon.upgrade_documents import Documents
from koinon import upgrade_exclusion
from koinon import upgrade_manifest as manifest
from koinon import upgrade_manual
from koinon import upgrade_plan
from koinon import upgrade_preflight


def _private_directory(path):
    try:
        path.mkdir(mode=0o700)
    except FileExistsError:
        pass
    manifest.check_root(path)
    if path.lstat().st_mode & 0o077:
        raise ValueError('upgrade recovery directory must be private')
    platform_support.sync_state_directory(path.parent)
    return path


def _current(prefix):
    path = prefix / '.upgrade' / 'current.json'
    value = durable_state.read(path)
    if value is None:
        return None
    if (set(value) != {'version', 'operation', 'plan'} or value['version'] != 1
            or not manifest.hex_digest(value['plan'])):
        raise ValueError('invalid upgrade operation pointer')
    loaded = upgrade_plan.load(value['operation'], value['plan'])
    if loaded['plan']['canonical_prefix'] != str(prefix):
        raise ValueError('upgrade pointer selects a different installation')
    return loaded


def prepare(prefix, source):
    """Complete native preflight under one installation lock; retain recovery first."""
    prefix = manifest.select_root(prefix)
    pair = upgrade_preflight.runtime_pair(prefix, source)
    caches = upgrade_preflight.bytecode_caches(prefix, pair)
    with install_state.locked(prefix) as installed:
        previous = _current(prefix)
        if previous is not None and previous['phase']['step'] != 19:
            raise ValueError('unfinished upgrade exists; use --resume with its retained operation')
        observed = upgrade_preflight.observe_locked(prefix, installed)
        upgrade_complete.supported(observed['components'])
        parent = _private_directory(prefix / '.upgrade')
        operation = _private_directory(parent / uuid.uuid4().hex)
        workspace = _private_directory(operation / 'inventory-workspace')
        budget = upgrade_preflight.storage_budget(observed['components'], pair['runtime'], operation)
        checks = []
        for component in observed['components']:
            record = component['selection']
            kind = component['kind']
            if kind == 'memory':
                from koinon import memory_service_config
                root, database = Path(record['service_directory']), 'memory.sqlite3'
                repo, _ = memory_service_config.identity(record['common_directory'])
            else:
                root, database, repo = Path(record['state_directory']), 'inbox.sqlite3', None
            checks.append(upgrade_preflight.database_check(root, database, workspace, kind=kind, repo=repo))
        report_overhead = len(json.dumps(dict(memory_ownership=observed['memory_ownership'],
                                              service_ownership=observed['service_ownership'],
                                              capacity=budget, databases=checks)).encode()) + 65536
        if sum(item['report_bytes'] for item in checks) + report_overhead > 1024 * 1024:
            raise ValueError('aggregate preservation report exceeds private document capacity')
        if upgrade_preflight.observe_locked(prefix, installed) != observed:
            raise ValueError('selected services changed during preflight')
        _private_directory(operation / 'runtime-backup')
        for index, _ in enumerate(observed['components']):
            _private_directory(operation / f'component-backup-{index:03d}')
        recovery = upgrade_bundle.prepare(operation, pair['source'], 'koinon/upgrade_command.py')
        prepared = upgrade_plan.prepare(operation, prefix, **pair,
            installation=installed.config, components=observed['components'], recovery=recovery)
        documents = Documents(operation)
        documents.put('prepared-checks', dict(version=1, plan=prepared['sha256'],
            memory_ownership=observed['memory_ownership'],
            service_ownership=observed['service_ownership'], capacity=budget, databases=checks,
            untrusted_caches=caches))
        # Publish a discoverable recovery pointer before the exclusion marker.
        # A crash here resumes phase zero under the original configuration.
        durable_state.publish(parent / 'current.json', dict(version=1,
            operation=str(operation), plan=prepared['sha256']))
        with upgrade_exclusion.operation(operation, prepared['sha256']) as exclusion:
            exclusion.activate_locked(installed)
        return upgrade_plan.load(operation, prepared['sha256'])


def resume(directory, digest):
    with upgrade_exclusion.operation(directory, digest) as exclusion:
        phase = exclusion.journal.read()
        upgrade_complete.supported(exclusion.loaded['documents']['components']['items'])
        if phase['step'] == 0:
            exclusion.activate()
            value = durable_state.read(Documents(directory).path('prepared-checks'), max_bytes=1024 * 1024)
            if not isinstance(value, dict) or value.get('plan') != digest:
                raise ValueError('prepared preflight evidence missing or mismatched')
            evidence = Documents(directory).read('prepared-checks', manifest.fingerprint(value))
            phase = exclusion.journal.advance(phase, evidence=manifest.fingerprint(evidence))
        if phase['step'] == 1:
            exclusion.verify()
            phase = exclusion.journal.advance(phase)
        if 2 <= phase['step'] <= 9:
            phase = upgrade_coordinator.through_replacement(exclusion)
            phase = exclusion.journal.advance(phase)
        return upgrade_complete.run(exclusion)


def _execute(loaded):
    archive = upgrade_bundle.verify(loaded['documents']['recovery'], loaded['documents']['source'],
                                    require_source=loaded['phase']['step'] != 19)
    # The bytecode guard's private cache directory stays empty, and execv skips the
    # exit handler that would remove it. The archive imports from the zip, which
    # never reads a __pycache__ directory.
    private = getattr(sys, 'pycache_prefix', None)
    if private and Path(private).name.startswith('koinon-pycache-'):
        try:
            os.rmdir(private)
        except OSError:
            pass
    os.execv(sys.executable, [sys.executable, '-I', str(archive), '--resume',
                             loaded['plan']['directory'], '--plan', loaded['sha256'], '--recovery'])


def main(argv=None):
    parser = argparse.ArgumentParser(description='Resumable runtime upgrade of an installed prefix')
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--prefix', help='explicit installed prefix for a new operation')
    mode.add_argument('--resume', metavar='DIRECTORY', help='retained operation directory')
    mode.add_argument('--status', metavar='PREFIX', help='report retained operation without service actions')
    parser.add_argument('--source', help='explicit new source root; required with --prefix')
    parser.add_argument('--plan', help='exact plan digest; required with --resume')
    parser.add_argument('--recovery', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    try:
        if args.status:
            if args.source or args.plan or args.recovery:
                parser.error('--status does not accept execution arguments')
            loaded = _current(manifest.select_root(args.status))
            if loaded is None:
                result = dict(status='no_recorded_upgrade')
            else:
                from koinon.upgrade_journal import Journal
                journal = Journal(loaded['plan']['directory'], loaded['sha256'])
                result = dict(operation=loaded['plan']['directory'], plan=loaded['sha256'],
                              **journal.describe(journal.read()))
        elif args.prefix:
            if not args.source or args.plan or args.recovery:
                parser.error('--prefix requires --source and does not accept resume arguments')
            loaded = prepare(args.prefix, args.source)
            _execute(loaded)
            raise RuntimeError('recovery execution unexpectedly returned')
        else:
            if not args.plan or args.source:
                parser.error('--resume requires --plan and does not accept --source')
            loaded = upgrade_plan.load(args.resume, args.plan)
            if not args.recovery:
                _execute(loaded)
                raise RuntimeError('recovery execution unexpectedly returned')
            archive = Path(loaded['documents']['recovery']['archive']['root']) / upgrade_bundle.ARCHIVE
            if Path(sys.argv[0]).resolve() != archive.resolve():
                raise ValueError('recovery actions must execute from the retained archive')
            result = resume(args.resume, args.plan)
        print(json.dumps(dict(ok=True, result=result), sort_keys=True))
        return 0
    except upgrade_manual.HandoffRequired as exc:
        print(json.dumps(dict(ok=False, **exc.handoff), sort_keys=True))
        return 75
    except (OSError, ValueError) as exc:
        result = dict(ok=False, error=str(exc))
        if isinstance(exc, (upgrade_preflight.UnownedMemoryError, upgrade_preflight.UnownedServiceError)):
            service = isinstance(exc, upgrade_preflight.UnownedServiceError)
            result['service_ownership' if service else 'memory_ownership'] = exc.report
            result['recovery'] = dict(
                code='unowned_service_requires_inventory' if service else 'unowned_memory_requires_inventory',
                guide='docs/WORK-ITEMS-UPGRADE.md#recovering-from-unowned-memory-refusal',
                next_step='Keep the current runtime and state intact; identify the reported store and its service before choosing the documented legacy upgrade procedure.',
                preserve='Do not delete, move, rename or relabel the reported state to make preflight pass. It may contain the only copy of shared memory.',
                phase='refused_before_shutdown')
        print(json.dumps(result, sort_keys=True), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
