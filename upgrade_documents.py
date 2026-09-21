"""Immutable private upgrade documents referenced by the coordinator's frozen plan.

Documents are data, never instructions. This helper does not validate their domain
schema or grant authority; the coordinator supplies and verifies expected digests.
Existing service-owner records retain durable_state's default 4 KiB limit.
"""
from contextlib import contextmanager
from itertools import islice
import os
import re

import durable_state
from participant_lock import file_lock
from upgrade_manifest import check_root, fingerprint, hex_digest, select_root

MAX_BYTES = 1024 * 1024
MAX_DIRECTORY_ENTRIES = 1024


class DocumentError(ValueError):
    pass


class Documents:
    def __init__(self, directory):
        self.directory = select_root(directory)
        self._directory()

    def _directory(self):
        check_root(self.directory)
        info = self.directory.lstat()
        if info.st_mode & 0o077:
            raise DocumentError('upgrade document directory must be private')
        return info.st_dev, info.st_ino

    def path(self, name):
        if (not isinstance(name, str) or name == 'phase'
                or re.fullmatch(r'[a-z][a-z0-9_-]{0,63}', name) is None):
            raise DocumentError('invalid upgrade document name')
        return self.directory / (name + '.json')

    @contextmanager
    def _locked(self):
        directory = self._directory()
        lock = self.directory / 'documents.lock'
        with file_lock(lock, 'upgrade_documents_busy', None) as fd:
            info, named = os.fstat(fd), lock.lstat()
            if ((info.st_dev, info.st_ino) != (named.st_dev, named.st_ino)
                    or self._directory() != directory):
                raise DocumentError('upgrade document ownership changed')
            yield

    def put(self, name, value):
        path = self.path(name)
        if not isinstance(value, dict):
            raise DocumentError('upgrade document must be an object')
        digest = fingerprint(value)
        with self._locked():
            current = durable_state.read(path, max_bytes=MAX_BYTES)
            if current is not None:
                if fingerprint(current) != digest:
                    raise DocumentError('retained upgrade document differs; preserve it')
                durable_state.confirm(path, current, max_bytes=MAX_BYTES)
                return digest
            if len(list(islice(self.directory.iterdir(), MAX_DIRECTORY_ENTRIES + 1))) >= MAX_DIRECTORY_ENTRIES:
                raise DocumentError('upgrade document directory capacity exceeded')
            durable_state.publish(path, value, max_bytes=MAX_BYTES)
            return digest

    def read(self, name, expected_digest):
        path = self.path(name)
        if not hex_digest(expected_digest):
            raise DocumentError('expected upgrade document digest required')
        with self._locked():
            value = durable_state.read(path, max_bytes=MAX_BYTES)
            if value is None or fingerprint(value) != expected_digest:
                raise DocumentError('upgrade document missing or changed')
            return durable_state.confirm(path, value, max_bytes=MAX_BYTES)
