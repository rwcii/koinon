#!/usr/bin/env python3
"""Staged repository-memory supervisor. Manager activation is a later integration."""
import argparse
import asyncio
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import threading
import time
import uuid

import durable_state
import install_state
import memory
import memory_service_artifacts
import memory_service_config
from participant_lock import file_lock, OwnershipError
from peer_transport import private_dir
import platform_support
import runtime_names
from work_policy import absolute_path

START_TIMEOUT = 20
STOP_TIMEOUT = 20
BACKOFF_INITIAL = 10
BACKOFF_MAX = 60
HEALTHY_RESET = 60
ERRORS = {
    'configuration_error': 78, 'supervisor_in_use': 78,
    'external_memory_service': 78, 'ownership_unknown': 78,
    'recorded_refusal': 78, 'shutdown_unconfirmed': 78,
    'memory_configuration_failure': 78, 'memory_software_failure': 70,
    'memory_temporary_failure': 75, 'internal_error': 70,
    'manager_observation_unknown': 75, 'manager_operation_failed': 75,
    'manager_ownership_conflict': 78,
}


class RunnerError(ValueError):
    def __init__(self, code, *, paths=()):
        self.paths = tuple(paths)
        self.code = code
        self.exit_status = ERRORS[code]
        self.primary_code = code
        self.shutdown_code = None
        super().__init__(code)


def classify(exc):
    if isinstance(exc, durable_state.StateReadBusyError):
        return RunnerError('memory_temporary_failure')
    return exc if isinstance(exc, RunnerError) else RunnerError(
        'configuration_error' if isinstance(exc, (OSError, ValueError)) else 'internal_error')


@contextmanager
def diagnostic_sink(selection):
    """Retain one bounded private failure even if later directory writes fail."""
    fd = os.open(selection.home / 'supervisor-diagnostic.json',
                 os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK, 0o600)
    try:
        durable_state.validate(os.fstat(fd))
        yield fd
    finally:
        os.close(fd)


def diagnose(fd, failure, recording_error):
    data = json.dumps(dict(code=failure.code, primary_code=failure.primary_code,
                           shutdown_code=failure.shutdown_code,
                           recording_error=type(recording_error).__name__)).encode()
    os.lseek(fd, 0, os.SEEK_SET)
    os.ftruncate(fd, 0)
    while data:
        written = os.write(fd, data)
        if written <= 0:
            raise OSError('diagnostic write made no progress')
        data = data[written:]
    platform_support.sync_state_file(fd)


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


@contextmanager
def configuration_boundary():
    try:
        yield
    except RunnerError:
        raise
    except durable_state.StateReadBusyError as exc:
        raise RunnerError('memory_temporary_failure') from exc
    except (OSError, ValueError) as exc:
        raise RunnerError('configuration_error', paths=getattr(exc, 'paths', ())) from exc


class Selection:
    def __init__(self, prefix, repository, *, state_root=None, backend=None, removing=False):
        with configuration_boundary():
            self.prefix = absolute_path(str(prefix))
            config = runtime_names.install_config(self.prefix)
            if config.get('installation_state') == 'removing' and not removing:
                raise RunnerError('configuration_error', paths=(self.prefix / 'install.json',))
            self.key = memory.repo_identity(repository)
            self.record = config.get('memory_services', {}).get('repositories', {}).get(self.key)
            removal = self.record and self.record['state'] == 'removing' and removing
            if not self.record or self.record['state'] != 'installed' and not removal:
                raise RunnerError('configuration_error')
            if removal:
                self.record = memory_service_artifacts.verify_removing(self.prefix, sys.executable, self.record)
            memory_service_config.verify_selection(self.record)
            self.backend = self.record['backend']
            if (state_root is not None and str(absolute_path(str(state_root))) != self.record['state_root']
                    or backend is not None and backend != self.backend):
                raise RunnerError('configuration_error')
            if self.backend != 'manual' and not removal:
                memory_service_artifacts.verify_owned(self.prefix, sys.executable, self.record)
            self.home = Path(self.record['service_directory'])
            self.installation = fingerprint([str(self.prefix), self.key, str(self.home)])
            self.configuration = fingerprint([self.installation, sys.executable, self.record])
            self.owner_path = self.home / 'supervisor.json'
            self.refusal_path = self.home / 'supervisor-refusal.json'
            self.stop_path = self.home / 'supervisor-stop.json'

    def command(self):
        return [sys.executable, str(self.prefix / 'memory.py'), '--state-dir',
                self.record['state_root'], '--repo-path', self.record['common_directory'], 'serve']

    def start_command(self):
        return platform_support.memory_service_command(
            self.prefix, sys.executable, self.key, self.record) + ['--foreground']


def _hex(value, length):
    return isinstance(value, str) and len(value) == length and all(c in '0123456789abcdef' for c in value)


def _process(value):
    return (isinstance(value, dict) and type(value.get('pid')) is int and value['pid'] > 0
            and isinstance(value.get('proc_start'), str) and bool(value['proc_start'].strip()))


def read_record(selection, path, *, refusal=False):
    with configuration_boundary():
        value = durable_state.read(path)
        if value is None:
            return None
        fields = {'version', 'installation', 'configuration', 'generation', 'pid', 'proc_start',
                  'phase', 'child', 'exit_status', 'primary_code', 'shutdown_code'}
        if (set(value) != fields or type(value['version']) is not int or value['version'] != 1
                or value['installation'] != selection.installation
                or not _hex(value['configuration'], 64) or not _hex(value['generation'], 32)
                or not _process(value)
                or value['phase'] not in ('starting', 'running', 'stopping', 'backoff', 'stopped', 'failed')
                or (value['exit_status'] is not None and
                    (type(value['exit_status']) is not int or value['exit_status'] not in (0, 70, 75, 78)))):
            raise RunnerError('configuration_error')
        if (value['primary_code'] is not None and
                (not isinstance(value['primary_code'], str) or value['primary_code'] not in ERRORS)
                or value['shutdown_code'] not in (None, 'shutdown_unconfirmed')):
            raise RunnerError('configuration_error')
        child = value['child']
        if child is not None and (not _process(child) or set(child) != {'pid', 'proc_start', 'generation'}
                                  or child['generation'] is not None and not _hex(child['generation'], 32)):
            raise RunnerError('configuration_error')
        if (value['phase'] == 'running' and (not child or not child['generation'])
                or value['phase'] == 'stopped' and value['exit_status'] != 0
                or value['phase'] == 'failed' and value['exit_status'] not in (70, 75, 78)):
            raise RunnerError('configuration_error')
        if refusal and (value['phase'] != 'failed' or value['exit_status'] not in (70, 78)):
            raise RunnerError('configuration_error')
        return value


def verify_memory(selection):
    try:
        return asyncio.run(memory.verify_running(selection.home, selection.key))
    except memory.MemoryError_ as exc:
        status = memory.memory_error_exit_status(exc.code)
        code = {70: 'memory_software_failure', 75: 'memory_temporary_failure'}.get(
            status, 'memory_configuration_failure')
        raise RunnerError(code) from exc


def process_state(record):
    return platform_support.process_state(record['pid'], record['proc_start'])


def observation(selection):
    """Read-only evidence. Absent or incomplete supervisor state is unavailable."""
    refusal = read_record(selection, selection.refusal_path, refusal=True)
    if refusal:
        return dict(status='refused', running=False, exit_status=refusal['exit_status'],
                    configuration_matches=refusal['configuration'] == selection.configuration,
                    primary_code=refusal['primary_code'], shutdown_code=refusal['shutdown_code'])
    owner = read_record(selection, selection.owner_path)
    if not owner:
        existing = verify_memory(selection)
        return dict(status='externally_managed' if existing else 'unavailable', running=False)
    if owner['configuration'] != selection.configuration:
        return dict(status='unavailable', running=False)
    if owner['phase'] == 'stopped' and process_state(owner) == 'dead':
        return dict(status='stopped', running=False)
    if process_state(owner) != 'alive':
        return dict(status='unavailable', running=False)
    if owner['phase'] != 'running' or not owner['child']:
        return dict(status=owner['phase'], running=False)
    live = verify_memory(selection)
    child = owner['child']
    after = read_record(selection, selection.owner_path)
    if (not live or process_state(child) != 'alive' or live['pid'] != child['pid'] or live['generation'] != child['generation']
            or after != owner):
        return dict(status='unavailable', running=False)
    return dict(status='running', running=True, generation=owner['generation'],
                child_generation=child['generation'])


def manager_observation(selection):
    """Corroborate loaded identity against exact registered bytes and arguments."""
    with configuration_boundary():
        if selection.backend == 'systemd':
            result = platform_support.systemd_service_observation(
                memory_service_config.artifact_name(selection.key, selection.backend))
        elif selection.backend == 'launchd':
            domain = selection.record.get('manager_domain')
            if domain is None:
                raise RunnerError('configuration_error')
            result = platform_support.launchd_service_observation(
                domain, memory_service_config.artifact_name(selection.key, selection.backend)[:-6])
        else:
            raise RunnerError('configuration_error')
        if result['status'] == 'observed':
            expected = platform_support.memory_service_command(
                selection.prefix, sys.executable, selection.key, selection.record)
            if selection.backend == 'systemd' and selection.record.get('template_version', 1) == 1:
                # D-Bus exposes stored command arguments before dollar expansion.
                # Compare the exact known template representation, never normalize
                # arbitrary manager output into a matching command.
                expected = [value.replace('$', '$$') for value in expected]
            if (result['argv'] != expected or result['executable'] != expected[0]):
                raise RunnerError('manager_ownership_conflict')
            memory_service_artifacts.verify_loaded(selection.prefix, sys.executable, selection.record,
                                                   result['artifact'])
        return result


def managed_status(selection):
    portable = observation(selection)
    if portable['status'] == 'refused':
        return portable
    observed = manager_observation(selection)
    if observed['status'] != 'observed':
        return dict(status='unavailable', running=False, manager=observed['status'])
    owner = read_record(selection, selection.owner_path)
    if (portable['running'] and owner and owner['generation'] == portable['generation']
            and observed['pid'] == owner['pid'] and process_state(owner) == 'alive'):
        return dict(portable, managed=True, backend=selection.backend)
    return dict(status='unavailable', running=False, manager=observed['active_state'])


def ensure_managed(selection):
    """Activate only a saved owned selection, then prove manager and child readiness."""
    portable = observation(selection)
    if portable['status'] == 'refused':
        return portable
    with configuration_boundary():
        domain = selection.record.get('manager_domain')
        if not platform_support.memory_manager_available(selection.backend, domain):
            return dict(status='manual_required', running=False,
                        start_command=shlex.join(selection.start_command()))
        private_dir(selection.home)
        try:
            with install_state.locked(selection.prefix) as installed, file_lock(
                    selection.home / 'manager.lock', 'manager_busy', None):
                saved = installed.config.get('memory_services', {}).get('repositories', {}).get(selection.key)
                if saved != selection.record:
                    raise RunnerError('configuration_error')
                observed = manager_observation(selection)
                if observed['status'] == 'unknown':
                    raise RunnerError('manager_observation_unknown')
                portable = observation(selection)
                if portable['status'] == 'refused':
                    return portable
                if portable['running']:
                    owner = read_record(selection, selection.owner_path)
                    if (observed['status'] == 'observed' and owner
                            and owner['generation'] == portable['generation']
                            and observed['pid'] == owner['pid']):
                        return dict(portable, managed=True, backend=selection.backend)
                    raise RunnerError('external_memory_service')
                if portable['status'] == 'externally_managed':
                    raise RunnerError('external_memory_service')
                operation = (('register' if selection.backend == 'systemd' else 'activate')
                             if observed['status'] == 'absent' else
                             ('activate' if selection.backend == 'systemd' else 'restart')
                             if observed['pid'] == 0 else None)
                if operation:
                    # Recheck immediately before mutation. This detects changes,
                    # but does not claim to lock the manager's global state.
                    if manager_observation(selection) != observed:
                        raise RunnerError('manager_observation_unknown')
                    try:
                        memory_service_artifacts.verify_owned(selection.prefix, sys.executable, selection.record)
                        platform_support.memory_manager_action(selection.record, operation)
                        if selection.backend == 'systemd':
                            # An inert loader link is verified before enablement can
                            # create future-login activation, and again before start.
                            registered = manager_observation(selection)
                            if registered['status'] != 'observed':
                                raise RunnerError('manager_observation_unknown')
                            if operation == 'register':
                                if manager_observation(selection) != registered:
                                    raise RunnerError('manager_observation_unknown')
                                platform_support.memory_manager_action(selection.record, 'activate')
                                if manager_observation(selection) != registered:
                                    raise RunnerError('manager_observation_unknown')
                            if registered['pid'] == 0:
                                if manager_observation(selection) != registered:
                                    raise RunnerError('manager_observation_unknown')
                                platform_support.memory_manager_action(selection.record, 'restart')
                    except (OSError, subprocess.SubprocessError) as exc:
                        raise RunnerError('manager_operation_failed') from exc
                deadline = time.monotonic() + START_TIMEOUT
                while True:
                    result = managed_status(selection)
                    if result['running'] or result['status'] == 'refused':
                        return result
                    if time.monotonic() >= deadline:
                        raise RunnerError('manager_observation_unknown')
                    time.sleep(.1)
        except OwnershipError as exc:
            raise RunnerError('manager_operation_failed') from exc


def child_failure(status, *, started=False):
    if status == 70:
        return RunnerError('memory_software_failure')
    if status == 78 or status == 0 and not started:
        return RunnerError('memory_configuration_failure')
    return RunnerError('memory_temporary_failure')


class StopRequest:
    """Generation-bound stop requests work during startup and retry backoff too."""
    def __init__(self, selection):
        self.selection = selection
        self.generation = None
        self.event = threading.Event()

    def set(self):
        self.event.set()

    def is_set(self):
        if self.event.is_set():
            return True
        with configuration_boundary():
            value = durable_state.read(self.selection.stop_path)
            if value is not None:
                if (set(value) != {'installation', 'generation'}
                        or value['installation'] != self.selection.installation
                        or not _hex(value['generation'], 32)):
                    raise RunnerError('configuration_error')
                if value['generation'] == self.generation:
                    self.event.set()
        return self.event.is_set()

    def wait(self, timeout):
        deadline = time.monotonic() + timeout
        while not self.is_set():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            self.event.wait(min(.1, remaining))
        return True


class Supervisor:
    def __init__(self, selection, stopped):
        self.selection, self.stopped = selection, stopped
        self.child = None
        self.healthy_since = None
        self.owner = dict(version=1, installation=selection.installation,
                          configuration=selection.configuration, generation=uuid.uuid4().hex,
                          pid=os.getpid(), proc_start=platform_support.proc_start(os.getpid()),
                          phase='starting', child=None, exit_status=None, primary_code=None, shutdown_code=None)

    def publish(self, phase, exit_status=None):
        self.owner.update(phase=phase, exit_status=exit_status)
        durable_state.publish(self.selection.owner_path, self.owner)

    def shutdown(self):
        child = self.child
        if child is None or child.poll() is not None:
            if child is not None:
                child.wait()
            return
        try:
            self.publish('stopping')
        except (OSError, ValueError):
            pass  # Still stop our directly owned process if diagnostics cannot be saved.
        captured = self.owner['child']
        try:
            if captured and captured['generation']:
                memory.stop_service(self.selection.home, self.selection.key, timeout=STOP_TIMEOUT,
                                    expected_generation=captured['generation'])
            else:
                child.terminate()  # Direct Popen ownership, never a PID from a record.
        except (OSError, ValueError):
            # Do not replace a refused generation-bound request with a broad stop.
            pass
        try:
            child.wait(timeout=STOP_TIMEOUT)
        except subprocess.TimeoutExpired as exc:
            # Do not force-kill memory during a drain; retain ownership evidence
            # and require recovery before a replacement can start.
            raise RunnerError('shutdown_unconfirmed') from exc

    def attempt(self):
        self.owner.update(child=None, primary_code=None, shutdown_code=None)
        self.healthy_since = None
        if verify_memory(self.selection):
            raise RunnerError('external_memory_service')
        self.publish('starting')
        primary = None
        try:
            self.child = subprocess.Popen(self.selection.command(), stdin=subprocess.DEVNULL,
                                          stdout=subprocess.DEVNULL)
            if self.child.poll() is not None:
                raise child_failure(self.child.returncode)
            marker = platform_support.proc_start(self.child.pid)
            self.owner['child'] = dict(pid=self.child.pid, proc_start=marker, generation=None)
            self.publish('starting')
            deadline = time.monotonic() + START_TIMEOUT
            while not self.stopped.is_set():
                status = self.child.poll()
                if status is not None:
                    raise child_failure(status)
                live = verify_memory(self.selection)
                if live:
                    if live['pid'] != self.child.pid:
                        raise RunnerError('external_memory_service')
                    memory_owner = memory.read_owner(self.selection.home)
                    if (live.get('schema') != memory.SCHEMA
                            or not {'memory_target_guard', 'work_items_v1', 'memory_record_format_2'}.issubset(live.get('capabilities', []))
                            or not memory_owner
                            or memory_owner.get('pid') != self.child.pid
                            or memory_owner.get('generation') != live['generation']
                            or not platform_support.same_process(marker, memory_owner.get('proc_start'))):
                        raise RunnerError('ownership_unknown')
                    self.owner['child']['generation'] = live['generation']
                    self.publish('running')
                    self.healthy_since = time.monotonic()
                    print(json.dumps(dict(status='running', running=True,
                                          generation=self.owner['generation'])), flush=True)
                    break
                if time.monotonic() >= deadline:
                    raise RunnerError('memory_temporary_failure')
                self.stopped.wait(.1)
            while not self.stopped.is_set() and self.child.poll() is None:
                self.stopped.wait(.2)
            if not self.stopped.is_set() and self.child.returncode != 0:
                raise child_failure(self.child.returncode, started=True)
        except Exception as exc:
            primary = classify(exc)
            raise
        finally:
            try:
                self.shutdown()
            except RunnerError as shutdown:
                # Exit uncertainty forbids another child. Preserve the triggering
                # failure as well; never hide it behind the shutdown diagnostic.
                shutdown.primary_code = primary.code if primary else shutdown.code
                shutdown.shutdown_code = shutdown.code
                raise


def run(selection, *, foreground=False):
    stopped = StopRequest(selection)
    previous = {}
    for signum in (signal.SIGTERM, signal.SIGINT):
        previous[signum] = signal.signal(signum, lambda *_: stopped.set())
    try:
        private_dir(selection.home)
        with file_lock(selection.home / 'supervisor.lock', 'supervisor_in_use', None), diagnostic_sink(selection) as diagnostics:
            if read_record(selection, selection.refusal_path, refusal=True):
                raise RunnerError('recorded_refusal')
            old = read_record(selection, selection.owner_path)
            if old and (process_state(old) != 'dead'
                        or old['child'] and process_state(old['child']) != 'dead'):
                raise RunnerError('ownership_unknown')
            supervisor = Supervisor(selection, stopped)
            stopped.generation = supervisor.owner['generation']
            delay = BACKOFF_INITIAL
            while True:
                try:
                    supervisor.attempt()
                    supervisor.publish('stopped', 0)
                    return 0
                except Exception as exc:
                    failure = classify(exc)
                    supervisor.owner.update(primary_code=failure.primary_code, shutdown_code=failure.shutdown_code)
                    status = failure.exit_status
                    if status in platform_support.PERMANENT_EXIT_STATUSES:
                        supervisor.owner.update(phase='failed', exit_status=status)
                        try:
                            durable_state.publish(selection.refusal_path, supervisor.owner)
                            durable_state.publish(selection.owner_path, supervisor.owner)
                        except (OSError, ValueError) as recording_error:
                            try:
                                diagnose(diagnostics, failure, recording_error)
                            except (OSError, ValueError):
                                pass  # Preserve the permanent exit even if both sinks fail.
                            print(json.dumps(dict(status='unavailable', running=False,
                                                  code=failure.code, refusal_recorded=False)), file=sys.stderr, flush=True)
                        raise failure
                    if not foreground or stopped.is_set():
                        supervisor.publish('failed', status)
                        raise failure
                    if (supervisor.healthy_since is not None
                            and time.monotonic() - supervisor.healthy_since >= HEALTHY_RESET):
                        delay = BACKOFF_INITIAL
                    supervisor.publish('backoff', status)
                    if stopped.wait(delay):
                        supervisor.publish('stopped', 0)
                        return 0
                    delay = min(BACKOFF_MAX, delay * 2)
    except OwnershipError as exc:
        raise RunnerError('supervisor_in_use' if exc.code == 'supervisor_in_use' else 'configuration_error') from exc
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def retry(selection):
    """Explicitly clear only this installation's recorded refusal while stopped."""
    with configuration_boundary():
        private_dir(selection.home)
        try:
            with file_lock(selection.home / 'supervisor.lock', 'supervisor_in_use', None):
                owner = read_record(selection, selection.owner_path)
                if owner and (process_state(owner) != 'dead'
                              or owner['child'] and process_state(owner['child']) != 'dead'):
                    raise RunnerError('ownership_unknown')
                refusal = read_record(selection, selection.refusal_path, refusal=True)
                if refusal:
                    selection.refusal_path.unlink()
                    platform_support.sync_state_directory(selection.home)
        except OwnershipError as exc:
            raise RunnerError('supervisor_in_use') from exc


def stop(selection):
    owner = read_record(selection, selection.owner_path)
    if not owner or owner['configuration'] != selection.configuration:
        raise RunnerError('ownership_unknown')
    child = owner['child']
    state = process_state(owner)
    if state == 'unknown':
        raise RunnerError('ownership_unknown')
    if state == 'alive':
        with configuration_boundary():
            try:
                with file_lock(selection.home / 'supervisor-stop.lock', 'stop_in_use', None):
                    if read_record(selection, selection.owner_path) != owner:
                        raise RunnerError('ownership_unknown')
                    durable_state.publish(selection.stop_path, dict(
                        installation=selection.installation, generation=owner['generation']))
            except OwnershipError as exc:
                raise RunnerError('ownership_unknown') from exc
    elif child and process_state(child) != 'dead':
        # An orphan child requires explicit recovery, not a stop of a successor.
        raise RunnerError('shutdown_unconfirmed')
    deadline = time.monotonic() + STOP_TIMEOUT
    while (process_state(owner) != 'dead'
           or child and process_state(child) != 'dead'):
        if time.monotonic() >= deadline:
            raise RunnerError('shutdown_unconfirmed')
        time.sleep(.1)
    return dict(status='stopped', running=False, generation=owner['generation'])


def deactivate_owned(selection):
    """Caller holds installation and manager locks; retain all artifact/data evidence."""
    observed = manager_observation(selection)
    if observed['status'] not in ('observed', 'absent'):
        raise RunnerError('manager_observation_unknown')
    owner = read_record(selection, selection.owner_path)
    if owner is None:
        if (runtime_names.present(platform_support.control_socket_path(selection.home))
                or observed['status'] == 'observed' and observed['pid'] != 0):
            raise RunnerError('ownership_unknown')
    else:
        if owner['configuration'] != selection.configuration:
            raise RunnerError('ownership_unknown')
        if process_state(owner) == 'alive' and (
                observed['status'] != 'observed' or observed['pid'] != owner['pid']):
            raise RunnerError('manager_ownership_conflict')
        stop(selection)
        after = read_record(selection, selection.owner_path)
        if (after is None or after['generation'] != owner['generation']
                or process_state(after) != 'dead'
                or after['child'] and process_state(after['child']) != 'dead'):
            raise RunnerError('shutdown_unconfirmed')
    stopped = manager_observation(selection)
    if (stopped['status'] not in ('observed', 'absent')
            or stopped['status'] == 'observed' and stopped['pid'] != 0
            or manager_observation(selection) != stopped):
        raise RunnerError('manager_observation_unknown')
    if stopped['status'] == 'observed':
        memory_service_artifacts.verify_owned(selection.prefix, sys.executable, selection.record)
        platform_support.memory_manager_deregister(selection.record)
    if manager_observation(selection)['status'] != 'absent':
        raise RunnerError('manager_observation_unknown')
    return dict(status='deactivated', running=False, retained=['selection', 'artifact', 'store', 'supervisor_evidence'])


def deactivate(selection):
    private_dir(selection.home)
    with install_state.locked(selection.prefix), file_lock(selection.home / 'manager.lock', 'manager_busy', None):
        selection = Selection(selection.prefix, selection.record['common_directory'], backend=selection.backend)
        return deactivate_owned(selection)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('ensure', 'run', 'status', 'stop', 'deactivate'))
    parser.add_argument('--prefix', type=Path, required=True)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--state-root', type=Path)
    parser.add_argument('--backend', choices=memory_service_config.BACKENDS)
    parser.add_argument('--foreground', action='store_true')
    parser.add_argument('--retry', action='store_true')
    args = parser.parse_args()
    if args.retry and args.action != 'ensure':
        parser.error('--retry requires ensure')
    status = 0
    backend = args.backend
    try:
        selection = Selection(args.prefix, args.repo, state_root=args.state_root, backend=args.backend)
        backend = selection.backend
        if args.action == 'run':
            status = run(selection, foreground=args.foreground or backend == 'manual')
        elif args.action == 'stop':
            print(json.dumps(stop(selection)))
        elif args.action == 'deactivate':
            print(json.dumps(deactivate(selection)))
        else:
            if args.retry:
                retry(selection)
            result = (ensure_managed(selection) if args.action == 'ensure' and selection.backend != 'manual'
                      else managed_status(selection) if args.action == 'status' and selection.backend != 'manual'
                      else observation(selection))
            if args.action == 'ensure' and not result['running']:
                if result['status'] == 'refused':
                    status = result['exit_status']
                elif result['status'] == 'externally_managed':
                    raise RunnerError('external_memory_service')
                else:
                    result = dict(status='manual_required', running=False,
                                  start_command=shlex.join(selection.start_command()))
            elif result['status'] == 'refused':
                status = result['exit_status']
            elif args.action == 'status' and not result['running']:
                status = 75
            print(json.dumps(result))
    except RunnerError as exc:
        status = exc.exit_status
        print(json.dumps(dict(ok=False, status='unavailable', running=False, code=exc.code,
                              exit_status=status, primary_code=exc.primary_code,
                              shutdown_code=exc.shutdown_code, paths=list(exc.paths))), flush=True)
    except durable_state.StateReadBusyError:
        status = 75
        print(json.dumps(dict(ok=False, status='unavailable', running=False,
                              code='memory_temporary_failure', exit_status=status)), flush=True)
    except (OSError, ValueError) as exc:
        status = 78
        print(json.dumps(dict(ok=False, status='unavailable', running=False,
                              code='configuration_error', exit_status=status)), flush=True)
    except Exception:
        status = 70
        print(json.dumps(dict(ok=False, status='unavailable', running=False,
                              code='internal_error', exit_status=status)), flush=True)
    if args.action == 'run' and not args.foreground:
        status = platform_support.managed_service_exit(backend, status)
    return status


if __name__ == '__main__':
    raise SystemExit(main())
