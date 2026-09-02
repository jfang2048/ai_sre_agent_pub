package collector

import (
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	"github.com/stretchr/testify/require"
)

func TestStatusSnapshotIncludesPrivilegeAndFallbackPosture(t *testing.T) {
	client, err := transport.New(transport.Config{
		Endpoints:      []string{"controller-a:9090"},
		AllowPlaintext: true,
		Auth: transport.AuthConfig{
			Enabled:     true,
			BearerToken: "collector-token",
		},
	}, nil)
	require.NoError(t, err)

	c := &Collector{
		cfg: Config{
			CollectorID:         "collector-a",
			Hostname:            "node-a",
			Version:             "v0.95",
			PrivilegeProfile:    PrivilegeProfileObservability,
			ControllerEndpoints: []string{"controller-a:9090"},
			Transport: TransportConfig{
				AllowPlaintext: true,
				Auth: TransportAuthConfig{
					Enabled: true,
				},
			},
			ProbeCore: ProbeCoreConfig{
				Enabled:      false,
				FallbackToGo: true,
			},
			EBPF:     EBPFConfig{Enabled: false},
			Security: SecurityConfig{Enabled: false},
		},
		transport: client,
		runtimeMode: collectorRuntimeInspection{
			RequestedMode: runtimeModeLimited,
			AppliedMode:   runtimeModeLimited,
			Degraded:      true,
			Reasons:       []string{"requested_host_mode_unavailable"},
		},
		sourcePipeline: &sourcePipeline{
			compatibilityActive: true,
			lastFallbackReason:  "probe_core_disabled",
		},
	}

	status := c.StatusSnapshot()
	require.Equal(t, PrivilegeProfileObservability, status.PrivilegeProfile)
	require.True(t, status.FallbackActive)
	require.Equal(t, "probe_core_disabled", status.FallbackReason)
	require.True(t, status.Transport.AuthEnabled)
	require.True(t, status.Transport.Plaintext)
	require.False(t, status.ProbeCore.Enabled)
	require.False(t, status.GPUEvidence.Enabled)
}
