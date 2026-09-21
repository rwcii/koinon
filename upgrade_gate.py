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
        self.loaded = loaded
        self.prefix = Path(loaded['plan']['prefix'])
        self.operation = loaded['plan']['directory']
        self.plan = loaded['sha256']
        self.generation = generation
        self.kind, self.root = kind, manifest.check_root(root)
        self.marked = upgrade_exclusion._configuration(loaded)
        self.journal = Journal(self.operation, self.plan)
        self._released = False
        creation_phase = self.journal.read()
        self.post_release_start = creation_phase['step'] >= RELEASE_STEP
        if creation_phase['step'] < 10:
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
        if self.post_release_start and not self.component['running']:
            raise GateError('originally inactive component remains stopped during release')
        self.component_index = loaded['documents']['components']['items'].index(self.component)

    def released(self):
        if self._released:
            return True
        def validate(value):
            if value != self.marked:
                raise GateError('upgrade marker changed or disappeared before release')
            return value
        if runtime_names._read_install_config(self.prefix, validate) != self.marked:
            raise GateError('upgrade marker missing before release')
        phase = self.journal.read()
        if phase['step'] >= RELEASE_STEP:
            import upgrade_release
            included = upgrade_release.member(self.loaded, phase, self.component_index, self.kind, self.generation)
            if not self.post_release_start and not included:
                # Keep this retryable at child entrypoints (currently exit 1),
                # never permanent configuration exit 78. Its successor can start
                # after release and supply live readiness without comparison.
                raise GateError('this child generation was not verified for upgrade release')
            self._released = True
        return self._released

    def status(self):
        return dict(plan=self.plan, operation=self.operation, generation=self.generation,
                    released=self.released(), post_release_start=self.post_release_start)

    def authorize_inventory(self, request):
        expected = {'op', 'plan', 'generation'}
        if self.kind == 'memory':
            import memory_service_config
            expected.add('repo')
            repo, _ = memory_service_config.identity(self.component['selection']['common_directory'])
            if request.get('repo') != repo:
                raise GateError('upgrade inventory requires the selected repository')
        if (set(request) != expected or request['plan'] != self.plan
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
