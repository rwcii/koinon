"""Observe the tmux pane and the host Codex process of one participant session.

`session.py ensure` runs outside the agent sandbox, where the host process and the
tmux socket are visible; inside the sandbox both observations fail and are reported,
never guessed. The records name a process and a pane, never a thread or a peer.
"""
import os
from pathlib import Path
import shutil
import subprocess
import time

from koinon import durable_state
from koinon import platform_support

TIMEOUT = 5
HOST = 'host.json'
TERMINAL = 'terminal.json'
# Set by tests/run.py for the whole run. Read once at import as well, so a test that clears
# os.environ does not lift the guard.
TMUX_GUARD_VARIABLE = 'KOINON_TEST_TMUX_GUARD'
AMBIENT_TMUX_VARIABLE = 'KOINON_TEST_AMBIENT_TMUX'
_TMUX_GUARDED = os.environ.get(TMUX_GUARD_VARIABLE) == '1'
_AMBIENT_TMUX = os.environ.get(AMBIENT_TMUX_VARIABLE, '')


class RealTmuxCall(BaseException):
    """A guarded test run reached a tmux server that is not a private test server.

    It derives from BaseException so that no tmux-unreadable handler turns it into an
    ordinary result; the test fails with the socket it reached.
    """


def _guard(socket):
    if not (_TMUX_GUARDED or os.environ.get(TMUX_GUARD_VARIABLE) == '1'):
        return
    ambient = _AMBIENT_TMUX or os.environ.get(AMBIENT_TMUX_VARIABLE, '')
    path = Path(socket)
    # tmux keeps the sockets of servers started without -S in tmux-<uid> under TMUX_TMPDIR.
    if path.parent.name == f'tmux-{os.getuid()}' or ambient and path == Path(ambient):
        raise RealTmuxCall(f'a test reached the tmux server at {socket}; use a private server '
                           'under a temporary directory')


def _now():
    return int(time.time() * 1000)


def observe_host(executable, start=None):
    """The nearest ancestor that is the configured Codex CLI itself.

    A command that runs under a Codex app-server has that server's CLI as its ancestor,
    and the server can serve the threads of other CLIs too. Such a host is `host_shared`:
    it is never recorded, and the terminal inherited from it is never used.
    """
    start = os.getppid() if start is None else start
    shared = []

    def host(candidate):
        if platform_support.codex_app_server(candidate):
            shared.append(candidate)
            return False
        return platform_support.codex_host(candidate, executable)
    try:
        pid = platform_support.ancestor_matching(start, host)
        started = platform_support.proc_start(pid) if pid is not None and not shared else None
    except (OSError, ValueError, subprocess.SubprocessError):
        pid = started = None
    if shared:
        return dict(state='unknown', reason='host_shared', observed_at_ms=_now())
    if pid is None or started is None:
        return dict(state='unknown', reason='host_not_found', observed_at_ms=_now())
    return dict(state='observed', pid=pid, proc_start=started, observed_at_ms=_now())


def tmux(socket, *arguments):
    """Run one tmux command on an explicit server; None when it cannot answer."""
    _guard(socket)
    executable = shutil.which('tmux')
    if executable is None:
        return None
    try:
        result = subprocess.run([executable, '-S', socket, *arguments], capture_output=True,
                                text=True, timeout=TIMEOUT)
    except (OSError, subprocess.SubprocessError):
        return None
    return result.stdout if result.returncode == 0 else None


def observe_terminal(host, environ=None):
    """The tmux pane this command runs in, accepted only when it contains the host."""
    environ = os.environ if environ is None else environ
    value, pane = environ.get('TMUX'), environ.get('TMUX_PANE')
    if not value or not pane:
        return dict(state='unavailable', reason='not_in_tmux', observed_at_ms=_now())
    if shutil.which('tmux') is None:
        return dict(state='unavailable', reason='tmux_unavailable', observed_at_ms=_now())
    socket = value.split(',', 1)[0]
    output = tmux(socket, 'display-message', '-p', '-t', pane, '#{pane_id}\t#{pane_pid}\t#{session_id}')
    fields = output.rstrip('\n').split('\t') if output else []
    if len(fields) != 3 or not fields[1].isdigit():
        return dict(state='unavailable', reason='tmux_unreadable', observed_at_ms=_now())
    pane_id, pane_pid, session_id = fields[0], int(fields[1]), fields[2]
    if host.get('state') != 'observed':
        # A shared host's pane is another session's; the reason keeps that visible.
        reason = 'host_shared' if host.get('reason') == 'host_shared' else 'host_not_found'
        return dict(state='unavailable', reason=reason, observed_at_ms=_now())
    if platform_support.ancestor_matching(host['pid'], lambda candidate: candidate == pane_pid) is None:
        return dict(state='unavailable', reason='pane_not_host', observed_at_ms=_now())
    return dict(state='observed', socket=socket, pane_id=pane_id, session_id=session_id,
                observed_at_ms=_now())


def record(state, executable, environ=None):
    """Observe and publish both records for one Codex `ensure`; returns them."""
    host = observe_host(executable)
    terminal = observe_terminal(host, environ)
    durable_state.publish(Path(state) / HOST, host)
    durable_state.publish(Path(state) / TERMINAL, terminal)
    return dict(host=host, terminal=terminal)


def report(state):
    """The recorded host and terminal, with the host's liveness observed now."""
    found = {}
    for key, name in (('host', HOST), ('terminal', TERMINAL)):
        try:
            value = durable_state.read(Path(state) / name)
        except (OSError, ValueError):
            value = None
        found[key] = value if isinstance(value, dict) else dict(state='unknown', reason='not_recorded')
    host = found['host']
    if host.get('state') == 'observed':
        try:
            live = (platform_support.process_alive(host['pid'])
                    and platform_support.same_process(host['proc_start'], platform_support.proc_start(host['pid'])))
        except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
            live = False
        found['host'] = dict(host, live=bool(live))
    return found


def agent_panes(terminal, executable, claude_pids):
    """The panes of the recorded tmux session whose process tree holds an agent.

    An agent is a Codex CLI process (`platform_support.codex_process`) or a live Claude
    registry process. Returns None when the session cannot be listed or the process scan is
    incomplete.
    """
    output = tmux(terminal['socket'], 'list-panes', '-s', '-t', terminal['session_id'],
                  '-F', '#{pane_id}\t#{pane_pid}')
    if output is None:
        return None
    parents = platform_support.process_parents()
    if parents is None:
        # Another agent pane cannot be ruled out; never rename the session on that basis.
        return None
    panes = []
    for line in output.splitlines():
        fields = line.split('\t')
        if len(fields) != 2 or not fields[1].isdigit():
            continue
        tree = platform_support.descendants(int(fields[1]), parents)
        if any(pid in claude_pids or platform_support.codex_process(pid, executable) for pid in tree):
            panes.append(fields[0])
    return panes


def name_terminal(terminal, name, executable, claude_pids):
    """Name the agent's own tmux session, or its own pane in a shared session, after name.

    Only the recorded session and pane are ever targeted, by their IDs. Returns the
    result for the `tmux` report field.
    """
    if terminal.get('state') != 'observed':
        return dict(result=terminal.get('reason', 'not_recorded'))
    socket, session_id, pane_id = terminal['socket'], terminal['session_id'], terminal['pane_id']
    panes = agent_panes(terminal, executable, claude_pids)
    if panes is None:
        return dict(result='panes_unknown')
    if len([pane for pane in panes if pane != pane_id]) > 0:
        if tmux(socket, 'select-pane', '-t', pane_id, '-T', name) is None:
            return dict(result='tmux_unreadable')
        return dict(result='pane_titled', name=name, pane_id=pane_id)
    current = tmux(socket, 'display-message', '-p', '-t', pane_id, '#{session_name}')
    if current is None:
        return dict(result='tmux_unreadable')
    if current.rstrip('\n') == name:
        return dict(result='unchanged', name=name)
    if tmux(socket, 'has-session', '-t', '=' + name) is not None:
        return dict(result='name_taken', name=name)
    tmux(socket, 'rename-session', '-t', session_id, name)
    after = tmux(socket, 'display-message', '-p', '-t', pane_id, '#{session_name}')
    if after is None or after.rstrip('\n') != name:
        return dict(result='rename_unconfirmed', name=name)
    return dict(result='renamed', name=name)
