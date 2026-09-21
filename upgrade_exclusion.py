"""Plan-bound installation exclusion for the resumable upgrade operation.

Normal installation readers never accept the upgrading lifecycle. Only this
module's exact frozen-configuration validator permits coordinator access. Hold
operation() across coordinator actions; installation/component locks are short
and never held while waiting for children to exit.
"""
from contextlib import contextmanager, ExitStack
from pathlib import Path

import install_state
from participant_lock import file_lock
import runtime_names
import session_service_artifacts
from upgrade_journal import Journal, LAST_STEP
import upgrade_manifest as manifest
import upgrade_plan


class ExclusionError(ValueError):
    pass


def _marker(loaded):
    return dict(version=1, operation=loaded['plan']['directory'], plan=loaded['sha256'])


def _configuration(loaded):
    original = loaded['documents']['installation']
    if 'upgrade' in original:
        raise ExclusionError('installation already uses the reserved upgrade field')
    return dict(original, installation_state='upgrading', upgrade=_marker(loaded))


def read(prefix):
    """Read a valid active operation, or None for ordinary installed configuration.

    A gate already bound to an operation must reject None, not resume traffic.
    Invalid/missing evidence never constructs a replacement plan or journal.
    """
    def validate(value):
        if not isinstance(value, dict):
            raise ExclusionError('invalid installation configuration')
        if value.get('installation_state') != 'upgrading':
            return runtime_names.validate_install_config(value)
        marker = value.get('upgrade')
        if (not isinstance(marker, dict) or set(marker) != {'version', 'operation', 'plan'}
                or type(marker['version']) is not int or marker['version'] != 1
                or not isinstance(marker['operation'], str)
                or not manifest.hex_digest(marker['plan'])):
            raise ExclusionError('invalid upgrade exclusion marker')
        return value
    config = runtime_names._read_install_config(prefix, validate)
    if config.get('installation_state') != 'upgrading':
        return None
    marker = config['upgrade']
    loaded = upgrade_plan.load(Path(marker['operation']), marker['plan'])
    if (manifest.select_root(prefix) != Path(loaded['plan']['canonical_prefix'])
            or config != _configuration(loaded)):
        raise ExclusionError('upgrade marker differs from frozen installation')
    return loaded


def component_configuration(prefix, kind, record):
    """Revalidate the active plan before an owned component uses frozen inputs."""
    loaded = read(prefix)
    if loaded is None or not any(
            item['kind'] == kind and item['selection'] == record
            for item in loaded['documents']['components']['items']):
        raise ExclusionError('component is not selected by the active upgrade')
    return loaded['documents']['installation']


class Exclusion:
    def __init__(self, loaded):
        self.loaded = loaded
        self.prefix = Path(loaded['plan']['prefix'])
        self.original = loaded['documents']['installation']
        self.marked = _configuration(loaded)
        self.journal = Journal(loaded['plan']['directory'], loaded['sha256'])

    def _validate(self, value):
        if value != self.original and value != self.marked:
            raise ExclusionError('installation changed from the frozen upgrade plan')
        return value

    def activate(self):
        """Exclude ordinary admission before any shutdown; repeat all flushes."""
        with install_state.locked(self.prefix, validator=self._validate) as installed:
            phase = self.journal.read()
            if installed.config == self.original and phase['step'] != 0:
                raise ExclusionError('upgrade marker missing after prepared intent')
            with ExitStack() as locks:
                # Serialize an ensure that already read the old installation.
                # Existing session admission takes lifecycle then registration;
                # memory admission takes installation then manager.
                components = self.loaded['documents']['components']['items']
                for component in sorted(components, key=lambda item: (item['kind'], str(item['selection']))):
                    record = component['selection']
                    if component['kind'] == 'session':
                        home = manifest.check_root(record['state_directory'])
                        for name in ('lifecycle.lock', 'registration.lock'):
                            locks.enter_context(session_service_artifacts.locked(home / name))
                    else:
                        home = manifest.check_root(record['service_directory'])
                        locks.enter_context(file_lock(home / 'manager.lock', 'manager_busy', None))
                if installed.config == self.marked:
                    installed.confirm()
                else:
                    installed.merge(dict(installation_state='upgrading', upgrade=_marker(self.loaded)))
            return self.journal.read()

    def verify(self):
        with install_state.locked(self.prefix, validator=self._validate) as installed:
            phase = self.journal.read()
            if installed.config != self.marked:
                raise ExclusionError('upgrade exclusion is not active')
            installed.confirm()
            return phase

    def finish(self):
        """Restore ordinary admission only after recorded final readiness."""
        with install_state.locked(self.prefix, validator=self._validate) as installed:
            if self.journal.read()['step'] != LAST_STEP:
                raise ExclusionError('upgrade completion evidence is not committed')
            # Replace the whole dictionary so only the coordinator's marker is
            # removed; every original opaque field is preserved exactly.
            installed.replace(self.original)


@contextmanager
def operation(directory, digest):
    loaded = upgrade_plan.load(directory, digest)
    exclusion = Exclusion(loaded)
    with exclusion.journal.operation():
        # Reopen after acquiring ownership; an unlocked observation is not a lease.
        yield Exclusion(upgrade_plan.load(directory, digest))
