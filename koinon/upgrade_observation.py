"""Read-only native-component observations for upgrade preflight.

The caller supplies validated saved selections. These observations neither
establish installation-wide inventory completeness nor exclude a later start;
quiescence must revalidate them under the appropriate ownership locks.
"""
import copy
from itertools import islice
import os
from pathlib import Path
import stat
import sys

from koinon import install_state
from koinon import runtime_names
from koinon import session_service_artifacts
from koinon import upgrade_manifest as manifest
from koinon import upgrade_plan as plans

import memory_service
from koinon import session_observation
import session_service
from koinon import session_service_manager


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
    if selection.backend == 'manual':
        from koinon import upgrade_manual
        return upgrade_manual.observe(selection, 'memory')
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


def installation(prefix):
    """Enumerate supported saved components under the installation lock.

    This freezes native registration selection only for the duration of this
    call. It is not startup exclusion, complete upgrade preflight, or authority
    to shut down a component. Legacy/manual/ambiguous registrations refuse.
    """
    prefix = manifest.select_root(prefix)
    with install_state.locked(prefix) as installed:
        return installation_locked(prefix, installed)


def installation_locked(prefix, installed):
    """Caller retains the installation lock through plan preparation and exclusion."""
    prefix = manifest.select_root(prefix)
    if manifest.select_root(installed.prefix) != prefix:
        raise ObservationError('installation lock selects another prefix')
    config = copy.deepcopy(installed.config)
    if not config or config.get('installation_state', 'installed') != 'installed':
        raise ObservationError('supported installed configuration required')
    state = Path(config['state_root'])
    canonical_state = manifest.select_root(state)
    sessions = state / 'sessions'

    def scan():
        if manifest.select_root(state) != canonical_state:
            raise ObservationError('selected state root changed during enumeration')
        try:
            before = sessions.lstat()
        except FileNotFoundError:
            return None
        if (not stat.S_ISDIR(before.st_mode) or before.st_uid != os.geteuid()
                or before.st_mode & 0o077):
            raise ObservationError('session inventory directory must be owned and private')
        with os.scandir(sessions) as entries:
            names = sorted(entry.name for entry in islice(entries, plans.MAX_COMPONENTS + 1))
        if len(names) > plans.MAX_COMPONENTS:
            raise ObservationError('session inventory exceeds upgrade component capacity')
        for name in names:
            if len(name) != 16 or any(char not in '0123456789abcdef' for char in name):
                raise ObservationError('unrecognized session inventory entry')
            info = (sessions / name).lstat()
            if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_mode & 0o077):
                raise ObservationError('session inventory entry must be owned and private')
        after = sessions.lstat()
        stamp = lambda value: (value.st_dev, value.st_ino, value.st_mtime_ns, value.st_ctime_ns)
        if stamp(before) != stamp(after):
            raise ObservationError('session inventory changed during enumeration')
        return dict(names=names, identity=stamp(after))

    before = scan()
    memories = config.get('memory_services', {}).get('repositories', {})
    names = [] if before is None else before['names']
    if len(names) + len(memories) > plans.MAX_COMPONENTS:
        raise ObservationError('installation exceeds upgrade component capacity')
    selections = []
    for name in names:
        home = sessions / name
        record = session_service_artifacts.load(home)
        if record is None or record['state'] != 'installed':
            raise ObservationError('legacy or unfinished session requires an explicit upgrade adapter')
        if record['prefix'] != str(prefix):
            raise ObservationError('shared session inventory selects a different runtime prefix')
        if record['python'] != sys.executable:
            raise ObservationError('session interpreter mismatch: run preflight with the installed '
                                   'interpreter ' + record['python'])
        try:
            selected = session_service.Selection(prefix, home)
        except (OSError, ValueError) as exc:
            raise ObservationError('cannot verify saved session selection with interpreter '
                                   + sys.executable) from exc
        selections.append(('session', selected))
    for key in sorted(memories):
        record = memories[key]
        if record['state'] != 'installed' or record['backend'] not in ('systemd', 'launchd', 'manual'):
            raise ObservationError('unfinished memory requires an explicit upgrade adapter')
        try:
            selected = memory_service.Selection(prefix, record['common_directory'])
        except (OSError, ValueError) as exc:
            # Memory records do not separately retain the interpreter. A
            # template mismatch cannot be attributed to Python alone.
            raise ObservationError('cannot verify saved memory selection with interpreter '
                                   + sys.executable + '; check the installed interpreter and '
                                   'retained artifact/selection') from exc
        selections.append(('memory', selected))
    result = [session(selected) if kind == 'session' else memory(selected)
              for kind, selected in selections]
    # Native registration publication takes this same installation lock;
    # rechecks also refuse uncooperative changes rather than omitting them.
    if runtime_names.install_config(prefix) != config or scan() != before:
        raise ObservationError('installation selection changed during observation')
    for kind, selected in selections:
        if kind == 'session' and session_service_artifacts.load(selected.home) != selected.record:
            raise ObservationError('saved session selection changed during observation')
    return dict(version=1, prefix=str(prefix), installation=config,
                components=result)
