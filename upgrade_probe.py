"""Read-only generation joins for private gated upgrade verification."""
import asyncio
from pathlib import Path

import memory_service
from peer_transport import control_exchange
import session_service_manager
import upgrade_manifest as manifest
import upgrade_quiescence


class ProbeError(ValueError):
    pass


def _selected(exclusion, component, *, live=False):
    phase = exclusion.verify()['step']
    if phase not in ((18,) if live else (10, 12, 14, 16)):
        raise ProbeError('verification is outside its selected pre/post-release phase')
    selected = upgrade_quiescence.selection(exclusion, component)
    source = exclusion.loaded['documents']['source']
    manifest.verify(source)
    current = manifest.capture(exclusion.loaded['plan']['canonical_prefix'], list(source['files']))
    if current['files'] != source['files']:
        raise ProbeError('gated verification requires the complete new runtime')
    return selected, phase


def _gate(exclusion, value, captured, connected_pid, *, released=False):
    if (type(connected_pid) is not int or connected_pid != captured['pid']
            or not isinstance(value, dict) or type(value.get('pid')) is not int
            or value['pid'] != connected_pid or value.get('generation') != captured['generation']):
        raise ProbeError('private control does not match the selected child generation')
    gate = value.get('upgrade')
    if not isinstance(gate, dict) or type(gate.get('post_release_start')) is not bool:
        raise ProbeError('selected child has no valid upgrade gate status')
    expected = dict(plan=exclusion.loaded['sha256'], operation=str(exclusion.journal.directory),
                    generation=captured['generation'], released=released,
                    post_release_start=gate['post_release_start'] if released else False)
    if gate != expected or gate.get('released') is not released:
        raise ProbeError('selected child has not confirmed the required upgrade gate boundary')


def gated(exclusion, component):
    return _observe(exclusion, component)


def live(exclusion, component):
    """Check post-release readiness, never preservation after renewed writes.

    A fresh post-release child may have a new generation. Its own gate checks the
    release receipt; this probe joins that status to the current owned process.
    """
    return _observe(exclusion, component, live=True)


def _observe(exclusion, component, *, live=False):
    selected, phase = (_selected(exclusion, component, live=True) if live
                       else _selected(exclusion, component))
    if component['kind'] == 'session':
        observe = lambda: session_service_manager.status(selected)
        owner_read = selected.records.read
    else:
        observe = lambda: memory_service.managed_status(selected)
        owner_read = lambda: memory_service.read_record(selected, selected.owner_path)
    if selected.backend == 'manual':
        import upgrade_manual
        observe = lambda: upgrade_manual.ready(selected, component['kind'])
    before = observe()
    if before.get('status') != 'running' or (selected.backend != 'manual' and before.get('managed') is not True):
        raise ProbeError('native manager and selected service are not jointly ready')
    owner = owner_read()
    if owner is None:
        raise ProbeError('selected supervisor has no ownership record')
    children = owner['children'] if component['kind'] == 'session' else {'memory': owner['child']}
    statuses = {}
    for kind, child in children.items():
        if child is None or child.get('generation') is None:
            raise ProbeError('selected child has no generation')
        root = selected.home / 'notifier' if kind == 'notifier' else selected.home
        reply, pid = asyncio.run(control_exchange(root, dict(op='hello' if kind == 'memory' else 'status'), timeout=5))
        if reply.get('ok') is not True:
            raise ProbeError('selected child refused private status')
        _gate(exclusion, reply.get('result'), child, pid, released=live)
        statuses[kind] = reply['result']
    if (owner_read() != owner or observe() != before or owner_read() != owner
            or exclusion.verify()['step'] != phase):
        raise ProbeError('gated ownership changed during verification')
    return dict(version=1, kind=component['kind'], selection=component['selection'],
                owner=owner, children=statuses)


def capture(exclusion, component):
    before = gated(exclusion, component)
    kind = 'bridge' if component['kind'] == 'session' else 'memory'
    status = before['children'][kind]
    record = component['selection']
    root = record['state_directory'] if kind == 'bridge' else record['service_directory']
    request = dict(op='upgrade-inventory', plan=exclusion.loaded['sha256'],
                   generation=status['generation'])
    if kind == 'memory':
        request['repo'] = status['repo']
    reply, pid = asyncio.run(control_exchange(Path(root), request, timeout=30))
    if reply.get('ok') is not True or type(pid) is not int or pid != status['pid']:
        raise ProbeError('selected child refused generation-bound inventory')
    after = gated(exclusion, component)
    if after['owner'] != before['owner']:
        raise ProbeError('selected generation changed across inventory capture')
    # Status timestamps are live observations, not preserved business records.
    return dict(version=1, kind=kind, selection=record, owner=before['owner'],
                gate=status['upgrade'], status=status, inventory=reply['result'])
