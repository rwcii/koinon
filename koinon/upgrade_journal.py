"""Private monotonic phase records for a future upgrade coordinator.

This records intent/completion, not evidence validity or permission to act. The
coordinator must hold its own operation lock across external actions, validate the
frozen plan and supplied evidence, and establish installation-wide exclusion.
Initialize only a new operation before publishing its active-installation marker;
resume must call read(), which refuses a missing journal rather than recreating it.
"""
from contextlib import contextmanager
import os

from koinon import durable_state
from koinon.participant_lock import file_lock
from koinon.upgrade_manifest import check_root, hex_digest, select_root

PHASES = ('prepared', 'quiescing_sessions', 'quiescing_memory', 'backing_up',
          'replacing', 'verifying_migration', 'starting_gated', 'verifying',
          'releasing', 'complete')
LAST_STEP = 2 * len(PHASES) - 1
RELEASE_STEP = 2 * PHASES.index('releasing') + 1


class JournalError(ValueError):
    pass


class Journal:
    def __init__(self, directory, plan_digest):
        if not hex_digest(plan_digest):
            raise JournalError('invalid frozen plan digest')
        self.directory = select_root(directory)
        self.plan_digest = plan_digest
        self.path = self.directory / 'phase.json'
        self.lock_path = self.directory / 'journal.lock'
        self._directory()

    def _directory(self):
        check_root(self.directory)
        info = self.directory.lstat()
        if info.st_mode & 0o077:
            raise JournalError('upgrade journal directory must be private')
        return info.st_dev, info.st_ino

    @contextmanager
    def _locked(self, *, operation=False):
        path = self.directory / 'coordinator.lock' if operation else self.lock_path
        code = 'upgrade_coordinator_busy' if operation else 'upgrade_journal_busy'
        directory = self._directory()
        with file_lock(path, code, None, timeout=0 if operation else 5) as fd:
            info, named = os.fstat(fd), path.lstat()
            if ((info.st_dev, info.st_ino) != (named.st_dev, named.st_ino)
                    or self._directory() != directory):
                raise JournalError('upgrade journal ownership changed')
            yield

    @contextmanager
    def operation(self):
        """Exclude another coordinator while permitting status and gate reads."""
        with self._locked(operation=True):
            yield

    def validate(self, value):
        if (not isinstance(value, dict) or set(value) != {'version', 'plan', 'step', 'receipts'}
                or type(value['version']) is not int or value['version'] != 1
                or value['plan'] != self.plan_digest
                or type(value['step']) is not int or not 0 <= value['step'] <= LAST_STEP
                or not isinstance(value['receipts'], list)
                or len(value['receipts']) != (value['step'] + 1) // 2
                or not all(hex_digest(item) for item in value['receipts'])):
            raise JournalError('invalid or mismatched upgrade journal')
        return value

    def _read(self):
        value = durable_state.read(self.path)
        if value is None:
            raise JournalError('upgrade journal missing; resume cannot initialize a new phase')
        value = self.validate(value)
        return durable_state.confirm(self.path, value)

    def read(self):
        with self._locked():
            return self._read()

    def initialize(self):
        """Prepare a new operation only; a retained journal is never reset."""
        with self._locked():
            value = durable_state.read(self.path)
            if value is not None:
                value = self.validate(value)
                return durable_state.confirm(self.path, value)
            value = dict(version=1, plan=self.plan_digest, step=0, receipts=[])
            durable_state.publish(self.path, value)
            return value

    def advance(self, expected, *, evidence=None):
        """Complete pending work with evidence, or record the next phase's intent.

        Lost replies are idempotent only for the identical expected predecessor
        and evidence. Other concurrent/stale transitions refuse and require reread.
        No external action belongs inside this journal's short publication lock.
        """
        expected = self.validate(expected)
        step = expected['step']
        if step == LAST_STEP:
            raise JournalError('upgrade journal is already complete')
        finishing = step % 2 == 0
        if finishing and not hex_digest(evidence) or not finishing and evidence is not None:
            raise JournalError('completion evidence required only for a pending phase')
        receipts = [*expected['receipts'], evidence] if finishing else list(expected['receipts'])
        desired = dict(version=1, plan=self.plan_digest, step=step + 1, receipts=receipts)
        with self._locked():
            current = self._read()
            if current == desired:
                return current
            if current != expected:
                raise JournalError('upgrade journal changed; reread before acting')
            durable_state.publish(self.path, desired)
            return desired

    def describe(self, value):
        value = self.validate(value)
        return dict(phase=PHASES[value['step'] // 2],
                    completion='complete' if value['step'] % 2 else 'pending',
                    release_decision_committed=value['step'] >= RELEASE_STEP,
                    finished=value['step'] == LAST_STEP)
