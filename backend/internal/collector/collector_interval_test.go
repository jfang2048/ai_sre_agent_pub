package collector

import (
	"errors"
	"testing"
	"time"

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
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{cpuPercent: 95, spoolBacklog: 80}, errors.New("send failed"))
	require.Equal(t, 15*time.Second, next)
}

func TestNextIntervalSpeedsUpWhenIdle(t *testing.T) {
	c := &Collector{
		cfg: Config{
			AdaptivePolling:       true,
			CollectionInterval:    10 * time.Second,
			MinCollectionInterval: 2 * time.Second,
			MaxCollectionInterval: 30 * time.Second,
			SpoolMaxBytes:         100,
		},
		currentInterval: 10 * time.Second,
		promMetrics:     newRuntimePromMetrics(),
	}

	next := c.nextInterval(cycleSnapshot{cpuPercent: 20, spoolBacklog: 0}, nil)
	require.Equal(t, 8*time.Second, next)
}
