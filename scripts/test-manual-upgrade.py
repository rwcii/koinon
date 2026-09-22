#!/usr/bin/env python3
"""Opt-in public manual handoff acceptance with parent-managed foreground jobs."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
import memory_service
from koinon import upgrade_exclusion
from koinon import upgrade_manual


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-isolated-job', action='store_true', required=True)
    parser.add_argument('--interrupt-phase', choices=['all'] + [str(i) for i in range(1, 20)])
    parser.add_argument('--initial-state', choices=('running', 'stopped'), default='running')
    args = parser.parse_args()
    os.umask(0o077)
    install = module('manual_install_fixture', SOURCE / 'scripts/test-native-install.py')
    acceptance = module('manual_upgrade_fixture', SOURCE / 'scripts/test-native-upgrade.py')
    fixture = install.Fixture('manual', session=False)
    jobs, logs, handoffs = [], [], []
    selections = []
    try:
        fixture.install()
        def select(kind):
            upgrading = upgrade_exclusion.read(fixture.prefix) is not None
            if kind != 'memory':
                raise ValueError('fixture selects only owned manual memory')
            return memory_service.Selection(fixture.prefix, fixture.repo, upgrading=upgrading)
        def argv(kind):
            return select(kind).start_command()
        def start(kind):
            log = (fixture.root / f'foreground-{len(jobs)}.log').open('wb')
            logs.append(log)
            job = subprocess.Popen(argv(kind), stdin=subprocess.DEVNULL, stdout=log,
                                   stderr=subprocess.STDOUT, env=fixture.env)
            jobs.append(job)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if job.poll() is not None:
                    raise RuntimeError('managed foreground fixture exited before handoff readiness')
                try:
                    if upgrade_manual.observe(select(kind), kind)['running']:
                        return
                except (OSError, ValueError):
                    pass
                time.sleep(.05)
            raise RuntimeError('manual foreground handoff never became ready')
        for kind in ('memory',):
            start(kind)
            selections.append(kind)
        fixture.note('note', 'retained manual fixture note', '--type', 'finding')
        before = json.loads(fixture.note('status'))['result']
        if args.initial_state == 'stopped':
            for kind in reversed(selections):
                upgrade_manual.stop(select(kind), kind)
        source = fixture.root / 'new-source'
        shutil.copytree(fixture.source, source)
        source.chmod(0o700)
        (fixture.prefix / 'LICENSE').write_text('synthetic previous manual release')
        def handoff(pending):
            kind = pending['component']
            if kind not in selections or pending['argv'] != argv(kind):
                raise RuntimeError('handoff does not match exact synthetic selection')
            # This parent retains process handles and owns their lifetime. No
            # detached child or copied peer command establishes a handoff.
            start(kind)
            handoffs.append(kind)
        result = acceptance.public_upgrade(fixture, source, args.interrupt_phase, handoff=handoff)
        if not result['ok'] or set(handoffs) != set(selections):
            raise RuntimeError('public operation did not verify the foreground handoff')
        for kind in selections:
            observed = upgrade_manual.observe(select(kind), kind)
            if observed['running'] != (args.initial_state == 'running'):
                raise RuntimeError('manual component original activity was not restored')
        if args.initial_state == 'stopped':
            start('memory')
        after = json.loads(fixture.note('status'))['result']
        if any(before[key] != after[key] for key in ('store_id', 'head', 'floor')):
            raise RuntimeError('manual upgrade changed retained memory identity/cursors')
        print(json.dumps(dict(ok=True, initial_state=args.initial_state,
            interrupted=args.interrupt_phase, checks=['persistent_foreground_handoff',
            'kernel_and_child_readiness', 'original_activity_restored', 'memory_preserved'])))
        return 0
    finally:
        for kind in reversed(selections):
            upgrade_manual.stop(select(kind), kind)
        for job in jobs:
            job.wait(timeout=10)
        for log in logs:
            log.close()


if __name__ == '__main__':
    raise SystemExit(main())
