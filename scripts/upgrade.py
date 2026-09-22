#!/usr/bin/env python3
"""Dispatch explicit upgrade selection from a complete source checkout."""
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
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from koinon.upgrade_command import main

if __name__ == '__main__':
    raise SystemExit(main())
