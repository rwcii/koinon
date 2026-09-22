"""Koinon's implementation modules.

The executable entrypoints stay beside this package at the installation prefix,
because installed service definitions name their paths and those definitions are
compared byte for byte: `bridge.py`, `notify.py`, `session.py`, `memory.py`,
`memory_service.py` and `session_service.py`. Everything they import lives here.
"""
