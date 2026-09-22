"""Work-items schema validated and migrated by memory startup.

The caller owns the connection and transaction. No function here configures a
connection, commits, creates a daemon, or opens a persistent database.
"""
import sqlite3
import re
import os
import uuid

VERSION = 5
TABLES = frozenset(('work_items', 'work_scope_revisions', 'claim_bundles',
                    'claim_resources', 'work_events'))
INDEXES = frozenset(('work_events_item',))
COUNTERS = ('work_id_counter', 'claim_generation')
MAX_COUNTER = 2**63 - 1

MIGRATION_STATEMENTS = (
    """ALTER TABLE idem ADD COLUMN operation TEXT NOT NULL DEFAULT 'note'""",
    """ALTER TABLE idem ADD COLUMN result TEXT""",
    """CREATE TABLE work_items (
    work_id TEXT PRIMARY KEY NOT NULL CHECK(length(work_id) = 32),
    revision INTEGER NOT NULL CHECK(revision > 0),
    lifecycle TEXT NOT NULL CHECK(lifecycle IN ('open','active','blocked','finished')),
    title TEXT NOT NULL,
    criteria TEXT NOT NULL,
    non_goals TEXT NOT NULL,
    proposed_assignee TEXT,
    created_at REAL NOT NULL,
    created_consumer TEXT NOT NULL,
    first_start_revision INTEGER,
    scope_revision INTEGER NOT NULL CHECK(scope_revision > 0),
    progress_epoch INTEGER NOT NULL DEFAULT 0 CHECK(progress_epoch >= 0),
    last_progress_at REAL,
    progress_deadline REAL,
    progress TEXT NOT NULL DEFAULT '',
    checkpoint TEXT NOT NULL DEFAULT '',
    next_artifact TEXT NOT NULL DEFAULT '',
    blocker TEXT NOT NULL DEFAULT '',
    last_writer TEXT,
    last_generation INTEGER,
    last_lease_expires REAL,
    lease_expired INTEGER NOT NULL DEFAULT 0 CHECK(lease_expired IN (0,1)),
    outcome TEXT CHECK(outcome IN ('completed','withdrawn')),
    reason TEXT NOT NULL DEFAULT '',
    references_json TEXT NOT NULL DEFAULT '[]',
    finished_at REAL,
    expires_at REAL,
    latest_seq INTEGER NOT NULL CHECK(latest_seq > 0),
    CHECK((lifecycle = 'finished' AND outcome IS NOT NULL
           AND finished_at IS NOT NULL AND expires_at IS NOT NULL)
       OR (lifecycle <> 'finished' AND outcome IS NULL
           AND finished_at IS NULL AND expires_at IS NULL))
)""",
    """CREATE TABLE work_scope_revisions (
    work_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    ts REAL NOT NULL,
    consumer TEXT NOT NULL,
    author TEXT,
    title TEXT NOT NULL,
    criteria TEXT NOT NULL,
    non_goals TEXT NOT NULL,
    PRIMARY KEY(work_id, revision)
)""",
    """CREATE TABLE claim_bundles (
    generation INTEGER PRIMARY KEY CHECK(generation > 0),
    work_id TEXT NOT NULL UNIQUE,
    consumer TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    issued_at REAL NOT NULL,
    renewed_at REAL NOT NULL,
    expires_at REAL NOT NULL,
    progress_epoch INTEGER NOT NULL CHECK(progress_epoch > 0),
    overdue_recorded INTEGER NOT NULL DEFAULT 0 CHECK(overdue_recorded IN (0,1)),
    active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
    overdue_credit INTEGER NOT NULL DEFAULT 1 CHECK(overdue_credit IN (0,1)),
    end_credit INTEGER NOT NULL DEFAULT 1 CHECK(end_credit IN (0,1))
)""",
    """CREATE TABLE claim_resources (
    generation INTEGER NOT NULL,
    ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 8),
    kind TEXT NOT NULL CHECK(kind IN ('writer','path','exact')),
    resource TEXT NOT NULL,
    PRIMARY KEY(generation, ordinal)
)""",
    """CREATE TABLE work_events (
    seq INTEGER PRIMARY KEY CHECK(seq > 0),
    work_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    kind TEXT NOT NULL CHECK(kind IN (
        'created','proposed','edited','started','updated','released','finished',
        'progress-overdue','lease-expired')),
    payload TEXT NOT NULL
)""",
    """CREATE INDEX work_events_item ON work_events(work_id, seq)""",
)


class SchemaError(ValueError):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


def catalog(db):
    """Include autoindexes and FTS shadows, not just application-named tables."""
    return tuple(db.execute(
        'SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY type,name'))


def sql_tokens(sql):
    """Ignore SQL layout/keyword case, while preserving quoted text exactly.

    Never casefold a CHECK literal: 'open' and 'OPEN' permit different values.
    Token boundaries also keep whitespace removal from joining identifiers.
    Managed DDL contains no comments; commented definitions are refused rather
    than stripping text that could occur inside a quoted string.
    """
    if sql is None:
        return None
    pattern = r"'(?:(?:'')|[^'])*'|\"(?:(?:\"\")|[^\"])*\"|`[^`]*`|\[[^\]]*\]|[A-Za-z_][A-Za-z_0-9]*|[0-9]+|[^\s]"
    return tuple(token if token[0] in "'\"`[" else token.casefold()
                 for token in re.findall(pattern, sql))


def comparable_catalog(rows):
    return tuple((kind, name, table, sql_tokens(sql)) for kind, name, table, sql in rows)


def validate_format(db):
    """Check the actual schema-format field, not the unrelated schema version.

    Call AFTER the owner's verified WAL reset/recovery, before work activation.
    Persistent callers must already own the connection and validated file. An
    in-memory reference database has no header file; serialize only that small
    synthetic database, never a persistent store.
    """
    path = next(row[2] for row in db.execute('PRAGMA database_list') if row[1] == 'main')
    try:
        if path:
            try:
                residual = os.stat(path + '-wal').st_size
            except FileNotFoundError:
                residual = 0
            if residual:
                raise SchemaError('storage_blocked',
                                  'reset the WAL before verifying the database schema format')
            with open(path, 'rb') as stream:
                header = stream.read(100)
        else:
            header = db.serialize()[:100]
    except OSError as exc:
        raise SchemaError('storage_blocked', 'cannot verify database header or WAL state') from exc
    except (sqlite3.Error, AttributeError) as exc:
        raise SchemaError('unsupported_sqlite', 'cannot verify database schema format') from exc
    if (len(header) < 100 or header[:16] != b'SQLite format 3\x00'
            or int.from_bytes(header[44:48], 'big') != 4):
        raise SchemaError('unsupported_sqlite', 'work claims require database schema format 4')


def expected_catalog(legacy_statements, version, with_fts):
    """Let this SQLite build describe its own autoindexes and optional FTS shadows.

    Keep quoted literals case-sensitive. Comparing only table_info would miss CHECK
    constraints, generated expressions and trigger/index changes to allocation costs.
    This reference connection is transient and never opens a file.
    """
    if version not in (3, 4, VERSION):
        raise SchemaError('incompatible_store', 'unsupported source schema')
    db = sqlite3.connect(':memory:', isolation_level=None)
    try:
        for statement in legacy_statements:
            db.execute(statement)
        if version == VERSION:
            for statement in MIGRATION_STATEMENTS:
                db.execute(statement)
        if with_fts:
            try:
                db.execute('CREATE VIRTUAL TABLE search USING fts5(body, content="")')
            except sqlite3.OperationalError as exc:
                if 'no such module: fts5' not in str(exc).lower():
                    raise
                raise SchemaError('unsupported_sqlite',
                                  'this store requires a SQLite build with FTS5') from exc
        return catalog(db)
    finally:
        db.close()


def validate(db, repo, legacy_statements):
    """Read-only validation, intended to run BEFORE Store.configure on activation."""
    try:
        meta = dict(db.execute('SELECT key,value FROM meta'))
        if meta.get('repo') != repo:
            raise SchemaError('wrong_repository', 'store belongs to another repository')
        version = meta.get('schema')
        if version not in ('3', '4', '5') or meta.get('protocol') != '1':
            raise SchemaError('incompatible_store', 'unsupported store identity')
        version = int(version)
        allowed = {'repo', 'schema', 'protocol', 'head', 'floor', 'indexed_through'}
        if version >= 4:
            allowed.add('store_id')
        if version == VERSION:
            allowed.update(COUNTERS)
        if set(meta) - allowed:
            raise SchemaError('incompatible_store', 'unexpected metadata keys')
        if (not isinstance(repo, str) or len(repo) != 16
                or any(c not in '0123456789abcdef' for c in repo)):
            raise SchemaError('incompatible_store', 'invalid repository identity')
        identity = meta.get('store_id')
        if version == 3:
            valid_id = identity is None
        else:
            valid_id = (isinstance(identity, str) and len(identity) == 32
                        and all(c in '0123456789abcdef' for c in identity))
        if not valid_id:
            raise SchemaError('incompatible_store', 'invalid store identity')
        actual = catalog(db)
        with_fts = any(row[1] == 'search' for row in actual)
        expected = expected_catalog(legacy_statements, version, with_fts)
        if comparable_catalog(actual) != comparable_catalog(expected):
            raise SchemaError('incompatible_store', 'store catalog differs from owned schema')
        for key in ('head', 'floor') + (COUNTERS if version == VERSION else ()):
            value = meta.get(key)
            if (not isinstance(value, str) or not value.isascii() or not value.isdecimal()
                    or len(value) > 19 or int(value) > MAX_COUNTER):
                raise SchemaError('incompatible_store', 'invalid durable counter: ' + key)
        if int(meta['floor']) > int(meta['head']):
            raise SchemaError('incompatible_store', 'history floor exceeds head')
        indexed = meta.get('indexed_through', '-1')
        if indexed != '-1' and (
                not isinstance(indexed, str) or not indexed.isascii() or not indexed.isdecimal()
                or len(indexed) > 19 or int(indexed) > int(meta['head'])):
            raise SchemaError('incompatible_store', 'invalid index coverage marker')
        if version < VERSION and any(key in meta for key in COUNTERS):
            raise SchemaError('incompatible_store', 'legacy store has unexpected work counters')
        return version
    except sqlite3.ProgrammingError:
        # Programming failures are not evidence of an incompatible operator file.
        raise
    except sqlite3.Error as exc:
        code = getattr(exc, 'sqlite_errorcode', 0) & 0xff
        if code in (sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED):
            raise SchemaError('store_busy', 'another owner holds the store; retry through '
                              'its control socket or after that owner exits') from exc
        raise SchemaError('incompatible_store', 'store schema could not be validated') from exc


def migrate(db, repo, legacy_statements):
    """Apply schema 3/4 -> 5 within the caller's admitted transaction.

    The caller must propagate errors out of its transaction and roll back. The
    caller must reset the WAL before BEGIN, disable cache spill, and invoke this
    helper before any other write in that transaction.
    Runtime startup invokes this after read-only source validation and configuration.
    Revalidation here also protects independent synthetic callers.
    """
    if not db.in_transaction:
        raise RuntimeError('migration requires a caller-owned transaction')
    version = validate(db, repo, legacy_statements)
    # The owning Store transaction must reset the WAL before BEGIN. Keeping this
    # out of read-only catalog validation avoids treating a stale main-file header
    # as the format of a crashed store whose page 1 is still in the WAL.
    validate_format(db)
    if version == VERSION:
        return
    for statement in MIGRATION_STATEMENTS:
        db.execute(statement)
    if version == 3:
        db.execute('INSERT INTO meta VALUES (?,?)', ('store_id', uuid.uuid4().hex))
    for key in COUNTERS:
        db.execute('INSERT INTO meta VALUES (?,?)', (key, '0'))
    db.execute("UPDATE meta SET value=? WHERE key='schema'", (str(VERSION),))


def allocate(db, key):
    """Allocate a durable counter only in the encompassing work transaction."""
    if key not in COUNTERS:
        raise ValueError('unknown work counter')
    if not db.in_transaction:
        raise RuntimeError('counter allocation requires a transaction')
    row = db.execute('SELECT value FROM meta WHERE key=?', (key,)).fetchone()
    if (row is None or not isinstance(row[0], str) or not row[0].isascii()
            or not row[0].isdecimal() or len(row[0]) > 19):
        raise SchemaError('incompatible_store', 'invalid work counter')
    value = int(row[0])
    if value >= MAX_COUNTER:
        raise SchemaError('capacity', 'durable work counter exhausted')
    value += 1
    db.execute('UPDATE meta SET value=? WHERE key=?', (str(value), key))
    return f'{value:032x}' if key == 'work_id_counter' else value
