package collector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCollectorRuntimeProfileAppliesAndExpires(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CollectionInterval = 5 * time.Second
	cfg.MinCollectionInterval = 1 * time.Second
	cfg.MaxCollectionInterval = 20 * time.Second

	c := &Collector{
		cfg:             cfg,
		currentInterval: cfg.CollectionInterval,
		profileRuntime:  newRuntimeProfileRuntime(),
		logger:          zap.NewNop(),
	}
	now := time.Unix(1700000000, 0).UTC()
	c.profileRuntime.now = func() time.Time { return now }

	status := c.ApplyRuntimeProfile(RuntimeProfile{
		ProfileID:        "profile-1",
		SceneFamily:      "network_connectivity",
		AllowedModules:   []string{"metrics", "dns"},
		SamplingInterval: 2 * time.Second,
		ProcessTopK:      20,
		LogBudget:        16,
		TTL:              30 * time.Second,
	})
	require.Equal(t, "active", status.State)
	require.Equal(t, 2*time.Second, c.intervalSnapshot())
	require.Equal(t, 20, c.configSnapshot().TopK)

	now = now.Add(31 * time.Second)
	require.Equal(t, 5*time.Second, c.intervalSnapshot())
	require.Equal(t, "inactive", c.runtimeProfileStatusSnapshot().State)
}
