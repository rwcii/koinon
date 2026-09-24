"""Loaded runtime identity and explicit, session-scoped guidance acknowledgements.

Revision values never replace service ownership or process-generation evidence.
"""
import ast
import hashlib
from importlib.resources import files
from functools import lru_cache
import json
from pathlib import Path
import re
import time

from koinon import durable_state, guidance, runtime_names
from koinon.inbox_schema import hex_value


def runtime_revision(root=None, *, manifest=None):
    """Hash the installed Python allowlist, equally in a checkout, prefix or recovery zip."""
    if manifest is not None:
        content = {name: value['sha256'] for name, value in manifest['files'].items() if name.endswith('.py')}
        return hashlib.sha256(json.dumps(content, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    root = files('koinon').parent if root is None else root
    tree = ast.parse(root.joinpath('scripts/install.py').read_text())
    names = next(ast.literal_eval(node.value) for node in tree.body
                 if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == 'FILES'
                                                         for t in node.targets))
    content = {name: hashlib.sha256(root.joinpath(name).read_bytes()).hexdigest()
               for name in sorted(names) if name.endswith('.py')}
    return hashlib.sha256(json.dumps(content, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


# Only long-lived service startup and publication call this. Short CLI reads never hash.
@lru_cache(maxsize=1)
def loaded_runtime_revision():
    return runtime_revision()


GUIDANCE_REVISION = guidance.revision()


def installed_fields():
    return dict(runtime_revision=loaded_runtime_revision(), guidance_revision=GUIDANCE_REVISION)


def ack_path(state=None, *, family=None, session_id=None, registry=None):
    if family == 'claude':
        if not isinstance(session_id, str) or re.fullmatch(r'[A-Za-z0-9_-]{1,128}', session_id) is None:
            raise ValueError('Claude guidance acknowledgement needs this session identity')
        from koinon.participant_status import directory
        return directory(registry) / f'guidance-ack-{session_id}.json'
    if state is None:
        raise ValueError('guidance acknowledgement needs this session state directory')
    return Path(state) / 'guidance-ack.json'


def acknowledged(path):
    try:
        value = durable_state.read(path)
        if (not isinstance(value, dict) or set(value) != {'revision', 'acknowledged_at'}
                or not hex_value(value.get('revision'), 64)
                or type(value.get('acknowledged_at')) not in (int, float)):
            return None
        return value['revision']
    except (OSError, ValueError):
        return None


def guidance_fields(config, path):
    revision = config.get('guidance_revision')
    if not hex_value(revision, 64):
        return dict(guide_revision=None, guide_stale=None)
    return dict(guide_revision=revision, guide_stale=None if path is None else acknowledged(path) != revision)


def acknowledge(prefix, revision, path):
    # Serialize the comparison with install/upgrade publication. A guide read never calls this.
    from koinon import install_state
    from koinon.peer_transport import private_dir
    if not hex_value(revision, 64):
        raise ValueError('invalid guidance revision')
    with install_state.locked(prefix) as installed:
        if upgrade_incomplete(prefix):
            raise ValueError('upgrade is incomplete; resume it before acknowledging guidance')
        if installed.config.get('guidance_revision') != revision:
            raise ValueError('guidance revision is not the installed revision; read guide again')
        private_dir(path.parent)
        durable_state.publish(path, dict(revision=revision, acknowledged_at=time.time()))


def observe_runtime(config, service_revision=None, *, upgrade=False):
    expected = config.get('runtime_revision')
    if upgrade:
        return dict(state='unavailable', reason='upgrade_incomplete', installed=expected,
                    running=service_revision)
    if not hex_value(expected, 64) or not hex_value(service_revision, 64):
        return dict(state='unknown', reason='runtime_revision_unavailable', installed=expected,
                    running=service_revision)
    return dict(state='observed', reason='match' if expected == service_revision else 'mismatch',
                installed=expected, running=service_revision)


def read_fields(prefix, path, service_revision=None):
    try:
        config = runtime_names.install_config(prefix)
        return dict(guidance_fields(config, path), runtime_revision=service_revision,
                    runtime=observe_runtime(config, service_revision, upgrade=upgrade_incomplete(prefix)))
    except (OSError, ValueError) as exc:
        return dict(guide_revision=None, guide_stale=None, runtime_revision=service_revision,
                    runtime=observe_runtime({}, service_revision,
                        upgrade=getattr(exc, 'code', None) == 'installation_upgrading'))


def running_owner_revision(state):
    """A stopped or unverifiable owner is not evidence of currently running code."""
    from koinon import platform_support
    try:
        owner = durable_state.read(Path(state) / 'session-supervisor.json')
        if (owner and type(owner.get('pid')) is int and platform_support.process_alive(owner['pid'])
                and platform_support.same_process(owner.get('proc_start'),
                                                 platform_support.proc_start(owner['pid']))):
            return owner.get('runtime_revision')
    except (OSError, ValueError):
        pass
    return None


def upgrade_incomplete(prefix):
    """Read journal evidence without taking a lock or confirming (writing) any file."""
    try:
        pointer = durable_state.read(Path(prefix) / '.upgrade' / 'current.json')
        if pointer is None:
            return False
        operation = Path(pointer['operation'])
        if not operation.is_absolute() or not hex_value(pointer.get('plan'), 64):
            return True
        phase = durable_state.read(operation / 'phase.json')
        if phase is None or phase.get('plan') != pointer['plan'] or phase.get('step') != 19:
            return True
        prepared = durable_state.read(operation / 'prepared-checks.json', max_bytes=1024 * 1024)
        if prepared is None or prepared.get('plan') != pointer['plan']:
            return True
        if 'runtime_revisions' not in prepared:
            return False  # A completed operation from an older release.
        receipt = durable_state.read(operation / 'runtime-revisions.json')
        return (receipt is None or receipt.get('plan') != pointer['plan']
                or not isinstance(receipt.get('outcome'), dict)
                or not all(receipt['outcome'].get(key) == value
                           for key, value in prepared['runtime_revisions'].items())
                or not hex_value(receipt['outcome'].get('guidance_revision'), 64))
    except (OSError, ValueError, KeyError, TypeError):
        return True


def service_revision(state, bridge=None):
    if state is None:
        return None
    try:
        if runtime_names.present(Path(state) / 'native-service.json'):
            return running_owner_revision(state)
        return (bridge or {}).get('runtime_revision')
    except (OSError, ValueError):
        return None
