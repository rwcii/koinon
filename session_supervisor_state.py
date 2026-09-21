"""Private ownership and recovery records for one explicitly selected session runner.

These primitives neither select a manager nor start, signal, or adopt a process.
The caller holds the supervisor lock while publishing owner/refusal state.
"""
from pathlib import Path
from contextlib import contextmanager
import os
import stat
import time
import uuid

import durable_state
from inbox_schema import hex_value
from participant_lock import file_lock, OwnershipError
import platform_support
from work_policy import absolute_path

CODES = frozenset(('invalid_session_state', 'session_ownership_unknown',
                   'session_shutdown_unconfirmed', 'session_configuration_failure',
                   'session_software_failure', 'session_temporary_failure',
                   'session_stop_in_use', 'session_supervisor_in_use'))
PHASES = frozenset(('starting', 'running', 'stopping', 'stopped', 'failed'))
CHILDREN = ('bridge', 'notifier')


class StateError(ValueError):
    def __init__(self, code):
        if code not in CODES:
            raise ValueError('unknown session state error')
        self.code = code
        super().__init__(code)


def process(value):
    return (isinstance(value, dict) and type(value.get('pid')) is int and value['pid'] > 0
            and isinstance(value.get('proc_start'), str) and 0 < len(value['proc_start']) <= 128
            and not any(ord(char) < 32 or ord(char) == 127 for char in value['proc_start']))


def alive_state(value):
    return platform_support.process_state(value['pid'], value['proc_start'])


@contextmanager
def state_lock(path, code):
    if code not in CODES:
        raise StateError('invalid_session_state')
    try:
        with file_lock(path, code, None):
            yield
    except OwnershipError as exc:
        raise StateError(exc.code if exc.code in CODES else 'invalid_session_state') from exc


class Records:
    def __init__(self, directory, installation, configuration):
        if not hex_value(installation, 64) or not hex_value(configuration, 64):
            raise StateError('invalid_session_state')
        self.directory = absolute_path(str(directory))
        info = self.directory.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077):
            raise StateError('invalid_session_state')
        self.installation, self.configuration = installation, configuration
        self.owner_path = self.directory / 'session-supervisor.json'
        self.refusal_path = self.directory / 'session-supervisor-refusal.json'
        self.stop_path = self.directory / 'session-supervisor-stop.json'
        self.retry_path = self.directory / 'session-supervisor-retry.json'
        self.recovery_path = self.directory / 'session-supervisor-recovery.json'

    def validate(self, value, *, refusal=False):
        fields = {'version', 'installation', 'configuration', 'generation', 'pid', 'proc_start',
                  'phase', 'children', 'spawn_pending', 'exit_status', 'primary_code', 'shutdown_code'}
        if isinstance(value, dict) and value.get('version') == 2:
            fields |= {'control_endpoints', 'endpoint_pending'}
        if (not isinstance(value, dict) or set(value) != fields or type(value['version']) is not int
                or value['version'] not in (1, 2) or value['installation'] != self.installation
                or not hex_value(value['configuration'], 64) or not hex_value(value['generation'], 32)
                or not process(value) or not isinstance(value['phase'], str) or value['phase'] not in PHASES
                or not isinstance(value['children'], dict) or set(value['children']) != set(CHILDREN)
                or (value['exit_status'] is not None and
                    (type(value['exit_status']) is not int or value['exit_status'] not in (0, 70, 75, 78)))
                or (value['primary_code'] is not None and
                    (not isinstance(value['primary_code'], str) or value['primary_code'] not in CODES))
                or value['shutdown_code'] not in (None, 'session_shutdown_unconfirmed')):
            raise StateError('invalid_session_state')
        for kind, child in value['children'].items():
            if child is not None and (not process(child) or set(child) not in (
                    {'pid', 'proc_start', 'generation'}, {'pid', 'proc_start', 'generation', 'control_endpoint'})
                                      or child['generation'] is not None and not hex_value(child['generation'], 32)):
                raise StateError('invalid_session_state')
            if child is not None and 'control_endpoint' in child:
                import session_endpoints
                root = self.directory if kind == 'bridge' else self.directory / 'notifier'
                try:
                    session_endpoints.validate(child['control_endpoint'], root)
                except (OSError, ValueError) as exc:
                    raise StateError('invalid_session_state') from exc
                if child['generation'] is None:
                    raise StateError('invalid_session_state')
        if value['version'] == 2:
            import session_endpoints
            endpoints, pending_endpoint = value['control_endpoints'], value['endpoint_pending']
            if (not isinstance(endpoints, dict) or set(endpoints) != set(CHILDREN)
                    or pending_endpoint is not None and (not isinstance(pending_endpoint, str)
                        or pending_endpoint not in CHILDREN or endpoints[pending_endpoint] is not None)):
                raise StateError('invalid_session_state')
            for kind, endpoint in endpoints.items():
                if endpoint is not None:
                    root = self.directory if kind == 'bridge' else self.directory / 'notifier'
                    try:
                        session_endpoints.validate(endpoint, root)
                    except (OSError, ValueError) as exc:
                        raise StateError('invalid_session_state') from exc
                    child = value['children'][kind]
                    if child is not None and child.get('control_endpoint', endpoint) != endpoint:
                        raise StateError('invalid_session_state')
            if pending_endpoint is not None and value['phase'] in ('running', 'stopped'):
                raise StateError('invalid_session_state')
        pending = value['spawn_pending']
        if (pending is not None and (not isinstance(pending, str) or pending not in CHILDREN
                                     or value['children'][pending] is not None)
                or pending is not None and value['phase'] in ('running', 'stopped')):
            raise StateError('invalid_session_state')
        pids = [child['pid'] for child in value['children'].values() if child is not None]
        if len(set(pids)) != len(pids) or value['pid'] in pids:
            raise StateError('invalid_session_state')
        if (value['phase'] in ('starting', 'running', 'stopping') and value['exit_status'] is not None
                or value['phase'] == 'running' and any(child is None or child['generation'] is None
                                               for child in value['children'].values())
                or value['phase'] == 'stopped' and value['exit_status'] != 0
                or value['phase'] == 'failed' and value['exit_status'] not in (70, 75, 78)
                or refusal and (value['phase'] != 'failed' or value['exit_status'] not in (70, 78))):
            raise StateError('invalid_session_state')
        return value

    def read(self, *, refusal=False):
        try:
            value = durable_state.read(self.refusal_path if refusal else self.owner_path)
            return None if value is None else self.validate(value, refusal=refusal)
        except (OSError, ValueError) as exc:
            if isinstance(exc, StateError):
                raise
            raise StateError('invalid_session_state') from exc

    def new_owner(self):
        return self.validate(dict(version=2, installation=self.installation, configuration=self.configuration,
                                  generation=uuid.uuid4().hex, pid=os.getpid(),
                                  proc_start=platform_support.proc_start(os.getpid()), phase='starting',
                                  children={key: None for key in CHILDREN}, spawn_pending=None, exit_status=None,
                                  control_endpoints={key: None for key in CHILDREN}, endpoint_pending=None,
                                  primary_code=None, shutdown_code=None))

    def publish(self, value, *, refusal=False):
        self.validate(value, refusal=refusal)
        if value['configuration'] != self.configuration:
            raise StateError('session_ownership_unknown')
        durable_state.publish(self.refusal_path if refusal else self.owner_path, value)

    def stop_requested(self, generation):
        if not hex_value(generation, 32):
            raise StateError('invalid_session_state')
        value = durable_state.read(self.stop_path)
        if value is None:
            return False
        if (set(value) != {'version', 'installation', 'generation'} or type(value['version']) is not int
                or value['version'] != 1 or value['installation'] != self.installation
                or not hex_value(value['generation'], 32)):
            raise StateError('invalid_session_state')
        return value['generation'] == generation

    def request_stop(self):
        with state_lock(self.directory / 'session-supervisor-stop.lock', 'session_stop_in_use'):
            owner = self.read()
            if (owner is None or owner['configuration'] != self.configuration
                    or alive_state(owner) != 'alive'):
                raise StateError('session_ownership_unknown')
            if self.read() != owner:
                raise StateError('session_ownership_unknown')
            durable_state.publish(self.stop_path, dict(version=1, installation=self.installation,
                                                       generation=owner['generation']))
            return owner

    def wait_stopped(self, captured, *, timeout=20):
        self.validate(captured)
        deadline = time.monotonic() + timeout
        while True:
            current = self.read()
            if (current is None or current['generation'] != captured['generation']
                    or current['configuration'] != captured['configuration']
                    or current['pid'] != captured['pid'] or current['proc_start'] != captured['proc_start']):
                raise StateError('session_ownership_unknown')
            children = [child for record in (captured, current)
                        for child in record['children'].values() if child is not None]
            if (alive_state(captured) == 'dead' and all(alive_state(child) == 'dead' for child in children)
                    and current['spawn_pending'] is None):
                return dict(status='stopped', generation=captured['generation'])
            # An unresolved spawn intent can represent an unrecorded child.
            # Neither an absent child record nor a dead parent proves its exit.
            if time.monotonic() >= deadline:
                raise StateError('session_shutdown_unconfirmed')
            time.sleep(.1)

    def retry(self):
        with state_lock(self.directory / 'supervisor.lock', 'session_supervisor_in_use'):
            owner, refusal = self.read(), self.read(refusal=True)
            for record in (owner, refusal):
                if record is None:
                    continue
                if (alive_state(record) != 'dead' or record.get('endpoint_pending') is not None
                        or record['spawn_pending'] is not None and not self.spawn_recovered(record)
                        or any(child is not None and alive_state(child) != 'dead'
                               for child in record['children'].values())):
                    raise StateError('session_ownership_unknown')
            target = owner or refusal
            if target is not None:
                durable_state.publish(self.retry_path, dict(version=1, installation=self.installation,
                    configuration=self.configuration, generation=target['generation']))
            if refusal is not None:
                self.refusal_path.unlink()
                platform_support.sync_state_directory(self.directory)

    def retry_requested(self, generation):
        value = durable_state.read(self.retry_path)
        if value is None:
            return False
        if (set(value) != {'version', 'installation', 'configuration', 'generation'}
                or type(value['version']) is not int or value['version'] != 1
                or value['installation'] != self.installation
                or not hex_value(value['configuration'], 64) or not hex_value(value['generation'], 32)):
            raise StateError('invalid_session_state')
        return value['generation'] == generation and value['configuration'] == self.configuration

    def spawn_recovered(self, record):
        value = durable_state.read(self.recovery_path)
        if value is None:
            return False
        if (set(value) != {'version', 'basis', 'generation', 'asserted_by_uid', 'owner', 'refusal'}
                or type(value['version']) is not int or value['version'] != 1
                or value['basis'] != 'operator_assertion'
                or type(value['asserted_by_uid']) is not int or value['asserted_by_uid'] != os.geteuid()
                or not hex_value(value['generation'], 32)):
            raise StateError('invalid_session_state')
        owner = self.validate(value['owner'])
        refusal = None if value['refusal'] is None else self.validate(value['refusal'], refusal=True)
        if (owner['spawn_pending'] is None or owner['generation'] != value['generation']
                or refusal is not None and any(refusal[key] != owner[key]
                    for key in ('generation', 'configuration', 'pid', 'proc_start'))):
            raise StateError('invalid_session_state')
        return record == owner or refusal is not None and record == refusal

    def recover_spawn(self, generation, *, assert_no_unrecorded_child=False):
        """Record an explicit operator assertion; never manufacture exit observation.

        The caller must expose this as a deliberate recovery action, never infer the
        assertion from absence, a timeout, or peer content. It does not authorize a
        new attempt until the separate explicit retry completes.
        """
        if assert_no_unrecorded_child is not True or not hex_value(generation, 32):
            raise StateError('invalid_session_state')
        with state_lock(self.directory / 'supervisor.lock', 'session_supervisor_in_use'):
            owner, refusal = self.read(), self.read(refusal=True)
            if owner is None or owner['generation'] != generation or owner['spawn_pending'] is None:
                raise StateError('session_ownership_unknown')
            if refusal is not None and any(refusal[key] != owner[key]
                    for key in ('generation', 'configuration', 'pid', 'proc_start')):
                raise StateError('session_ownership_unknown')
            for record in (owner, refusal):
                if record is not None and (alive_state(record) != 'dead'
                        or any(child is not None and alive_state(child) != 'dead'
                               for child in record['children'].values())):
                    raise StateError('session_ownership_unknown')
            durable_state.publish(self.recovery_path, dict(version=1, basis='operator_assertion',
                generation=generation, asserted_by_uid=os.geteuid(), owner=owner, refusal=refusal))
            return dict(status='operator_assertion_recorded', generation=generation)
