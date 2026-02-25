"""Unit tests for anomaly detection and forecasting helpers."""

import unittest

from _bootstrap import ensure_pythonpath
from _fixtures import (
    anomaly_series_iqr_outliers,
    anomaly_series_stable,
    anomaly_series_with_outlier,
    forecasting_series,
    linear_trend_series,
)

ensure_pythonpath()


class TestAnomalyDetection(unittest.TestCase):
    """Statistical anomaly detection checks."""

    def test_zscore_detection(self):
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        anomalies = detect_anomalies_zscore(anomaly_series_with_outlier(), threshold=2.0)
        self.assertEqual(len(anomalies), 1)
        self.assertEqual(anomalies[0].index, 7)
        self.assertEqual(anomalies[0].value, 100)

    def test_iqr_detection(self):
        from sre_agent.analysis.anomaly import detect_anomalies_iqr

        anomalies = detect_anomalies_iqr(anomaly_series_iqr_outliers(), multiplier=1.5)
        self.assertGreaterEqual(len(anomalies), 2)

    def test_no_anomalies(self):
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        anomalies = detect_anomalies_zscore(anomaly_series_stable(), threshold=3.0)
        self.assertEqual(len(anomalies), 0)

    def test_empty_data(self):
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        anomalies = detect_anomalies_zscore([], threshold=2.0)
        self.assertEqual(len(anomalies), 0)


class TestTimeSeriesForecasting(unittest.TestCase):
    """Forecasting helper checks."""

    def test_simple_moving_average(self):
        from sre_agent.analysis.forecast import simple_moving_average

        data = forecasting_series()
        forecast = simple_moving_average(data, window=3)
        expected = (13 + 14 + 15) / 3
        self.assertAlmostEqual(forecast, expected, places=1)

    def test_exponential_smoothing(self):
        from sre_agent.analysis.forecast import exponential_smoothing

        data = forecasting_series()
        forecast = exponential_smoothing(data, alpha=0.3)
        self.assertGreater(forecast, min(data))
        self.assertLess(forecast, max(data))

    def test_linear_trend(self):
        from sre_agent.analysis.forecast import linear_trend_forecast

        data = linear_trend_series()
        forecast = linear_trend_forecast(data, steps=1)
        self.assertGreater(forecast, data[-1])

    def test_empty_forecast_data(self):
        from sre_agent.analysis.forecast import simple_moving_average

        forecast = simple_moving_average([1, 2], window=3)
        self.assertIsNotNone(forecast)


if __name__ == "__main__":
    unittest.main()
