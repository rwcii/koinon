"""Recoverable native session publication. No manager calls or process adoption.

The containing session registration must already exist. Publication locks follow
installation, lifecycle, registration, supervisor order; public integration must
call this before acquiring those locks itself.
"""
from contextlib import contextmanager
import copy
import os
from pathlib import Path
import stat

import durable_state
import install_state
import memory_service_artifacts as files
from participant_lock import file_lock, OwnershipError
import platform_support
import runtime_names
import session_service_config as configuration
from work_policy import absolute_path


@contextmanager
def locked(path):
    try:
        with file_lock(path, 'session_configuration_busy', None) as fd:
            held, current = os.fstat(fd), path.lstat()
            if (held.st_dev, held.st_ino) != (current.st_dev, current.st_ino):
                raise ValueError('session configuration lock changed')
            yield
    except OwnershipError as exc:
        error = ValueError('session lock unavailable or unsafe; preserve and inspect ' + str(path))
        error.paths = (str(path),)
        raise error from exc


def private_home(home):
    files._parents(home / 'native-service.json')
    info = home.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise ValueError('session directory must be private and owned')


def load(home):
    home = absolute_path(str(home))
    private_home(home)
    record = durable_state.read(home / 'native-service.json')
    if record is not None:
        configuration.validate(record)
        if record['state_directory'] != str(home):
            raise ValueError('selection belongs to another session directory')
    return record


def expected(record):
    configuration.validate(record)
    if record['backend'] == 'manual':
        raise ValueError('manual selection has no managed session artifact')
    content = platform_support.session_service_artifact(record)
    if len(content) > files.MAX_ARTIFACT_BYTES or files.digest(content) != record['artifact_digest']:
        raise ValueError('native session artifact digest differs from command')
    return content


def inputs(record):
    prefix, home = Path(record['prefix']), Path(record['state_directory'])
    files._parents(prefix / 'install.json')
    config = runtime_names.install_config(prefix)
    private_home(home)
    registration = durable_state.read(home / 'session.json')
    configuration.verify(record, config, registration)
    return config, registration


@contextmanager
def boundary(record, observed_artifact=None):
    try:
        yield
    except durable_state.StateReadBusyError:
        raise
    except (OSError, ValueError) as exc:
        paths = list(getattr(exc, 'paths', ()))
        if isinstance(record, dict):
            home = record.get('state_directory')
            if isinstance(home, str):
                paths.append(str(Path(home) / 'native-service.json'))
            if isinstance(record.get('artifact'), str):
                paths.append(record['artifact'])
        if observed_artifact is not None:
            paths.append(str(observed_artifact))
        error = ValueError(str(exc))
        error.paths = tuple(dict.fromkeys(str(path) for path in paths))
        raise error from exc


def verify_owned(record, *, observed_artifact=None):
    """Verify saved selection and literal disk evidence; never infer live health."""
    with boundary(record, observed_artifact):
        content = expected(record)
        inputs(record)
        if record['state'] != 'installed' or load(record['state_directory']) != record:
            raise ValueError('native session has no completed owning selection')
        path = Path(record['artifact'])
        if observed_artifact is not None and str(observed_artifact) != str(path):
            raise ValueError('loaded session artifact differs from selection')
        if files._read(path) != content:
            raise ValueError('owned session artifact missing or changed')
        return record

def verify_loaded(record, observed_artifact):
    with boundary(record, observed_artifact):
        verify_owned(record)
        path, expected_path = absolute_path(str(observed_artifact)), Path(record['artifact'])
        if path == expected_path:
            return record
        if record['backend'] != 'systemd':
            raise ValueError('loaded session artifact differs from selection')
        files.verify_loader_link(path, expected_path)
        return verify_owned(record)


def prepare(desired):
    content = expected(desired)
    if desired['state'] != 'installed':
        raise ValueError('publication requires a completed desired selection')
    inputs(desired)
    home, path = Path(desired['state_directory']), Path(desired['artifact'])
    # The installer prepares this private directory, not this publication step.
    private_home(path.parent)
    old = load(home)
    current = files._read(path)
    if old is None:
        if current is not None:
            raise ValueError('session artifact exists without ownership')
    else:
        base = {key: value for key, value in old.items() if key in configuration.BASE_FIELDS}
        base['state'] = 'installed'
        if base != desired or old['state'] == 'removing':
            raise ValueError('session selection requires explicit reconciliation')
        if old['state'] == 'installed' and current != content:
            raise ValueError('owned session artifact missing or changed')
        if old['state'] == 'pending' and current not in (None, content):
            raise ValueError('unexpected pending session artifact; evidence retained')
    return home, path, content, old, current


def publish(desired):
    """New selection or exact repeat, with durable intent preceding publication.

    All directories and session.json must already exist. No executable is started,
    no manager queried, and no existing unregistered artifact is adopted.
    """
    desired = copy.deepcopy(desired)
    home, _, _, _, _ = prepare(desired)
    with install_state.locked(desired['prefix']), locked(home / 'lifecycle.lock'), \
            locked(home / 'registration.lock'), locked(home / 'supervisor.lock'):
        home, path, content, old, before = prepare(desired)
        if old is not None and old['state'] == 'installed':
            return desired
        if old is None:
            durable_state.publish(home / 'native-service.json', dict(desired, state='pending',
                                  before_digest=None, after_digest=desired['artifact_digest']))
        if before != content:
            files._replace(path, content, before)
        else:
            platform_support.sync_state_directory(path.parent)
        if files._read(path) != content:
            raise ValueError('session artifact changed before completion')
        durable_state.publish(home / 'native-service.json', desired)
        return desired
