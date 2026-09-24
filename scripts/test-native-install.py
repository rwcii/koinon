#!/usr/bin/env python3
"""Exercise public installation and removal in explicitly requested synthetic fixtures."""
import argparse
import ast
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
from koinon import platform_support
from scripts.install import FILES


MANAGER_QUERIES = []


def memory_observation(record):
    name = Path(record['artifact']).name
    run = subprocess.run
    def observed(argv, *args, **kwargs):
        try:
            result = run(argv, *args, **kwargs)
        except subprocess.CalledProcessError as exc:
            if len(MANAGER_QUERIES) < 64:
                MANAGER_QUERIES.append(dict(argv=list(map(str, argv)), returncode=exc.returncode,
                                           stdout=exc.stdout, stderr=exc.stderr))
            raise
        if len(MANAGER_QUERIES) < 64:
            MANAGER_QUERIES.append(dict(argv=list(map(str, argv)), returncode=result.returncode,
                                       stdout=result.stdout, stderr=result.stderr))
        return result
    with patch.object(platform_support.subprocess, 'run', side_effect=observed):
        if record['backend'] == 'systemd':
            return platform_support.systemd_service_observation(name)
        return platform_support.launchd_service_observation(record['manager_domain'], name[:-6])


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def released_manifest(release):
    """Read shipped data without importing the old runtime into this process."""
    tree = ast.parse((release / 'scripts/install.py').read_text())
    assignments = [node for node in tree.body if isinstance(node, ast.Assign)
                   and any(isinstance(target, ast.Name) and target.id == 'FILES'
                           for target in node.targets)]
    if len(assignments) != 1:
        raise ValueError(f'{release}: expected one literal FILES manifest')
    try:
        manifest = ast.literal_eval(assignments[0].value)
    except (ValueError, TypeError) as exc:
        raise ValueError(f'{release}: FILES manifest must be literal') from exc
    if not isinstance(manifest, (tuple, list)) or not all(isinstance(name, str) for name in manifest):
        raise ValueError(f'{release}: FILES manifest must contain path strings')
    return manifest


class Fixture:
    def __init__(self, backend, session, release=None):
        # `release` builds the installable tree from a different release than the
        # one running this fixture, using that release's own manifest and layout.
        self.release = Path(release) if release is not None else SOURCE
        self.backend, self.session = backend, session
        self.root = Path(tempfile.mkdtemp(prefix='koinon-install-fixture-')).resolve()
        self.source = self.root / 'source'
        self.prefix = self.root / 'app space $literal %n'
        self.repo, self.state = self.root / 'repo', self.root / 'state'
        self.thread = 'synthetic-install-' + uuid.uuid4().hex
        self.env = dict(os.environ, CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        # A user status line that installation must wrap and uninstall must restore.
        (self.root / 'claude').mkdir(mode=0o700)
        self.status_line = dict(type='command', command='echo synthetic-status-line', padding=0)
        (self.root / 'claude' / 'settings.json').write_text(json.dumps(dict(statusLine=self.status_line)))
        self.records, self.session_records = [], []
        self.removed = False
        manifest = FILES if self.release == SOURCE else released_manifest(self.release)
        for name in manifest:
            target = self.source / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(self.release / name, target)
            target.chmod(0o600)
        self.apply_overrides(self.source)
        self.command(['git', 'init', '-q', str(self.repo)])
        self.command(['git', '-C', str(self.repo), '-c', 'user.name=Synthetic Fixture',
                      '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false',
                      '-c', 'core.hooksPath=/dev/null', 'commit', '--allow-empty', '-qm', 'fixture'])

    def apply_overrides(self, tree):
        """Isolate synthetic participant locks and peer discovery in a release tree.

        The installed release and the release upgraded to both need every one of
        these. Omitting one moves that state to the host default after replacement,
        which is a real leak rather than a fixture detail. The layout differs
        between releases, so the module is located rather than assumed.
        """
        tree = Path(tree)
        platform = 'koinon/platform_support.py' if (tree / 'koinon').is_dir() else 'platform_support.py'
        with (tree / platform).open('a') as stream:
            stream.write('\nos.environ["CLAUDE_CONFIG_DIR"] = ' + repr(self.env['CLAUDE_CONFIG_DIR']) + '\n')
            stream.write('\ndef participant_lock_dir():\n    return Path(' + repr(str(self.root / 'locks')) + ')\n')

    def command(self, argv):
        result = subprocess.run(list(map(str, argv)), env=self.env, capture_output=True, text=True, timeout=90)
        require(result.returncode == 0, 'public command failed: ' + repr(list(map(str, argv)))
                + '\n' + result.stdout + result.stderr)
        return result.stdout

    def install(self, *, staged=False, repo=None):
        argv = [sys.executable, self.source / 'scripts/install.py', '--configure-memory',
                '--repo', repo or self.repo, '--prefix', self.prefix, '--state-dir', self.state,
                '--unit-dir', self.root / 'legacy-units', '--service-backend', self.backend]
        if self.session:
            argv += ['--configure-codex', '--codex-home', self.root / 'codex',
                     '--thread', self.thread, '--codex', sys.executable]
        if staged:
            argv += ['--no-start']
        self.command(argv)
        config = json.loads((self.prefix / 'install.json').read_text())
        if self.release == SOURCE:
            line = self.settings()['statusLine']
            if staged:
                require(line == self.status_line and 'hooks' not in self.settings()
                        and not (self.root / 'claude' / 'CLAUDE.md').exists(),
                        'a staged installation changed Claude settings')
            else:
                require(line.get('padding') == 0 and str(self.prefix / 'statusline.py') in line.get('command', '')
                        and 'synthetic-status-line' in line['command'],
                        'installation did not wrap the Claude status line: ' + json.dumps(line))
                self.check_claude_guidance()
        self.records = list(config['memory_services']['repositories'].values())
        self.session_records = [json.loads(path.read_text())
                                for path in (self.state / 'sessions').glob('*/native-service.json')]
        self.removed = False
        return config

    def check_claude_guidance(self):
        """The CLAUDE.md block and the one SessionStart hook, run as Claude Code runs it."""
        groups = self.settings().get('hooks', {}).get('SessionStart', [])
        hooks = [hook for group in groups for hook in group.get('hooks', [])
                 if str(self.prefix / 'session.py') in hook.get('command', '')]
        require(len(hooks) == 1 and hooks[0].get('timeout') == 10,
                'installation did not add one Claude SessionStart hook: ' + json.dumps(groups))
        require('<!-- BEGIN KOINON CLAUDE -->' in (self.root / 'claude' / 'CLAUDE.md').read_text(),
                'installation did not add the Claude guidance block')
        result = subprocess.run(['/bin/sh', '-c', hooks[0]['command']], input='{"session_id": "synthetic"}',
                                env=self.env, capture_output=True, text=True, timeout=10)
        require(result.returncode == 0 and result.stdout.startswith('Koinon guidance for claude'),
                'the Claude SessionStart hook did not print the guide: ' + result.stdout + result.stderr)

    def settings(self):
        return json.loads((self.root / 'claude' / 'settings.json').read_text())

    def status(self):
        memory = json.loads(self.command([sys.executable, self.prefix / 'memory_service.py',
                                          'status', '--prefix', self.prefix, '--repo', self.repo]))
        require(memory.get('running') is True and memory.get('managed') is True,
                'public memory status did not prove native readiness: ' + json.dumps(memory))
        if self.session:
            session = json.loads(self.command([sys.executable, self.prefix / 'session.py',
                                               'status', '--thread', self.thread, '--repo', self.repo]))
            require(session.get('status') == 'running' and session.get('managed') is True,
                    'public session status did not prove native readiness: ' + json.dumps(session))
        return memory

    def note(self, *args):
        return self.command([sys.executable, self.prefix / 'memory.py', '--state-dir', self.state,
                             '--repo-path', self.repo, '--consumer', 'synthetic-install-fixture', *args])

    def verify_registration(self):
        for record in self.records:
            if self.backend == 'systemd':
                paths = platform_support.memory_registration_paths(record)
                for path in paths:
                    require(path.is_symlink() and os.readlink(path) == record['artifact'],
                            'persistent memory registration missing or mismatched: ' + str(path))
                persistent, runtime = platform_support.systemd_registration_directories()
                require(paths[0].parent == persistent and persistent != runtime,
                        'memory fixture used runtime registration')
            else:
                require(Path(record['artifact']).parent == platform_support.account_home() / 'Library/LaunchAgents',
                        'memory fixture did not use the account LaunchAgents directory')

    def uninstall(self):
        if self.removed or not (self.prefix / 'install.json').exists():
            return
        # Read newly staged selections even if installation failed after publication.
        config = json.loads((self.prefix / 'install.json').read_text())
        self.records = list(config.get('memory_services', {}).get('repositories', {}).values())
        self.session_records = [json.loads(path.read_text())
                                for path in (self.state / 'sessions').glob('*/native-service.json')]
        self.command([sys.executable, self.source / 'scripts/uninstall.py', '--prefix', self.prefix])
        for record in self.records:
            require(memory_observation(record)['status'] == 'absent',
                    'removed memory job is not provably absent')
            require(not Path(record['artifact']).exists(), 'removed memory artifact remains')
            if self.backend == 'systemd':
                require(not any(os.path.lexists(path) for path in platform_support.memory_registration_paths(record)),
                        'persistent memory registration remains')
        for record in self.session_records:
            require(platform_support.session_manager_observation(record)['status'] == 'absent',
                    'removed session job is not provably absent')
            require(not Path(record['artifact']).exists(), 'removed session artifact remains')
        require(not (self.prefix / 'install.json').exists(), 'installation configuration remains')
        if self.release == SOURCE:
            require(self.settings().get('statusLine') == self.status_line,
                    'uninstall did not restore the Claude status line')
            require('hooks' not in self.settings()
                    and 'KOINON CLAUDE' not in (self.root / 'claude' / 'CLAUDE.md').read_text(),
                    'uninstall did not remove the Claude guidance')
        require(not (self.prefix / 'session.py').exists(), 'runtime remains')
        self.removed = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--backend', choices=('systemd', 'launchd'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    evidence = dict(acceptance='unmet', backend=args.backend, checks=[], registration='persistent-memory')
    fixtures = []
    try:
        domain = f'gui/{os.geteuid()}' if args.backend == 'launchd' else None
        require(platform_support.memory_manager_available(args.backend, domain),
                'native user manager unavailable; acceptance unmet (no skip or runtime fallback)')
        if args.backend == 'systemd':
            # systemctl can answer over the manager's private socket before its
            # user-bus name is ready. Wait for the typed interface used by the
            # product, without registering or starting any fixture service.
            deadline = time.monotonic() + 15
            while True:
                try:
                    platform_support.systemd_registration_layout()
                    break
                except OSError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError('native user-manager D-Bus interface unavailable; environment acceptance unmet')
                    time.sleep(.1)
        first = Fixture(args.backend, session=True)
        fixtures.append(first)
        first.install(staged=True)
        for record in first.records:
            observed = memory_observation(record)
            require(observed['status'] == 'absent',
                    'no-start memory absence not established: ' + json.dumps(observed))
        for record in first.session_records:
            require(platform_support.session_manager_observation(record)['status'] == 'absent',
                    'no-start registered a session job')
        evidence['checks'].append('public no-start stages both components without registration')
        first.install()
        first.status()
        first.verify_registration()
        first.note('note', 'synthetic-install-preserved', '--type', 'finding')
        registration = Path(first.session_records[0]['state_directory']) / 'session.json'
        saved = registration.read_bytes()
        first.install()
        first.status()
        require('synthetic-install-preserved' in first.note('recall', 'synthetic-install-preserved'),
                'repeat installation lost stored note')
        evidence['checks'].append('public combined installation and exact repeat retain data')
        linked = first.root / 'linked'
        first.command(['git', '-C', first.repo, 'worktree', 'add', '--detach', linked])
        config = first.install(repo=linked)
        require(len(config['memory_services']['repositories']) == 1, 'linked worktree duplicated memory service')
        evidence['checks'].append('linked worktrees share one memory service')
        second = Fixture(args.backend, session=False)
        fixtures.append(second)
        config = second.install()
        require(config['participants'] == [] and config['codex'] is None,
                'memory-only installation required participant configuration')
        second.status()
        second.verify_registration()
        evidence['checks'].append('independent memory-only public installation')
        first.uninstall()
        second.status()
        require(registration.read_bytes() == saved, 'uninstall changed retained session registration')
        require((first.prefix / '.install.lock').exists(), 'uninstall deleted permanent installation lock')
        evidence['checks'].append('public uninstall removes owned jobs and retains state and unrelated service')
        first.install()
        first.status()
        first.verify_registration()
        require(registration.read_bytes() == saved, 'reinstall changed retained session registration')
        require('synthetic-install-preserved' in first.note('recall', 'synthetic-install-preserved'),
                'clean reinstall lost retained memory')
        evidence['checks'].append('clean public reinstall reuses retained session and memory data')
        evidence['acceptance'] = 'native_public_installation_complete'
    except Exception as exc:
        evidence['error'] = str(exc)
        evidence['manager_queries'] = list(MANAGER_QUERIES)
    finally:
        errors = []
        for fixture in reversed(fixtures):
            try:
                fixture.uninstall()
            except Exception as exc:
                errors.append(dict(root=str(fixture.root), error=str(exc)))
        evidence['cleanup'] = 'confirmed' if not errors else errors
        if errors:
            evidence['acceptance'] = 'unmet'
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(evidence, indent=2) + '\n')
        print(json.dumps(evidence, indent=2))
    return 0 if evidence['acceptance'] == 'native_public_installation_complete' else 1


if __name__ == '__main__':
    raise SystemExit(main())
