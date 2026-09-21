"""Staged portable owner for one bridge/notifier pair; no manager activation."""
import asyncio
from contextlib import contextmanager
import json
import os
import signal
import subprocess
import threading
import time

import durable_state
import generation_stop
from inbox_schema import hex_value
import notification_health
from peer_transport import control_exchange
import platform_support
import session_observation
import session_endpoints
import session_socket_handoff
from session_supervisor_state import Records, StateError, alive_state, state_lock

START_TIMEOUT = 20
STOP_TIMEOUT = 20
STATUSES = {code: 78 for code in (
    'invalid_session_state', 'session_ownership_unknown', 'session_shutdown_unconfirmed',
    'session_configuration_failure', 'session_stop_in_use', 'session_supervisor_in_use')}
STATUSES.update(session_software_failure=70, session_temporary_failure=75)


def child_failure(status):
    return StateError({70: 'session_software_failure', 78: 'session_configuration_failure'}.get(
        status, 'session_temporary_failure'))


@contextmanager
def diagnostics(records):
    fd = os.open(records.directory / 'session-supervisor-diagnostic.json',
                 os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, 0o600)
    try:
        durable_state.validate(os.fstat(fd))
        yield fd
    finally:
        os.close(fd)


def diagnose(fd, owner, recording_error):
    data = json.dumps(dict(code=owner['primary_code'], shutdown_code=owner['shutdown_code'],
                           exit_status=owner['exit_status'], recording_error=type(recording_error).__name__)).encode()
    os.lseek(fd, 0, os.SEEK_SET)
    os.ftruncate(fd, 0)
    while data:
        count = os.write(fd, data)
        if count <= 0:
            raise OSError('diagnostic publication made no progress')
        data = data[count:]
    platform_support.sync_state_file(fd)


class Runner:
    def __init__(self, records, commands, stopped):
        if set(commands) != {'bridge', 'notifier'}:
            raise StateError('session_configuration_failure')
        self.records, self.commands, self.stopped = records, commands, stopped
        self.owner = records.new_owner()
        self.children = {}
        self.bound_controls = {}

    def stopping(self):
        return self.stopped.is_set() or self.records.stop_requested(self.owner['generation'])

    def publish(self, phase):
        self.owner['phase'] = phase
        self.records.publish(self.owner)

    def check_children(self):
        exits = [child.poll() for child in self.children.values()]
        for status in exits:
            if status in platform_support.PERMANENT_EXIT_STATUSES:
                raise child_failure(status)
        if any(status is not None for status in exits):
            raise child_failure(next(status for status in exits if status is not None))

    def probe(self, kind):
        captured = self.owner['children'][kind]
        root = self.records.directory if kind == 'bridge' else self.records.directory / 'notifier'
        try:
            before = session_endpoints.capture(root)
            reply, pid = asyncio.run(control_exchange(root, dict(op='status'), timeout=1))
        except (OSError, ValueError, TimeoutError):
            return False
        value = reply.get('result')
        if reply.get('ok') is not True or not isinstance(value, dict):
            return False
        if (type(pid) is not int or pid != captured['pid'] or type(value.get('pid')) is not int
                or value['pid'] != captured['pid'] or alive_state(captured) != 'alive'
                or not hex_value(value.get('generation'), 32)):
            raise StateError('session_ownership_unknown')
        capabilities = value.get('control_capabilities')
        if not isinstance(capabilities, list) or generation_stop.CAPABILITY not in capabilities:
            raise StateError('session_configuration_failure')
        if captured['generation'] is not None and captured['generation'] != value['generation']:
            raise StateError('session_ownership_unknown')
        if session_endpoints.capture(root) != before or (
                captured.get('control_endpoint') is not None and captured['control_endpoint'] != before):
            raise StateError('session_ownership_unknown')
        captured['control_endpoint'] = before
        captured['generation'] = value['generation']
        self.publish('starting')
        if kind == 'bridge':
            return value.get('database_status') == 'ready'
        ready = notification_health.verify_owner(self.records.directory)
        return (ready is not None and ready['owner'] == captured['generation']
                and ready['notifier_pid'] == captured['pid']
                and platform_support.same_process(ready['proc_start'], captured['proc_start'])
                and ready['bridge_pid'] == self.owner['children']['bridge']['pid']
                and value.get('bridge_generation') == self.owner['children']['bridge']['generation']
                and value.get('lifecycle') == 'running')

    def shutdown(self):
        unconfirmed = False
        try:
            self.publish('stopping')
        except (OSError, ValueError):
            pass
        for kind, child in reversed(list(self.children.items())):
            if child.poll() is None:
                captured = self.owner['children'][kind]
                try:
                    if captured and captured['generation'] is not None:
                        root = self.records.directory if kind == 'bridge' else self.records.directory / 'notifier'
                        asyncio.run(generation_stop.request_stop(root, captured['generation'],
                                                                 captured['pid'], captured['proc_start']))
                    else:
                        # Only an unreaped Popen child before control readiness.
                        child.terminate()
                except (OSError, ValueError, TimeoutError):
                    pass  # No broad stop or signal fallback after a guarded refusal.
            try:
                child.wait(timeout=STOP_TIMEOUT)
                if self.owner['spawn_pending'] == kind:
                    self.owner['spawn_pending'] = None
            except subprocess.TimeoutExpired:
                unconfirmed = True
        for endpoint in self.bound_controls.values():
            endpoint.close()
        if unconfirmed:
            raise StateError('session_shutdown_unconfirmed')
        for kind in self.bound_controls:
            root = self.records.directory if kind == 'bridge' else self.records.directory / 'notifier'
            if not session_observation.endpoint_present(root):
                continue
            captured = self.owner['control_endpoints'][kind]
            if session_endpoints.capture(root) != captured:
                raise StateError('session_ownership_unknown')
            from pathlib import Path
            path = Path(captured['path'])
            path.unlink()
            platform_support.sync_state_directory(path.parent)

    def attempt(self):
        if (session_observation.endpoint_present(self.records.directory)
                or session_observation.endpoint_present(self.records.directory / 'notifier')
                or session_observation.lock_held(self.records.directory / 'notifier.lock')):
            raise StateError('session_ownership_unknown')
        self.publish('starting')
        primary = None
        try:
            for kind in ('bridge', 'notifier'):
                if self.stopping():
                    return
                root = self.records.directory if kind == 'bridge' else self.records.directory / 'notifier'
                self.owner['endpoint_pending'] = kind
                try:
                    self.publish('starting')
                except BaseException:
                    self.owner['endpoint_pending'] = None  # Bind has not been called.
                    raise
                endpoint, identity = session_socket_handoff.bind(root)
                self.bound_controls[kind] = endpoint
                self.owner['control_endpoints'][kind] = identity
                self.owner['endpoint_pending'] = None
                self.publish('starting')
                self.owner['spawn_pending'] = kind
                self.publish('starting')
                try:
                    command = [*self.commands[kind], '--supervisor-control-fd', str(endpoint.fileno()),
                               '--supervisor-generation', self.owner['generation']]
                    child = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                             pass_fds=(endpoint.fileno(),))
                    endpoint.close()
                except OSError:
                    # Popen reaps an exec-failed child before raising its OSError.
                    self.owner['spawn_pending'] = None
                    raise
                self.children[kind] = child
                if child.poll() is not None:
                    raise child_failure(child.returncode)
                self.owner['children'][kind] = dict(pid=child.pid, proc_start=platform_support.proc_start(child.pid),
                                                    generation=None)
                self.owner['spawn_pending'] = None
                self.publish('starting')
                deadline = time.monotonic() + START_TIMEOUT
                while not self.stopping():
                    self.check_children()
                    if self.probe(kind):
                        break
                    if time.monotonic() >= deadline:
                        raise StateError('session_temporary_failure')
                    self.stopped.wait(.1)
                else:
                    return
            self.check_children()
            self.publish('running')
            print(json.dumps(dict(status='running', generation=self.owner['generation'])), flush=True)
            while not self.stopping():
                self.check_children()
                self.stopped.wait(.2)
        except Exception as exc:
            primary = exc.code if isinstance(exc, StateError) else (
                'session_temporary_failure' if isinstance(exc, durable_state.StateReadBusyError) else
                    'session_configuration_failure' if isinstance(exc, (OSError, ValueError)) else 'session_software_failure')
            raise
        finally:
            try:
                self.shutdown()
            except StateError as exc:
                exc.primary_code = primary or exc.code
                raise


def run(records, commands, backend):
    """Run one attempt. Native managers own restart cadence; permanent exits persist."""
    if backend not in ('manual', 'systemd', 'launchd'):
        raise StateError('session_configuration_failure')
    stopped = threading.Event()
    previous = {}
    for signum in (signal.SIGTERM, signal.SIGINT):
        previous[signum] = signal.signal(signum, lambda *_: stopped.set())
    runner = None
    status = 0
    try:
        with state_lock(records.directory / 'supervisor.lock', 'session_supervisor_in_use'), diagnostics(records) as fd:
            if records.read(refusal=True) is not None:
                raise StateError('session_configuration_failure')
            old = records.read()
            if old is not None and (old.get('endpoint_pending') is not None
                                    or old['spawn_pending'] is not None and not records.spawn_recovered(old)
                                    or alive_state(old) != 'dead'
                                    or any(child is not None and alive_state(child) != 'dead'
                                           for child in old['children'].values())):
                raise StateError('session_ownership_unknown')
            if (old is not None and (old['exit_status'] in platform_support.PERMANENT_EXIT_STATUSES
                                    or old['spawn_pending'] is not None)
                    and not records.retry_requested(old['generation'])):
                raise StateError('session_configuration_failure')
            if old is not None:
                session_endpoints.recover(records, old)
            runner = Runner(records, commands, stopped)
            try:
                runner.attempt()
                runner.owner['exit_status'] = 0
                runner.publish('stopped')
            except Exception as exc:
                failure = exc if isinstance(exc, StateError) else StateError(
                    'session_temporary_failure' if isinstance(exc, durable_state.StateReadBusyError) else
                    'session_configuration_failure' if isinstance(exc, (OSError, ValueError)) else 'session_software_failure')
                status = STATUSES[failure.code]
                runner.owner.update(phase='failed', exit_status=status, primary_code=getattr(failure, 'primary_code', failure.code),
                                    shutdown_code='session_shutdown_unconfirmed'
                                    if failure.code == 'session_shutdown_unconfirmed' else None)
                try:
                    if status in platform_support.PERMANENT_EXIT_STATUSES:
                        records.publish(runner.owner, refusal=True)
                    records.publish(runner.owner)
                except (OSError, ValueError) as recording_error:
                    try:
                        diagnose(fd, runner.owner, recording_error)
                    except (OSError, ValueError):
                        pass
                raise failure
    except (OSError, ValueError) as exc:
        status = 75 if isinstance(exc, durable_state.StateReadBusyError) else STATUSES.get(getattr(exc, 'code', None), 78)
        print(json.dumps(dict(status='unavailable', exit_status=status,
                              code=getattr(exc, 'code', 'invalid_session_state'))), flush=True)
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)
    return platform_support.managed_service_exit(backend, status)
