"""Statistical anomaly and trend analysis utilities for SRE metrics."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Sequence

import numpy as np


@dataclass(frozen=True)
class AnomalyResult:
    """Represents one detected anomaly in a time series."""

    is_anomaly: bool
    score: float
    threshold: float
    index: int
    value: float
    reason: str


def _as_float_array(values: Sequence[float]) -> np.ndarray:
    """Normalize list-like inputs into a float NumPy array."""

    return np.asarray(values, dtype=float)


def detect_anomalies_zscore(
    values: Sequence[float],
    threshold: float = 2.0,
) -> list[AnomalyResult]:
    """Detect anomalies using z-score distance from the series mean."""

    if threshold <= 0:
        raise ValueError("threshold must be > 0")
    if len(values) < 3:
        return []

    arr = _as_float_array(values)
    mean = float(np.mean(arr))
    std = float(np.std(arr))
    if std == 0:
        return []

    z_scores = np.abs((arr - mean) / std)
    anomalies: list[AnomalyResult] = []
    for i, (z_score, val) in enumerate(zip(z_scores, arr)):
        if float(z_score) > threshold:
            anomalies.append(
                AnomalyResult(
                    is_anomaly=True,
                    score=float(z_score),
                    threshold=threshold,
                    index=i,
                    value=float(val),
                    reason=f"Z-score {float(z_score):.2f} exceeds threshold {threshold}",
                )
            )
    return anomalies


def detect_anomalies_iqr(
    values: Sequence[float],
    multiplier: float = 1.5,
) -> list[AnomalyResult]:
    """Detect anomalies using interquartile range (IQR) bounds."""

    if multiplier <= 0:
        raise ValueError("multiplier must be > 0")
    if len(values) < 4:
        return []

    arr = _as_float_array(values)
    q1 = float(np.percentile(arr, 25))
    q3 = float(np.percentile(arr, 75))
    iqr = q3 - q1
    lower_bound = q1 - multiplier * iqr
    upper_bound = q3 + multiplier * iqr

    anomalies: list[AnomalyResult] = []
    for i, val in enumerate(arr):
        if float(val) < lower_bound or float(val) > upper_bound:
            anomalies.append(
                AnomalyResult(
                    is_anomaly=True,
                    score=0.0,
                    threshold=multiplier,
                    index=i,
                    value=float(val),
                    reason=(
                        f"Value {float(val):.2f} outside IQR bounds "
                        f"[{lower_bound:.2f}, {upper_bound:.2f}]"
                    ),
                )
            )
    return anomalies


def detect_anomalies_moving_average(
    values: Sequence[float],
    window: int = 5,
    threshold: float = 3.0,
) -> list[AnomalyResult]:
    """Detect anomalies by comparing points against a moving baseline window."""

    if window <= 0:
        raise ValueError("window must be > 0")
    if threshold <= 0:
        raise ValueError("threshold must be > 0")
    if len(values) < window + 1:
        return []

    arr = _as_float_array(values)
    anomalies: list[AnomalyResult] = []
    for i in range(window, len(arr)):
        segment = arr[i - window : i]
        mean = float(np.mean(segment))
        std = float(np.std(segment))
        if std == 0:
            continue

        z_score = abs((float(arr[i]) - mean) / std)
        if z_score > threshold:
            anomalies.append(
                AnomalyResult(
                    is_anomaly=True,
                    score=z_score,
                    threshold=threshold,
                    index=i,
                    value=float(arr[i]),
                    reason=f"Moving average z-score {z_score:.2f} exceeds threshold {threshold}",
                )
            )
    return anomalies


def calculate_trend(values: Sequence[float]) -> dict[str, float]:
    """Calculate linear trend and fit quality (R²) for a time series."""

    if len(values) < 2:
        return {"slope": 0.0, "intercept": 0.0, "r_squared": 0.0}

    x = np.arange(len(values), dtype=float)
    y = _as_float_array(values)
    slope, intercept = np.polyfit(x, y, 1)

    predicted = slope * x + intercept
    ss_res = float(np.sum((y - predicted) ** 2))
    ss_tot = float(np.sum((y - np.mean(y)) ** 2))
    r_squared = 1.0 - (ss_res / ss_tot) if ss_tot > 0 else 0.0

    return {
        "slope": float(slope),
        "intercept": float(intercept),
        "r_squared": float(r_squared),
    }


def calculate_volatility(values: Sequence[float], window: Optional[int] = None) -> float:
    """Calculate whole-series or rolling-window standard deviation."""

    if len(values) == 0:
        return 0.0
    if window is not None and window <= 0:
        raise ValueError("window must be > 0 when provided")

    arr = _as_float_array(values)
    if window is None or len(arr) < window:
        return float(np.std(arr))

    rolling = [float(np.std(arr[i - window : i])) for i in range(window, len(arr) + 1)]
    return float(np.mean(rolling)) if rolling else 0.0


def percentile(values: Sequence[float], p: float) -> float:
    """Calculate percentile `p` (0-100) for the series."""

    if p < 0 or p > 100:
        raise ValueError("p must be between 0 and 100")
    if len(values) == 0:
        return 0.0
    return float(np.percentile(_as_float_array(values), p))


def calculate_moving_average(values: Sequence[float], window: int) -> list[float]:
    """Calculate simple moving average with cumulative warm-up for early points."""

    if window <= 0:
        raise ValueError("window must be > 0")
    if len(values) == 0:
        return []

    arr = _as_float_array(values)
    result: list[float] = []
    for i in range(len(arr)):
        start = max(0, i - window + 1)
        result.append(float(np.mean(arr[start : i + 1])))
    return result


def exponential_smoothing(
    values: Sequence[float],
    alpha: float = 0.3,
) -> list[float]:
    """Apply exponential smoothing to a time series."""

    if alpha <= 0 or alpha > 1:
        raise ValueError("alpha must be in (0, 1]")
    if len(values) == 0:
        return []

    arr = _as_float_array(values)
    smoothed = [float(arr[0])]
    for idx in range(1, len(arr)):
        smoothed.append(alpha * float(arr[idx]) + (1 - alpha) * smoothed[-1])
    return smoothed


def detect_change_point(
    values: Sequence[float],
    min_size: int = 10,
    threshold: float = 2.0,
) -> Optional[int]:
    """Detect a change point by comparing mean shifts between two segments."""

    if min_size <= 0:
        raise ValueError("min_size must be > 0")
    if threshold <= 0:
        raise ValueError("threshold must be > 0")
    if len(values) < 2 * min_size:
        return None

    arr = _as_float_array(values)
    best_score = 0.0
    best_index: Optional[int] = None

    for cp in range(min_size, len(arr) - min_size):
        before = arr[:cp]
        after = arr[cp:]

        std_before = float(np.std(before))
        std_after = float(np.std(after))
        if std_before == 0 or std_after == 0:
            continue

        pooled_std = np.sqrt((std_before**2 / len(before)) + (std_after**2 / len(after)))
        if pooled_std == 0:
            continue

        mean_shift = float(np.mean(after) - np.mean(before))
        z_score = abs(mean_shift) / float(pooled_std)
        if z_score > threshold and z_score > best_score:
            best_score = z_score
            best_index = cp

    return best_index
