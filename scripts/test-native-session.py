#!/usr/bin/env python3
"""Run explicitly requested isolated native jobs with synthetic session participants."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import uuid

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
import durable_state
import platform_support
from scripts.install import FILES
import session_service
import session_service_artifacts
import session_service_config
import session_service_manager


def wait_for(predicate, description, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.2)
    raise RuntimeError(description + ': deadline exceeded')


class Fixture:
    def __init__(self, backend):
        self.root = Path(tempfile.mkdtemp(prefix='koinon-session-fixture-')).resolve()
        self.prefix = self.root / 'prefix space $literal %n "quote"'
        self.prefix.mkdir(mode=0o700)
        for filename in FILES:
            target = self.prefix / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(SOURCE / filename, target)
            target.chmod(0o600)
        account = self.root / 'account'
        account.mkdir(mode=0o700)
        with (self.prefix / 'platform_support.py').open('a') as stream:
            stream.write('\ndef account_home():\n    return Path(' + repr(str(account)) + ')\n')
        wrapper = self.prefix / 'session_service.py'
        wrapper.write_text('import os as _fixture_os\n_fixture_os.environ["CLAUDE_CONFIG_DIR"] = '
                           + repr(str(self.root / 'claude')) + '\n' + wrapper.read_text())
        self.registration = dict(thread='synthetic-' + uuid.uuid4().hex,
                                 name='synthetic-native-' + uuid.uuid4().hex[:12], repo=str(self.root))
        state = self.root / 'state'
        key, _, _ = session_service_config.registration_identity(self.registration)
        self.home = state / 'sessions' / key
        for directory in (state, state / 'sessions', self.home, self.home / 'native-service'):
            directory.mkdir(mode=0o700)
        config = dict(state_root=str(state), unit_dir=str(self.root / 'legacy-units'), codex=sys.executable)
        durable_state.publish(self.prefix / 'install.json', config)
        durable_state.publish(self.home / 'session.json', self.registration)
        self.record = session_service_config.selection(self.prefix, sys.executable, self.home, config,
                    self.registration, backend, domain=f'gui/{os.geteuid()}' if backend == 'launchd' else None)
        session_service_artifacts.publish(self.record)
        self.selection = session_service.Selection(self.prefix, self.home, backend=backend)
        if session_service_manager.observation(self.selection)['status'] != 'absent':
            raise RuntimeError('synthetic manager identity is not provably absent')
        self.runs = self.root / 'runs'
        wrapper.write_text('if __name__ == "__main__":\n'
                           '    import sys as _fixture_sys\n'
                           '    if len(_fixture_sys.argv) > 1 and _fixture_sys.argv[1] == "run":\n'
                           + '        with open(' + repr(str(self.runs)) + ', "a") as stream: stream.write("start\\n")\n'
                           + wrapper.read_text())
        self.failure = self.root / 'fail-startup'
        self.prehandshake = self.root / 'crash-before-handoff'
        self.crash = self.root / 'crash-notifier'
        notifier = self.prefix / 'notify.py'
        notifier.write_text('''if __name__ == '__main__':
    import os as _fixture_os, threading as _fixture_threading, time as _fixture_time
    from pathlib import Path as _FixturePath
    _prehandshake = _FixturePath(''' + repr(str(self.prehandshake)) + ''')
    if _prehandshake.exists():
        _prehandshake.unlink()
        _fixture_os._exit(75)
    if _FixturePath(''' + repr(str(self.failure)) + ''').exists():
        raise SystemExit(78)
    def _fixture_crash():
        while True:
            marker = _FixturePath(''' + repr(str(self.crash)) + ''')
            if marker.exists():
                code = int(marker.read_text())
                marker.unlink()
                _fixture_os._exit(code)
            _fixture_time.sleep(.1)
    _fixture_threading.Thread(target=_fixture_crash, daemon=True).start()
''' + notifier.read_text())

    def ensure(self):
        completed = subprocess.run([sys.executable, str(self.prefix / 'session.py'), 'ensure',
                    '--thread', self.registration['thread'], '--repo', str(self.root)],
                    capture_output=True, text=True, timeout=60)
        if completed.returncode:
            raise RuntimeError('public ensure failed: ' + completed.stdout + completed.stderr)
        result = json.loads(completed.stdout)
        if result.get('status') != 'running' or result.get('managed') is not True:
            raise RuntimeError('native pair readiness not established: ' + json.dumps(result))
        return result

    def remove(self):
        result = session_service_manager.deactivate(self.selection)
        if result['status'] != 'deactivated':
            raise RuntimeError('owned fixture deactivation unconfirmed')
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--backend', choices=('systemd', 'launchd'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    evidence = dict(acceptance='unmet', backend=args.backend, checks=[], cleanup='unconfirmed')
    fixtures = []
    try:
        first = Fixture(args.backend)
        fixtures.append(first)
        second = Fixture(args.backend)
        fixtures.append(second)
        first.prehandshake.touch()
        started = first.ensure()
        if len(first.runs.read_text().splitlines()) < 2:
            raise RuntimeError('pre-handshake child crash did not restart wrapper')
        evidence['checks'].append('prehandshake_child_crash_restarts_pair')
        second_started = second.ensure()
        evidence['checks'].append('two_owned_pairs_ready')
        first.crash.write_text('75')
        def restarted():
            result = session_service_manager.status(first.selection)
            return result if result.get('managed') and result.get('owner', {}).get('generation') != started['owner']['generation'] else None
        wait_for(restarted, 'temporary notifier failure restarts complete pair')
        evidence['checks'].append('temporary_failure_restarts_pair')
        unaffected = session_service_manager.status(second.selection)
        if unaffected.get('status') != 'running' or unaffected['owner']['generation'] != second_started['owner']['generation']:
            raise RuntimeError('second session was disrupted')
        evidence['checks'].append('second_session_unchanged')
        first.crash.write_text('70')
        refused = wait_for(lambda: (result if (result := session_service_manager.status(first.selection))['status'] == 'refused' else None),
                           'permanent runtime refusal')
        if refused['exit_status'] != 70:
            raise RuntimeError('permanent failure classification lost')
        wait_for(lambda: session_service.status(first.selection)['status'] == 'stopped', 'refused pair exit')
        attempts = first.runs.read_text()
        time.sleep(12)
        if first.runs.read_text() != attempts:
            raise RuntimeError('permanent failure restarted automatically')
        first.selection.records.retry()
        first.ensure()
        evidence['checks'].append('permanent_runtime_refusal_and_explicit_retry')
        first.remove()
        fixtures.remove(first)
        if not (first.home / 'inbox.sqlite3').is_file() or not (first.home / 'native-service.json').is_file():
            raise RuntimeError('owned removal discarded retained data')
        evidence['checks'].append('guarded_shutdown_deregistration_retains_data')
        failed_start = Fixture(args.backend)
        fixtures.append(failed_start)
        failed_start.failure.touch()
        result = session_service_manager.ensure(failed_start.selection)
        if result['status'] != 'refused' or result['exit_status'] != 78:
            raise RuntimeError('permanent startup refusal missing')
        wait_for(lambda: session_service.status(failed_start.selection)['status'] == 'stopped', 'startup refusal pair exit')
        failed_start.remove()
        fixtures.remove(failed_start)
        evidence['checks'].append('permanent_startup_refusal_and_deregistration')
        second.remove()
        fixtures.remove(second)
        evidence.update(acceptance='met', cleanup='confirmed')
    except Exception as exc:
        evidence['failure'] = dict(type=type(exc).__name__, error=str(exc), paths=list(getattr(exc, 'paths', ())))
    finally:
        failures = []
        for fixture in fixtures:
            try:
                fixture.remove()
            except Exception as exc:
                failures.append(dict(directory=str(fixture.root), error=str(exc)))
        if failures:
            evidence['cleanup_failures'] = failures
        elif fixtures:
            evidence['cleanup'] = 'confirmed'
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(evidence, indent=2) + '\n')
        print(json.dumps(evidence, indent=2))
    return 0 if evidence['acceptance'] == 'met' else 1


if __name__ == '__main__':
    raise SystemExit(main())
