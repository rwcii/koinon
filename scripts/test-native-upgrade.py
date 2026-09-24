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
import tempfile
import time

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
from koinon import install_state
import memory_service
from koinon.participant_lock import file_lock
from koinon import platform_support
from scripts import install


def public_upgrade(fixture, source, interrupt, handoff=None):
    """Kill only the isolated coordinator after durable phase publications."""
    phases = list(range(1, 20)) if interrupt == 'all' else ([int(interrupt)] if interrupt else [])
    evidence = fixture.root / 'interruptions'
    evidence.mkdir(mode=0o700)
    if phases:
        module = source / 'koinon/upgrade_journal.py'
        with module.open('a') as stream:
            stream.write("\n# Synthetic process-interruption acceptance only.\n")
            stream.write('_fixture_advance = Journal.advance\n')
            stream.write('def _fixture_interrupt(self, *args, **kwargs):\n')
            stream.write('    result = _fixture_advance(self, *args, **kwargs)\n')
            stream.write('    from pathlib import Path\n    import signal\n')
            stream.write('    marker = Path(' + repr(str(evidence)) + ') / str(result["step"])\n')
            stream.write('    if result["step"] in ' + repr(phases) + ' and not marker.exists():\n')
            stream.write('        with marker.open("x") as record: record.write("process interruption")\n')
            stream.write('        os.kill(os.getpid(), signal.SIGKILL)\n')
            stream.write('    return result\nJournal.advance = _fixture_interrupt\n')
    command = [sys.executable, str(source / 'scripts/upgrade.py'),
               '--prefix', str(fixture.prefix), '--source', str(source)]
    # The upgrade sets up the Claude status line. The fixture's overrides point the
    # installed code at the fixture's own Claude configuration; the private directory in
    # the environment only keeps the host's configuration out of reach if they do not.
    # Each file without a status line gets a synthetic user one, which must survive.
    claude = Path(tempfile.mkdtemp(prefix='koinon-upgrade-claude-'))
    status_line = dict(type='command', command='echo synthetic-status-line')
    before = {}
    for settings in (claude / 'settings.json', fixture.root / 'claude/settings.json'):
        settings.parent.mkdir(mode=0o700, exist_ok=True)
        content = json.loads(settings.read_text()) if settings.exists() else {}
        if 'statusLine' not in content:
            content['statusLine'] = status_line
            settings.write_text(json.dumps(content))
        before[settings.resolve()] = content['statusLine'].get('command', '')
    environment = dict(os.environ, CLAUDE_CONFIG_DIR=str(claude))
    interrupted = []
    for _ in range(len(phases) + (32 if handoff else 1)):
        result = subprocess.run(command, capture_output=True, text=True, timeout=120, env=environment)
        if result.returncode == 75 and handoff is not None:
            pending = json.loads(result.stdout)
            if pending.get('status') != 'manual_handoff_required':
                raise RuntimeError('unexpected pending public operation')
            pointer = json.loads((fixture.prefix / '.upgrade/current.json').read_text())
            if pending['operation'] != pointer['operation'] or pending['plan'] != pointer['plan']:
                raise RuntimeError('manual handoff refers to another operation')
            handoff(pending)
            command = [sys.executable, str(source / 'scripts/upgrade.py'), '--resume',
                       pointer['operation'], '--plan', pointer['plan']]
            continue
        if result.returncode != -9:
            if result.returncode:
                raise RuntimeError('public upgrade failed: ' + result.stderr)
            if sorted(interrupted) != phases:
                raise RuntimeError('not every selected interruption boundary was exercised')
            report = json.loads(result.stdout)
            outcome = report['result'].get('claude_statusline') or {}
            settings = Path(outcome.get('settings_file') or '').resolve()
            command = json.loads(settings.read_text()).get('statusLine', {}).get('command', '') \
                if settings in before and settings.is_file() else ''
            if outcome.get('outcome') not in ('set_up', 'unchanged') \
                    or str(fixture.prefix / 'statusline.py') not in command \
                    or ('synthetic-status-line' in before[settings]
                        and 'synthetic-status-line' not in command):
                raise RuntimeError('upgrade did not set up the Claude status line: ' + json.dumps(outcome))
            guidance = report['result'].get('claude_guidance') or {}
            hooks = [hook for group in json.loads(settings.read_text()).get('hooks', {}).get('SessionStart', [])
                     for hook in group.get('hooks', []) if str(fixture.prefix / 'session.py') in hook.get('command', '')]
            if (guidance.get('outcome') not in ('set_up', 'unchanged') or Path(guidance.get('settings_file') or '').resolve() != settings
                    or len(hooks) != 1):
                raise RuntimeError('upgrade did not set up the Claude guidance: ' + json.dumps(guidance))
            shutil.rmtree(claude, ignore_errors=True)
            return report
        pointer = json.loads((fixture.prefix / '.upgrade/current.json').read_text())
        phase = json.loads((Path(pointer['operation']) / 'phase.json').read_text())['step']
        if phase not in phases or phase in interrupted:
            raise RuntimeError('coordinator died outside the selected interruption boundary')
        interrupted.append(phase)
        command = [sys.executable, str(source / 'scripts/upgrade.py'), '--resume',
                   pointer['operation'], '--plan', pointer['plan']]
    raise RuntimeError('public coordinator did not complete after interruption retries')


def inactive_check(selection, kind, registered):
    from koinon import upgrade_observation
    observe = upgrade_observation.session if kind == 'session' else upgrade_observation.memory
    deadline = time.monotonic() + 10
    while True:
        try:
            value = observe(selection)
            if not value['running'] and value['registered'] is registered:
                return
        except (OSError, ValueError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError('inactive selection/registration was not restored')
        time.sleep(.05)


# The last release published before the implementation modules moved into the
# `koinon` package. Upgrading from a synthesized layout proves nothing: the code
# and the file list have to be the ones that release actually shipped.
PREVIOUS_RELEASE = '86567bef93021ff0e57facc1f19b1e5f4b883992'


def materialize_release(root, ref=PREVIOUS_RELEASE):
    """Extract a pinned release from history, as that release actually shipped."""
    target = Path(root) / ('release-' + ref[:12])
    target.mkdir(mode=0o700)
    archive = subprocess.run(['git', '-C', str(SOURCE), 'archive', '--format=tar', ref],
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if archive.returncode:
        raise RuntimeError('pinned release ' + ref + ' is unavailable; a shallow clone '
                           'cannot upgrade from a real previous release: '
                           + archive.stderr.decode(errors='replace'))
    extract = subprocess.run(['tar', '-x', '-C', str(target)], input=archive.stdout,
                             stderr=subprocess.PIPE)
    if extract.returncode:
        raise RuntimeError('could not extract the pinned release: '
                           + extract.stderr.decode(errors='replace'))
    return target


def session_case(backend, initial_state, interrupt):
    spec = importlib.util.spec_from_file_location('native_session_fixture', SOURCE / 'scripts/test-native-session.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    staging = Path(tempfile.mkdtemp(prefix='koinon-previous-release-'))
    previous = materialize_release(staging)
    fixture = module.Fixture(backend, release=previous)
    try:
        # The installed runtime is the pinned previous release; the source is this
        # checkout. Retain the fixture's private account/registry overrides in both,
        # so no real participant receives messages and no real peer registry is used.
        source = fixture.root / 'new-source'
        source.mkdir(mode=0o700)
        for name in install.FILES:
            target = source / name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copyfile(SOURCE / name, target)
            target.chmod(0o600)
        fixture.apply_overrides(source)
        retired = sorted(set(module.released_manifest(previous)) - set(install.FILES))
        if not retired:
            raise RuntimeError('the pinned release ships nothing this release retires; '
                               'the cross-release upgrade would prove nothing')
        for root in (source, fixture.prefix):
            for path in root.rglob('*'):
                if path.is_dir():
                    path.chmod(0o700)
        (fixture.prefix / 'LICENSE').write_text('synthetic previous release')
        before = fixture.ensure()
        from koinon import session_service_manager
        if initial_state != 'running':
            if initial_state == 'stopped':
                session_service_manager.stop(fixture.selection)
            else:
                session_service_manager.deactivate(fixture.selection)
            inactive_check(fixture.selection, 'session', initial_state == 'stopped')
        report = public_upgrade(fixture, source, interrupt)
        from koinon import session_service_manager
        if initial_state != 'running':
            inactive_check(fixture.selection, 'session', initial_state == 'stopped')
            fixture.ensure()
        after = session_service_manager.status(fixture.selection)
        if (not report['ok'] or after.get('status') != 'running'
                or before['owner']['generation'] == after['owner']['generation']):
            raise RuntimeError('public session upgrade did not replace the owned pair')
        present = [name for name in retired if (fixture.prefix / name).exists()]
        if present:
            raise RuntimeError('retired runtime paths survived the upgrade: ' + ', '.join(present[:5]))
        for name in install.FILES:
            if not (fixture.prefix / name).exists():
                raise RuntimeError('upgraded runtime is missing a published path: ' + name)
        print(json.dumps(dict(ok=True, backend=backend, initial_state=initial_state, interrupted=interrupt,
            checks=['public_archive_execution', 'owned_native_pair_stop_restart',
                    'inbox_preservation', 'notifier_gate', 'layout_migration',
                    'post_release_readiness'])))
        return 0
    finally:
        import session_service
        from koinon import session_service_manager
        from koinon import upgrade_exclusion
        active = upgrade_exclusion.read(fixture.prefix)
        selected = session_service.Selection(fixture.prefix, fixture.home, upgrading=active is not None)
        from koinon import session_service_artifacts
        with session_service_artifacts.locked(selected.home / 'lifecycle.lock'), \
                session_service_artifacts.locked(selected.home / 'registration.lock'):
            session_service_manager.deactivate_locked(selected, allow_unstarted=True)


def refresh_binding(fixture, bridge, binding):
    """Only the documented concurrent-observation refusal is retryable here."""
    replies = []
    for _ in range(3):
        # Each public refresh reads the binding again before observing memory.
        result = subprocess.run(list(map(str, bridge + ['refresh-memory', binding])),
                                env=fixture.env, capture_output=True, text=True, timeout=90)
        replies.append(dict(returncode=result.returncode, stdout=result.stdout, stderr=result.stderr))
        try:
            reply = json.loads(result.stdout)
        except ValueError:
            break
        if not isinstance(reply, dict):
            break
        if result.returncode == 0 and reply.get('ok') is True:
            return
        if (reply.get('ok') is not False or reply.get('code') != 'binding_observation_changed'
                or reply.get('recovery') != 'retry'):
            break
    raise RuntimeError('initial binding refresh failed: ' + json.dumps(replies))


def combined_case(backend, interrupt, interactive_umask=0o077):
    spec = importlib.util.spec_from_file_location('native_install_fixture', SOURCE / 'scripts/test-native-install.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    staging = Path(tempfile.mkdtemp(prefix='koinon-previous-release-'))
    previous = materialize_release(staging)
    fixture = module.Fixture(backend, session=True, release=previous)
    try:
        fixture.install()
        fixture.status()
        # The new source is this checkout, carrying the fixture's own isolation, so
        # the upgrade crosses releases rather than upgrading a release to itself.
        source = fixture.root / 'new-source'
        source.mkdir(mode=0o700)
        for name in install.FILES:
            target = source / name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copyfile(SOURCE / name, target)
            target.chmod(0o600)
        fixture.apply_overrides(source)
        source.chmod(0o700)
        (fixture.prefix / 'LICENSE').write_text('synthetic previous combined release')
        retired = sorted(set(module.released_manifest(previous)) - set(install.FILES))
        if not retired:
            raise RuntimeError('the pinned release ships nothing this release retires; '
                               'the cross-release upgrade would prove nothing')
        fixture.note('note', 'retained combined-fixture note', '--type', 'finding')
        deadline = str(int(time.time()) + 3600)
        created = json.loads(fixture.note('work', 'create', '--title', 'Synthetic upgrade claim',
            '--criteria', 'Preserved by upgrade', '--non-goals', 'No live peer work',
            '--key', 'synthetic-create', '--deadline', deadline))['result']
        started = json.loads(fixture.note('work', 'start', created['work_id'],
            '--if-revision', str(created['revision']), '--checkpoint', 'Before upgrade',
            '--next-artifact', 'Synthetic evidence', '--progress-deadline', deadline,
            '--lease-seconds', '3600', '--key', 'synthetic-start', '--deadline', deadline))['result']
        home = fixture.session_records[0]['state_directory']
        bridge = [sys.executable, fixture.prefix / 'bridge.py', '--state-dir', home]
        binding = json.loads(fixture.command(bridge + ['bind-memory', '--repo-path', fixture.repo,
            '--memory-state-dir', fixture.records[0]['service_directory']]))['result']['binding']
        # Finish the explicit initial observation before establishing the fixture
        # baseline; bind alone leaves observation to asynchronous notifier work.
        refresh_binding(fixture, bridge, binding)
        def retained_bindings():
            from koinon import memory_bindings
            page = json.loads(fixture.command(bridge + ['memory-bindings']))['result']
            if page['more']:
                raise RuntimeError('synthetic single binding unexpectedly paginated')
            return [{key: row[key] for key in memory_bindings.FIELDS} for row in page['bindings']]
        bindings = retained_bindings()
        before = json.loads(fixture.note('status'))['result']
        caches = [fixture.prefix / part / '__pycache__' for part in ('', 'koinon', 'scripts')]
        def interactive():
            # An operator's own commands, run from the installed prefix under the
            # operator's umask rather than the fixture's private one.
            for argv in (bridge + ['memory-bindings'],
                         [sys.executable, fixture.prefix / 'memory.py', '--help']):
                subprocess.run([str(value) for value in argv], check=True, capture_output=True,
                               preexec_fn=lambda: os.umask(interactive_umask))
        if interactive_umask != 0o077:
            # The fixture's own commands above ran under umask 077 and created the old
            # release's caches privately, and Python never changes an existing cache
            # directory's mode. Remove them so the operator's commands are the first
            # importers, as on an install whose services have not yet imported them.
            for path in caches:
                if path.is_dir():
                    shutil.rmtree(path)
            interactive()
            if not any(path.is_dir() and path.stat().st_mode & 0o022 for path in caches):
                raise RuntimeError('the previous release left no group-writable cache; '
                                   'the untrusted-cache upgrade would prove nothing')
        report = public_upgrade(fixture, source, interrupt)
        if interactive_umask != 0o077:
            reported = report['result']['preflight']['untrusted_caches']
            if not reported:
                raise RuntimeError('preflight did not report the untrusted cache')
            operation = Path(json.loads((fixture.prefix / '.upgrade' / 'current.json').read_text())['operation'])
            if len(list((operation / 'untrusted-cache').iterdir())) != len(reported):
                raise RuntimeError('an untrusted cache was not quarantined')
            interactive()
            unsafe = [str(path) for path in caches if path.is_dir() and path.stat().st_mode & 0o022]
            if unsafe:
                raise RuntimeError('the upgraded runtime created group-writable caches: ' + ', '.join(unsafe))
        fixture.status()
        after = json.loads(fixture.note('status'))['result']
        if (not report['ok'] or before['store_id'] != after['store_id']
                or before['head'] != after['head'] or before['floor'] != after['floor']):
            raise RuntimeError('combined upgrade changed memory identity or stream')
        if retained_bindings() != bindings:
            raise RuntimeError('combined upgrade changed explicit memory binding')
        present = [name for name in retired if (fixture.prefix / name).exists()]
        if present:
            raise RuntimeError('retired runtime paths survived the upgrade: ' + ', '.join(present[:5]))
        for name in install.FILES:
            if not (fixture.prefix / name).exists():
                raise RuntimeError('upgraded runtime is missing a published path: ' + name)
        # The original token must still renew against its original claim revision.
        fixture.note('claim', 'renew', created['work_id'], '--claim-generation',
                     str(started['claim']['generation']), '--if-claim-revision', str(started['claim']['revision']))
        print(json.dumps(dict(ok=True, backend=backend, interrupted=interrupt,
            interactive_umask='%03o' % interactive_umask,
            checks=['combined_owned_native_restart', 'active_claim_preserved',
                    'binding_preserved', 'memory_preserved', 'layout_migration',
                    'post_release_readiness']
                   + (['untrusted_cache_quarantined', 'private_caches_after_upgrade']
                      if interactive_umask != 0o077 else []))))
        return 0
    finally:
        import session_service
        from koinon import session_service_artifacts
        from koinon import session_service_manager
        from koinon import upgrade_exclusion
        if (fixture.prefix / 'install.json').exists():
            active = upgrade_exclusion.read(fixture.prefix)
            for record in fixture.session_records:
                selected = session_service.Selection(fixture.prefix, record['state_directory'], upgrading=active is not None)
                with session_service_artifacts.locked(selected.home / 'lifecycle.lock'), \
                        session_service_artifacts.locked(selected.home / 'registration.lock'):
                    session_service_manager.deactivate_locked(selected, allow_unstarted=True)
            for record in fixture.records:
                selected = memory_service.Selection(fixture.prefix, record['common_directory'], upgrading=active is not None)
                with install_state.locked(fixture.prefix, validator=lambda value: value), \
                        file_lock(selected.home / 'manager.lock', 'fixture_busy', None):
                    memory_service.deactivate_owned(selected)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--backend', choices=('systemd', 'launchd'), required=True)
    parser.add_argument('--kind', choices=('memory', 'session', 'combined'), default='memory')
    parser.add_argument('--initial-state', choices=('running', 'stopped', 'deactivated'), default='running')
    parser.add_argument('--interrupt-phase', choices=['all'] + [str(i) for i in range(1, 20)])
    parser.add_argument('--interactive-umask', choices=('077', '002'), default='077',
                        help='umask for operator commands run from the prefix (combined only)')
    args = parser.parse_args()
    # Python imports can create caches before a child sets its own umask.
    # All fixture subprocesses must inherit private creation permissions.
    os.umask(0o077)
    domain = f'gui/{os.geteuid()}' if args.backend == 'launchd' else None
    if not platform_support.memory_manager_available(args.backend, domain):
        raise RuntimeError('native user manager unavailable; acceptance unmet')
    if args.backend == 'systemd':
        # The manager private socket can answer before its typed user-bus
        # interface is ready. Observe readiness before creating any fixture.
        deadline = time.monotonic() + 15
        while True:
            try:
                platform_support.systemd_registration_layout()
                break
            except OSError:
                if time.monotonic() >= deadline:
                    raise RuntimeError('native user-manager D-Bus interface unavailable; acceptance unmet')
                time.sleep(.1)
    if args.kind == 'combined':
        if args.initial_state != 'running':
            parser.error('combined fixture currently requires running components')
        return combined_case(args.backend, args.interrupt_phase, int(args.interactive_umask, 8))
    if args.kind == 'session':
        return session_case(args.backend, args.initial_state, args.interrupt_phase)
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
        if args.initial_state != 'running':
            if args.initial_state == 'stopped':
                memory_service.stop(fixture.selection)
            else:
                with install_state.locked(fixture.prefix), file_lock(
                        fixture.selection.home / 'manager.lock', 'fixture_busy', None):
                    memory_service.deactivate_owned(fixture.selection)
            inactive_check(fixture.selection, 'memory', args.initial_state == 'stopped')
        report = public_upgrade(fixture, source, args.interrupt_phase)
        if args.initial_state != 'running':
            inactive_check(fixture.selection, 'memory', args.initial_state == 'stopped')
            memory_service.ensure_managed(fixture.selection)
        after = fixture.memory_cli('status')['result']
        if (not report['ok'] or before['store_id'] != after['store_id']
                or before['head'] != after['head'] or before['floor'] != after['floor']):
            raise RuntimeError('public upgrade changed retained memory identity or cursors')
        if before['generation'] == after['generation']:
            raise RuntimeError('public upgrade did not replace the running child')
        print(json.dumps(dict(ok=True, backend=args.backend, initial_state=args.initial_state, interrupted=args.interrupt_phase,
                              checks=['public_archive_execution', 'owned_native_stop_restart',
                                      'memory_preservation', 'post_release_readiness'])))
        return 0
    finally:
        # The exact temporary selection owns these actions; no host service or
        # unrelated unit is discovered, stopped, disabled, or deleted here.
        with install_state.locked(fixture.prefix, validator=lambda value: value):
            with file_lock(fixture.selection.home / 'manager.lock', 'fixture_busy', None):
                from koinon import upgrade_exclusion
                active = upgrade_exclusion.read(fixture.prefix)
                selected = memory_service.Selection(fixture.prefix, fixture.repo, upgrading=active is not None)
                memory_service.deactivate_owned(selected)
        # Keep the temporary prefix, store and operation evidence for inspection.


if __name__ == '__main__':
    raise SystemExit(main())
