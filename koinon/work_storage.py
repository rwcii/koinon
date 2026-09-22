"""Work-row accounting used by the shared memory transaction boundary.

No connection ownership, commits, migration, or activation lives here. The same
row charge is used for projections and stored usage; snapshots retain the memory
service's existing accounting and do not count toward the work sub-budget.
"""
from koinon import claims

MAX_LOGICAL_BYTES = 12 * 1024 * 1024
MAX_EVENTS = 2048
MAX_ITEMS = 128
MAX_SCOPES = 1024
MAX_SCOPES_PER_ITEM = 64
ROW_OVERHEAD = 512
REPLAY_OVERHEAD = 128
TABLES = ('work_items', 'work_scope_revisions', 'claim_bundles',
          'claim_resources', 'work_events')


def payload_bytes(values):
    # Numeric/NULL fields are covered by fixed metadata overhead, as with the
    # existing note accounting. SQLite returns text and BLOBs as str and bytes.
    return sum(len(value.encode('utf-8')) if isinstance(value, str) else len(value)
               for value in values if isinstance(value, (str, bytes)))


def row_charge(values):
    return ROW_OVERHEAD + payload_bytes(values)


def replay_extra(values):
    """Operation and result, in addition to the unchanged key+64 note charge."""
    return REPLAY_OVERHEAD + payload_bytes(values)


def sql_payload_bytes(columns):
    """SQL equivalent of payload_bytes, including embedded NULs and UTF-8.

    Columns come from the owned schema, never requests. typeof keeps numeric and
    NULL fields in the fixed metadata allowance just like the Python projection.
    """
    terms = []
    for column in columns:
        quoted = '"' + column.replace('"', '""') + '"'
        terms.append(f"CASE WHEN typeof({quoted}) IN ('text','blob') "
                     f'THEN length(cast({quoted} AS BLOB)) ELSE 0 END')
    return ' + '.join(terms) or '0'


def usage(db, entry_charge, replay_charge):
    counts, logical = {}, 0
    for table in TABLES:
        columns = [row[1] for row in db.execute('PRAGMA table_info(' + table + ')')]
        count, stored = db.execute(
            'SELECT count(*),coalesce(sum(? + ' + sql_payload_bytes(columns)
            + '),0) FROM ' + table, (ROW_OVERHEAD,)).fetchone()
        logical += stored
        counts[table] = count
    scopes_per_item = db.execute(
        'SELECT coalesce(max(n),0) FROM '
        '(SELECT count(*) n FROM work_scope_revisions GROUP BY work_id)').fetchone()[0]
    table_bytes = logical
    logical += db.execute('SELECT coalesce(sum(? + '
        + sql_payload_bytes(('body', 'path', 'author', 'scope_target', 'consumer'))
        + "),0) FROM entries WHERE type='work-event'", (entry_charge(()),)).fetchone()[0]
    replay_bytes = str(REPLAY_OVERHEAD) + ' + ' + sql_payload_bytes(('operation', 'result'))
    extra, replay_total = db.execute('SELECT coalesce(sum(' + replay_bytes
        + '),0),coalesce(sum(? + ' + sql_payload_bytes(('key',)) + ' + ' + replay_bytes
        + "),0) FROM idem WHERE operation<>'note'", (replay_charge(()),)).fetchone()
    logical += replay_total
    return dict(work_logical=logical, work_replay_extra=extra,
                work_table_bytes=table_bytes,
                work_counts=counts, work_scopes_per_item=scopes_per_item)


def debt(db):
    """Do not filter expired bundles: their unrecorded obligations remain funded."""
    return claims.debt(db)
