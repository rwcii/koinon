"""Staged memory selections; no publication or activation.

validate() checks internal consistency only, without filesystem resolution.
verify_selection() rechecks Git identity where filesystem access is appropriate;
future publication must call it before admitting a saved selection to runtime use.
"""
import copy
import hashlib
from pathlib import Path
import re

from koinon import runtime_names
from koinon.work_policy import absolute_path

VERSION = 1
MAX_SERVICES = 64
DIGEST = re.compile(r'[0-9a-f]{64}')
BACKENDS = ('systemd', 'launchd', 'manual')
IDENTITY_FIELDS = frozenset(('common_directory', 'identity_digest', 'state_root',
                             'service_directory', 'backend', 'artifact'))
BASE_FIELDS = IDENTITY_FIELDS | {'state', 'artifact_digest'}


def base_fields(record):
    # Retain older staged selections without inventing a launchd domain. Native
    # activation requires the explicit field; ordinary validation is read-only.
    return BASE_FIELDS | {field for field in ('manager_domain', 'template_version') if field in record}


def identity(common_directory):
    """Hash exactly the common-directory string used by memory.repo_identity."""
    common = absolute_path(str(common_directory))
    digest = hashlib.sha256(str(common).encode()).hexdigest()
    return digest[:16], digest


def selection(repository, state_root):
    """Resolve Git identity without creating a store, directory, or registration."""
    from memory import repo_common_directory
    common = repo_common_directory(repository)
    key, digest = identity(common)
    root = absolute_path(str(state_root))
    return key, dict(common_directory=str(common), identity_digest=digest,
                     state_root=str(root), service_directory=str(root / 'memory' / key))


def artifact_name(key, backend):
    if not isinstance(key, str) or re.fullmatch(r'[0-9a-f]{16}', key) is None:
        raise ValueError('invalid memory repository key')
    if backend == 'systemd':
        return f'koinon-memory-{key}.service'
    if backend == 'launchd':
        return f'io.github.rwcii.koinon.memory.{key}.plist'
    if backend == 'manual':
        return None
    raise ValueError('invalid memory service backend')


def _digest(value):
    return isinstance(value, str) and DIGEST.fullmatch(value) is not None


def validate(value):
    """Validate retained evidence structurally; never adopt files or repair it."""
    if (not isinstance(value, dict) or set(value) != {'version', 'repositories'}
            or type(value['version']) is not int or value['version'] != VERSION
            or not isinstance(value['repositories'], dict)
            or len(value['repositories']) > MAX_SERVICES):
        raise ValueError('invalid memory-services version or repository inventory')
    for key, record in value['repositories'].items():
        if not isinstance(record, dict):
            raise ValueError('invalid memory-service record')
        state = record.get('state')
        fields = base_fields(record) | ({'before_digest', 'after_digest'}
                                if state in ('pending', 'removing') else set())
        if set(record) != fields or state not in ('pending', 'installed', 'removing'):
            raise ValueError('invalid memory-service fields or state')
        common = absolute_path(record['common_directory'])
        expected_key, full_digest = identity(common)
        if key != expected_key or record['identity_digest'] != full_digest:
            raise ValueError('memory repository identity mismatch')
        root = absolute_path(record['state_root'])
        home = absolute_path(record['service_directory'])
        if home != root / 'memory' / key:
            raise ValueError('memory service directory does not match selection')
        backend = record['backend']
        expected_name = artifact_name(key, backend)
        if 'template_version' in record:
            version = record['template_version']
            if (type(version) is not int or version not in (1, 2) or backend == 'manual'
                    or version == 2 and backend != 'systemd'):
                raise ValueError('unsupported memory artifact template')
        if 'manager_domain' in record:
            domain = record['manager_domain']
            if (backend != 'launchd' or not isinstance(domain, str)
                    or re.fullmatch(r'gui/(0|[1-9][0-9]{0,9})', domain) is None
                    or int(domain[4:]) > 4294967295):
                raise ValueError('invalid selected memory manager domain')
        if backend == 'manual':
            if (record['artifact'] is not None or record['artifact_digest'] is not None
                    or state != 'installed'):
                raise ValueError('manual selection has no managed artifact')
        else:
            artifact = absolute_path(record['artifact'])
            if artifact.name != expected_name or not _digest(record['artifact_digest']):
                raise ValueError('invalid managed memory artifact')
        if state == 'pending':
            before, after = record['before_digest'], record['after_digest']
            if ((before is not None and not _digest(before))
                    or not _digest(after) or after != record['artifact_digest']):
                raise ValueError('invalid memory publication evidence')
        elif state == 'removing':
            if (record['before_digest'] != record['artifact_digest']
                    or not _digest(record['before_digest']) or record['after_digest'] is not None):
                raise ValueError('invalid memory removal evidence')
    return value


def admit(current, record):
    """Pure new-selection admission; transitions require later lifecycle machinery.

    This helper is installer-only. The runner never admits a registration and must
    not emit memory_service_limit through the memory protocol error vocabulary.
    """
    validate(current)
    candidate = copy.deepcopy(record)
    if not isinstance(candidate, dict):
        raise ValueError('invalid memory-service admission record')
    common = absolute_path(candidate.get('common_directory'))
    key, _ = identity(common)
    validate(dict(version=VERSION, repositories={key: candidate}))
    existing = current['repositories'].get(key)
    if existing is not None:
        # Compare full evidence, not just the short-key filename. Changes require
        # explicit migration/publication handling, never implicit admission.
        if existing != candidate:
            raise ValueError('existing memory selection requires explicit reconciliation')
        return copy.deepcopy(current)
    if len(current['repositories']) >= MAX_SERVICES:
        raise runtime_names.NameConflict('memory_service_limit', (candidate['common_directory'],))
    updated = copy.deepcopy(current)
    updated['repositories'][key] = candidate
    return validate(updated)


def verify_selection(record):
    """Revalidate selected Git identity before future publication or runtime use.

    This does not verify artifact ownership, start services, or create state.
    A structurally consistent alias is refused if Git resolves it differently.
    """
    if not isinstance(record, dict):
        raise ValueError('invalid memory-service selection')
    common = absolute_path(record.get('common_directory'))
    key, _ = identity(common)
    validate(dict(version=VERSION, repositories={key: record}))
    actual_key, actual = selection(common, record['state_root'])
    if actual_key != key or any(record[field] != value for field, value in actual.items()):
        raise ValueError('saved memory repository no longer resolves to its selected identity')
    return actual_key, actual
