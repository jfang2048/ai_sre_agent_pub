package classifier

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func makeDataPoint(metrics map[string]float64, logs []queue.LogEntry) *queue.DataPoint {
	dp := &queue.DataPoint{
		NodeName:  "test-node",
		Timestamp: time.Now(),
	}
	for name, value := range metrics {
		dp.Metrics = append(dp.Metrics, queue.MetricData{Name: name, Value: value})
	}
	dp.Logs = logs
	return dp
}

// ── Rule-based classification tests ────────────────────────────────────

func TestClassifyCPUSaturationCritical(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{
		"system.cpu.usage": 95,
		"system.load.1m":   12,
	}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryCPUSaturation && r.Severity == SeverityCritical {
			found = true
			assert.Equal(t, "rules", r.Method)
			assert.InDelta(t, 0.90, r.Confidence, 0.01)
		}
	}
	assert.True(t, found, "Should detect critical CPU saturation at 95%%")
}

func TestClassifyCPUSaturationWarning(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{
		"system.cpu.usage": 85,
		"system.load.1m":   6,
	}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryCPUSaturation && r.Severity == SeverityWarning {
			found = true
		}
	}
	assert.True(t, found, "Should detect warning CPU saturation at 85%% + load 6")
}

func TestClassifyNoIssueForNormalMetrics(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{
		"system.cpu.usage":    30,
		"system.memory.usage": 50,
		"system.load.1m":      1,
	}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)
	assert.Empty(t, results, "Normal metrics should produce no classifications")
}

func TestClassifyMemoryPressureCritical(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{"system.memory.usage": 97}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryMemoryPressure && r.Severity == SeverityCritical {
			found = true
		}
	}
	assert.True(t, found, "Should classify 97%% memory as critical")
}

func TestClassifyDiskIOCritical(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{"system.disk.io.utilization": 95}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryDiskIOBottleneck && r.Severity == SeverityCritical {
			found = true
		}
	}
	assert.True(t, found, "Should classify 95%% disk IO as critical")
}

func TestClassifyNetworkSaturation(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{"system.network.rx.utilization": 92}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryNetworkSaturation && r.Severity == SeverityCritical {
			found = true
		}
	}
	assert.True(t, found, "Should classify 92%% rx utilization as critical network saturation")
}

func TestClassifyResourceLeak(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{
		"system.fd.allocated": 900,
		"system.fd.maximum":   1000,
	}, nil)

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryResourceLeak {
			found = true
			assert.Equal(t, SeverityWarning, r.Severity)
		}
	}
	assert.True(t, found, "Should detect resource leak when fd usage > 80%%")
}

// ── Log-based classification tests ──────────────────────────────────────

func TestClassifyOOMKilledFromLogs(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(nil, []queue.LogEntry{
		{Message: "Process OOMKilled after exceeding memory limit", Level: "error"},
	})

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryMemoryPressure && r.Severity == SeverityCritical {
			found = true
		}
	}
	assert.True(t, found, "Should classify OOMKilled log as critical memory pressure")
}

func TestClassifyDiskFullFromLogs(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(nil, []queue.LogEntry{
		{Message: "write /var/log/app.log: no space left on device", Level: "error"},
	})

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryDiskIOBottleneck {
			found = true
		}
	}
	assert.True(t, found, "Should classify 'no space left' log as disk issue")
}

func TestClassifyConnectionRefusedFromLogs(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(nil, []queue.LogEntry{
		{Message: "dial tcp 10.0.0.1:5432: connection refused", Level: "error"},
	})

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryExternalDependency {
			found = true
		}
	}
	assert.True(t, found, "Should classify connection refused log as external dependency issue")
}

func TestClassifySegfaultFromLogs(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(nil, []queue.LogEntry{
		{Message: "[1234] segmentation fault (core dumped)", Level: "error"},
	})

	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)

	found := false
	for _, r := range results {
		if r.Category == CategoryApplicationError && r.Severity == SeverityCritical {
			found = true
		}
	}
	assert.True(t, found, "Should classify segfault log as critical application error")
}

// ── Helper function tests ──────────────────────────────────────────────

func TestMetricsToMap(t *testing.T) {
	metrics := []queue.MetricData{
		{Name: "cpu", Value: 50.0},
		{Name: "mem", Value: 75.0},
	}

	m := metricsToMap(metrics)
	assert.Equal(t, 50.0, m["cpu"])
	assert.Equal(t, 75.0, m["mem"])
}

func TestMergeClassificationsPrefsHigherConfidence(t *testing.T) {
	rules := []Classification{
		{Category: CategoryCPUSaturation, Confidence: 0.7, Method: "rules"},
	}
	ml := []Classification{
		{Category: CategoryCPUSaturation, Confidence: 0.95, Method: "ml"},
	}

	merged := mergeClassifications(rules, ml)
	require.Len(t, merged, 1)
	assert.Equal(t, 0.95, merged[0].Confidence) // ML had higher confidence
	assert.Equal(t, "hybrid", merged[0].Method)
}

func TestDeduplicateAndSortBySeverity(t *testing.T) {
	input := []Classification{
		{Category: CategoryCPUSaturation, Severity: SeverityWarning},
		{Category: CategoryMemoryPressure, Severity: SeverityCritical},
		{Category: CategoryNetworkSaturation, Severity: SeverityError},
	}

	sorted := deduplicateAndSort(input)
	require.Len(t, sorted, 3)
	assert.Equal(t, SeverityCritical, sorted[0].Severity)
	assert.Equal(t, SeverityError, sorted[1].Severity)
	assert.Equal(t, SeverityWarning, sorted[2].Severity)
}

func TestClassifyWithNilMLClient(t *testing.T) {
	c := New(DefaultConfig(), zap.NewNop())
	ctx := context.Background()

	dp := makeDataPoint(map[string]float64{"system.cpu.usage": 96}, nil)
	results, err := c.Classify(ctx, dp)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "Should work without ML client")
}
