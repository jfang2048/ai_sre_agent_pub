package controller

import "github.com/jfang2048/ai_sre_agent_pub/internal/controller/timeseries"

// TSDBConfig controls optional controller-side durable telemetry storage.
type TSDBConfig = timeseries.Config

// DefaultTSDBConfig returns controller-side TSDB defaults.
func DefaultTSDBConfig() TSDBConfig {
	return timeseries.DefaultConfig()
}
