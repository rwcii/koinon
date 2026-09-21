"""Captured control socket inodes for the explicitly owned session pair.

A failed connection is never death evidence. Only the recorded process-start
identity and an unchanged captured private socket permit recovery.
"""
from pathlib import Path

import platform_support
from peer_transport import service_path
import session_observation


def validate(value, root):
    if (not isinstance(value, dict) or set(value) != {'path', 'device', 'inode'}
            or value['path'] != str(platform_support.control_socket_path(root))
            or type(value['device']) is not int or value['device'] < 0
            or type(value['inode']) is not int or value['inode'] <= 0):
        raise ValueError('invalid captured session endpoint')
    return value


def capture(root):
    path = service_path(root)
    info = path.lstat()
    if not platform_support.socket_mode_ok(info):
        raise ValueError('unsafe captured session endpoint')
    return validate(dict(path=str(path), device=info.st_dev, inode=info.st_ino), root)


def recover(records, owner):
    """Called under supervisor.lock before a successor owner can replace evidence."""
    from session_supervisor_state import alive_state, StateError
    records.validate(owner)
    def stopped():
        return (records.read() == owner and alive_state(owner) == 'dead'
                and all(child is None or alive_state(child) == 'dead' for child in owner['children'].values()))
    if not stopped():
        raise StateError('session_ownership_unknown')
    pending = []
    try:
        for kind, child in owner['children'].items():
            root = records.directory if kind == 'bridge' else records.directory / 'notifier'
            if not session_observation.endpoint_present(root):
                continue
            captured = None if child is None else child.get('control_endpoint')
            if captured is None or capture(root) != captured:
                raise StateError('session_ownership_unknown')
            pending.append((root, captured))
        for root, captured in pending:
            if not stopped() or capture(root) != captured:
                raise StateError('session_ownership_unknown')
            path = Path(captured['path'])
            path.unlink()
            platform_support.sync_state_directory(path.parent)
    except (OSError, ValueError) as exc:
        raise StateError('session_ownership_unknown') from exc
