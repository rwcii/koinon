"""Internal native completion driver for a frozen native selection.

The public preflight must establish this supported selection before shutdown.
Inactive native selections are restored before release; manual handoff is separate.
Caller retains the coordinator operation lock throughout; no rollback is automatic.
"""
from pathlib import Path

from koinon import durable_state
from koinon import memory_service_config
from koinon import upgrade_backup
from koinon.upgrade_documents import Documents
from koinon import upgrade_manifest as manifest
from koinon import upgrade_migration
from koinon import upgrade_probe
from koinon import upgrade_release
from koinon import upgrade_quiescence
from koinon import upgrade_start


class CompletionError(ValueError):
    pass


def supported(components):
    if any(item['selection']['backend'] not in ('systemd', 'launchd')
           and not (item['kind'] == 'memory' and item['selection']['backend'] == 'manual')
           for item in components):
        raise CompletionError('completion requires a supported component adapter')


def _backups(exclusion, documents, phase):
    value = documents.read('backups', phase['receipts'][3])
    entries = value['components']['components']
    components = exclusion.loaded['documents']['components']['items']
    if len(entries) != len(components):
        raise CompletionError('backup count differs from frozen components')
    for index, (entry, component) in enumerate(zip(entries, components)):
        if entry['kind'] != component['kind'] or entry['selection'] != component['selection']:
            raise CompletionError('backup selection differs from frozen component')
        if documents.read(f'capture-{index:03d}-backup', entry['backup_document']) != entry['backup']:
            raise CompletionError('component backup document changed')
        source = documents.read(f'capture-{index:03d}-source', entry['source_document'])
        if source != dict(version=1, component=manifest.fingerprint(component), source=entry['source']):
            raise CompletionError('component source document changed')
        if entry['backup']['source'] != entry['source']['sha256']:
            raise CompletionError('component backup has a different source')
        upgrade_backup.verify(entry['backup']['destination'])
    upgrade_backup.verify(value['runtime']['backup']['destination'])
    return entries


def _notifier_files(entry):
    """The notifier gate keeps journal/checkpoint business files closed until release."""
    if entry['kind'] != 'session':
        return
    snapshot = entry['source']
    names = [name for name in snapshot['files']
             if (name.startswith('notify-') and name not in ('notify-ready.json', 'notify-health.json'))
             or name == 'session.json']
    current = upgrade_backup.capture(Path(snapshot['root']), names)
    if any(current['files'][name] != snapshot['files'][name] for name in names):
        raise CompletionError('notifier checkpoint/journal or session binding changed before release')


def _order(components):
    # Memory is ready before any session inspects its explicit binding.
    return sorted(enumerate(components), key=lambda item: (item[1]['kind'] != 'memory', item[0]))


def _expectations(exclusion, documents, phase, components, backups):
    workspace = manifest.check_root(exclusion.journal.directory / 'inventory-workspace')
    if workspace.lstat().st_mode & 0o077:
        raise CompletionError('prepared inventory workspace must be private')
    receipts = [None] * len(components)
    for index, component in _order(components):
        upgrade_start.component(exclusion, component)
        observed = upgrade_probe.capture(exclusion, component)
        snapshot = backups[index]['backup']['destination']
        if component['kind'] == 'memory':
            repo, _ = memory_service_config.identity(component['selection']['common_directory'])
            expected = upgrade_migration.expected_memory(snapshot, workspace, repo,
                                                          observed['status']['store_id'])
        else:
            expected = upgrade_migration.expected_inbox(snapshot, workspace)
        upgrade_migration.verify(expected, observed['inventory'])
        _notifier_files(backups[index])
        receipts[index] = documents.put(f'migration-{index:03d}', expected)
    return dict(version=1, plan=exclusion.loaded['sha256'], expectations=receipts)


def _compare(exclusion, documents, phase, components, backups):
    migration = documents.read('migration', phase['receipts'][5])
    if (migration.get('plan') != exclusion.loaded['sha256']
            or len(migration['expectations']) != len(components)):
        raise CompletionError('migration receipt does not cover the frozen operation')
    results, members = [], []
    for index, component in enumerate(components):
        expected = documents.read(f'migration-{index:03d}', migration['expectations'][index])
        observed = upgrade_probe.capture(exclusion, component)
        result = upgrade_migration.verify(expected, observed['inventory'])
        _notifier_files(backups[index])
        # A second joint observation includes both session children; the bridge
        # inventory alone must never silently authorize an unobserved notifier.
        gate = upgrade_probe.gated(exclusion, component)
        if gate['owner'] != observed['owner']:
            raise CompletionError('generation changed after preservation comparison')
        for kind, child in gate['children'].items():
            if not component['running']:
                continue
            members.append(dict(component=index, kind=kind, generation=child['generation']))
        results.append(dict(component=index, result=result))
    return dict(version=1, plan=exclusion.loaded['sha256'], components=results), members


def run(exclusion):
    """Resume phases 10..19 without ever redoing preservation checks after release.

    Its caller checks supported() before shutdown. Native inactive components
    are temporarily started behind the gate and restored before release.
    """
    components = exclusion.loaded['documents']['components']['items']
    supported(components)
    phase = exclusion.journal.read()
    documents = Documents(exclusion.journal.directory)
    if phase['step'] == 19:
        result = documents.read('complete', phase['receipts'][9])
        # finish() requires the frozen configuration. Once the marker is gone the
        # installation was released, and the status-line step may have added its record.
        from koinon import upgrade_exclusion
        if upgrade_exclusion.read(exclusion.prefix) is not None:
            exclusion.finish()
        return _runtime_revisions(exclusion, documents, phase,
            _participant_guidance(exclusion, documents, phase,
                                 _claude_statusline(exclusion, documents, phase, result)))
    if not 10 <= phase['step'] <= 18:
        raise CompletionError('completion requires migration through final-readiness phase')
    exclusion.verify()
    while phase['step'] < 19:
        step = phase['step']
        if step % 2:
            phase = exclusion.journal.advance(phase)
            continue
        if step == 10:
            backups = _backups(exclusion, documents, phase)
            value = _expectations(exclusion, documents, phase, components, backups)
            name = 'migration'
        elif step == 12:
            value = dict(version=1, plan=exclusion.loaded['sha256'], components=[])
            for index, component in _order(components):
                upgrade_start.component(exclusion, component)
                value['components'].append(dict(component=index, gated_ready=True))
            name = 'starting'
        elif step in (14, 16):
            # A retry may follow an inactive component's completed restoration
            # at 16. Reopen only the selected gated generation for comparison.
            for _, component in _order(components):
                upgrade_start.component(exclusion, component)
            backups = _backups(exclusion, documents, phase)
            comparison, members = _compare(exclusion, documents, phase, components, backups)
            if step == 14:
                value, name = comparison, 'verification'
            else:
                # Restore sessions before memory, preserving their original
                # registration as well as their stopped state before release.
                for _, component in reversed(_order(components)):
                    if not component['running']:
                        upgrade_quiescence.restore_inactive(exclusion, component)
                value = dict(version=1, plan=exclusion.loaded['sha256'], members=members)
                upgrade_release.validate(exclusion.loaded, value)
                name = 'release'
                retained = durable_state.read(documents.path(name), max_bytes=1024 * 1024)
                if retained is not None:
                    # A lost phase publication may leave a valid old decision.
                    # Fresh comparison above still proves preserved state. Children
                    # not in that retained decision refuse and restart after 17;
                    # their successor is checked for ordinary readiness at 18.
                    value = upgrade_release.validate(exclusion.loaded, retained)
                    documents.read(name, manifest.fingerprint(value))
        else:
            readiness = []
            for index, component in _order(components):
                if component['running']:
                    upgrade_start.component(exclusion, component)
                    readiness.append(dict(component=index, live_ready=True))
                else:
                    upgrade_quiescence.inactive(exclusion, component)
                    readiness.append(dict(component=index, live_ready=False,
                                          stopped=True, registered=component['registered']))
            verification = documents.read('verification', phase['receipts'][7])
            value = dict(version=1, plan=exclusion.loaded['sha256'],
                         preflight=documents.read('prepared-checks', phase['receipts'][0]),
                         preservation=verification, live_readiness=readiness,
                         released_generations=documents.read('release', phase['receipts'][8]),
                         boundary='preservation_before_release_live_readiness_after_release')
            name = 'complete'
        digest = documents.put(name, value)
        exclusion.verify()
        phase = exclusion.journal.advance(phase, evidence=digest)
    result = documents.read('complete', phase['receipts'][9])
    exclusion.finish()
    return _runtime_revisions(exclusion, documents, phase,
            _participant_guidance(exclusion, documents, phase,
                                 _claude_statusline(exclusion, documents, phase, result)))


def _claude_statusline(exclusion, documents, phase, result):
    """Apply the preflight's Claude status-line plan once ordinary admission is back.

    The upgrade restores the frozen install.json at finish(), so the saved original can be
    recorded only afterwards. The outcome is reported with the result and kept beside it.
    """
    import sys
    from koinon import claude_statusline
    retained = durable_state.read(documents.path('claude-statusline'), max_bytes=1024 * 1024)
    if retained is not None:
        # A resumed completion reports the first outcome instead of applying the plan again.
        return dict(result, claude_statusline=retained.get('outcome'))
    planned = documents.read('prepared-checks', phase['receipts'][0]).get('claude_statusline')
    outcome = claude_statusline.apply_planned(exclusion.prefix, planned, sys.executable)
    if outcome is not None:
        documents.put('claude-statusline', dict(version=1, plan=exclusion.loaded['sha256'], outcome=outcome))
    return dict(result, claude_statusline=outcome)


def _participant_guidance(exclusion, documents, phase, result):
    """Reconcile the managed guidance blocks that the preflight observed, once.

    Like the status-line step, this runs after finish() restores ordinary admission, and a
    resumed completion reports the kept outcome instead of reconciling again.
    """
    import sys
    from koinon import participant_instructions
    retained = durable_state.read(documents.path('participant-guidance'), max_bytes=1024 * 1024)
    if retained is not None:
        return dict(result, participant_guidance=retained.get('outcome'))
    planned = documents.read('prepared-checks', phase['receipts'][0]).get('participant_guidance')
    try:
        outcome = participant_instructions.apply_planned(exclusion.prefix, planned, sys.executable)
    except (OSError, ValueError) as exc:
        outcome = dict(outcome='failed', error=str(exc))
    if outcome is not None:
        documents.put('participant-guidance', dict(version=1, plan=exclusion.loaded['sha256'], outcome=outcome))
    return dict(result, participant_guidance=outcome)


def _runtime_revisions(exclusion, documents, phase, result):
    """Publish the frozen new revisions only after finish; retain evidence across resume."""
    from koinon import install_state
    retained = durable_state.read(documents.path('runtime-revisions'))
    if retained is not None:
        return dict(result, runtime_revisions=retained['outcome'])
    target = documents.read('prepared-checks', phase['receipts'][0]).get('runtime_revisions')
    if target is None:
        return result
    from koinon import revisions
    # The recovery archive is the frozen new source, so its catalog is authoritative.
    target = dict(target)
    target.setdefault('guidance_revision', revisions.GUIDANCE_REVISION)
    with install_state.locked(exclusion.prefix) as installed:
        installed.merge(target)
    documents.put('runtime-revisions', dict(version=1, plan=exclusion.loaded['sha256'], outcome=target))
    return dict(result, runtime_revisions=target)
