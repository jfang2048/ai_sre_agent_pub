"""Correlation routines for SRE time-series analysis."""

from __future__ import annotations

from typing import List, Sequence, Tuple

import numpy as np


def pearson_correlation(x: Sequence[float], y: Sequence[float]) -> float:
    """Return the Pearson correlation coefficient for two equal-length series."""

    if len(x) == 0 or len(y) == 0 or len(x) != len(y):
        return 0.0

    x_arr = np.asarray(x, dtype=float)
    y_arr = np.asarray(y, dtype=float)

    if np.std(x_arr) == 0 or np.std(y_arr) == 0:
        return 0.0

    return float(np.corrcoef(x_arr, y_arr)[0, 1])


def cross_correlation(
    x: Sequence[float],
    y: Sequence[float],
    max_lag: int = 10,
) -> Tuple[List[int], List[float]]:
    """Compute lag-wise similarity for lags in [-max_lag, max_lag].

    The base signal score is Pearson correlation. A tiny MAE-based penalty is
    applied to break ties between equally-correlated lags where one alignment is
    visibly closer in absolute values.
    """

    if max_lag < 0:
        raise ValueError("max_lag must be >= 0")
    if len(x) == 0 or len(y) == 0:
        return [], []

    x_arr = np.asarray(x, dtype=float)
    y_arr = np.asarray(y, dtype=float)

    lags = list(range(-max_lag, max_lag + 1))
    correlations: List[float] = []

    for lag in lags:
        if lag < 0:
            offset = -lag
            x_slice = x_arr[offset:]
            y_slice = y_arr[:-offset]
        elif lag > 0:
            x_slice = x_arr[:-lag]
            y_slice = y_arr[lag:]
        else:
            x_slice = x_arr
            y_slice = y_arr

        if len(x_slice) < 2 or len(y_slice) < 2:
            correlations.append(0.0)
            continue

        corr = pearson_correlation(x_slice, y_slice)

        # Tie-break nearly identical correlations by preferring lower absolute
        # mismatch for aligned points.
        mae = float(np.mean(np.abs(x_slice - y_slice)))
        scale = float(np.std(x_slice) + np.std(y_slice))
        if scale > 0:
            corr -= mae / (scale * 1000.0)

        correlations.append(float(corr))

    return lags, correlations
