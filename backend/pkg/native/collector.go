//go:build linux

package native

import (
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
)

// Collector is a Go wrapper for the C++ system collector
// Currently stubbed - C++ implementation is experimental
type Collector struct{}

// NewCollector creates a new native collector
func NewCollector() *Collector {
	return &Collector{}
}

// Collect returns system metrics
// Currently stubbed - in production would interface with C++ code
func (c *Collector) Collect() []sources.Metric {
	_ = time.Now() // Placeholder for future use
	return []sources.Metric{}
}
