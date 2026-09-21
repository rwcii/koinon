"""Read-only native-component observations for upgrade preflight.

The caller supplies validated saved selections. These observations neither
establish installation-wide inventory completeness nor exclude a later start;
quiescence must revalidate them under the appropriate ownership locks.
"""
import copy

import memory_service
import session_observation
import session_service
import session_service_manager


class ObservationError(ValueError):
    pass


def _native(selection):
    if selection.backend not in ('systemd', 'launchd'):
        raise ObservationError('explicit manual upgrade adapter required')


def _manager(before, after):
    if before != after or before.get('status') not in ('observed', 'absent'):
        raise ObservationError('manager ownership unavailable or changed')
    if before['status'] == 'observed':
        pid = before.get('pid')
        if type(pid) is not int or pid < 0:
            raise ObservationError('manager process observation invalid')
        return True, pid
    return False, 0


def _quiet(home, *, session):
    roots = (home, home / 'notifier') if session else (home,)
    locks = ('supervisor.lock', 'notifier.lock') if session else ('supervisor.lock', 'start.lock')
    if (any(session_observation.endpoint_present(root) for root in roots)
            or any(session_observation.lock_held(home / name) for name in locks)):
        raise ObservationError('inactive component retains an endpoint or active owner lock')


def _result(kind, selection, manager, registered, running, owner):
    return copy.deepcopy(dict(kind=kind, selection=selection.record, manager=manager,
                              registered=registered, running=running, owner=owner))


def session(selection):
    _native(selection)
    before = session_service_manager.observation(selection)
    portable = session_service.status(selection)
    after = session_service_manager.observation(selection)
    registered, pid = _manager(before, after)
    owner = portable.get('owner')
    if selection.records.read() != owner:
        raise ObservationError('session owner changed during preflight')
    if portable['status'] == 'running':
        if not registered or owner is None or pid != owner['pid']:
            raise ObservationError('running session lacks matching manager ownership')
        return _result('session', selection, before, registered, True, owner)
    if (pid != 0 or portable['status'] not in ('stopped', 'unobserved')
            or portable['status'] == 'unobserved' and owner is not None):
        raise ObservationError('session stopped state is unconfirmed')
    _quiet(selection.home, session=True)
    return _result('session', selection, before, registered, False, owner)


def memory(selection):
    _native(selection)
    before = memory_service.manager_observation(selection)
    owner = memory_service.read_record(selection, selection.owner_path)
    portable = memory_service.observation(selection)
    after = memory_service.manager_observation(selection)
    registered, pid = _manager(before, after)
    if memory_service.read_record(selection, selection.owner_path) != owner:
        raise ObservationError('memory owner changed during preflight')
    if portable['status'] == 'running':
        if (not registered or owner is None or pid != owner['pid']
                or portable.get('generation') != owner['generation']):
            raise ObservationError('running memory lacks matching manager ownership')
        return _result('memory', selection, before, registered, True, owner)
    if pid != 0 or portable['status'] not in ('stopped', 'unavailable'):
        raise ObservationError('memory stopped state is unconfirmed')
    if owner is not None:
        if (portable['status'] != 'stopped' or memory_service.process_state(owner) != 'dead'
                or owner['child'] is not None and memory_service.process_state(owner['child']) != 'dead'):
            raise ObservationError('memory child or supervisor exit is unconfirmed')
    _quiet(selection.home, session=False)
    return _result('memory', selection, before, registered, False, owner)
