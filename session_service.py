#!/usr/bin/env python3
"""Owned native-session runner and explicit recovery; legacy session CLI is unchanged."""
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
import asyncio
import json
import os
import shlex
from pathlib import Path
import sys
import stat

from koinon import durable_state
from koinon import generation_stop
from koinon import notification_health
from koinon.peer_transport import control_exchange
from koinon import platform_support
from koinon import session_service_artifacts as artifacts
from koinon import session_service_config as configuration
from koinon import session_supervisor
from koinon.session_supervisor_state import Records, StateError, alive_state
from koinon.work_policy import absolute_path


class ServiceError(ValueError):
    def __init__(self, code='session_configuration_failure', *, paths=()):
        if code not in session_supervisor.STATUSES:
            raise ValueError('invalid session service error code')
        self.code, self.paths = code, tuple(str(path) for path in paths)
        super().__init__(code)


class Selection:
    def __init__(self, prefix, home, *, backend=None, python=None, removing=False, upgrading=False):
        self.prefix, self.home = absolute_path(str(prefix)), absolute_path(str(home))
        from koinon import runtime_names
        from koinon import upgrade_exclusion
        self.upgrade = upgrade_exclusion.read(self.prefix) if upgrading else None
        installed = (self.upgrade['documents']['installation'] if self.upgrade is not None
                     else runtime_names.install_config(self.prefix))
        if installed.get('installation_state') == 'removing' and not removing:
            raise ServiceError(paths=(self.prefix / 'install.json',))
        self.record = artifacts.load(self.home)
        removal = self.record and self.record['state'] == 'removing' and removing
        if removal:
            self.record = artifacts.verify_removing(self.record)
        if (self.record is None or self.record['state'] != 'installed'
                or self.record['prefix'] != str(self.prefix)
                or self.record['python'] != (python or sys.executable)
                or backend is not None and self.record['backend'] != backend):
            raise ServiceError(paths=(self.home / 'native-service.json',))
        if not removal:
            artifacts.verify_owned(self.record, upgrading=self.upgrade is not None)
        if self.upgrade is not None and not any(
                item['kind'] == 'session' and item['selection'] == self.record
                for item in self.upgrade['documents']['components']['items']):
            raise ServiceError(paths=(self.home / 'native-service.json',))
        config, registration = artifacts.inputs(self.record, upgrading=self.upgrade is not None)
        self.registration = registration
        self.commands = configuration.commands(self.prefix, self.record['python'], self.home, config, registration)
        self.backend = self.record['backend']
        installation = configuration.fingerprint([str(self.prefix), str(self.home), self.record['session_key']])
        self.records = Records(self.home, installation, configuration.fingerprint(self.record))

    def validate_programs(self):
        python = Path(self.record['python'])
        if not python.is_file() or not os.access(python, os.X_OK):
            raise ServiceError(paths=(python,))
        for name in ('session_service.py', 'bridge.py', 'notify.py'):
            path = self.prefix / name
            info = path.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
                raise ServiceError(paths=(path,))
        # Refuse old nonprivate lock evidence with a concrete operator diagnostic.
        # Neither unlink nor permission repair is part of starting the runner.
        for name in ('supervisor.lock',):
            path = self.home / name
            try:
                info = path.lstat()
            except FileNotFoundError:
                continue
            try:
                durable_state.validate(info)
            except ValueError as exc:
                raise ServiceError(paths=(path,)) from exc

    def recovery(self):
        raw = durable_state.read(self.records.recovery_path)
        if raw is None:
            return None
        # Validate every field, including mandatory basis, before exposing it.
        retained = raw.get('owner')
        if not isinstance(retained, dict) or not self.records.spawn_recovered(retained):
            raise StateError('invalid_session_state')
        return dict(basis=raw['basis'], generation=raw['generation'], asserted_by_uid=raw['asserted_by_uid'])


def pair_ready(selection, owner):
    """Read-only identity join; a saved running phase alone is not readiness."""
    if owner['phase'] != 'running' or owner['spawn_pending'] is not None or alive_state(owner) != 'alive':
        return False
    values = {}
    for kind, captured in owner['children'].items():
        if captured is None or captured['generation'] is None or alive_state(captured) != 'alive':
            return False
        root = selection.home if kind == 'bridge' else selection.home / 'notifier'
        try:
            reply, pid = asyncio.run(control_exchange(root, dict(op='status'), timeout=1))
        except (OSError, ValueError, TimeoutError):
            return False
        value = reply.get('result')
        if (reply.get('ok') is not True or not isinstance(value, dict)
                or type(pid) is not int or pid != captured['pid']
                or type(value.get('pid')) is not int or value['pid'] != pid
                or value.get('generation') != captured['generation']
                or not isinstance(value.get('control_capabilities'), list)
                or generation_stop.CAPABILITY not in value['control_capabilities']):
            return False
        values[kind] = value
    ready = notification_health.verify_owner(selection.home)
    bridge, notifier = owner['children']['bridge'], owner['children']['notifier']
    return (ready is not None and values['bridge'].get('database_status') == 'ready'
            and ready['owner'] == notifier['generation'] and ready['notifier_pid'] == notifier['pid']
            and platform_support.same_process(ready['proc_start'], notifier['proc_start'])
            and ready['bridge_pid'] == bridge['pid']
            and values['notifier'].get('bridge_generation') == bridge['generation']
            and values['notifier'].get('lifecycle') == 'running'
            and all(alive_state(value) == 'alive' for value in (owner, bridge, notifier))
            and selection.records.read() == owner)


def status(selection):
    owner = selection.records.read()
    recovery = selection.recovery()
    if owner is not None and owner['configuration'] != selection.records.configuration:
        raise StateError('session_ownership_unknown')
    result = dict(status='unknown', basis='recorded_state', backend=selection.backend,
                  owner=owner, recovery=recovery)
    if owner is None:
        result['status'] = 'unobserved'
        return result
    if pair_ready(selection, owner):
        result.update(status='running', basis='live_pair_identity')
    else:
        state = alive_state(owner)
        children = [alive_state(child) for child in owner['children'].values() if child is not None]
        if state == 'dead' and all(child == 'dead' for child in children) and owner['spawn_pending'] is None:
            result.update(status='stopped', basis='kernel_process_start')
        else:
            result.update(status='unresolved' if owner['spawn_pending'] is not None else 'unavailable',
                          basis='incomplete_observation')
    if selection.records.read() != owner:
        result.update(status='unknown', basis='changed_during_observation')
    return result


def stop_owned(selection):
    owner = selection.records.request_stop()
    selection.records.wait_stopped(owner)
    return dict(status='stopped', basis='kernel_process_start', generation=owner['generation'])


def execute(action, selection, *, generation=None, assertion=False):
    if action == 'run':
        if selection.upgrade is not None and selection.upgrade['phase']['step'] < 10:
            raise ServiceError(paths=(selection.prefix / 'install.json',))
        selection.validate_programs()
        return session_supervisor.run(selection.records, selection.commands, selection.backend)
    if action in ('ensure', 'status', 'deactivate'):
        from koinon import session_service_manager
        result = {'ensure': session_service_manager.ensure, 'status': session_service_manager.status,
                  'deactivate': session_service_manager.deactivate}[action](selection)
        if action in ('ensure', 'status'):
            result = dict(result, name=selection.registration['name'], state_dir=str(selection.home),
                          inbox_command=shlex.join([selection.record['python'],
                              str(selection.prefix / 'bridge.py'), '--state-dir', str(selection.home), 'inbox']))
        return result
    if action == 'stop':
        from koinon import session_service_manager
        return session_service_manager.stop(selection)
    if action == 'retry':
        selection.recovery()  # Refuse malformed provenance before granting retry.
        selection.records.retry()
        return dict(status='retry_authorized', basis='explicit_request', recovery=selection.recovery())
    if action == 'recover-spawn':
        selection.records.recover_spawn(generation, assert_no_unrecorded_child=assertion)
        return dict(status='operator_assertion_recorded', recovery=selection.recovery())
    raise ServiceError()


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('ensure', 'run', 'status', 'stop', 'retry', 'recover-spawn', 'deactivate'))
    parser.add_argument('--prefix', required=True)
    parser.add_argument('--state-dir', required=True)
    parser.add_argument('--backend', choices=configuration.BACKENDS, required=True)
    parser.add_argument('--generation')
    parser.add_argument('--assert-no-unrecorded-child', action='store_true')
    args = parser.parse_args(argv)
    try:
        if args.action != 'recover-spawn' and (args.generation is not None or args.assert_no_unrecorded_child):
            raise ServiceError()
        selection = Selection(args.prefix, args.state_dir, backend=args.backend,
                              upgrading=args.action in ('run', 'status', 'stop'))
        result = execute(args.action, selection, generation=args.generation, assertion=args.assert_no_unrecorded_child)
        if type(result) is int:
            return result
        print(json.dumps(result), flush=True)
        if result.get('status') == 'refused':
            return result['exit_status']
        if args.action == 'ensure' and result.get('status') == 'manual_required':
            return 0
        return 75 if args.action in ('ensure', 'status') and result.get('status') != 'running' else 0
    except (OSError, ValueError) as exc:
        code = ('session_temporary_failure' if isinstance(exc, durable_state.StateReadBusyError)
                else getattr(exc, 'code', 'session_configuration_failure'))
        if code == 'installation_upgrading':
            print(json.dumps(dict(status='unavailable', code=code, exit_status=78,
                                  paths=list(getattr(exc, 'paths', ())),
                                  recovery='use upgrade status or resume; do not repair or reinstall')), flush=True)
            return platform_support.managed_service_exit(args.backend, 78) if args.action == 'run' else 78
        if code not in session_supervisor.STATUSES:
            code = 'session_configuration_failure'
        exit_status = session_supervisor.STATUSES[code]
        print(json.dumps(dict(status='unavailable', code=code, exit_status=exit_status,
                              error=str(exc), paths=list(getattr(exc, 'paths', ())),
                              recovery='preserve evidence; inspect the reported selection or lock before explicit reconciliation')),
              flush=True)
        return platform_support.managed_service_exit(args.backend, exit_status) if args.action == 'run' else exit_status


if __name__ == '__main__':
    raise SystemExit(main())
