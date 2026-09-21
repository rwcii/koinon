#!/usr/bin/env python3
"""Install a per-user bridge and optional systemd user services."""
import argparse
import copy
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys

# Run directly, `scripts/` is sys.path[0], so the project root is added to reach
# the platform module rather than testing the platform here.
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
import platform_support
import runtime_names
import install_state

MARKER = runtime_names.SERVICE_MARKER
SERVICES = runtime_names.service_names()

FILES = ('scripts/upgrade.py', 'upgrade_command.py', 'upgrade_complete.py', 'upgrade_preflight.py', 'upgrade_release.py', 'upgrade_coordinator.py', 'upgrade_probe.py', 'upgrade_start.py', 'upgrade_replace.py', 'upgrade_migration.py', 'upgrade_capture.py', 'upgrade_quiescence.py', 'upgrade_reservation.py', 'upgrade_backup.py', 'upgrade_backup_inventory.py', 'upgrade_bundle.py', 'upgrade_documents.py', 'upgrade_exclusion.py', 'upgrade_gate.py', 'upgrade_inventory.py', 'upgrade_journal.py', 'upgrade_manifest.py', 'upgrade_observation.py', 'upgrade_plan.py', 'uninstall_finalize.py', 'component_remove.py', 'session_install.py', 'component_install.py', 'session_socket_handoff.py', 'session_endpoints.py', 'session_service_manager.py', 'session_service.py', 'session_service_artifacts.py', 'session_service_config.py', 'session_supervisor.py', 'session_supervisor_state.py', 'generation_stop.py', 'memory_service.py', 'memory_service_artifacts.py', 'memory_service_config.py', 'docs/WORK-ITEMS-UPGRADE.md', 'work_guidance.py', 'docs/WORK-ITEMS-POLICY.md', 'install_state.py', 'work_policy.py', 'work_maintenance.py', 'work_items.py', 'work_storage.py', 'claims.py', 'work_schema.py', 'docs/DELIVERY.md', 'participant_presence.py', 'delivery_ledger.py', 'usage_report.py', 'usage_sources.py', 'usage_selection.py', 'docs/USAGE.md', 'runtime_names.py', 'participant_instructions.py', 'session_observation.py', 'durable_state.py', 'notification_delivery.py', 'notification_health.py', 'notification_journal.py', 'notification_legacy.py', 'notification_memory.py', 'notification_migration.py', 'notification_notices.py', 'notification_provider.py', 'notification_runtime.py', 'notification_source.py', 'notification_state.py', 'subscriptions.py','memory_bindings.py', 'inbox_schema.py', 'database_worker.py', 'service_runtime.py', 'participant_lock.py', 'peer_transport.py', 'peer_guidance.py', 'CHANGELOG.md', 'memory.py', 'session.py', 'codex_instructions.py', 'platform_support.py', 'dsh_delivery.py', 'scripts/install.py', 'scripts/uninstall.py', 'scripts/uninstall.sh', 'bridge.py', 'notify.py', 'README.md', 'PROTOCOL.md', 'LICENSE', 'CONTRIBUTING.md', 'AGENTS.md', 'docs/INSTALL.md', 'docs/NOTIFIER.md', 'docs/IDENTIFIER-MIGRATION.md', 'docs/PARITY-MEMORY-DESIGN.md')


def unit_arg(value):
    if any(c in str(value) for c in '\n\r\x00'):
        raise ValueError('unit arguments cannot contain newlines or NUL')
    # systemd specifiers and ExecStart environment expansion are distinct from shell quoting.
    return json.dumps(str(value).replace('%', '%%').replace('$', '$$'), ensure_ascii=False)


def units(prefix, state, thread, name, repo, python, codex, instance=None,
          agent='codex', model=None, dsh_url=None, dsh_credentials=None, legacy=False):
    base = [python, str(prefix/'bridge.py'), '--state-dir', str(state), 'serve']
    watcher = [python, str(prefix/'notify.py'), '--state-dir', str(state), '--thread', thread,
               '--name', name, '--repo', repo]
    # Codex is the notifier's own default, so a Codex install renders exactly the
    # argv it did before participants existed.
    if agent != 'codex':
        watcher += ['--agent', agent]
    if agent == 'deepseek':
        # A service does not inherit the harness environment, so the endpoint and
        # credential path are recorded explicitly.
        for flag, value in (('--dsh-url', dsh_url), ('--dsh-credentials', dsh_credentials)):
            if value:
                watcher += [flag, str(value)]
    else:
        watcher += ['--codex', codex]
    common = '\nRestart=on-failure\nRestartSec=5\nUMask=0077\n\n[Install]\nWantedBy=default.target\n'
    permanent = '\nRestartPreventExitStatus=' + ' '.join(map(str, platform_support.PERMANENT_EXIT_STATUSES))
    notify_name, bridge_name = runtime_names.service_names(legacy=legacy)
    rendered = {
        bridge_name: MARKER + '[Unit]\nDescription=Koinon local peer messaging bridge\n\n[Service]\nType=simple\nExecStart=' + ' '.join(map(unit_arg,base)) + permanent + common,
        notify_name: MARKER + f'[Unit]\nDescription=Koinon inbox notifications\nRequires={bridge_name}\nAfter={bridge_name}\n\n[Service]\nType=simple\nExecStart=' + ' '.join(map(unit_arg,watcher)) + permanent + common,
    }

    if instance:
        import re
        if not re.fullmatch(r'[a-f0-9]{16}',instance):
            raise ValueError('invalid service instance')
        supervisor = start_command_for(python, prefix, thread, repo, agent, model)
        rendered = {runtime_names.service_names(instance, legacy=legacy)[0]: MARKER +
                    '[Unit]\nDescription=Koinon session supervisor\n\n[Service]\nType=simple\nExecStart=' +
                    ' '.join(map(unit_arg,supervisor)) + permanent + common}
    return rendered


def start_command_for(python, prefix, thread, repo, agent, model):
    """Supervisor argv for a service instance, mirroring session.py's own shape."""
    command = [python, str(prefix/'session.py'), 'run', '--thread', thread, '--repo', repo]
    if agent != 'codex':
        command += ['--agent', agent]
    if model:
        command += ['--model', model]
    return command


def check_owned_unit(path, prefix=None):
    if path.is_symlink():
        raise ValueError(f'refusing symlinked service file: {path}')
    if path.exists() and (not path.is_file() or path.stat().st_uid != os.getuid()
                          or not path.read_text().startswith(runtime_names.SERVICE_MARKERS)):
        raise ValueError(f'refusing unrelated service file: {path}')

    if prefix is not None and path.exists() and not unit_targets_prefix(path, prefix):
        raise ValueError(f'refusing service owned by a different installation: {path}')


def unit_arguments(path):
    starts = [line[len('ExecStart='):] for line in path.read_text().splitlines()
              if line.startswith('ExecStart=')]
    if len(starts) != 1:
        raise ValueError(f'expected one managed service command: {path}')
    # The renderer uses JSON-quoted string literals. Shell parsing does not
    # decode escapes such as \t the same way and can misidentify an owned unit.
    text, arguments, offset = starts[0], [], 0
    decoder = json.JSONDecoder()
    while offset < len(text):
        if text[offset].isspace():
            offset += 1
            continue
        if text[offset] == '"':
            value, end = decoder.raw_decode(text, offset)
            if end < len(text) and not text[end].isspace():
                raise ValueError(f'ambiguous managed service argument: {path}')
        else:
            end = offset
            while end < len(text) and not text[end].isspace():
                end += 1
            value = text[offset:end]
            if any(char in value for char in ('"', "'", '\\')):
                raise ValueError(f'unsupported managed service quoting: {path}')
        arguments.append(value)
        offset = end
    return arguments


def unit_literal(value, path):
    """Decode only literal systemd escaping, never a variable or specifier."""
    result = []
    index = 0
    while index < len(value):
        char = value[index]
        if char in ('%', '$'):
            if index + 1 == len(value) or value[index + 1] != char:
                raise ValueError(f'managed unit contains a variable or specifier: {path}')
            index += 1
        result.append(char)
        index += 1
    return ''.join(result)


def unit_targets_prefix(path, prefix):
    executable = 'session.py' if '-session-' in path.name else ('notify.py' if '-notify.' in path.name else 'bridge.py')
    arguments = unit_arguments(path)
    if len(arguments) < 2:
        raise ValueError(f'managed service executable cannot be determined: {path}')
    supplied = Path(unit_literal(arguments[1], path))
    if not supplied.is_absolute():
        raise ValueError(f'managed service executable is not absolute: {path}')
    # These are installation files, not peer socket addresses. macOS aliases
    # and explicit directory symlinks must identify the same installed script.
    return supplied.resolve() == (Path(prefix) / executable).resolve()


def saved_unit_option(path, flag):
    if not path.exists():
        return None
    arguments = unit_arguments(path)
    if arguments.count(flag) != 1:
        raise ValueError(f'missing or duplicate {flag} in managed service: {path}')
    index = arguments.index(flag) + 1
    if index >= len(arguments):
        raise ValueError(f'missing {flag} value in managed service: {path}')
    return unit_literal(arguments[index], path)


def active_units(unit_dir, names=SERVICES, prefix=None):
    existing = []
    for name in names:
        local = unit_dir/name
        check_owned_unit(local, prefix)
        result = platform_support.user_service_manager('fragment', [name],
                                                       check=True, capture_output=True, text=True)
        fragment = result.stdout.strip()
        if fragment:
            actual = Path(fragment)
            if actual != local:
                raise ValueError(f'refusing service outside selected unit directory: {name}')
            check_owned_unit(actual, prefix)
            existing.append(name)
        elif local.exists():
            existing.append(name)
    return existing



def report_session_restarts(prefix, unit_dir, *, no_start=False):
    """Read-only advice; installed files do not identify loaded process versions."""
    print('Runtime replacement does not reload existing session supervisors.')
    print('Supervisors newly started by this installation load the installed files.')
    print('Restart affected manually managed supervisors explicitly; their state is not inspected.')
    if platform_support.SERVICE_MANAGER != 'systemd':
        print('Session supervisor service state: unverified (no supported service manager).')
        return
    candidates = []
    try:
        for path in Path(unit_dir).iterdir():
            if re.fullmatch(r'(?:koinon|codex-peer)-session-[a-f0-9]{16}\.service', path.name):
                candidates.append(path)
                if len(candidates) > 128:
                    print('Session supervisor inventory incomplete (more than 128 candidate units); inspect manually.')
                    return
    except FileNotFoundError:
        return
    except OSError:
        print('Session supervisor inventory unverified (selected unit directory is unreadable).')
        return
    owned = {}
    for path in sorted(candidates):
        try:
            check_owned_unit(path)
            if not path.exists():
                raise ValueError('unit disappeared')
            if not unit_targets_prefix(path, prefix):
                continue
            owned[path.name] = path
        except (OSError, ValueError):
            print('Session supervisor ownership unverified:', path.name, '(inspect manually).')
    if not owned:
        return
    reason = 'service query disabled by --no-start' if no_start else None
    observations = {}
    if not no_start:
        try:
            result = platform_support.user_service_manager(
                'observe', owned, capture_output=True, text=True, timeout=5)
            if result.returncode:
                reason = 'service manager query failed'
            else:
                # An unparseable listing invalidates the entire observation.
                for block in result.stdout.strip().split('\n\n'):
                    values = {}
                    for line in block.splitlines():
                        key, separator, value = line.partition('=')
                        if not separator or key in values:
                            raise ValueError('malformed service observation')
                        values[key] = value
                    name = values.get('Id')
                    if name not in owned or name in observations:
                        raise ValueError('unexpected service identity')
                    observations[name] = values
        except (OSError, subprocess.SubprocessError, ValueError):
            reason = 'service manager observation unavailable'
    for name, path in owned.items():
        observation = observations.get(name, {})
        if reason:
            print('Session supervisor state unverified:', name, '(' + reason + ').')
            continue
        try:
            if observation.get('FragmentPath') != str(path):
                raise ValueError('service fragment mismatch')
            # Recheck ownership after the service-manager round trip.
            check_owned_unit(path, prefix)
            if not path.exists():
                raise ValueError('unit disappeared')
        except (OSError, ValueError):
            print('Session supervisor state unverified:', name, '(service ownership or fragment changed).')
            continue
        state = observation.get('ActiveState')
        if state in ('active', 'activating', 'reloading', 'deactivating', 'refreshing'):
            print('Session supervisor may still use previous runtime; explicit restart required:', name)
        elif state in ('inactive', 'failed'):
            print('Session supervisor inactive at observation:', name, '(next start loads installed files).')
        else:
            print('Session supervisor state unverified:', name, '(unknown service state).')

def main():
    p = argparse.ArgumentParser(description=__doc__)
    work_mode = p.add_mutually_exclusive_group()
    work_mode.add_argument('--configure-work-items', action='store_true')
    work_mode.add_argument('--remove-work-items', action='store_true')
    p.add_argument('--participant', choices=['codex', 'deepseek', 'claude'])
    p.add_argument('--guidance-file', type=Path)
    p.add_argument('--thread', help='exact existing Codex thread ID')
    p.add_argument('--configure-codex', action='store_true', help='install managed global guidance and per-session registration')
    p.add_argument('--configure-memory', action='store_true', help='install repository memory without requiring a participant executable')
    p.add_argument('--service-backend', choices=('systemd', 'launchd', 'manual'))
    p.add_argument('--configure-deepseek', action='store_true',
                   help='install managed harness guidance for DeepSeek (DSH) sessions')
    p.add_argument('--codex-home', type=Path)
    p.add_argument('--dsh-home', type=Path,
                   default=None,
                   help='harness home whose AGENTS.md receives the managed DeepSeek section')
    p.add_argument('--name')
    p.add_argument('--repo')
    p.add_argument('--prefix', type=Path)
    p.add_argument('--state-dir', type=Path)
    p.add_argument('--unit-dir', type=Path)
    p.add_argument('--codex')
    p.add_argument('--no-start', action='store_true', help='write files and units without calling systemctl')
    a = p.parse_args()
    if not platform_support.SUPPORTED or sys.version_info < (3,11):
        p.error('Linux or macOS with Python 3.11+ is required')
    if a.configure_work_items or a.remove_work_items:
        if (a.thread or a.configure_codex or a.configure_deepseek or a.configure_memory or a.service_backend or a.name or a.no_start
                or a.codex_home or a.dsh_home or a.state_dir or a.unit_dir or a.codex):
            p.error('work configuration is independent of runtime installation and service options')
        if not a.repo or not a.participant or (a.configure_work_items and not a.guidance_file):
            p.error('work configuration requires --repo, --participant and an explicit --guidance-file when enabling')
        if a.remove_work_items and a.guidance_file:
            p.error('removal uses the previously configured guidance file')
        import work_guidance
        prefix = (a.prefix or runtime_names.default_prefix()).expanduser().resolve()
        try:
            result = (work_guidance.configure(prefix, a.repo, a.participant, a.guidance_file)
                      if a.configure_work_items else work_guidance.remove(prefix, a.repo, a.participant))
        except runtime_names.NameConflict:
            raise
        except (OSError, ValueError) as exc:
            print(json.dumps(dict(ok=False, code=getattr(exc, 'code', 'invalid_work_configuration'),
                                  error=str(exc), path=getattr(exc, 'path', None))))
            raise SystemExit(platform_support.CONFIGURATION_EXIT_STATUS) from None
        print(json.dumps(dict(ok=True, result=result)))
        return
    if a.participant or a.guidance_file:
        p.error('--participant and --guidance-file require a work configuration mode')
    if a.configure_memory and not a.repo:
        p.error('--configure-memory requires an explicit --repo')
    a.prefix = (a.prefix or runtime_names.default_prefix()).expanduser().resolve()
    # Refusals must not create even a prefix/lock. Recheck under the lock
    # using fresh configuration before any publication or service changes. Legacy
    # service-manager availability is deliberately checked in both passes.
    install(copy.deepcopy(a), p, validate_only=True)
    # The lock creates missing prefix ancestors before install() copies files.
    os.umask(0o077)
    with install_state.locked(a.prefix) as configuration:
        install(a, p, configuration)
    if getattr(a, 'memory_selection', None) is not None:
        import component_install
        record = component_install.stage_memory(a.prefix, a.memory_selection)
        if a.no_start:
            print('Memory selection staged; manager was not queried or started.')
        else:
            subprocess.run([sys.executable, str(a.prefix / 'memory_service.py'), 'ensure',
                            '--prefix', str(a.prefix), '--repo', record['common_directory']], check=True)
    if (a.thread and (a.configure_codex or a.configure_deepseek or a.configure_memory
                      or getattr(a, 'memory_selection', None) is not None)
            and (not a.no_start or getattr(a, 'memory_selection', None) is not None)):
        subprocess.run([sys.executable, str(a.prefix / 'session.py'), 'stage' if a.no_start else 'ensure',
                        '--thread', a.thread, '--repo', a.repo or os.getcwd()], check=True)


def install(a, p, configuration=None, validate_only=False):
    previous = configuration.config if configuration is not None else runtime_names.install_config(a.prefix)
    if previous.get('installation_state') == 'removing':
        p.error('installation removal is incomplete; resume uninstall before reinstalling')
    a.state_dir = Path(a.state_dir or previous.get('state_root') or runtime_names.default_state_root()).expanduser().resolve()
    a.unit_dir = Path(a.unit_dir or previous.get('unit_dir') or Path.home()/'.config/systemd/user').expanduser().resolve()
    memory_only = a.configure_memory and not (a.thread or a.configure_codex or a.configure_deepseek)
    if not a.thread and not (a.configure_codex or a.configure_deepseek or a.configure_memory):
        p.error('--thread, --configure-codex, --configure-deepseek or --configure-memory is required')
    a.memory_selection = None
    if a.repo and (a.configure_memory or a.configure_codex or a.configure_deepseek or a.thread):
        import component_install
        import memory_service_config
        key, _ = memory_service_config.selection(a.repo, a.state_dir)
        existing = bool(previous) or (a.prefix / 'bridge.py').exists()
        already_selected = key in previous.get('memory_services', {}).get('repositories', {})
        if not existing or already_selected or a.configure_memory:
            _, a.memory_selection = component_install.memory_selection(
                a.prefix, a.repo, a.state_dir, a.service_backend, previous)
        elif not validate_only:
            print('Existing installation scope retained. To add repository memory, rerun with '
                  '--configure-memory --repo and the same prefix/state paths.')
    elif a.service_backend:
        p.error('--service-backend requires an explicit repository selection')
    participants = set(previous.get('participants', ['codex'] if previous else []))
    participants.update(name for name, chosen in (('codex', a.configure_codex),
                                                  ('deepseek', a.configure_deepseek)) if chosen)
    explicit_codex = a.codex is not None
    saved_codex = previous.get('codex')
    saved_codex_usable = (saved_codex and Path(saved_codex).is_absolute()
                          and Path(saved_codex).is_file() and os.access(saved_codex, os.X_OK))
    if memory_only:
        a.codex = previous.get('codex')
    elif not explicit_codex:
        a.codex = saved_codex if saved_codex_usable else shutil.which('codex')
    # --thread starts a Codex session even when only DeepSeek guidance is selected.
    if not memory_only and (explicit_codex or a.thread or 'codex' in participants):
        if (not a.codex or not Path(a.codex).is_absolute()
                or not Path(a.codex).is_file() or not os.access(a.codex, os.X_OK)):
            p.error('provide an executable absolute --codex path, or install Codex CLI on PATH')
    if a.configure_codex or a.configure_deepseek or a.configure_memory or a.memory_selection is not None:
        import component_install
        component_install.runtime_preflight(a.prefix, Path(__file__).resolve().parent.parent, FILES, previous)
        if (not memory_only and a.memory_selection is not None and previous.get('session_backend') is not None
                and previous['session_backend'] != a.memory_selection['backend']):
            p.error('changing the saved session backend requires explicit reconciliation')
        a.codex_home = a.codex_home or Path(previous.get('codex_home') or
                                          os.environ.get('CODEX_HOME', str(Path.home()/'.codex')))
        a.dsh_home = a.dsh_home or Path(previous.get('dsh_home') or
                                      os.environ.get('DSH_HOME', str(Path.home()/'.dsh')))
        if validate_only:
            return
        os.umask(0o077)
        a.prefix.mkdir(parents=True,exist_ok=True)
        updates = dict(state_root=str(a.state_dir),
            unit_dir=str(a.unit_dir),codex=a.codex,codex_home=str(a.codex_home.expanduser().resolve()),
            dsh_home=str(a.dsh_home.expanduser().resolve()),
            # Which managed sections this installation wrote, so uninstall removes
            # exactly those and leaves any other participant's guidance alone.
            participants=sorted(participants),
            dsh_url=(os.environ.get('DSH_WEB_URL', previous.get('dsh_url'))
                     if a.configure_deepseek else previous.get('dsh_url')),
            dsh_credentials=(str(a.dsh_home.expanduser().resolve()/'.credentials.yaml')
                             if a.configure_deepseek else previous.get('dsh_credentials')))
        if a.memory_selection is not None and not memory_only:
            updates['session_backend'] = a.memory_selection['backend']
        if a.memory_selection is not None:
            component_install.prepare_artifact_directory(a.memory_selection)
            import memory_service_config
            key, _ = memory_service_config.identity(a.memory_selection['common_directory'])
            inventory = previous.get('memory_services', dict(version=1, repositories={}))
            if key not in inventory['repositories']:
                pending = (dict(a.memory_selection, state='pending', before_digest=None,
                                after_digest=a.memory_selection['artifact_digest'])
                           if a.memory_selection['backend'] != 'manual' else a.memory_selection)
                updates['memory_services'] = memory_service_config.admit(inventory, pending)
        # Persist the requested component set before copying modules or guidance,
        # so a retry cannot mistake an interrupted fresh install for a legacy one.
        configuration.merge(updates)
        source = Path(__file__).resolve().parent.parent
        for file in FILES:
            dest=a.prefix/file
            dest.parent.mkdir(parents=True,exist_ok=True)
            if (source/file).resolve() != dest.resolve():
                component_install.copy_runtime(source/file, dest)
        sys.path.insert(0,str(a.prefix))
        from participant_instructions import update
        guidance = update(a.codex_home,a.prefix) if a.configure_codex else None
        # The harness reads its guidance from AGENTS.md in the harness home, so a
        # DeepSeek session learns to register and read its inbox the same way a
        # Codex session does.
        dsh_guidance = update(a.dsh_home,a.prefix,agent='deepseek') if a.configure_deepseek else None
        print('Installed runtime:',a.prefix)
        print('State directory:',a.state_dir)
        if guidance is not None:
            print('Managed Codex guidance:',guidance)
        if dsh_guidance is not None:
            print('Managed DeepSeek guidance:',dsh_guidance)
        if not memory_only:
            print('New sessions run session.py ensure with their own session identity.')
            report_session_restarts(a.prefix, a.unit_dir, no_start=a.no_start)
        return
    if platform_support.SERVICE_MANAGER is None and not a.no_start:
        # The managed supervisor is the portable alternative to a service manager:
        # `session.py ensure` reports `manual_required` with a start command, and
        # `session.py run` owns both children in one persistent session.
        p.error('systemd user services are Linux-only; on macOS use --configure-codex and run the '
                'printed start_command in a managed session, or pass --no-start to write files only')
    checkpoint = a.state_dir/'notify-cursor.json'
    if checkpoint.exists() and json.loads(checkpoint.read_text())['thread'] != a.thread:
        p.error('existing state belongs to a different thread; select another state directory')
    selected = runtime_names.selected_service_names(a.unit_dir)
    for name in selected:
        check_owned_unit(a.unit_dir/name, a.prefix)
    notifier_unit = a.unit_dir / selected[0]
    saved_target = saved_unit_option(notifier_unit, '--thread')
    if saved_target is not None and saved_target != a.thread:
        p.error('existing service belongs to a different thread; preserve its state and select a separate installation')
    a.name = a.name or saved_unit_option(notifier_unit, '--name') or 'codex-peer'
    a.repo = a.repo or saved_unit_option(notifier_unit, '--repo') or os.getcwd()
    rendered = units(a.prefix,a.state_dir,a.thread,a.name,str(Path(a.repo).resolve()),sys.executable,a.codex,
                     legacy=selected == runtime_names.service_names(legacy=True))
    if not a.no_start:
        # Fail before modifying installation if the user manager is unavailable.
        platform_support.user_service_manager('available',check=True,stdout=subprocess.DEVNULL)
        existing = active_units(a.unit_dir, selected, a.prefix)
        if validate_only:
            return
        if existing:
            platform_support.user_service_manager('stop', existing, check=True)
    if validate_only:
        return
    os.umask(0o077)
    a.prefix.mkdir(parents=True,exist_ok=True)
    a.unit_dir.mkdir(parents=True,exist_ok=True)
    source = Path(__file__).resolve().parent.parent
    for file in FILES:
        (a.prefix/file).parent.mkdir(parents=True,exist_ok=True)
        if (source/file).resolve() != (a.prefix/file).resolve():
            shutil.copyfile(source/file,a.prefix/file)
    for name,content in rendered.items():
        (a.unit_dir/name).write_text(content)
    if a.no_start:
        print('Files and units written; services were not changed.')
    else:
        platform_support.user_service_manager('reload', check=True)
        platform_support.user_service_manager('enable', rendered, check=True)
    print('Installed at',a.prefix)
    print('State directory:',a.state_dir)
    print('Check: systemctl --user status', *selected)
    report_session_restarts(a.prefix, a.unit_dir, no_start=a.no_start)


if __name__ == '__main__':
    try:
        main()
    except runtime_names.NameConflict as exc:
        print(json.dumps(dict(ok=False, code=exc.code, paths=exc.paths, error=str(exc))))
        raise SystemExit(75 if exc.code == 'configuration_busy' else
                         platform_support.CONFIGURATION_EXIT_STATUS) from None
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        import durable_state
        temporary = isinstance(exc, (durable_state.StateReadBusyError, subprocess.SubprocessError))
        print(json.dumps(dict(ok=False, code=getattr(exc, 'code', 'installation_incomplete'),
                              error=str(exc), paths=getattr(exc, 'paths', ()),
                              recovery='Preserve existing state and retry after correcting the reported condition.')))
        raise SystemExit(getattr(exc, 'exit_status', 75 if temporary else 78)) from None
