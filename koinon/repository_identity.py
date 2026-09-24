"""Lightweight Git repository identity shared by memory and status writers."""
import hashlib
from pathlib import Path
import subprocess


def repo_common_directory(start=None):
    """Canonical repository key: the Git common directory, absolute, hashed.

    The bare `--git-common-dir` prints a path relative to the working directory, so
    two conventional checkouts both report `.git` and would collide. The absolute
    form is required, and it is what makes every worktree of one repository share a
    single memory service.
    """
    try:
        out = subprocess.run(['git', 'rev-parse', '--path-format=absolute', '--git-common-dir'],
                             cwd=str(start or Path.cwd()), capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError) as exc:
        raise ValueError('cannot resolve the repository') from exc
    path = out.stdout.strip()
    if out.returncode or not path or not Path(path).is_absolute():
        raise ValueError('not inside a Git repository, or Git is too old for --path-format')
    return Path(path).resolve()


def repo_identity(start=None):
    return hashlib.sha256(str(repo_common_directory(start)).encode()).hexdigest()[:16]
