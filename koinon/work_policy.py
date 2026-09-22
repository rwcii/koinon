"""Validated repository/participant selections; no guidance publication or activation."""
import hashlib
import os
from pathlib import Path
import re
import stat

from koinon import path_permissions

VERSION = 1
MAX_RULES = 64
PARTICIPANTS = ('codex', 'deepseek', 'claude')
DIGEST = re.compile(r'[0-9a-f]{64}')


def absolute_path(value):
    if (not isinstance(value, str) or not value or '\x00' in value
            or any(ord(char) < 32 or 127 <= ord(char) <= 159 for char in value)
            or len(value.encode('utf-8')) > 4096 or not Path(value).is_absolute()
            or '..' in Path(value).parts or str(Path(value)) != value):
        raise ValueError('expected a bounded absolute configuration path')
    return Path(value)


def guidance_path(value):
    """Check the explicit target without following links or creating directories."""
    path = absolute_path(value)
    for component in (*reversed(path.parents), path):
        try:
            info = component.lstat()
        except FileNotFoundError:
            if component == path:
                break
            raise ValueError('guidance parent must exist') from None
        if stat.S_ISLNK(info.st_mode):
            raise ValueError('guidance path cannot contain symlinks')
        if component != path and not stat.S_ISDIR(info.st_mode):
            raise ValueError('guidance parent must be a directory')
        if component == path and (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()):
            raise ValueError('guidance must be a regular file owned by this user')
    fault = path_permissions.target_fault(path.parent, path.parent.stat(), os.getuid())
    if fault is not None:
        raise ValueError('guidance parent must be user-owned and not writable by others: '
                         + fault)
    return path


def repository_key(common_directory):
    return hashlib.sha256(str(common_directory).encode()).hexdigest()[:16]


def validate(value):
    """Validate all retained rules; never reset an invalid object to defaults."""
    if (not isinstance(value, dict) or set(value) != {'version', 'rules'}
            or type(value['version']) is not int or value['version'] != VERSION
            or not isinstance(value['rules'], dict) or len(value['rules']) > MAX_RULES):
        raise ValueError('invalid work-items configuration version or rules')
    for key, rule in value['rules'].items():
        if not isinstance(key, str) or not isinstance(rule, dict):
            raise ValueError('invalid work-items rule')
        fields = {'common_directory', 'guidance_file', 'state', 'digest'}
        if rule.get('state') == 'pending':
            fields |= {'before_digest', 'after_digest'}
        if set(rule) != fields or rule.get('state') not in ('pending', 'enabled', 'disabled'):
            raise ValueError('invalid work-items rule fields or state')
        common = absolute_path(rule['common_directory'])
        absolute_path(rule['guidance_file'])
        if key not in {repository_key(common) + ':' + agent for agent in PARTICIPANTS}:
            raise ValueError('work-items rule key disagrees with repository or participant')
        for field in fields & {'digest', 'before_digest', 'after_digest'}:
            if not isinstance(rule[field], str) or DIGEST.fullmatch(rule[field]) is None:
                raise ValueError('invalid work-items digest')
    return value


def query(config, repository, participant):
    if participant not in PARTICIPANTS:
        raise ValueError('work-policy requires an explicit participant')
    # This is the same identity used by memory; worktrees share their common dir.
    from memory import repo_common_directory
    common = repo_common_directory(repository)
    key = repository_key(common)
    selection = validate(config.get('work_items', {'version': VERSION, 'rules': {}}))
    rule = selection['rules'].get(key + ':' + participant)
    state = 'enabled' if rule and rule['state'] == 'enabled' else 'disabled'
    reason = None
    if state == 'enabled':
        from koinon.work_guidance import verify
        try:
            if not verify(rule, key + ':' + participant):
                state, reason = 'disabled', 'guidance_unverified'
        except (OSError, ValueError):
            state, reason = 'disabled', 'guidance_unverified'
    return dict(version=VERSION, repo=key, common_directory=str(common), participant=participant,
                state=state, enabled=state == 'enabled', reason=reason, digest=rule['digest'] if rule else None,
                guidance_file=rule['guidance_file'] if rule else None)
