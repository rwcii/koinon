#!/usr/bin/env python3
"""Generate the committed Python-era state trees that `koinon import` tests read.

The trees are written by the Python runtime of the pinned `main` release itself, taken
from a `git archive` of that commit, so they have that release's schemas. They are
generated once and committed under internal/importer/testdata/; tests and the native
workflow read the committed trees and never run this script. Rerun it only to change
the fixtures:

    python3 scripts/make-import-fixtures.py

Repository paths are placeholders under /koinon-fixture; the memory store keys are the
hashes of those exact strings, as the Python runtime computes them. The clock is fixed,
so every timestamp, lease and expiry is relative to BASE in fixture.json.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time

BASELINE = '3d60c73c88f3110ca4bc91a9a0cc7e16f7daf827'
BASE = 1790000000.0
ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / 'internal' / 'importer' / 'testdata' / 'python-state'
REPOS = {
    'a': '/koinon-fixture/repo-a/.git',  # resolved through install.json
    'c': '/koinon-fixture/repo-c/.git',  # resolved through an inbox memory binding
    'b': '/koinon-fixture/repo-b/.git',  # resolved by nothing: repository_unresolved
}
# The state root as install.json and inbox bindings record it; tests pass the real tree.
PLACEHOLDER_STATE = '/koinon-fixture/state'
THREADS = {
    'codex': '019a0000-0000-7000-8000-00000000000a',
    'deepseek': 'dsh-fixture-session-b',
    'legacy': '019a0000-0000-7000-8000-0000000000ff',
}


class Clock:
    """A settable clock patched over time.time in the baseline modules."""
    def __init__(self):
        self.now = BASE

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def key_of(path):
    return hashlib.sha256(path.encode()).hexdigest()[:16]


def extract(destination):
    data = subprocess.run(['git', '-C', str(ROOT), 'archive', BASELINE], check=True,
                          capture_output=True).stdout
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        archive.extractall(destination, filter='data')


def frame(content, sender, name=None, msg_id=None):
    if name is not None:
        content = f'<cross-session-message from="{sender}" from-name="{name}">\n{content}\n</cross-session-message>'
    result = {'msgV': 1, 'type': 'user', 'priority': 'next', 'from': sender,
              'message': {'role': 'user', 'content': content}}
    if msg_id:
        result['msg_id'] = msg_id
    return result


def make_inbox(bridge, directory, clock, messages, *, ack_through=0, controls=0, binding=None, handled=()):
    """Store messages through the baseline InboxStore, then acknowledge and bind."""
    store = bridge.InboxStore(directory)
    try:
        for item in messages:
            clock.advance(7)
            store.store(os.getpid(), item)
        for _ in range(controls):
            clock.advance(3)
            store.store(os.getpid(), {'type': 'control', 'from': 'uds:/tmp/cc-socks/fixture-claude.sock',
                                      'request': {'subtype': 'ping'}})
        for seq in handled:
            store.command({'op': 'handled', 'seq': seq, 'outcome': 'done'})
        if ack_through:
            store.command({'op': 'ack', 'through': ack_through})
        if binding is not None:
            created = store.bind_memory(binding)
            clock.advance(5)
            store.refresh_memory(created, {'store_id': binding['store_id'], 'head': 3, 'pid': os.getpid()})
    finally:
        store.close()


def note(commands, consumer, body, **extra):
    request = dict(op='note', consumer=consumer, type=extra.pop('type', 'finding'), body=body, **extra)
    return commands.command(request, os.getpid())


def work(commands, consumer, op, **fields):
    return commands.command(dict(op=op, consumer=consumer, **fields), os.getpid())


def make_memory(memory, state_root, key, clock, *, full):
    home = state_root / 'memory' / key
    home.mkdir(parents=True, mode=0o700)
    store = memory.Store(home / 'memory.sqlite3', key)
    commands = memory.MemoryCommands(state_root, key, store)
    try:
        first = note(commands, 'consumer-alpha', 'The import keeps every record.', author='fixture-author',
                     key='note-1', deadline=clock() + 600)
        if not full:
            return dict(store_id=store.meta('store_id'))
        clock.advance(10)
        second = note(commands, 'consumer-alpha', 'Superseded finding.', path='docs/INSTALL.md')
        clock.advance(10)
        note(commands, 'consumer-beta', 'A replacement finding.', supersedes=second['seq'], scope='task',
             scope_target='chunk-11')
        clock.advance(10)
        withdrawn = note(commands, 'consumer-beta', 'A finding to withdraw.')
        clock.advance(10)
        note(commands, 'consumer-beta', 'Withdrawn.', revokes=withdrawn['seq'])
        clock.advance(10)
        note(commands, 'consumer-alpha', 'An expiring note.', expires=clock() + 7 * 86400, type='directive')
        # consumer-alpha takes and acknowledges a snapshot; consumer-beta leaves one pending.
        page = commands.command(dict(op='sync', consumer='consumer-alpha', record_format=2), os.getpid())
        while page.get('next_page_token') is not None:
            page = commands.command(dict(op='sync', consumer='consumer-alpha', record_format=2,
                                         snapshot_id=page['snapshot_id'], page_token=page['next_page_token']),
                                    os.getpid())
        commands.command(dict(op='ack', consumer='consumer-alpha', record_format=2,
                              snapshot_id=page['snapshot_id']), os.getpid())
        # consumer-idle registers, then idles past the retirement age; the active consumers
        # are touched just before, so only consumer-idle is retired.
        commands.command(dict(op='sync', consumer='consumer-idle', record_format=2), os.getpid())
        clock.advance(memory.CONSUMER_TTL - 100)
        commands.command(dict(op='sync', consumer='consumer-alpha', record_format=2), os.getpid())
        commands.command(dict(op='status', consumer='consumer-beta'), os.getpid())
        commands.command(dict(op='sync', consumer='consumer-beta', record_format=2), os.getpid())
        clock.advance(200)
        note(commands, 'consumer-alpha', 'A delta after the snapshot.', key='note-delta', deadline=clock() + 600)
        # consumer-beta leaves a snapshot pending.
        commands.command(dict(op='sync', consumer='consumer-beta', record_format=2), os.getpid())
        # Work: one live claim, one claim whose lease expires, one finished item, one open item.
        deadline = lambda: clock() + 600
        live = work(commands, 'consumer-alpha', 'work-create', title='#1: live claim', criteria='c', non_goals='n',
                    key='create-1', deadline=deadline())
        expiring = work(commands, 'consumer-beta', 'work-create', title='#2: expiring claim', criteria='c',
                        non_goals='n', key='create-2', deadline=deadline())
        finished = work(commands, 'consumer-alpha', 'work-create', title='#3: finished', criteria='c',
                        non_goals='n', references=['https://example.com/3'], key='create-3', deadline=deadline())
        work(commands, 'consumer-beta', 'work-create', title='#4: open', criteria='c', non_goals='n',
             key='create-4', deadline=deadline())
        started = work(commands, 'consumer-beta', 'work-start', work_id=expiring['work_id'], if_revision=1,
                       checkpoint='started', next_artifact='lease', progress_deadline=clock() + 7200,
                       lease_seconds=60, key='start-2', deadline=deadline())
        done = work(commands, 'consumer-alpha', 'work-start', work_id=finished['work_id'], if_revision=1,
                    checkpoint='started', next_artifact='finish', progress_deadline=clock() + 7200,
                    lease_seconds=600, key='start-3', deadline=deadline())
        work(commands, 'consumer-alpha', 'work-finish', work_id=finished['work_id'], if_revision=done['revision'],
             claim_generation=done['claim']['generation'], outcome='completed', references=['https://example.com/pr/3'], key='finish-3', deadline=deadline())
        clock.advance(120)
        commands.maintain_work()
        held = work(commands, 'consumer-alpha', 'work-start', work_id=live['work_id'], if_revision=1,
                    checkpoint='started', next_artifact='import', progress_deadline=clock() + 14400,
                    lease_seconds=3600, resources=[['path', 'internal/importer']],
                    key='start-1', deadline=deadline())
        work(commands, 'consumer-alpha', 'work-update', work_id=live['work_id'], if_revision=held['revision'],
             claim_generation=held['claim']['generation'], progress='half', checkpoint='mapping',
             next_artifact='verify', progress_deadline=clock() + 14400, key='update-1', deadline=deadline())
        # A claim whose lease has expired, with no maintenance since: its bundle is retained.
        lapsed = work(commands, 'consumer-beta', 'work-create', title='#5: lapsed lease', criteria='c',
                      non_goals='n', key='create-5', deadline=deadline())
        work(commands, 'consumer-beta', 'work-start', work_id=lapsed['work_id'], if_revision=1,
             checkpoint='started', next_artifact='lapse', progress_deadline=clock() + 7200,
             lease_seconds=60, key='start-5', deadline=deadline())
        clock.advance(90)
        return dict(lapsed_work=lapsed['work_id'], store_id=store.meta('store_id'), live_work=live['work_id'], expired_work=expiring['work_id'],
                    finished_work=finished['work_id'], first_note=first['seq'],
                    expired_generation=started['claim']['generation'])
    finally:
        store.close()


def main():
    with tempfile.TemporaryDirectory() as scratch:
        baseline = Path(scratch) / 'baseline'
        extract(baseline)
        sys.path.insert(0, str(baseline))
        clock = Clock()
        time.time = clock
        import bridge
        import memory
        from koinon import memory_service_config

        if OUTPUT.exists():
            shutil.rmtree(OUTPUT)
        state_root = OUTPUT / 'state'
        prefix = OUTPUT / 'prefix'
        state_root.mkdir(parents=True, mode=0o700)
        prefix.mkdir(mode=0o700)
        keys = {name: key_of(path) for name, path in REPOS.items()}
        stores = {}
        stores['a'] = make_memory(memory, state_root, keys['a'], clock, full=True)
        stores['c'] = make_memory(memory, state_root, keys['c'], clock, full=False)
        stores['b'] = make_memory(memory, state_root, keys['b'], clock, full=False)

        claude = 'uds:/tmp/cc-socks/fixture-claude.sock'
        sessions = {}
        # A Codex session of repository A: handled and unhandled messages, a control frame,
        # a memory pointer, and a notifier checkpoint.
        name_a = 'codex-repo-a-3f'
        home = state_root / 'sessions' / hashlib.sha256(THREADS['codex'].encode()).hexdigest()
        home.mkdir(parents=True, mode=0o700)
        (home / 'session.json').write_text(json.dumps(dict(
            thread=THREADS['codex'], name=name_a, repo='/koinon-fixture/repo-a', agent='codex', model=None)))
        make_inbox(bridge, home, clock, [
            frame('First message, acknowledged.', claude, 'claude-koinon', 'm-1'),
            frame('Second message, acknowledged.', claude, 'claude-koinon', 'm-2'),
            frame('Third message, notified but not acknowledged.', claude),
            frame('Fourth message, waiting.', 'uds:/tmp/cc-socks/fixture-codex.sock', 'codex-repo-c-91', 'm-4'),
        ], ack_through=2, controls=1, handled=(1,))
        (home / 'notify-cursor.json').write_text(json.dumps(dict(thread=THREADS['codex'], through=3)))
        sessions['codex'] = dict(name=name_a, directory=home.name, messages=4, ack_through=2)
        # A DeepSeek session without a repository whose inbox binds repository C.
        name_b = 'deepseek-scratch-b2'
        home = state_root / 'sessions' / hashlib.sha256(THREADS['deepseek'].encode()).hexdigest()
        home.mkdir(parents=True, mode=0o700)
        (home / 'session.json').write_text(json.dumps(dict(
            thread=THREADS['deepseek'], name=name_b, repo=None, agent='deepseek', model='v4')))
        binding = dict(binding=hashlib.sha256(b'fixture-binding-c').hexdigest(), repo_path=REPOS['c'],
                       repo_key=keys['c'], memory_state_dir=PLACEHOLDER_STATE + '/memory/' + keys['c'],
                       store_id=stores['c']['store_id'])
        make_inbox(bridge, home, clock, [
            frame('A DeepSeek message.', claude, 'claude-koinon', 'd-1'),
            frame('Another DeepSeek message.', claude, 'claude-koinon', 'd-2'),
        ], binding=binding)
        sessions['deepseek'] = dict(name=name_b, directory=home.name, messages=2, ack_through=0)
        # A legacy single-thread inbox in the state root, owned by its notifier checkpoint.
        make_inbox(bridge, state_root, clock, [frame('A legacy message.', claude, 'claude-koinon', 'l-1')])
        (state_root / 'notify-cursor.json').write_text(json.dumps(dict(thread=THREADS['legacy'], through=0)))
        sessions['legacy'] = dict(messages=1, ack_through=0)

        record = dict(common_directory=REPOS['a'], identity_digest=hashlib.sha256(REPOS['a'].encode()).hexdigest(),
                      state_root=PLACEHOLDER_STATE, service_directory=PLACEHOLDER_STATE + '/memory/' + keys['a'],
                      backend='manual', artifact=None, state='installed', artifact_digest=None)
        services = dict(version=memory_service_config.VERSION, repositories={keys['a']: record})
        memory_service_config.validate(services)
        (prefix / 'install.json').write_text(json.dumps(dict(
            state_root=PLACEHOLDER_STATE, unit_dir='/koinon-fixture/units', memory_services=services),
            sort_keys=True) + '\n')
        manifest = dict(baseline=BASELINE, base=BASE, generated_until=clock(), placeholder_state=PLACEHOLDER_STATE,
                        repositories={name: dict(path=path, key=keys[name]) for name, path in REPOS.items()},
                        threads=THREADS, sessions=sessions, stores=stores)
        (OUTPUT / 'fixture.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
        for path in OUTPUT.rglob('*'):
            path.chmod(0o700 if path.is_dir() else 0o600)
    print(OUTPUT)


if __name__ == '__main__':
    main()
