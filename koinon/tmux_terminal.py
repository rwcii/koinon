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


def _now():
    return int(time.time() * 1000)


def observe_host(executable, start=None):
    """The nearest ancestor that is the configured Codex CLI itself."""
    start = os.getppid() if start is None else start
    try:
        pid = platform_support.ancestor_matching(
            start, lambda candidate: platform_support.codex_host(candidate, executable))
        started = platform_support.proc_start(pid) if pid is not None else None
    except (OSError, ValueError, subprocess.SubprocessError):
        pid = started = None
    if pid is None or started is None:
        return dict(state='unknown', reason='host_not_found', observed_at_ms=_now())
    return dict(state='observed', pid=pid, proc_start=started, observed_at_ms=_now())


def tmux(socket, *arguments):
    """Run one tmux command on an explicit server; None when it cannot answer."""
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
        return dict(state='unavailable', reason='host_not_found', observed_at_ms=_now())
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
