"""Installer selection and staging for repository-scoped runtime components."""
import copy
import os
import stat
import tempfile
from pathlib import Path
import sys

from koinon import install_state
from koinon import memory_service_artifacts as artifacts
from koinon import memory_service_config as memory_config
from koinon import platform_support
from koinon import runtime_names


def runtime_preflight(prefix, source, files, config):
    """Never overwrite unsafe paths or upgrade selected native runtimes in place."""
    selected = bool(config.get('memory_services', {}).get('repositories')) or 'session_backend' in config
    for name in files:
        destination = Path(prefix) / name
        try:
            info = destination.lstat()
        except FileNotFoundError:
            continue
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o022 or info.st_nlink != 1):
            raise ValueError('unsafe runtime destination: ' + str(destination))
        if selected and destination.read_bytes() != (Path(source) / name).read_bytes():
            raise ValueError('selected runtime change requires coordinated upgrade: ' + str(destination))


def memory_selection(prefix, repository, state_root, backend, config):
    """Resolve one desired selection without creating files or querying a manager."""
    prefix, state_root = Path(prefix), Path(state_root)
    key, selected = memory_config.selection(repository, state_root)
    inventory = config.get('memory_services', dict(version=1, repositories={}))
    memory_config.validate(inventory)
    saved = inventory['repositories'].get(key)
    if saved is not None:
        memory_config.verify_selection(saved)
        if (saved['identity_digest'] != selected['identity_digest']
                or saved['state_root'] != str(state_root)
                or backend is not None and saved['backend'] != backend
                or saved['state'] == 'removing'):
            raise ValueError('saved memory selection requires explicit reconciliation')
        desired = {field: copy.deepcopy(saved[field]) for field in memory_config.base_fields(saved)}
        desired['state'] = 'installed'
        if desired['backend'] != 'manual':
            artifacts._prepare(prefix, sys.executable, desired, config)
        return key, desired
    backend = backend or platform_support.installation_backend()
    name = memory_config.artifact_name(key, backend)
    home = platform_support.memory_artifact_directory(prefix, backend)
    selected.update(backend=backend, state='installed', artifact=str(home / name) if name else None,
                    artifact_digest='0' * 64 if name else None)
    if backend == 'systemd':
        selected['template_version'] = 2
    elif backend == 'launchd':
        selected['manager_domain'] = f'gui/{os.geteuid()}'
    if name:
        selected['artifact_digest'] = artifacts.digest(
            platform_support.memory_service_artifact(prefix, sys.executable, key, selected))
    memory_config.admit(inventory, selected)
    if name:
        artifact = Path(selected['artifact'])
        artifacts.preflight_registration(selected, (artifact,))
        if runtime_names.present(artifact):
            raise ValueError('memory artifact exists without an owning selection: ' + str(artifact))
    return key, selected



def prepare_artifact_directory(desired):
    if desired['backend'] == 'manual':
        return
    artifact = Path(desired['artifact'])
    artifacts.preflight_registration(desired, (artifact,))
    for parent in reversed(artifact.parents):
        if not runtime_names.present(parent):
            parent.mkdir(mode=0o700, exist_ok=True)
    artifacts._parents(artifact)


def copy_runtime(source, destination):
    """Publish a complete module atomically; exact repeats never truncate live files."""
    data = Path(source).read_bytes()
    destination = Path(destination)
    if destination.exists() and destination.read_bytes() == data:
        return
    fd, temporary = tempfile.mkstemp(prefix='.runtime-', dir=destination.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            platform_support.sync_state_file(stream.fileno())
        os.replace(temporary, destination)
        platform_support.sync_state_directory(destination.parent)
    finally:
        Path(temporary).unlink(missing_ok=True)


def stage_memory(prefix, desired):
    """Publish a selected memory component, without activation or manager queries.

    The caller must release its installation lock before entering this function.
    Publication revalidates the complete saved configuration under that lock.
    """
    if desired['backend'] == 'manual':
        with install_state.locked(prefix) as state:
            inventory = state.config.get('memory_services', dict(version=1, repositories={}))
            state.merge(dict(memory_services=memory_config.admit(inventory, desired)))
        return desired
    prepare_artifact_directory(desired)
    return artifacts.publish(prefix, sys.executable, desired)
