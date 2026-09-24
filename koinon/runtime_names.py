"""Product defaults and deliberately stable cross-version identifiers."""
import os
import json
import sys
from pathlib import Path
import re
import stat

NAME = 'koinon'
LEGACY_NAME = 'codex-peer-bridge'
SERVICE_MARKER = '# Managed by koinon\n'
LEGACY_SERVICE_MARKER = '# Managed by codex-peer-bridge\n'
SERVICE_MARKERS = (SERVICE_MARKER, LEGACY_SERVICE_MARKER)

# These values cross an external or old-client boundary; they are not branding.
REGISTRY_ENTRYPOINT = 'codex-peer-bridge'
MEMORY_SERVICE = 'codex-peer-memory'
PATH_SELECTION_CODES = frozenset(('ambiguous_default_paths', 'unsafe_default_path', 'default_path_unavailable'))
CONFIGURATION_CODES = PATH_SELECTION_CODES | {'ambiguous_service_units', 'invalid_install_configuration', 'configuration_busy', 'memory_service_limit', 'installation_upgrading'}
GUIDANCE_LOCK_NAMES = {'codex': '.codex-peer-bridge.lock',
                       'deepseek': '.deepseek-peer-bridge.lock'}
CLAUDE_GUIDANCE_LOCK_NAME = '.koinon-claude-guidance.lock'


class NameConflict(ValueError):
    def __init__(self, code, paths):
        if code not in CONFIGURATION_CODES:
            raise KeyError(code)
        self.code = code
        self.paths = tuple(str(path) for path in paths)
        advice = {'installation_upgrading': 'upgrade in progress; use upgrade status or resume; do not repair or reinstall',
                  'memory_service_limit': 'memory service registration limit reached; preserve existing selections',
                  'invalid_install_configuration': 'preserve and repair configuration',
                  'configuration_busy': 'another installation holds the configuration lock; '
                                        'check that installer and retry; do not delete the lock'}.get(
                                            code, 'select an explicit path')
        super().__init__(code + ': ' + advice + '; ' + ', '.join(self.paths))


def installation_lock_error(code, path):
    """Keep retryable contention distinct from unsafe or unreadable evidence."""
    if code == 'configuration_busy':
        return NameConflict('configuration_busy', (path,))
    return NameConflict('invalid_install_configuration', (path,))


def present(path):
    try:
        Path(path).lstat()
        return True
    except FileNotFoundError:
        return False


def choose_default(parent):
    """Read-only selection. Even a broken symlink prevents a silent new install."""
    current, legacy = (Path(parent) / name for name in (NAME, LEGACY_NAME))
    try:
        new_present, old_present = present(current), present(legacy)
    except OSError as exc:
        raise NameConflict('default_path_unavailable', (current, legacy)) from exc
    if new_present and old_present:
        raise NameConflict('ambiguous_default_paths', (current, legacy))
    selected = legacy if old_present else current
    if present(selected) and (selected.is_symlink() or not selected.is_dir()):
        raise NameConflict('unsafe_default_path', (selected,))
    return selected


def default_prefix():
    return reported_default(Path.home() / '.local' / 'share', 'prefix')


def default_state_root():
    base = Path(os.environ.get('XDG_STATE_HOME', str(Path.home() / '.local' / 'state')))
    return reported_default(base, 'state')


def service_names(instance=None, *, legacy=False):
    if instance is not None and re.fullmatch('[a-f0-9]{16}', instance) is None:
        raise ValueError('invalid service instance')
    stem = 'codex-peer' if legacy else NAME
    if instance is not None:
        return (f'{stem}-session-{instance}.service',)
    return (f'{stem}-notify.service', f'{stem}-bridge.service')


def selected_service_names(unit_dir, instance=None):
    """Keep an existing unit family; never silently create a parallel family."""
    current, legacy = service_names(instance), service_names(instance, legacy=True)
    new_present = [Path(unit_dir) / name for name in current if present(Path(unit_dir) / name)]
    old_present = [Path(unit_dir) / name for name in legacy if present(Path(unit_dir) / name)]
    if new_present and old_present:
        raise NameConflict('ambiguous_service_units', (*new_present, *old_present))
    return legacy if old_present else current


def reported_default(base, label):
    selected = choose_default(base)
    reason = 'legacy path reused' if selected.name == LEGACY_NAME else 'Koinon default'
    print(f'{label}: {selected} ({reason})', file=sys.stderr)
    return selected


def validate_install_config(result):
    if not isinstance(result, dict):
        raise ValueError('configuration is not an object')
    if result.get('installation_state', 'installed') not in ('installed', 'removing'):
        raise ValueError('invalid installation lifecycle state')
    for key in ('state_root', 'unit_dir'):
        if not isinstance(result.get(key), str) or not Path(result[key]).is_absolute():
            raise ValueError('missing or nonabsolute installation path')
    if 'work_items' in result:
        from koinon.work_policy import validate
        validate(result['work_items'])
    if 'memory_services' in result:
        from koinon.memory_service_config import validate
        validate(result['memory_services'])
    if 'session_backend' in result and result['session_backend'] not in ('systemd', 'launchd', 'manual'):
        raise ValueError('invalid session backend selection')
    if 'claude_statusline' in result:
        record = result['claude_statusline']
        if (not isinstance(record, dict) or set(record) != {'state', 'settings_file', 'original', 'wrapper'}
                or record['state'] not in ('enabled', 'pending', 'declined')
                or not isinstance(record['settings_file'], str) or not Path(record['settings_file']).is_absolute()
                or not (record['original'] is None or isinstance(record['original'], dict))
                or not (record['wrapper'] is None or isinstance(record['wrapper'], dict))
                or (record['state'] != 'declined' and record['wrapper'] is None)):
            raise ValueError('invalid Claude status-line selection')
    if 'claude_guidance' in result:
        record = result['claude_guidance']
        if (not isinstance(record, dict) or set(record) != {'state', 'settings_file', 'hook', 'created'}
                or record['state'] not in ('enabled', 'pending', 'declined')
                or not isinstance(record['settings_file'], str) or not Path(record['settings_file']).is_absolute()
                or not (record['hook'] is None or isinstance(record['hook'], dict)
                        and isinstance(record['hook'].get('command'), str))
                or (record['state'] != 'declined' and record['hook'] is None)
                or not isinstance(record['created'], list)
                or not set(record['created']) <= {'hooks', 'SessionStart'}):
            raise ValueError('invalid Claude guidance selection')
    for field in ('runtime_revision', 'guidance_revision'):
        if field in result and (not isinstance(result[field], str)
                                or re.fullmatch(r'[0-9a-f]{64}', result[field]) is None):
            raise ValueError('invalid installed revision')
    if 'participant_guidance' in result:
        record = result['participant_guidance']
        blocks = record.get('blocks') if isinstance(record, dict) else None
        if (not isinstance(record, dict) or record.get('version') != 1 or not isinstance(blocks, dict)
                or any(agent not in ('codex', 'deepseek', 'claude') or not isinstance(block, dict)
                       or not isinstance(block.get('path'), str) or not Path(block['path']).is_absolute()
                       or block.get('state') not in ('current', 'edited', 'missing')
                       or not (block.get('digest') is None or isinstance(block.get('digest'), str))
                       for agent, block in blocks.items())):
            raise ValueError('invalid participant guidance record')
    return result


def install_config(prefix):
    """Absence is valid for old explicit-thread installs; invalid evidence refuses."""
    def validate(value):
        if isinstance(value, dict) and value.get('installation_state') == 'upgrading':
            marker = value.get('upgrade')
            if (isinstance(marker, dict) and set(marker) == {'version', 'operation', 'plan'}
                    and type(marker['version']) is int and marker['version'] == 1
                    and isinstance(marker['operation'], str) and Path(marker['operation']).is_absolute()
                    and isinstance(marker['plan'], str) and re.fullmatch('[0-9a-f]{64}', marker['plan'])):
                raise NameConflict('installation_upgrading', (marker['operation'],))
        return validate_install_config(value)
    return _read_install_config(prefix, validate)


def _read_install_config(prefix, validate):
    """Shared file checks; lifecycle exceptions require a caller-owned validator."""
    path = Path(prefix) / 'install.json'
    try:
        if not present(path):
            return {}
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, encoding='utf-8') as stream:
            info = os.fstat(stream.fileno())
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                    or info.st_mode & 0o022):
                raise ValueError('configuration must be a user-owned regular file not writable by others')
            return validate(json.load(stream))
    except NameConflict:
        raise
    except (OSError, ValueError) as exc:
        raise NameConflict('invalid_install_configuration', (path,)) from exc
