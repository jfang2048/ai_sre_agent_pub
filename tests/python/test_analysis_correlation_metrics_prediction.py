"""Unit tests for correlation, metrics, and prediction helpers."""

import unittest

import numpy as np

from _bootstrap import ensure_pythonpath
from _fixtures import (
    metrics_percentile_series,
    normal_prediction_series,
    shifted_series,
    slo_history_degrading,
)

ensure_pythonpath()


class TestCorrelationAnalysis(unittest.TestCase):
    """Cross-correlation and Pearson checks."""

    def test_pearson_correlation(self):
        from sre_agent.analysis.correlation import pearson_correlation

        corr = pearson_correlation([1, 2, 3, 4, 5], [2, 4, 6, 8, 10])
        self.assertAlmostEqual(corr, 1.0, places=5)

    def test_negative_correlation(self):
        from sre_agent.analysis.correlation import pearson_correlation

        corr = pearson_correlation([1, 2, 3, 4, 5], [5, 4, 3, 2, 1])
        self.assertAlmostEqual(corr, -1.0, places=5)

    def test_no_correlation(self):
        from sre_agent.analysis.correlation import pearson_correlation

        corr = pearson_correlation([1, 2, 3, 4, 5], [1, 1, 1, 1, 1])
        self.assertEqual(corr, 0)

    def test_cross_correlation_lag(self):
        from sre_agent.analysis.correlation import cross_correlation

        x, y = shifted_series()
        lags, correlations = cross_correlation(x, y, max_lag=2)
        max_lag = lags[int(np.argmax(correlations))]
        self.assertEqual(max_lag, 1)


class TestMetricsAnalysis(unittest.TestCase):
    """Metrics utility checks."""

    def test_calculate_sli(self):
        from sre_agent.analysis.metrics import calculate_availability_sli

        sli = calculate_availability_sli(total_requests=100, errors=5)
        self.assertAlmostEqual(sli, 0.95, places=2)

    def test_error_budget_calculation(self):
        from sre_agent.analysis.metrics import calculate_error_budget

        error_budget = calculate_error_budget(slo_target=0.999, current_sli=0.995)
        self.assertLess(error_budget, 0)

    def test_percentile_calculation(self):
        from sre_agent.analysis.metrics import percentile

        data = metrics_percentile_series()
        p50 = percentile(data, 50)
        p95 = percentile(data, 95)
        p99 = percentile(data, 99)

        self.assertAlmostEqual(p50, 5.5, places=1)
        self.assertGreater(p99, p95)
        self.assertGreater(p95, p50)


class TestIncidentPrediction(unittest.TestCase):
    """Prediction utility checks."""

    def test_baseline_prediction(self):
        from sre_agent.analysis.prediction import BaselinePredictor

        predictor = BaselinePredictor()
        predictor.train(normal_prediction_series())

        _, normal_confidence = predictor.predict(next_value=100)
        _, anomaly_confidence = predictor.predict(next_value=200)
        self.assertGreater(normal_confidence, 0.5)
        self.assertLess(anomaly_confidence, 0.5)

    def test_slo_violation_prediction(self):
        from sre_agent.analysis.prediction import predict_slo_violation

        prediction = predict_slo_violation(slo_history_degrading(), slo_target=0.995)
        self.assertTrue(prediction["will_violate"])
        self.assertIn("confidence", prediction)


if __name__ == "__main__":
    unittest.main()
