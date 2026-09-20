"""Staged work commands. Reported data and advisory ownership never grant authority.

The existing Store owns the connection, transactions, stream publication and budgets.
No filesystem access, background scheduling, or production schema activation lives here.
"""
import hashlib
import json
import time
import work_storage

import claims
import work_schema

RETENTION = 30 * 86400
MAX_RECORD = 16 * 1024
MAX_SCOPE = 8 * 1024
MAX_RESULT = 2 * 1024
ERROR_CODES = frozenset(('work_not_found', 'revision_conflict', 'stale_claim',
    'claim_conflict', 'claim_capacity', 'invalid_transition', 'record_too_large',
    'invalid_request', 'incompatible_store', 'capacity', 'idempotency_conflict', 'retry_deadline_expired'))
LIMITS = dict(title=256, criteria=4096, non_goals=2048, progress=2048,
              checkpoint=1024, next_artifact=1024, blocker=1024, reason=1024)
COMMON = {'op', 'repo', 'generation', 'consumer', 'author', 'key', 'deadline'}
FIELDS = {
    'work-create': {'title', 'criteria', 'non_goals', 'proposed_assignee', 'references'},
    'work-get': {'work_id', 'revision'},
    'work-list': {'lifecycle', 'owner', 'proposed_assignee', 'stale', 'blocked', 'limit'},
    'work-propose': {'work_id', 'if_revision', 'proposed_assignee'},
    'work-edit': {'work_id', 'if_revision', 'claim_generation', 'title', 'criteria', 'non_goals'},
    'work-start': {'work_id', 'if_revision', 'checkpoint', 'next_artifact',
                   'progress_deadline', 'lease_seconds', 'resources'},
    'work-update': {'work_id', 'if_revision', 'claim_generation', 'progress',
                    'checkpoint', 'next_artifact', 'progress_deadline', 'lifecycle',
                    'blocker', 'references', 'renew_for'},
    'work-release': {'work_id', 'if_revision', 'claim_generation', 'checkpoint'},
    'work-finish': {'work_id', 'if_revision', 'claim_generation', 'outcome', 'reason', 'references'},
    'claim-renew': {'work_id', 'claim_generation', 'if_claim_revision', 'lease_seconds'},
}
READS = {'work-get', 'work-list'}


def encoded(value):
    return json.dumps(value, ensure_ascii=True, sort_keys=True,
                      allow_nan=False)


def freshness(lifecycle, progress_deadline, active, lease_expires, now):
    """One observation rule for full views and list summaries/filters."""
    live = bool(active and lease_expires > now)
    ongoing = lifecycle in ('active', 'blocked')
    overdue = bool(ongoing and progress_deadline is not None and progress_deadline <= now)
    return live, overdue, bool(ongoing and (not live or overdue))


def cli_parsers(sub):
    """Provider-neutral CLI; server-side validation remains authoritative."""
    work = sub.add_parser('work', help='staged work commands; requires schema-5 service')
    operations = work.add_subparsers(dest='work_op', required=True)
    for op in FIELDS:
        if not op.startswith('work-'):
            continue
        name = op[5:]
        p = operations.add_parser(name)
        if name not in ('create', 'list'):
            p.add_argument('work_id')
        for field in sorted(FIELDS[op] - {'work_id'}):
            flag = '--' + field.replace('_', '-')
            if field in ('if_revision', 'claim_generation', 'revision', 'limit', 'lease_seconds', 'renew_for'):
                p.add_argument(flag, type=int)
            elif field == 'progress_deadline':
                p.add_argument(flag, type=float)
            elif field in ('stale', 'blocked'):
                p.add_argument(flag, action='store_true', default=None)
            elif field == 'references':
                p.add_argument('--reference', dest=field, action='append')
            elif field == 'resources':
                p.add_argument('--path-resource', action='append')
                p.add_argument('--exact-resource', action='append')
            else:
                p.add_argument(flag)
        if op not in READS:
            p.add_argument('--author', help='asserted provenance, never authority')
            p.add_argument('--key')
            p.add_argument('--deadline', type=float)
        if name == 'propose':
            p.add_argument('--clear-assignee', action='store_true')
    claim = sub.add_parser('claim')
    renew = claim.add_subparsers(dest='claim_op', required=True).add_parser('renew')
    renew.add_argument('work_id')
    renew.add_argument('--claim-generation', required=True, type=int,
                       help='durable writer generation, not service-instance generation')
    renew.add_argument('--if-claim-revision', required=True, type=int)
    renew.add_argument('--lease-seconds', type=int)
    renew.add_argument('--key')
    renew.add_argument('--deadline', type=float)
    renew.add_argument('--author', help='asserted provenance, never authority')


def cli_request(op, args):
    if op == 'work':
        op = 'work-' + args.pop('work_op')
        if op == 'work-start':
            args['resources'] = ([['path', key] for key in args.pop('path_resource') or []]
                                 + [['exact', key] for key in args.pop('exact_resource') or []])
        if op == 'work-propose' and args.pop('clear_assignee'):
            if args.get('proposed_assignee') is not None:
                raise ValueError('choose an assignee or --clear-assignee')
            # Preserve explicit null through the ordinary CLI None filtering.
            args['proposed_assignee'] = None
            args['_clear_assignee'] = True
    elif op == 'claim':
        op = 'claim-' + args.pop('claim_op')
    return op


class WorkItems:
    def __init__(self, store, error_factory):
        self.store, self.db, self.error = store, store.db, error_factory

    def fail(self, code, detail):
        raise self.error(code, detail)

    def bounded(self, value, maximum):
        text = encoded(value)
        if len(text.encode('utf-8')) > maximum:
            self.fail('record_too_large', 'encoded work payload exceeds its byte bound')
        return text

    def row(self, sql, args=()):
        cursor = self.db.execute(sql, args)
        row = cursor.fetchone()
        return dict(zip((c[0] for c in cursor.description), row)) if row else None

    def record(self, work_id, now):
        record = self.row('SELECT * FROM work_items WHERE work_id=?', (work_id,))
        if record is None or (record['expires_at'] is not None and record['expires_at'] <= now):
            self.fail('work_not_found', 'work item is absent or its retention has expired')
        return record

    def claim(self, work_id):
        return self.row('SELECT * FROM claim_bundles WHERE work_id=?', (work_id,))

    def view(self, record, now):
        item = dict(record)
        item['references'] = json.loads(item.pop('references_json'))
        item.update(type='work-item', seq=item['latest_seq'], observed_at=now)
        held = self.claim(item['work_id'])
        live, overdue, unverified = freshness(item['lifecycle'], item['progress_deadline'],
            held['active'] if held else False, held['expires_at'] if held else None, now)
        # Historical event fields are not rewritten by renewal. Expose the
        # deadline used for this observation even when the retained lease is
        # expired and therefore cannot appear as a current claim.
        item['observed_lease_expires'] = held['expires_at'] if held else None
        item['current_claim'] = None
        if live:
            item['current_claim'] = {k: held[k] for k in
                                    ('consumer', 'generation', 'revision', 'expires_at')}
            item['current_claim']['resources'] = [list(r) for r in self.db.execute(
                'SELECT kind,resource FROM claim_resources WHERE generation=? ORDER BY ordinal',
                (held['generation'],))]
        item['lease_valid'] = live
        ongoing = item['lifecycle'] in ('active', 'blocked')
        item['progress_overdue'] = overdue
        reasons = []
        if ongoing and not live:
            reasons.append('lease_expired')
        if item['progress_overdue']:
            reasons.append('progress_deadline_missed')
        item['progress_unverified'] = unverified
        item['progress_unverified_reasons'] = reasons
        scopes = self.db.execute('SELECT revision,criteria,non_goals FROM work_scope_revisions '
                                 'WHERE work_id=? ORDER BY revision', (item['work_id'],)).fetchall()
        item['scope_revisions'] = [r[0] for r in scopes]
        first = item['first_start_revision']
        item['criteria_changed_after_start'] = bool(first and any(
            revision > first and (criteria, non_goals) != scopes[i - 1][1:]
            for i, (revision, criteria, non_goals) in enumerate(scopes) if i))
        self.bounded(item, MAX_RECORD)
        return item

    def get(self, work_id, now, revision=None):
        claims.work_key(work_id)
        record = self.record(work_id, now)
        if revision is None:
            return self.view(record, now)
        claims.integer(revision, 'revision')
        scope = self.row('SELECT * FROM work_scope_revisions WHERE work_id=? AND revision=?',
                         (work_id, revision))
        if scope is None:
            self.fail('work_not_found', 'scope revision is not retained')
        return scope

    def snapshot_views(self, now):
        return [self.view(self.record(row[0], now), now) for row in self.db.execute(
            'SELECT work_id FROM work_items WHERE expires_at IS NULL OR expires_at>? '
            'ORDER BY work_id', (now,)).fetchall()]

    def listing(self, request, now):
        results, truncated = [], False
        used = len(encoded(dict(items=[], truncated=False, observed_at=now)))
        rows = self.db.execute('SELECT w.work_id,w.revision,w.title,w.lifecycle,'
            'w.proposed_assignee,w.progress_deadline,b.consumer,b.active,b.expires_at '
            'FROM work_items w LEFT JOIN claim_bundles b ON b.work_id=w.work_id '
            'WHERE w.expires_at IS NULL OR w.expires_at>? ORDER BY w.work_id', (now,))
        for work_id, revision, title, lifecycle, proposed, deadline, consumer, active, expires in rows:
            live, _, stale = freshness(lifecycle, deadline, active, expires, now)
            owner = consumer if live else None
            tests = dict(owner=owner, lifecycle=lifecycle, proposed_assignee=proposed,
                         stale=stale, blocked=lifecycle == 'blocked')
            if any(request[k] != v for k, v in tests.items() if k in request):
                continue
            summary = dict(work_id=work_id, revision=revision, title=title, lifecycle=lifecycle,
                proposed_assignee=proposed, progress_unverified=bool(stale), progress_deadline=deadline,
                lease_valid=live, owner=owner, observed_at=now)
            size = len(encoded(summary)) + (2 if results else 0)
            if len(results) >= request.get('limit', 100) or used + size > 48 * 1024:
                truncated = True
                break
            results.append(summary)
            used += size
        return dict(items=results, truncated=truncated, observed_at=now)

    def validate(self, r):
        op = r.get('op')
        if op not in FIELDS or set(r) - (COMMON | FIELDS[op]):
            self.fail('invalid_request', 'unknown work operation or request fields')
        if op not in READS:
            claims.consumer_key(r.get('consumer'))
        for field in ('author', 'proposed_assignee', 'owner'):
            if r.get(field) is not None:
                claims.consumer_key(r[field])
        if op not in ('work-create', 'work-list'):
            claims.work_key(r.get('work_id'))
        if op not in READS | {'work-create', 'claim-renew'}:
            claims.integer(r.get('if_revision'), 'if_revision')
        for field in ('claim_generation', 'if_claim_revision', 'revision'):
            if field in r:
                claims.integer(r[field], field)
        if op in ('work-update', 'work-release', 'work-finish', 'claim-renew'):
            claims.integer(r.get('claim_generation'), 'claim_generation')
        if op == 'claim-renew':
            claims.integer(r.get('if_claim_revision'), 'if_claim_revision')
        for field, maximum in LIMITS.items():
            if field in r:
                if not isinstance(r[field], str):
                    self.fail('invalid_request', field + ' must be text')
                if r[field]:
                    claims.text(r[field], field, maximum)
        required = {'work-create': ('title', 'criteria', 'non_goals'),
                    'work-start': ('checkpoint', 'next_artifact'),
                    'work-update': ('progress', 'checkpoint', 'next_artifact'),
                    'work-release': ('checkpoint',)}.get(op, ())
        if any(not isinstance(r.get(k), str) or not r[k].strip() for k in required):
            self.fail('invalid_request', 'required work text is missing or empty')
        if op == 'work-edit' and not any(k in r for k in ('title', 'criteria', 'non_goals')):
            self.fail('invalid_request', 'edit requires at least one scope field')
        if op in ('work-create', 'work-edit') and any(k in r and not r[k].strip()
                for k in ('title', 'criteria', 'non_goals')):
            self.fail('invalid_request', 'scope text must not be empty')
        if op == 'work-propose' and 'proposed_assignee' not in r:
            self.fail('invalid_request', 'proposal requires an assignee or explicit null')
        if op in ('work-start', 'work-update'):
            claims.timestamp(r.get('progress_deadline'))
        for field in ('lease_seconds', 'renew_for'):
            if field in r:
                claims.integer(r[field], field, claims.MIN_LEASE, claims.MAX_LEASE)
        if 'resources' in r:
            claims.bundle(r['work_id'], r['resources'])
        if 'references' in r:
            refs = r['references']
            if not isinstance(refs, list) or len(refs) > 8:
                self.fail('invalid_request', 'at most eight inert references are allowed')
            for ref in refs:
                claims.text(ref, 'reference', 512)
        if op == 'work-finish':
            if r.get('outcome') not in ('completed', 'withdrawn'):
                self.fail('invalid_request', 'finish requires completed or withdrawn outcome')
            if r['outcome'] == 'completed' and not r.get('references'):
                self.fail('invalid_request', 'completion requires evidence references')
            if r['outcome'] == 'withdrawn' and not r.get('reason', '').strip():
                self.fail('invalid_request', 'withdrawal requires a reason')
        if 'lifecycle' in r:
            allowed = ('active', 'blocked') if op == 'work-update' else ('open', 'active', 'blocked', 'finished')
            if r['lifecycle'] not in allowed:
                self.fail('invalid_request', 'invalid lifecycle filter or transition')
        if op == 'work-update' and r.get('lifecycle') == 'blocked' and not r.get('blocker', '').strip():
            self.fail('invalid_request', 'blocked progress requires a blocker')
        for field in ('stale', 'blocked'):
            if field in r and type(r[field]) is not bool:
                self.fail('invalid_request', field + ' must be a boolean')
        if 'limit' in r:
            claims.integer(r['limit'], 'limit', 1, 100)
        keyed = 'key' in r or 'deadline' in r
        if keyed or op in ('work-create', 'work-start'):
            claims.text(r.get('key'), 'key', 128)
            claims.timestamp(r.get('deadline'))
        return keyed or op in ('work-create', 'work-start')

    def replay(self, r, now, keyed):
        if not keyed:
            return None, None, None
        if r['deadline'] <= now:
            self.fail('retry_deadline_expired', 'retry deadline has expired; query to reconcile')
        if r['deadline'] > now + 86400:
            self.fail('invalid_request', 'retry deadline exceeds 24 hours')
        key = 'work:' + hashlib.sha256(encoded([self.store.repo, r['consumer'], r['key']]).encode()).hexdigest()
        # Service-instance generation is transport routing, not work identity.
        content = {k: v for k, v in r.items() if k not in ('generation', 'repo')}
        fingerprint = hashlib.sha256(encoded(content).encode()).hexdigest()
        found = self.db.execute('SELECT fingerprint,operation,result FROM idem WHERE key=?',
                                (key,)).fetchone()
        if found:
            if found[:2] != (fingerprint, r['op']):
                self.fail('idempotency_conflict', 'work replay key is bound to a different request')
            return key, fingerprint, dict(json.loads(found[2]), duplicate=True)
        return key, fingerprint, None

    def engine(self):
        def admit(before, after, control):
            self.store.enforce_pages(control, after)
            self.store.enforce_logical(control, after)
        return claims.LeaseEngine(self.db, admit)

    def write_record(self, item):
        fields = [k for k in item if k != 'work_id']
        self.db.execute('UPDATE work_items SET ' + ','.join(k + '=?' for k in fields)
                        + ' WHERE work_id=?', [item[k] for k in fields] + [item['work_id']])

    def scope(self, item, r, now):
        values = dict(work_id=item['work_id'], revision=item['revision'], ts=now,
                      consumer=r['consumer'], author=r.get('author'),
                      **{k: item[k] for k in ('title', 'criteria', 'non_goals')})
        self.bounded(values, MAX_SCOPE)
        self.db.execute('INSERT INTO work_scope_revisions VALUES (?,?,?,?,?,?,?,?)', tuple(values.values()))

    def event(self, item, kind, consumer, author, pid, now):
        if work_storage.row_charge(item.values()) > 16857:
            self.fail('record_too_large', 'stored work record exceeds the physical proof bound')
        item['latest_seq'] = self.store.head() + 1
        payload = self.view(item, now)
        seq = self.store.work_event(kind, item['work_id'], item['revision'], payload,
                                    consumer=consumer, author=author, pid=pid, now=now)
        if seq != item['latest_seq']:
            raise RuntimeError('work event sequence changed during one atomic mutation')
        item['latest_seq'] = seq
        self.write_record(item)

    def reconcile(self, work_id, now):
        """One target, one due event; expiry takes precedence over overdue progress."""
        item, held = self.record(work_id, now), self.claim(work_id)
        if not held or not held['active']:
            return
        if held['progress_epoch'] != item['progress_epoch']:
            self.fail('incompatible_store', 'work and claim progress epochs disagree')
        expired = held['expires_at'] <= now
        overdue = (item['progress_deadline'] is not None and item['progress_deadline'] <= now
                   and not held['overdue_recorded'])
        if not expired and not overdue:
            return
        with self.store.transaction(control=True):
            item['revision'] = claims.integer(item['revision'] + 1, 'revision')
            if expired:
                self.engine().expire(held['generation'], now=now)
                item['lease_expired'] = 1
            else:
                self.engine().overdue(held['generation'], item['progress_epoch'])
            self.event(item, 'lease-expired' if expired else 'progress-overdue',
                       held['consumer'], None, None, now)

    def due_targets(self, now, limit):
        return [row[0] for row in self.db.execute(
            'SELECT w.work_id FROM work_items w JOIN claim_bundles b ON b.work_id=w.work_id '
            'WHERE b.active=1 AND (b.expires_at<=? OR '
            '(w.progress_deadline<=? AND b.overdue_recorded=0)) '
            'ORDER BY b.expires_at,w.work_id LIMIT ?', (now, now, limit))]

    def maintenance_counts(self, now):
        pending = self.db.execute('SELECT count(*) FROM claim_bundles b '
            'JOIN work_items w ON w.work_id=b.work_id WHERE b.active=1 '
            'AND (b.expires_at<=? OR (w.progress_deadline<=? AND b.overdue_recorded=0))',
            (now, now)).fetchone()[0]
        expired = self.db.execute("SELECT count(*) FROM work_items WHERE lifecycle='finished' "
                                  'AND expires_at<=?', (now,)).fetchone()[0]
        inactive = self.engine().inactive_count()
        return dict(pending_due=pending, expired_items=expired, inactive_bundles=inactive)

    def reclaim_finished_one(self, now):
        """Remove one entire expired item; snapshots and replay results stay intact."""
        selected = self.db.execute("SELECT work_id FROM work_items WHERE lifecycle='finished' "
            'AND expires_at<=? ORDER BY expires_at,work_id LIMIT 1', (now,)).fetchone()
        if selected is None:
            return False
        work_id = selected[0]
        with self.store.transaction(control=True):
            item = self.row("SELECT * FROM work_items WHERE work_id=? AND lifecycle='finished' "
                            'AND expires_at<=?', (work_id, now))
            if item is None:
                return False
            held = self.claim(work_id)
            if held:
                if held['active']:
                    self.fail('incompatible_store', 'finished work still has an active claim')
                # Inactive resources must be reclaimed through ordinary admission.
                return False
            scopes = self.db.execute('SELECT count(*) FROM work_scope_revisions WHERE work_id=?',
                                     (work_id,)).fetchone()[0]
            if not 1 <= scopes <= work_storage.MAX_SCOPES_PER_ITEM:
                self.fail('incompatible_store', 'expired work scope history exceeds retained bounds')
            self.store.reclaim_work_events(work_id, item['latest_seq'])
            self.db.execute('DELETE FROM work_scope_revisions WHERE work_id=?', (work_id,))
            self.db.execute('DELETE FROM work_items WHERE work_id=?', (work_id,))
        return True

    def command(self, request, pid=None, now=None):
        now = time.time() if now is None else now
        try:
            return self._command(dict(request), pid, now)
        except claims.ClaimError as exc:
            self.fail(exc.code, dict(message=str(exc), **exc.details))
        except work_schema.SchemaError as exc:
            self.fail(exc.code, str(exc))

    def _command(self, r, pid, now):
        claims.timestamp(now)
        keyed = self.validate(r)
        op = r['op']
        if op in READS:
            # One owner/connection serializes these reads; an explicit read transaction
            # keeps the view internally consistent without a write-capacity precondition.
            self.db.execute('BEGIN')
            try:
                return (self.get(r['work_id'], now, r.get('revision')) if op == 'work-get'
                        else self.listing(r, now))
            finally:
                self.db.execute('ROLLBACK')
        key, fingerprint, replay = self.replay(r, now, keyed)
        if replay is not None:
            return replay
        if op in ('work-start', 'work-update') and not now < r['progress_deadline'] <= now + 86400:
            self.fail('invalid_request', 'progress deadline must be within the next day')
        if op != 'work-create':
            self.record(r['work_id'], now)  # Unknown targets never trigger maintenance.
        # Preserve legacy lifetime enforcement on work-driven stores too. Replay
        # and read-only requests remain available without cleanup writes.
        self.store.maybe_expire()
        if op != 'work-create':
            self.reconcile(r['work_id'], now)
        if op == 'work-start':
            held = self.claim(r['work_id'])
            if held and not held['active']:
                with self.store.transaction(control=False):
                    self.db.execute('DELETE FROM claim_resources WHERE generation=?', (held['generation'],))
                    self.db.execute('DELETE FROM claim_bundles WHERE generation=? AND active=0', (held['generation'],))
        control = op in ('work-release', 'work-finish')
        with self.store.transaction(control=control):
            engine = self.engine()
            if op == 'work-create':
                work_id = work_schema.allocate(self.db, 'work_id_counter')
                self.db.execute('INSERT INTO work_items '
                    '(work_id,revision,lifecycle,title,criteria,non_goals,proposed_assignee,'
                    'created_at,created_consumer,scope_revision,references_json,latest_seq) '
                    "VALUES (?,1,'open',?,?,?,?,?,?,1,?,?)", (work_id, r['title'], r['criteria'],
                    r['non_goals'], r.get('proposed_assignee'), now, r['consumer'],
                    encoded(r.get('references', [])), self.store.head() + 1))
                item = self.record(work_id, now)
                self.scope(item, r, now)
                kind = 'created'
            else:
                item = self.record(r['work_id'], now)
                if item['lifecycle'] == 'finished':
                    self.fail('invalid_transition', 'finished work is terminal')
                held = self.claim(item['work_id'])
                owner_required = op in ('work-update', 'work-release', 'work-finish', 'claim-renew')
                owner_required |= op == 'work-edit' and bool(held and held['active'] and held['expires_at'] > now)
                if owner_required:
                    owner = engine.owner(item['work_id'], r['consumer'], r.get('claim_generation'), now=now)
                    if owner['progress_epoch'] != item['progress_epoch']:
                        self.fail('incompatible_store', 'work and claim progress epochs disagree')
                if op == 'claim-renew':
                    renewed = engine.renew(item['work_id'], r['consumer'], r['claim_generation'],
                        r['if_claim_revision'], now=now, duration=r.get('lease_seconds', claims.DEFAULT_LEASE))
                    result = dict(work_id=item['work_id'], revision=item['revision'], claim=renewed,
                                  seq=None, duplicate=False)
                    self.store_replay(key, fingerprint, r, result, now)
                    return result
                if r['if_revision'] != item['revision']:
                    self.fail('revision_conflict', dict(current_revision=item['revision'],
                        detail='target reconciliation or inactive-bundle cleanup may already have committed; reread work'))
                item['revision'] = claims.integer(item['revision'] + 1, 'revision')
                kind = {'work-propose': 'proposed', 'work-edit': 'edited', 'work-start': 'started',
                        'work-update': 'updated', 'work-release': 'released', 'work-finish': 'finished'}[op]
                if op == 'work-propose':
                    item['proposed_assignee'] = r['proposed_assignee']
                elif op == 'work-edit':
                    for field in ('title', 'criteria', 'non_goals'):
                        if field in r:
                            item[field] = r[field]
                    item['scope_revision'] = item['revision']
                    self.scope(item, r, now)
                elif op in ('work-start', 'work-update'):
                    if not now < r['progress_deadline'] <= now + 86400:
                        self.fail('invalid_request', 'progress deadline must be within the next day')
                    item['progress_epoch'] = claims.integer(item['progress_epoch'] + 1, 'progress_epoch')
                    if op == 'work-start':
                        held = engine.acquire(item['work_id'], r['consumer'], item['progress_epoch'],
                            r.get('resources', ()), now=now, duration=r.get('lease_seconds', claims.DEFAULT_LEASE))
                        item.update(last_writer=r['consumer'], last_generation=held['generation'],
                                    last_lease_expires=held['expires_at'], lease_expired=0)
                        item['first_start_revision'] = item['first_start_revision'] or item['revision']
                    else:
                        engine.progress(item['work_id'], r['consumer'], r['claim_generation'],
                                        item['progress_epoch'], now=now)
                        if 'renew_for' in r:
                            held = engine.owner(item['work_id'], r['consumer'], r['claim_generation'], now=now)
                            engine.renew(item['work_id'], r['consumer'], r['claim_generation'],
                                         held['revision'], now=now, duration=r['renew_for'])
                    item.update(lifecycle=r.get('lifecycle', 'active'), last_progress_at=now,
                                progress_deadline=r['progress_deadline'], checkpoint=r['checkpoint'],
                                next_artifact=r['next_artifact'], progress=r.get('progress', ''),
                                blocker=r.get('blocker', '') if r.get('lifecycle') == 'blocked' else '')
                    if 'references' in r:
                        item['references_json'] = encoded(r['references'])
                elif control:
                    engine.release(item['work_id'], r['consumer'], r['claim_generation'], now=now)
                    item['progress_deadline'] = None
                    if op == 'work-release':
                        item.update(lifecycle='open', checkpoint=r['checkpoint'], blocker='')
                    else:
                        item.update(lifecycle='finished', outcome=r['outcome'], reason=r.get('reason', ''),
                                    references_json=encoded(r.get('references', [])),
                                    finished_at=now, expires_at=now + RETENTION)
            self.event(item, kind, r['consumer'], r.get('author'), pid, now)
            result = dict(work_id=item['work_id'], revision=item['revision'], seq=item['latest_seq'],
                          duplicate=False)
            held = self.claim(item['work_id'])
            if op in ('work-start', 'work-update') and held:
                result['claim'] = {k: held[k] for k in ('generation', 'revision', 'expires_at')}
            self.store_replay(key, fingerprint, r, result, now)
            return result

    def store_replay(self, key, fingerprint, r, result, now):
        payload = self.bounded(result, MAX_RESULT)
        if key is not None:
            self.db.execute('INSERT INTO idem(key,fingerprint,seq,ts,deadline,operation,result) '
                            'VALUES (?,?,?,?,?,?,?)',
                            (key, fingerprint, result['seq'], now, r['deadline'], r['op'], payload))
