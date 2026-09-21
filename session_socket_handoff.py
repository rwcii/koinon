"""Explicit parent-owned control descriptors; ordinary direct starts still bind exclusively."""
import os
from pathlib import Path
import socket

import durable_state
import platform_support
from peer_transport import private_dir
import session_endpoints
from session_supervisor_state import Records


def bind(root):
    """Return a bound, non-listening descriptor and its private path identity.

    The caller must have durably recorded creation intent before calling this,
    and must publish the returned identity before passing the descriptor to a child.
    """
    private_dir(Path(root))
    platform_support.refuse_legacy_control_conflict(root)
    path = platform_support.control_socket_path(root)
    private_dir(path.parent)
    endpoint = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        endpoint.bind(str(path))
        path.chmod(0o600)
        identity = session_endpoints.capture(root)
        endpoint.setblocking(False)
        return endpoint, identity
    except BaseException:
        # A created but unpublished path is retained for the caller's intent
        # protocol; this helper never guesses whether a failed bind created it.
        endpoint.close()
        raise


def take(fd, directory, kind, generation):
    """Validate an explicitly inherited descriptor against its live parent's record."""
    if type(fd) is not int or fd < 3 or kind not in ('bridge', 'notifier'):
        raise ValueError('invalid inherited control descriptor')
    directory = Path(directory)
    raw = durable_state.read(directory / 'session-supervisor.json')
    if not isinstance(raw, dict):
        raise ValueError('missing inherited descriptor owner')
    records = Records(directory, raw.get('installation'), raw.get('configuration'))
    owner = records.read()
    root = directory if kind == 'bridge' else directory / 'notifier'
    if (owner is None or owner['generation'] != generation or owner['pid'] != os.getppid()
            or platform_support.process_state(owner['pid'], owner['proc_start']) != 'alive'
            or owner.get('control_endpoints', {}).get(kind) is None):
        raise ValueError('inherited descriptor parent is not the recorded owner')
    captured = owner['control_endpoints'][kind]
    endpoint = socket.socket(fileno=os.dup(fd))
    try:
        if (endpoint.family != socket.AF_UNIX or endpoint.type != socket.SOCK_STREAM
                or endpoint.getsockname() != captured['path']
                or session_endpoints.capture(root) != captured):
            raise ValueError('inherited socket differs from captured parent endpoint')
        child = owner['children'][kind]
        if not (owner['spawn_pending'] == kind or child is not None and child['pid'] == os.getpid()):
            raise ValueError('inherited descriptor is not assigned to this child')
        after = records.read()
        if after is None or any(after[key] != owner[key] for key in (
                'generation', 'pid', 'proc_start', 'configuration', 'control_endpoints')):
            raise ValueError('inherited descriptor owner changed')
        assigned = after['children'][kind]
        if (after['phase'] not in ('starting', 'running')
                or not (after['spawn_pending'] == kind or assigned is not None and assigned['pid'] == os.getpid())):
            raise ValueError('inherited descriptor assignment changed')
        os.close(fd)
        endpoint.setblocking(False)
        return endpoint
    except BaseException:
        endpoint.close()
        raise
