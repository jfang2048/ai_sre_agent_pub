"""Time-series forecasting for SRE metrics."""

import numpy as np
from typing import List, Dict, Any, Tuple
from dataclasses import dataclass


@dataclass
class ForecastResult:
    """Result of a forecast."""
    forecast: List[float]
    confidence_lower: List[float]
    confidence_upper: List[float]
    method: str
    horizon: int


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

    # Compute smoothed values
    smoothed = [float(values[0])]
    for i in range(1, len(values)):
        smoothed.append(
            alpha * float(values[i]) + (1 - alpha) * smoothed[-1]
        )

    # Forecast is the last smoothed value
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

    # Fit linear trend
    coeffs = np.polyfit(x, y, 1)
    trend = np.poly1d(coeffs)

    # Forecast
    forecast_x = len(values) + steps - 1
    return float(trend(forecast_x))


def double_exponential_smoothing(
    values: List[float],
    alpha: float = 0.3,
    beta: float = 0.1,
    steps: int = 1,
) -> List[float]:
    """Forecast using double exponential smoothing (Holt's method).

    Args:
        values: Historical values
        alpha: Level smoothing factor
        beta: Trend smoothing factor
        steps: Steps ahead to forecast

    Returns:
        List of forecasted values
    """
    if len(values) < 2:
        return [values[-1] if values else 0.0] * steps

    y = np.array(values, dtype=float)

    # Initialize level and trend
    level = y[0]
    trend = y[1] - y[0]

    # Fit model
    for i in range(1, len(y)):
        prev_level = level
        level = alpha * y[i] + (1 - alpha) * (level + trend)
        trend = beta * (level - prev_level) + (1 - beta) * trend

    # Forecast
    forecasts = []
    for h in range(1, steps + 1):
        forecasts.append(level + h * trend)

    return forecasts


def calculate_confidence_interval(
    values: List[float],
    forecast: float,
    confidence: float = 0.95,
) -> Tuple[float, float]:
    """Calculate confidence interval for a forecast.

    Args:
        values: Historical values
        forecast: Forecasted value
        confidence: Confidence level (0-1)

    Returns:
        Tuple of (lower_bound, upper_bound)
    """
    if len(values) < 2:
        return forecast * 0.9, forecast * 1.1

    # Calculate residuals from a simple mean forecast
    mean = np.mean(values)
    residuals = np.array(values) - mean
    std_error = np.std(residuals)

    # Use t-distribution for small samples
    from scipy import stats
    n = len(values)
    t_value = stats.t.ppf((1 + confidence) / 2, n - 1)
    margin = t_value * std_error * np.sqrt(1 + 1/n)

    return forecast - margin, forecast + margin


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

    # Determine if violation will occur
    if slo_target > current_sli:
        # Lower is better (e.g., latency, error rate)
        will_violate = predicted_value >= slo_target
    else:
        # Higher is better (e.g., availability, throughput)
        will_violate = predicted_value <= slo_target

    # Calculate time to violation
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
