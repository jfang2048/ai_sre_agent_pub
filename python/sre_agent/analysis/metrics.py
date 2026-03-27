"""Metric-derived SLI/SLO utility functions."""

from __future__ import annotations

from typing import Sequence

import numpy as np


def calculate_availability_sli(total_requests: float, errors: float) -> float:
    """Calculate availability SLI in [0, 1]."""

    if total_requests <= 0:
        return 0.0
    success = max(0.0, total_requests - max(0.0, errors))
    return float(success / total_requests)


def calculate_error_budget(slo_target: float, current_sli: float) -> float:
    """Return remaining error budget (positive means within target)."""

    return float(current_sli - slo_target)
