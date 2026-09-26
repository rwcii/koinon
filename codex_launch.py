#!/usr/bin/env python3
"""Start the configured Codex CLI so that `session.py ensure` finds this session's own terminal.

Usage: codex_launch.py [--tmux-session NAME --directory DIR] [--] [CODEX ARGUMENTS...]
       codex_launch.py --help         (Codex's own help: codex_launch.py -- --help)

Without --tmux-session the launcher replaces itself with Codex in the current terminal,
keeping its process ID, and passes that ID to every command of the session as
KOINON_CODEX_HOST. With --tmux-session it starts itself that way in a new detached tmux
session, so a Claude or Codex agent can start a peer; it prints the session and pane.
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

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

from koinon import runtime_names
from koinon import tmux_terminal

OPTIONS = ('--tmux-session', '--directory')


def split(arguments):
    """The launcher's own leading options, and the arguments that Codex receives unchanged."""
    options = {}
    while arguments and arguments[0] in OPTIONS:
        if len(arguments) < 2:
            raise ValueError(f'{arguments[0]} needs a value')
        options[arguments[0]] = arguments[1]
        arguments = arguments[2:]
    if arguments[:1] == ['--']:
        arguments = arguments[1:]
    return options, arguments


def command(executable, pid, arguments):
    """The Codex command line; the one override also keeps the CLI off a shared app-server."""
    return [executable, '-c', f'shell_environment_policy.set.{tmux_terminal.HOST_VARIABLE}="{pid}"',
            *arguments]


def start_in_tmux(name, directory, arguments):
    """Start this launcher in a new detached tmux session; never reuse or rename one.

    The session is a sibling on the server of this command's environment, or on the user's
    default server. It is never attached, so tmux is never nested in a pane.
    """
    socket = tmux_terminal.default_socket()
    if tmux_terminal.tmux(socket, 'has-session', '-t', '=' + name) is not None:
        return dict(ok=False, code='name_taken', session=name)
    launch = [sys.executable, str(Path(__file__).resolve()), '--', *arguments]
    output = tmux_terminal.tmux(socket, 'new-session', '-d', '-P', '-F', '#{session_id}\t#{pane_id}',
                                '-s', name, '-c', str(directory), *launch)
    fields = output.rstrip('\n').split('\t') if output else []
    if len(fields) != 2:
        return dict(ok=False, code='tmux_unavailable', session=name,
                    error='tmux did not start the session; inside an agent sandbox run this '
                          'through the agent\'s approval request')
    return dict(ok=True, session=name, session_id=fields[0], pane_id=fields[1], socket=socket)


def main():
    if sys.argv[1:2] in (['--help'], ['-h']):
        print(__doc__.strip())
        return
    try:
        options, arguments = split(sys.argv[1:])
    except ValueError as exc:
        raise SystemExit(f'codex_launch.py: {exc}')
    try:
        config = runtime_names.install_config(Path(__file__).resolve().parent) or {}
    except runtime_names.NameConflict as exc:
        raise SystemExit(f'codex_launch.py: {exc.code}: {", ".join(map(str, exc.paths))}')
    # A source checkout has no install.json; it uses the codex on PATH.
    executable = shutil.which(config.get('codex') or 'codex')
    if not executable:
        raise SystemExit('codex_launch.py: the configured Codex CLI was not found; see docs/INSTALL.md')
    if '--tmux-session' in options:
        directory = Path(options.get('--directory', os.getcwd())).expanduser()
        if not directory.is_dir():
            raise SystemExit(f'codex_launch.py: {directory} is not a directory')
        result = start_in_tmux(options['--tmux-session'], directory.resolve(), arguments)
        print(json.dumps(result))
        raise SystemExit(0 if result['ok'] else 1)
    if '--directory' in options:
        os.chdir(Path(options['--directory']).expanduser())
    os.execv(executable, command(executable, os.getpid(), arguments))


if __name__ == '__main__':
    main()
