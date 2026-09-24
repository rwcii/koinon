"""One rule, and one wording, for refusing a path a second account could control.

Installation and upgrade refuse a path whose mode carries group or other write
permission, because neither can be shown to grant write access to the owner
alone. A group looks exclusive when its entry lists no other member, but primary
group members are never listed there, and account enumeration may be partial or
unavailable, so an exclusive-looking group is not a proof. The rule therefore
stays as it is and refuses.

What was missing is the operator's remedy. An account whose umask is 002 creates
directories with mode 0775, and the refusals named only the path, so an operator
could see neither the condition nor what to change. `ancestor_fault` and
`target_fault` report the condition that actually failed, so a wrong owner is not
described as a mode to correct, and a mode is not described as a wrong owner.

These helpers serve the checks that validate paths the operator creates. The
checks that guard files this package writes with an explicit 0700 or 0600 mode
keep the plain bit test, because no umask can widen those. Nothing here repairs
permissions or adopts a path.
"""
import os
import stat

from koinon import platform_support

WRITABLE_BY_OTHERS = 0o022
REMEDY = 'remove group and other write permission, for example chmod go-w'
SANDBOX_HINT = ('; an agent sandbox can show this owner for a path it does not map, so run '
                'this command outside the sandbox through the agent\'s approval request')


def writable_by_others(info):
    """True when this entry's mode grants write access beyond its owner."""
    return bool(info.st_mode & WRITABLE_BY_OTHERS)


def temporary_root(info):
    """True for the sticky root-owned system temporary directory."""
    return info.st_uid == 0 and bool(info.st_mode & stat.S_ISVTX)


def _wrong_owner(path, info, allowed):
    """Name the owner; add the sandbox remedy when the owner is the unmapped uid.

    The unmapped uid alone does not prove a sandbox, so the text says it can be one.
    """
    text = '%s is owned by uid %d, not by %s' % (path, info.st_uid, allowed)
    return text + SANDBOX_HINT if info.st_uid == platform_support.overflow_uid() else text


def describe(path, info):
    """Name the path, the offending mode and the remedy, as issue #79 requires."""
    return '%s (mode %04o): %s' % (path, stat.S_IMODE(info.st_mode), REMEDY)


def ancestor_fault(path, info, owner=None):
    """Report why this ancestor cannot be trusted, or None when it can.

    An ancestor may be owned by this user or by root, and the root-owned sticky
    temporary directory is the one place a writable mode is allowed.
    """
    if not stat.S_ISDIR(info.st_mode):
        return '%s is not a directory' % path
    if info.st_uid not in (0, os.geteuid() if owner is None else owner):
        return _wrong_owner(path, info, 'this user or root')
    if writable_by_others(info) and not temporary_root(info):
        return describe(path, info)
    return None


def target_fault(path, info, owner=None):
    """Report why this selected path cannot be trusted, or None when it can.

    The target itself must be owned by this user, and no temporary-directory
    exception applies to it.
    """
    if info.st_uid != (os.geteuid() if owner is None else owner):
        return _wrong_owner(path, info, 'this user')
    if writable_by_others(info):
        return describe(path, info)
    return None
