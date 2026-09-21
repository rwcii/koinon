"""Internal phase driver for an already prepared and exclusively owned upgrade.

Preflight and the public recovery entrypoint must establish the frozen plan and
prepared destinations first. This driver stops at replacement completion; callers
must not treat that boundary as a completed upgrade or restart old runtime code.
"""
import upgrade_capture
from upgrade_documents import Documents
import upgrade_manifest as manifest
import upgrade_quiescence
import upgrade_replace


class CoordinatorError(ValueError):
    pass


def destinations(exclusion):
    """Require preflight-created private destinations; never create after shutdown."""
    root = exclusion.journal.directory
    runtime = root / 'runtime-backup'
    components = [root / f'component-backup-{index:03d}'
                  for index, _ in enumerate(exclusion.loaded['documents']['components']['items'])]
    for path in [runtime, *components]:
        checked = manifest.check_root(path)
        if checked != path or checked.lstat().st_mode & 0o077:
            raise CoordinatorError('prepared backup destination is not private')
    return runtime, components


def through_replacement(exclusion):
    """Resume owned phases 2..9; return only after replacement receipt is durable.

    Caller retains exclusion.operation() ownership. Source/runtime compatibility and
    backup destinations are checked again before any shutdown, not invented here.
    Phase 9 means migration has not yet started and ordinary service admission stays
    closed. Errors retain the current journal and all recovery evidence.
    """
    phase = exclusion.verify()
    if not 2 <= phase['step'] <= 9:
        raise CoordinatorError('shutdown/replacement driver requires phases 2 through 9')
    source, runtime = (exclusion.loaded['documents'][name] for name in ('source', 'runtime'))
    upgrade_replace.preflight(source, runtime)
    runtime_destination, component_destinations = destinations(exclusion)
    documents = Documents(exclusion.journal.directory)
    while phase['step'] < 6:
        if phase['step'] % 2:
            phase = exclusion.journal.advance(phase)
            continue
        kind = 'session' if phase['step'] == 2 else 'memory'
        evidence = upgrade_quiescence.stop_phase(exclusion, kind)
        receipt = documents.put('quiescing-' + kind, evidence)
        phase = exclusion.journal.advance(phase, evidence=receipt)
    if phase['step'] == 9:
        with upgrade_capture.hold(exclusion) as guard:
            upgrade_replace.confirm(guard)
        return phase
    with upgrade_capture.hold(exclusion) as guard:
        if phase['step'] == 6:
            components = upgrade_capture.copy_components(guard, component_destinations)
            runtime = upgrade_capture.copy_runtime(guard, runtime_destination)
            receipt = documents.put('backups', dict(version=1, runtime=runtime, components=components))
            guard.verify()
            phase = exclusion.journal.advance(phase, evidence=receipt)
        if phase['step'] == 7:
            phase = exclusion.journal.advance(phase)
        if phase['step'] == 8:
            evidence = upgrade_replace.replace(guard)
            receipt = documents.put('replacement', evidence)
            guard.verify()
            phase = exclusion.journal.advance(phase, evidence=receipt)
    return phase
