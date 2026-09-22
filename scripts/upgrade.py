#!/usr/bin/env python3
"""Dispatch explicit upgrade selection from a complete source checkout."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from koinon.upgrade_command import main

if __name__ == '__main__':
    raise SystemExit(main())
