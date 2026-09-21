"""Prepare and reopen plan-bound recovery documents before an upgrade marker.

This does not enumerate installations, grant action authority, publish exclusion,
or complete the prepared phase. The coordinator must first perform complete
preflight, hold installation ownership, and later revalidate every external action.
"""
from itertools import islice
from pathlib import Path

import memory_service_config
import runtime_names
import session_service_config
from work_policy import absolute_path
import upgrade_bundle
from upgrade_documents import Documents, MAX_DIRECTORY_ENTRIES
from upgrade_journal import Journal
import upgrade_manifest as manifest

MAX_COMPONENTS = 128
DOCUMENTS = ('source', 'runtime', 'installation', 'components', 'recovery')


class PlanError(ValueError):
    pass


def _components(values, prefix, config):
    if not isinstance(values, list) or len(values) > MAX_COMPONENTS:
        raise PlanError('upgrade component capacity exceeded or invalid inventory')
    seen, memories = set(), {}
    for value in values:
        if (not isinstance(value, dict)
                or set(value) != {'kind', 'selection', 'manager', 'registered', 'running', 'owner'}
                or value['kind'] not in ('memory', 'session')
                or type(value['registered']) is not bool or type(value['running']) is not bool
                or not isinstance(value['manager'], dict)
                or value['manager'].get('status') != ('observed' if value['registered'] else 'absent')
                or value['owner'] is not None and not isinstance(value['owner'], dict)
                or value['running'] and (not value['registered'] or value['owner'] is None)):
            raise PlanError('invalid component observation')
        selection = value['selection']
        if value['kind'] == 'session':
            session_service_config.validate(selection)
            key = selection['session_key']
            if (selection['prefix'] != prefix or selection['state'] != 'installed'
                    or Path(selection['state_directory']) != Path(config['state_root']) / 'sessions' / key):
                raise PlanError('session selection differs from installation')
        else:
            if not isinstance(selection, dict):
                raise PlanError('invalid memory selection')
            key, _ = memory_service_config.identity(selection.get('common_directory'))
            memory_service_config.validate(dict(version=1, repositories={key: selection}))
            if selection['state'] != 'installed' or selection['backend'] not in ('systemd', 'launchd', 'manual'):
                raise PlanError('memory selection requires a supported installed adapter')
            memories[key] = selection
        identity = value['kind'], key
        if identity in seen:
            raise PlanError('duplicate component observation')
        seen.add(identity)
    if memories != config.get('memory_services', {}).get('repositories', {}):
        raise PlanError('memory component inventory differs from installation')


def _validate(plan, root):
    if (not isinstance(plan, dict)
            or set(plan) != {'version', 'directory', 'prefix', 'canonical_prefix', 'documents', 'component_count'}
            or type(plan['version']) is not int or plan['version'] != 1
            or plan['directory'] != str(root)
            or not isinstance(plan['documents'], dict) or set(plan['documents']) != set(DOCUMENTS)
            or not all(manifest.hex_digest(value) for value in plan['documents'].values())
            or type(plan['component_count']) is not int or not 0 <= plan['component_count'] <= MAX_COMPONENTS):
        raise PlanError('invalid frozen upgrade plan')
    absolute_path(plan['prefix'])
    canonical = absolute_path(plan['canonical_prefix'])
    if (root.parent != canonical / '.upgrade' or len(root.name) != 32
            or any(char not in '0123456789abcdef' for char in root.name)):
        raise PlanError('recovery directory is outside the selected installation')
    return plan


def _contents(plan, values, *, completed=False):
    source, runtime = (manifest.validate(values[name]) for name in ('source', 'runtime'))
    if runtime['root'] != plan['canonical_prefix']:
        raise PlanError('old runtime manifest selects another installation')
    config = runtime_names.validate_install_config(values['installation'])
    if config.get('installation_state', 'installed') != 'installed':
        raise PlanError('installation already has an unfinished lifecycle operation')
    inventory = values['components']
    if (not isinstance(inventory, dict) or set(inventory) != {'version', 'items'}
            or type(inventory['version']) is not int or inventory['version'] != 1):
        raise PlanError('invalid frozen component inventory')
    _components(inventory['items'], plan['prefix'], config)
    if len(inventory['items']) != plan['component_count']:
        raise PlanError('frozen component count mismatch')
    recovery = values['recovery']
    archive = upgrade_bundle.verify(recovery, source, require_source=not completed)
    if archive.parent != Path(plan['directory']):
        raise PlanError('recovery archive selects another operation')
    return values


def prepare(directory, prefix, *, source, runtime, installation, components, recovery):
    """Retain immutable evidence and initialize intent; no marker or service action."""
    root = manifest.select_root(directory)
    prefix = str(absolute_path(str(prefix)))
    canonical = manifest.select_root(prefix)
    selected_source = Path(manifest.verify(source)['root'])
    if selected_source == canonical or selected_source in canonical.parents or canonical in selected_source.parents:
        raise PlanError('source and installed runtime roots must be disjoint')
    manifest.verify(runtime)
    values = dict(source=source, runtime=runtime, installation=installation,
                  components=dict(version=1, items=components), recovery=recovery)
    plan = dict(version=1, directory=str(root), prefix=prefix, canonical_prefix=str(canonical),
                documents={name: manifest.fingerprint(value) for name, value in values.items()},
                component_count=len(components) if isinstance(components, list) else -1)
    _validate(plan, root)
    _contents(plan, values)
    digest = manifest.fingerprint(plan)
    journal = Journal(root, digest)
    documents = Documents(root)
    with journal.operation():
        # Reserve one reservation, two capture documents and one migration document per selected
        # component, plus phase/manifests/locks/scratch headroom. No post-shutdown
        # directory growth is permitted to turn a full plan into a partial one.
        current = len(list(islice(root.iterdir(), MAX_DIRECTORY_ENTRIES + 1)))
        if current + 4 * plan['component_count'] + 64 > MAX_DIRECTORY_ENTRIES:
            raise PlanError('insufficient recovery directory capacity for complete operation')
        for name in DOCUMENTS:
            documents.put(name, values[name])
        documents.put('plan', plan)
        phase = journal.initialize()
    return dict(plan=plan, sha256=digest, phase=phase)


def load(directory, expected_digest):
    """Verify frozen data for explicit resume; never initialize missing evidence.

    Old installed bytes may have changed during replacement. Only their retained
    manifest structure is checked here; the phase-specific coordinator decides
    which current bytes are valid. Unfinished operations require the frozen source
    bytes. Completed operations require only its retained manifest and archive, so
    a later checkout update cannot prevent status or the next upgrade.
    """
    root = manifest.check_root(directory)
    documents = Documents(root)
    plan = _validate(documents.read('plan', expected_digest), root)
    if manifest.select_root(plan['prefix']) != Path(plan['canonical_prefix']):
        raise PlanError('installed prefix alias changed since preparation')
    values = {name: documents.read(name, plan['documents'][name]) for name in DOCUMENTS}
    phase = Journal(root, expected_digest).read()
    _contents(plan, values, completed=phase['step'] == 19)
    return dict(plan=plan, sha256=expected_digest, phase=phase, documents=values)
