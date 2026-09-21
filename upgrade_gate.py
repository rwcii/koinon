"""Runtime admission bound to one frozen upgrade plan and service generation."""
import asyncio
from pathlib import Path

import runtime_names
import upgrade_exclusion
from upgrade_journal import Journal, RELEASE_STEP
import upgrade_manifest as manifest


class GateError(ValueError):
    pass


class Gate:
    def __init__(self, loaded, kind, root, generation):
        self.prefix = Path(loaded['plan']['prefix'])
        self.operation = loaded['plan']['directory']
        self.plan = loaded['sha256']
        self.generation = generation
        self.kind, self.root = kind, manifest.check_root(root)
        self.marked = upgrade_exclusion._configuration(loaded)
        self.journal = Journal(self.operation, self.plan)
        self._released = False
        if loaded['phase']['step'] < 10:
            raise GateError('upgrade has not authorized new-runtime startup')
        matches = []
        for component in loaded['documents']['components']['items']:
            record = component['selection']
            if kind in ('bridge', 'notifier') and component['kind'] == 'session':
                expected = record['state_directory']
            elif kind == 'memory' and component['kind'] == 'memory':
                expected = record['service_directory']
            else:
                continue
            if manifest.check_root(expected) == self.root:
                matches.append(component)
        if len(matches) != 1:
            raise GateError('runtime state is not selected by this upgrade')
        self.component = matches[0]

    def released(self):
        if self._released:
            return True
        def validate(value):
            if value != self.marked:
                raise GateError('upgrade marker changed or disappeared before release')
            return value
        if runtime_names._read_install_config(self.prefix, validate) != self.marked:
            raise GateError('upgrade marker missing before release')
        self._released = self.journal.read()['step'] >= RELEASE_STEP
        return self._released

    def status(self):
        return dict(plan=self.plan, operation=self.operation, generation=self.generation,
                    released=self.released())

    def authorize_inventory(self, request):
        if (set(request) != {'op', 'plan', 'generation'} or request['plan'] != self.plan
                or request['generation'] != self.generation or self.released()):
            raise GateError('upgrade inventory requires the selected gated generation')

    async def wait(self, stop):
        while not stop.is_set():
            if await asyncio.to_thread(self.released):
                return True
            try:
                await asyncio.wait_for(stop.wait(), .25)
            except asyncio.TimeoutError:
                pass
        return False


def select(prefix, kind, root, generation):
    loaded = upgrade_exclusion.read(prefix)
    return None if loaded is None else Gate(loaded, kind, root, generation)
