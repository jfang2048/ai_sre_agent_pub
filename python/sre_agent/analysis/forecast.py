"""Time-series forecasting for SRE metrics."""

from typing import Any, Dict, List

import numpy as np


def simple_moving_average(
    values: List[float],
    window: int,
    steps: int = 1,
) -> float:
    """Forecast using simple moving average.

    Args:
        values: Historical values
        window: Moving average window size
        steps: Number of steps to forecast ahead

    Returns:
        Forecasted value
    """
    if len(values) < window:
        return float(np.mean(values)) if values else 0.0

    return float(np.mean(values[-window:]))


def exponential_smoothing_forecast(
    values: List[float],
    alpha: float = 0.3,
    steps: int = 1,
) -> float:
    """Forecast using exponential smoothing.

    Args:
        values: Historical values
        alpha: Smoothing factor
        steps: Steps ahead to forecast

    Returns:
        Forecasted value
    """
    if not values:
        return 0.0

    smoothed = [float(values[0])]
    for i in range(1, len(values)):
        smoothed.append(alpha * float(values[i]) + (1 - alpha) * smoothed[-1])

    return smoothed[-1]


def linear_trend_forecast(
    values: List[float],
    steps: int = 1,
) -> float:
    """Forecast using linear trend extrapolation.

    Args:
        values: Historical values
        steps: Steps ahead to forecast

    Returns:
        Forecasted value
    """
    if len(values) < 2:
        return values[-1] if values else 0.0

    x = np.arange(len(values))
    y = np.array(values)

    coeffs = np.polyfit(x, y, 1)
    trend = np.poly1d(coeffs)

    forecast_x = len(values) + steps - 1
    return float(trend(forecast_x))


def predict_slo_violation(
    current_sli: float,
    slo_target: float,
    trend: float,
    window_minutes: int = 60,
) -> Dict[str, Any]:
    """Predict if an SLO violation will occur.

    Args:
        current_sli: Current SLI value
        slo_target: SLO target value
        trend: Rate of change per minute
        window_minutes: Time window for prediction

    Returns:
        Dictionary with prediction results
    """
    predicted_value = current_sli + (trend * window_minutes)

    if slo_target > current_sli:
        will_violate = predicted_value >= slo_target
    else:
        will_violate = predicted_value <= slo_target

    time_to_violation = None
    if will_violate and trend != 0:
        if slo_target > current_sli:
            time_to_violation = (slo_target - current_sli) / trend
        else:
            time_to_violation = (current_sli - slo_target) / (-trend)

    confidence = min(0.95, abs(trend) * 10)
    confidence = max(0.5, confidence)

    return {
        "will_violate": will_violate,
        "predicted_value": predicted_value,
        "confidence": confidence,
        "time_to_violation_minutes": time_to_violation,
        "trend_per_minute": trend,
    }
