"""Session inspection must not create a registration that blocks native ensure."""
from contextlib import redirect_stdout
import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import session
from koinon import durable_state, platform_support
from scripts.install import FILES
from repo_root import ROOT


def tree_digest(root):
    digest = hashlib.sha256()
    for path in sorted(root.rglob('*')):
        info = path.lstat()
        digest.update(str(path.relative_to(root)).encode())
        digest.update(str(stat.S_IMODE(info.st_mode)).encode())
        digest.update(path.read_bytes() if path.is_file() else b'directory')
    return digest.hexdigest()


class SessionIdentityTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.prefix = self.root / "installed app's files"
        self.prefix.mkdir(mode=0o700)
        for name in FILES:
            if name.endswith('.py'):
                target = self.prefix / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
                target.chmod(0o600)
        self.env = dict(os.environ, CODEX_HOME=str(self.root / 'codex'),
                        DSH_HOME=str(self.root / 'deepseek'),
                        CLAUDE_CONFIG_DIR=str(self.root / 'claude'))
        for key in ('CODEX_THREAD_ID', 'DSH_SESSION_ID', 'PYTHONDONTWRITEBYTECODE', 'PYTHONPYCACHEPREFIX'):
            self.env.pop(key, None)

    def configure(self, backend):
        config = dict(state_root=str(self.root / 'state'), unit_dir=str(self.root / 'units'),
                      codex=sys.executable, dsh_url='http://127.0.0.1:9999',
                      dsh_credentials=str(self.root / 'deepseek' / 'credentials.json'))
        if backend is not None:
            config['session_backend'] = backend
        durable_state.publish(self.prefix / 'install.json', config)
        return config

    def invoke(self, action, agent, thread):
        output = io.StringIO()
        args = ['session.py', action, '--agent', agent, '--thread', thread,
                '--repo', str(self.root / 'different-repo')]
        with patch.object(session, '__file__', str(self.prefix / 'session.py')), \
                patch.object(sys, 'argv', args), patch.dict(os.environ, self.env, clear=True), \
                patch.object(session, 'peers', return_value=[]), \
                patch.object(platform_support, 'session_manager_observation', return_value={'status': 'unknown'}), \
                patch.object(platform_support, 'memory_manager_available', return_value=False), \
                patch.object(platform_support, 'user_service_manager', return_value=subprocess.CompletedProcess([], 1)), \
                redirect_stdout(output):
            code = 0
            try:
                session.main()
            except SystemExit as exc:
                code = exc.code
        return code, json.loads(output.getvalue())

    def test_unregistered_status_is_read_only_then_ensure_registers(self):
        for backend in (None, 'manual', 'systemd', 'launchd'):
            for agent in ('codex', 'deepseek'):
                with self.subTest(backend=backend, agent=agent):
                    config = self.configure(backend)
                    thread = f'synthetic-{backend}-{agent}'
                    state, _, _ = session.details(self.prefix, config, thread, '/synthetic-repo', agent)
                    # Check the actual installed entrypoint, including import-time cache writes.
                    before = tree_digest(self.root)
                    result = subprocess.run([sys.executable, str(self.prefix / 'session.py'), 'status',
                                             '--agent', agent, '--thread', thread],
                                            capture_output=True, text=True, env=self.env, timeout=15)
                    self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                    reported = json.loads(result.stdout)
                    self.assertEqual({k: reported[k] for k in ('status', 'state_dir', 'agent')},
                                     dict(status='unregistered', state_dir=str(state), agent=agent))
                    self.assertIsNone(reported['guide_stale'])
                    self.assertEqual(reported['runtime']['state'], 'unknown')
                    self.assertEqual(tree_digest(self.root), before)
                    code, ensured = self.invoke('ensure', agent, thread)
                    self.assertEqual(code, 0, ensured)
                    self.assertEqual(ensured['status'], 'manual_required')
                    saved = json.loads((state / 'session.json').read_text())
                    self.assertEqual(ensured['name'], saved['name'])
                    self.assertEqual(ensured['state_dir'], str(state))
                    self.assertEqual(shlex.split(ensured['inbox_command']),
                                     [sys.executable, str(self.prefix / 'bridge.py'), '--state-dir', str(state), 'inbox'])
                    code, reported = self.invoke('status', agent, thread)
                    self.assertEqual(code, 75 if backend in ('systemd', 'launchd') else 0)
                    for key in ('name', 'state_dir', 'inbox_command'):
                        self.assertEqual(reported[key], ensured[key])
                    if backend in ('systemd', 'launchd'):
                        self.assertTrue((state / 'native-service.json').exists())
                        self.assertEqual(reported['status'], 'unavailable')

    def test_native_identity_uses_saved_name_and_preserves_lifecycle_status(self):
        from koinon import session_service_manager
        config = self.configure('systemd')
        thread = 'synthetic-saved-name'
        self.invoke('ensure', 'codex', thread)
        state, _, _ = session.details(self.prefix, config, thread, '/synthetic-repo')
        saved = durable_state.read(state / 'session.json')
        # The saved registration, including any name collision suffix, owns identity.
        for action in ('ensure', 'status'):
            for observed, expected_code in ((dict(status='running', basis='synthetic-live-pair'), 0),
                                            (dict(status='refused', exit_status=78), 78)):
                with self.subTest(action=action, status=observed['status']), \
                        patch.object(session_service_manager, action, return_value=observed):
                    code, result = self.invoke(action, 'codex', thread)
                    self.assertEqual(code, expected_code)
                    self.assertEqual(result['name'], saved['name'])
                    for key, value in observed.items():
                        self.assertEqual(result[key], value)
                    self.assertNotIn('name', observed, 'do not mutate the underlying observation')
