"""Shared bootstrap helpers for Python unit tests."""

from __future__ import annotations

import sys
from pathlib import Path


def ensure_pythonpath() -> None:
    """Ensure local `python/` sources are importable as `sre_agent.*`."""

    repo_root = Path(__file__).resolve().parents[2]
    python_dir = repo_root / "python"
    python_dir_str = str(python_dir)
    if python_dir_str not in sys.path:
        sys.path.insert(0, python_dir_str)

