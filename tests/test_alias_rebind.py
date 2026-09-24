"""Rebind and retirement of a replaced Codex thread (stable-alias chunk 03), synthetic peers only."""
from contextlib import redirect_stdout
import hashlib
import io
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import session
from koinon import alias_lease, durable_state, platform_support

SOCKET = '/tmp/synthetic-tmux/default'


def dead_operation():
    process = subprocess.Popen([sys.executable, '-c', 'pass'])
    process.wait()
    return dict(pid=process.pid, proc_start='gone')


def tree_digest(root, skip=()):
    digest = hashlib.sha256()
    for path in sorted(Path(root).rglob('*')):
        if path.name in skip:
            continue
        info = path.lstat()
        digest.update(str(path.relative_to(root)).encode() + str(stat.S_IMODE(info.st_mode)).encode())
        digest.update(path.read_bytes() if path.is_file() else b'directory')
    return digest.hexdigest()


class World:
    """Running synthetic services; a notifier chooses its registry name only at start."""

    def __init__(self, fixture):
        self.fixture, self.running, self.events, self.hooks = fixture, {}, [], {}

    def start(self, key):
        registration = self.fixture.registration(key)
        with alias_lease.publication(self.fixture.state(key), registration['name'], registration['repo']) as (name, _):
            self.running[key] = name
        self.events.append(('start', key))

    def stop(self, key):
        self.running.pop(key, None)
        self.events.append(('stop', key))
        self.hook('after_stop')

    def hook(self, point):
        if point in self.hooks:
            self.hooks[point]()

    def peers(self):
        return [dict(name=published, thread_name=self.fixture.registration(key)['name'],
                     address='uds:/synthetic/' + key) for key, published in self.running.items()]

    def lifecycle(self, state, _active):
        return 'running' if Path(state).name in self.running else 'stopped'

    def command(self, prefix, action, thread):
        key = session.identity(thread)
        if action == 'stop':
            self.stop(key)
            return 0, dict(status='stopped')
        registration = self.fixture.registration(key)
        prepared = session.alias_prepare(prefix, self.fixture.state(key), registration['repo'], registration['name'])
        self.hook('before_restart')
        if prepared.get('restart') and key in self.running:
            self.stop(key)
        if key not in self.running:
            self.start(key)
        self.hook('before_confirm')
        alias_lease.confirm(self.fixture.state_root, key, registration['name'], peers=self.peers)
        return 0, dict(status='running', alias=session.alias_report(self.fixture.state(key), registration['repo'],
                                                                   registration['name']))


class RebindFixture(unittest.TestCase):
    def setUp(self):
        previous = os.umask(0o077)
        self.addCleanup(os.umask, previous)
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.state_root = self.root / 'state'
        self.repo = self.root / 'koinon'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True, timeout=30)
        self.registry = self.root / 'claude' / 'sessions'
        self.registry.mkdir(parents=True)
        self.config = dict(state_root=str(self.state_root), unit_dir=str(self.root / 'units'), codex=sys.executable)
        self.world = World(self)
        self.threads = {}
        for patcher in (mock.patch.object(session, 'peers', side_effect=self.world.peers),
                        mock.patch.object(session, 'bridge_status', return_value=None),
                        mock.patch.object(session.session_observation, 'lifecycle', side_effect=self.world.lifecycle),
                        mock.patch.object(session, '_session_command', side_effect=self.world.command),
                        mock.patch.object(session, 'record_attachment'),
                        mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude'))):
            patcher.start()
            self.addCleanup(patcher.stop)

    def state(self, key):
        return self.state_root / 'sessions' / key

    def registration(self, key):
        return durable_state.read(self.state(key) / 'session.json')

    def add(self, label, pane='%1', socket=SOCKET, agent='codex', repo=None, host=1234, inbox=0):
        thread = 'synthetic-' + label
        key = session.identity(thread)
        self.threads[label] = thread
        state = self.state(key)
        state.mkdir(parents=True)
        durable_state.publish(state / 'session.json', dict(thread=thread, name=f'codex-koinon-{label}',
                                                           repo=str(repo or self.repo), agent=agent))
        durable_state.publish(state / 'host.json', dict(state='observed', pid=host, proc_start='s', observed_at_ms=1))
        terminal = (dict(state='observed', socket=socket, pane_id=pane, session_id='$0', observed_at_ms=1)
                    if pane else dict(state='unavailable', reason='not_in_tmux', observed_at_ms=1))
        durable_state.publish(state / 'terminal.json', terminal)
        with sqlite3.connect(state / 'inbox.sqlite3') as db:
            db.execute('CREATE TABLE inbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, received REAL, pid INTEGER, frame TEXT)')
            for number in range(inbox):
                db.execute('INSERT INTO inbox (received, pid, frame) VALUES (?, ?, ?)', (number, 1, 'SYNTHETIC BODY'))
        return key

    def ensure(self, label):
        """A normal ensure: the take rule, then start."""
        return self.world.command(self.root, 'ensure', self.threads[label])

    def rebind(self, label, predecessor, user_authorized=False):
        output = io.StringIO()
        with redirect_stdout(output):
            code = session.rebind(self.root, self.config, self.threads[label], self.threads[predecessor],
                                  user_authorized)
        return code, json.loads(output.getvalue())

    def lease(self):
        return durable_state.read(self.state_root / 'aliases' / 'codex-koinon.json')

    def alias_records(self):
        return [key for key, name in self.world.running.items() if name == 'codex-koinon']


class RebindTests(RebindFixture):
    def test_same_pane_moves_the_alias_and_stops_the_predecessor(self):
        old, new = self.add('0a', inbox=3), self.add('0b')
        self.ensure('0a')
        self.ensure('0b')
        self.assertEqual(self.alias_records(), [old])
        code, result = self.rebind('0b', '0a')
        self.assertEqual(code, 0, result)
        self.assertEqual(result['predecessor'], dict(name='codex-koinon-0a', result='predecessor_stopped',
                                                    inbox_records=3))
        self.assertTrue(result['evidence']['same_pane'])
        self.assertNotIn('SYNTHETIC BODY', json.dumps(result))
        self.assertEqual((result['alias']['name'], result['alias']['held']), ('codex-koinon', True))
        self.assertEqual(self.alias_records(), [new])
        self.assertNotIn(old, self.world.running)
        self.assertEqual((self.lease()['holder'], self.lease()['state']), (new, 'held'))

    def test_a_resume_rebinds_back(self):
        old, new = self.add('0a'), self.add('0b')
        self.ensure('0a')
        self.ensure('0b')
        self.rebind('0b', '0a')
        self.ensure('0a')  # the user resumed the older thread; ensure registers it again
        code, result = self.rebind('0a', '0b')
        self.assertEqual(code, 0, result)
        self.assertEqual(self.alias_records(), [old])
        self.assertNotIn(new, self.world.running)

    def test_weaker_evidence_refuses_and_changes_nothing(self):
        cases = dict(other_pane=(dict(pane='%2'), 'terminal_mismatch'),
                     other_server=(dict(socket='/tmp/other/default'), 'terminal_mismatch'),
                     not_recorded=(dict(pane=None), 'terminal_not_recorded'),
                     host_only=(dict(pane='%2', host=1234), 'host_only'))
        for name, (fields, code) in cases.items():
            with self.subTest(case=name):
                self.setUp()
                old = self.add('0a', **fields)
                if name != 'host_only':
                    durable_state.publish(self.state(old) / 'host.json',
                                          dict(state='observed', pid=999, proc_start='s', observed_at_ms=1))
                new = self.add('0b')
                self.ensure('0a')
                self.ensure('0b')
                before = (dict(self.world.running), self.lease())
                returned, result = self.rebind('0b', '0a')
                self.assertEqual((returned, result['code']), (78, code))
                self.assertEqual((dict(self.world.running), self.lease()), before)

    def test_user_authorization_skips_only_the_terminal_match(self):
        old, new = self.add('0a', pane='%7'), self.add('0b')
        self.ensure('0a')
        self.ensure('0b')
        code, result = self.rebind('0b', '0a', user_authorized=True)
        self.assertEqual(code, 0, result)
        self.assertTrue(result['evidence']['user_authorized'])
        self.assertEqual(self.alias_records(), [new])

    def test_other_family_repository_or_itself_refuses(self):
        other = self.root / 'other'
        other.mkdir()
        self.add('0a', agent='deepseek')
        self.add('0c', repo=other)
        self.add('0b')
        self.ensure('0b')
        for predecessor, code in (('0a', 'predecessor_unknown'), ('0c', 'same_repository'), ('0b', 'same_thread')):
            with self.subTest(predecessor=predecessor):
                returned, result = self.rebind('0b', predecessor)
                self.assertEqual((returned, result['code']), (78, code))

    def test_predecessor_without_the_alias_and_no_live_holder(self):
        holder, old, new = self.add('0d', pane='%9'), self.add('0a'), self.add('0b')
        self.ensure('0d')
        self.ensure('0a')
        self.world.stop(holder)
        self.ensure('0b')
        code, result = self.rebind('0b', '0a')
        self.assertEqual(code, 0, result)
        self.assertEqual(result['predecessor']['result'], 'predecessor_stopped')
        self.assertEqual(self.alias_records(), [new])

    def test_predecessor_without_the_alias_and_a_live_holder_elsewhere(self):
        holder, old, new = self.add('0d', pane='%9'), self.add('0a'), self.add('0b')
        self.ensure('0d')
        self.ensure('0a')
        self.ensure('0b')
        code, result = self.rebind('0b', '0a')
        self.assertEqual(code, 0, result)
        self.assertNotIn(old, self.world.running)
        self.assertEqual(self.alias_records(), [holder])
        self.assertEqual(result['predecessor']['result'], 'predecessor_stopped')
        self.assertEqual(result['ensure']['alias']['holder'], 'codex-koinon-0d')

    def test_already_stopped_predecessor(self):
        old, new = self.add('0a'), self.add('0b')
        self.ensure('0a')
        self.ensure('0b')
        self.world.stop(old)
        code, result = self.rebind('0b', '0a')
        self.assertEqual((code, result['predecessor']['result']), (0, 'already_stopped'))
        self.assertEqual(self.alias_records(), [new])

    def test_the_predecessor_keeps_its_state(self):
        old, new = self.add('0a', inbox=2), self.add('0b')
        self.ensure('0a')
        self.ensure('0b')
        before = tree_digest(self.state(old))
        self.rebind('0b', '0a')
        self.assertEqual(tree_digest(self.state(old)), before)
        self.ensure('0a')
        with sqlite3.connect(self.state(old) / 'inbox.sqlite3') as db:
            self.assertEqual(db.execute('SELECT count(*) FROM inbox').fetchone()[0], 2)

    def test_third_thread_never_takes_during_a_live_rebind(self):
        old, new, third = self.add('0a'), self.add('0b'), self.add('0c', pane='%5')
        self.ensure('0a')
        self.ensure('0b')
        seen = []

        def third_tries():
            result = alias_lease.prepare(self.state_root, third, str(self.repo), 'codex-koinon-0c',
                                         peers=self.world.peers, stopped=lambda key: key not in self.world.running,
                                         registry=self.registry)
            seen.append(result.get('reason'))
            self.assertLessEqual(len(self.alias_records()), 1)
        for point in ('after_stop', 'before_restart', 'before_confirm'):
            self.world.hooks[point] = third_tries
        code, result = self.rebind('0b', '0a')
        self.assertEqual(code, 0, result)
        self.assertTrue(seen)
        self.assertTrue(all(reason in ('alias_busy', 'alias_held_by') for reason in seen), seen)
        self.assertEqual(self.lease()['holder'], new)


class InterruptedRebindTests(RebindFixture):
    """A rebind killed after each step: its operation is dead and the successor completes it."""

    def interrupted(self, point):
        old, new, third = self.add('0a'), self.add('0b'), self.add('0c', pane='%5')
        self.ensure('0a')
        self.ensure('0b')

        class Killed(Exception):
            pass

        def kill():
            raise Killed(point)
        dead = dead_operation()
        if point == 'after_begin':
            alias_lease.begin_move(self.state_root, old, new, str(self.repo), peers=self.world.peers)
        else:
            self.world.hooks[point] = kill
            with mock.patch.object(alias_lease, 'operation', return_value=dead), self.assertRaises(Killed):
                self.rebind('0b', '0a')
            self.world.hooks.clear()
        lease = self.lease()
        if lease.get('operation'):
            durable_state.publish(self.state_root / 'aliases' / 'codex-koinon.json', dict(lease, operation=dead))
        self.assertLessEqual(len(self.alias_records()), 1)
        return old, new, third

    def test_each_kill_point_is_completed_by_the_successor(self):
        for point in ('after_begin', 'after_stop', 'before_restart', 'before_confirm'):
            with self.subTest(point=point):
                self.setUp()
                old, new, _ = self.interrupted(point)
                code, result = self.rebind('0b', '0a')
                self.assertEqual(code, 0, result)
                self.assertEqual(self.alias_records(), [new])
                self.assertEqual((self.lease()['holder'], self.lease()['state']), (new, 'held'))
                self.assertNotIn(old, self.world.running)

    def test_the_successors_ensure_completes_after_the_stop(self):
        old, new, _ = self.interrupted('after_stop')
        self.ensure('0b')
        self.assertEqual(self.alias_records(), [new])
        self.assertEqual(self.lease()['state'], 'held')

    def test_a_stopped_third_thread_waits_for_a_running_successor(self):
        old, new, third = self.interrupted('after_stop')
        stopped = lambda key: key not in self.world.running
        result = alias_lease.prepare(self.state_root, third, str(self.repo), 'codex-koinon-0c',
                                     peers=self.world.peers, stopped=stopped, registry=self.registry)
        self.assertEqual(result['reason'], 'alias_busy')
        self.world.stop(new)
        result = alias_lease.prepare(self.state_root, third, str(self.repo), 'codex-koinon-0c',
                                     peers=self.world.peers, stopped=stopped, registry=self.registry)
        self.assertEqual(result['state'], 'publishing')
        self.assertEqual(self.lease()['holder'], third)

    def test_a_kill_before_the_stop_is_cancelled_by_the_live_predecessor(self):
        old, new, _ = self.interrupted('after_begin')
        self.assertEqual(self.lease()['state'], 'moving')
        self.ensure('0a')
        self.assertEqual((self.lease()['holder'], self.lease()['state']), (old, 'held'))
        self.assertEqual(self.alias_records(), [old])


class CommandLineTests(unittest.TestCase):
    def test_rebind_action_is_wired_and_refuses_an_unregistered_thread(self):
        previous = os.umask(0o077)
        self.addCleanup(os.umask, previous)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = dict(state_root=str(root / 'state'), unit_dir=str(root / 'units'), codex=sys.executable)
            output = io.StringIO()
            args = ['session.py', 'rebind', '--thread', 'synthetic-new', '--predecessor', 'synthetic-old']
            with mock.patch.object(session, 'read_config', return_value=config), \
                    mock.patch.object(sys, 'argv', args), redirect_stdout(output), \
                    self.assertRaises(SystemExit) as raised:
                session.main()
        self.assertEqual((raised.exception.code, json.loads(output.getvalue())['code']), (78, 'not_registered'))


if __name__ == '__main__':
    unittest.main()
