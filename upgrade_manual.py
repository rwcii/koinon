"""Explicit foreground handoff for selected manual upgrade supervisors.

The coordinator never detaches a process or treats a printed command as readiness.
The caller runs the returned argv in a persistent managed session and resumes the
same operation; ordinary live ownership and child/control joins establish readiness.
"""
import shlex

import memory_service


class HandoffRequired(ValueError):
    def __init__(self, exclusion, selection, kind):
        if kind != 'memory':
            raise ValueError('manual session upgrade requires an explicit ownership adapter')
        argv = selection.start_command()
        self.handoff = dict(
            status='manual_handoff_required', operation=str(exclusion.journal.directory),
            plan=exclusion.loaded['sha256'], phase=exclusion.verify()['step'],
            component=kind, argv=argv, command=shlex.join(argv),
            instruction='Run this exact command in a persistent managed terminal/tool session, keep that job alive, then resume this operation with its plan digest. Do not detach it or start a second copy while the first is running.')
        super().__init__('verified manual supervisor handoff required before upgrade can continue')


def observe(selection, kind):
    import upgrade_observation
    if selection.backend != 'manual' or kind != 'memory':
        raise upgrade_observation.ObservationError('owned manual memory selection required')
    read = lambda: memory_service.read_record(selection, selection.owner_path)
    portable = lambda: memory_service.observation(selection)
    state = memory_service.process_state
    children = lambda owner: [owner['child']]
    owner = read()
    before = portable()
    running = before['status'] == 'running'
    if running:
        if owner is None or state(owner) != 'alive':
            raise upgrade_observation.ObservationError('manual supervisor ownership is unconfirmed')
        manager = dict(status='observed', adapter='foreground_supervisor',
                       pid=owner['pid'], generation=owner['generation'])
    else:
        if (before['status'] not in ('stopped', 'unobserved', 'unavailable')
                or owner is not None and (before['status'] != 'stopped' or state(owner) != 'dead'
                    or any(child is not None and state(child) != 'dead' for child in children(owner)))):
            raise upgrade_observation.ObservationError('manual supervisor or child exit is unconfirmed')
        upgrade_observation._quiet(selection.home, session=kind == 'session')
        manager = dict(status='absent')
    if read() != owner or portable() != before:
        raise upgrade_observation.ObservationError('manual ownership changed during observation')
    return upgrade_observation._result(kind, selection, manager, running, running, owner)


def ready(selection, kind):
    observed = observe(selection, kind)
    return dict(status='running' if observed['running'] else 'stopped', managed=False,
                basis='foreground_supervisor_and_live_children', owner=observed['owner'])


def ensure(exclusion, selection, kind):
    observed = observe(selection, kind)
    if not observed['running']:
        raise HandoffRequired(exclusion, selection, kind)
    return dict(status='running', managed=False, basis='foreground_supervisor_and_live_children')


def stop(selection, kind):
    """Stop only the exact owned foreground supervisor; confirm all child exits."""
    observed = observe(selection, kind)
    if observed['running']:
        memory_service.stop(selection)
    after = observe(selection, kind)
    if after['running']:
        raise ValueError('manual supervisor did not stop')
    return after
