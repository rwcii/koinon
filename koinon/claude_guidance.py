"""Set up, decline and remove the Koinon guidance for Claude Code sessions.

Two entries point a Claude session at the guide in the installed runtime:

- a managed block in `<claude config dir>/CLAUDE.md`, with the same bootstrap contract as the
  Codex and DeepSeek blocks, written through `participant_instructions` with its own markers,
  locking, backup and drift report; and
- one `SessionStart` command hook in `<claude config dir>/settings.json` that prints
  `session.py guide --agent claude --brief` at startup, resume, `/clear` and compaction.

The hook is a POSIX `sh` command, so it still exits 0 with a short message after the runtime
is removed. It is found by its exact command; every other hook and setting is kept. The
settings file is edited with the reading, conflict detection and atomic replacement of the
status-line integration (`claude_statusline`). Removal takes out only entries that are still
exactly what Koinon wrote; an entry the user edited is kept and reported.
"""
import hashlib
import json
from pathlib import Path
import shlex

from koinon import claude_statusline, participant_instructions
from koinon.claude_statusline import SettingsError

KEY = 'claude_guidance'
EVENT = 'SessionStart'
TIMEOUT = 10


def hook_command(prefix, python):
    """The shell command of the hook: the brief guide, or a message once the runtime is gone."""
    session = str(Path(prefix) / 'session.py')
    missing = f'Koinon guidance unavailable: {session} is missing'
    return (f'if [ -f {shlex.quote(session)} ]; then {shlex.quote(str(python))} {shlex.quote(session)} '
            f'guide --agent claude --brief; else echo {shlex.quote(missing)}; fi; exit 0')


def hook_entry(prefix, python):
    return dict(type='command', command=hook_command(prefix, python), timeout=TIMEOUT)


def repair_command(prefix, python):
    return shlex.join([str(python), str(Path(prefix) / 'scripts' / 'install.py'),
                       '--prefix', str(prefix), '--claude-guidance'])


def _groups(settings, path):
    """The `SessionStart` matcher groups, refusing a shape Koinon cannot edit safely."""
    hooks = settings.get('hooks', {})
    if not isinstance(hooks, dict):
        raise SettingsError('settings_invalid', 'Claude settings "hooks" is not an object; not changed', path)
    groups = hooks.get(EVENT, [])
    if not isinstance(groups, list):
        raise SettingsError('settings_invalid', f'Claude settings "hooks.{EVENT}" is not a list; not changed', path)
    return groups


def _find(groups, command):
    """Every `(group index, hook index, hook)` whose command is exactly this one."""
    return [(g, h, hook) for g, group in enumerate(groups) if isinstance(group, dict)
            and isinstance(group.get('hooks'), list)
            for h, hook in enumerate(group['hooks'])
            if isinstance(hook, dict) and hook.get('command') == command]


def _add(settings, entry):
    """Settings with one new matcher group for the entry, and which containers it created."""
    hooks = dict(settings.get('hooks', {}))
    created = [name for name, present in (('hooks', 'hooks' in settings), (EVENT, EVENT in hooks)) if not present]
    hooks[EVENT] = list(hooks.get(EVENT, [])) + [dict(hooks=[entry])]
    return dict(settings, hooks=hooks), created


def _without(settings, found, created):
    """Settings without Koinon's hook; a container Koinon created goes once it is empty."""
    (group_index, hook_index, _), = found
    hooks = dict(settings['hooks'])
    groups = list(hooks[EVENT])
    group = dict(groups[group_index])
    group['hooks'] = [hook for index, hook in enumerate(group['hooks']) if index != hook_index]
    if group['hooks'] or set(group) != {'hooks'}:
        groups[group_index] = group
    else:
        del groups[group_index]
    hooks[EVENT] = groups
    if not groups and EVENT in created:
        del hooks[EVENT]
    result = dict(settings, hooks=hooks)
    if not hooks and 'hooks' in created:
        del result['hooks']
    return result


def _blocks(state):
    return dict((state.config.get(participant_instructions.RECORD_KEY) or {}).get('blocks', {}))


def _save_block(state, record):
    blocks = _blocks(state)
    if record is None:
        blocks.pop('claude', None)
    else:
        blocks['claude'] = record
    state.merge({participant_instructions.RECORD_KEY: dict(version=1, blocks=blocks)})


def _record(state, value, path, hook=None, created=()):
    state.merge({KEY: dict(state=value, settings_file=str(path), hook=hook, created=list(created))})


def _result(action, outcome, path, **extra):
    return dict(action=action, outcome=outcome, settings_file=str(path), **extra)


def set_up(state, prefix, python, *, explicit=False, directory=None, replace=False, expected=None):
    """Write the block and the hook, keeping a saved decline and every user edit.

    `explicit` is the user's request to set them up now: it reverses a decline and adds an
    entry the user had removed. `replace` also replaces an edited block, after a backup.
    `expected` maps block paths to the digests an upgrade plan observed.
    """
    path = claude_statusline.settings_path(directory)
    record = state.config.get(KEY) or {}
    if not path.parent.is_dir():
        return _result('set_up', 'skipped', path, reason='claude_config_missing')
    if record.get('state') == 'declined' and not explicit:
        return _result('set_up', 'declined', path)
    raw, settings = claude_statusline._read(path)
    groups = _groups(settings, path)
    block = _blocks(state).get('claude')
    if explicit and block and block.get('state') == 'missing':
        block = None  # The user asked again for a block they had removed.
    report, block = participant_instructions.reconcile(path.parent, prefix, 'claude', block, python=python,
                                                       replace=replace, expected=expected)
    if block is not None:
        _save_block(state, block)
    entry = hook_entry(prefix, python)
    active = record.get('state') in ('enabled', 'pending')
    recorded = record.get('hook') if active else None
    created = record.get('created', []) if active else []
    own = _find(groups, recorded['command']) if recorded else []
    same = _find(groups, entry['command'])
    changed = _result('set_up', 'changed', path, block=report, reason='hook_changed',
                      repair=repair_command(prefix, python))
    if len(own) == 1 and own[0][2] == recorded:
        # Koinon's own entry, untouched: refresh it in place if the runtime path changed.
        if recorded == entry:
            _record(state, 'enabled', path, entry, created)
            return _result('set_up', 'unchanged', path, block=report)
        group_index, hook_index, _ = own[0]
        hooks = dict(settings['hooks'])
        hooks[EVENT] = [dict(group, hooks=[entry if (g, h) == (group_index, hook_index) else hook
                                           for h, hook in enumerate(group['hooks'])])
                        if g == group_index else group for g, group in enumerate(groups)]
        updated = dict(settings, hooks=hooks)
    elif same:
        # The current command is already there: adopt an exact copy, never add a duplicate.
        if len(same) == 1 and same[0][2] == entry:
            _record(state, 'enabled', path, entry, created)
            return _result('set_up', 'unchanged', path, block=report)
        return changed
    elif active and record.get('state') == 'enabled' and not explicit:
        # The user removed or edited Koinon's entry after set-up; keep their choice.
        return changed
    else:
        updated, created = _add(settings, entry)
    # Record the entry before the settings change, so removal can always find it.
    _record(state, 'pending', path, entry, created)
    claude_statusline._write(path, raw, updated)
    _record(state, 'enabled', path, entry, created)
    return _result('set_up', 'set_up', path, block=report)


def remove(state, prefix, directory=None, python=None):
    """Remove the hook and the block while they are still Koinon's, then record a decline."""
    record = state.config.get(KEY) or {}
    path = Path(record.get('settings_file') or claude_statusline.settings_path(directory))
    block = _blocks(state).get('claude')
    report = (participant_instructions.remove_owned(path.parent, prefix, 'claude', block, python=python)
              if block or record.get('state') in ('enabled', 'pending') else [])
    _save_block(state, None)
    outcome, extra = 'not_set_up', {}
    if record.get('state') in ('enabled', 'pending') and record.get('hook'):
        raw, settings = claude_statusline._read(path)
        found = _find(_groups(settings, path), record['hook']['command'])
        if len(found) == 1 and found[0][2] == record['hook']:
            claude_statusline._write(path, raw, _without(settings, found, record.get('created', [])))
            outcome = 'removed'
        elif found or record.get('state') == 'enabled':
            outcome, extra = 'changed', dict(reason='hook_changed', action_required=(
                f'The {EVENT} hook differs from the one Koinon set up or is gone; it was kept unchanged.'))
        else:
            outcome = 'removed'  # Set-up stopped before its settings write.
    _record(state, 'declined', path)
    return _result('remove', outcome, path, block=report, **extra)


def decline(state, prefix, directory=None):
    """Record that the user declines the guidance entries; remove them if they are set up."""
    record = state.config.get(KEY) or {}
    if record.get('state') in ('enabled', 'pending'):
        return dict(remove(state, prefix, directory=directory), action='decline')
    path = claude_statusline.settings_path(directory)
    _record(state, 'declined', path)
    return _result('decline', 'declined', path)


def release(state, prefix):
    """Before uninstall deletes the runtime, remove the entries that are still Koinon's.

    A kept hook stays harmless: its command reports the missing runtime and exits 0.
    """
    record = state.config.get(KEY) or {}
    if record.get('state') in ('enabled', 'pending') or _blocks(state).get('claude'):
        return remove(state, prefix)
    return None


def _settings_digest(settings):
    # The status-line step of the same upgrade runs first and owns `statusLine`.
    rest = {key: value for key, value in settings.items() if key != 'statusLine'}
    return hashlib.sha256(json.dumps(rest, sort_keys=True).encode()).hexdigest()


def plan(config, directory=None, prefix=None):
    """The upgrade's planned action, observed at preflight: set_up, declined or skipped."""
    path = claude_statusline.settings_path(directory)
    record = config.get(KEY) or {}
    if record.get('state') == 'declined':
        return dict(action='declined', settings_file=str(path), digest=None, block=[])
    if not path.parent.is_dir():
        return dict(action='skipped', settings_file=str(path), digest=None, block=[], reason='claude_config_missing')
    try:
        _, settings = claude_statusline._read(path)
        _groups(settings, path)
        block = (participant_instructions.classify(
            path.parent, prefix, 'claude',
            ((config.get(participant_instructions.RECORD_KEY) or {}).get('blocks') or {}).get('claude'))
            if prefix is not None else [])
    except (OSError, ValueError) as exc:
        return dict(action='skipped', settings_file=str(path), digest=None, block=[],
                    reason=getattr(exc, 'code', 'settings_unavailable'))
    return dict(action='set_up', settings_file=str(path), digest=_settings_digest(settings), block=block)


def _own_write(installed, path, settings):
    """Whether the settings hold exactly the hook this installation recorded (a resumed run)."""
    record = installed.config.get(KEY) or {}
    return (record.get('state') in ('enabled', 'pending') and record.get('settings_file') == str(path)
            and record.get('hook') is not None
            and [found for *_, found in _find(_groups(settings, path), record['hook']['command'])]
            == [record['hook']])


def apply_planned(prefix, planned, python):
    """Run the planned upgrade action after ordinary admission is restored.

    A settings file that changed since preflight, other than by the status-line step and by
    Koinon's own earlier write, is a conflict and is not written. Set-up is idempotent, so
    running this again after a crash is safe.
    """
    from koinon import install_state
    if planned is None or planned.get('action') != 'set_up':
        return planned
    path = Path(planned['settings_file'])
    failed = dict(action='set_up', repair=repair_command(prefix, python))
    try:
        with install_state.locked(prefix) as installed:
            _, settings = claude_statusline._read(path)
            if _settings_digest(settings) != planned['digest'] and not _own_write(installed, path, settings):
                return dict(failed, outcome='conflict', code='settings_conflict',
                            error='Claude settings changed during the upgrade; not changed')
            expected = {entry['path']: entry.get('digest') for entry in planned.get('block', [])}
            return set_up(installed, prefix, python, directory=path.parent, expected=expected)
    except (OSError, ValueError) as exc:
        return dict(failed, outcome='failed', code=getattr(exc, 'code', 'settings_unavailable'), error=str(exc))
