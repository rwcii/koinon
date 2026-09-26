"""Stable alias of a Codex participant (stable-alias chunk 02), with synthetic peers only."""
import argparse
from contextlib import redirect_stdout
import fcntl
import io
import json
import multiprocessing
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import types
import unittest
from unittest import mock

import bridge
import session
from koinon import alias_lease, durable_state, platform_support, runtime_names
from koinon.notification_runtime import Runtime
from scripts.install import FILES
from repo_root import ROOT


def git_repo(path):
    path.mkdir(parents=True)
    subprocess.run(['git', 'init', '-q', str(path)], check=True, timeout=30)
    return path


def dead_pid():
    process = subprocess.Popen([sys.executable, '-c', 'pass'])
    process.wait()
    return process.pid


def race(arguments):
    """One competing `ensure` take, in its own process, released by a shared barrier."""
    state_root, key, repo, name, barrier = arguments
    (Path(state_root) / 'registry').mkdir(parents=True, exist_ok=True)
    barrier.wait()
    result = alias_lease.prepare(Path(state_root), key, repo, name, peers=lambda: [],
                                 stopped=lambda other: False, registry=Path(state_root) / 'registry')
    return key, result


class Fixture(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.state_root = self.root / 'state'
        self.registry = self.root / 'registry'
        self.registry.mkdir()
        self.records = []

    def register(self, key, name, repo, agent='codex'):
        state = self.state_root / 'sessions' / key
        state.mkdir(parents=True, exist_ok=True)
        durable_state.publish(state / 'session.json', dict(thread='t-' + key, name=name, repo=str(repo), agent=agent))
        return state

    def prepare(self, key, repo, name, stopped=lambda other: True):
        return alias_lease.prepare(self.state_root, key, str(repo), name, peers=lambda: list(self.records),
                                   stopped=stopped, registry=self.registry)

    def live(self, name, published=None):
        """A live registry record, as bridge.peers() reports it."""
        self.records.append(dict(name=published or name, thread_name=name, address='uds:/synthetic/' + name))

    def lease(self, alias):
        return durable_state.read(self.state_root / 'aliases' / (alias + '.json'))

    def settle(self, alias):
        """As after a completed publication: `held`, no operation."""
        durable_state.publish(self.state_root / 'aliases' / (alias + '.json'),
                              dict(self.lease(alias), state='held', operation=None))


class ReservationTests(Fixture):
    def test_first_repository_gets_the_base_alias(self):
        repo = git_repo(self.root / 'a' / 'koinon')
        self.register('k1', 'codex-koinon-05', repo)
        result = self.prepare('k1', repo, 'codex-koinon-05')
        self.assertEqual(result['name'], 'codex-koinon')
        self.assertEqual(self.lease('codex-koinon')['holder'], 'k1')

    def test_same_label_in_two_repositories_gets_two_aliases(self):
        first, second = git_repo(self.root / 'a' / 'koinon'), git_repo(self.root / 'b' / 'koinon')
        self.register('k1', 'codex-koinon-05', first)
        self.register('k2', 'codex-koinon-06', second)
        one = self.prepare('k1', first, 'codex-koinon-05')['name']
        two = self.prepare('k2', second, 'codex-koinon-06')['name']
        digest = alias_lease.repository_digest(str(second))
        self.assertEqual((one, two), ('codex-koinon', 'codex-koinon-' + digest[:4]))
        self.assertEqual(self.lease(two)['holder'], 'k2')

    def test_saved_per_thread_name_forces_the_suffixed_form(self):
        foo, foo_ab = git_repo(self.root / 'foo'), git_repo(self.root / 'foo-ab')
        self.register('k1', 'codex-foo-ab', foo)
        self.register('k2', 'codex-foo-ab-3c', foo_ab)
        name = self.prepare('k2', foo_ab, 'codex-foo-ab-3c')['name']
        self.assertEqual(name, 'codex-foo-ab-' + alias_lease.repository_digest(str(foo_ab))[:4])

    def test_shared_digest_prefix_probes_a_longer_suffix(self):
        first, second = git_repo(self.root / 'a' / 'x'), git_repo(self.root / 'b' / 'x')
        digests = {str(first): 'abcdef' + '0' * 58, str(second): 'abcdef' + '1' * 58}
        (self.state_root / 'aliases').mkdir(parents=True)
        for alias, digest in (('codex-x', 'e' * 64), ('codex-x-abcd', 'f' * 64)):
            durable_state.publish(self.state_root / 'aliases' / (alias + '.json'),
                                  dict(alias=alias, repository=digest, holder=None, state='held',
                                       **{'from': None}, to=None, operation=None))
        with mock.patch.object(alias_lease, 'repository_digest', side_effect=lambda repo: digests[repo]):
            one = self.prepare('k1', first, 'codex-x-02')['name']
            two = self.prepare('k2', second, 'codex-x-03')['name']
        self.assertEqual((one, two), ('codex-x-abcdef', 'codex-x-abcdef11'))

    def test_reservation_is_permanent_and_new_names_avoid_it(self):
        repo = git_repo(self.root / 'koinon')
        self.register('k1', 'codex-koinon-05', repo)
        self.prepare('k1', repo, 'codex-koinon-05')
        self.assertTrue(alias_lease.reserved(self.state_root, 'codex-koinon'))
        with mock.patch.object(session, 'peers', return_value=[]):
            state = self.state_root / 'sessions' / 'k9'
            state.mkdir(parents=True)
            saved = session.save_registration(state, self.state_root, 'synthetic-thread', str(repo))
        self.assertNotEqual(saved['name'], 'codex-koinon')
        self.assertTrue(saved['name'].startswith('codex-koinon-'))

    def test_missing_registry_refuses_the_take(self):
        repo = git_repo(self.root / 'koinon')
        self.register('k1', 'codex-koinon-05', repo)
        self.registry.rmdir()
        result = self.prepare('k1', repo, 'codex-koinon-05')
        self.assertEqual((result['reason'], result['paths']), ('registry_missing', [str(self.registry)]))
        self.assertFalse((self.state_root / 'aliases').exists())

    def test_outside_a_repository_no_alias(self):
        plain = self.root / 'plain'
        plain.mkdir()
        with mock.patch.object(alias_lease.repository_identity, 'repo_common_directory', side_effect=ValueError):
            self.assertEqual(self.prepare('k1', plain, 'codex-plain-01')['reason'], 'not_a_repository')


class TakeTests(Fixture):
    def setUp(self):
        super().setUp()
        self.repo = git_repo(self.root / 'koinon')
        self.register('a', 'codex-koinon-0a', self.repo)
        self.register('b', 'codex-koinon-0b', self.repo)

    def test_live_holder_keeps_the_alias(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        self.settle('codex-koinon')
        self.live('codex-koinon-0a', 'codex-koinon')
        result = self.prepare('b', self.repo, 'codex-koinon-0b', stopped=lambda other: False)
        self.assertEqual((result['reason'], result['holder']), ('alias_held_by', 'codex-koinon-0a'))
        self.assertEqual(self.lease('codex-koinon')['holder'], 'a')

    def test_running_holder_without_alias_keeps_it(self):
        # A pre-upgrade holder runs with its per-thread name: it is live, so no take.
        self.prepare('a', self.repo, 'codex-koinon-0a')
        self.settle('codex-koinon')
        self.live('codex-koinon-0a')
        self.assertEqual(self.prepare('b', self.repo, 'codex-koinon-0b')['reason'], 'alias_held_by')

    def test_stopped_holder_without_record_loses_it(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        lease = self.lease('codex-koinon')
        durable_state.publish(self.state_root / 'aliases' / 'codex-koinon.json',
                              dict(lease, state='held', operation=None))
        result = self.prepare('b', self.repo, 'codex-koinon-0b')
        self.assertEqual(result['state'], 'publishing')
        self.assertEqual(self.lease('codex-koinon')['holder'], 'b')

    def test_live_transient_operation_refuses(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')  # publishing, owned by this live process
        self.assertEqual(self.prepare('b', self.repo, 'codex-koinon-0b')['reason'], 'alias_busy')

    def test_dead_transient_is_reserved_for_its_running_successor(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        lease = self.lease('codex-koinon')
        dead = dict(pid=dead_pid(), proc_start='gone')
        durable_state.publish(self.state_root / 'aliases' / 'codex-koinon.json',
                              dict(lease, state='moving', holder='c', to='a', operation=dead, **{'from': 'c'}))
        running = lambda other: other != 'a'  # the successor a runs
        self.assertEqual(self.prepare('b', self.repo, 'codex-koinon-0b', stopped=running)['reason'], 'alias_busy')
        self.assertEqual(self.lease('codex-koinon')['to'], 'a')
        # Once the successor is stopped and has no record, a third key may take it.
        result = self.prepare('b', self.repo, 'codex-koinon-0b', stopped=lambda other: True)
        self.assertEqual(result['state'], 'publishing')
        self.assertEqual(self.lease('codex-koinon')['holder'], 'b')

    def test_holder_restarts_a_running_service_to_publish(self):
        self.live('codex-koinon-0a')
        result = self.prepare('a', self.repo, 'codex-koinon-0a')
        self.assertTrue(result['restart'])
        self.assertFalse(self.prepare('b', self.repo, 'codex-koinon-0b', stopped=lambda other: False)['restart'])

    def test_confirm_marks_held_only_when_the_alias_is_live(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        peers = lambda: list(self.records)
        self.assertFalse(alias_lease.confirm(self.state_root, 'a', 'codex-koinon-0a', peers=peers))
        self.live('codex-koinon-0a', 'codex-koinon')
        self.assertTrue(alias_lease.confirm(self.state_root, 'a', 'codex-koinon-0a', peers=peers))
        self.assertEqual(self.lease('codex-koinon')['state'], 'held')
        report = alias_lease.report(self.state_root, 'a', str(self.repo), 'codex-koinon-0a')
        self.assertEqual(report, dict(state='observed', name='codex-koinon', held=True, lease='held'))
        other = alias_lease.report(self.state_root, 'b', str(self.repo), 'codex-koinon-0b')
        self.assertEqual((other['held'], other['holder']), (False, 'codex-koinon-0a'))

    def write_record(self, pid, **fields):
        record = dict(name='codex-koinon', entrypoint=runtime_names.REGISTRY_ENTRYPOINT, procStart='gone')
        record.update(fields)
        path = self.registry / f'{pid}.json'
        path.write_text(json.dumps(record))
        return path

    def test_crashed_holder_record_is_removed_before_the_take(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        lease = self.lease('codex-koinon')
        durable_state.publish(self.state_root / 'aliases' / 'codex-koinon.json', dict(lease, operation=None, state='held'))
        durable_state.publish(self.state_root / 'sessions' / 'a' / 'notify-ready.json', dict(owner='gen-a'))
        stale = self.write_record(dead_pid(), bridgeOwner='gen-a')
        self.prepare('b', self.repo, 'codex-koinon-0b')
        self.assertFalse(stale.exists())
        self.assertEqual(self.lease('codex-koinon')['holder'], 'b')

    def test_foreign_or_live_record_refuses_the_take(self):
        self.prepare('a', self.repo, 'codex-koinon-0a')
        lease = self.lease('codex-koinon')
        durable_state.publish(self.state_root / 'aliases' / 'codex-koinon.json', dict(lease, operation=None, state='held'))
        durable_state.publish(self.state_root / 'sessions' / 'a' / 'notify-ready.json', dict(owner='gen-a'))
        for fields in (dict(bridgeOwner='gen-a', entrypoint='claude-code'), dict(bridgeOwner='another-generation')):
            with self.subTest(fields=fields):
                foreign = self.write_record(dead_pid(), **fields)
                result = self.prepare('b', self.repo, 'codex-koinon-0b')
                self.assertEqual(result['reason'], 'alias_occupied')
                self.assertTrue(foreign.exists())
                self.assertEqual(self.lease('codex-koinon')['holder'], 'a')
                foreign.unlink()
        live = self.write_record(os.getpid(), bridgeOwner='gen-a',
                                 procStart=platform_support.proc_start(os.getpid()))
        self.assertEqual(self.prepare('b', self.repo, 'codex-koinon-0b')['reason'], 'alias_occupied')
        self.assertTrue(live.exists())

    def test_concurrent_takes_leave_one_holder(self):
        context = multiprocessing.get_context('spawn')
        with context.Manager() as manager:
            barrier = manager.Barrier(2)
            with context.Pool(2) as pool:
                results = dict(pool.map(race, [(str(self.state_root), key, str(self.repo), f'codex-koinon-0{key}', barrier)
                                               for key in ('a', 'b')]))
        holder = self.lease('codex-koinon')['holder']
        loser = 'b' if holder == 'a' else 'a'
        self.assertIn(holder, ('a', 'b'))
        self.assertEqual(results[holder]['state'], 'publishing')
        self.assertIn(results[loser].get('reason'), ('alias_busy', 'alias_held_by'))
        self.assertEqual(len(list((self.state_root / 'aliases').glob('*.json'))), 1)


class PublicationTests(Fixture):
    def create(self, key, name, repo, agent='codex'):
        state = self.state_root / 'sessions' / key
        record = self.registry / f'{key}.json'
        runtime = types.SimpleNamespace(root=state, options=argparse.Namespace(name=name, repo=str(repo), agent=agent))
        Runtime.create_record(runtime, record, dict(name=name, koinonName=name))
        return json.loads(record.read_text())

    def test_holder_publishes_the_alias_and_others_their_name(self):
        repo = git_repo(self.root / 'koinon')
        self.register('a', 'codex-koinon-0a', repo)
        self.register('b', 'codex-koinon-0b', repo)
        self.prepare('a', repo, 'codex-koinon-0a')
        holder = self.create('a', 'codex-koinon-0a', repo)
        other = self.create('b', 'codex-koinon-0b', repo)
        self.assertEqual((holder['name'], holder['koinonName'], holder['koinonAlias']),
                         ('codex-koinon', 'codex-koinon-0a', 'codex-koinon'))
        self.assertEqual((other['name'], other['koinonAlias']), ('codex-koinon-0b', 'codex-koinon'))

    def test_deepseek_and_legacy_layouts_publish_their_name(self):
        repo = git_repo(self.root / 'koinon')
        self.register('d', 'deepseek-koinon-0d', repo, agent='deepseek')
        record = self.create('d', 'deepseek-koinon-0d', repo, agent='deepseek')
        self.assertNotIn('koinonAlias', record)
        legacy = types.SimpleNamespace(root=self.root / 'legacy-state',
                                       options=argparse.Namespace(name='codex-peer', repo=str(repo), agent='codex'))
        Runtime.create_record(legacy, self.registry / 'legacy.json', dict(name='codex-peer'))
        self.assertEqual(json.loads((self.registry / 'legacy.json').read_text())['name'], 'codex-peer')

    def test_record_creation_waits_for_names_lock(self):
        repo = git_repo(self.root / 'koinon')
        self.register('a', 'codex-koinon-0a', repo)
        with alias_lease.publication(self.state_root / 'sessions' / 'a', 'codex-koinon-0a', str(repo)):
            with (self.state_root / 'names.lock').open('a') as lock:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)


class SendResolutionTests(Fixture):
    def test_name_resolution(self):
        records = [dict(name='codex-koinon', address='uds:/synthetic/one'),
                   dict(name='twin', address='uds:/synthetic/two'), dict(name='twin', address='uds:/synthetic/three')]
        repo = git_repo(self.root / 'koinon')
        self.register('a', 'codex-koinon-0a', repo)
        self.prepare('a', repo, 'codex-koinon-0a')
        root = self.state_root / 'sessions' / 'a'
        with mock.patch.object(bridge, 'scan_peers', return_value=(records, 0)):
            self.assertEqual(bridge.resolve_name(root, 'codex-koinon'), dict(address='uds:/synthetic/one'))
            self.assertEqual(bridge.resolve_name(root, 'twin')['code'], 'peer_ambiguous')
            self.assertEqual(bridge.resolve_name(root, 'nobody')['code'], 'peer_not_found')
        with mock.patch.object(bridge, 'scan_peers', return_value=([], 0)):
            unheld = bridge.resolve_name(root, 'codex-koinon')
        self.assertEqual((unheld['code'], unheld['name']), ('alias_unheld', 'codex-koinon'))
        # An unjudged record, such as one left by a container that shares this machine,
        # reports the sandbox only when it hides every peer.
        with mock.patch.object(bridge, 'scan_peers', return_value=(records, 1)):
            self.assertEqual(bridge.resolve_name(root, 'nobody')['code'], 'peer_not_found')
        with mock.patch.object(bridge, 'scan_peers', return_value=([], 1)):
            self.assertEqual(bridge.resolve_name(root, 'nobody')['code'], 'sandboxed')

    def test_send_cli_refuses_an_unheld_alias_before_any_control_request(self):
        repo = git_repo(self.root / 'koinon')
        self.register('a', 'codex-koinon-0a', repo)
        self.prepare('a', repo, 'codex-koinon-0a')
        root = self.state_root / 'sessions' / 'a'
        output = io.StringIO()
        with mock.patch.object(sys, 'argv', ['bridge.py', '--state-dir', str(root), 'send', 'codex-koinon', 'hello']), \
                mock.patch.object(bridge, 'scan_peers', return_value=([], 0)), \
                mock.patch.object(bridge, 'client', side_effect=AssertionError('no control request')), \
                redirect_stdout(output), self.assertRaises(SystemExit) as raised:
            bridge.cli_main()
        self.assertEqual(raised.exception.code, 1)
        self.assertEqual(json.loads(output.getvalue())['code'], 'alias_unheld')


class PeersFieldsTests(Fixture):
    def test_peers_reports_alias_holder_and_thread_name(self):
        pid = os.getpid()
        entry = dict(name='codex-koinon', koinonName='codex-koinon-0a', koinonAlias='codex-koinon',
                     messagingSocketPath='/tmp/synthetic.sock', procStart=platform_support.proc_start(pid),
                     cwd='/synthetic')
        folder = self.root / 'claude' / 'sessions'
        folder.mkdir(parents=True)
        (folder / f'{pid}.json').write_text(json.dumps(entry))
        with mock.patch.dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude')), \
                mock.patch.object(bridge, 'target_path'):
            found = [record for record in bridge.peers() if record['pid'] == pid]
        self.assertEqual({key: found[0][key] for key in ('name', 'thread_name', 'alias', 'alias_holder')},
                         dict(name='codex-koinon', thread_name='codex-koinon-0a', alias='codex-koinon',
                              alias_holder=True))


class EnsureAliasTests(unittest.TestCase):
    """`session.py ensure` takes the alias and never holds names.lock while it starts a service."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix = self.root / 'prefix'
        self.prefix.mkdir(mode=0o700)
        for name in FILES:
            if name.endswith('.py'):
                target = self.prefix / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
                target.chmod(0o600)
        self.repo = git_repo(self.root / 'koinon')
        (self.root / 'claude' / 'sessions').mkdir(parents=True)
        self.env = dict(os.environ, CODEX_HOME=str(self.root / 'codex'), CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        for key in ('CODEX_THREAD_ID', 'DSH_SESSION_ID', 'TMUX', 'TMUX_PANE'):
            self.env.pop(key, None)
        self.config = dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'),
                           codex=sys.executable, dsh_url='http://127.0.0.1:9999',
                           dsh_credentials=str(self.root / 'deepseek' / 'credentials.json'))
        durable_state.publish(self.prefix / 'install.json', self.config)

    def invoke(self, agent, thread, manager):
        output = io.StringIO()
        args = ['session.py', 'ensure', '--agent', agent, '--thread', thread, '--repo', str(self.repo)]
        with mock.patch.object(session, '__file__', str(self.prefix / 'session.py')), \
                mock.patch.object(sys, 'argv', args), mock.patch.dict(os.environ, self.env, clear=True), \
                mock.patch.object(session, 'peers', return_value=[]), \
                mock.patch.object(platform_support, 'codex_host', return_value=False), \
                mock.patch.object(platform_support, 'memory_manager_available', return_value=False), \
                mock.patch.object(platform_support, 'session_manager_observation', return_value={'status': 'unknown'}), \
                mock.patch.object(platform_support, 'user_service_manager', side_effect=manager), \
                redirect_stdout(output):
            try:
                session.main()
            except (SystemExit, RuntimeError):
                pass
        return json.loads(output.getvalue()) if output.getvalue().strip() else None

    def test_codex_ensure_takes_the_alias_and_starts_without_names_lock(self):
        seen = []

        def manager(operation, *args, **kwargs):
            if operation == 'start':
                with (self.root / 'state' / 'names.lock').open('a') as lock:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)  # raises if ensure holds it
                seen.append('started')
            return subprocess.CompletedProcess([], 0)
        self.invoke('codex', 'synthetic-alias', manager)
        self.assertEqual(seen, ['started'])
        lease = durable_state.read(self.root / 'state' / 'aliases' / 'codex-koinon.json')
        self.assertEqual(lease['state'], 'publishing')
        state, _, _ = session.details(self.prefix, self.config, 'synthetic-alias', str(self.repo))
        self.assertEqual(lease['holder'], state.name)

    def test_manual_codex_ensure_reports_the_alias_and_deepseek_gets_none(self):
        unavailable = lambda *args, **kwargs: subprocess.CompletedProcess([], 1)
        reported = self.invoke('codex', 'synthetic-manual', unavailable)
        self.assertEqual(reported['alias']['name'], 'codex-koinon')
        self.assertEqual(reported['alias']['lease'], 'publishing')
        deepseek = self.invoke('deepseek', 'synthetic-deepseek', unavailable)
        self.assertNotIn('alias', deepseek)
        self.assertEqual(len(list((self.root / 'state' / 'aliases').glob('*.json'))), 1)

    def test_running_holder_restarts_to_publish(self):
        unavailable = lambda *args, **kwargs: subprocess.CompletedProcess([], 1)
        self.invoke('codex', 'synthetic-running', unavailable)
        state, name, _ = session.details(self.prefix, self.config, 'synthetic-running', str(self.repo))
        name = durable_state.read(state / 'session.json')['name']
        running = [dict(name=name, thread_name=name, address='uds:/synthetic')]
        calls = []
        with mock.patch.object(session, 'stop_legacy', side_effect=lambda *a: calls.append('stop')), \
                mock.patch.object(session.session_observation, 'lifecycle', side_effect=['running', 'stopped']), \
                mock.patch.object(session, 'notifier_readiness', return_value=None):
            output = io.StringIO()
            args = ['session.py', 'ensure', '--thread', 'synthetic-running', '--repo', str(self.repo)]
            with mock.patch.object(session, '__file__', str(self.prefix / 'session.py')), \
                    mock.patch.object(sys, 'argv', args), mock.patch.dict(os.environ, self.env, clear=True), \
                    mock.patch.object(session, 'peers', return_value=running), \
                    mock.patch.object(platform_support, 'codex_host', return_value=False), \
                    mock.patch.object(platform_support, 'session_manager_observation', return_value={'status': 'unknown'}), \
                    mock.patch.object(platform_support, 'user_service_manager', side_effect=unavailable), \
                    redirect_stdout(output):
                session.main()
        self.assertEqual(calls, ['stop'])

    def test_native_holder_restart_stops_before_the_delegated_ensure(self):
        self.config['session_backend'] = 'systemd'
        durable_state.publish(self.prefix / 'install.json', self.config)
        unavailable = lambda *args, **kwargs: subprocess.CompletedProcess([], 1)
        self.invoke('codex', 'synthetic-native', unavailable)
        import session_service
        calls = []

        def delegated(argv):
            calls.append(argv[0])
            return 0
        with mock.patch.object(session, 'alias_prepare', return_value=dict(restart=True)), \
                mock.patch.object(session_service, 'main', side_effect=delegated):
            self.invoke('codex', 'synthetic-native', unavailable)
        self.assertEqual(calls, ['stop', 'ensure'])
        # A failed stop refuses; the old service is never reported as the publication.
        calls.clear()

        def failing(argv):
            calls.append(argv[0])
            if argv[0] == 'stop':
                print(json.dumps(dict(status='unavailable', code='session_ownership_unknown')))
                return 78
            return 0
        with mock.patch.object(session, 'alias_prepare', return_value=dict(restart=True)), \
                mock.patch.object(session_service, 'main', side_effect=failing):
            output = io.StringIO()
            args = ['session.py', 'ensure', '--agent', 'codex', '--thread', 'synthetic-native', '--repo', str(self.repo)]
            with mock.patch.object(session, '__file__', str(self.prefix / 'session.py')), \
                    mock.patch.object(sys, 'argv', args), mock.patch.dict(os.environ, self.env, clear=True), \
                    mock.patch.object(session, 'peers', return_value=[]), \
                    mock.patch.object(platform_support, 'codex_host', return_value=False), \
                    mock.patch.object(platform_support, 'session_manager_observation', return_value={'status': 'unknown'}), \
                    mock.patch.object(platform_support, 'user_service_manager', side_effect=unavailable), \
                    redirect_stdout(output), self.assertRaises(SystemExit) as raised:
                session.main()
        self.assertEqual((calls, raised.exception.code), (['stop'], 78))
        refused = json.loads(output.getvalue())
        self.assertEqual((refused['code'], refused['stop']['code']), ('alias_restart_failed', 'session_ownership_unknown'))

    def test_public_ensure_reports_a_missing_registry(self):
        unavailable = lambda *args, **kwargs: subprocess.CompletedProcess([], 1)
        registry = self.root / 'claude' / 'sessions'
        registry.rmdir()
        for backend in (None, 'systemd'):
            with self.subTest(backend=backend):
                if backend:
                    self.config['session_backend'] = backend
                    durable_state.publish(self.prefix / 'install.json', self.config)
                reported = self.invoke('codex', f'synthetic-missing-{backend}', unavailable)
                self.assertEqual(reported['alias']['take'], dict(reason='registry_missing', paths=[str(registry)]))
                self.assertEqual(reported['alias']['reason'], 'not_reserved')

    def test_guide_reports_the_alias_for_codex(self):
        unavailable = lambda *args, **kwargs: subprocess.CompletedProcess([], 1)
        self.invoke('codex', 'synthetic-guide', unavailable)
        with mock.patch.dict(os.environ, self.env, clear=True), mock.patch.object(session, 'peers', return_value=[]):
            found = session.guide_observations(self.prefix, 'codex', 'synthetic-guide', str(self.repo))
        self.assertEqual((found['alias']['state'], found['alias']['name']), ('observed', 'codex-koinon'))


if __name__ == '__main__':
    unittest.main()
