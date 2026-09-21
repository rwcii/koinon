"""Internal native completion driver for an all-running frozen selection.

The public preflight must establish this supported selection before shutdown.
Inactive and manual adapters are deliberately not inferred by this driver.
Caller retains the coordinator operation lock throughout; no rollback is automatic.
"""
from pathlib import Path

import durable_state
import memory_service_config
import upgrade_backup
from upgrade_documents import Documents
import upgrade_manifest as manifest
import upgrade_migration
import upgrade_probe
import upgrade_release
import upgrade_start


class CompletionError(ValueError):
    pass


def supported(components):
    if any(not item['running'] or item['selection']['backend'] not in ('systemd', 'launchd')
           for item in components):
        raise CompletionError('completion requires an explicit inactive/manual adapter')


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
            members.append(dict(component=index, kind=kind, generation=child['generation']))
        results.append(dict(component=index, result=result))
    return dict(version=1, plan=exclusion.loaded['sha256'], components=results), members


def run(exclusion):
    """Resume phases 10..19 without ever redoing preservation checks after release.

    This internal driver currently accepts only originally running native
    components. Its caller must check supported() before any shutdown. The public
    operation still needs the inactive/manual adapters and complete preflight.
    """
    components = exclusion.loaded['documents']['components']['items']
    supported(components)
    phase = exclusion.journal.read()
    documents = Documents(exclusion.journal.directory)
    if phase['step'] == 19:
        result = documents.read('complete', phase['receipts'][9])
        exclusion.finish()
        return result
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
            backups = _backups(exclusion, documents, phase)
            comparison, members = _compare(exclusion, documents, phase, components, backups)
            if step == 14:
                value, name = comparison, 'verification'
            else:
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
                upgrade_start.component(exclusion, component)
                readiness.append(dict(component=index, live_ready=True))
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
    return result
