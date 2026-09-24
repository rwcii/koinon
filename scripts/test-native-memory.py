#!/usr/bin/env python3
"""Exercise an explicitly requested temporary memory installation under a native manager."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
import memory_service
from koinon import memory_service_artifacts
from koinon import memory_service_config
from koinon import platform_support


def wait_for(predicate, description, timeout=45):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = predicate()
        if last:
            return last
        time.sleep(.2)
    raise RuntimeError(f'{description}: deadline exceeded; last={last!r}')


class Fixture:
    def __init__(self, backend):
        self.backend = backend
        self.root = Path(tempfile.mkdtemp(prefix='koinon-memory-fixture-')).resolve()
        self.prefix = self.root / 'prefix space $literal %n "quote"'
        self.repo, self.state, self.units = (self.root / name for name in ('repo space', 'state space', 'units'))
        self.prefix.mkdir(mode=0o700)
        self.units.mkdir(mode=0o700)
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        spec = importlib.util.spec_from_file_location('fixture_installer', SOURCE / 'scripts/install.py')
        installer = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(installer)
        for name in installer.FILES:
            target = self.prefix / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(SOURCE / name, target)
        self.key, record = memory_service_config.selection(self.repo, self.state)
        record.update(backend=backend, state='installed', artifact_digest='0' * 64,
                      artifact=str(self.units / memory_service_config.artifact_name(self.key, backend)))
        if backend == 'systemd':
            record['template_version'] = 2
        if backend == 'launchd':
            record['manager_domain'] = f'gui/{os.geteuid()}'
        record['artifact_digest'] = memory_service_artifacts.digest(
            platform_support.memory_service_artifact(self.prefix, sys.executable, self.key, record))
        config = self.prefix / 'install.json'
        config.write_text(json.dumps(dict(state_root=str(self.state), unit_dir=str(self.units))))
        config.chmod(0o600)
        memory_service_artifacts.publish(self.prefix, sys.executable, record)
        self.selection = memory_service.Selection(self.prefix, self.repo)
        initial = memory_service.manager_observation(self.selection)
        if initial['status'] != 'absent':
            raise RuntimeError('synthetic manager identity is not provably absent: ' + json.dumps(initial))
        self.loader_path = None
        self.runs, self.children, self.failure, self.crash, self.block_marker = (
            self.root / name for name in ('runs', 'children', 'failure', 'crash', 'block-marker'))
        wrapper = self.prefix / 'memory_service.py'
        wrapper.write_text('if __name__ == "__main__":\n'
                          '    import sys as _fixture_sys\n'
                          '    if len(_fixture_sys.argv) > 1 and _fixture_sys.argv[1] == "run":\n'
                          f'        with open({str(self.runs)!r}, "a") as _fixture_log: _fixture_log.write("start\\n")\n'
                          + wrapper.read_text())
        child = self.prefix / 'memory.py'
        injection = ('if __name__ == "__main__":\n'
                     '    import os as _fixture_os, threading as _fixture_threading, time as _fixture_time\n'
                     '    from pathlib import Path as _FixturePath\n'
                     f'    with open({str(self.children)!r}, "a") as _fixture_log: _fixture_log.write("start\\n")\n'
                     f'    if _FixturePath({str(self.block_marker)!r}).exists():\n'
                     f'        _FixturePath({str(self.selection.refusal_path)!r}).mkdir()\n'
                     f'    if _FixturePath({str(self.failure)!r}).exists():\n'
                     f'        raise SystemExit(int(_FixturePath({str(self.failure)!r}).read_text()))\n'
                     '    def _fixture_crash():\n'
                     '        while True:\n'
                     f'            marker = _FixturePath({str(self.crash)!r})\n'
                     '            if marker.exists():\n'
                     '                marker.unlink()\n'
                     '                _fixture_os._exit(75)\n'
                     '            _fixture_time.sleep(.1)\n'
                     '    _fixture_threading.Thread(target=_fixture_crash, daemon=True).start()\n')
        child.write_text(injection + child.read_text())

    def count(self, path):
        return len(path.read_text().splitlines()) if path.exists() else 0

    def owner_dead(self):
        owner = memory_service.read_record(self.selection, self.selection.owner_path)
        return owner and memory_service.process_state(owner) == 'dead' and (
            not owner['child'] or memory_service.process_state(owner['child']) == 'dead')

    def result(self):
        action = platform_support.memory_manager_action
        def selected(record, operation):
            return action(record, operation, runtime=self.backend == 'systemd')
        with patch.object(platform_support, 'memory_manager_action', side_effect=selected):
            return memory_service.ensure_managed(self.selection)

    def ensure(self, retry=False):
        if retry:
            memory_service.retry(self.selection)
        result = self.result()
        if not result.get('managed') or not result['running']:
            raise RuntimeError('native managed readiness not established: ' + json.dumps(result))
        report = memory_service.manager_observation(self.selection)
        self.loader_path = Path(report["artifact"])
        if self.backend == "systemd":
            expected_parent = Path(os.environ["XDG_RUNTIME_DIR"]) / "systemd/user"
            if self.loader_path.parent != expected_parent:
                raise RuntimeError("runtime fixture loaded from an unexpected unit directory")
        return result

    def stop(self):
        memory_service.stop(self.selection)
        wait_for(self.owner_dead, 'owned runner and child exit')

    def memory_cli(self, *args):
        result = subprocess.run([sys.executable, str(self.prefix / 'memory.py'), '--state-dir', str(self.state),
                                 '--repo-path', str(self.repo), *args], capture_output=True, text=True, timeout=15)
        if result.returncode:
            raise RuntimeError('synthetic memory command failed')
        return json.loads(result.stdout)

    def remove(self):
        report = memory_service.manager_observation(self.selection)
        if report['status'] == 'unknown':
            raise RuntimeError('fixture removal ownership unknown; temporary evidence retained')
        owner = memory_service.read_record(self.selection, self.selection.owner_path)
        if owner and not self.owner_dead():
            self.stop()
        database = self.selection.home / 'memory.sqlite3'
        before = hashlib.sha256(database.read_bytes()).hexdigest() if database.exists() else None
        if report['status'] == 'observed':
            # Re-verify exact registered artifact/argv before deactivation.
            if memory_service.manager_observation(self.selection)['status'] != 'observed':
                raise RuntimeError('fixture ownership changed during removal')
            platform_support.memory_manager_action(self.selection.record, 'deactivate', runtime=self.backend == 'systemd')
            wait_for(lambda: memory_service.manager_observation(self.selection)['status'] == 'absent',
                     'owned fixture job removal')
        if self.backend == 'systemd' and self.loader_path is not None:
            for link in (self.loader_path, self.loader_path.parent / 'default.target.wants' / self.loader_path.name):
                if os.path.lexists(link):
                    raise RuntimeError('fixture enablement link remains after removal')
        if before is not None and hashlib.sha256(database.read_bytes()).hexdigest() != before:
            raise RuntimeError('manager deactivation changed the retained store')
        return before is not None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--backend', choices=('systemd', 'launchd'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    evidence = dict(acceptance='unmet', backend=args.backend, checks=[],
                    registration='runtime' if args.backend == 'systemd' else 'temporary-plist')
    fixtures = []
    try:
        domain = f'gui/{os.geteuid()}' if args.backend == 'launchd' else None
        if not platform_support.memory_manager_available(args.backend, domain):
            raise RuntimeError('selected native user manager unavailable; acceptance unmet')
        primary = Fixture(args.backend)
        fixtures.append(primary)
        primary.ensure()
        evidence['loader_parent'] = str(primary.loader_path.parent)
        evidence['checks'].append('owned activation and verified readiness')
        primary.memory_cli('--consumer', 'synthetic-fixture', 'note', 'fixture-preserved', '--type', 'finding')
        generation = memory_service.read_record(primary.selection, primary.selection.owner_path)['generation']
        primary.crash.touch()
        wait_for(lambda: (lambda value: value['running'] and value['generation'] != generation)(
            memory_service.managed_status(primary.selection)), 'native throttled crash restart')
        evidence['checks'].append('child crash and manager restart')
        for code in (70, 78):
            primary.stop()
            primary.failure.write_text(str(code))
            starts = primary.count(primary.runs)
            result = primary.result()
            if result.get('status') != 'refused' or result.get('exit_status') != code:
                raise RuntimeError('permanent failure did not remain visible')
            wait_for(primary.owner_dead, 'permanent wrapper exit')
            time.sleep(25)  # Exceeds the configured 10-second restart throttle.
            if primary.count(primary.runs) != starts + 1:
                raise RuntimeError('manager restarted a permanently refused wrapper')
            primary.failure.unlink()
            primary.ensure(retry=True)
            evidence['checks'].append(f'permanent {code} non-restart and explicit retry')
        primary.stop()
        primary.failure.write_text('70')
        primary.block_marker.touch()
        starts = primary.count(primary.runs)
        try:
            primary.result()
        except memory_service.RunnerError:
            pass
        else:
            raise RuntimeError('unwritable refusal was reported ready')
        wait_for(primary.owner_dead, 'unwritable-marker wrapper exit')
        time.sleep(25)
        if primary.count(primary.runs) != starts + 1:
            raise RuntimeError('unwritable marker caused wrapper restart')
        diagnostic = primary.selection.home / 'supervisor-diagnostic.json'
        if diagnostic.stat().st_mode & 0o777 != 0o600:
            raise RuntimeError('failure diagnostic is not private')
        if json.loads(diagnostic.read_text())['primary_code'] != 'memory_software_failure':
            raise RuntimeError('original failure diagnostic missing')
        primary.selection.refusal_path.rmdir()  # Remove only this fixture's injected empty directory.
        primary.failure.unlink()
        primary.block_marker.unlink()
        primary.ensure(retry=True)
        evidence['checks'].append('unwritable refusal non-restart and recovery')
        if 'fixture-preserved' not in json.dumps(primary.memory_cli('recall', 'fixture-preserved')):
            raise RuntimeError('stored note lost across lifecycle recovery')
        secondary = Fixture(args.backend)
        fixtures.append(secondary)
        secondary.ensure()
        if not primary.remove() or not memory_service.managed_status(secondary.selection)['running']:
            raise RuntimeError('owned removal lost store or affected unrelated fixture')
        evidence['checks'].append('stop and owned removal preserve store and unrelated job')
        evidence['acceptance'] = 'native_memory_lifecycle_complete'
        return 0
    except Exception as exc:
        evidence['error'] = str(exc)
        import traceback as _tb
        evidence['diagnostic_traceback'] = _tb.format_exception(exc)[-12:]
        chain, cause = [], exc
        while cause is not None and len(chain) < 6:
            chain.append(f'{type(cause).__name__}: {cause!r} code={getattr(cause, "code", None)!r}')
            cause = cause.__cause__ or cause.__context__
        evidence['diagnostic_chain'] = chain
        for fixture in fixtures:
            for name in ('supervisor-diagnostic.json', 'supervisor.json', 'owner.json'):
                for path in fixture.state.rglob(name):
                    try:
                        evidence.setdefault('diagnostic_files', {})[str(path.relative_to(fixture.root))] = path.read_text()[:2000]
                    except OSError:
                        pass
        return 1
    finally:
        for fixture in reversed(fixtures):
            try:
                fixture.remove()
                shutil.rmtree(fixture.root)
            except Exception as exc:
                evidence['acceptance'] = 'unmet'
                evidence.setdefault('cleanup_errors', []).append(str(exc))
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(evidence, indent=2) + '\n')
        args.output.chmod(0o600)
        print(json.dumps(evidence))
        if evidence['acceptance'] == 'unmet':
            # Cleanup failure must also fail CI even if the checks had returned 0.
            raise SystemExit(1)


if __name__ == '__main__':
    raise SystemExit(main())
