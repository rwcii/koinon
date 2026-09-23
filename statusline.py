#!/usr/bin/env python3
"""Claude Code status-line wrapper: record model and context use, then run the user's command.

Claude Code runs the `statusLine` command through a shell on each update and passes session
data as JSON on standard input. This wrapper reads that input once, runs the user's own
status-line command with the same bytes, returns its output and exit status unchanged, and
meanwhile records the model ID, context limit and tokens used in a content-free status record
(`koinon/participant_status.py`). Nothing else from the input is read or stored. The user's
command runs even when recording fails.
"""
# Bytecode guard (docs/INSTALL.md). It runs before the first
# project import and uses only the standard library, because a shared helper would
# itself load from the cache it must judge. It keeps the canonical text below, which
# tests/test_bytecode_guard.py compares across every entrypoint.
if __name__ == '__main__':
    import os as _os, stat as _stat, sys as _sys, tempfile as _tempfile
    _os.umask(0o077)
    _here = _os.path.dirname(_os.path.abspath(__file__))
    _script = _os.path.basename(_here) == 'scripts'
    _root = _os.path.dirname(_here) if _script else _here

    def _owned(info, kind):
        return kind(info.st_mode) and info.st_uid == _os.geteuid() and not info.st_mode & 0o022

    def _trusted(path):
        try:
            info = _os.lstat(path)
        except FileNotFoundError:
            return True
        if not _owned(info, _stat.S_ISDIR):
            return False
        with _os.scandir(path) as entries:
            return all(_owned(entry.stat(follow_symlinks=False), _stat.S_ISREG)
                       and entry.stat(follow_symlinks=False).st_nlink == 1 for entry in entries)

    if _script or not all(_trusted(_os.path.join(_root, part, '__pycache__'))
                            for part in ('', 'koinon', 'scripts')):
        _sys.pycache_prefix = _tempfile.mkdtemp(prefix='koinon-pycache-')
        _sys.dont_write_bytecode = True
        import atexit as _atexit
        _atexit.register(lambda path=_sys.pycache_prefix: _os.path.isdir(path) and _os.rmdir(path))
# End of bytecode guard.
import os
import signal
import subprocess
import sys
import time

MAX_PARSED = 1024 * 1024
SOURCE = 'claude_statusline'


def fields(data):
    """The allowlisted status groups from one status-line input, or None."""
    import json
    value = json.loads(data)
    session = value.get('session_id')
    model = value.get('model') or {}
    window = value.get('context_window') or {}
    now = int(time.time() * 1000)
    groups = {}
    if isinstance(model.get('id'), str) and model['id']:
        groups['model'] = dict(source=SOURCE, recorded_at_ms=now, reason=None, id=model['id'])
    limit, used = window.get('context_window_size'), window.get('total_input_tokens')
    available = window.get('current_usage') is not None
    if type(limit) is int and type(used) is int:
        groups['context'] = dict(source=SOURCE, recorded_at_ms=now, reason=None, limit_tokens=limit,
                                 used_tokens=used, usage_available=available)
    elif 'context_window' in value:
        groups['context'] = dict(source=SOURCE, recorded_at_ms=now, reason='no_token_usage')
    return session, groups


def record(data):
    """Write this session's status record; any failure leaves the record as it was."""
    if len(data) > MAX_PARSED:
        return
    from koinon import participant_status
    session, groups = fields(data)
    participant = participant_status.claude_process(session)
    if participant is None or not groups:
        return
    participant_status.write('claude', session, participant=participant, groups=groups)


USAGE = 'usage: statusline.py [--command COMMAND]\n'


def main(argv):
    # One option, parsed by hand: this runs on every status-line update, and argparse
    # costs more start-up time than the rest of the wrapper.
    if argv in (['-h'], ['--help']):
        sys.stdout.write(USAGE + __doc__)
        return 0
    if argv in ([], ) or (len(argv) == 2 and argv[0] == '--command'):
        command = argv[1] if argv else None
    else:
        sys.stderr.write(USAGE)
        return 2
    data = sys.stdin.buffer.read()
    child = None
    if command:
        # Claude Code runs a status-line command as `/bin/sh -c <command>`; do the same, so
        # expansions, pipes and quoting keep their meaning. Output and errors go straight
        # to the inherited descriptors.
        child = subprocess.Popen(['/bin/sh', '-c', command], stdin=subprocess.PIPE)

        def forward(number, frame):
            child.send_signal(number)
        for number in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
            signal.signal(number, forward)
        try:
            child.stdin.write(data)
        except BrokenPipeError:
            pass
        finally:
            try:
                child.stdin.close()
            except BrokenPipeError:
                pass
    try:
        record(data)
    except Exception:
        # Recording is advisory; the user's status line never depends on it.
        pass
    if child is None:
        return 0
    status = child.wait()
    return 128 - status if status < 0 else status


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
