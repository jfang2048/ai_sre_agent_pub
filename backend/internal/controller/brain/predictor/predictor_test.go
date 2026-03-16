package predictor

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto"
	metricspb "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── calculateTrend tests ───────────────────────────────────────────────

func TestCalculateTrendUpward(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50}
	trend := calculateTrend(values)
	assert.InDelta(t, 10.0, trend, 0.01, "Linear upward trend should be 10 per step")
}

func TestCalculateTrendDownward(t *testing.T) {
	values := []float64{100, 80, 60, 40, 20}
	trend := calculateTrend(values)
	assert.InDelta(t, -20.0, trend, 0.01, "Linear downward trend should be -20 per step")
}

func TestCalculateTrendFlat(t *testing.T) {
	values := []float64{50, 50, 50, 50}
	trend := calculateTrend(values)
	assert.InDelta(t, 0.0, trend, 0.01, "Constant values should have zero trend")
}

func TestCalculateTrendSingleValue(t *testing.T) {
	trend := calculateTrend([]float64{42})
	assert.Equal(t, 0.0, trend, "Single value should return 0 trend")
}

func TestCalculateTrendEmpty(t *testing.T) {
	trend := calculateTrend([]float64{})
	assert.Equal(t, 0.0, trend, "Empty values should return 0 trend")
}

func TestCalculateTrendTwoPoints(t *testing.T) {
	trend := calculateTrend([]float64{10, 20})
	assert.InDelta(t, 10.0, trend, 0.01, "Two points should compute slope correctly")
}

// ── calculateAnomalyScore tests ────────────────────────────────────────

func TestAnomalyScoreNormalValues(t *testing.T) {
	// Values around mean=50, stddev≈0.7, last value at mean
	values := []float64{50, 51, 49, 50, 50, 51, 49, 50, 50, 50}
	score := calculateAnomalyScore(values)
	assert.Less(t, score, 0.3, "Normal values should have a low anomaly score")
}

func TestAnomalyScoreWithSpike(t *testing.T) {
	// Stable around 50, then spike to 200
	values := []float64{50, 50, 50, 50, 50, 50, 50, 50, 50, 200}
	score := calculateAnomalyScore(values)
	assert.Greater(t, score, 0.7, "Large spike should produce high anomaly score")
}

func TestAnomalyScoreBoundedTo1(t *testing.T) {
	// Extreme spike
	values := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 10000}
	score := calculateAnomalyScore(values)
	assert.LessOrEqual(t, score, 1.0, "Anomaly score should be capped at 1.0")
}

func TestAnomalyScoreIdenticalValues(t *testing.T) {
	// All the same → stddev=0 → divide by zero case
	values := []float64{50, 50, 50, 50, 50, 50, 50, 50, 50, 50}
	score := calculateAnomalyScore(values)
	assert.Equal(t, 0.0, score, "Identical values should not produce a fake anomaly")
}

// ── extractMetricValues tests ──────────────────────────────────────────

func TestExtractMetricValues(t *testing.T) {
	metrics := []*metricspb.Metric{
		{
			Name: "cpu",
			Points: []*metricspb.MetricPoint{
				{Value: 10},
				{Value: 20},
			},
		},
		{
			Name: "mem",
			Points: []*metricspb.MetricPoint{
				{Value: 30},
			},
		},
	}

	values := extractMetricValues(metrics)
	require.Len(t, values, 3)
	assert.Equal(t, 10.0, values[0])
	assert.Equal(t, 20.0, values[1])
	assert.Equal(t, 30.0, values[2])
}

func TestExtractMetricValuesEmpty(t *testing.T) {
	values := extractMetricValues(nil)
	assert.Empty(t, values)
}

func TestExtractMetricValuesFromMetric(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "cpu",
		Points: []*metricspb.MetricPoint{
			{Value: 10},
			{Value: 20},
			{Value: 30},
		},
	}

	values := extractMetricValuesFromMetric(metric)
	assert.Equal(t, []float64{10, 20, 30}, values)
}

// ── combinePredictions tests ───────────────────────────────────────────

func TestCombinePredictionsBothViolate(t *testing.T) {
	now := time.Now()
	stat := &SLOPrediction{
		SLOName:       "latency_p99",
		WillViolate:   true,
		Confidence:    0.8,
		ViolationTime: now.Add(10 * time.Minute),
	}
	llm := &SLOPrediction{
		SLOName:          "latency_p99",
		WillViolate:      true,
		Confidence:       0.9,
		ViolationTime:    now.Add(5 * time.Minute), // earlier
		SuggestedActions: []string{"scale up"},
	}

	p := NewPredictor(nil, mustNopLogger())
	result := p.combinePredictions(stat, llm)

	assert.True(t, result.WillViolate)
	assert.Equal(t, now.Add(5*time.Minute), result.ViolationTime, "Should use earlier violation time")
	assert.Contains(t, result.SuggestedActions, "scale up")
}

func TestCombinePredictionsHighLLMConfidence(t *testing.T) {
	stat := &SLOPrediction{WillViolate: false, Confidence: 0.5}
	llm := &SLOPrediction{WillViolate: true, Confidence: 0.9} // high confidence → llmWeight=0.7

	p := NewPredictor(nil, mustNopLogger())
	result := p.combinePredictions(stat, llm)

	assert.True(t, result.WillViolate, "High-confidence LLM should dominate")
	// combinedConf = 0.5*0.3 + 0.9*0.7 = 0.15 + 0.63 = 0.78
	assert.InDelta(t, 0.78, result.Confidence, 0.01)
}

func TestCombinePredictionsLowLLMConfidence(t *testing.T) {
	stat := &SLOPrediction{WillViolate: true, Confidence: 0.8}
	llm := &SLOPrediction{WillViolate: false, Confidence: 0.2} // low confidence → statWeight=0.8

	p := NewPredictor(nil, mustNopLogger())
	result := p.combinePredictions(stat, llm)

	assert.True(t, result.WillViolate, "High-confidence stat should dominate when LLM is low")
	// combinedConf = 0.8*0.8 + 0.2*0.2 = 0.64 + 0.04 = 0.68
	assert.InDelta(t, 0.68, result.Confidence, 0.01)
}

func TestPredictSLOViolationUsesSampleTimestamps(t *testing.T) {
	p := NewPredictor(nil, mustNopLogger())
	now := time.Now().UTC()
	slo := &proto.SLO{
		Name: "p99-latency",
		Type: proto.SLOType_SLO_TYPE_LATENCY,
		Target: &proto.SLO_LatencyTarget{
			LatencyTarget: &proto.LatencyTarget{Value: 120, Unit: "ms", Percentile: 99},
		},
	}
	history := []*metricspb.Metric{
		{
			Name: "latency_p99",
			Points: []*metricspb.MetricPoint{
				{Timestamp: timestamppb.New(now.Add(-4 * time.Minute)), Value: 60},
				{Timestamp: timestamppb.New(now.Add(-3 * time.Minute)), Value: 70},
				{Timestamp: timestamppb.New(now.Add(-2 * time.Minute)), Value: 80},
				{Timestamp: timestamppb.New(now.Add(-1 * time.Minute)), Value: 90},
			},
		},
	}

	prediction, err := p.PredictSLOViolation(context.Background(), slo, history)
	require.NoError(t, err)
	require.True(t, prediction.WillViolate)
	require.Contains(t, prediction.Reasoning, "steady")
	require.NotEmpty(t, prediction.SuggestedActions)
	require.WithinDuration(t, time.Now().Add(3*time.Minute), prediction.ViolationTime, 20*time.Second)
}

func TestPredictAnomalyAddsReasonAndExtrapolation(t *testing.T) {
	p := NewPredictor(nil, mustNopLogger())
	now := time.Now().UTC()
	metric := &metricspb.Metric{
		Name: "queue_depth",
		Points: []*metricspb.MetricPoint{
			{Timestamp: timestamppb.New(now.Add(-10 * time.Minute)), Value: 3},
			{Timestamp: timestamppb.New(now.Add(-9 * time.Minute)), Value: 3},
			{Timestamp: timestamppb.New(now.Add(-8 * time.Minute)), Value: 4},
			{Timestamp: timestamppb.New(now.Add(-7 * time.Minute)), Value: 4},
			{Timestamp: timestamppb.New(now.Add(-6 * time.Minute)), Value: 4},
			{Timestamp: timestamppb.New(now.Add(-5 * time.Minute)), Value: 5},
			{Timestamp: timestamppb.New(now.Add(-4 * time.Minute)), Value: 5},
			{Timestamp: timestamppb.New(now.Add(-3 * time.Minute)), Value: 5},
			{Timestamp: timestamppb.New(now.Add(-2 * time.Minute)), Value: 6},
			{Timestamp: timestamppb.New(now.Add(-1 * time.Minute)), Value: 18},
		},
	}

	prediction, err := p.PredictAnomaly(context.Background(), []*metricspb.Metric{metric})
	require.NoError(t, err)
	require.True(t, prediction.WillBeAnomalous)
	require.NotEmpty(t, prediction.Reason)
	require.Len(t, prediction.PredictedValues, 3)
}

func mustNopLogger() *zap.Logger {
	return zap.NewNop()
}
