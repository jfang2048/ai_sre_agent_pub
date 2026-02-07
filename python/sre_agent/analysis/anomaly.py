"""Statistical analysis for SRE metrics."""

import numpy as np
from scipy import stats
from typing import List, Tuple, Dict, Any, Optional
from dataclasses import dataclass


@dataclass
class AnomalyResult:
    """Result of anomaly detection."""
    is_anomaly: bool
    score: float
    threshold: float
    index: int
    value: float
    reason: str


def detect_anomalies_zscore(
    values: List[float],
    threshold: float = 2.0,
) -> List[AnomalyResult]:
    """Detect anomalies using z-score method.

    Args:
        values: List of values to analyze
        threshold: Z-score threshold for anomaly detection

    Returns:
        List of AnomalyResult for detected anomalies
    """
    if len(values) < 3:
        return []

    arr = np.array(values)
    mean = np.mean(arr)
    std = np.std(arr)

    if std == 0:
        return []

    z_scores = np.abs((arr - mean) / std)

    anomalies = []
    for i, (z, val) in enumerate(zip(z_scores, arr)):
        if z > threshold:
            anomalies.append(AnomalyResult(
                is_anomaly=True,
                score=float(z),
                threshold=threshold,
                index=i,
                value=float(val),
                reason=f"Z-score {z:.2f} exceeds threshold {threshold}",
            ))

    return anomalies


def detect_anomalies_iqr(
    values: List[float],
    multiplier: float = 1.5,
) -> List[AnomalyResult]:
    """Detect anomalies using IQR (Interquartile Range) method.

    Args:
        values: List of values to analyze
        multiplier: IQR multiplier for outlier detection

    Returns:
        List of AnomalyResult for detected anomalies
    """
    if len(values) < 4:
        return []

    arr = np.array(values)
    q1 = np.percentile(arr, 25)
    q3 = np.percentile(arr, 75)
    iqr = q3 - q1

    lower_bound = q1 - multiplier * iqr
    upper_bound = q3 + multiplier * iqr

    anomalies = []
    for i, val in enumerate(arr):
        if val < lower_bound or val > upper_bound:
            anomalies.append(AnomalyResult(
                is_anomaly=True,
                score=0.0,  # IQR doesn't have a score
                threshold=multiplier,
                index=i,
                value=float(val),
                reason=f"Value {val} outside IQR bounds [{lower_bound:.2f}, {upper_bound:.2f}]",
            ))

    return anomalies


def detect_anomalies_moving_average(
    values: List[float],
    window: int = 5,
    threshold: float = 3.0,
) -> List[AnomalyResult]:
    """Detect anomalies using moving average method.

    Args:
        values: List of values to analyze
        window: Moving average window size
        threshold: Standard deviation threshold

    Returns:
        List of AnomalyResult for detected anomalies
    """
    if len(values) < window + 1:
        return []

    arr = np.array(values)
    anomalies = []

    for i in range(window, len(arr)):
        window_values = arr[i-window:i]
        mean = np.mean(window_values)
        std = np.std(window_values)

        if std == 0:
            continue

        z_score = abs((arr[i] - mean) / std)

        if z_score > threshold:
            anomalies.append(AnomalyResult(
                is_anomaly=True,
                score=float(z_score),
                threshold=threshold,
                index=i,
                value=float(arr[i]),
                reason=f"Moving average z-score {z_score:.2f} exceeds threshold {threshold}",
            ))

    return anomalies


def calculate_trend(values: List[float]) -> Dict[str, float]:
    """Calculate trend information for a time series.

    Args:
        values: List of values to analyze

    Returns:
        Dictionary with trend information
    """
    if len(values) < 2:
        return {"slope": 0.0, "intercept": 0.0, "r_squared": 0.0}

    x = np.arange(len(values))
    y = np.array(values)

    # Linear regression
    slope, intercept = np.polyfit(x, y, 1)

    # Calculate R-squared
    y_pred = slope * x + intercept
    ss_res = np.sum((y - y_pred) ** 2)
    ss_tot = np.sum((y - np.mean(y)) ** 2)
    r_squared = 1 - (ss_res / ss_tot) if ss_tot > 0 else 0

    return {
        "slope": float(slope),
        "intercept": float(intercept),
        "r_squared": float(r_squared),
    }


def calculate_volatility(values: List[float], window: Optional[int] = None) -> float:
    """Calculate volatility (standard deviation) of values.

    Args:
        values: List of values to analyze
        window: Optional window for rolling volatility

    Returns:
        Volatility measure
    """
    arr = np.array(values)

    if window is None:
        return float(np.std(arr))

    # Rolling volatility
    if len(arr) < window:
        return float(np.std(arr))

    volatilities = []
    for i in range(window, len(arr) + 1):
        volatilities.append(np.std(arr[i-window:i]))

    return float(np.mean(volatilities))


def percentile(values: List[float], p: float) -> float:
    """Calculate percentile of values.

    Args:
        values: List of values
        p: Percentile (0-100)

    Returns:
        Percentile value
    """
    if not values:
        return 0.0
    return float(np.percentile(values, p))


def calculate_moving_average(values: List[float], window: int) -> List[float]:
    """Calculate simple moving average.

    Args:
        values: List of values
        window: Window size

    Returns:
        List of moving average values
    """
    if len(values) < window:
        return [float(np.mean(values))] * len(values)

    result = []
    for i in range(len(values)):
        if i < window - 1:
            result.append(float(np.mean(values[:i+1])))
        else:
            result.append(float(np.mean(values[i-window+1:i+1])))

    return result


def exponential_smoothing(
    values: List[float],
    alpha: float = 0.3,
) -> List[float]:
    """Apply exponential smoothing to values.

    Args:
        values: List of values
        alpha: Smoothing factor (0-1)

    Returns:
        Smoothed values
    """
    if not values:
        return []

    smoothed = [float(values[0])]
    for i in range(1, len(values)):
        smoothed.append(
            alpha * float(values[i]) + (1 - alpha) * smoothed[-1]
        )

    return smoothed


def detect_change_point(
    values: List[float],
    min_size: int = 10,
    threshold: float = 2.0,
) -> Optional[int]:
    """Detect a change point in the time series.

    Uses a simple statistical test for change point detection.

    Args:
        values: List of values to analyze
        min_size: Minimum segment size
        threshold: Z-score threshold for change detection

    Returns:
        Index of change point, or None if no change detected
    """
    if len(values) < 2 * min_size:
        return None

    arr = np.array(values)
    best_score = 0
    best_cp = None

    for cp in range(min_size, len(arr) - min_size):
        before = arr[:cp]
        after = arr[cp:]

        # Compare means using z-test
        mean_before = np.mean(before)
        mean_after = np.mean(after)
        std_before = np.std(before)
        std_after = np.std(after)

        if std_before == 0 or std_after == 0:
            continue

        # Z-score for difference in means
        pooled_std = np.sqrt(
            (std_before ** 2 / len(before)) +
            (std_after ** 2 / len(after))
        )
        z_score = abs(mean_after - mean_before) / pooled_std

        if z_score > threshold and z_score > best_score:
            best_score = z_score
            best_cp = cp

    return best_cp
