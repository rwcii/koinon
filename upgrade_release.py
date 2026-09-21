"""Generation membership in the digest-bound global upgrade release decision."""
from upgrade_documents import Documents
from upgrade_journal import RELEASE_STEP


class ReleaseError(ValueError):
    pass


def validate(loaded, value):
    if (not isinstance(value, dict) or set(value) != {'version', 'plan', 'members'}
            or type(value['version']) is not int or value['version'] != 1
            or value['plan'] != loaded['sha256'] or not isinstance(value['members'], list)
            or len(value['members']) > 256):
        raise ReleaseError('invalid generation-bound release evidence')
    components = loaded['documents']['components']['items']
    allowed, required = set(), set()
    for index, component in enumerate(components):
        kinds = ('bridge', 'notifier') if component['kind'] == 'session' else ('memory',)
        for kind in kinds:
            allowed.add((index, kind))
            if component['running']:
                required.add((index, kind))
    seen = set()
    for member in value['members']:
        if (not isinstance(member, dict) or set(member) != {'component', 'kind', 'generation'}
                or type(member['component']) is not int or not isinstance(member['kind'], str)
                or not isinstance(member['generation'], str) or len(member['generation']) != 32
                or any(char not in '0123456789abcdef' for char in member['generation'])):
            raise ReleaseError('invalid release generation member')
        key = member['component'], member['kind']
        if key not in allowed or key in seen:
            raise ReleaseError('duplicate or unselected release member')
        seen.add(key)
    if not required <= seen:
        raise ReleaseError('release evidence omits an originally running component')
    return value


def member(loaded, phase, component_index, kind, generation):
    if phase['step'] < RELEASE_STEP:
        return False
    value = Documents(loaded['plan']['directory']).read('release', phase['receipts'][RELEASE_STEP // 2])
    validate(loaded, value)
    return dict(component=component_index, kind=kind, generation=generation) in value['members']
