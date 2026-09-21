"""Exact owned component removal while retaining stores and recovery evidence."""
import copy
from pathlib import Path
import sys

import memory_service
import memory_service_artifacts as artifacts
from participant_lock import file_lock
from peer_transport import private_dir
import platform_support
import runtime_names


def remove_memory(prefix, installed, key):
    """Caller holds the installation lock for the entire selected operation."""
    record = installed.config['memory_services']['repositories'][key]
    selection = memory_service.Selection(prefix, record['common_directory'], removing=True)
    private_dir(selection.home)
    with file_lock(selection.home / 'manager.lock', 'manager_busy', None):
        if record['backend'] == 'manual':
            owner = memory_service.read_record(selection, selection.owner_path)
            if owner is not None:
                memory_service.stop(selection)
            elif runtime_names.present(platform_support.control_socket_path(selection.home)):
                raise memory_service.RunnerError('ownership_unknown')
        elif record['state'] == 'installed':
            memory_service.deactivate_owned(selection)
            removing = dict(record, state='removing', before_digest=record['artifact_digest'], after_digest=None)
            artifacts._save(installed, key, removing)
            record = removing
        else:
            # A durable removing record is written only after deregistration and
            # confirmed exit. Revalidate both facts before resuming artifact removal.
            if memory_service.manager_observation(selection)['status'] != 'absent':
                raise memory_service.RunnerError('manager_observation_unknown')
            owner = memory_service.read_record(selection, selection.owner_path)
            if owner is not None:
                memory_service.stop(selection)
            elif runtime_names.present(platform_support.control_socket_path(selection.home)):
                raise memory_service.RunnerError('ownership_unknown')
        if record['backend'] != 'manual':
            artifacts.verify_removing(prefix, sys.executable, record)
            path = Path(record['artifact'])
            before = artifacts._read(path)
            if before is not None:
                identity = path.lstat()
                if artifacts._read(path) != before:
                    raise ValueError('memory artifact changed before removal')
                current = path.lstat()
                if (current.st_dev, current.st_ino) != (identity.st_dev, identity.st_ino):
                    raise ValueError('memory artifact replaced before removal')
                path.unlink()
                platform_support.sync_state_directory(path.parent)
        inventory = copy.deepcopy(installed.config['memory_services'])
        del inventory['repositories'][key]
        installed.merge(dict(memory_services=inventory))
        return dict(status='removed', retained=record['service_directory'])


def remove_session(prefix, home):
    """Caller holds the installation lock; keep the removal record with session data."""
    import durable_state
    import session_observation
    import session_service
    import session_service_artifacts as session_artifacts
    import session_service_manager as manager
    from session_supervisor_state import alive_state
    home = Path(home)
    with session_artifacts.locked(home / 'lifecycle.lock'), session_artifacts.locked(home / 'registration.lock'):
        record = session_artifacts.load(home)
        selection = session_service.Selection(prefix, home, removing=True)
        if record['state'] == 'installed':
            manager.deactivate_locked(selection, allow_unstarted=True)
            record = dict(record, state='removing', before_digest=record['artifact_digest'], after_digest=None)
            durable_state.publish(home / 'native-service.json', record)
        else:
            if manager.observation(selection)['status'] != 'absent':
                raise session_service.ServiceError('session_ownership_unknown')
            owner = selection.records.read()
            if owner is not None:
                if (alive_state(owner) != 'dead' or owner['spawn_pending'] is not None
                        or any(child is not None and alive_state(child) != 'dead'
                               for child in owner['children'].values())):
                    raise session_service.ServiceError('session_shutdown_unconfirmed')
            elif any(session_observation.endpoint_present(root) for root in (home, home / 'notifier')):
                raise session_service.ServiceError('session_ownership_unknown')
        session_artifacts.verify_removing(record)
        path = Path(record['artifact'])
        before = artifacts._read(path)
        if before is not None:
            identity = path.lstat()
            if artifacts._read(path) != before:
                raise ValueError('session artifact changed before removal')
            current = path.lstat()
            if (current.st_dev, current.st_ino) != (identity.st_dev, identity.st_ino):
                raise ValueError('session artifact replaced before removal')
            path.unlink()
            platform_support.sync_state_directory(path.parent)
        # Retain removal provenance with the inbox. Ordinary ensure cannot
        # silently turn an interrupted uninstall back into an active job.
        return dict(status='removed', retained=str(home))
