"""Pure saved native-session selection; no publication, process or manager actions."""
import hashlib
import json
from pathlib import Path
import re

from koinon.inbox_schema import hex_value
from koinon.participant_lock import identity
from koinon.work_policy import absolute_path

BACKENDS = ('systemd', 'launchd')
BASE_FIELDS = frozenset(('version', 'state', 'prefix', 'python', 'state_directory', 'session_key',
                         'thread_digest', 'participant_digest', 'registration_digest', 'command_digest',
                         'backend', 'manager_domain', 'artifact', 'artifact_digest'))


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'),
                                     ensure_ascii=True).encode()).hexdigest()


def registration_identity(registration):
    if (not isinstance(registration, dict) or not {'thread', 'name', 'repo'} <= set(registration)
            or set(registration) - {'thread', 'name', 'repo', 'agent', 'model'}):
        raise ValueError('invalid session registration')
    thread = registration['thread']
    if not isinstance(thread, str) or re.fullmatch(r'[A-Za-z0-9_-]{1,128}', thread) is None:
        raise ValueError('invalid explicit session identity')
    name = registration['name']
    if (not isinstance(name, str) or not name or len(name.encode()) > 256
            or any(ord(char) < 32 or 127 <= ord(char) <= 159 for char in name)):
        raise ValueError('invalid registered peer name')
    absolute_path(registration['repo'])
    agent = registration.get('agent', 'codex')
    if agent not in ('codex', 'deepseek'):
        raise ValueError('unsupported session participant')
    model = registration.get('model')
    if model is not None and (not isinstance(model, str) or not model or len(model.encode()) > 512
                             or any(ord(char) < 32 or 127 <= ord(char) <= 159 for char in model)):
        raise ValueError('invalid registered model')
    digest = hashlib.sha256(thread.encode()).hexdigest()
    return digest[:16], digest, identity(agent, thread)['digest']


def artifact_name(key, backend):
    if not hex_value(key, 16):
        raise ValueError('invalid session key')
    if backend == 'systemd':
        return f'koinon-session-{key}.service'
    if backend == 'launchd':
        return f'io.github.rwcii.koinon.session.{key}.plist'
    raise ValueError('invalid session backend')


def commands(prefix, python, state, config, registration):
    key, _, _ = registration_identity(registration)
    prefix, python, state = (absolute_path(str(value)) for value in (prefix, python, state))
    if state != absolute_path(config.get('state_root')) / 'sessions' / key:
        raise ValueError('session state directory differs from installation selection')
    bridge = [str(python), str(prefix / 'bridge.py'), '--state-dir', str(state), 'serve']
    notifier = [str(python), str(prefix / 'notify.py'), '--state-dir', str(state),
                '--thread=' + registration['thread'], '--name=' + registration['name'],
                '--repo', registration['repo']]
    if registration.get('agent', 'codex') == 'deepseek':
        notifier += ['--agent', 'deepseek']
        # A native job does not inherit a selected harness session's environment.
        url, credentials = config.get('dsh_url'), config.get('dsh_credentials')
        if (not isinstance(url, str) or not url or len(url.encode()) > 2048
                or any(ord(char) < 32 or 127 <= ord(char) <= 159 for char in url)):
            raise ValueError('native DeepSeek selection requires an explicit harness URL')
        notifier += ['--dsh-url=' + url, '--dsh-credentials', str(absolute_path(credentials))]
    else:
        notifier += ['--codex', str(absolute_path(config.get('codex')))]
    return dict(bridge=bridge, notifier=notifier)


def validate(record):
    if not isinstance(record, dict):
        raise ValueError('invalid native session selection')
    fields = BASE_FIELDS | ({'before_digest', 'after_digest'}
                            if record.get('state') in ('pending', 'removing') else set())
    if (set(record) != fields or type(record['version']) is not int or record['version'] != 1
            or record['state'] not in ('pending', 'installed', 'removing')
            or record['backend'] not in BACKENDS
            or not all(hex_value(record[key], 64) for key in (
                'thread_digest', 'participant_digest', 'registration_digest', 'command_digest'))
            or record['session_key'] != record['thread_digest'][:16]):
        raise ValueError('invalid native session fields')
    for field in ('prefix', 'python', 'state_directory'):
        absolute_path(record[field])
    home = Path(record['state_directory'])
    if home.name != record['session_key'] or home.parent.name != 'sessions':
        raise ValueError('native session directory mismatch')
    backend = record['backend']
    domain = record['manager_domain']
    if backend == 'launchd':
        if (not isinstance(domain, str) or re.fullmatch(r'gui/(0|[1-9][0-9]{0,9})', domain) is None
                or int(domain[4:]) > 4294967295):
            raise ValueError('explicit user launchd domain required')
    elif domain is not None:
        raise ValueError('unexpected native session domain')
    if (absolute_path(record['artifact']) != home / 'native-service' / artifact_name(record['session_key'], backend)
            or not hex_value(record['artifact_digest'], 64)):
        raise ValueError('native session artifact mismatch')
    if record['state'] == 'pending':
        if record['before_digest'] is not None or record['after_digest'] != record['artifact_digest']:
            raise ValueError('only new native session publication is supported')
    elif record['state'] == 'removing':
        if record['before_digest'] != record['artifact_digest'] or record['after_digest'] is not None:
            raise ValueError('invalid native session removal evidence')
    if len(json.dumps(record, sort_keys=True, separators=(',', ':')).encode()) > 4096:
        raise ValueError('native session selection exceeds private record capacity')
    return record


def selection(prefix, python, state, config, registration, backend, *, domain=None):
    key, thread_digest, participant_digest = registration_identity(registration)
    prefix, python, state = (absolute_path(str(value)) for value in (prefix, python, state))
    name = artifact_name(key, backend)
    record = dict(version=1, state='installed', prefix=str(prefix), python=str(python),
                  state_directory=str(state), session_key=key, thread_digest=thread_digest,
                  participant_digest=participant_digest, registration_digest=fingerprint(registration),
                  command_digest=fingerprint(commands(prefix, python, state, config, registration)),
                  backend=backend, manager_domain=domain,
                  artifact=str(state / 'native-service' / name) if name else None,
                  artifact_digest='0' * 64 if name else None)
    validate(record)
    if name:
        from koinon import platform_support
        record['artifact_digest'] = hashlib.sha256(platform_support.session_service_artifact(record)).hexdigest()
    return validate(record)


def verify(record, config, registration):
    validate(record)
    candidate = selection(record['prefix'], record['python'], record['state_directory'],
                          config, registration, record['backend'], domain=record['manager_domain'])
    current = {key: value for key, value in record.items() if key in BASE_FIELDS}
    current['state'] = 'installed'
    if current != candidate:
        raise ValueError('saved native session selection changed; explicit reconciliation required')
    return record
