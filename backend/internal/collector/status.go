package collector

import "strings"

type CollectorStatus struct {
	CollectorID      string                     `json:"collector_id,omitempty"`
	Hostname         string                     `json:"hostname,omitempty"`
	Version          string                     `json:"version,omitempty"`
	PrivilegeProfile string                     `json:"privilege_profile"`
	RuntimeMode      collectorRuntimeInspection `json:"runtime_mode"`
	Transport        CollectorTransportStatus   `json:"transport"`
	ProbeCore        CollectorProbeCoreStatus   `json:"probe_core"`
	EBPF             CollectorEBPFStatus        `json:"ebpf"`
	RuntimeSecurity  CollectorFeatureStatus     `json:"runtime_security"`
	GPUEvidence      CollectorFeatureStatus     `json:"gpu_evidence"`
	FallbackActive   bool                       `json:"fallback_active"`
	FallbackReason   string                     `json:"fallback_reason,omitempty"`
}

type CollectorTransportStatus struct {
	EndpointCount int    `json:"endpoint_count"`
	AuthEnabled   bool   `json:"auth_enabled"`
	TLSEnabled    bool   `json:"tls_enabled"`
	MTLSEnabled   bool   `json:"mtls_enabled"`
	Plaintext     bool   `json:"plaintext"`
	LastEndpoint  string `json:"last_endpoint,omitempty"`
	LastErrorKind string `json:"last_error_kind,omitempty"`
	Compressed    bool   `json:"compressed"`
}

type CollectorProbeCoreStatus struct {
	Enabled             bool `json:"enabled"`
	ClientAvailable     bool `json:"client_available"`
	PrimaryStarted      bool `json:"primary_started"`
	FallbackToGo        bool `json:"fallback_to_go"`
	CompatibilityActive bool `json:"compatibility_active"`
}

type CollectorEBPFStatus struct {
	Enabled  bool   `json:"enabled"`
	Expected bool   `json:"expected"`
	Healthy  bool   `json:"healthy"`
	Reason   string `json:"reason,omitempty"`
}

type CollectorFeatureStatus struct {
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
}

func (c *Collector) StatusSnapshot() CollectorStatus {
	status := CollectorStatus{
		PrivilegeProfile: defaultPrivilegeProfile,
	}
	if c == nil {
		return status
	}
	cfg := c.configSnapshot()
	c.mu.RLock()
	runtime := c.runtimeMode
	probeCoreAvailable := c.probeCore != nil
	runtimeSecurityActive := cfg.Security.Enabled && c.securityAuditor != nil
	transportClient := c.transport
	c.mu.RUnlock()
	pipeline := sourcePipelineStatus{}
	if c.sourcePipeline != nil {
		pipeline = c.sourcePipeline.status()
	}
	expected, healthy, reason := c.ebpfRuntimeStatus()
	status.CollectorID = strings.TrimSpace(cfg.CollectorID)
	status.Hostname = strings.TrimSpace(cfg.Hostname)
	status.Version = strings.TrimSpace(cfg.Version)
	status.PrivilegeProfile = cfg.PrivilegeProfile
	status.RuntimeMode = runtime
	status.FallbackActive = pipeline.CompatibilityActive
	status.FallbackReason = strings.TrimSpace(pipeline.FallbackReason)
	status.Transport = CollectorTransportStatus{
		EndpointCount: len(cfg.ControllerEndpoints),
		AuthEnabled:   cfg.Transport.Auth.Enabled,
		TLSEnabled:    cfg.Transport.TLS.Enabled,
		MTLSEnabled:   cfg.Transport.TLS.Enabled && strings.TrimSpace(cfg.Transport.TLS.CertFile) != "" && strings.TrimSpace(cfg.Transport.TLS.KeyFile) != "",
		Plaintext:     !cfg.Transport.TLS.Enabled,
	}
	if transportClient != nil {
		status.Transport.LastEndpoint = strings.TrimSpace(transportClient.LastEndpoint())
		status.Transport.LastErrorKind = strings.TrimSpace(transportClient.LastErrorKind())
		status.Transport.Compressed = transportClient.LastCompressed()
	}
	status.ProbeCore = CollectorProbeCoreStatus{
		Enabled:             cfg.ProbeCore.Enabled,
		ClientAvailable:     probeCoreAvailable,
		PrimaryStarted:      pipeline.PrimaryStarted,
		FallbackToGo:        cfg.ProbeCore.FallbackToGo,
		CompatibilityActive: pipeline.CompatibilityActive,
	}
	status.EBPF = CollectorEBPFStatus{
		Enabled:  cfg.EBPF.Enabled,
		Expected: expected,
		Healthy:  healthy,
		Reason:   strings.TrimSpace(reason),
	}
	status.RuntimeSecurity = CollectorFeatureStatus{
		Enabled: cfg.Security.Enabled,
		Active:  runtimeSecurityActive,
	}
	gpuExpected := cfg.PrivilegeProfile == PrivilegeProfileGPU || cfg.PrivilegeProfile == PrivilegeProfileDeepRuntime
	status.GPUEvidence = CollectorFeatureStatus{
		Enabled: gpuExpected,
		Active:  gpuExpected && cfg.ProbeCore.Enabled && probeCoreAvailable,
	}
	return status
}
