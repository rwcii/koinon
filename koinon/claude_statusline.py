"""Set up, decline and remove the Koinon status-line wrapper in the user's Claude settings.

Installation makes `statusline.py` the Claude Code `statusLine` command, so that a Claude
session's model and context reach `bridge.py peers` (`docs/DELIVERY.md`). Only the
`statusLine` entry of `<claude config dir>/settings.json` changes; every other setting and
every other field of that entry is kept. The previous entry is saved once in `install.json`
under `claude_statusline` and restored on removal while the entry is still the wrapper.

The settings file is shared with Claude Code, which takes no Koinon lock. A write therefore
compares the file with what was read immediately before replacing it and again afterwards;
a difference is reported as a conflict and the user's content is kept. A Claude Code write
between the last comparison and the replacement cannot be detected, which is why the
documentation asks the user not to change Claude settings during installation or removal.
"""
import json
import os
from pathlib import Path
import shlex
import stat

KEY = 'claude_statusline'
MAX_BYTES = 1024 * 1024


class SettingsError(ValueError):
    def __init__(self, code, message, path=None):
        super().__init__(message)
        self.code, self.path = code, None if path is None else str(path)


def config_directory():
    return Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude')))


def settings_path(directory=None):
    return Path(directory or config_directory()) / 'settings.json'


def wrapper_path(prefix):
    return Path(prefix) / 'statusline.py'


def _command(entry):
    if isinstance(entry, dict) and entry.get('type') == 'command' and isinstance(entry.get('command'), str):
        return entry['command']
    return None


def is_wrapper(entry, prefix):
    """Whether an entry runs this installation's wrapper."""
    command = _command(entry)
    if not command:
        return False
    try:
        words = shlex.split(command)
    except ValueError:
        return False
    return len(words) >= 2 and words[1] == str(wrapper_path(prefix))


def wrapper_entry(original, prefix, python):
    """The `statusLine` entry that runs the wrapper around the original command, if any."""
    words = [str(python), str(wrapper_path(prefix))]
    user = _command(original)
    if user:
        words += ['--command', user]
    entry = {k: v for k, v in original.items() if k not in ('type', 'command')} if isinstance(original, dict) else {}
    return dict(entry, type='command', command=shlex.join(words))


def repair_command(prefix, python):
    return shlex.join([str(python), str(Path(prefix) / 'scripts' / 'install.py'),
                       '--prefix', str(prefix), '--claude-statusline'])


def _read(path):
    """Return `(raw bytes or None, settings object)`; refuse anything unsafe to replace."""
    try:
        info = path.lstat()
    except FileNotFoundError:
        return None, {}
    if stat.S_ISLNK(info.st_mode):
        raise SettingsError('settings_symlink', 'Claude settings file is a symbolic link; not changed', path)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_size > MAX_BYTES:
        raise SettingsError('settings_unsafe', 'Claude settings file is not a regular file owned by this user', path)
    raw = path.read_bytes()
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeError, RecursionError):
        raise SettingsError('settings_invalid', 'Claude settings file is not valid JSON; not changed', path) from None
    if not isinstance(value, dict):
        raise SettingsError('settings_invalid', 'Claude settings file is not a JSON object; not changed', path)
    return raw, value


def _current(path):
    try:
        return path.read_bytes()
    except FileNotFoundError:
        return None


def _write(path, expected, settings):
    """Replace the file atomically unless it changed since it was read; report a later change."""
    data = (json.dumps(settings, indent=2) + '\n').encode()
    mode = 0o600
    if expected is not None:
        mode = stat.S_IMODE(path.stat().st_mode)
    temp = path.with_name('.settings.json.koinon')
    temp.unlink(missing_ok=True)
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as stream:
            fd = None
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        if _current(path) != expected:
            raise SettingsError('settings_conflict', 'Claude settings changed while Koinon was editing '
                                'them; the other change was kept', path)
        os.replace(temp, path)
    finally:
        if fd is not None:
            os.close(fd)
        temp.unlink(missing_ok=True)
    directory = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
    if _current(path) != data:
        raise SettingsError('settings_conflict', 'Claude settings changed right after Koinon wrote '
                            'them; the other change was kept', path)


def _result(action, outcome, path, **extra):
    return dict(action=action, outcome=outcome, settings_file=str(path), **extra)


def set_up(state, prefix, python, *, explicit=False, directory=None):
    """Make the wrapper the `statusLine` command, saving the previous entry once.

    Without `explicit`, a saved decline is kept and an entry that the user changed after
    set-up is left alone and reported. `explicit` is the user's request to set it up now:
    it reverses a decline and wraps whatever entry is current.
    """
    path = settings_path(directory)
    record = state.config.get(KEY)
    if not path.parent.is_dir():
        return _result('set_up', 'skipped', path, reason='claude_config_missing')
    if record and record.get('state') == 'declined' and not explicit:
        return _result('set_up', 'declined', path)
    raw, settings = _read(path)
    current = settings.get('statusLine')
    ours = is_wrapper(current, prefix)
    if record and record.get('state') in ('enabled', 'pending') and not explicit:
        if not ours and current != record.get('original'):
            # The user replaced or removed the wrapper after set-up; keep their choice.
            return _result('set_up', 'changed', path, reason='statusline_changed',
                           repair=repair_command(prefix, python))
        original = record.get('original')
    elif ours:
        # Never wrap the wrapper, and keep the original that was saved when it was set up.
        original = (record or {}).get('original')
    else:
        original = current
    entry = wrapper_entry(original, prefix, python)
    if entry == current:
        state.merge({KEY: dict(state='enabled', settings_file=str(path), original=original, wrapper=entry)})
        return _result('set_up', 'unchanged', path)
    # Save the original before the settings change, so a crash can never lose it.
    state.merge({KEY: dict(state='pending', settings_file=str(path), original=original, wrapper=entry)})
    _write(path, raw, dict(settings, statusLine=entry))
    state.merge({KEY: dict(state='enabled', settings_file=str(path), original=original, wrapper=entry)})
    return _result('set_up', 'set_up', path)


def decline(state, prefix, directory=None):
    """Record that the user declines the wrapper; remove it if it is set up."""
    record = state.config.get(KEY)
    if record and record.get('state') in ('enabled', 'pending'):
        return remove(state, prefix, directory=directory)
    path = settings_path(directory)
    state.merge({KEY: dict(state='declined', settings_file=str(path), original=None, wrapper=None)})
    return _result('decline', 'declined', path)


def remove(state, prefix, directory=None):
    """Restore the saved entry while the wrapper is still in place, then record a decline."""
    record = state.config.get(KEY) or {}
    path = Path(record.get('settings_file') or settings_path(directory))
    declined = {KEY: dict(state='declined', settings_file=str(path), original=None, wrapper=None)}
    if record.get('state') not in ('enabled', 'pending'):
        state.merge(declined)
        return _result('remove', 'not_set_up', path)
    raw, settings = _read(path)
    current = settings.get('statusLine')
    if not is_wrapper(current, prefix):
        state.merge(declined)
        return _result('remove', 'changed', path, reason='statusline_changed',
                       action_required='The statusLine entry no longer runs this installation; '
                                       'it was kept unchanged.')
    restored = dict(settings)
    if record.get('original') is None:
        restored.pop('statusLine', None)
    else:
        restored['statusLine'] = record['original']
    _write(path, raw, restored)
    state.merge(declined)
    return _result('remove', 'restored', path)


def missing(prefix, directory=None):
    """Whether the wrapper is absent from the settings that a Claude peer reads."""
    try:
        _, settings = _read(settings_path(directory))
    except (OSError, SettingsError):
        return True
    return not is_wrapper(settings.get('statusLine'), prefix)
