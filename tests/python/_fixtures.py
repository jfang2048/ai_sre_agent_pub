"""Shared fixtures for Python unit tests."""

from __future__ import annotations

from typing import Dict, List, Tuple


def anomaly_series_with_outlier() -> List[float]:
    return [10, 11, 10, 12, 11, 10, 11, 100, 10, 11]


def anomaly_series_iqr_outliers() -> List[float]:
    return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 100, -50]


def anomaly_series_stable() -> List[float]:
    return [10, 11, 10, 12, 11, 10, 11, 12, 10, 11]


def forecasting_series() -> List[float]:
    return [10, 12, 11, 13, 12, 14, 13, 15]


def linear_trend_series() -> List[float]:
    return [10, 12, 14, 16, 18, 20, 22, 24]


def shifted_series() -> Tuple[List[int], List[int]]:
    return [1, 2, 3, 4, 5], [0, 1, 2, 3, 4]


def metrics_percentile_series() -> List[float]:
    return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]


def normal_prediction_series() -> List[float]:
    return [100, 102, 98, 101, 99, 100, 101]


def slo_history_degrading() -> List[float]:
    return [0.999, 0.998, 0.997, 0.996, 0.995, 0.994]


def memory_pressure_metrics(value: float = 97.0) -> List[Dict[str, float | str]]:
    return [
        {"name": "system.memory.usage", "value": value},
        {"name": "system.cpu.usage", "value": 65.0},
    ]


def oom_logs(message: str = "OOM killed process worker") -> List[Dict[str, str]]:
    return [{"level": "error", "message": message}]
