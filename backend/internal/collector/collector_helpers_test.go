package collector

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "transport typed error",
			err: &transport.Error{
				Kind: transport.ErrorKindSend,
				Err:  errors.New("send failed"),
			},
			want: "send",
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: "context",
		},
		{
			name: "fallback unknown",
			err:  errors.New("other"),
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyError(tt.err))
		})
	}
}

func TestClampDuration(t *testing.T) {
	require.Equal(t, 2*time.Second, clampDuration(1*time.Second, 2*time.Second, 5*time.Second))
	require.Equal(t, 5*time.Second, clampDuration(6*time.Second, 2*time.Second, 5*time.Second))
	require.Equal(t, 3*time.Second, clampDuration(3*time.Second, 2*time.Second, 5*time.Second))
}

func TestMetricValue(t *testing.T) {
	metrics := []*telemetryv1.Metric{
		{Name: "a", Value: 1},
		{Name: "b", Value: 2},
	}
	require.Equal(t, 2.0, metricValue(metrics, "b"))
	require.Equal(t, 0.0, metricValue(metrics, "missing"))
}

func TestBuildLabelsSortsKeysAndSkipsBlank(t *testing.T) {
	labels := buildLabels(map[string]string{
		"z": "last",
		"":  "drop-me",
		"a": "first",
	})
	require.Len(t, labels, 2)
	require.Equal(t, "a", labels[0].Key)
	require.Equal(t, "first", labels[0].Value)
	require.Equal(t, "z", labels[1].Key)
	require.Equal(t, "last", labels[1].Value)
}

func TestConvertExternalMetricsValidatesNameAndValue(t *testing.T) {
	metrics, dropped := convertExternalMetrics([]extMetric{
		{Name: " ext_cpu_usage ", Value: 42.5, Labels: map[string]string{" host ": " node-a "}},
		{Name: "bad name with spaces", Value: 1},
		{Name: "ext_mem_usage", Value: math.NaN()},
	}, 123)

	require.Equal(t, 2, dropped)
	require.Len(t, metrics, 1)
	require.Equal(t, "ext_cpu_usage", metrics[0].Name)
	require.Equal(t, int64(123), metrics[0].TimestampUnixNano)
	require.Len(t, metrics[0].Labels, 1)
	require.Equal(t, "host", metrics[0].Labels[0].Key)
	require.Equal(t, "node-a", metrics[0].Labels[0].Value)
}

func TestParseExternalMetricCommandRejectsShellControlChars(t *testing.T) {
	_, _, err := parseExternalMetricCommand("echo ok; cat /etc/passwd")
	require.Error(t, err)
	require.ErrorContains(t, err, "shell control operators")
}

func TestParseExternalMetricCommandParsesBinaryAndArgs(t *testing.T) {
	name, args, err := parseExternalMetricCommand("/usr/local/bin/metrics --json --limit 10")
	require.NoError(t, err)
	require.Equal(t, "/usr/local/bin/metrics", name)
	require.Equal(t, []string{"--json", "--limit", "10"}, args)
}
