package controller

import (
	"context"
	"fmt"
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

type controllerIngestDurabilityStatus struct {
	PersistenceConfigured bool   `json:"persistence_configured"`
	PersistenceEnabled    bool   `json:"persistence_enabled"`
	Backend               string `json:"backend"`
	Shared                bool   `json:"shared"`
	SingleWriter          bool   `json:"single_writer"`
	LocalFirst            bool   `json:"local_first"`
	FallbackActive        bool   `json:"fallback_active"`
	Path                  string `json:"path,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

type controllerDeploymentPosture struct {
	Mode             string                             `json:"mode"`
	ProductionLike   bool                               `json:"production_like"`
	InsecureOverride bool                               `json:"insecure_override"`
	Degraded         bool                               `json:"degraded"`
	Reasons          []string                           `json:"reasons,omitempty"`
	Workflow         agentcore.WorkflowDurabilityStatus `json:"workflow"`
	Artifacts        agentcore.ArtifactDurabilityStatus `json:"artifacts"`
	Ingest           controllerIngestDurabilityStatus   `json:"ingest"`
	IngestTransport  controllerIngestTransportStatus    `json:"ingest_transport"`
}

func controllerInsecureOverrideEnabled(cfg Config, auth ResolvedAuthConfig) bool {
	if cfg.Deployment.InsecureOverride || auth.InsecureOverride {
		return true
	}
	return !auth.Enabled && cfg.Auth.AllowInsecureDisable
}

func (c *Controller) workflowDurabilityStatus() agentcore.WorkflowDurabilityStatus {
	if c == nil || c.agentWorkflow == nil {
		return agentcore.WorkflowDurabilityStatus{
			Enabled:    false,
			Backend:    "disabled",
			LocalFirst: true,
		}
	}
	return c.agentWorkflow.DurabilityStatus()
}

func (c *Controller) ingestDurabilityStatus() controllerIngestDurabilityStatus {
	status := controllerIngestDurabilityStatus{
		PersistenceConfigured: c != nil && strings.TrimSpace(c.config.Ingest.Inbox.Backend) != ingest.InboxBackendMemory,
		PersistenceEnabled:    false,
		Backend:               "unavailable",
		SingleWriter:          c != nil && c.config.Ingest.Inbox.SingleWriter,
		LocalFirst:            true,
	}
	if c == nil {
		return status
	}
	if c.ingestInbox == nil {
		return status
	}
	stats := c.ingestInbox.Stats(context.Background())
	status.PersistenceEnabled = stats.Enabled && stats.Backend != ingest.InboxBackendMemory
	status.Path = nonEmptyString(stats.Path, c.config.Ingest.Inbox.Path)
	status.LastError = strings.TrimSpace(stats.LastError)
	status.Backend = stats.Backend
	status.Shared = stats.Shared
	status.LocalFirst = !stats.Shared
	status.FallbackActive = status.PersistenceConfigured && !status.PersistenceEnabled
	return status
}

func (c *Controller) artifactDurabilityStatus() agentcore.ArtifactDurabilityStatus {
	if c == nil || c.agentWorkflow == nil {
		return agentcore.ArtifactDurabilityStatus{
			Enabled:         false,
			MetadataBackend: "disabled",
			PayloadBackend:  "disabled",
		}
	}
	return c.agentWorkflow.ArtifactStatus()
}

func (c *Controller) deploymentPostureStatus() controllerDeploymentPosture {
	mode := defaultDeploymentMode
	var auth ResolvedAuthConfig
	var workflow agentcore.WorkflowDurabilityStatus
	var artifacts agentcore.ArtifactDurabilityStatus
	var ingestStatus controllerIngestDurabilityStatus
	var transport controllerIngestTransportStatus
	corsMode := "same_origin_only"
	if c != nil {
		if normalized := normalizeControllerDeploymentMode(c.config.Deployment.Mode); normalized != "" {
			mode = normalized
		}
		auth = c.auth
		workflow = c.workflowDurabilityStatus()
		artifacts = c.artifactDurabilityStatus()
		ingestStatus = c.ingestDurabilityStatus()
		transport = c.ingestTransportStatus()
		corsMode = c.corsMode()
	}
	return buildControllerDeploymentPosture(c.config, auth, mode, corsMode, workflow, artifacts, ingestStatus, transport)
}

func buildControllerDeploymentPosture(cfg Config, auth ResolvedAuthConfig, deploymentMode, corsMode string, workflow agentcore.WorkflowDurabilityStatus, artifacts agentcore.ArtifactDurabilityStatus, ingestStatus controllerIngestDurabilityStatus, transport controllerIngestTransportStatus) controllerDeploymentPosture {
	mode := defaultDeploymentMode
	corsMode = strings.TrimSpace(corsMode)
	if normalized := normalizeControllerDeploymentMode(deploymentMode); normalized != "" {
		mode = normalized
	} else if normalized := normalizeControllerDeploymentMode(cfg.Deployment.Mode); normalized != "" {
		mode = normalized
	}
	posture := controllerDeploymentPosture{
		Mode:             mode,
		ProductionLike:   mode != defaultDeploymentMode,
		InsecureOverride: controllerInsecureOverrideEnabled(cfg, auth),
		Workflow:         workflow,
		Artifacts:        artifacts,
		Ingest:           ingestStatus,
		IngestTransport:  transport,
	}
	if !posture.ProductionLike {
		return posture
	}
	if !auth.Enabled {
		posture.Reasons = append(posture.Reasons, "controller API auth is disabled")
	}
	if auth.Enabled && !auth.IngestAuthEnabled {
		posture.Reasons = append(posture.Reasons, "gRPC ingest authentication is disabled")
	}
	if corsMode == "local_dev_defaults" {
		posture.Reasons = append(posture.Reasons, "controller API is using local-dev CORS defaults")
	}
	if !posture.Ingest.PersistenceConfigured {
		posture.Reasons = append(posture.Reasons, "durable ingest inbox is disabled; the controller cannot make crash-safe ACKs")
	}
	if posture.Mode == "distributed" && !posture.Ingest.Shared && !posture.Ingest.SingleWriter {
		posture.Reasons = append(posture.Reasons, "distributed ingest requires a shared PostgreSQL inbox or an explicit single-writer topology")
	}
	if posture.IngestTransport.PlaintextActive {
		switch posture.Mode {
		case "cluster-lite", "distributed":
			posture.Reasons = append(posture.Reasons, "gRPC ingest transport is plaintext; cluster modes should enable TLS or mTLS")
		}
	}
	if posture.Ingest.FallbackActive {
		posture.Reasons = append(posture.Reasons,
			fmt.Sprintf("ingest persistence is configured at %q but the controller is running memory-only", posture.Ingest.Path))
	}
	if posture.Workflow.Enabled && !posture.Workflow.Persistent {
		reason := "workflow durable store is running in-memory"
		if posture.Workflow.FallbackActive && posture.Workflow.StorePath != "" {
			reason = fmt.Sprintf("workflow durable store at %q fell back to in-memory", posture.Workflow.StorePath)
		}
		posture.Reasons = append(posture.Reasons, reason)
	}
	if posture.Workflow.Enabled && posture.Workflow.Persistent && !posture.Workflow.Shared {
		switch posture.Mode {
		case "cluster-lite", "distributed":
			posture.Reasons = append(posture.Reasons, "workflow durable state is controller-local; approval state, idempotency, and run history do not fail over across controller replacement")
		}
	}
	if posture.Workflow.Enabled && posture.Workflow.Shared && posture.Artifacts.Enabled && !posture.Artifacts.MetadataShared {
		posture.Reasons = append(posture.Reasons, "artifact metadata is controller-local; evidence, message history, and incident memory references do not fail over across controller replacement")
	}
	if posture.Workflow.Enabled && posture.Workflow.Shared && posture.Artifacts.Enabled && posture.Artifacts.MetadataShared && !posture.Artifacts.PayloadSharedSurvivable {
		posture.Reasons = append(posture.Reasons, "artifact payload backend is not shared-survivable; a replacement controller can resolve artifact metadata but cannot rely on payload availability after failover")
	}
	if posture.Artifacts.Enabled && posture.Artifacts.LastError != "" {
		posture.Reasons = append(posture.Reasons, fmt.Sprintf("artifact manager is degraded: %s", posture.Artifacts.LastError))
	}
	posture.Degraded = len(posture.Reasons) > 0
	return posture
}

func validateControllerDeploymentPosture(c *Controller) error {
	if c == nil {
		return nil
	}
	posture := c.deploymentPostureStatus()
	if !posture.ProductionLike || !posture.Degraded {
		return nil
	}
	if posture.InsecureOverride {
		c.logger.Warn("controller started in degraded production posture via explicit insecure override",
			zap.String("deployment_mode", posture.Mode),
			zap.Strings("reasons", posture.Reasons))
		return nil
	}
	return fmt.Errorf(
		"deployment mode %q has degraded production posture: %s; fix the configuration or set deployment.insecure_override=true (auth.allow_insecure_disable=true also works for backward compatibility)",
		posture.Mode,
		strings.Join(posture.Reasons, "; "),
	)
}
