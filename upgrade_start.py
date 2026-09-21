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
    phase = Journal(active['plan']['directory'], active['sha256']).read()['step']
    if phase not in (10, 12, 14, 16, 18):
        raise StartupError('gated startup is outside a pending startup phase')
    if phase == 18 and any(not item['running'] and item['kind'] == kind
                           and item['selection'] == selection.record
                           for item in active['documents']['components']['items']):
        raise StartupError('originally inactive component cannot start after release')
    source = active['documents']['source']
    manifest.verify(source)
    current = manifest.capture(active['plan']['canonical_prefix'], list(source['files']))
    if current['files'] != source['files']:
        raise StartupError('gated startup requires the complete new runtime')
    return active


def component(exclusion, selected_component):
    """Start one explicit selection and return its verified child gate observations.

    The coordinator chooses which inactive stores require temporary migration startup
    and must restore their original stopped state before releasing the installation.
    """
    import memory_service
    import session_service_manager
    import upgrade_probe
    import upgrade_quiescence
    selected = upgrade_quiescence.selection(exclusion, selected_component)
    kind = selected_component['kind']
    validate(exclusion, selected, kind)
    if kind == 'session':
        result = session_service_manager.ensure(selected, upgrade=exclusion)
    else:
        result = memory_service.ensure_managed(selected, upgrade=exclusion)
    if result.get('status') != 'running':
        raise StartupError('selected native service did not become ready under the gate')
    if exclusion.verify()['step'] == 18:
        return upgrade_probe.live(exclusion, selected_component)
    return upgrade_probe.gated(exclusion, selected_component)
