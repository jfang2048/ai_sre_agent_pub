package ingest

import (
	"math"
	"testing"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestValidateBatch(t *testing.T) {
	tests := []struct {
		name        string
		batch       *telemetryv1.TelemetryBatch
		errContains string
	}{
		{
			name: "valid batch",
			batch: &telemetryv1.TelemetryBatch{
				BatchId: "b-1",
				Collector: &telemetryv1.CollectorInfo{
					CollectorId: "node-1",
					Hostname:    "node-1",
				},
				Metrics: []*telemetryv1.Metric{
					{
						Name:  "node_cpu_usage_percent",
						Value: 42.5,
						Labels: []*telemetryv1.Label{
							{Key: "source", Value: "proc"},
						},
					},
				},
			},
		},
		{
			name: "missing collector id",
			batch: &telemetryv1.TelemetryBatch{
				BatchId: "b-2",
				Collector: &telemetryv1.CollectorInfo{
					CollectorId: "",
					Hostname:    "node-1",
				},
			},
			errContains: "collector_id is required",
		},
		{
			name: "metric with nan",
			batch: &telemetryv1.TelemetryBatch{
				BatchId: "b-3",
				Collector: &telemetryv1.CollectorInfo{
					CollectorId: "node-1",
					Hostname:    "node-1",
				},
				Metrics: []*telemetryv1.Metric{
					{Name: "node_cpu_usage_percent", Value: math.NaN()},
				},
			},
			errContains: "value must be finite",
		},
		{
			name: "label with control chars",
			batch: &telemetryv1.TelemetryBatch{
				BatchId: "b-4",
				Collector: &telemetryv1.CollectorInfo{
					CollectorId: "node-1",
					Hostname:    "node-1",
				},
				Metrics: []*telemetryv1.Metric{
					{
						Name:  "node_cpu_usage_percent",
						Value: 1,
						Labels: []*telemetryv1.Label{
							{Key: "source", Value: "proc\nbad"},
						},
					},
				},
			},
			errContains: "label contains control characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBatch(tt.batch)
			if tt.errContains == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.ErrorContains(t, err, tt.errContains)
		})
	}
}

func FuzzValidateLabel(f *testing.F) {
	f.Add("source", "proc")
	f.Add("", "proc")
	f.Add("key\nbad", "ok")
	f.Add("very-long-key", "very-long-value")

	f.Fuzz(func(t *testing.T, key string, value string) {
		label := &telemetryv1.Label{Key: key, Value: value}
		_ = validateLabel(label)
	})
}
