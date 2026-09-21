"""Single permanent installation lock and preserving atomic configuration writes."""
from contextlib import contextmanager, ExitStack
import json
import os
from pathlib import Path
import stat
import tempfile

import platform_support
import runtime_names
from participant_lock import file_lock, OwnershipError

LOCK_NAME = '.install.lock'
LOCK_TIMEOUT = 30


class LockedConfiguration:
    def __init__(self, prefix):
        self.prefix = prefix
        self.config = runtime_names.install_config(prefix)

    def merge(self, updates):
        merged = dict(self.config)
        merged.update(updates)
        runtime_names.validate_install_config(merged)
        target = self.prefix / 'install.json'
        # Revalidate retained evidence before publication, including target type.
        runtime_names.install_config(self.prefix)
        fd, temporary = tempfile.mkstemp(prefix='.install-', dir=self.prefix)
        try:
            with os.fdopen(fd, 'w', encoding='utf-8') as stream:
                json.dump(merged, stream, ensure_ascii=False, sort_keys=True)
                stream.write('\n')
                stream.flush()
                platform_support.sync_state_file(stream.fileno())
                os.replace(temporary, target)
                platform_support.sync_state_directory(self.prefix)
                platform_support.sync_state_file(stream.fileno())
            self.config = merged
            return merged
        finally:
            Path(temporary).unlink(missing_ok=True)


    def confirm(self):
        """Republish unchanged configuration after an ambiguous completion.

        Retain every unknown field and existing validation rule, including older
        readable config modes. A retry succeeds only after all durability flushes.
        """
        if runtime_names.install_config(self.prefix) != self.config:
            raise ValueError('installation configuration changed before confirmation')
        return self.merge({})


@contextmanager
def locked(prefix):
    """Lock order: installation, sorted artifact locks, then sorted guidance locks.

    Never remove or replace permanent lock inodes.
    """
    prefix = Path(prefix)
    path = prefix / LOCK_NAME
    with ExitStack() as stack:
        try:
            prefix.mkdir(parents=True, exist_ok=True, mode=0o700)
            info = prefix.lstat()
            if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
                    or info.st_mode & 0o022):
                raise ValueError('unsafe installation directory')
            fd = stack.enter_context(file_lock(path, 'configuration_busy', None, timeout=LOCK_TIMEOUT))
            info, current = os.fstat(fd), path.lstat()
            if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
                raise ValueError('installation lock was replaced')
            state = LockedConfiguration(prefix)
        except runtime_names.NameConflict:
            raise
        except OwnershipError as exc:
            raise runtime_names.installation_lock_error(exc.code, path) from exc
        except (OSError, ValueError) as exc:
            raise runtime_names.NameConflict('invalid_install_configuration', (path,)) from exc
        # Exceptions raised by publication are not reclassified as lock failures.
        yield state
