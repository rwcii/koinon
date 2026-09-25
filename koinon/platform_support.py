#!/usr/bin/env python3
"""Platform-specific process, credential, and socket primitives.

The bridge speaks one wire protocol on every platform, but the local facts it
relies on differ between Linux and macOS. This module is the only place that
knows those differences, so `bridge.py`, `notify.py`, and `session.py` stay
platform-neutral.

Three differences matter:

* **Peer identity.** Linux exposes `SO_PEERCRED`, one getsockopt returning the
  peer pid, uid, and gid. macOS has no such option: `getpeereid(3)` returns only
  uid and gid, and the peer pid comes from a separate `LOCAL_PEERPID` socket
  option. Both halves are required, so macOS needs two calls where Linux needs
  one.
* **Process start time.** Linux reads field 22 of `/proc/<pid>/stat`, a count of
  clock ticks since boot. macOS has no `/proc`; `ps -o lstart=` reports the
  start time as an asctime string. Claude Code writes `procStart` in UTC
  asctime form on macOS and as the tick count on Linux, so the value we publish
  and the value we compare against must match the local convention exactly.
* **Process namespace.** Linux peers identify themselves as
  `linux:<machine-id>:<pid-namespace>`. macOS Claude Code uses the literal
  string `darwin`.

Python standard library only.
"""
import asyncio
from concurrent.futures import ThreadPoolExecutor
import hashlib
import threading
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys

DARWIN = sys.platform == 'darwin'
LINUX = sys.platform.startswith('linux')

# Capability facts other modules consult instead of testing `sys.platform`
# themselves, so this module stays the only place that knows the differences.
SUPPORTED = DARWIN or LINUX
SERVICE_MANAGER = 'systemd' if LINUX else None


def installation_backend():
    """Choose a native artifact format without probing a service manager."""
    if LINUX:
        return 'systemd'
    if DARWIN:
        return 'launchd'
    raise ValueError('unsupported installation platform')


def memory_artifact_directory(prefix, backend):
    if backend == 'launchd':
        return account_home() / 'Library' / 'LaunchAgents'
    if backend in ('systemd', 'manual'):
        return Path(prefix) / 'service-artifacts' / 'memory'
    raise ValueError('unsupported installation backend')
# Explicit configuration/ownership refusals require operator action, not restart loops.
CONFIGURATION_EXIT_STATUS = 78
TEMPORARY_EXIT_STATUS = 75
SOFTWARE_EXIT_STATUS = 70
PERMANENT_EXIT_STATUSES = (SOFTWARE_EXIT_STATUS, CONFIGURATION_EXIT_STATUS)

if not SUPPORTED:
    raise RuntimeError(f'unsupported platform: {sys.platform}')

# macOS socket options. Python exposes neither, so they are spelled out here.
# SOL_LOCAL is 0 on Darwin; LOCAL_PEERPID returns the peer's pid as a C int.
SOL_LOCAL = 0
LOCAL_PEERPID = 0x002

# `sockaddr_un.sun_path`, including the terminating NUL. Keyed by DARWIN so the
# lookup reads as the platform question it is. The usable path is one byte less.
SUN_PATH_BYTES = {True: 104, False: 108}


def peer_pid(sock):
    """Kernel-verified PID of the process at the other end of a unix socket.

    The uid is verified before the pid is returned on both platforms: this
    bridge's authentication policy is same-user only, so a peer from another
    account is rejected rather than described.

    @param sock - a connected AF_UNIX socket.
    @returns the peer pid.
    @throws ValueError when the peer belongs to another user.
    @throws OSError when the kernel cannot report the peer.
    """
    if LINUX:
        import struct
        pid, uid, _ = struct.unpack('3i', sock.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid != os.getuid():
            raise ValueError('different user')
        return pid
    uid, _ = _getpeereid(sock.fileno())
    if uid != os.getuid():
        raise ValueError('different user')
    # A peer that connected and vanished before this call makes the option
    # unavailable. Fail closed: the caller treats an unusable credential as a
    # rejected connection.
    import struct
    return struct.unpack('i', sock.getsockopt(SOL_LOCAL, LOCAL_PEERPID, 4))[0]


def _getpeereid(fd):
    """Read uid and gid of a connected peer.

    Darwin's `getpeereid(3)` has no Python binding, so it is called through
    ctypes. It carries no pid; `peer_pid` combines it with LOCAL_PEERPID.
    """
    import ctypes
    libc = ctypes.CDLL(None, use_errno=True)
    uid = ctypes.c_uint()
    gid = ctypes.c_uint()
    if libc.getpeereid(ctypes.c_int(fd), ctypes.byref(uid), ctypes.byref(gid)) != 0:
        errno = ctypes.get_errno()
        raise OSError(errno, os.strerror(errno))
    return uid.value, gid.value


PROCESS_QUERY_TIMEOUT = 5
_PROCESS_PROBES = ThreadPoolExecutor(max_workers=2, thread_name_prefix='process-identity')
_PROCESS_PROBE_SLOTS = threading.BoundedSemaphore(2)


async def async_proc_start(pid):
    """Keep accepted OS probes bounded and off the service event loop."""
    if not _PROCESS_PROBE_SLOTS.acquire(blocking=False):
        raise BlockingIOError('process identity probe capacity reached')
    try:
        future = _PROCESS_PROBES.submit(proc_start, pid)
    except BaseException:
        _PROCESS_PROBE_SLOTS.release()
        raise
    # Release on actual completion, never merely because a caller stopped waiting.
    future.add_done_callback(lambda result: _PROCESS_PROBE_SLOTS.release())
    wrapped = asyncio.wrap_future(future)
    wrapped.add_done_callback(lambda result: None if result.cancelled() else result.exception())
    return await asyncio.shield(wrapped)


def proc_start(pid):
    """Local process-start marker for one pid, in this platform's own form.

    Used as a PID-reuse guard: a pid alone can be recycled, but the pair of pid
    and start time identifies one specific process. The value is also published
    in the peer registry, so it must use the form Claude Code expects on this
    platform.

    A pid that no longer exists raises `ProcessLookupError` on **both**
    platforms, so callers have one thing to catch when a process disappears
    mid-check rather than a platform-specific error each.

    @param pid - the process to describe.
    @returns the platform's start marker as a string.
    @throws ProcessLookupError when the process is gone.
    """
    if LINUX:
        try:
            return Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()[19]
        except FileNotFoundError as exc:
            raise ProcessLookupError(f'no such process: {pid}') from exc
    # `ps` reports local time by default while Claude writes UTC, so the zone is
    # forced rather than inherited. `lstart` uses asctime's space-padded day,
    # matching Claude's format; `normalize_start` absorbs any residual padding
    # difference so a comparison never fails on whitespace alone.
    try:
        result = subprocess.run(['ps', '-o', 'lstart=', '-p', str(pid)],
                                capture_output=True, text=True, check=True,
                                env={**os.environ, 'TZ': 'UTC'}, timeout=PROCESS_QUERY_TIMEOUT)
    except subprocess.CalledProcessError as exc:
        raise ProcessLookupError(f'no such process: {pid}') from exc
    value = result.stdout.strip()
    if not value:
        raise ProcessLookupError(f'no start time reported for pid {pid}')
    return value


def normalize_start(value):
    """Collapse whitespace so start markers compare independent of padding.

    asctime pads a single-digit day with a space (`Sep  3`); ps and Claude can
    disagree on that padding. Only whitespace is normalized — the remaining
    fields must still match exactly for the guard to hold.
    """
    return ' '.join(str(value).split())


def same_process(recorded, actual):
    """Whether a recorded start marker still describes the live process.

    A `None` record means the writer published no marker; the pid check alone
    then decides, which is the behavior older registry entries rely on.
    """
    return recorded is None or normalize_start(recorded) == normalize_start(actual)


def process_alive(pid):
    """Whether a pid is currently in use by a process this user may signal."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # Exists but belongs to another user; it cannot be our peer.
        return False
    return True


def process_state(pid, expected_start):
    """Observe one process incarnation without counting zombies as running.

    Unreadable or malformed observations remain unknown; only an absent PID,
    a different start marker, or an OS-reported dead state proves exit.
    """
    if not isinstance(expected_start, str) or not expected_start.strip():
        return 'unknown'
    try:
        if LINUX:
            fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
            state, marker = fields[0], fields[19]
        else:
            result = subprocess.run(
                ['ps', '-o', 'state=', '-o', 'lstart=', '-p', str(pid)],
                capture_output=True, text=True, env={**os.environ, 'TZ': 'UTC'},
                timeout=PROCESS_QUERY_TIMEOUT)
            if result.returncode:
                # A failed ps invocation alone does not prove process exit.
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    return 'dead'
                return 'unknown'
            state, marker = result.stdout.strip().split(maxsplit=1)
        if not state or not marker.strip():
            return 'unknown'
        if not same_process(expected_start, marker) or state[0] in ('Z', 'X'):
            return 'dead'
        return 'alive'
    except FileNotFoundError:
        if LINUX:
            return 'dead'
        return 'unknown'
    except (OSError, subprocess.SubprocessError, IndexError, ValueError):
        return 'unknown'


def pid_domain():
    """Peer-domain identifier recorded alongside this process in the registry.

    Linux peers disambiguate by machine id and pid namespace, so a pid is only
    meaningful within one of those. macOS Claude Code publishes the literal
    string `darwin` and performs no further namespacing.
    """
    if DARWIN:
        return 'darwin'
    return ('linux:' + Path('/etc/machine-id').read_text().strip() + ':'
            + os.readlink('/proc/self/ns/pid'))


def allowed_socket_dirs():
    """Directories a peer address may live in, canonicalized.

    These are the locations Claude Code uses for its peer sockets. macOS
    resolves `/tmp` to `/private/tmp`, so both forms are returned and callers
    compare resolved paths; the allowlist itself stays the literal, auditable
    list.
    """
    uid = os.getuid()
    dirs = [Path('/tmp/cc-socks'), Path(f'/tmp/cc-socks-{uid}')]
    if LINUX:
        dirs.append(Path(f'/run/user/{uid}/cc-socks'))
    resolved = set()
    for directory in dirs:
        resolved.add(directory.resolve())
    return resolved


def socket_mode_ok(info):
    """Whether a socket's metadata satisfies the private-socket requirement."""
    return stat.S_ISSOCK(info.st_mode) and info.st_uid == os.getuid() and not info.st_mode & 0o077


def control_socket_path(root):
    """Path for one instance's private control socket.

    AF_UNIX addresses are bounded by the kernel's `sun_path` field: 108 bytes on
    Linux and 104 on macOS, both including the terminating NUL, so the usable
    path is one byte shorter. An over-long `bind` fails with an opaque
    "AF_UNIX path too long" and the bridge simply never starts.

    The state directory can legitimately be deeper than that. A per-session
    state directory adds `sessions/<16 hex>`, and macOS puts temporary
    directories under a long `/var/folders/...` path, which is enough to exceed
    the limit. When the natural path does not fit, the socket is placed in the
    peer socket directory instead, under a name derived from the state
    directory, so it stays unique per instance and just as private: that
    directory is already required to be mode 0700 and owned by this user.

    The digest uses the **resolved** directory, so one state directory reached
    through two spellings (a symlinked parent, a relative path) still maps to one
    socket. A server and a later CLI invocation must agree on this path.

    The fallback lives in the shared socket directory rather than in the instance
    state directory, so cleanup does not own it. A path left behind by a killed
    instance is therefore removed by hand after verifying the old process is dead
    and its socket refuses connections, exactly as the project already requires for
    stale peer sockets. The owned session runner separately records verified child
    socket inodes and may remove an exact match after every owned process is proven
    dead; uncaptured and operator-installed endpoints keep this manual rule.
    Nothing in this path-selection function removes a socket automatically: a listening
    socket and a saturated one are indistinguishable by probing, because a full
    accept queue refuses a connection on macOS just as a dead owner does.
    """
    root = Path(root).resolve()
    direct = root / 'control.sock'
    if len(os.fsencode(direct)) < SUN_PATH_BYTES[DARWIN]:
        return direct
    return fallback_control_socket(root)


def fallback_control_socket(root):
    """The stable private fallback used by both old and new service code."""
    digest = hashlib.sha256(str(Path(root).resolve()).encode()).hexdigest()[:16]
    return Path('/tmp/cc-socks') / f'{digest}-control.sock'


def legacy_control_socket(root, recorded=None):
    """A bounded old direct endpoint; never a peer or registry address.

    An old server could select a short alias before checking the path length.
    Only that root's direct socket is a permitted compatibility destination.
    """
    candidate = Path(root) / 'control.sock' if recorded is None else recorded
    if not isinstance(candidate, (str, Path)):
        return None
    candidate = Path(candidate)
    try:
        if (candidate.is_absolute() and len(os.fsencode(candidate)) < SUN_PATH_BYTES[DARWIN]
                and candidate.resolve() == (Path(root).resolve() / 'control.sock')):
            return candidate
    except (OSError, RuntimeError, ValueError):
        pass
    return None


def same_control_socket(recorded, expected):
    """Compare private service endpoint identities, never peer token paths."""
    if not isinstance(recorded, str) or not Path(recorded).is_absolute():
        return False
    try:
        return Path(recorded).resolve() == Path(expected).resolve()
    except (OSError, RuntimeError, ValueError):
        return False


def refuse_legacy_control_conflict(root):
    """Never start a new endpoint beside either retained legacy endpoint."""
    canonical = control_socket_path(root).resolve()
    candidates = (Path(root).resolve() / 'control.sock', fallback_control_socket(root))
    for candidate in candidates:
        if candidate.resolve() == canonical:
            continue
        try:
            candidate.lstat()
        except FileNotFoundError:
            continue
        raise FileExistsError(f'legacy control endpoint remains at {candidate}; '
                              'stop its owner and verify it is gone before removing a leftover socket')


class AccountHomeUnavailable(RuntimeError):
    """The effective OS account has no usable persistent home directory."""


def account_home():
    """Read the OS account entry, never HOME or a provider-specific override."""
    import pwd
    try:
        value = pwd.getpwuid(os.geteuid()).pw_dir
        if not value or not Path(value).is_absolute() or not Path(value).is_dir():
            raise AccountHomeUnavailable('account_home_unavailable')
        return Path(value)
    except (KeyError, OSError, ValueError):
        raise AccountHomeUnavailable('account_home_unavailable') from None


def overflow_uid():
    """The owner a Linux user namespace shows for an unmapped uid, or None.

    Agent sandboxes that run in a user namespace show root-owned paths with this
    uid. macOS has no such mapping.
    """
    if not LINUX:
        return None
    try:
        return int(Path('/proc/sys/kernel/overflowuid').read_text())
    except (OSError, ValueError):
        return None


def participant_lock_dir():
    """Persistent singleton namespace shared by every state root of this account."""
    home = account_home()
    if DARWIN:
        return home / 'Library' / 'Application Support' / 'koinon-locks'
    return home / '.local' / 'state' / 'koinon-locks'


def sync_state_file(fd):
    """Flush one state file; request the stronger device flush on macOS.

    Fail visibly if the platform cannot honor the requested flush. Filesystem and
    device compliance remains an assumption, not a process-crash test result.
    """
    os.fsync(fd)
    if DARWIN:
        import fcntl
        operation = getattr(fcntl, 'F_FULLFSYNC', None)
        if operation is None:
            raise OSError('full state synchronization is unavailable')
        fcntl.fcntl(fd, operation)


def sync_state_directory(path):
    # O_NOFOLLOW preserves the refusal the memory artifact helper applied before it
    # delegated here. Every caller validates the directory first, so a state
    # directory is never legitimately reached through a symlink.
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def standalone_state_sync_source():
    """Freeze these primitives into recovery code that removes this module."""
    import inspect
    return ('import os\nDARWIN = ' + repr(DARWIN) + '\n' +
            inspect.getsource(sync_state_file) + '\n' +
            inspect.getsource(sync_state_directory))


def memory_service_command(prefix, python, key, selection):
    from koinon import memory_service_config
    from koinon.work_policy import absolute_path
    prefix, python = absolute_path(str(prefix)), absolute_path(str(python))
    memory_service_config.validate(dict(version=1, repositories={key: selection}))
    return [str(python), str(prefix / 'memory_service.py'), 'run',
            '--prefix', str(prefix), '--repo', selection['common_directory'],
            '--state-root', selection['state_root'], '--backend', selection['backend']]


def memory_service_artifact(prefix, python, key, selection):
    """Canonical staged artifact bytes; no manager selection or activation.

    This exact template is the ownership grammar. Unknown directives, arguments,
    duplicate plist keys, and alternative expansion syntax are not adopted.
    """
    import json
    import plistlib
    from koinon import memory_service_config
    from koinon import runtime_names
    from koinon.work_policy import absolute_path

    prefix, python = absolute_path(str(prefix)), absolute_path(str(python))
    memory_service_config.validate(dict(version=1, repositories={key: selection}))
    argv = memory_service_command(prefix, python, key, selection)
    backend = selection['backend']
    if backend == 'systemd':
        # Path validation above excludes control characters; do not accept arbitrary
        # JSON unicode escapes as systemd command syntax.
        version = selection.get('template_version', 1)
        def argument(value):
            escaped = value.replace('%', '%%')
            if version == 1:
                escaped = escaped.replace('$', '$$')
            return json.dumps(escaped, ensure_ascii=False)
        text = (runtime_names.SERVICE_MARKER + f'# Memory service template v{version}\n[Unit]\nDescription=Koinon repository memory\n\n'
                '[Service]\nType=simple\nExecStart=' + (':' if version == 2 else '') + ' '.join(map(argument, argv)) +
                '\nRestart=on-failure\nRestartSec=10\nRestartPreventExitStatus=' +
                ' '.join(map(str, PERMANENT_EXIT_STATUSES)) +
                '\nUMask=0077\n\n[Install]\nWantedBy=default.target\n')
        return text.encode('utf-8')
    if backend == 'launchd':
        label = memory_service_config.artifact_name(key, backend)[:-len('.plist')]
        return plistlib.dumps(dict(Label=label, ProgramArguments=argv,
                                   KoinonManaged='memory-service-v1', Umask=63,
                                   RunAtLoad=True, ThrottleInterval=10,
                                   KeepAlive=dict(SuccessfulExit=False)), sort_keys=True)
    raise ValueError('manual operation has no service artifact')



def session_launchd_artifact(prefix, python, thread, repository, *, domain, agent='codex', model=None):
    """Render a staged session job; never select, publish or activate a manager.

    The session refusal boundary and loaded-job ownership join must be implemented
    before callers may activate this artifact. Existing Linux rendering is unchanged.
    """
    import plistlib
    import re
    from koinon.work_policy import absolute_path

    if domain != f'gui/{os.geteuid()}':
        raise ValueError('launchd domain must belong to the current user')
    if not isinstance(thread, str) or re.fullmatch(r'[A-Za-z0-9_-]{1,128}', thread) is None:
        raise ValueError('valid explicit participant identity required')
    if agent not in ('codex', 'deepseek'):
        raise ValueError('unsupported session participant')
    if model is not None and (not isinstance(model, str) or not model
                              or len(model.encode('utf-8')) > 512
                              or any(ord(c) < 32 or 127 <= ord(c) <= 159 for c in model)):
        raise ValueError('invalid session model')
    prefix, python, repository = (absolute_path(str(value)) for value in (prefix, python, repository))
    key = hashlib.sha256(thread.encode()).hexdigest()[:16]
    # Equals forms preserve values that begin with a dash without shell evaluation.
    argv = [str(python), str(prefix / 'session.py'), 'run', '--thread=' + thread,
            '--repo', str(repository)]
    if agent != 'codex':
        argv += ['--agent', agent]
    if model is not None:
        argv += ['--model=' + model]
    return plistlib.dumps(dict(Label='io.github.rwcii.koinon.session.' + key,
                               ProgramArguments=argv, KoinonManaged='session-service-v1',
                               Umask=63, RunAtLoad=True, ThrottleInterval=10,
                               KeepAlive=dict(SuccessfulExit=False)), sort_keys=True)


def session_service_command(record):
    from koinon import session_service_config
    session_service_config.validate(record)
    return [record['python'], str(Path(record['prefix']) / 'session_service.py'), 'run',
            '--prefix', record['prefix'], '--state-dir', record['state_directory'],
            '--backend', record['backend']]


def session_service_artifact(record):
    """Render a saved native selection for the owned pair runner; no manager calls."""
    import json
    import plistlib
    from koinon import session_service_config
    session_service_config.validate(record)
    argv = session_service_command(record)
    if record['backend'] == 'systemd':
        from koinon import runtime_names
        escaped = [json.dumps(value.replace('%', '%%'), ensure_ascii=False) for value in argv]
        return (runtime_names.SERVICE_MARKER + '# Native session service template v1\n'
                '[Unit]\nDescription=Koinon owned session supervisor\n\n'
                '[Service]\nType=simple\nExecStart=:' + ' '.join(escaped) +
                '\nRestart=on-failure\nRestartSec=5\nRestartPreventExitStatus=' +
                ' '.join(map(str, PERMANENT_EXIT_STATUSES)) + '\nUMask=0077\n').encode()
    if record['backend'] == 'launchd':
        if record['manager_domain'] != f'gui/{os.geteuid()}':
            raise ValueError('launchd domain must belong to the current user')
        label = session_service_config.artifact_name(record['session_key'], 'launchd')[:-6]
        return plistlib.dumps(dict(Label=label, ProgramArguments=argv, KoinonManaged='owned-session-v1',
                                   Umask=63, RunAtLoad=True, ThrottleInterval=10,
                                   KeepAlive=dict(SuccessfulExit=False)), sort_keys=True)
    raise ValueError('manual session has no native artifact')



MANAGER_GUARD_VARIABLE = 'KOINON_TEST_MANAGER_GUARD'
_REAL_RUN = subprocess.run
# Read once at import as well, so a test that clears os.environ does not lift the guard.
_MANAGER_GUARDED = os.environ.get(MANAGER_GUARD_VARIABLE) == '1'


class RealManagerCall(BaseException):
    """A guarded test run reached the real user service manager.

    It derives from BaseException so that no manager-unavailable handler turns it into an
    ordinary refusal; the test fails with the name of the unpatched call.
    """


def lift_manager_guard():
    """Opt a native job that drives the real user manager on purpose out of the guard."""
    global _MANAGER_GUARDED
    _MANAGER_GUARDED = False
    os.environ.pop(MANAGER_GUARD_VARIABLE, None)


def _manager_run(caller, argv, **options):
    """Run one user-manager command, unless the test runner's guard refuses it.

    `tests/run.py` sets the guard for the whole run, children included. A test that clears
    the environment keeps it, because it is also read at import. A test passes the
    guard by patching the caller or `subprocess.run`, or by placing a stub executable under
    the temporary directory ahead of the real one on PATH.
    """
    guarded = _MANAGER_GUARDED or os.environ.get(MANAGER_GUARD_VARIABLE) == '1'
    if guarded and subprocess.run is _REAL_RUN:
        import shutil
        import tempfile
        found = shutil.which(argv[0])
        stub = found is not None and Path(found).resolve().is_relative_to(
            Path(tempfile.gettempdir()).resolve())
        if found is not None and not stub:
            raise RealManagerCall(f'{caller} reached the real service manager ({argv[0]}); '
                                  f'patch platform_support.{caller} or subprocess.run in this test')
    return subprocess.run(argv, **options)


def user_service_manager(operation, names=(), **options):
    """Run one supported user-manager operation, preserving caller I/O policy.

    This first abstraction retains the shipped systemd commands. Unsupported
    managers raise OSError so existing availability probes report manual operation.
    It does not start an alternate manager or change retry/readiness behavior.
    """
    names = tuple(names)
    commands = {
        'available': ['show-environment'],
        'reload': ['daemon-reload'],
        'start': ['start'],
        'stop': ['stop'],
        'enable': ['enable', '--now'],
        'disable': ['disable', '--now'],
        'fragment': ['show', *names, '--property=FragmentPath', '--value'],
        'observe': ['show', '--property=Id', '--property=ActiveState',
                    '--property=FragmentPath', *names],
    }
    if operation not in commands:
        raise ValueError('unsupported user service operation')
    if SERVICE_MANAGER != 'systemd':
        raise OSError('no supported user service manager')
    arguments = commands[operation]
    if operation in ('start', 'stop', 'enable', 'disable'):
        arguments = [*arguments, *names]
    return _manager_run('user_service_manager', ['systemctl', '--user', *arguments], **options)


def managed_service_exit(backend, status):
    """launchd's binary restart predicate requires permanent failures to exit zero.

    Original failure and refusal-record durability remain visible in runner status.
    This mapping is required even when writing the refusal marker failed.
    """
    if backend == 'launchd' and status in PERMANENT_EXIT_STATUSES:
        return 0
    return status


def systemd_service_observation(name):
    """Read one user unit through typed D-Bus properties, without activation.

    Missing tools, unsupported methods, malformed data, and observations that
    change during the query remain unknown. Callers still compare the returned
    artifact and argv with their owned registration and live runner handshake.
    """
    import json
    import re

    if not isinstance(name, str) or re.fullmatch(r'[A-Za-z0-9_.@-]+\.service', name) is None:
        raise ValueError('invalid service name')
    if not LINUX:
        return dict(status='unknown', reason='backend_unavailable')
    bus = ['busctl', '--user', '--auto-start=no', '--allow-interactive-authorization=no',
           '--timeout=5', '--json=short']
    destination = 'org.freedesktop.systemd1'

    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate manager response key')
            result[key] = value
        return result

    def query(arguments, signatures):
        result = _manager_run('systemd_service_observation', bus + arguments, capture_output=True,
                              text=True, check=True, timeout=PROCESS_QUERY_TIMEOUT + 1)
        if len(result.stdout) > 65536:
            raise ValueError('manager observation too large')
        rows = result.stdout.splitlines()
        if len(rows) != len(signatures):
            raise ValueError('unexpected manager response count')
        values = []
        for row, signature in zip(rows, signatures):
            value = json.loads(row, object_pairs_hook=pairs)
            if not isinstance(value, dict) or set(value) != {'type', 'data'} or value['type'] != signature:
                raise ValueError('unexpected manager response type')
            values.append(value['data'])
        return values

    def properties(path, interface, names, signatures):
        return query(['get-property', destination, path, interface, *names], signatures)

    try:
        listed, = query(['call', destination, '/org/freedesktop/systemd1', destination + '.Manager',
                         'ListUnitsByNames', 'as', '1', name], ['a(ssssssouso)'])
        if (not isinstance(listed, list) or len(listed) != 1 or not isinstance(listed[0], list)
                or len(listed[0]) != 1 or not isinstance(listed[0][0], list) or len(listed[0][0]) != 10):
            raise ValueError('unexpected unit listing')
        unit = listed[0][0]
        if unit[0] != name:
            raise ValueError('unit listing identity mismatch')
        if unit[2:5] == ['not-found', 'inactive', 'dead']:
            return dict(status='absent')
        path = unit[6]
        if not isinstance(path, str) or not path.startswith('/org/freedesktop/systemd1/unit/'):
            raise ValueError('invalid unit object path')
        unit_names = ['Id', 'FragmentPath', 'LoadState', 'ActiveState', 'InvocationID']
        unit_types = ['s', 's', 's', 's', 'ay']
        service_names, service_types = ['ExecStart', 'MainPID'], ['a(sasbttttuii)', 'u']
        before = properties(path, destination + '.Unit', unit_names, unit_types)
        service = properties(path, destination + '.Service', service_names, service_types)
        after = properties(path, destination + '.Unit', unit_names, unit_types)
        service_after = properties(path, destination + '.Service', service_names, service_types)
        if before != after or service != service_after:
            return dict(status='unknown', reason='observation_changed')
        identity, artifact, loaded, active, invocation = before
        executions, pid = service
        # A removed failed unit can retain a manager tombstone. Establish absence
        # only from stable typed empty artifact/command and zero live PID evidence.
        if (identity == name and loaded == 'not-found' and artifact == ''
                and active in ('inactive', 'failed') and type(pid) is int and pid == 0
                and executions == []):
            return dict(status='absent')
        if (identity != name or not isinstance(artifact, str) or not Path(artifact).is_absolute()
                or loaded != 'loaded' or not isinstance(active, str)
                or not isinstance(invocation, list) or len(invocation) not in (0, 16)
                or any(type(x) is not int or not 0 <= x <= 255 for x in invocation)
                or type(pid) is not int or pid < 0
                or not isinstance(executions, list) or len(executions) != 1
                or not isinstance(executions[0], list) or len(executions[0]) != 10):
            raise ValueError('incomplete unit ownership evidence')
        executable, argv = executions[0][:2]
        if (not isinstance(executable, str) or not Path(executable).is_absolute()
                or not isinstance(argv, list) or not argv or not all(isinstance(x, str) for x in argv)):
            raise ValueError('invalid loaded executable arguments')
        return dict(status='observed', artifact=artifact, executable=executable, argv=argv,
                    pid=pid, active_state=active, invocation=bytes(invocation).hex())
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError, IndexError, RecursionError):
        return dict(status='unknown', reason='manager_observation_unavailable')


def _launchd_print_identity(text, target):
    """Parse only the native shape verified by the isolated macOS fixture.

    launchctl print is not a stable API. Ambiguity or shape changes refuse an
    observation; these fields never replace byte ownership or runner handshakes.
    """
    import re
    lines = text.splitlines()
    if not lines or lines[0] != target + ' = {' or lines[-1] != '}':
        raise ValueError('unexpected launchd job envelope')
    fields = {}
    arguments = None
    in_arguments = False
    for line in lines[1:-1]:
        if in_arguments:
            if line == '\t}':
                in_arguments = False
                continue
            if not line.startswith('\t\t'):
                raise ValueError('unexpected launchd argument boundary')
            argument = line[2:]
            if any(ord(c) < 32 or 127 <= ord(c) <= 159 for c in argument):
                raise ValueError('ambiguous launchd argument')
            arguments.append(argument)
            continue
        match = re.match(r'^\t(path|program|arguments|state|pid)\b', line)
        if not match:
            continue
        key = match.group(1)
        prefix = '\t' + key + ' = '
        if not line.startswith(prefix) or key in fields:
            raise ValueError('ambiguous launchd identity field')
        value = line[len(prefix):]
        fields[key] = value
        if key == 'arguments':
            if value != '{':
                raise ValueError('unexpected launchd arguments shape')
            arguments, in_arguments = [], True
    if in_arguments or not {'path', 'program', 'arguments', 'state'}.issubset(fields):
        raise ValueError('incomplete launchd ownership evidence')
    if (not Path(fields['path']).is_absolute() or not Path(fields['program']).is_absolute()
            or not arguments or not fields['state']
            or any(ord(c) < 32 or 127 <= ord(c) <= 159
                   for key in ('path', 'program', 'state') for c in fields[key])):
        raise ValueError('invalid launchd ownership evidence')
    if 'pid' in fields:
        if not re.fullmatch(r'[1-9][0-9]*', fields['pid']):
            raise ValueError('invalid launchd process identity')
        pid = int(fields['pid'])
    elif fields['state'] in ('not running', 'waiting'):
        pid = 0
    else:
        raise ValueError('missing launchd process identity')
    return dict(status='observed', artifact=fields['path'], executable=fields['program'],
                argv=arguments, pid=pid, active_state=fields['state'])


def launchd_service_observation(domain, label):
    """Observe exactly one job in the explicitly selected current-user GUI domain."""
    import re
    if domain != f'gui/{os.geteuid()}':
        raise ValueError('launchd domain must belong to the current user')
    if not isinstance(label, str) or re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*', label) is None:
        raise ValueError('invalid launchd job label')
    if not DARWIN:
        return dict(status='unknown', reason='backend_unavailable')
    target = domain + '/' + label

    def query(selected):
        result = _manager_run('launchd_service_observation', ['launchctl', 'print', selected],
                              capture_output=True, text=True, timeout=PROCESS_QUERY_TIMEOUT)
        if len(result.stdout) > 65536:
            raise ValueError('manager observation too large')
        return result

    try:
        before = query(target)
        if before.returncode == 113:
            # Missing domain and missing job share an error code. An absent job
            # is established only while its selected domain is queryable.
            # Domain output includes unrelated jobs and may exceed the bounded
            # selected-job response. Only its success status is needed; never
            # capture or parse that unrelated domain inventory.
            if memory_manager_available('launchd', domain):
                return dict(status='absent')
            return dict(status='unknown', reason='domain_unavailable')
        if before.returncode != 0:
            return dict(status='unknown', reason='manager_observation_unavailable')
        first = _launchd_print_identity(before.stdout, target)
        after = query(target)
        if after.returncode != 0 or first != _launchd_print_identity(after.stdout, target):
            return dict(status='unknown', reason='observation_changed')
        return first
    except (OSError, subprocess.SubprocessError, ValueError, TypeError):
        return dict(status='unknown', reason='manager_observation_unavailable')


def memory_manager_available(backend, domain=None):
    """Probe only the explicitly selected user manager, without activation."""
    if backend == 'launchd':
        if domain != f'gui/{os.geteuid()}':
            raise ValueError('launchd domain must belong to the current user')
        if not DARWIN:
            return False
        argv = ['launchctl', 'print', domain]
    elif backend == 'systemd':
        if not LINUX:
            return False
        argv = ['systemctl', '--user', '--no-ask-password', 'show', '--property=Version', '--value']
    else:
        raise ValueError('unsupported memory manager backend')
    try:
        return _manager_run('memory_manager_available', argv, stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL, timeout=PROCESS_QUERY_TIMEOUT).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False




def systemd_registration_layout():
    """Recognize the native user manager's standard UnitPath layout, or refuse.

    Use manager evidence, not the caller's XDG environment. An overridden or changed
    layout is unsupported rather than guessed. No environment inventory is read.
    """
    import json
    from koinon.work_policy import absolute_path
    if not LINUX:
        raise OSError('selected memory manager unavailable')
    command = ['busctl', '--user', '--auto-start=no', '--allow-interactive-authorization=no',
               '--timeout=5', '--json=short', 'get-property', 'org.freedesktop.systemd1',
               '/org/freedesktop/systemd1', 'org.freedesktop.systemd1.Manager', 'UnitPath']
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate manager response key')
            result[key] = value
        return result
    def query():
        reply = _manager_run('systemd_registration_layout', command, capture_output=True, text=True,
                             check=True, timeout=PROCESS_QUERY_TIMEOUT + 1)
        if len(reply.stdout) > 65536:
            raise ValueError('manager paths exceed observation bound')
        value = json.loads(reply.stdout, object_pairs_hook=pairs)
        if (not isinstance(value, dict) or set(value) != {'type', 'data'} or value['type'] != 'as'
                or not isinstance(value['data'], list) or not 6 <= len(value['data']) <= 128):
            raise ValueError('unsupported manager path response')
        paths = [absolute_path(path) for path in value['data']]
        if len(set(paths)) != len(paths):
            raise ValueError('duplicate manager paths')
        return paths
    try:
        paths = query()
        if query() != paths:
            raise ValueError('manager paths changed')
        control, runtime_control, transient, early, persistent = paths[:5]
        runtime = runtime_control.with_name('user')
        if (control.name != 'user.control' or control.parent.name != 'systemd'
                or runtime_control.name != 'user.control' or runtime_control.parent.name != 'systemd'
                or persistent != control.with_name('user') or runtime not in paths
                or transient != runtime_control.with_name('transient')
                or early != runtime_control.with_name('generator.early')):
            raise ValueError('unsupported manager path layout')
        return persistent, runtime, tuple(paths)
    except (ValueError, TypeError, subprocess.SubprocessError) as exc:
        raise OSError('manager registration paths unavailable') from exc


def systemd_registration_directories():
    persistent, runtime, _ = systemd_registration_layout()
    return persistent, runtime


def session_registration_preflight(record):
    """Refuse known higher-priority shadows before creating a runtime loader link."""
    from koinon import memory_service_artifacts
    from koinon import session_service_config
    session_service_config.validate(record)
    if record['backend'] != 'systemd':
        raise ValueError('systemd session selection required')
    _, runtime, paths = systemd_registration_layout()
    name = session_service_config.artifact_name(record['session_key'], 'systemd')
    for directory in paths[:paths.index(runtime)]:
        candidate = directory / name
        try:
            candidate.lstat()
        except FileNotFoundError:
            continue
        # Even a matching link in an unselected loader directory is not ours.
        raise memory_service_artifacts.RegistrationPathError(candidate)
    memory_service_artifacts.preflight_registration(record, (runtime / name,))
    return runtime / name


def memory_registration_paths(record, *, runtime=False):
    if type(runtime) is not bool:
        raise ValueError('runtime selection must be boolean')
    persistent, temporary = systemd_registration_directories()
    directory = temporary if runtime else persistent
    name = Path(record['artifact']).name
    return (directory / name, directory / 'default.target.wants' / name)


def memory_manager_deregister(record):
    """Remove only verified literal registration links for a stopped memory job."""
    from koinon import memory_service_artifacts as artifacts
    if record['backend'] == 'launchd':
        return memory_manager_action(record, 'deactivate')
    if record['backend'] != 'systemd' or not LINUX:
        raise ValueError('native memory registration required')
    paths = memory_registration_paths(record)
    artifacts.preflight_registration(record, paths)
    identities = {}
    for path in paths:
        try:
            info = path.lstat()
        except FileNotFoundError:
            continue
        artifacts.verify_loader_link(path, record['artifact'])
        identities[path] = (info.st_dev, info.st_ino)
    # Check the whole selected set before removing any link. An interrupted
    # removal accepts missing links on retry, but never replaces changed links.
    for path, identity in identities.items():
        artifacts.verify_loader_link(path, record['artifact'])
        info = path.lstat()
        if (info.st_dev, info.st_ino) != identity:
            raise artifacts.RegistrationPathError(path)
    for path, identity in identities.items():
        artifacts.verify_loader_link(path, record['artifact'])
        info = path.lstat()
        if (info.st_dev, info.st_ino) != identity:
            raise artifacts.RegistrationPathError(path)
        path.unlink()
        sync_state_directory(path.parent)
    return _manager_run('memory_manager_deregister', ['systemctl', '--user', '--no-ask-password',
                        'daemon-reload'], capture_output=True, text=True, check=True, timeout=15)

def memory_manager_action(record, operation, *, runtime=False):
    """Execute a preflighted owned action; callers supply ownership verification."""
    from koinon import memory_service_config
    key, _ = memory_service_config.identity(record['common_directory'])
    memory_service_config.validate(dict(version=1, repositories={key: record}))
    if type(runtime) is not bool:
        raise ValueError('runtime selection must be boolean')
    if operation not in ('register', 'activate', 'restart', 'deactivate'):
        raise ValueError('unsupported memory manager operation')
    backend = record['backend']
    if backend == 'systemd':
        if not LINUX:
            raise OSError('selected memory manager unavailable')
        name = memory_service_config.artifact_name(key, backend)
        if operation in ('register', 'activate', 'restart'):
            from koinon import memory_service_artifacts
            memory_service_artifacts.preflight_registration(
                record, memory_registration_paths(record, runtime=runtime))
        commands = {'register': ['link', record['artifact']],
                    'activate': ['enable', record['artifact']],
                    'restart': ['start', name], 'deactivate': ['disable', name]}
        argv = ['systemctl', '--user', '--no-ask-password',
                *(['--runtime'] if runtime else []), *commands[operation]]
    elif backend == 'launchd':
        if operation == 'register':
            raise ValueError('launchd registration is part of activation')
        domain = record.get('manager_domain')
        if domain != f'gui/{os.geteuid()}':
            raise ValueError('launchd domain must belong to the current user')
        if not DARWIN:
            raise OSError('selected memory manager unavailable')
        label = memory_service_config.artifact_name(key, backend)[:-len('.plist')]
        commands = {'activate': ['bootstrap', domain, record['artifact']],
                    'restart': ['kickstart', domain + '/' + label],
                    'deactivate': ['bootout', domain + '/' + label]}
        argv = ['launchctl', *commands[operation]]
    else:
        raise ValueError('manual selection has no manager operation')
    return _manager_run('memory_manager_action', argv, capture_output=True, text=True, check=True, timeout=15)

def session_manager_observation(record):
    from koinon import session_service_config
    session_service_config.validate(record)
    name = session_service_config.artifact_name(record['session_key'], record['backend'])
    if record['backend'] == 'systemd':
        return systemd_service_observation(name)
    if record['backend'] == 'launchd':
        return launchd_service_observation(record['manager_domain'], name[:-6])
    return dict(status='unknown', reason='manual_selection')


def session_manager_action(record, operation):
    """Selected session jobs last for this user-manager lifetime, never login-enabled."""
    from koinon import session_service_config
    session_service_config.validate(record)
    if operation not in ('register', 'start'):
        raise ValueError('unsupported session manager action')
    name = session_service_config.artifact_name(record['session_key'], record['backend'])
    if record['backend'] == 'systemd':
        if not LINUX:
            raise OSError('selected session manager unavailable')
        if operation in ('register', 'start'):
            session_registration_preflight(record)
        arguments = {'register': ['link', record['artifact']], 'start': ['start', name]}[operation]
        argv = ['systemctl', '--user', '--no-ask-password', '--runtime', *arguments]
    elif record['backend'] == 'launchd':
        domain = record['manager_domain']
        if not DARWIN or domain != f'gui/{os.geteuid()}':
            raise OSError('selected session manager unavailable')
        label = name[:-6]
        arguments = {'register': ['bootstrap', domain, record['artifact']],
                     'start': ['kickstart', domain + '/' + label]}[operation]
        argv = ['launchctl', *arguments]
    else:
        raise ValueError('manual selection has no manager action')
    return _manager_run('session_manager_action', argv, capture_output=True, text=True, check=True, timeout=15)



def session_manager_deactivate(record):
    """Remove only the selected stopped job registration, preserving its artifact."""
    from koinon import session_service_config
    from koinon import memory_service_artifacts
    session_service_config.validate(record)
    name = session_service_config.artifact_name(record['session_key'], record['backend'])
    if record['backend'] == 'systemd':
        if not LINUX:
            raise OSError('selected session manager unavailable')
        path = session_registration_preflight(record)
        before = path.lstat()
        if (not stat.S_ISLNK(before.st_mode) or before.st_uid != os.geteuid()
                or os.readlink(path) != record['artifact']):
            raise memory_service_artifacts.RegistrationPathError(path)
        after = path.lstat()
        if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino):
            raise memory_service_artifacts.RegistrationPathError(path)
        path.unlink()
        sync_state_directory(path.parent)
        argv = ['systemctl', '--user', '--no-ask-password', 'daemon-reload']
    else:
        domain = record['manager_domain']
        if not DARWIN or domain != f'gui/{os.geteuid()}':
            raise OSError('selected session manager unavailable')
        argv = ['launchctl', 'bootout', domain + '/' + name[:-6]]
    return _manager_run('session_manager_deactivate', argv, capture_output=True, text=True, check=True, timeout=15)


OS_DEFINITION_ROOT = '/System/Library/LaunchAgents'


def upgrade_os_definition(path):
    """Is this an operating-system launchd definition, which is not parsed as a file?

    The job itself stays a same-user GUI observation and its command is still
    inspected; only the vendor's definition file is left unread. Both the literal
    and the resolved path must lie inside the OS directory, so a link or a
    traversal that leaves it cannot borrow the exclusion.
    """
    root = Path(OS_DEFINITION_ROOT)
    candidate = Path(path)
    if not candidate.is_absolute() or root not in candidate.parents:
        return False
    try:
        resolved, actual = candidate.resolve(), root.resolve()
    except OSError:
        return False
    return root in resolved.parents or actual in resolved.parents


def upgrade_service_sources(prefix):
    """Read native user definition locations, including loaded custom artifacts.

    Output contains paths and scope only. No service is loaded, started or changed.
    A missing manager is explicit; an available manager with unparseable inventory
    is an error, never evidence that it has no external services.
    """
    import re
    home = account_home()
    references, os_definitions = [], []
    def query(argv):
        result = _manager_run('upgrade_service_sources', argv, capture_output=True, text=True,
                              timeout=PROCESS_QUERY_TIMEOUT + 1)
        if len(result.stdout.encode()) > 1024 * 1024:
            raise ValueError('native service discovery exceeds response capacity')
        return result
    if LINUX:
        if memory_manager_available('systemd'):
            _, _, roots = systemd_registration_layout()
            result = query(['systemctl', '--user', '--no-pager', '--no-ask-password', 'show',
                            '*.service', '--all', '--property=Id,FragmentPath,ExecStart,LoadState'])
            if result.returncode:
                raise ValueError('loaded systemd service discovery unavailable')
            loaded, references = _upgrade_systemd_definitions(result.stdout, prefix)
            status = 'observed_systemd_user_manager'
        else:
            config = Path(os.environ.get('XDG_CONFIG_HOME', str(home / '.config')))
            runtime = Path(os.environ.get('XDG_RUNTIME_DIR', f'/run/user/{os.geteuid()}'))
            roots = (config / 'systemd/user', home / '.config/systemd/user',
                     runtime / 'systemd/user', runtime / 'systemd/transient',
                     Path('/etc/systemd/user'), Path('/usr/local/lib/systemd/user'),
                     Path('/usr/lib/systemd/user'))
            loaded, status = [], 'manager_unavailable_static_paths_only'
    elif DARWIN:
        roots = (home / 'Library/LaunchAgents', Path('/Library/LaunchAgents'))
        domain = f'gui/{os.geteuid()}'
        if memory_manager_available('launchd', domain):
            result = query(['launchctl', 'print', domain])
            if result.returncode:
                raise ValueError('loaded launchd service discovery unavailable')
            labels = _upgrade_launchd_labels(result.stdout, domain)
            loaded = []
            for label in labels:
                job = query(['launchctl', 'print', domain + '/' + label])
                if job.returncode:
                    raise ValueError('launchd service disappeared during discovery')
                # Built-in jobs may have no plist. Preserve only explicit paths;
                # never parse nested environment values as service identity.
                paths = re.findall(r'^\tpath = (.+)$', job.stdout, re.MULTILINE)
                if len(paths) > 1 or any(not Path(path).is_absolute() for path in paths):
                    raise ValueError('invalid loaded launchd definition path')
                # Application and submitted jobs can name an executable or bundle
                # rather than a plist. Inspect their command, never read a binary
                # as if it were a service definition.
                # An OS-provided definition is not read as an external service file.
                # The job stays in scope: its command is inspected below exactly as
                # any other, so a direct runtime reference still becomes a finding.
                for path in paths:
                    if Path(path).suffix not in ('.plist', '.service'):
                        continue
                    if upgrade_os_definition(path):
                        os_definitions.append(path)
                    else:
                        loaded.append(path)
                reference = _upgrade_loaded_reference(label, paths[0] if paths else None,
                    paths + _upgrade_launchd_command(job.stdout), prefix)
                if reference is not None:
                    references.append(reference)
            status = 'observed_launchd_gui_domain'
        else:
            loaded, status = [], 'gui_domain_unavailable_static_paths_only'
    else:
        raise ValueError('service discovery platform unavailable')
    paths = sorted(set(map(str, roots)))
    if any(not Path(path).is_absolute() for path in paths):
        raise ValueError('service discovery requires absolute native search paths')
    return dict(directories=paths, loaded_artifacts=sorted(set(loaded)),
                loaded_references=references, loaded_discovery=status,
                os_definitions=[dict(path=value, reason='os_definition_not_parsed')
                                for value in sorted(set(os_definitions))])


def _upgrade_launchd_labels(text, domain):
    """Accept one bounded services table from an explicit launchd GUI domain.

    The native print interface is not stable. Unsupported shapes refuse instead
    of being interpreted as an empty inventory. Only labels leave this parser.
    """
    import re
    lines = text.splitlines()
    if not lines or lines[0] != domain + ' = {' or lines[-1] != '}':
        raise ValueError('unexpected launchd domain envelope')
    starts = [index for index, line in enumerate(lines) if line == '\tservices = {']
    if len(starts) != 1:
        raise ValueError('launchd service inventory is unavailable')
    labels = []
    for line in lines[starts[0] + 1:]:
        if line == '\t}':
            if len(labels) != len(set(labels)):
                raise ValueError('duplicate launchd service identity')
            return sorted(labels)
        # A label is arbitrary text and may contain spaces, so take the row remainder
        # verbatim instead of splitting it. Refusing those labels would make one
        # unrelated third-party job refuse every upgrade on the host. A label is only
        # ever passed as a single argv element, never to a shell.
        columns = line[2:].split(None, 2)
        label = columns[2].rstrip() if len(columns) == 3 else ''
        if (not line.startswith('\t\t') or len(columns) != 3 or not label
                or not label.isprintable()
                or re.fullmatch(r'(?:0|[1-9][0-9]*|-)', columns[0]) is None
                or re.fullmatch(r'(?:[0-9]+|[A-Za-z-]+)', columns[1]) is None):
            raise ValueError('unsupported launchd service inventory shape')
        labels.append(label)
        if len(labels) > 4096:
            raise ValueError('launchd service inventory exceeds capacity')
    raise ValueError('unterminated launchd service inventory')

def upgrade_memory_processes(prefix):
    """Observe direct same-user memory runner argv without retaining arguments.

    This is inventory, not process ownership or permission to signal a PID. The
    coordinator joins returned start markers to saved supervisor/child records.
    """
    import re
    scripts = {str(Path(root) / name): kind
               for root in (str(prefix), str(Path(prefix).resolve()))
               for name, kind in (('memory.py', 'memory'), ('memory_service.py', 'memory_supervisor'))}
    def selected(arguments):
        for script, kind in scripts.items():
            if isinstance(arguments, list):
                if script not in arguments:
                    continue
                rest = arguments[arguments.index(script) + 1:]
                match = ('serve' if kind == 'memory' else 'run') in rest
            else:
                suffix = 'serve' if kind == 'memory' else 'run'
                match = re.search(re.escape(script) + r'(?:\s|$).*\b' + suffix + r'\b', arguments) is not None
            if match:
                return kind
        return None
    rows = []
    if LINUX:
        with os.scandir('/proc') as entries:
            for count, entry in enumerate(entries):
                if count >= 65536:
                    raise ValueError('process discovery exceeds capacity')
                if not entry.name.isdecimal():
                    continue
                try:
                    if entry.stat(follow_symlinks=False).st_uid != os.geteuid():
                        continue
                    pid = int(entry.name)
                    before = proc_start(pid)
                    with (Path(entry.path) / 'cmdline').open('rb') as stream:
                        raw = stream.read(65537)
                    if len(raw) > 65536:
                        raise ValueError('process arguments exceed discovery capacity')
                    arguments = [os.fsdecode(value) for value in raw.split(b'\0') if value]
                    kind = selected(arguments)
                    if kind is not None:
                        if not same_process(before, proc_start(pid)):
                            raise ValueError('memory process changed during discovery')
                        rows.append(dict(pid=pid, proc_start=before, kind=kind))
                except (FileNotFoundError, ProcessLookupError):
                    continue
    elif DARWIN:
        result = subprocess.run(['ps', '-axww', '-o', 'uid=,pid=,command='], capture_output=True,
                                text=True, timeout=PROCESS_QUERY_TIMEOUT)
        if result.returncode or len(result.stdout.encode()) > 16 * 1024 * 1024:
            raise ValueError('same-user process discovery unavailable')
        for count, line in enumerate(result.stdout.splitlines()):
            if count >= 65536:
                raise ValueError('process discovery exceeds capacity')
            fields = line.split(None, 2)
            if len(fields) != 3 or not fields[0].isdecimal() or not fields[1].isdecimal():
                raise ValueError('invalid process discovery response')
            if int(fields[0]) != os.geteuid():
                continue
            kind = selected(fields[2])
            if kind is not None:
                pid = int(fields[1])
                try:
                    rows.append(dict(pid=pid, proc_start=proc_start(pid), kind=kind))
                except ProcessLookupError:
                    continue
    else:
        raise ValueError('process discovery platform unavailable')
    return sorted(rows, key=lambda item: item['pid'])


def _upgrade_loaded_reference(identity, artifact, values, prefix):
    import json
    import re
    text = '\n'.join(values).replace('%%', '%').replace('$$', '$')
    text = re.sub(r'\\x([0-9a-fA-F]{2})', lambda m: chr(int(m[1], 16)), text)
    candidates = {str(prefix), str(Path(prefix).resolve())}
    candidates.update(json.dumps(value, ensure_ascii=False)[1:-1] for value in tuple(candidates))
    if not any(value + '/' in text for value in candidates):
        return None
    return dict(identity=identity, artifact=artifact,
                command_sha256=hashlib.sha256(json.dumps(values, ensure_ascii=True).encode()).hexdigest())


def _upgrade_systemd_definitions(text, prefix):
    paths, references = [], []
    blocks = text.strip().split('\n\n') if text.strip() else []
    if len(blocks) > 4096:
        raise ValueError('loaded systemd inventory exceeds capacity')
    for block in blocks:
        fields = {}
        for line in block.splitlines():
            key, delimiter, value = line.partition('=')
            if not delimiter or key in fields or key not in ('Id', 'FragmentPath', 'ExecStart', 'LoadState'):
                raise ValueError('invalid loaded systemd inventory')
            fields[key] = value
        if not {'Id', 'FragmentPath', 'LoadState'} <= set(fields) or not fields['Id'].endswith('.service'):
            raise ValueError('incomplete loaded systemd inventory')
        if fields['LoadState'] == 'not-found' and not fields['FragmentPath'] and not fields.get('ExecStart'):
            continue
        if 'ExecStart' not in fields:
            raise ValueError('loaded systemd service has no command observation')
        path = fields['FragmentPath']
        if path:
            if not Path(path).is_absolute():
                raise ValueError('invalid loaded systemd definition path')
            paths.append(path)
        reference = _upgrade_loaded_reference(fields['Id'], path or None, [fields['ExecStart']], prefix)
        if reference is not None:
            references.append(reference)
    return paths, references


def _upgrade_launchd_command(text):
    commands, arguments = [], False
    for line in text.splitlines():
        if arguments:
            if line == '\t}':
                arguments = False
            elif line.startswith('\t\t'):
                commands.append(line[2:])
            else:
                raise ValueError('invalid loaded launchd argument boundary')
        elif line == '\targuments = {':
            arguments = True
        elif line.startswith('\tprogram = '):
            commands.append(line[len('\tprogram = '):])
    if arguments:
        raise ValueError('unterminated loaded launchd arguments')
    return commands


def holds_open(pid, path):
    """Whether this same-user process currently holds the selected file open."""
    try:
        if LINUX:
            root = Path('/proc') / str(pid)
            if root.stat().st_uid != os.geteuid():
                return False
            wanted = os.stat(path)
            for fd in (root / 'fd').iterdir():
                try:
                    found = fd.stat()
                    if (found.st_dev, found.st_ino) == (wanted.st_dev, wanted.st_ino):
                        return True
                except OSError:
                    continue
            return False
        result = subprocess.run(['lsof', '-a', '-p', str(pid), '-u', str(os.geteuid()),
                                 '-t', '--', str(path)], capture_output=True, text=True,
                                timeout=PROCESS_QUERY_TIMEOUT)
        return result.returncode == 0 and str(pid) in result.stdout.split()
    except (OSError, subprocess.SubprocessError):
        return False


def open_file_holders(path):
    """Find same-user holders; an incomplete probe is not proof of a unique owner."""
    if LINUX:
        import time
        deadline = time.monotonic() + PROCESS_QUERY_TIMEOUT
        result = []
        for entry in Path('/proc').iterdir():
            if time.monotonic() > deadline:
                raise TimeoutError('file-holder observation timed out')
            if entry.name.isdigit() and holds_open(int(entry.name), path):
                result.append(int(entry.name))
        return result
    result = subprocess.run(['lsof', '-a', '-u', str(os.geteuid()), '-t', '--', str(path)],
                            capture_output=True, text=True, timeout=PROCESS_QUERY_TIMEOUT)
    if result.returncode not in (0, 1):
        raise OSError('file-holder observation failed')
    return sorted({int(value) for value in result.stdout.split() if value.isdigit()})


def process_command(pid):
    """Return parent PID and argv for a same-user process, for CLI wrapper matching."""
    import shlex
    if LINUX:
        root = Path('/proc') / str(pid)
        if root.stat().st_uid != os.geteuid():
            raise ProcessLookupError('different process owner')
        parent = int((root / 'stat').read_text().rsplit(')', 1)[1].split()[1])
        argv = (root / 'cmdline').read_bytes().split(b'\0')
        return parent, [os.fsdecode(value) for value in argv if value]
    result = subprocess.run(['ps', '-ww', '-o', 'uid=,ppid=,command=', '-p', str(pid)],
                            capture_output=True, text=True, check=True, timeout=PROCESS_QUERY_TIMEOUT)
    uid, parent, command = result.stdout.strip().split(None, 2)
    if int(uid) != os.geteuid():
        raise ProcessLookupError('different process owner')
    return int(parent), shlex.split(command)


def ancestor_matching(pid, predicate, limit=64):
    """The first of pid and its ancestors that predicate accepts, or None.

    The walk stops at pid 1, at a process owned by another user, and after limit
    steps, so a malformed or cyclic parent chain cannot run unbounded.
    """
    for _ in range(limit):
        if pid <= 1:
            return None
        if predicate(pid):
            return pid
        try:
            parent, _ = process_command(pid)
        except (OSError, ValueError, subprocess.SubprocessError):
            return None
        if parent == pid:
            return None
        pid = parent
    return None


def process_parents():
    """Map pid to parent pid for this user's processes, or None when the scan is incomplete.

    A process that exits during the scan is skipped; any other unreadable own process, or a
    failed or malformed `ps`, makes the whole snapshot unavailable, so a caller never mistakes
    a partial scan for the absence of a process.
    """
    parents = {}
    if LINUX:
        uid = os.geteuid()
        try:
            entries = list(Path('/proc').iterdir())
        except OSError:
            return None
        for entry in entries:
            if not entry.name.isdigit():
                continue
            try:
                if entry.stat().st_uid != uid:
                    continue
                parents[int(entry.name)] = int((entry / 'stat').read_text().rsplit(')', 1)[1].split()[1])
            except (FileNotFoundError, ProcessLookupError):
                continue
            except (OSError, ValueError, IndexError):
                return None
        return parents
    try:
        result = subprocess.run(['ps', '-A', '-o', 'pid=,ppid=,uid='], capture_output=True, text=True,
                                check=True, timeout=PROCESS_QUERY_TIMEOUT)
    except (OSError, subprocess.SubprocessError):
        return None
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) != 3 or not all(field.isdigit() for field in fields):
            return None
        if int(fields[2]) == os.geteuid():
            parents[int(fields[0])] = int(fields[1])
    return parents


def descendants(pid, parents=None):
    """pid and every process below it, from one parent-map snapshot."""
    parents = process_parents() if parents is None else parents
    if parents is None:
        raise OSError('process snapshot unavailable')
    children = {}
    for child, parent in parents.items():
        children.setdefault(parent, []).append(child)
    found, pending = [], [pid]
    while pending and len(found) < 4096:
        current = pending.pop()
        if current in found:
            continue
        found.append(current)
        pending.extend(children.get(current, ()))
    return found


def _selected_executable(executable):
    import shutil
    selected = shutil.which(executable)
    return Path(selected).resolve() if selected else None


def _launches(argv, selected):
    # Script launchers name their script as argv[1]; never search prompt arguments.
    candidates = argv[:1]
    if argv and Path(argv[0]).name in ('node', 'nodejs', 'python3', 'python'):
        candidates = argv[:2]
    return any(Path(value).is_absolute() and Path(value).resolve() == selected for value in candidates)


def _is_executable(pid, argv, selected):
    if _launches(argv, selected):
        return True
    if LINUX and Path(f'/proc/{pid}/exe').resolve() == selected:
        return True
    if DARWIN:
        native = subprocess.run(['ps', '-ww', '-o', 'comm=', '-p', str(pid)],
                                capture_output=True, text=True,
                                timeout=PROCESS_QUERY_TIMEOUT).stdout.strip()
        if native and Path(native).is_absolute() and Path(native).resolve() == selected:
            return True
    return False


def codex_process(pid, executable):
    """Match the configured native CLI or a direct child of its interpreter wrapper."""
    selected = _selected_executable(executable)
    if not selected:
        return False
    try:
        parent, argv = process_command(pid)
        if _is_executable(pid, argv, selected):
            return True
        _, parent_argv = process_command(parent)
        return _launches(parent_argv, selected)
    except (OSError, ValueError, subprocess.SubprocessError):
        return False


def codex_host(pid, executable):
    """Match only the configured CLI process itself, never a child it started.

    `codex_process` also accepts a child of a launcher, which would match a shell
    that the CLI started; the host of a session must be the CLI.
    """
    selected = _selected_executable(executable)
    if not selected:
        return False
    try:
        _, argv = process_command(pid)
        return _is_executable(pid, argv, selected)
    except (OSError, ValueError, subprocess.SubprocessError):
        return False
