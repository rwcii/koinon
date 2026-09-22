"""Bounded read-only service definition inventory; never adopts a service."""
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import stat
import xml.parsers.expat

from koinon import platform_support

MAX_ENTRIES = 4096
MAX_FILE_BYTES = 256 * 1024
MAX_TOTAL_BYTES = 16 * 1024 * 1024


class DiscoveryError(ValueError):
    pass


def _stamp(value):
    return (value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns)


def _strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, list):
        for item in value:
            yield from _strings(item)
    elif isinstance(value, dict):
        for item in value.values():
            yield from _strings(item)


def _references(data, suffix, prefix, source=None):
    if suffix == '.plist' or data.startswith(b'bplist') or data.lstrip().startswith((b'<?xml', b'<plist')):
        try:
            text = '\n'.join(_strings(plistlib.loads(data)))
        # ExpatError does not derive from ValueError, so a malformed XML declaration
        # escaped this handler and crashed the upgrade instead of refusing. Name the
        # file: a refusal an operator cannot locate is not actionable.
        except (ValueError, plistlib.InvalidFileException, OverflowError,
                xml.parsers.expat.ExpatError) as exc:
            raise DiscoveryError('invalid service property list: ' + str(source)) from exc
    else:
        try:
            text = data.decode('utf-8')
        except UnicodeError as exc:
            raise DiscoveryError('invalid service definition encoding') from exc
        # Recognize literal escaped strings without evaluating a unit or shell.
        text = re.sub(r'\\x([0-9a-fA-F]{2})', lambda m: chr(int(m[1], 16)), text)
        text = text.replace('$$', '$')
        # Expand specifiers in one left-to-right pass. A literal '%' produced by '%%'
        # is never rescanned, so '%%h' stays the text '%h' exactly as systemd renders
        # it; rescanning expanded it to the home path and reported a unit that does
        # not reference this runtime as unowned.
        home = str(platform_support.account_home())
        text = re.sub(r'%(.)', lambda m: '%' if m[1] == '%' else home if m[1] == 'h' else m[0], text)
    candidates = {str(prefix), str(Path(prefix).resolve())}
    candidates.update(json.dumps(item, ensure_ascii=False)[1:-1] for item in tuple(candidates))
    return any(value + '/' in text for value in candidates)


def inventory(prefix, config, components):
    """Inspect direct references to this runtime in supported user service sources.

    Arbitrary opaque wrappers cannot be traced from service definitions. Scope and
    unavailable native discovery are reported explicitly, never called host-wide.
    """
    source = platform_support.upgrade_service_sources(prefix)
    roots = {Path(value) for value in source['directories']}
    if config.get('unit_dir'):
        roots.add(Path(config['unit_dir']))
    paths = {Path(value) for value in source['loaded_artifacts']}
    owned = {}
    for component in components:
        record = component['selection']
        if record.get('artifact'):
            owned[Path(record['artifact']).resolve()] = record['artifact_digest']
    count, total = 0, 0
    directories = []
    def scan(directory, depth=0):
        nonlocal count
        try:
            before = directory.stat()
        except FileNotFoundError:
            return
        if not stat.S_ISDIR(before.st_mode):
            raise DiscoveryError('unsafe service inventory directory: ' + str(directory))
        directories.append((directory, _stamp(before)))
        with os.scandir(directory) as entries:
            for entry in entries:
                count += 1
                if count > MAX_ENTRIES:
                    raise DiscoveryError('service discovery exceeds entry capacity')
                path = Path(entry.path)
                if path.suffix in ('.service', '.plist') or depth and path.suffix == '.conf':
                    paths.add(path)
                elif depth == 0 and path.name.endswith(('.service.d', '.wants', '.requires')):
                    scan(path, depth + 1)
    for root in sorted(roots):
        scan(root)
    if len(paths) > MAX_ENTRIES:
        raise DiscoveryError('loaded service discovery exceeds entry capacity')
    findings, files, verified = [], [], set()
    for path in sorted(paths):
        # Loader symlinks are evidence paths, not peer addresses. Read their target
        # without changing either path; retain both identities across the read.
        try:
            link = path.lstat()
            resolved = path.resolve(strict=True)
        except FileNotFoundError:
            raise DiscoveryError('service discovery contains a missing target: ' + str(path)) from None
        if str(resolved) == '/dev/null':  # A masked systemd unit has no program.
            continue
        info = resolved.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_FILE_BYTES:
            raise DiscoveryError('unsafe or oversized service definition: ' + str(path))
        fd = os.open(resolved, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, 'rb') as stream:
            if _stamp(os.fstat(stream.fileno())) != _stamp(info):
                raise DiscoveryError('service definition changed before read')
            data = stream.read(MAX_FILE_BYTES + 1)
            if _stamp(os.fstat(stream.fileno())) != _stamp(info):
                raise DiscoveryError('service definition changed during read')
        total += len(data)
        if len(data) > MAX_FILE_BYTES or total > MAX_TOTAL_BYTES:
            raise DiscoveryError('service discovery exceeds byte capacity')
        if (_stamp(path.lstat()) != _stamp(link) or path.resolve(strict=True) != resolved
                or _stamp(resolved.lstat()) != _stamp(info)):
            raise DiscoveryError('service definition changed during inventory')
        files.append((path, _stamp(link), resolved, _stamp(info)))
        if not _references(data, path.suffix, prefix, path):
            continue
        digest = hashlib.sha256(data).hexdigest()
        matched = owned.get(resolved) == digest
        if matched:
            verified.add(resolved)
        findings.append(dict(path=str(path), target=str(resolved), sha256=digest,
                             ownership='saved_selection' if matched else 'unowned',
                             action='preserved' if matched else 'refused'))
    for path, link, resolved, identity in files:
        if (_stamp(path.lstat()) != link or path.resolve(strict=True) != resolved
                or _stamp(resolved.lstat()) != identity):
            raise DiscoveryError('service file changed before inventory completion')
    for directory, identity in directories:
        if _stamp(directory.stat()) != identity:
            raise DiscoveryError('service directory changed during inventory')
    loaded = []
    for reference in source.get('loaded_references', []):
        artifact = reference.get('artifact')
        matched = artifact is not None and Path(artifact).resolve() in verified
        loaded.append(dict(reference, ownership='saved_selection' if matched else 'unowned',
                           action='preserved' if matched else 'refused'))
    processes = []
    owners = []
    for component in components:
        if component.get('kind') != 'memory':
            continue
        owner = component.get('owner')
        if owner is not None:
            owners.extend(item for item in (owner, owner.get('child')) if item is not None)
    for process in platform_support.upgrade_memory_processes(prefix):
        matched = any(owner.get('pid') == process['pid']
                      and isinstance(owner.get('proc_start'), str) and owner['proc_start']
                      and platform_support.same_process(owner['proc_start'], process['proc_start'])
                      for owner in owners)
        processes.append(dict(process, ownership='saved_selection' if matched else 'unowned',
                              action='preserved' if matched else 'refused'))
    return dict(version=1, scope='direct_runtime_references_in_user_services_and_memory_processes',
        directories=sorted(map(str, roots)), loaded_discovery=source['loaded_discovery'],
        os_definitions=source.get('os_definitions', []),
        limitations=['opaque_wrappers_and_indirect_runtime_selection_are_not_resolved',
                     'other_users_and_system_service_managers_are_not_selected',
                     'operating_system_definition_files_are_not_parsed_but_their_jobs_are_inspected'],
        findings=findings, loaded_references=loaded, processes=processes,
        action='refused' if any(x['ownership'] == 'unowned' for x in findings + loaded + processes)
        else 'no_unowned_definition_or_process_found')
