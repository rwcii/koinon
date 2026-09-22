"""Recorded control-endpoint reservations while upgrade capture excludes writers.

An interrupted bind with no recorded inode is ambiguous and refuses automatic
cleanup. A recorded reservation is recoverable only after its exact owner died.
No listener is started and no descriptor is passed to children.
"""
from contextlib import contextmanager
import os
from pathlib import Path

from koinon import durable_state
from koinon import platform_support
from koinon import session_endpoints
from koinon import session_observation
from koinon import session_socket_handoff
from koinon import upgrade_manifest


class ReservationError(ValueError):
    pass


def _validate(value, root, plan):
    if (not isinstance(value, dict)
            or set(value) != {'version', 'plan', 'root', 'pid', 'proc_start', 'state', 'endpoint'}
            or type(value['version']) is not int or value['version'] != 1
            or value['plan'] != plan or value['root'] != str(root)
            or type(value['pid']) is not int or value['pid'] <= 0
            or not isinstance(value['proc_start'], str) or not value['proc_start'].strip()
            or value['state'] not in ('pending', 'reserved', 'released')
            or value['state'] == 'reserved' and value['endpoint'] is None):
        raise ReservationError('invalid retained upgrade reservation')
    if value['endpoint'] is not None:
        session_endpoints.validate(value['endpoint'], root)
    return value


def _remove(root, captured):
    if session_endpoints.capture(root) != captured:
        raise ReservationError('upgrade reservation endpoint changed; preserve it')
    path = Path(captured['path'])
    path.unlink()
    platform_support.sync_state_directory(path.parent)


@contextmanager
def hold(directory, root, plan):
    """Caller holds operation and selected supervisor locks throughout this scope."""
    root = upgrade_manifest.check_root(root)
    directory = upgrade_manifest.check_root(directory)
    if directory.lstat().st_mode & 0o077 or not upgrade_manifest.hex_digest(plan):
        raise ReservationError('private operation and frozen plan required')
    path = directory / ('reservation-' + upgrade_manifest.fingerprint(str(root))[:32] + '.json')
    old = durable_state.read(path)
    if old is not None:
        _validate(old, root, plan)
        if old['state'] != 'released':
            if platform_support.process_state(old['pid'], old['proc_start']) != 'dead':
                raise ReservationError('retained reservation owner has not exited')
            if session_observation.endpoint_present(root):
                if old['endpoint'] is None:
                    raise ReservationError('interrupted reservation has no captured inode; preserve endpoint')
                _remove(root, old['endpoint'])
    # A foreign endpoint, including one appearing after recovery, is never removed.
    if session_observation.endpoint_present(root):
        raise ReservationError('control endpoint already exists before upgrade reservation')
    value = dict(version=1, plan=plan, root=str(root), pid=os.getpid(),
                 proc_start=platform_support.proc_start(os.getpid()), state='pending', endpoint=None)
    durable_state.publish(path, value)
    endpoint, captured = session_socket_handoff.bind(root)
    try:
        value.update(state='reserved', endpoint=captured)
        durable_state.publish(path, value)
        yield captured
    finally:
        endpoint.close()
        # Even failed publication has the in-process captured identity. After a
        # process death, recovery instead uses only the durably retained record.
        _remove(root, captured)
        value['state'] = 'released'
        durable_state.publish(path, value)
