"""The repository root, resolved from this package's own location.

Tests run from `tests/` but read installer scripts, module sources and documents
that live at the repository root. Resolving that root here keeps the relationship
in one place instead of repeating a parent walk at every call site.
"""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
