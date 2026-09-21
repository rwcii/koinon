"""Plan-bound admission for native gated restart during upgrade orchestration.

The coordinator holds the operation lock. These checks do not advance phases or
certify migration: the service must still supply generation-bound inventory.
"""
import upgrade_exclusion
from upgrade_journal import Journal
import upgrade_manifest as manifest


class StartupError(ValueError):
    pass


def validate(exclusion, selection, kind):
    active = upgrade_exclusion.read(exclusion.prefix)
    if (active is None or active['sha256'] != exclusion.loaded['sha256']
            or getattr(selection, 'upgrade', None) is None
            or selection.upgrade['sha256'] != active['sha256']
            or not any(item['kind'] == kind and item['selection'] == selection.record
                       for item in active['documents']['components']['items'])):
        raise StartupError('gated startup selection differs from the active plan')
    if Journal(active['plan']['directory'], active['sha256']).read()['step'] not in (10, 12):
        raise StartupError('gated startup is outside a pending startup phase')
    source = active['documents']['source']
    manifest.verify(source)
    current = manifest.capture(active['plan']['canonical_prefix'], list(source['files']))
    if current['files'] != source['files']:
        raise StartupError('gated startup requires the complete new runtime')
    return active
