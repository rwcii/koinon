"""Run the test suite under a progress watchdog.

    python3 tests/run.py -v                 # every test under tests/
    python3 tests/run.py -v test_session    # named modules, classes or tests

A run that makes no progress for the bound (300 seconds times KOINON_TEST_TIMEOUT_SCALE, or
KOINON_TEST_WATCHDOG_SECONDS when set) is ended with exit status 3. Before it exits, the
watchdog prints the running test, or the class or module fixture when no test is running, the
stack of every thread, and the stack of each asyncio task of the main thread's event loop. A faulthandler backstop 120 seconds later ends a run whose blocked
thread holds the interpreter lock. `python3 -m unittest discover -s tests` still works, without
the watchdog.
"""
import asyncio
import faulthandler
import math
import os
from pathlib import Path
import sys
import threading
import time
import unittest

TESTS = Path(__file__).resolve().parent
sys.path[:0] = [str(TESTS), os.getcwd()]

import waiting  # noqa: E402

WATCHDOG_VARIABLE = 'KOINON_TEST_WATCHDOG_SECONDS'
DEFAULT_BOUND = 300
BACKSTOP_MARGIN = 120
EXIT_STATUS = 3
FIXTURES = ('setUpClass', 'tearDownClass', 'setUpModule', 'tearDownModule')
VALUED_OPTIONS = ('-k', '-p', '-s', '-t', '--pattern', '--start-directory', '--top-level-directory')


def read_bound(value):
    if value is None or value == '':
        return DEFAULT_BOUND * waiting.SCALE
    try:
        bound = float(value)
    except ValueError:
        raise ValueError(f'{WATCHDOG_VARIABLE} must be a finite positive number, not {value!r}') from None
    if not (math.isfinite(bound) and bound > 0):
        raise ValueError(f'{WATCHDOG_VARIABLE} must be a finite positive number, not {value!r}')
    return bound


class Watchdog:
    """Ends the run when no test starts or stops within the bound."""

    def __init__(self, bound, stream):
        self.bound = bound
        self.stream = stream
        self.main = threading.main_thread()
        self.lock = threading.Lock()
        self.running = None
        self.last = time.monotonic()
        self.progress(None)

    def progress(self, test_id):
        with self.lock:
            self.running = test_id
            self.last = time.monotonic()
        faulthandler.dump_traceback_later(self.bound + BACKSTOP_MARGIN, exit=True, file=self.stream)

    def start(self):
        threading.Thread(target=self.watch, name='test-watchdog', daemon=True).start()

    def watch(self):
        while True:
            time.sleep(min(1.0, self.bound / 10))
            with self.lock:
                idle = time.monotonic() - self.last
                running = self.running
            if idle >= self.bound:
                self.fire(idle, running)

    def activity(self, running):
        if running is not None:
            return f'running test: {running}'
        frame = sys._current_frames().get(self.main.ident)
        while frame is not None:
            name = frame.f_code.co_name
            if name in ('setUpClass', 'tearDownClass'):
                owner = frame.f_locals.get('cls')
                label = f'{owner.__module__}.{owner.__qualname__}' if owner else '?'
                return f'running fixture: {label}.{name}'
            if name in ('setUpModule', 'tearDownModule'):
                return f'running fixture: {frame.f_globals.get("__name__", "?")}.{name}'
            frame = frame.f_back
        return 'no test or fixture running'

    def fire(self, idle, running):
        stream = self.stream
        stream.write(f'\nWATCHDOG: no test progress for {idle:.0f}s (bound {self.bound:g}s); '
                     f'{self.activity(running)}\n')
        stream.flush()
        faulthandler.dump_traceback(file=stream, all_threads=True)
        stream.flush()
        self.dump_tasks()
        os._exit(EXIT_STATUS)

    def dump_tasks(self):
        """Print each task of the main thread's event loop; a thread dump shows only the idle loop."""
        frame = sys._current_frames().get(self.main.ident)
        while frame is not None:
            loop = frame.f_locals.get('self')
            if frame.f_code.co_name == 'run_forever' and isinstance(loop, asyncio.AbstractEventLoop):
                break
            frame = frame.f_back
        else:
            return
        stream = self.stream
        try:
            tasks = list(asyncio.all_tasks(loop))
        except RuntimeError as error:
            stream.write(f'\nasyncio tasks unavailable: {error}\n')
            return
        stream.write(f'\nasyncio tasks of the main thread ({len(tasks)}):\n')
        for task in tasks:
            try:
                task.print_stack(file=stream)
            except Exception as error:
                stream.write(f'{task!r}: stack unavailable: {error}\n')
        stream.flush()


class WatchdogResult(unittest.TextTestResult):
    watchdog = None

    def startTest(self, test):
        self.watchdog.progress(test.id())
        super().startTest(test)

    def stopTest(self, test):
        super().stopTest(test)
        self.watchdog.progress(None)


class WatchdogRunner(unittest.TextTestRunner):
    resultclass = WatchdogResult


def arguments(args):
    """Discover under tests/ unless the arguments name what to run."""
    names = []
    skip = False
    for arg in args:
        if skip:
            skip = False
        elif arg in VALUED_OPTIONS:
            skip = True
        elif not arg.startswith('-'):
            names.append(arg)
    if names or 'discover' in args:
        return args
    return ['discover', '-s', str(TESTS), '-t', str(TESTS), *args]


HOMES_VARIABLE = 'KOINON_TEST_HOMES'
# The same name as platform_support.MANAGER_GUARD_VARIABLE. The runner does not import
# koinon, because a test may start it from another working directory.
MANAGER_GUARD_VARIABLE = 'KOINON_TEST_MANAGER_GUARD'


def isolate_agent_homes():
    """Point the agent configuration directories at a temporary directory for the whole run.

    Tests that start real installers, notifiers or status-line wrappers would otherwise read
    or write the developer's Claude settings and Codex sessions. A test that needs its own
    directories still sets them itself.
    """
    import atexit
    import shutil
    import tempfile
    inherited = os.environ.get(HOMES_VARIABLE)
    if inherited and Path(inherited).is_dir():
        # A runner started by a test reuses its parent's directories, so a child that
        # exits without cleanup leaves nothing behind.
        return Path(inherited)
    root = Path(tempfile.mkdtemp(prefix='koinon-test-homes-'))
    atexit.register(shutil.rmtree, root, ignore_errors=True)
    for variable, name in (('CLAUDE_CONFIG_DIR', 'claude'), ('CODEX_HOME', 'codex')):
        (root / name).mkdir(mode=0o700)
        os.environ[variable] = str(root / name)
    os.environ[HOMES_VARIABLE] = str(root)
    return root


def guard_service_managers():
    """Make every unpatched call to the real systemd or launchd manager fail its test.

    A test that reaches the real manager can register and start real user services outside
    its temporary tree. The guard holds for child processes too.
    """
    os.environ[MANAGER_GUARD_VARIABLE] = '1'


def main(args=None):
    args = sys.argv[1:] if args is None else args
    isolate_agent_homes()
    guard_service_managers()
    watchdog = Watchdog(read_bound(os.environ.get(WATCHDOG_VARIABLE)), sys.__stderr__)
    WatchdogResult.watchdog = watchdog
    watchdog.start()
    unittest.main(module=None, argv=['run.py', *arguments(args)], testRunner=WatchdogRunner)


if __name__ == '__main__':
    main()
