"""Synthetic tests of staged work primitives; no live databases or peer traffic."""
import contextlib
from pathlib import Path
import sqlite3
import tempfile
import time
import unittest
from unittest.mock import patch

import claims
import memory
import work_schema

REPO = '0123456789abcdef'
WORK = '00000000000000000000000000000001'
OTHER = '00000000000000000000000000000002'

# Frozen from released schema 3 (d1e0a32) and schema 4 (1e65882), whose DDL is
# identical. Do not derive this fixture from current memory.SCHEMA_STATEMENTS:
# that would hide a later incompatible edit to the migration's source schema.
RELEASED_DDL = (
    'CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)',
    '''CREATE TABLE entries(
    seq INTEGER PRIMARY KEY, ts REAL, type TEXT, scope TEXT, scope_target TEXT, path TEXT,
    body TEXT, author TEXT, author_pid INTEGER, consumer TEXT, revision INTEGER,
    supersedes INTEGER, revokes INTEGER, superseded_by INTEGER, revoked_by INTEGER,
    conflicts_with INTEGER, expires REAL)''',
    '''CREATE TABLE idem(
    key TEXT PRIMARY KEY, fingerprint TEXT, seq INTEGER, ts REAL, deadline REAL)''',
    '''CREATE TABLE cursors(
    consumer TEXT PRIMARY KEY, seq INTEGER, issued INTEGER, snapshot TEXT,
    bootstrapped INTEGER, resnapshot INTEGER, updated REAL)''',
    'CREATE TABLE retired(consumer TEXT PRIMARY KEY, seq INTEGER, at REAL)',
    '''CREATE TABLE snapshots(
    id TEXT PRIMARY KEY, consumer TEXT, head INTEGER, created REAL, items INTEGER,
    issued INTEGER, acked INTEGER, acked_at REAL)''',
    '''CREATE TABLE snapshot_items(
    id TEXT, position INTEGER, seq INTEGER, payload TEXT, bytes INTEGER,
    PRIMARY KEY(id, position))''',
    'CREATE INDEX entries_live ON entries(superseded_by, revoked_by, expires)',
)


@contextlib.contextmanager
def transaction(db):
    db.execute('BEGIN IMMEDIATE')
    try:
        yield
        db.execute('COMMIT')
    except BaseException:
        db.execute('ROLLBACK')
        raise


def legacy(db, version=4):
    for statement in RELEASED_DDL:
        db.execute(statement)
    db.executemany('INSERT INTO meta VALUES (?,?)',
                   [('repo', REPO), ('schema', str(version)), ('protocol', '1'),
                    ('head', '0'), ('floor', '0')])
    if version == 4:
        db.execute('INSERT INTO meta VALUES (?,?)', ('store_id', 'a' * 32))


class MigrationTests(unittest.TestCase):
    def database(self, version=4):
        db = sqlite3.connect(':memory:', isolation_level=None)
        self.addCleanup(db.close)
        legacy(db, version)
        return db

    def test_legacy_records_identity_and_replays_survive(self):
        with tempfile.TemporaryDirectory() as root:
            with patch.object(memory, 'SCHEMA', 4):
                store = memory.Store(Path(root) / 'memory.sqlite3', REPO)
            try:
                store.note('writer', 'decision', 'synthetic evidence', key='key',
                           deadline=time.time() + 600)
                commands = memory.MemoryCommands(Path(root), REPO, store)
                commands.command(dict(op='sync', consumer='reader'), 4242)
                tables = ('entries', 'cursors', 'snapshots', 'snapshot_items', 'idem')
                before = {table: store.db.execute('SELECT * FROM ' + table).fetchall()
                          for table in tables}
                identity, head = store.meta('store_id'), store.head()
                with store.transaction(control=True):
                    work_schema.migrate(store.db, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(work_schema.validate(store.db, REPO,
                                                      memory.SCHEMA_STATEMENTS), 5)
                self.assertEqual(store.meta('store_id'), identity)
                self.assertEqual(store.head(), head)
                for table in tables:
                    after = store.db.execute('SELECT * FROM ' + table).fetchall()
                    if table == 'idem':
                        self.assertEqual([row[-2:] for row in after], [('note', None)])
                        after = [row[:-2] for row in after]
                    self.assertEqual(after, before[table])
            finally:
                store.close()
            with patch.object(memory, 'SCHEMA', 4), self.assertRaises(memory.MemoryError_) as caught:
                memory.Store(Path(root) / 'memory.sqlite3', REPO)
            self.assertEqual(caught.exception.code, 'schema_too_new')

    def test_three_and_four_migrate_to_identical_catalogs(self):
        for version in (3, 4):
            with self.subTest(version=version):
                db = self.database(version)
                with transaction(db):
                    work_schema.migrate(db, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(work_schema.catalog(db), work_schema.expected_catalog(
                    memory.SCHEMA_STATEMENTS, 5, False))
                self.assertEqual(work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS), 5)
                identity = db.execute("SELECT value FROM meta WHERE key='store_id'").fetchone()
                with transaction(db):
                    work_schema.migrate(db, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(db.execute("SELECT value FROM meta WHERE key='store_id'")
                                 .fetchone(), identity)

    def test_failure_at_each_write_rolls_back_all_ddl_and_identity(self):
        # SQLite authorizers can reject a write at its actual execution boundary.
        # A wrapper instead injects after each whole statement, covering successful
        # ALTER/CREATE followed by process-like failure before the final commit.
        for version in (3, 4):
            writes = len(work_schema.MIGRATION_STATEMENTS) + 3 + (version == 3)
            for fail_after in range(1, writes + 1):
                with self.subTest(version=version, fail_after=fail_after):
                    db = self.database(version)
                    before = tuple(db.iterdump())

                    class Fault:
                        count = 0
                        in_transaction = True

                        def __getattr__(self, name):
                            return getattr(db, name)

                        def execute(self, sql, params=()):
                            result = db.execute(sql, params)
                            if not sql.startswith(('SELECT', 'PRAGMA')):
                                self.count += 1
                                if self.count == fail_after:
                                    raise RuntimeError('synthetic interrupted migration')
                            return result

                    with self.assertRaises(RuntimeError), transaction(db):
                        work_schema.migrate(Fault(), REPO, memory.SCHEMA_STATEMENTS)
                    self.assertEqual(tuple(db.iterdump()), before)

    def test_foreign_catalog_or_identity_is_refused_without_changes(self):
        edits = (
            'CREATE TRIGGER surprise AFTER INSERT ON entries BEGIN SELECT 1; END',
            'CREATE INDEX surprise ON entries(body)',
            'CREATE TABLE surprise(value)',
            "UPDATE meta SET value='other' WHERE key='repo'",
            "UPDATE meta SET value='0' WHERE key='schema'",
            "DELETE FROM meta WHERE key='store_id'",
            "INSERT INTO meta VALUES ('claim_generation','0')",
            'ALTER TABLE entries ADD COLUMN surprise TEXT',
            'DROP INDEX entries_live',
            work_schema.MIGRATION_STATEMENTS[2],
        )
        for edit in edits:
            with self.subTest(edit=edit):
                db = self.database()
                db.execute(edit)
                before = tuple(db.iterdump())
                with self.assertRaises(work_schema.SchemaError), transaction(db):
                    work_schema.migrate(db, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(tuple(db.iterdump()), before)

    def test_fts_shadow_definition_is_verified(self):
        db = self.database()
        try:
            db.execute(memory.FTS_TABLE_SQL)
        except sqlite3.OperationalError:
            self.skipTest('SQLite without FTS5')
        work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS)
        db.execute('ALTER TABLE search_docsize ADD COLUMN unexpected TEXT')
        with self.assertRaises(work_schema.SchemaError):
            work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS)

    def test_migration_requires_transaction(self):
        with self.assertRaises(RuntimeError):
            work_schema.migrate(self.database(), REPO, memory.SCHEMA_STATEMENTS)

    def test_sql_layout_is_ignored_but_quoted_constraint_values_are_not(self):
        db = sqlite3.connect(':memory:', isolation_level=None)
        self.addCleanup(db.close)
        for statement in memory.SCHEMA_STATEMENTS:
            db.execute(statement.replace('CREATE TABLE', 'create table')
                       .replace('PRIMARY KEY', 'primary key').replace(',', ' ,\n'))
        db.executemany('INSERT INTO meta VALUES (?,?)',
                       [('repo', REPO), ('schema', '4'), ('protocol', '1'),
                        ('head', '0'), ('floor', '0'), ('store_id', 'a' * 32)])
        self.assertEqual(work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS), 4)
        self.assertNotEqual(work_schema.sql_tokens("CHECK(state IN ('open'))"),
                            work_schema.sql_tokens("CHECK(state IN ('OPEN'))"))
        self.assertNotEqual(work_schema.sql_tokens("DEFAULT 'a  b'"),
                            work_schema.sql_tokens("DEFAULT 'a b'"))
        self.assertNotEqual(work_schema.sql_tokens("DEFAULT 'it''s here'"),
                            work_schema.sql_tokens("DEFAULT 'it''s Here'"))

    def test_schema_format_is_checked_from_header(self):
        db = self.database()
        work_schema.validate_format(db)

        class OldFormat:
            def execute(self, sql):
                return db.execute(sql)

            def serialize(self):
                data = bytearray(db.serialize())
                data[44:48] = (3).to_bytes(4, 'big')
                return bytes(data)

        with self.assertRaises(work_schema.SchemaError) as caught:
            work_schema.validate_format(OldFormat())
        self.assertEqual(caught.exception.code, 'unsupported_sqlite')

    def test_residual_wal_requires_recovery_before_format_check(self):
        with tempfile.TemporaryDirectory() as root:
            db = sqlite3.connect(Path(root) / 'wal.sqlite3', isolation_level=None)
            try:
                db.execute('PRAGMA journal_mode=WAL')
                legacy(db)
                self.assertEqual(work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS), 4)
                with self.assertRaises(work_schema.SchemaError) as caught:
                    work_schema.validate_format(db)
                self.assertEqual(caught.exception.code, 'storage_blocked')
                db.execute('PRAGMA wal_checkpoint(TRUNCATE)').fetchall()
                work_schema.validate_format(db)
            finally:
                db.close()

    def test_exclusive_owner_is_busy_not_incompatible(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'owned.sqlite3'
            owner = sqlite3.connect(path, isolation_level=None)
            other = sqlite3.connect(path, isolation_level=None, timeout=0)
            try:
                legacy(owner)
                owner.execute('BEGIN EXCLUSIVE')
                with self.assertRaises(work_schema.SchemaError) as caught:
                    work_schema.validate(other, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(caught.exception.code, 'store_busy')
                owner.execute('ROLLBACK')
                self.assertEqual(work_schema.validate(other, REPO, memory.SCHEMA_STATEMENTS), 4)
            finally:
                other.close()
                owner.close()

    def test_metadata_reserve_assumptions_are_enforced(self):
        edits = (
            "INSERT INTO meta VALUES ('unexpected','data')",
            "INSERT INTO meta VALUES ('indexed_through','1')",
            "INSERT INTO meta VALUES ('indexed_through','-2')",
            "INSERT INTO meta VALUES ('indexed_through','9999999999999999999999999')",
            "INSERT INTO meta VALUES ('indexed_through','1.5')",
            "UPDATE meta SET value='1' WHERE key='floor'",
            "UPDATE meta SET value='-1' WHERE key='head'",
        )
        for edit in edits:
            with self.subTest(edit=edit):
                db = self.database()
                db.execute(edit)
                before = tuple(db.iterdump())
                with self.assertRaises(work_schema.SchemaError):
                    work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS)
                self.assertEqual(tuple(db.iterdump()), before)
        db = self.database()
        db.execute("UPDATE meta SET value='repo' WHERE key='repo'")
        with self.assertRaises(work_schema.SchemaError):
            work_schema.validate(db, 'repo', memory.SCHEMA_STATEMENTS)
        for value in ('-1', '0'):
            db = self.database()
            db.execute("INSERT INTO meta VALUES ('indexed_through',?)", (value,))
            work_schema.validate(db, REPO, memory.SCHEMA_STATEMENTS)

    def test_missing_fts_module_has_operator_recovery_code(self):
        original = sqlite3.connect

        class WithoutFTS:
            def __init__(self):
                self.db = original(':memory:', isolation_level=None)

            def execute(self, sql):
                if 'VIRTUAL TABLE' in sql:
                    raise sqlite3.OperationalError('no such module: fts5')
                return self.db.execute(sql)

            def close(self):
                self.db.close()

        with patch('work_schema.sqlite3.connect', side_effect=lambda *a, **kw: WithoutFTS()):
            with self.assertRaises(work_schema.SchemaError) as caught:
                work_schema.expected_catalog(memory.SCHEMA_STATEMENTS, 4, True)
        self.assertEqual(caught.exception.code, 'unsupported_sqlite')

    def test_schema_matches_reviewed_ddl_and_rejects_invalid_terminal_state(self):
        spec = (Path(__file__).parent / 'docs/WORK-ITEMS-SCHEMA.md').read_text()
        sql = spec.split('```sql\n', 1)[1].split('```', 1)[0]
        self.assertEqual(tuple(s.strip() for s in sql.split(';') if s.strip()),
                         work_schema.MIGRATION_STATEMENTS)
        db = self.database()
        with transaction(db):
            work_schema.migrate(db, REPO, memory.SCHEMA_STATEMENTS)
        with self.assertRaises(sqlite3.IntegrityError):
            db.execute('INSERT INTO work_items '
                       '(work_id,revision,lifecycle,title,criteria,non_goals,created_at,'
                       'created_consumer,scope_revision,latest_seq) '
                       "VALUES (?,1,'finished','title','criteria','',100,'writer',1,1)",
                       (WORK,))


class ResourceTests(unittest.TestCase):
    def test_component_boundaries_namespaces_and_no_alias_resolution(self):
        for a, b, expected in (
            (('path', 'auth'), ('path', 'auth/session.py'), True),
            (('path', 'auth'), ('path', 'authorization'), False),
            (('path', '.'), ('path', 'anything'), True),
            (('path', 'Auth'), ('path', 'auth'), False),
            (('path', 'link'), ('path', 'target'), False),
            (('path', 'auth'), ('exact', 'auth'), False),
            (('exact', 'auth'), ('exact', 'auth/sub'), False),
            (('exact', 'auth'), ('exact', 'auth'), True),
        ):
            with self.subTest(a=a, b=b):
                self.assertEqual(claims.overlaps(a, b), expected)
                self.assertEqual(claims.overlaps(b, a), expected)

    def test_ambiguous_and_oversized_resources_refused(self):
        for key in ('', '/root', 'a/', 'a//b', './a', 'a/./b', 'a/../b', '..',
                    'a\\b', 'a\x00b', 'a\x7fb', 'a\x85b', 'é' * 257, '\ud800'):
            with self.subTest(key=repr(key)), self.assertRaises(claims.ClaimError):
                claims.resource('path', key)
        for optional in ([('path', 'a')] * 2, [('writer', OTHER)], [('path', 'a')] * 9,
                         'a', [{'kind': 'path', 'key': 'a'}]):
            with self.subTest(optional=optional), self.assertRaises(claims.ClaimError):
                claims.bundle(WORK, optional)


class LeaseTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(':memory:', isolation_level=None)
        self.addCleanup(self.db.close)
        legacy(self.db)
        with transaction(self.db):
            work_schema.migrate(self.db, REPO, memory.SCHEMA_STATEMENTS)
        self.admissions = []
        self.engine = claims.LeaseEngine(self.db, lambda *args: self.admissions.append(args))

    def acquire(self, work=WORK, consumer='writer', resources=(), now=100, duration=60):
        with transaction(self.db):
            return self.engine.acquire(work, consumer, 1, resources, now=now, duration=duration)

    def test_bundle_conflict_rolls_back_even_for_same_consumer(self):
        self.acquire(resources=[('path', 'auth')])
        before = tuple(self.db.iterdump())
        with self.assertRaises(claims.ClaimError) as caught:
            self.acquire(OTHER, resources=[('exact', 'free'), ('path', 'auth/x')])
        self.assertEqual(caught.exception.code, 'claim_conflict')
        self.assertEqual(tuple(self.db.iterdump()), before)
        self.acquire(OTHER, resources=[('path', 'authorization')])

    def test_stale_owner_generation_revision_and_expiry(self):
        first = self.acquire()
        for owner, gen, now in [('other', 1, 100), ('writer', 2, 100), ('writer', 1, 160)]:
            with self.subTest(owner=owner, gen=gen, now=now):
                with self.assertRaises(claims.ClaimError) as caught, transaction(self.db):
                    self.engine.release(WORK, owner, gen, now=now)
                self.assertEqual(caught.exception.code, 'stale_claim')
        with self.assertRaises(claims.ClaimError) as caught, transaction(self.db):
            self.engine.renew(WORK, 'writer', first['generation'], 2, now=120)
        self.assertEqual(caught.exception.code, 'revision_conflict')

    def test_renewal_leaves_head_progress_and_obligations_unchanged(self):
        first = self.acquire()
        before = claims.debt(self.db)
        with transaction(self.db):
            renewed = self.engine.renew(WORK, 'writer', first['generation'], 1, now=120)
        self.assertEqual(renewed, dict(generation=1, revision=2, expires_at=1020))
        self.assertEqual(claims.debt(self.db), before)
        self.assertEqual(self.db.execute("SELECT value FROM meta WHERE key='head'")
                         .fetchone(), ('0',))
        self.assertEqual(self.db.execute('SELECT progress_epoch FROM claim_bundles')
                         .fetchone(), (1,))
        self.assertEqual(self.db.execute('SELECT count(*) FROM work_events').fetchone(), (0,))

    def test_end_only_updates_flags_then_separately_admits_deletion(self):
        self.acquire(resources=[('path', 'auth')])
        members = self.db.execute('SELECT * FROM claim_resources').fetchall()
        trace = []
        self.db.set_trace_callback(trace.append)
        with transaction(self.db):
            self.engine.release(WORK, 'writer', 1, now=120)
        self.db.set_trace_callback(None)
        updates = [sql for sql in trace if sql.startswith('UPDATE claim_bundles')]
        self.assertEqual(updates, ['UPDATE claim_bundles SET active=0,overdue_credit=0,'
                                   'end_credit=0 WHERE generation=1'])
        self.assertEqual(self.db.execute('SELECT * FROM claim_resources').fetchall(), members)
        self.assertEqual(claims.debt(self.db), claims.Debt(0, 0))
        self.assertTrue(self.admissions[-1][2])
        with self.assertRaises(claims.ClaimError) as caught:
            self.acquire(now=130)
        self.assertIn('retry after maintenance succeeds', str(caught.exception))
        with transaction(self.db):
            self.assertTrue(self.engine.reclaim_one())
        self.assertFalse(self.admissions[-1][2])
        second = self.acquire(now=130)
        self.assertEqual(second['generation'], 2)

    def test_expiry_is_idempotent_and_does_not_grant_new_ownership(self):
        self.acquire(resources=[('path', 'auth')])
        with transaction(self.db):
            self.assertFalse(self.engine.expire(1, now=159))
        with transaction(self.db):
            self.assertTrue(self.engine.expire(1, now=160))
            self.assertFalse(self.engine.expire(1, now=161))
        with self.assertRaises(claims.ClaimError), transaction(self.db):
            self.engine.renew(WORK, 'writer', 1, 1, now=161)
        self.assertEqual(self.db.execute('SELECT consumer,active FROM claim_bundles')
                         .fetchall(), [('writer', 0)])

    def test_expired_unreconciled_resources_do_not_conflict_but_retain_debt(self):
        self.acquire(resources=[('path', 'auth')])
        self.acquire(OTHER, resources=[('path', 'auth')], now=160)
        self.assertEqual(claims.debt(self.db), claims.Debt(2, 2))

    def test_admission_failure_rolls_back_bundle_members_and_counter(self):
        def refuse(*args):
            raise claims.ClaimError('capacity', 'synthetic full store')

        self.engine = claims.LeaseEngine(self.db, refuse)
        before = tuple(self.db.iterdump())
        with self.assertRaises(claims.ClaimError):
            self.acquire(resources=[('path', 'auth'), ('exact', 'build')])
        self.assertEqual(tuple(self.db.iterdump()), before)

    def test_failed_control_or_cleanup_preserves_original_claim_and_debt(self):
        self.acquire(resources=[('path', 'auth')])
        admitted = self.engine.admit

        def refuse(*args):
            raise claims.ClaimError('capacity', 'synthetic storage refusal')

        for mutate in (lambda: self.engine.release(WORK, 'writer', 1, now=120),
                       lambda: self.engine.expire(1, now=160),
                       lambda: self.engine.renew(WORK, 'writer', 1, 1, now=120)):
            before = tuple(self.db.iterdump())
            self.engine.admit = refuse
            with self.assertRaises(claims.ClaimError), transaction(self.db):
                mutate()
            self.assertEqual(tuple(self.db.iterdump()), before)
        self.engine.admit = admitted
        with transaction(self.db):
            self.engine.release(WORK, 'writer', 1, now=120)
        before = tuple(self.db.iterdump())
        self.engine.admit = refuse
        with self.assertRaises(claims.ClaimError), transaction(self.db):
            self.engine.reclaim_one()
        self.assertEqual(tuple(self.db.iterdump()), before)

    def test_durable_generation_and_debt_survive_connection_restart(self):
        self.acquire(resources=[('path', 'auth')])
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'synthetic.sqlite3'
            copy = sqlite3.connect(path, isolation_level=None)
            self.db.backup(copy)
            copy.close()
            reopened = sqlite3.connect(path, isolation_level=None)
            try:
                engine = claims.LeaseEngine(reopened, lambda *args: None)
                self.assertEqual(claims.debt(reopened), claims.Debt(1, 1))
                with self.assertRaises(claims.ClaimError), transaction(reopened):
                    engine.acquire(WORK, 'replacement', 1, now=159)
                with transaction(reopened):
                    engine.expire(1, now=160)
                    engine.reclaim_one()
                    fresh = engine.acquire(WORK, 'replacement', 2, now=160)
                self.assertEqual(fresh['generation'], 2)
                with self.assertRaises(claims.ClaimError), transaction(reopened):
                    engine.renew(WORK, 'writer', 1, 1, now=161)
            finally:
                reopened.close()

    def test_all_retained_bundles_count_and_all_debt_dimensions(self):
        for i in range(16):
            self.acquire(work=f'{i + 1:032x}')
        debt = claims.debt(self.db)
        self.assertEqual((debt.pages, debt.logical_bytes, debt.event_slots, debt.replay_slots),
                         (10240, 1572864, 32, 16))
        with transaction(self.db):
            self.engine.expire(1, now=160)
        with self.assertRaises(claims.ClaimError) as caught:
            self.acquire(work=f'{17:032x}', now=160)
        self.assertEqual(caught.exception.code, 'claim_capacity')

    def test_counter_overflow_and_rollback(self):
        with transaction(self.db):
            first = work_schema.allocate(self.db, 'work_id_counter')
        self.assertEqual(first, WORK)
        with self.assertRaises(RuntimeError), transaction(self.db):
            work_schema.allocate(self.db, 'work_id_counter')
            raise RuntimeError('synthetic interruption')
        with transaction(self.db):
            self.assertEqual(work_schema.allocate(self.db, 'work_id_counter'), OTHER)
        self.db.execute("UPDATE meta SET value=? WHERE key='claim_generation'",
                        (str(2**63 - 1),))
        with self.assertRaises(work_schema.SchemaError):
            self.acquire()

    def test_strict_numeric_validation_and_required_transaction(self):
        for now in (True, float('nan'), float('inf'), -1, 10**1000):
            with self.subTest(now=repr(now)), self.assertRaises(claims.ClaimError):
                self.acquire(now=now)
        for duration in (True, 59, 3601, 60.0):
            with self.subTest(duration=duration), self.assertRaises(claims.ClaimError):
                self.acquire(duration=duration)
        with self.assertRaises(RuntimeError):
            self.engine.acquire(WORK, 'writer', 1, now=100)


if __name__ == '__main__':
    unittest.main()
