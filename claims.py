"""Advisory lease primitives staged for work-items v1.

These functions never access the filesystem or grant permission to perform work.
LeaseEngine uses its caller's transaction and mandatory admission callback. It is
not yet wired into memory.py; stream and budget integration must land together.
"""
from dataclasses import dataclass
import math
import unicodedata

import work_schema

MAX_BUNDLES = 16
MAX_RESOURCES = 8
DEFAULT_LEASE = 900
MIN_LEASE = 60
MAX_LEASE = 3600
MAX_INTEGER = 2**63 - 1


class ClaimError(ValueError):
    def __init__(self, code, message, **details):
        super().__init__(message)
        self.code, self.details = code, details


def integer(value, field, minimum=1, maximum=MAX_INTEGER):
    if type(value) is not int or not minimum <= value <= maximum:
        raise ClaimError('invalid_request', field + ' must be a bounded integer')
    return value


def timestamp(value):
    if type(value) not in (int, float):
        raise ClaimError('invalid_request', 'time must be a finite number')
    try:
        valid = math.isfinite(value) and 0 <= value <= MAX_INTEGER
    except OverflowError:
        valid = False
    if not valid:
        raise ClaimError('invalid_request', 'time must be a finite nonnegative number')
    return value


def text(value, field, limit=512):
    try:
        valid = isinstance(value, str) and 0 < len(value.encode('utf-8')) <= limit
    except UnicodeEncodeError:
        valid = False
    if not valid:
        raise ClaimError('invalid_request', field + ' must be bounded UTF-8 text')
    return value


def consumer_key(value):
    text(value, 'consumer')
    if len(value) > 128:
        raise ClaimError('invalid_request', 'consumer exceeds 128 characters')
    return value


def work_key(value):
    if (not isinstance(value, str) or len(value) != 32
            or any(c not in '0123456789abcdef' for c in value)):
        raise ClaimError('invalid_request', 'invalid work ID')
    return value


def resource(kind, key):
    if kind not in ('writer', 'path', 'exact'):
        raise ClaimError('invalid_request', 'unknown resource kind')
    text(key, 'resource')
    if '\\' in key or any(unicodedata.category(c) == 'Cc' for c in key):
        raise ClaimError('invalid_request', 'ambiguous resource spelling')
    if kind == 'writer':
        work_key(key)
    elif kind == 'path' and key != '.':
        if any(part in ('', '.', '..') for part in key.split('/')):
            raise ClaimError('invalid_request', 'path must use relative components')
    return kind, key


def overlaps(left, right):
    """Compare validated keys by namespace and path components, without resolution."""
    a_kind, a = resource(*left)
    b_kind, b = resource(*right)
    if a_kind != b_kind:
        return False
    if a_kind != 'path':
        return a == b
    return a == b or a == '.' or b == '.' or a.startswith(b + '/') or b.startswith(a + '/')


def bundle(work_id, optional):
    writer = resource('writer', work_id)
    if not isinstance(optional, (list, tuple)) or len(optional) > MAX_RESOURCES:
        raise ClaimError('invalid_request', 'at most eight optional resources are allowed')
    result = [writer]
    for item in optional:
        if (not isinstance(item, (list, tuple)) or len(item) != 2
                or item[0] not in ('path', 'exact')):
            raise ClaimError('invalid_request', 'optional resources require path or exact keys')
        key = resource(*item)
        if key in result:
            raise ClaimError('invalid_request', 'duplicate resource')
        result.append(key)
    return tuple(result)


@dataclass(frozen=True)
class Debt:
    overdue: int
    end: int

    @property
    def pages(self):
        return 256 * self.overdue + 384 * self.end

    @property
    def logical_bytes(self):
        return 48 * 1024 * (self.overdue + self.end)

    @property
    def event_slots(self):
        return self.overdue + self.end

    @property
    def replay_slots(self):
        return self.end


def debt(db):
    """Outstanding promises come from durable flags, including overdue deadlines."""
    row = db.execute('SELECT coalesce(sum(overdue_credit),0), '
                     'coalesce(sum(end_credit),0) FROM claim_bundles').fetchone()
    return Debt(*row)


class LeaseEngine:
    """Bounded SQL primitives, not a standalone work command or transaction owner.

    admit(before, after, control) must enforce shared slot, byte and page budgets.
    It runs after the mutation, before the caller commits all work/stream/replay
    changes. The caller MUST roll back on any exception, including admission
    failure, and perform its final whole-transaction accounting before commit.
    The work-command caller must validate item existence, lifecycle, revision and
    progress epoch in that same transaction, and write the matching work/event
    changes. This reusable engine does not independently create a work record.
    """
    def __init__(self, db, admit):
        if not callable(admit):
            raise TypeError('a storage admission callback is required')
        self.db, self.admit = db, admit

    def _transaction(self):
        if not self.db.in_transaction:
            raise RuntimeError('claim mutation requires a caller-owned transaction')
        return debt(self.db)

    def _admit(self, before, control=False):
        self.admit(before, debt(self.db), control)

    def acquire(self, work_id, consumer, progress_epoch, optional=(), *, now,
                duration=DEFAULT_LEASE):
        keys = bundle(work_id, optional)
        consumer_key(consumer)
        integer(progress_epoch, 'progress_epoch')
        timestamp(now)
        integer(duration, 'duration', MIN_LEASE, MAX_LEASE)
        timestamp(now + duration)
        before = self._transaction()
        rows = self.db.execute('SELECT generation,work_id,consumer,expires_at,active '
                               'FROM claim_bundles ORDER BY generation').fetchall()
        for generation, held_work, owner, expiry, active in rows:
            if active and expiry > now:
                held = self.db.execute('SELECT kind,resource FROM claim_resources '
                                       'WHERE generation=? ORDER BY ordinal', (generation,))
                for old in held:
                    for key in keys:
                        if overlaps(key, old):
                            raise ClaimError('claim_conflict', 'resource has a live owner',
                                             work_id=held_work, consumer=owner,
                                             generation=generation, expires_at=expiry,
                                             resource=old)
            if held_work == work_id:
                raise ClaimError('claim_capacity', 'retained bundle requires expiry reconciliation '
                                 'and inactive-bundle reclamation; retry after maintenance succeeds')
        if len(rows) >= MAX_BUNDLES:
            raise ClaimError('claim_capacity', 'retained claim bundle limit reached')
        generation = work_schema.allocate(self.db, 'claim_generation')
        self.db.execute('INSERT INTO claim_bundles '
                        '(generation,work_id,consumer,revision,issued_at,renewed_at,expires_at,'
                        'progress_epoch) VALUES (?,?,?,1,?,?,?,?)',
                        (generation, work_id, consumer, now, now, now + duration, progress_epoch))
        self.db.executemany('INSERT INTO claim_resources VALUES (?,?,?,?)',
                            [(generation, i, kind, key) for i, (kind, key) in enumerate(keys)])
        self._admit(before)
        return dict(generation=generation, revision=1, expires_at=now + duration)

    def owner(self, work_id, consumer, generation, *, now):
        work_key(work_id)
        consumer_key(consumer)
        integer(generation, 'generation')
        timestamp(now)
        row = self.db.execute('SELECT revision,expires_at,progress_epoch FROM claim_bundles '
                              'WHERE work_id=? AND consumer=? AND generation=? AND active=1',
                              (work_id, consumer, generation)).fetchone()
        if row is None or row[1] <= now:
            raise ClaimError('stale_claim', 'claim generation is not currently owned')
        return dict(revision=row[0], expires_at=row[1], progress_epoch=row[2])

    def renew(self, work_id, consumer, generation, revision, *, now,
              duration=DEFAULT_LEASE):
        integer(revision, 'revision')
        integer(duration, 'duration', MIN_LEASE, MAX_LEASE)
        timestamp(now)
        timestamp(now + duration)
        before = self._transaction()
        current = self.owner(work_id, consumer, generation, now=now)
        if current['revision'] != revision:
            raise ClaimError('revision_conflict', 'claim revision changed')
        if revision == MAX_INTEGER:
            raise ClaimError('capacity', 'claim revision exhausted')
        self.db.execute('UPDATE claim_bundles SET revision=?,renewed_at=?,expires_at=? '
                        'WHERE generation=?', (revision + 1, now, now + duration, generation))
        self._admit(before)
        return dict(generation=generation, revision=revision + 1, expires_at=now + duration)

    def release(self, work_id, consumer, generation, *, now):
        before = self._transaction()
        self.owner(work_id, consumer, generation, now=now)
        self._end(generation)
        self._admit(before, control=True)

    def expire(self, generation, *, now):
        integer(generation, 'generation')
        timestamp(now)
        before = self._transaction()
        row = self.db.execute('SELECT expires_at,active FROM claim_bundles '
                              'WHERE generation=?', (generation,)).fetchone()
        if row is None or not row[1] or row[0] > now:
            return False
        self._end(generation)
        self._admit(before, control=True)
        return True

    def _end(self, generation):
        # Only equal-size boolean fields: never touch an indexed column here.
        self.db.execute('UPDATE claim_bundles SET active=0,overdue_credit=0,end_credit=0 '
                        'WHERE generation=?', (generation,))

    def reclaim_one(self):
        before = self._transaction()
        row = self.db.execute('SELECT generation FROM claim_bundles WHERE active=0 '
                              'ORDER BY generation LIMIT 1').fetchone()
        if row is None:
            return False
        self.db.execute('DELETE FROM claim_resources WHERE generation=?', row)
        self.db.execute('DELETE FROM claim_bundles WHERE generation=?', row)
        # Deletion can allocate pages; it is ordinary admission, not a free control.
        self._admit(before)
        return True
