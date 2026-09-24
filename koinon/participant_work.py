"""Explicit per-session work ownership and read-only claimed-work observations."""
import hashlib
import json
import subprocess
import sys
import time
from pathlib import Path

from koinon import PREFIX, participant_status, runtime_names


def consumer_key(value):
    # Same bounded UTF-8 identifier as claims.consumer_key, without loading the
    # database implementation in Claude's status-line process.
    if not isinstance(value, str) or not 0 < len(value) <= 128:
        raise ValueError('consumer must contain 1 to 128 characters')
    value.encode('utf-8')
    return value


def validate(value):
    if value is None:
        return None
    if not isinstance(value, dict):
        raise ValueError('invalid work association')
    kept = {}
    for key in ('repository', 'state_root', 'repo'):
        item = value.get(key)
        if not isinstance(item, str) or not item or len(item) > 4096:
            raise ValueError('invalid work association')
        kept[key] = item
    if not all(Path(kept[key]).is_absolute() for key in ('repository', 'state_root')):
        raise ValueError('work paths must be absolute')
    if len(kept['repo']) != 16 or any(c not in '0123456789abcdef' for c in kept['repo']):
        raise ValueError('invalid repository identity')
    kept['consumer'] = consumer_key(value.get('consumer'))
    return kept


def key_path(agent, session, registry=None):
    if agent not in ('codex', 'deepseek', 'claude') or not isinstance(session, str) or not session:
        raise ValueError('native session identity required')
    digest = hashlib.sha256(session.encode()).hexdigest()
    return participant_status.directory(registry) / f'work-key-{agent}-{digest}.json'


def set_key(agent, session, key, registry=None):
    from koinon import durable_state
    consumer_key(key)
    path = key_path(agent, session, registry)
    participant_status._private_dir(path.parent)
    durable_state.publish(path, dict(consumer=key))


def association(agent, session, repository, *, prefix=PREFIX, registry=None):
    """Select only the writing installation's saved memory service."""
    if not repository or not session:
        return None
    try:
        from koinon.repository_identity import repo_identity
        repo = repo_identity(repository)
        config = runtime_names.install_config(prefix)
        selected = ((config or {}).get('memory_services') or {}).get('repositories', {}).get(repo)
        if not selected or selected.get('state') != 'installed':
            return None
        custom = participant_status._load(key_path(agent, session, registry), 4096)
        return validate(dict(repository=str(Path(repository).absolute()), repo=repo,
                             state_root=selected['state_root'],
                             consumer=custom['consumer'] if custom else session))
    except (OSError, ValueError, KeyError):
        return None


def observe(value, now):
    """Ask the peer's selected store; never substitute the caller's default store."""
    view = participant_status.work_view(now)
    if value is None:
        return view
    try:
        value = validate(value)
        home = Path(value['state_root']) / 'memory' / value['repo']
        result = subprocess.run(
            [sys.executable, str(PREFIX / 'memory.py'), '--service-dir', str(home),
             '--repo-path', value['repository'], 'work', 'list', '--owner', value['consumer']],
            capture_output=True, text=True, timeout=5)
        reply = json.loads(result.stdout)
        if result.returncode or not reply.get('ok'):
            raise ValueError('memory unavailable')
        reply = reply['result']
        claims = [{key: item.get(key) for key in ('work_id', 'title', 'checkpoint')}
                  for item in reply['items'] if item.get('lease_valid') and item.get('owner') == value['consumer']]
        view.update(state='observed', source='memory_work_list', claims=claims,
                    recorded_at_ms=int(reply['observed_at'] * 1000),
                    observed_at_ms=int(time.time()*1000), reason=None,
                    truncated=bool(reply.get('truncated')))
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        view.update(reason='memory_unavailable')
    return view
