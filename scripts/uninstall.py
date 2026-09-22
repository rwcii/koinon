#!/usr/bin/env python3
"""Stop installed sessions, remove owned services/guidance, and preserve inbox data."""
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
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
from install import FILES, check_owned_unit, unit_targets_prefix
from koinon import platform_support
from koinon import runtime_names
from koinon import install_state
from koinon import work_guidance
from koinon import component_remove
import memory_service
import session_service
from koinon import session_service_artifacts
from koinon import uninstall_finalize


def removal_preflight(prefix, config):
    """Verify retained component selections before any removal mutation."""
    native_sessions = []
    for record in config.get('memory_services', {}).get('repositories', {}).values():
        try:
            memory_service.Selection(prefix, record['common_directory'], removing=True)
        except (OSError, ValueError, KeyError) as exc:
            raise ValueError('cannot verify retained memory selection: '
                             + str(record.get('artifact') or record.get('service_directory'))) from exc
    state_root = Path(config.get('state_root') or runtime_names.default_state_root())
    sessions = state_root / 'sessions'
    registrations = []
    if runtime_names.present(sessions):
        if sessions.is_symlink() or not sessions.is_dir():
            raise ValueError(f'unsafe session inventory: {sessions}')
        for home in sessions.iterdir():
            if home.is_symlink():
                raise ValueError(f'unsafe session inventory entry: {home}')
            if not home.is_dir():
                continue
            native = home / 'native-service.json'
            if runtime_names.present(native):
                try:
                    record = session_service_artifacts.load(home)
                    session_service.Selection(prefix, home, removing=True)
                except (OSError, ValueError, KeyError) as exc:
                    raise ValueError('cannot verify retained native session: ' + str(native)) from exc
                native_sessions.append(home)
            registration = home / 'session.json'
            if runtime_names.present(registration):
                if registration.is_symlink() or not registration.is_file():
                    raise ValueError(f'unsafe session registration: {registration}')
                value = json.loads(registration.read_text())
                if not isinstance(value, dict) or not isinstance(value.get('thread'), str) or not value['thread']:
                    raise ValueError(f'invalid session registration: {registration}')
                if home not in native_sessions:
                    archived = session_service_artifacts.archived_removal(prefix, home, config, value)
                    if archived is None:
                        registrations.append(value['thread'])
    return registrations, native_sessions


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--prefix',type=Path)
    a=p.parse_args()
    prefix=(a.prefix or runtime_names.default_prefix()).expanduser().resolve()
    runtime_names.install_config(prefix)  # Refuse malformed evidence before locking.
    with install_state.locked(prefix) as state:
        uninstall(prefix, state)


def uninstall(prefix, state):
    config_path=prefix/'install.json'
    config=state.config
    registrations, native_sessions = removal_preflight(prefix, config)
    unit_dir=Path(config.get('unit_dir',str(Path.home()/'.config/systemd/user')))
    state_root=Path(config.get('state_root') or runtime_names.default_state_root())
    candidates = set(runtime_names.service_names()) | set(runtime_names.service_names(legacy=True))
    for stem in ('koinon', 'codex-peer'):
        candidates.update(path.name for path in unit_dir.glob(f'{stem}-session-*.service')
                          if re.fullmatch(r'(?:koinon|codex-peer)-session-[a-f0-9]{16}\.service', path.name))
    owned = []
    for name in sorted(candidates):
        path = unit_dir / name
        if not runtime_names.present(path):
            continue
        # Other installations can share the unit directory. Remove only a unit
        # whose marker and executable both identify this installation.
        if path.is_symlink() or not path.is_file():
            raise ValueError(f'refusing unsafe service file: {path}')
        if not unit_targets_prefix(path, prefix):
            continue
        # An apparent unit for this prefix with a missing marker is a refusal,
        # not a reason to delete its executable and orphan the enabled unit.
        check_owned_unit(path, prefix)
        owned.append(name)
    # All retained component and legacy-unit evidence has passed preflight.
    if state.config and state.config.get('installation_state') != 'removing':
        state.merge(dict(installation_state='removing'))
    for home in native_sessions:
        component_remove.remove_session(prefix, home)
    for key in list(state.config.get('memory_services', dict(repositories={}))['repositories']):
        component_remove.remove_memory(prefix, state, key)
    # Disable/remove each work rule through the same crash-recoverable path.
    for key in list(state.config.get('work_items', {'rules': {}})['rules']):
        work_guidance.remove_locked(prefix, state, key)
    if config:
        for thread in registrations:
            subprocess.run([sys.executable,str(prefix/'session.py'),'stop','--thread',thread],check=True)
    existing=[name for name in owned if (unit_dir/name).exists()]
    if existing:
        platform_support.user_service_manager('disable', existing, check=True)
        for name in existing:
            (unit_dir/name).unlink()
        platform_support.user_service_manager('reload', check=True)
    if config:
        sys.path.insert(0,str(prefix))
        from koinon.participant_instructions import update
        # Remove the managed section for every participant this installation
        # configured, not just Codex: a harness section left behind would keep
        # telling sessions to register against a runtime that is gone. Older
        # installations recorded no participant list and were Codex-only.
        homes = {'codex': config.get('codex_home'), 'deepseek': config.get('dsh_home')}
        for agent in config.get('participants', ['codex']):
            home = homes.get(agent)
            if home:
                update(Path(home),prefix,remove=True,agent=agent)
    for home in native_sessions:
        with session_service_artifacts.locked(home / 'lifecycle.lock'), session_service_artifacts.locked(home / 'registration.lock'):
            session_service_artifacts.archive_removed(session_service_artifacts.load(home))
    uninstall_finalize.prepare(prefix, FILES)
    print('If final file cleanup is interrupted, resume with:',
          shlex.join([sys.executable, str(prefix / uninstall_finalize.RECOVERY), '--prefix', str(prefix)]), flush=True)
    uninstall_finalize.finish(prefix, locked=True, names=FILES)
    print('Owned services, managed guidance and runtime removed; inbox state preserved.')


if __name__ == '__main__':
    try:
        main()
    except runtime_names.NameConflict as exc:
        print(json.dumps(dict(ok=False, code=exc.code, paths=exc.paths, error=str(exc))))
        raise SystemExit(75 if exc.code == 'configuration_busy' else
                         platform_support.CONFIGURATION_EXIT_STATUS) from None
    except work_guidance.GuidanceError as exc:
        print(json.dumps(dict(ok=False, code=exc.code, path=exc.path, error=str(exc))))
        raise SystemExit(platform_support.CONFIGURATION_EXIT_STATUS) from None
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        print(json.dumps(dict(ok=False, code=getattr(exc, 'code', 'removal_incomplete'),
                              error=str(exc), paths=getattr(exc, 'paths', ()),
                              recovery='Preserve state and retry uninstall; use the printed standalone command if runtime cleanup began.')))
        raise SystemExit(getattr(exc, 'exit_status', 75 if isinstance(exc, (OSError, subprocess.SubprocessError)) else 78)) from None
