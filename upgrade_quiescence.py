"""Owned native shutdown phases for the selected upgrade operation.

The caller holds the coordinator operation lock. Phase completion is returned as
observed evidence, never inferred from a successful stop request. Backup still
requires retained writer exclusion after these observations.
"""
from pathlib import Path

import install_state
import memory_service
from participant_lock import file_lock
import session_service
import session_service_artifacts
import session_service_manager
import upgrade_observation


class QuiescenceError(ValueError):
    pass


def selection(exclusion, component):
    if component not in exclusion.loaded['documents']['components']['items']:
        raise QuiescenceError('component is not in the frozen operation')
    record = component['selection']
    if component['kind'] == 'session':
        selected = session_service.Selection(exclusion.prefix, record['state_directory'],
                                             backend=record['backend'], upgrading=True)
    else:
        selected = memory_service.Selection(exclusion.prefix, record['common_directory'],
                                            backend=record['backend'], upgrading=True)
    if selected.record != record or selected.upgrade is None or selected.upgrade['sha256'] != exclusion.loaded['sha256']:
        raise QuiescenceError('selected component changed from the frozen operation')
    return selected


def stopped(exclusion, component):
    selected = selection(exclusion, component)
    observed = (upgrade_observation.session(selected) if component['kind'] == 'session'
                else upgrade_observation.memory(selected))
    if observed['running'] or observed['registered']:
        raise QuiescenceError('selected component is not stopped and deactivated')
    return observed


def stop_phase(exclusion, kind):
    """Stop sessions before memory, preserving selections, artifacts and state.

    This function neither advances the phase journal nor publishes completion.
    Resume repeats current ownership observations before deciding to stop again.
    No memory start lock or supervisor lifetime lock is held while waiting for exit.
    """
    expected = {'session': 2, 'memory': 4}
    if kind not in expected or exclusion.verify()['step'] != expected[kind]:
        raise QuiescenceError('owned shutdown requested outside its pending phase')
    components = exclusion.loaded['documents']['components']['items']
    if kind == 'memory':
        for component in components:
            if component['kind'] == 'session':
                stopped(exclusion, component)
    results = []
    for component in components:
        if component['kind'] != kind:
            continue
        selected = selection(exclusion, component)
        if selected.backend == 'manual':
            import upgrade_manual
            upgrade_manual.stop(selected, kind)
        elif kind == 'session':
            with session_service_artifacts.locked(selected.home / 'lifecycle.lock'), \
                    session_service_artifacts.locked(selected.home / 'registration.lock'):
                selected = selection(exclusion, component)
                session_service_manager.deactivate_locked(selected, allow_unstarted=True)
        else:
            with install_state.locked(exclusion.prefix, validator=exclusion._validate) as installed:
                if installed.config != exclusion.marked:
                    raise QuiescenceError('upgrade exclusion disappeared during shutdown')
                with file_lock(Path(selected.home) / 'manager.lock', 'manager_busy', None):
                    selected = selection(exclusion, component)
                    memory_service.deactivate_owned(selected)
        results.append(stopped(exclusion, component))
    # A prior component must not have restarted while another was stopping.
    final = [stopped(exclusion, component) for component in components if component['kind'] == kind]
    if results != final or exclusion.verify()['step'] != expected[kind]:
        raise QuiescenceError('shutdown evidence changed before phase completion')
    return dict(version=1, kind=kind, components=final)


def inactive(exclusion, component):
    """Observe the originally inactive native selection without starting it."""
    if component['running'] or exclusion.verify()['step'] not in (16, 18):
        raise QuiescenceError('inactive restoration is outside its selected boundary')
    selected = selection(exclusion, component)
    observe = upgrade_observation.session if component['kind'] == 'session' else upgrade_observation.memory
    before = observe(selected)
    if (before['running'] or before['registered'] != component['registered']
            or observe(selected) != before):
        raise QuiescenceError('original inactive registration/state was not restored')
    return before


def restore_inactive(exclusion, component):
    """Stop a temporary migration generation; retain the original registration."""
    if component['running'] or exclusion.verify()['step'] != 16:
        raise QuiescenceError('inactive restoration requires the pending release phase')
    selected = selection(exclusion, component)
    if selected.backend == 'manual':
        import upgrade_manual
        upgrade_manual.stop(selected, component['kind'])
    elif component['kind'] == 'session':
        if component['registered']:
            session_service_manager.stop(selected)
        else:
            with session_service_artifacts.locked(selected.home / 'lifecycle.lock'), \
                    session_service_artifacts.locked(selected.home / 'registration.lock'):
                selected = selection(exclusion, component)
                session_service_manager.deactivate_locked(selected, allow_unstarted=True)
    else:
        with install_state.locked(exclusion.prefix, validator=exclusion._validate) as installed:
            if installed.config != exclusion.marked:
                raise QuiescenceError('upgrade exclusion disappeared during restoration')
            with file_lock(Path(selected.home) / 'manager.lock', 'manager_busy', None):
                selected = selection(exclusion, component)
                if component['registered']:
                    memory_service.stop(selected)
                else:
                    memory_service.deactivate_owned(selected)
    return inactive(exclusion, component)
