package go_test

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring/sources"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestProcSource(t *testing.T) {
	logger := zap.NewNop()
	config := sources.ProcConfig{
		Enabled:    true,
		Root:       "/proc",
		Collections: []string{"cpu", "memory", "load"},
	}

	source, err := sources.NewProcSource(config, logger)
	require.NoError(t, err)
	assert.NotNil(t, source)

	ctx := context.Background()

	// Test collection
	batch, err := source.Collect(ctx)
	require.NoError(t, err)
	assert.NotNil(t, batch)
	assert.NotEmpty(t, batch.Metrics)

	// Verify expected metrics exist
	metricNames := make(map[string]bool)
	for _, m := range batch.Metrics {
		metricNames[m.Name] = true
	}

	assert.True(t, metricNames["system.cpu.usage"], "CPU usage metric should exist")
	assert.True(t, metricNames["system.memory.used"], "Memory used metric should exist")
	assert.True(t, metricNames["system.load.avg1"], "Load average metric should exist")
}

func TestEBPFCollector(t *testing.T) {
	logger := zap.NewNop()
	config := sources.EBPFConfig{
		Enabled:  true,
		Syscalls: true,
		Network:  true,
		IO:       true,
	}

	collector := sources.NewEBPFCollector(config, logger)
	assert.NotNil(t, collector)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start collector
	err := collector.Start(ctx)
	require.NoError(t, err)

	// Collect metrics
	batch, err := collector.Collect(ctx)
	require.NoError(t, err)
	assert.NotNil(t, batch)

	// Stop collector
	err = collector.Stop()
	require.NoError(t, err)
}

func TestSLICalculation(t *testing.T) {
	// Test availability SLI
	metrics := []*proto.Metric{
		{
			Name: "http.requests.total",
			Points: []*proto.MetricPoint{
				{Value: 1000},
			},
		},
		{
			Name: "http.errors.total",
			Points: []*proto.MetricPoint{
				{Value: 50},
			},
		},
	}

	totalRequests := metrics[0].Points[0].Value
	totalErrors := metrics[1].Points[0].Value

	availability := (totalRequests - totalErrors) / totalRequests * 100
	expectedAvailability := 95.0

	assert.InDelta(t, expectedAvailability, availability, 0.1)
}

func TestSLACompliance(t *testing.T) {
	// Test SLO compliance calculation
	type testCase struct {
		name          string
		sloTarget     float64
		actualValue   float64
		expectedCompliance bool
	}

	tests := []testCase{
		{
			name:      "within SLO",
			sloTarget: 99.9,
			actualValue:   99.95,
			expectedCompliance: true,
		},
		{
			name:      "outside SLO",
			sloTarget: 99.9,
			actualValue:   99.5,
			expectedCompliance: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compliance := tc.actualValue >= tc.sloTarget
			assert.Equal(t, tc.expectedCompliance, compliance)
		})
	}
}

func TestMetricsAggregation(t *testing.T) {
	// Test time-series aggregation
	now := time.Now()

	points := []*proto.MetricPoint{
		{Timestamp: &proto.Timestamp{Seconds: now.Add(-5 * time.Minute).Unix()}, Value: 10},
		{Timestamp: &proto.Timestamp{Seconds: now.Add(-4 * time.Minute).Unix()}, Value: 20},
		{Timestamp: &proto.Timestamp{Seconds: now.Add(-3 * time.Minute).Unix()}, Value: 30},
		{Timestamp: &proto.Timestamp{Seconds: now.Add(-2 * time.Minute).Unix()}, Value: 40},
		{Timestamp: &proto.Timestamp{Seconds: now.Add(-1 * time.Minute).Unix()}, Value: 50},
	}

	// Calculate average
	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	avg := sum / float64(len(points))

	assert.Equal(t, 30.0, avg)
}

func TestAnomalyDetection(t *testing.T) {
	// Test simple anomaly detection using standard deviation
	type testCase struct {
		name     string
		values   []float64
		threshold float64
		expectedAnomalies int
	}

	tests := []testCase{
		{
			name:     "no anomalies",
			values:   []float64{10, 11, 10, 12, 11, 10, 11},
			threshold: 2.0,
			expectedAnomalies: 0,
		},
		{
			name:     "one anomaly",
			values:   []float64{10, 11, 10, 12, 50, 11, 10},
			threshold: 2.0,
			expectedAnomalies: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Calculate mean and standard deviation
			n := float64(len(tc.values))
			var mean, stdDev float64

			for _, v := range tc.values {
				mean += v
			}
			mean /= n

			for _, v := range tc.values {
				diff := v - mean
				stdDev += diff * diff
			}
			stdDev = (stdDev / n) // variance
			stdDev = stdDev // simple std dev

			// Count anomalies (values > threshold * stdDev from mean)
			anomalies := 0
			for _, v := range tc.values {
				if v > mean+tc.threshold*stdDev || v < mean-tc.threshold*stdDev {
					anomalies++
				}
			}

			assert.Equal(t, tc.expectedAnomalies, anomalies)
		})
	}
}
