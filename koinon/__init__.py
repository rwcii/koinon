"""Koinon's implementation modules.

The executable entrypoints stay beside this package at the installation prefix,
because installed service definitions name their paths and those definitions are
compared byte for byte: `bridge.py`, `notify.py`, `session.py`, `memory.py`,
`memory_service.py`, `session_service.py` and `usage_report.py`. Everything they
import lives here.
"""
from pathlib import Path

# The installation prefix: the directory holding the entrypoints and this package.
# A module inside the package must never treat its own directory as the prefix,
# because every entrypoint path and the upgrade marker live one level above it.
PREFIX = Path(__file__).resolve().parent.parent
