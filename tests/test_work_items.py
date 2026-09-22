"""Synthetic command, replay, stream and snapshot integration on staged schema 5."""
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import memory
from koinon import work_items
from koinon import work_schema


class WorkCommandsTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.store = memory.Store(Path(self.tmp.name) / 'memory.sqlite3', '0123456789abcdef')
        self.addCleanup(self.store.close)
        with self.store.transaction():
            work_schema.migrate(self.store.db, self.store.repo, memory.SCHEMA_STATEMENTS)
        self.commands = memory.MemoryCommands(self.tmp.name, self.store.repo, self.store)
        self.work = work_items.WorkItems(self.store, memory.MemoryError_)
        self.now = time.time()
        self.key = 0

    def request(self, op, **fields):
        self.key += 1
        return dict(op='work-' + op, consumer='writer', key=str(self.key),
                    deadline=self.now + 3600, **fields)

    def call(self, r, now=None):
        return self.work.command(r, pid=12345, now=self.now if now is None else now)

    def create(self, **fields):
        return self.call(self.request('create', title='Synthetic work', criteria='Tests pass',
                                     non_goals='No deployment', **fields))

    def start(self, item, **fields):
        return self.call(self.request('start', work_id=item['work_id'], if_revision=item['revision'],
            checkpoint='Design read', next_artifact='Implementation', progress_deadline=self.now + 600,
            **fields))

    def mutation(self, op, item, **fields):
        return self.request(op, work_id=item['work_id'], if_revision=item['revision'],
                            claim_generation=item['claim']['generation'], **fields)

    def refusal(self, code, request, *, unchanged=True, now=None):
        before = tuple(self.store.db.iterdump())
        with self.assertRaises(memory.MemoryError_) as caught:
            self.call(request, now=now)
        self.assertEqual(caught.exception.code, code)
        if unchanged:
            self.assertEqual(tuple(self.store.db.iterdump()), before)
        return caught.exception

    def test_creation_proposal_is_not_acceptance_and_provenance_is_inert(self):
        item = self.create(proposed_assignee='reviewer', references=['file:///inert'])
        view = self.work.get(item['work_id'], self.now)
        self.assertEqual(view['lifecycle'], 'open')
        self.assertFalse(view['lease_valid'])
        self.assertEqual(view['proposed_assignee'], 'reviewer')
        self.assertEqual(view['references'], ['file:///inert'])
        row = self.store.db.execute('SELECT type,body,author_pid,consumer,expires FROM entries').fetchone()
        self.assertEqual(row, ('work-event', '', 12345, 'writer', None))
        self.assertEqual(self.store.head(), 1)

    def test_replay_precedes_revision_and_preserves_original_result(self):
        request = self.request('create', title='A', criteria='B', non_goals='C')
        created = self.call(request)
        started = self.start(created)
        replay = self.call(request)
        self.assertEqual(replay, dict(created, duplicate=True))
        self.assertEqual(self.store.head(), started['seq'])
        self.refusal('idempotency_conflict', dict(request, title='changed'))
        self.refusal('retry_deadline_expired', request, now=self.now + 3601)

    def test_atomic_competing_starts_and_resource_conflict(self):
        first, second = self.create(), self.create()
        started = self.start(first, resources=[['path', 'src']])
        request = self.request('start', work_id=second['work_id'], if_revision=1,
            checkpoint='Read', next_artifact='Patch', progress_deadline=self.now + 600,
            resources=[['path', 'docs'], ['path', 'src/module']])
        self.refusal('claim_conflict', request)
        self.assertEqual(self.store.db.execute('SELECT count(*) FROM claim_bundles').fetchone()[0], 1)
        self.refusal('revision_conflict', self.request('start', work_id=first['work_id'], if_revision=1,
            checkpoint='Read', next_artifact='Patch', progress_deadline=self.now + 600))
        self.assertTrue(self.work.get(started['work_id'], self.now)['lease_valid'])

    def test_scope_history_including_reversion_and_owner_enforcement(self):
        item = self.start(self.create())
        request = self.mutation('edit', item, criteria='Changed')
        self.refusal('stale_claim', dict(request, consumer='other'))
        edit = self.call(request)
        self.call(self.request('edit', work_id=item['work_id'], if_revision=edit['revision'],
                               claim_generation=item['claim']['generation'], criteria='Tests pass'))
        view = self.work.get(item['work_id'], self.now)
        self.assertTrue(view['criteria_changed_after_start'])
        self.assertEqual(view['scope_revisions'], [1, 3, 4])
        self.assertEqual(self.work.get(item['work_id'], self.now, 3)['criteria'], 'Changed')

    def test_title_only_edit_does_not_flag_changed_criteria(self):
        item = self.start(self.create())
        self.call(self.mutation('edit', item, title='New title'))
        self.assertFalse(self.work.get(item['work_id'], self.now)['criteria_changed_after_start'])

    def test_renewal_does_not_advance_stream_or_report_progress(self):
        item = self.start(self.create())
        before = self.work.get(item['work_id'], self.now)
        wake = []
        self.store.on_change = lambda: wake.append(self.store.head())
        r = dict(op='claim-renew', consumer='writer', work_id=item['work_id'],
                 claim_generation=item['claim']['generation'], if_claim_revision=1,
                 lease_seconds=1200, key='renew', deadline=self.now + 3600)
        result = self.call(r, now=self.now + 100)
        after = self.work.get(item['work_id'], self.now + 100)
        self.assertEqual(after['revision'], before['revision'])
        self.assertEqual(after['last_progress_at'], before['last_progress_at'])
        self.assertEqual(after['progress_deadline'], before['progress_deadline'])
        self.assertEqual(after['last_lease_expires'], before['last_lease_expires'])
        self.assertGreater(after['current_claim']['expires_at'], before['current_claim']['expires_at'])
        self.assertEqual(wake, [])
        self.assertEqual(self.call(r), dict(result, duplicate=True))
        expired = self.work.get(item['work_id'], self.now + 1301)
        self.assertIsNone(expired['current_claim'])
        self.assertEqual(expired['last_lease_expires'], before['last_lease_expires'])
        self.assertEqual(expired['observed_lease_expires'], self.now + 1300)
        self.assertTrue(expired['progress_unverified'])

    def test_overdue_then_update_rearms_credit_and_blocked_report(self):
        item = self.start(self.create())
        r = self.mutation('update', item, progress='Investigating', checkpoint='Stack trace',
            next_artifact='Fix', progress_deadline=self.now + 1000, lifecycle='blocked', blocker='Dependency')
        self.refusal('revision_conflict', r, now=self.now + 601, unchanged=False)
        view = self.work.get(item['work_id'], self.now + 601)
        self.assertTrue(view['progress_overdue'])
        self.assertEqual(self.store.work_debt().overdue, 0)
        r['if_revision'] = view['revision']
        result = self.call(r, now=self.now + 601)
        view = self.work.get(item['work_id'], self.now + 602)
        self.assertEqual(view['lifecycle'], 'blocked')
        self.assertFalse(view['progress_unverified'])
        self.assertEqual(self.store.work_debt().overdue, 1)
        self.assertEqual(view['revision'], result['revision'])

    def test_expiry_wins_once_reacquisition_and_stale_owner_refusal(self):
        item = self.start(self.create(), lease_seconds=60)
        later = self.now + 1000
        self.work.reconcile(item['work_id'], later)
        head = self.store.head()
        self.work.reconcile(item['work_id'], later)
        self.assertEqual(self.store.head(), head)
        self.assertEqual(self.store.db.execute('SELECT kind FROM work_events ORDER BY seq DESC LIMIT 1').fetchone()[0], 'lease-expired')
        old = self.mutation('finish', item, outcome='completed', references=['inert'])
        self.refusal('stale_claim', old, now=later)
        view = self.work.get(item['work_id'], later)
        renewed = self.call(self.request('start', work_id=item['work_id'], if_revision=view['revision'],
            checkpoint='Reconciled', next_artifact='Finish', progress_deadline=later + 600), now=later)
        self.assertGreater(renewed['claim']['generation'], item['claim']['generation'])
        self.refusal('stale_claim', old, now=later)

    def test_finished_retention_fixed_and_terminal_replay(self):
        item = self.start(self.create())
        r = self.mutation('finish', item, outcome='withdrawn', reason='Obsolete')
        result = self.call(r)
        view = self.work.get(item['work_id'], self.now)
        self.assertEqual(view['expires_at'], self.now + work_items.RETENTION)
        self.assertFalse(view['lease_valid'])
        self.assertEqual(self.call(r, now=self.now + 100), dict(result, duplicate=True))
        self.refusal('invalid_transition', self.request('propose', work_id=item['work_id'],
                    if_revision=result['revision'], proposed_assignee='other'))
        self.refusal('work_not_found', dict(op='work-get', work_id=item['work_id']),
                     now=self.now + work_items.RETENTION)
        self.assertEqual(self.work.snapshot_views(self.now + work_items.RETENTION), [])

    def test_release_preserves_checkpoint_and_atomic_rollback(self):
        item = self.start(self.create())
        r = self.mutation('release', item, checkpoint='Saved progress')
        wake = []
        self.store.on_change = lambda: wake.append(self.store.head())
        with patch.object(self.store, 'work_event', side_effect=RuntimeError('injected')):
            before = tuple(self.store.db.iterdump())
            with self.assertRaises(RuntimeError):
                self.call(r)
            self.assertEqual(tuple(self.store.db.iterdump()), before)
        self.assertEqual(wake, [])
        self.call(r)
        self.assertEqual(len(wake), 1)
        self.assertEqual(self.work.get(item['work_id'], self.now)['checkpoint'], 'Saved progress')

    def test_snapshot_current_once_delta_immutable_and_note_isolation(self):
        self.store.note('writer', 'decision', 'ordinary searchable note')
        item = self.create()
        snap = self.commands.command(dict(op='sync', consumer='reader', record_format=2), 1)
        work = [e for e in snap['entries'] if e['type'] == 'work-item']
        self.assertEqual(len(work), 1)
        self.assertEqual(work[0]['lifecycle'], 'open')
        started = self.start(item)
        again = self.commands.snapshot_page('reader', snap['snapshot_id'], {})
        self.assertEqual(again['entries'], snap['entries'])
        self.commands.ack(dict(consumer='reader', record_format=2, snapshot_id=snap['snapshot_id']))
        delta = self.commands.sync(dict(consumer='reader', record_format=2))
        self.assertEqual(delta['entries'][0]['event_kind'], 'started')
        self.call(self.mutation('release', started, checkpoint='Stopped'))
        self.assertEqual(self.commands.sync(dict(consumer='reader', record_format=2))['entries'][0]['payload']['lifecycle'], 'active')
        for field in ('revokes', 'supersedes'):
            with self.assertRaises(memory.MemoryError_) as caught:
                self.store.note('writer', 'directive', 'Cannot rewrite work', **{field: item['seq']})
            self.assertEqual(caught.exception.code, 'invalid_request')
        if self.store.fts:
            self.assertTrue(self.store.index_usable())
            self.assertTrue(self.commands.recall(dict(query='ordinary'))['indexed'])
            with self.store.transaction():
                self.store.set_meta('indexed_through', -1)
            self.store._reconcile_index()
            self.assertEqual(self.store.db.execute('SELECT count(*) FROM search').fetchone()[0], 1)

    def test_old_reader_guard_precedes_all_mutations(self):
        self.create()
        for op in ('sync', 'ack'):
            for fmt in (None, 1, True, 2.0, '2'):
                before = tuple(self.store.db.iterdump())
                with patch.object(self.store, 'maybe_expire', side_effect=AssertionError('maintenance called')):
                    with self.assertRaises(memory.MemoryError_) as caught:
                        self.commands.command(dict(op=op, consumer='old', record_format=fmt), 1)
                self.assertEqual(caught.exception.code, 'client_upgrade_required')
                self.assertEqual(tuple(self.store.db.iterdump()), before)

    def test_readonly_list_filters_bounds_and_no_cursor_updates(self):
        first = self.create(proposed_assignee='reviewer')
        self.create()
        self.start(first)
        self.store.blocked = 'synthetic blocked store'
        result = self.call(dict(op='work-list', limit=1))
        self.assertEqual(len(result['items']), 1)
        self.assertTrue(result['truncated'])
        result = self.call(dict(op='work-list', lifecycle='active', owner='writer', proposed_assignee='reviewer', stale=False, blocked=False))
        self.assertEqual([i['work_id'] for i in result['items']], [first['work_id']])
        self.assertEqual(self.store.db.execute('SELECT count(*) FROM cursors').fetchone()[0], 0)

    def test_strict_validation_and_encoded_limits(self):
        base = self.request('create', title='Title', criteria='Criteria', non_goals='No extras')
        for extra in ({'now': self.now}, {'criteria': True}, {'title': ''}, {'deadline': float('nan')},
                      {'consumer': 'x' * 129}, {'references': ['x' * 513]}):
            self.refusal('invalid_request', dict(base, **extra))
        # Raw UTF-8 fields fit, but JSON escaping makes the aggregate exceed 16 KiB.
        self.refusal('record_too_large', dict(base, criteria='\x01' * 4096))
        item = self.start(self.create())
        self.refusal('invalid_request', self.mutation('finish', item, outcome='completed'))
        self.refusal('invalid_request', self.mutation('finish', item, outcome='withdrawn', reason=''))

    def test_all_sixteen_funded_commands_end_at_ordinary_saturation(self):
        items = []
        for number in range(16):
            item = self.call(self.request('create', title='t' * 256,
                criteria='c' * 4096, non_goals='n' * 2048))
            items.append(self.start(item))
        written = 0
        while written < memory.MAX_ENTRIES:
            try:
                self.store.note('writer', 'decision', 'x' * memory.MAX_BODY)
                written += 1
            except memory.MemoryError_ as exc:
                self.assertEqual(exc.code, 'capacity')
                break
        self.assertGreater(written, 100)
        self.assertLess(written, memory.MAX_ENTRIES)
        held = self.store.usage()['idem']
        # The final sixteen replay slots belong to the admitted end operations.
        with patch.object(memory, 'MAX_IDEM_ROWS', held + 16):
            for number, item in enumerate(items):
                if number % 2:
                    r = self.mutation('release', item, checkpoint='c' * 1024)
                else:
                    r = self.mutation('finish', item, outcome='completed', references=['r' * 512] * 8)
                result = self.call(r)
                self.assertEqual(self.call(r), dict(result, duplicate=True))
        self.assertEqual(self.store.work_debt().event_slots, 0)
        self.assertEqual(self.store.db.execute('PRAGMA integrity_check').fetchone()[0], 'ok')

    def test_legacy_frozen_snapshot_resumes_then_work_arrives_as_delta(self):
        path = Path(self.tmp.name) / 'legacy.sqlite3'
        with patch.object(memory, 'SCHEMA', 4):
            old = memory.Store(path, self.store.repo, fts=False)
        self.addCleanup(old.close)
        commands = memory.MemoryCommands(self.tmp.name, old.repo, old)
        old.note('writer', 'decision', 'legacy note')
        snapshot = commands.sync(dict(consumer='reader'))
        payload_before = old.db.execute('SELECT payload FROM snapshot_items').fetchall()
        with old.transaction():
            work_schema.migrate(old.db, old.repo, memory.SCHEMA_STATEMENTS)
        work_items.WorkItems(old, memory.MemoryError_).command(
            self.request('create', title='New', criteria='Deliver', non_goals='Nothing extra'), now=self.now)
        resumed = commands.sync(dict(consumer='reader', record_format=2))
        self.assertEqual(resumed['entries'], snapshot['entries'])
        self.assertEqual(old.db.execute('SELECT payload FROM snapshot_items').fetchall(), payload_before)
        commands.ack(dict(consumer='reader', snapshot_id=resumed['snapshot_id'], record_format=2))
        delta = commands.sync(dict(consumer='reader', record_format=2))
        self.assertEqual(delta['entries'][0]['event_kind'], 'created')

    def test_malformed_or_unknown_request_cannot_reconcile_due_target(self):
        item = self.start(self.create(), lease_seconds=60)
        r = self.mutation('update', item, progress='Progress', checkpoint='Checkpoint',
                          next_artifact='Patch', progress_deadline=self.now + 10)
        self.refusal('invalid_request', r, now=self.now + 100)
        self.refusal('invalid_request', dict(r, invented=True), now=self.now + 100)
        self.refusal('work_not_found', dict(r, work_id='f' * 32, progress_deadline=self.now + 1000),
                     now=self.now + 100)
        self.assertEqual(self.store.head(), item['seq'])

    def test_end_record_event_and_replay_are_one_failure_boundary(self):
        item = self.start(self.create())
        r = self.mutation('finish', item, outcome='completed', references=['test output'])
        before = tuple(self.store.db.iterdump())
        with patch.object(self.work, 'store_replay', side_effect=RuntimeError('after event')):
            with self.assertRaises(RuntimeError):
                self.call(r)
        self.assertEqual(tuple(self.store.db.iterdump()), before)
        with patch.object(self.store, 'enforce_logical', side_effect=memory.MemoryError_('capacity', 'synthetic')):
            self.refusal('capacity', r)

    def test_new_scope_and_progress_debt_refuse_without_partial_effects(self):
        item = self.start(self.create())
        with patch.object(work_items, 'MAX_SCOPE', 10):
            self.refusal('record_too_large', self.mutation('edit', item, criteria='New scope'))
        self.work.reconcile(item['work_id'], self.now + 601)
        view = self.work.get(item['work_id'], self.now + 601)
        r = self.mutation('update', dict(item, revision=view['revision']), progress='Checked',
            checkpoint='Saved', next_artifact='Next', progress_deadline=self.now + 1000)
        cap = self.store.pages() + self.store.work_debt().pages + memory.COMMIT_SLACK
        with patch.object(memory, 'ORDINARY_MAX_PAGES', cap):
            self.refusal('capacity', r, now=self.now + 601)

    def test_cli_maps_resources_and_clear_proposal(self):
        import argparse
        p = argparse.ArgumentParser()
        work_items.cli_parsers(p.add_subparsers(dest='op'))
        args = vars(p.parse_args(['work', 'start', '0' * 32, '--path-resource', 'src',
                                 '--exact-resource', 'artifact', '--if-revision', '1',
                                 '--checkpoint', 'Saved', '--next-artifact', 'Patch',
                                 '--progress-deadline', '123', '--key', 'start', '--deadline', '123']))
        self.assertEqual(work_items.cli_request(args.pop('op'), args), 'work-start')
        self.assertEqual(args['resources'], [['path', 'src'], ['exact', 'artifact']])
        args = vars(p.parse_args(['work', 'propose', '0' * 32, '--if-revision', '1', '--clear-assignee']))
        self.assertEqual(work_items.cli_request(args.pop('op'), args), 'work-propose')
        self.assertTrue(args.pop('_clear_assignee'))
        self.assertIsNone(args['proposed_assignee'])

    def test_event_sequence_disagreement_rolls_back_every_table(self):
        before = tuple(self.store.db.iterdump())
        original = self.store.work_event
        def wrong_sequence(*args, **kwargs):
            return original(*args, **kwargs) + 1
        with patch.object(self.store, 'work_event', side_effect=wrong_sequence):
            with self.assertRaises(RuntimeError):
                self.create()
        self.assertEqual(tuple(self.store.db.iterdump()), before)

    def test_listing_uses_summaries_and_incremental_byte_bound(self):
        for number in range(110):
            self.call(self.request('create', title='\x01' * 256, criteria='Done', non_goals='No extras'))
        with patch.object(self.work, 'snapshot_views', side_effect=AssertionError('full views built')):
            result = self.call(dict(op='work-list'))
        self.assertTrue(result['truncated'])
        self.assertLess(len(result['items']), 100)
        self.assertLessEqual(len(work_items.encoded(result)), 48 * 1024)

    def test_mismatched_progress_epoch_is_corruption_not_repaired_silently(self):
        item = self.start(self.create())
        with self.store.transaction():
            self.store.db.execute('UPDATE claim_bundles SET progress_epoch=2')
        r = self.mutation('update', item, progress='Report', checkpoint='Saved',
                          next_artifact='Patch', progress_deadline=self.now + 700)
        self.refusal('incompatible_store', r)
        self.refusal('incompatible_store', r, now=self.now + 601)

    def test_replay_survives_new_service_generation_and_skips_due_reconciliation(self):
        item = self.create()
        r = self.request('start', work_id=item['work_id'], if_revision=1,
            checkpoint='Read', next_artifact='Patch', progress_deadline=self.now + 600,
            lease_seconds=60)
        r.update(repo=self.store.repo, generation='old-service')
        accepted = self.call(r)
        before = tuple(self.store.db.iterdump())
        replay = self.call(dict(r, generation='new-service'), now=self.now + 100)
        self.assertEqual(replay, dict(accepted, duplicate=True))
        self.assertEqual(tuple(self.store.db.iterdump()), before)

    def test_optional_renewal_with_update_is_atomic_and_uses_claim_revision(self):
        item = self.start(self.create())
        request = self.mutation('update', item, progress='Updated', checkpoint='Saved',
            next_artifact='Next patch', progress_deadline=self.now + 1200, renew_for=1200)
        result = self.call(request, now=self.now + 10)
        self.assertEqual(result['claim']['revision'], 2)
        self.assertEqual(result['claim']['expires_at'], self.now + 1210)
        self.assertEqual(self.store.head(), item['seq'] + 1)

    def test_request_error_codes_remain_explicitly_classified(self):
        import ast
        tree = ast.parse(Path(work_items.__file__).read_text())
        emitted = {n.args[0].value for n in ast.walk(tree) if isinstance(n, ast.Call)
                   and isinstance(n.func, ast.Attribute) and n.func.attr == 'fail'
                   and n.args and isinstance(n.args[0], ast.Constant)}
        self.assertTrue(emitted <= work_items.ERROR_CODES)
        classified = set().union(*memory.ERROR_EXIT_CLASSES.values())
        self.assertTrue(work_items.ERROR_CODES <= classified)

    def test_work_mutation_runs_legacy_expiry_after_validation_and_replay(self):
        note = self.store.note('writer', 'finding', 'old note', expires=self.now - 1)
        self.store.expired_at = 0
        self.create()
        self.assertIsNone(self.store.db.execute('SELECT seq FROM entries WHERE seq=?',
                                               (note['seq'],)).fetchone())
        request = self.request('create', title='Replay', criteria='Done', non_goals='No extras')
        accepted = self.call(request)
        with patch.object(self.store, 'maybe_expire', side_effect=AssertionError('cleanup ran')):
            self.assertEqual(self.call(request), dict(accepted, duplicate=True))
            self.refusal('invalid_request', dict(request, now=self.now))
            self.call(dict(op='work-get', work_id=accepted['work_id']))


if __name__ == '__main__':
    unittest.main()
