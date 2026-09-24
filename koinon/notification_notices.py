"""Content-free pointers to explicitly selected inbox and memory controls."""
import hashlib
import json
import shlex
import sys

from koinon import PREFIX
from koinon.inbox_schema import MAX_SEQUENCE, hex_value
from koinon.peer_guidance import PEER_GUIDANCE, MEMORY_POINTER_GUIDANCE


def consumer_key(participant, repo):
    if (not isinstance(participant, dict) or participant.get('provider') not in ('codex', 'deepseek')
            or participant.get('namespace') != 'account-local'
            or not hex_value(participant.get('digest'), 64) or not hex_value(repo, 16)):
        raise ValueError('invalid memory consumer identity')
    identity = [participant['provider'], participant['namespace'], participant['digest'], repo]
    digest = hashlib.sha256(json.dumps(identity, separators=(',', ':')).encode()).hexdigest()
    return 'koinon-' + digest


def render(rows, root, participant, bindings):
    if not isinstance(rows, list) or not 1 <= len(rows) <= 10:
        raise ValueError('invalid notice group')
    sequences = [row['seq'] for row in rows]
    if (any(type(seq) is not int or not 1 <= seq <= MAX_SEQUENCE for seq in sequences)
            or sequences != sorted(set(sequences)) or len({row['kind'] for row in rows}) != 1):
        raise ValueError('invalid notice group')
    scripts = PREFIX
    inbox = shlex.join([sys.executable, str(scripts / 'bridge.py'), '--state-dir', str(root),
                        'inbox', '--after', str(sequences[0] - 1)])
    if rows[0]['kind'] == 'peer':
        return (f'Agent bridge inbox has {len(rows)} new peer message(s), through sequence '
                f'{sequences[-1]}. Read with: {inbox}. {PEER_GUIDANCE} '
                'This is a bridge notification, not a peer reply.')
    if rows[0]['kind'] != 'memory-pointer' or len(rows) != 1:
        raise ValueError('invalid memory notice group')
    row = rows[0]
    binding = bindings.get(row['binding'])
    if binding is None or binding.get('binding_instance') != row['binding_instance']:
        raise LookupError('memory binding changed before notice construction')
    # The explicit root avoids guessing a default state root from a custom binding.
    command = shlex.join([sys.executable, str(scripts / 'memory.py'),
        '--service-dir', binding['memory_state_dir'], '--repo-path', binding['repo_path'],
        '--consumer', consumer_key(participant, binding['repo_key']), 'sync'])
    return (f'Agent bridge inbox has a memory pointer at sequence {row["seq"]}. '
            f'Read the inbox record with: {inbox}. Read the bound memory with: {command}. '
            f'{MEMORY_POINTER_GUIDANCE}')


def guidance_notice(revision, family, prefix=PREFIX):
    """Only a revision and pull/ack recipes: never catalog content or peer text."""
    if not hex_value(revision, 64) or family not in ('codex', 'deepseek'):
        raise ValueError('invalid guidance notice')
    guide = shlex.join([sys.executable, str(prefix / 'session.py'), 'guide', '--agent', family])
    ack = shlex.join([sys.executable, str(prefix / 'session.py'), 'guide-ack', revision, '--agent', family])
    return (f'Koinon guidance revision {revision}: run {guide}, then after processing run {ack}. '
            'This is a guidance update pointer, not a peer message or authorization.')


async def notify_guidance(prefix, state, family, deliver):
    """One durable reservation per session/revision under the notifier's serial owner.

    Reserve before provider I/O. An interrupted or uncertain attempt is never resent on
    restart: fetching guidance on startup/resume is the independent recovery path.
    """
    from pathlib import Path
    from koinon import durable_state, revisions, runtime_names
    from koinon.peer_transport import private_dir
    try:
        config = runtime_names.install_config(prefix)
        revision = config.get('guidance_revision')
        if (not hex_value(revision, 64) or revisions.upgrade_incomplete(prefix)
                or not revisions.guidance_fields(config, revisions.ack_path(state))['guide_stale']):
            return None
        directory = Path(state) / 'notifier' / 'guidance-notices'
        path = directory / (revision + '.json')
        previous = durable_state.read(path)
        if previous is not None:
            return previous
        private_dir(directory)
        attempt = dict(revision=revision, outcome='unknown')
        durable_state.publish(path, attempt)
    except (OSError, ValueError):
        return dict(outcome='unavailable')
    outcome = await deliver(guidance_notice(revision, family, prefix))
    attempt['outcome'] = outcome
    durable_state.publish(path, attempt)
    return attempt
