"""Declared memory migration expectations built only on disposable backup copies.

The live gated inventory must match this expectation exactly. Existing business
rows and metadata are checked independently of the migration's new tables/columns.
Search initialization is deferred by the gated runtime and is not an exception.
"""
import hashlib

import memory
from koinon import work_schema
from koinon import upgrade_backup_inventory
from koinon import upgrade_inventory as inventory


class MigrationError(ValueError):
    pass


def _projection(db, table, columns):
    rows, total = [], 0
    sql = 'SELECT ' + ','.join(inventory.identifier(name) for name in columns)
    sql += ' FROM ' + inventory.identifier(table) + ' LIMIT ?'
    for row in db.execute(sql, (inventory.MAX_ROWS + 1,)):
        if len(rows) >= inventory.MAX_ROWS:
            raise MigrationError('migration projection exceeds row capacity')
        digest = hashlib.sha256()
        for value in row:
            data = inventory.encoded(value)
            total += len(data)
            if len(data) > inventory.MAX_VALUE_BYTES or total > inventory.MAX_LOGICAL_BYTES:
                raise MigrationError('migration projection exceeds byte capacity')
            digest.update(len(data).to_bytes(8, 'big'))
            digest.update(data)
        rows.append(digest.digest())
    return hashlib.sha256(b''.join(sorted(rows))).hexdigest(), len(rows)


def _rows(db, table, columns, *, where=''):
    # Fixed internal table/column lists only. These fields are private identity
    # and cursor evidence, never note bodies, inbox frames or work descriptions.
    names = columns.split(',')
    query = 'SELECT ' + ','.join(inventory.identifier(name) for name in names)
    query += ' FROM ' + inventory.identifier(table) + where + ' ORDER BY 1 LIMIT ?'
    rows = db.execute(query, (inventory.MAX_ROWS + 1,)).fetchall()
    if len(rows) > inventory.MAX_ROWS:
        raise MigrationError('report row capacity exceeded')
    return [dict(zip(names, row)) for row in rows]


def _summary(db, kind, captured):
    result = dict(table_counts={name: value['rows'] for name, value in captured['tables'].items()})
    if kind == 'memory':
        result['metadata'] = dict(db.execute('SELECT key,value FROM meta'))
        result['consumers'] = _rows(db, 'cursors',
            'consumer,seq,issued,snapshot,bootstrapped,resnapshot,updated')
        result['snapshots'] = _rows(db, 'snapshots',
            'id,consumer,head,created,items,issued,acked,acked_at')
        if 'work_items' in captured['tables']:
            result['unfinished_work'] = _rows(db, 'work_items',
                'work_id,revision,lifecycle,scope_revision,latest_seq', where=" WHERE lifecycle <> 'finished'")
            result['claims'] = _rows(db, 'claim_bundles',
                'generation,work_id,consumer,revision,expires_at,active,overdue_credit,end_credit')
    else:
        from koinon import inbox_schema
        result['metadata'] = inbox_schema.metadata(db)
        result['allocated_head'] = inbox_schema.allocated_head(db)
        columns = ','.join(row[1] for row in db.execute('PRAGMA table_info(memory_binding)'))
        result['bindings'] = _rows(db, 'memory_binding', columns)
    return result


def expected_memory(snapshot, workspace, repo, store_id):
    """Derive schema 3/4->5 or exact schema-5 expectation and explicit identity.

    store_id comes from the coordinator's verified gated service generation. A
    schema-3 source may acquire that valid new UUID; schema 4/5 must preserve it.
    """
    if (not isinstance(store_id, str) or len(store_id) != 32
            or any(char not in '0123456789abcdef' for char in store_id)):
        raise MigrationError('verified gated store identity required')
    with upgrade_backup_inventory.open_copy(snapshot, 'memory.sqlite3', workspace) as db:
        version = work_schema.validate(db, repo, memory.SCHEMA_STATEMENTS)
        before = inventory.capture(db)
        original = dict(db.execute('SELECT key,value FROM meta'))
        before_summary = _summary(db, 'memory', before)
        if version >= 4 and original['store_id'] != store_id:
            raise MigrationError('migration changed an existing store UUID')
        if version == 5:
            return dict(version=1, source_schema=5, target_schema=5, changes=[],
                        identity=original, inventory=before,
                        before=before_summary, after=before_summary)
        replay_columns = ('key', 'fingerprint', 'seq', 'ts', 'deadline')
        replays = _projection(db, 'idem', replay_columns)
        expected_meta = dict(original, schema='5', store_id=store_id,
                             work_id_counter='0', claim_generation='0')
        # All mutations below affect the disposable copy, never retained backup.
        db.isolation_level = None
        db.execute('PRAGMA query_only=OFF')
        db.execute('PRAGMA journal_mode=DELETE').fetchall()
        db.execute('BEGIN IMMEDIATE')
        try:
            work_schema.migrate(db, repo, memory.SCHEMA_STATEMENTS)
            if version == 3:
                db.execute("UPDATE meta SET value=? WHERE key='store_id'", (store_id,))
            db.commit()
        except BaseException:
            db.rollback()
            raise
        if dict(db.execute('SELECT key,value FROM meta')) != expected_meta:
            raise MigrationError('undeclared metadata change in migration expectation')
        if _projection(db, 'idem', replay_columns) != replays:
            raise MigrationError('migration changed retained replay fields')
        if db.execute("SELECT count(*) FROM idem WHERE operation <> 'note' OR result IS NOT NULL").fetchone()[0]:
            raise MigrationError('migration produced undeclared replay defaults')
        after = inventory.capture(db)
        for name, retained in before['tables'].items():
            if name not in ('meta', 'idem') and after['tables'].get(name) != retained:
                raise MigrationError('migration changed retained table: ' + name)
        if set(after['tables']) - set(before['tables']) != work_schema.TABLES:
            raise MigrationError('migration added unexpected tables')
        if any(after['tables'][name]['rows'] for name in work_schema.TABLES):
            raise MigrationError('migration created unexpected work records')
        return dict(version=1, source_schema=version, target_schema=5,
                    changes=['work_schema_5', *(['new_store_uuid'] if version == 3 else [])],
                    identity=expected_meta, inventory=after,
                    before=before_summary, after=_summary(db, 'memory', after))


def verify(expected, observed):
    change = inventory.compare(expected['inventory'], observed)
    if not change['equal']:
        raise MigrationError('gated inventory differs from declared migration: ' + str(change))
    return dict(version=1, verified=True, source_schema=expected['source_schema'],
                target_schema=expected['target_schema'], changes=expected['changes'],
                identity=expected['identity'], inventory=observed['sha256'],
                before=expected['before'], after=expected['after'])


def expected_inbox(snapshot, workspace):
    """Exact current-schema inbox expectation; older transitions require an adapter.

    This check is also required in preflight before selected services are stopped.
    It never upgrades the retained backup or invents binding/delivery identities.
    """
    from koinon import delivery_ledger
    from koinon import inbox_schema
    with upgrade_backup_inventory.open_copy(snapshot, 'inbox.sqlite3', workspace) as db:
        identity = inbox_schema.metadata(db)
        inbox_schema.validate_bindings(db)
        identity = dict(identity, allocated_head=inbox_schema.allocated_head(db),
                        delivery_identity=delivery_ledger.identity(db))
        expected = inventory.capture(db)
        summary = _summary(db, 'session', expected)
    return dict(version=1, source_schema=inbox_schema.SCHEMA, target_schema=inbox_schema.SCHEMA,
                changes=[], identity=identity, inventory=expected, before=summary, after=summary)
