#!/usr/bin/env python3
"""Install a per-user bridge and optional systemd user services."""
# Bytecode guard (docs/INSTALL.md). It runs before the first
# project import and uses only the standard library, because a shared helper would
# itself load from the cache it must judge. It keeps the canonical text below, which
# tests/test_bytecode_guard.py compares across every entrypoint.
if __name__ == '__main__':
    import os as _os, stat as _stat, sys as _sys, tempfile as _tempfile
    _os.umask(0o077)
    _here = _os.path.dirname(_os.path.abspath(__file__))
    _script = _os.path.basename(_here) == 'scripts'
    _root = _os.path.dirname(_here) if _script else _here

    def _owned(info, kind):
        return kind(info.st_mode) and info.st_uid == _os.geteuid() and not info.st_mode & 0o022

    def _trusted(path):
        try:
            info = _os.lstat(path)
        except FileNotFoundError:
            return True
        if not _owned(info, _stat.S_ISDIR):
            return False
        with _os.scandir(path) as entries:
            return all(_owned(entry.stat(follow_symlinks=False), _stat.S_ISREG)
                       and entry.stat(follow_symlinks=False).st_nlink == 1 for entry in entries)

    if _script or not all(_trusted(_os.path.join(_root, part, '__pycache__'))
                            for part in ('', 'koinon', 'scripts')):
        _sys.pycache_prefix = _tempfile.mkdtemp(prefix='koinon-pycache-')
        _sys.dont_write_bytecode = True
        import atexit as _atexit
        _atexit.register(lambda path=_sys.pycache_prefix: _os.path.isdir(path) and _os.rmdir(path))
# End of bytecode guard.
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
from koinon import platform_support
from koinon import runtime_names
from koinon import install_state

MARKER = runtime_names.SERVICE_MARKER
SERVICES = runtime_names.service_names()

FILES = ('koinon/path_permissions.py', 'koinon/upgrade_discovery.py', 'koinon/upgrade_manual.py', 'scripts/upgrade.py', 'koinon/upgrade_command.py', 'koinon/upgrade_complete.py', 'koinon/upgrade_preflight.py', 'koinon/upgrade_release.py', 'koinon/upgrade_coordinator.py', 'koinon/upgrade_probe.py', 'koinon/upgrade_start.py', 'koinon/upgrade_replace.py', 'koinon/upgrade_migration.py', 'koinon/upgrade_capture.py', 'koinon/upgrade_quiescence.py', 'koinon/upgrade_reservation.py', 'koinon/upgrade_backup.py', 'koinon/upgrade_backup_inventory.py', 'koinon/upgrade_bundle.py', 'koinon/upgrade_documents.py', 'koinon/upgrade_exclusion.py', 'koinon/upgrade_gate.py', 'koinon/upgrade_inventory.py', 'koinon/upgrade_journal.py', 'koinon/upgrade_layout.py', 'koinon/upgrade_manifest.py', 'koinon/upgrade_observation.py', 'koinon/upgrade_plan.py', 'koinon/uninstall_finalize.py', 'koinon/component_remove.py', 'koinon/session_install.py', 'koinon/component_install.py', 'koinon/session_socket_handoff.py', 'koinon/session_endpoints.py', 'koinon/session_service_manager.py', 'session_service.py', 'koinon/session_service_artifacts.py', 'koinon/session_service_config.py', 'koinon/session_supervisor.py', 'koinon/session_supervisor_state.py', 'koinon/generation_stop.py', 'memory_service.py', 'koinon/memory_service_artifacts.py', 'koinon/memory_service_config.py', 'docs/WORK-ITEMS-UPGRADE.md', 'koinon/work_guidance.py', 'docs/WORK-ITEMS-POLICY.md', 'koinon/install_state.py', 'koinon/work_policy.py', 'koinon/work_maintenance.py', 'koinon/work_items.py', 'koinon/work_storage.py', 'koinon/__init__.py', 'koinon/claims.py', 'koinon/work_schema.py', 'docs/DELIVERY.md', 'koinon/participant_presence.py', 'koinon/participant_status.py', 'koinon/participant_work.py', 'koinon/repository_identity.py', 'koinon/codex_status.py', 'koinon/claude_statusline.py', 'koinon/claude_guidance.py', 'koinon/delivery_ledger.py', 'usage_report.py', 'statusline.py', 'koinon/usage_sources.py', 'koinon/usage_selection.py', 'docs/USAGE.md', 'koinon/runtime_names.py', 'koinon/participant_instructions.py', 'koinon/guidance.py', 'koinon/revisions.py', 'koinon/session_observation.py', 'koinon/tmux_terminal.py', 'koinon/alias_lease.py', 'koinon/durable_state.py', 'koinon/notification_delivery.py', 'koinon/notification_health.py', 'koinon/notification_journal.py', 'koinon/notification_legacy.py', 'koinon/notification_memory.py', 'koinon/notification_migration.py', 'koinon/notification_notices.py', 'koinon/notification_provider.py', 'koinon/notification_runtime.py', 'koinon/notification_source.py', 'koinon/notification_state.py', 'koinon/subscriptions.py', 'koinon/memory_bindings.py', 'koinon/inbox_schema.py', 'koinon/database_worker.py', 'koinon/service_runtime.py', 'koinon/participant_lock.py', 'koinon/peer_transport.py', 'koinon/peer_guidance.py', 'CHANGELOG.md', 'memory.py', 'session.py', 'koinon/codex_instructions.py', 'koinon/platform_support.py', 'koinon/dsh_delivery.py', 'scripts/install.py', 'scripts/uninstall.py', 'scripts/uninstall.sh', 'bridge.py', 'notify.py', 'README.md', 'PROTOCOL.md', 'LICENSE', 'CONTRIBUTING.md', 'AGENTS.md', 'docs/INSTALL.md', 'docs/NOTIFIER.md', 'docs/IDENTIFIER-MIGRATION.md', 'docs/PARITY-MEMORY-DESIGN.md')


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
    work_mode.add_argument('--claude-statusline', action='store_true',
                           help='set up the Claude Code status-line wrapper now, reversing a saved decline')
    work_mode.add_argument('--remove-claude-statusline', action='store_true',
                           help='restore the previous Claude Code status line and record a decline')
    work_mode.add_argument('--claude-guidance', action='store_true',
                           help='set up the Claude CLAUDE.md block and SessionStart hook now, reversing a saved decline')
    work_mode.add_argument('--remove-claude-guidance', action='store_true',
                           help='remove the Claude CLAUDE.md block and SessionStart hook and record a decline')
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
    p.add_argument('--replace-guidance', action='store_true',
                   help='replace a managed guidance block that was edited or removed, after a backup')
    p.add_argument('--no-claude-statusline', action='store_true',
                   help='do not set up the Claude Code status-line wrapper during installation')
    p.add_argument('--no-claude-guidance', action='store_true',
                   help='do not set up the Claude CLAUDE.md block and SessionStart hook during installation')
    a = p.parse_args()
    if not platform_support.SUPPORTED or sys.version_info < (3,11):
        p.error('Linux or macOS with Python 3.11+ is required')
    if a.claude_statusline or a.remove_claude_statusline or a.claude_guidance or a.remove_claude_guidance:
        if (a.thread or a.configure_codex or a.configure_deepseek or a.configure_memory or a.service_backend
                or a.name or a.no_start or a.codex_home or a.dsh_home or a.state_dir or a.unit_dir or a.codex
                or a.repo or a.participant or a.guidance_file or a.no_claude_statusline or a.no_claude_guidance
                or (a.replace_guidance and not a.claude_guidance)):
            p.error('the Claude integration modes take only --prefix, and --replace-guidance with --claude-guidance')
        prefix = (a.prefix or runtime_names.default_prefix()).expanduser().resolve()
        if not (prefix / 'statusline.py').is_file() or not (prefix / 'install.json').is_file():
            p.error(f'no installed runtime at {prefix}; install first')
        if (a.claude_guidance or a.remove_claude_guidance) and not (prefix / 'koinon/claude_guidance.py').is_file():
            p.error(f'the runtime at {prefix} predates Claude guidance; upgrade it first')
        sys.path.insert(0, str(prefix))
        from koinon import claude_guidance, claude_statusline
        os.umask(0o077)
        try:
            with install_state.locked(prefix) as configuration:
                if a.claude_statusline:
                    result = claude_statusline.set_up(configuration, prefix, sys.executable, explicit=True)
                elif a.remove_claude_statusline:
                    result = claude_statusline.remove(configuration, prefix)
                elif a.claude_guidance:
                    result = claude_guidance.set_up(configuration, prefix, sys.executable, explicit=True,
                                                    replace=a.replace_guidance)
                else:
                    result = claude_guidance.remove(configuration, prefix)
        except runtime_names.NameConflict:
            raise
        except (OSError, ValueError) as exc:
            # A settings refusal, or a CLAUDE.md block whose markers need manual repair.
            print(json.dumps(dict(ok=False, code=getattr(exc, 'code', 'guidance_unavailable'), error=str(exc),
                                  path=getattr(exc, 'path', None))))
            raise SystemExit(platform_support.CONFIGURATION_EXIT_STATUS) from None
        print(json.dumps(dict(ok=True, result=result)))
        return
    if a.configure_work_items or a.remove_work_items:
        if (a.thread or a.configure_codex or a.configure_deepseek or a.configure_memory or a.service_backend or a.name or a.no_start
                or a.codex_home or a.dsh_home or a.state_dir or a.unit_dir or a.codex):
            p.error('work configuration is independent of runtime installation and service options')
        if not a.repo or not a.participant or (a.configure_work_items and not a.guidance_file):
            p.error('work configuration requires --repo, --participant and an explicit --guidance-file when enabling')
        if a.remove_work_items and a.guidance_file:
            p.error('removal uses the previously configured guidance file')
        from koinon import work_guidance
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
        from koinon import component_install
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


def claude_status_line(a, configuration):
    """Set up the Claude Code status-line wrapper by default; never fail the installation.

    `--no-start` stages files without activating anything, so it leaves Claude settings
    unchanged; so does a user's decline.
    """
    from koinon import claude_statusline
    if a.no_start:
        return dict(action='set_up', outcome='skipped', reason='no_start')
    try:
        if a.no_claude_statusline:
            return claude_statusline.decline(configuration, a.prefix)
        return claude_statusline.set_up(configuration, a.prefix, sys.executable)
    except (OSError, claude_statusline.SettingsError) as exc:
        return dict(action='set_up', outcome='failed', code=getattr(exc, 'code', 'settings_unavailable'),
                    error=str(exc), repair=claude_statusline.repair_command(a.prefix, sys.executable))


def claude_guidance_setup(a, configuration):
    """Set up the Claude CLAUDE.md block and SessionStart hook by default, like the status line."""
    from koinon import claude_guidance
    if a.no_start:
        return dict(action='set_up', outcome='skipped', reason='no_start')
    try:
        if a.no_claude_guidance:
            return claude_guidance.decline(configuration, a.prefix)
        return claude_guidance.set_up(configuration, a.prefix, sys.executable, replace=a.replace_guidance)
    except (OSError, ValueError) as exc:
        return dict(action='set_up', outcome='failed', code=getattr(exc, 'code', 'settings_unavailable'),
                    error=str(exc), repair=claude_guidance.repair_command(a.prefix, sys.executable))


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
        from koinon import component_install
        from koinon import memory_service_config
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
        from koinon import component_install
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
            from koinon import memory_service_config
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
        from koinon import revisions
        configuration.merge(revisions.installed_fields())
        sys.path.insert(0,str(a.prefix))
        from koinon import participant_instructions
        # The managed block only points at the installed guide. One reconciliation serves a
        # fresh and a repeated installation alike: it writes only the file the agent reads,
        # keeps a block the user edited, and records what Koinon wrote.
        saved = configuration.config.get(participant_instructions.RECORD_KEY) or {}
        blocks = dict(saved.get('blocks', {}))
        reports = {}
        for agent, home, chosen in (('codex', a.codex_home, a.configure_codex),
                                    ('deepseek', a.dsh_home, a.configure_deepseek)):
            if chosen:
                reports[agent], record = participant_instructions.reconcile(
                    home, a.prefix, agent, blocks.get(agent), replace=a.replace_guidance)
                if record is not None:
                    blocks[agent] = record
        if reports:
            configuration.merge({participant_instructions.RECORD_KEY: dict(version=1, blocks=blocks)})
        print('Installed runtime:',a.prefix)
        print('Configured state directory (may not exist until service initialization):',a.state_dir)
        for agent, label in (('codex', 'Codex'), ('deepseek', 'DeepSeek')):
            if agent in reports:
                target = next(entry['path'] for entry in reports[agent] if entry['role'] == 'target')
                print(f'Managed {label} guidance:', target)
                print(f'{label} guidance reconciliation:', json.dumps(reports[agent]))
        print('Claude status line:', json.dumps(claude_status_line(a, configuration)))
        print('Claude guidance:', json.dumps(claude_guidance_setup(a, configuration)))
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
    print('Configured state directory (may not exist until service initialization):',a.state_dir)
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
        from koinon import durable_state
        temporary = isinstance(exc, (durable_state.StateReadBusyError, subprocess.SubprocessError))
        print(json.dumps(dict(ok=False, code=getattr(exc, 'code', 'installation_incomplete'),
                              error=str(exc), paths=getattr(exc, 'paths', ()),
                              recovery='Preserve existing state and retry after correcting the reported condition.')))
        raise SystemExit(getattr(exc, 'exit_status', 75 if temporary else 78)) from None
