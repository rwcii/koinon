#!/usr/bin/env python3
"""Collect native launchd evidence using one explicitly requested throwaway job."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import time
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import platform_support


def command(argv):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=15)
    return result


def describe(result):
    # Do not retain manager environment dumps. Only synthetic job identity fields
    # and the result shape are useful to the ownership implementation.
    raw = result.stdout.encode()
    value = dict(returncode=result.returncode, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest())
    if raw.startswith(b'<?xml'):
        try:
            data = plistlib.loads(raw)
            value['plist'] = {key: data[key] for key in
                             ('Label', 'Program', 'ProgramArguments', 'PID', 'LastExitStatus') if key in data}
        except (ValueError, TypeError, plistlib.InvalidFileException):
            value['plist_unreadable'] = True
    else:
        lines = []
        in_arguments = False
        for line in result.stdout.splitlines():
            stripped = line.strip()
            if line.startswith('\t') and not line.startswith('\t\t') and stripped.startswith(
                    ('path = ', 'program = ', 'pid = ', 'state = ', 'arguments = ')):
                lines.append(line)
                in_arguments = stripped == 'arguments = {'
            elif in_arguments:
                lines.append(line)
                if stripped == '}':
                    in_arguments = False
        value['identity_lines'] = lines
    return value


def wait_for(predicate, name):
    deadline = time.monotonic() + 20
    last = None
    while time.monotonic() < deadline:
        last = predicate()
        if last:
            return last
        time.sleep(.1)
    raise RuntimeError(f'{name}: deadline exceeded; last={last!r}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    evidence = dict(acceptance='unmet', purpose='native manager interface evidence, not Koinon lifecycle acceptance')
    try:
        if not platform_support.DARWIN:
            raise RuntimeError('requires a native macOS user manager')
        domain = f'gui/{os.geteuid()}'
        label = 'io.github.rwcii.koinon.fixture.' + uuid.uuid4().hex
        target = domain + '/' + label
        evidence['platform'] = command(['sw_vers']).stdout.strip()
        evidence['help'] = {}
        for name in ('bootstrap', 'bootout', 'kickstart', 'list', 'print'):
            help_result = command(['launchctl', 'help', name])
            evidence['help'][name] = dict(returncode=help_result.returncode,
                                         stdout=help_result.stdout, stderr=help_result.stderr)
        if command(['launchctl', 'print', domain]).returncode != 0:
            raise RuntimeError('selected current-user GUI domain is unavailable')
        missing = command(['launchctl', 'print', target])
        evidence['missing_print'] = describe(missing)
        evidence['missing_list'] = describe(command(['launchctl', 'list', label]))
        if missing.returncode != 113:
            raise RuntimeError('could not prove synthetic label absent with expected ESRCH result')
        with tempfile.TemporaryDirectory(prefix='koinon-launchd-fixture-') as directory:
            root = Path(directory).resolve()
            root.chmod(0o700)
            starts = root / 'starts'
            program = ('import os,pathlib,time; '
                       f'p=pathlib.Path({str(starts)!r}); '
                       'f=p.open("a"); f.write(str(os.getpid())+"\\n"); f.close(); time.sleep(90)')
            argv = [sys.executable, '-c', program, 'argument with spaces']
            artifact = root / (label + '.plist')
            artifact.write_bytes(plistlib.dumps(dict(Label=label, ProgramArguments=argv,
                                                     RunAtLoad=True, KeepAlive=False, Umask=63)))
            artifact.chmod(0o600)
            attempted = False
            try:
                attempted = True
                loaded = command(['launchctl', 'bootstrap', domain, str(artifact)])
                evidence['bootstrap'] = describe(loaded)
                if loaded.returncode != 0:
                    raise RuntimeError('synthetic bootstrap failed')
                wait_for(lambda: starts.exists() and starts.read_text().splitlines(), 'synthetic startup')
                evidence['loaded_print'] = describe(command(['launchctl', 'print', target]))
                evidence['loaded_list'] = describe(command(['launchctl', 'list', label]))
                evidence['loaded_list_xml'] = describe(command(['launchctl', 'list', '-x', label]))
                evidence['duplicate_bootstrap'] = describe(command(['launchctl', 'bootstrap', domain, str(artifact)]))
                evidence['kickstart'] = describe(command(['launchctl', 'kickstart', '-k', target]))
                wait_for(lambda: len(starts.read_text().splitlines()) >= 2, 'synthetic restart')
                evidence['starts'] = len(starts.read_text().splitlines())
            finally:
                if attempted:
                    removed = command(['launchctl', 'bootout', target])
                    evidence['bootout'] = describe(removed)
                    wait_for(lambda: command(['launchctl', 'print', target]).returncode == 113,
                             'synthetic job removal')
            evidence['removed_print'] = describe(command(['launchctl', 'print', target]))
            evidence['acceptance'] = 'interface_probe_complete'
        return 0
    except Exception as exc:
        evidence['error'] = str(exc)
        return 1
    finally:
        args.output.write_text(json.dumps(evidence, indent=2) + '\n')
        args.output.chmod(0o600)
        print(json.dumps(dict(acceptance=evidence['acceptance'], output=str(args.output))))


if __name__ == '__main__':
    raise SystemExit(main())
