package collector

import (
	"errors"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	"github.com/stretchr/testify/require"
)

func TestNextIntervalBacksOffOnHighLoad(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
		},
		currentInterval: 10 * time.Second,
		jitterUnit:      func() float64 { return 0.5 },
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{cpuPercent: 95, spoolBacklog: 80}, errors.New("send failed"))
	require.Equal(t, 15*time.Second, next)
}

func TestNextIntervalSpeedsUpDuringEmergingIncident(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
		},
		currentInterval: 10 * time.Second,
		jitterUnit:      func() float64 { return 0.5 },
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{cpuPercent: 42, spoolBacklog: 0, signalPressure: 2}, nil)
	require.Equal(t, 7500*time.Millisecond, next)
}

func TestNextIntervalReturnsBaselineWhenCalm(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
		},
		currentInterval: 6 * time.Second,
		jitterUnit:      func() float64 { return 0.5 },
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{cpuPercent: 20, spoolBacklog: 0}, nil)
	require.Equal(t, 10*time.Second, next)
}

func TestNextIntervalUsesExponentialBackoffForTransientFailures(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
		},
		currentInterval: 10 * time.Second,
		jitterUnit:      func() float64 { return 0.5 },
		promMetrics:     newRuntimePromMetrics(),
	}

	err := &transport.Error{Kind: transport.ErrorKindRetryExhaust, Err: errors.New("network timeout")}

	first := c.nextInterval(cycleSnapshot{}, err)
	second := c.nextInterval(cycleSnapshot{}, err)

	require.Equal(t, 15*time.Second, first)
	require.Equal(t, 22500*time.Millisecond, second)
}

func TestNextIntervalBacksOffAggressivelyInCriticalProtectionMode(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
			Protection: ProtectionConfig{
				MaxCPUPercent: 5,
			},
		},
		currentInterval: 10 * time.Second,
		jitterUnit:      func() float64 { return 0.5 },
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{protectionMode: protectionModeCritical}, nil)
	require.Equal(t, 20*time.Second, next)
}
