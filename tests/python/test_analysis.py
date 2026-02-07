"""
Unit tests for SRE Agent Python analysis modules.

Tests anomaly detection, forecasting, and correlation analysis.
"""

import unittest
import numpy as np
from unittest.mock import Mock, patch, MagicMock
import sys
import os

# Add the python package to the path
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..', '..', 'python'))


class TestAnomalyDetection(unittest.TestCase):
    """Test statistical anomaly detection."""

    def test_zscore_detection(self):
        """Test z-score based anomaly detection."""
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        # Normal data with one anomaly
        data = [10, 11, 10, 12, 11, 10, 11, 100, 10, 11]
        anomalies = detect_anomalies_zscore(data, threshold=2.0)

        self.assertEqual(len(anomalies), 1)
        self.assertEqual(anomalies[0]['index'], 7)
        self.assertEqual(anomalies[0]['value'], 100)

    def test_iqr_detection(self):
        """Test IQR (Interquartile Range) based anomaly detection."""
        from sre_agent.analysis.anomaly import detect_anomalies_iqr

        # Data with outliers
        data = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 100, -50]
        anomalies = detect_anomalies_iqr(data, multiplier=1.5)

        self.assertGreaterEqual(len(anomalies), 2)

    def test_no_anomalies(self):
        """Test with data containing no anomalies."""
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        data = [10, 11, 10, 12, 11, 10, 11, 12, 10, 11]
        anomalies = detect_anomalies_zscore(data, threshold=3.0)

        self.assertEqual(len(anomalies), 0)

    def test_empty_data(self):
        """Test with empty data."""
        from sre_agent.analysis.anomaly import detect_anomalies_zscore

        data = []
        anomalies = detect_anomalies_zscore(data, threshold=2.0)

        self.assertEqual(len(anomalies), 0)


class TestTimeSeriesForecasting(unittest.TestCase):
    """Test time-series forecasting."""

    def test_simple_moving_average(self):
        """Test simple moving average forecast."""
        from sre_agent.analysis.forecast import simple_moving_average

        data = [10, 12, 11, 13, 12, 14, 13, 15]
        forecast = simple_moving_average(data, window=3)

        # Forecast should be close to recent average
        expected = (13 + 14 + 15) / 3
        self.assertAlmostEqual(forecast, expected, places=1)

    def test_exponential_smoothing(self):
        """Test exponential smoothing forecast."""
        from sre_agent.analysis.forecast import exponential_smoothing

        data = [10, 12, 11, 13, 12, 14, 13, 15]
        forecast = exponential_smoothing(data, alpha=0.3)

        # Forecast should be between min and max
        self.assertGreater(forecast, min(data))
        self.assertLess(forecast, max(data))

    def test_linear_trend(self):
        """Test linear trend forecasting."""
        from sre_agent.analysis.forecast import linear_trend_forecast

        # Data with clear upward trend
        data = [10, 12, 14, 16, 18, 20, 22, 24]
        forecast = linear_trend_forecast(data, steps=1)

        # Forecast should continue the trend
        self.assertGreater(forecast, data[-1])

    def test_empty_forecast_data(self):
        """Test forecasting with insufficient data."""
        from sre_agent.analysis.forecast import simple_moving_average

        data = [1, 2]  # Not enough for window=3
        forecast = simple_moving_average(data, window=3)

        # Should fall back to available data
        self.assertIsNotNone(forecast)


class TestCorrelationAnalysis(unittest.TestCase):
    """Test cross-correlation analysis."""

    def test_pearson_correlation(self):
        """Test Pearson correlation coefficient."""
        from sre_agent.analysis.correlation import pearson_correlation

        # Perfect positive correlation
        x = [1, 2, 3, 4, 5]
        y = [2, 4, 6, 8, 10]

        corr = pearson_correlation(x, y)
        self.assertAlmostEqual(corr, 1.0, places=5)

    def test_negative_correlation(self):
        """Test negative correlation."""
        from sre_agent.analysis.correlation import pearson_correlation

        # Perfect negative correlation
        x = [1, 2, 3, 4, 5]
        y = [5, 4, 3, 2, 1]

        corr = pearson_correlation(x, y)
        self.assertAlmostEqual(corr, -1.0, places=5)

    def test_no_correlation(self):
        """Test no correlation."""
        from sre_agent.analysis.correlation import pearson_correlation

        x = [1, 2, 3, 4, 5]
        y = [1, 1, 1, 1, 1]

        # Zero variance in y should return 0
        corr = pearson_correlation(x, y)
        self.assertEqual(corr, 0)

    def test_cross_correlation_lag(self):
        """Test cross-correlation with lag."""
        from sre_agent.analysis.correlation import cross_correlation

        x = [1, 2, 3, 4, 5]
        y = [0, 1, 2, 3, 4]  # x shifted by 1

        # Find the lag with maximum correlation
        lags, correlations = cross_correlation(x, y, max_lag=2)
        max_lag = lags[np.argmax(correlations)]

        self.assertEqual(max_lag, 1)


class TestLLMClient(unittest.TestCase):
    """Test LLM client integration."""

    @patch('sre_agent.llm.openai_client.OpenAI')
    def test_openai_client_init(self, mock_openai):
        """Test OpenAI client initialization."""
        from sre_agent.llm.openai_client import OpenAIClient

        client = OpenAIClient(api_key="test-key")
        self.assertIsNotNone(client)
        self.assertEqual(client.api_key, "test-key")

    @patch('sre_agent.llm.anthropic_client.Anthropic')
    def test_anthropic_client_init(self, mock_anthropic):
        """Test Anthropic client initialization."""
        from sre_agent.llm.anthropic_client import AnthropicClient

        client = AnthropicClient(api_key="test-key")
        self.assertIsNotNone(client)
        self.assertEqual(client.api_key, "test-key")

    def test_prompt_builder_basic(self):
        """Test basic prompt building."""
        from sre_agent.llm.prompt_builder import PromptBuilder

        builder = PromptBuilder()
        prompt = builder.build_system_prompt(
            role="SRE Analyst",
            capabilities=["analyze metrics", "predict failures"]
        )

        self.assertIn("SRE Analyst", prompt)
        self.assertIn("analyze metrics", prompt)


class TestMetricsAnalysis(unittest.TestCase):
    """Test metrics analysis utilities."""

    def test_calculate_sli(self):
        """Test SLI (Service Level Indicator) calculation."""
        from sre_agent.analysis.metrics import calculate_availability_sli

        # 100 requests, 5 errors
        total_requests = 100
        errors = 5

        sli = calculate_availability_sli(total_requests, errors)
        expected = 0.95

        self.assertAlmostEqual(sli, expected, places=2)

    def test_error_budget_calculation(self):
        """Test error budget calculation."""
        from sre_agent.analysis.metrics import calculate_error_budget

        slo_target = 0.999  # 99.9%
        current_sli = 0.995  # 99.5%

        error_budget = calculate_error_budget(slo_target, current_sli)

        # Error budget should be negative (SLO not met)
        self.assertLess(error_budget, 0)

    def test_percentile_calculation(self):
        """Test percentile calculation."""
        from sre_agent.analysis.metrics import percentile

        data = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]

        p50 = percentile(data, 50)
        p95 = percentile(data, 95)
        p99 = percentile(data, 99)

        self.assertAlmostEqual(p50, 5.5, places=1)
        self.assertGreater(p99, p95)
        self.assertGreater(p95, p50)


class TestIncidentPrediction(unittest.TestCase):
    """Test incident prediction models."""

    def test_baseline_prediction(self):
        """Test baseline prediction model."""
        from sre_agent.analysis.prediction import BaselinePredictor

        predictor = BaselinePredictor()

        # Train on normal data
        normal_data = [100, 102, 98, 101, 99, 100, 101]
        predictor.train(normal_data)

        # Predict normal behavior
        prediction, confidence = predictor.predict(next_value=100)
        self.assertGreater(confidence, 0.5)

        # Predict anomalous behavior
        prediction, confidence = predictor.predict(next_value=200)
        self.assertLess(confidence, 0.5)

    def test_slo_violation_prediction(self):
        """Test SLO violation prediction."""
        from sre_agent.analysis.prediction import predict_slo_violation

        # Simulated metric history
        history = [0.999, 0.998, 0.997, 0.996, 0.995, 0.994]
        slo_target = 0.995

        prediction = predict_slo_violation(history, slo_target)

        # Should predict violation
        self.assertTrue(prediction['will_violate'])
        self.assertIn('confidence', prediction)


if __name__ == '__main__':
    unittest.main()
