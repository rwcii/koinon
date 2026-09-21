"""Canonical logical SQLite inventories for a quiesced upgrade coordinator.

This module neither establishes ownership nor opens files, stops services, migrates
stores or authorizes replacement. The caller supplies a dedicated connection to a
validated, stopped store (or its verified consistent backup). A transaction gives
one database snapshot; it is not proof that other writers have stopped.

All catalog objects and all ordinary/shadow table values are included. Virtual
interfaces are recorded but never queried: their owned shadow tables carry the
retained index data. Schema validation and declared migration rules are separate.
"""
import hashlib
import json
import sqlite3
import struct

FORMAT = 1
MAX_OBJECTS = 128
MAX_COLUMNS = 128
MAX_ROWS = 250_000
MAX_VALUE_BYTES = 1 << 20
MAX_LOGICAL_BYTES = 256 << 20


class InventoryError(ValueError):
    pass


class UnsupportedSQLiteError(InventoryError):
    code = 'unsupported_sqlite'


def encoded(value):
    """Unambiguous SQLite storage-class encoding, independent of display format."""
    if value is None:
        return b'n'
    if type(value) is int:
        return b'i' + str(value).encode('ascii')
    if type(value) is float:
        return b'f' + struct.pack('>d', value)
    if type(value) is str:
        return b't' + value.encode('utf-8')
    if type(value) is bytes:
        return b'b' + value
    raise InventoryError('unsupported SQLite value')


def identifier(name):
    if not isinstance(name, str) or not name or '\x00' in name:
        raise InventoryError('invalid catalog identifier')
    return '"' + name.replace('"', '""') + '"'


def capture(db, *, max_rows=MAX_ROWS, max_bytes=MAX_LOGICAL_BYTES):
    """Return counts and canonical hashes without exposing retained row contents.

    Uses a dedicated, default-row/text-factory connection with no transaction in
    progress. Limits bound catalog, row count, value size and encoded input volume;
    they are not wall-clock I/O deadlines. Sorting fixed-size row hashes makes the
    multiset digest independent of SQL collation, physical layout and insertion
    order while retaining duplicates. SHA-256 collision resistance is assumed.
    """
    if (db.in_transaction or db.row_factory is not None or db.text_factory is not str
            or type(max_rows) is not int or not 0 < max_rows <= MAX_ROWS
            or type(max_bytes) is not int or not 0 < max_bytes <= MAX_LOGICAL_BYTES):
        raise InventoryError('dedicated connection and bounded limits required')
    previous_limit = db.setlimit(sqlite3.SQLITE_LIMIT_LENGTH, MAX_VALUE_BYTES)
    try:
        db.execute('BEGIN')
        result = _capture(db, max_rows, max_bytes)
        db.rollback()
        return result
    except sqlite3.Error as exc:
        raise InventoryError('SQLite inventory failed: ' + str(exc)) from exc
    finally:
        # No caller transaction existed on entry; even a capacity failure must
        # release this snapshot and restore the connection limit.
        if db.in_transaction:
            db.rollback()
        db.setlimit(sqlite3.SQLITE_LIMIT_LENGTH, previous_limit)


def _capture(db, max_rows, max_bytes):
    remaining = max_bytes

    def row_digest(row, domain):
        nonlocal remaining
        digest = hashlib.sha256(domain)
        digest.update(len(row).to_bytes(4, 'big'))
        for value in row:
            data = encoded(value)
            remaining -= len(data) + 8
            if remaining < 0:
                raise InventoryError('inventory logical byte limit exceeded')
            digest.update(len(data).to_bytes(8, 'big'))
            digest.update(data)
        return digest.digest()

    # Unknown SQLite pragmas silently return no rows. Probe the required
    # capability before reading sqlite_schema so an old build is not blamed on
    # the operator's data. Even an empty main database lists sqlite_schema.
    listing = db.execute('PRAGMA main.table_list').fetchall()
    if not any(row[:3] == ('main', 'sqlite_schema', 'table') for row in listing):
        raise UnsupportedSQLiteError(
            'upgrade inventory requires PRAGMA table_list (SQLite 3.37 or newer)')
    catalog = db.execute(
        'SELECT type,name,tbl_name,sql FROM main.sqlite_schema ORDER BY type,name LIMIT ?',
        (MAX_OBJECTS + 1,)).fetchall()
    if len(catalog) > MAX_OBJECTS:
        raise InventoryError('inventory catalog limit exceeded')
    # table_list identifies virtual interfaces without parsing arbitrary SQL and
    # distinguishes their physical shadow tables. Never query a view or virtual
    # interface, whose implementation could evaluate code or hide retained rows.
    kinds = {row[1]: row[2] for row in listing if row[0] == 'main'}
    objects = []
    tables = {}
    rows_left = max_rows
    for kind, name, table, sql in catalog:
        objects.append(dict(kind=kind, name=name, table=table,
                            sha256=row_digest((kind, name, table, sql), b'catalog-v1\0').hex()))
        if kind != 'table':
            continue
        storage = kinds.get(name)
        if storage == 'virtual':
            continue
        if storage not in ('table', 'shadow'):
            raise InventoryError('unclassified retained table')
        columns = db.execute('PRAGMA main.table_xinfo(' + identifier(name) + ')').fetchall()
        if not columns or len(columns) > MAX_COLUMNS or any(row[6] != 0 for row in columns):
            raise InventoryError('unsupported or oversized table shape')
        shape = row_digest(tuple(value for row in columns for value in row), b'columns-v1\0').hex()
        selected = ','.join(identifier(row[1]) for row in columns)
        cursor = db.execute('SELECT ' + selected + ' FROM main.' + identifier(name)
                            + ' LIMIT ?', (rows_left + 1,))
        hashes = []
        for row in cursor:
            if len(hashes) >= rows_left:
                raise InventoryError('inventory row limit exceeded')
            hashes.append(row_digest(row, b'row-v1\0'))
        rows_left -= len(hashes)
        digest = hashlib.sha256(b'table-v1\0')
        digest.update(len(hashes).to_bytes(8, 'big'))
        for value in sorted(hashes):
            digest.update(value)
        tables[name] = dict(rows=len(hashes), sha256=digest.hexdigest(), columns_sha256=shape)
    result = dict(format=FORMAT, objects=objects, tables=tables,
                  rows=max_rows - rows_left, logical_bytes=max_bytes - remaining)
    canonical = json.dumps(result, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()
    result['sha256'] = hashlib.sha256(b'inventory-v1\0' + canonical).hexdigest()
    return result


def compare(before, after):
    """Describe exact logical changes; never classify a migration as acceptable."""
    if before.get('format') != FORMAT or after.get('format') != FORMAT:
        raise InventoryError('unsupported inventory format')
    old, new = before['tables'], after['tables']
    added = sorted(new.keys() - old.keys())
    removed = sorted(old.keys() - new.keys())
    changed = sorted(name for name in old.keys() & new.keys() if old[name] != new[name])
    catalog_changed = before['objects'] != after['objects']
    return dict(equal=not (added or removed or changed or catalog_changed),
                added_tables=added, removed_tables=removed, changed_tables=changed,
                catalog_changed=catalog_changed)
