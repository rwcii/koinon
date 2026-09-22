"""One wording for refusing a path that another account can write.

Every ownership check in this package refuses a path whose mode carries group or
other write permission, because neither can be shown to grant write access to
the owner alone. A group appears exclusive when its entry lists no other member,
but primary-group members are never listed there, and account enumeration may be
partial or unavailable, so an exclusive-looking group is not a proof. The rule
therefore stays as it is and refuses.

What was missing is the operator's remedy. A 002 umask creates directories with
mode 0o775, and the refusals named only the path, so an operator could not see
what was wrong or what to change. These helpers add the mode and the remedy to
the refusals raised for paths the operator created, not for the files and
directories this package creates itself with an explicit mode.

Nothing here repairs permissions or adopts a path.
"""
import stat

WRITABLE_BY_OTHERS = 0o022
REMEDY = 'remove group and other write permission, for example chmod go-w'


def writable_by_others(info):
    """True when this entry's mode grants write access beyond its owner."""
    return bool(info.st_mode & WRITABLE_BY_OTHERS)


def temporary_root(info):
    """True for the sticky root-owned system temporary directory."""
    return info.st_uid == 0 and bool(info.st_mode & stat.S_ISVTX)


def describe(path, info):
    """Name the path, the offending mode and the remedy, as issue #79 requires."""
    return '%s (mode %04o): %s' % (path, stat.S_IMODE(info.st_mode), REMEDY)
