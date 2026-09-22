"""Bounded worker-owned work maintenance and content-free diagnostic snapshots."""
import sqlite3
import threading
import time

from koinon import claims
from koinon import work_items

INTERVAL = 30
MAX_TRANSITIONS = 32


class MaintenanceState:
    """Only bounded diagnostic values cross from the database thread to the loop."""
    def __init__(self):
        self._lock = threading.Lock()
        self._state = dict(enabled=None, last_successful_sweep=None, observed_at=None,
                           pending_due=None, expired_items=None, inactive_bundles=None,
                           fault=None, fault_at=None, skipped_submissions=0)

    def update(self, **values):
        with self._lock:
            self._state.update(values)

    def skipped(self):
        with self._lock:
            self._state['skipped_submissions'] += 1

    def snapshot(self):
        with self._lock:
            return dict(self._state)


class WorkMaintenance:
    def __init__(self, store, error_factory, state):
        self.store, self.error, self.state = store, error_factory, state
        self.work = work_items.WorkItems(store, error_factory)

    def observe(self, now=None):
        now = time.time() if now is None else now
        if self.store.accounting_version() < 5:
            self.state.update(enabled=False, observed_at=now, pending_due=0,
                              expired_items=0, inactive_bundles=0)
        else:
            counts = self.work.maintenance_counts(now)
            self.state.update(enabled=True, observed_at=now, **counts)
        return self.state.snapshot()

    def record_fault(self, exc, now):
        if isinstance(exc, sqlite3.ProgrammingError):
            code = 'internal_error'
        elif isinstance(exc, (sqlite3.Error, OSError)):
            code = 'storage_error'
        else:
            code = getattr(exc, 'database_fault', None) or getattr(exc, 'code', 'internal_error')
        self.state.update(fault=code, fault_at=now)

    def diagnostics(self):
        try:
            return self.observe()
        except Exception as exc:
            self.record_fault(exc, time.time())
            return self.state.snapshot()

    def sweep(self, now=None):
        """One ordinary worker job; each transition/cleanup owns its transaction."""
        clock = time.time if now is None else lambda: now
        now = clock()
        try:
            observed = self.observe(now)
            if not observed['enabled']:
                return observed
            for work_id in self.work.due_targets(now, MAX_TRANSITIONS):
                # This is also the validated mutation-boundary transition function.
                self.work.reconcile(work_id, now)
            if self.work.engine().inactive_count():
                with self.store.transaction(control=True):
                    self.work.engine().reclaim_one(control=True)
            self.work.reclaim_finished_one(now)
            self.observe(now)
            self.state.update(last_successful_sweep=clock(), fault=None, fault_at=None)
            return self.state.snapshot()
        except Exception as exc:
            self.record_fault(exc, clock())
            # Re-observe partial batch progress when readable; never obscure the
            # original failure if the diagnostic query also fails.
            try:
                self.observe(now)
            except Exception:
                pass
            if isinstance(exc, claims.ClaimError):
                raise self.error(exc.code, str(exc)) from None
            raise
