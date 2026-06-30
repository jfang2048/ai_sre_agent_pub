package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
	"go.uber.org/zap"
)

type workflowEvidencePackage struct {
	Run                    *DurableRun               `json:"run,omitempty"`
	Trace                  *AgentTrace               `json:"trace,omitempty"`
	Audit                  []WorkflowAuditRecord     `json:"audit,omitempty"`
	Report                 any                       `json:"report,omitempty"`
	MessageManifestPath    string                    `json:"message_manifest_path,omitempty"`
	MessageHistoryArtifact *DurableArtifactRef       `json:"message_history_artifact,omitempty"`
	MessageHistory         *AgentMessageHistory      `json:"message_history,omitempty"`
	ArtifactChain          *WorkflowArtifactChain    `json:"artifact_chain,omitempty"`
	ArtifactManifest       *WorkflowArtifactManifest `json:"artifact_manifest,omitempty"`
	ArtifactManifestRef    *DurableArtifactRef       `json:"artifact_manifest_ref,omitempty"`
	GeneratedAt            time.Time                 `json:"generated_at"`
}

type workflowEvidenceWriteResult struct {
	PackageRef       DurableEvidencePackageRef
	ArtifactManifest *DurableArtifactRef
}

func decodeWorkflowEvidencePackage(raw []byte) (workflowEvidencePackage, error) {
	var pkg workflowEvidencePackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return workflowEvidencePackage{}, err
	}
	return pkg, nil
}

type workflowEvidenceBuilder struct {
	rootPath  string
	artifacts *artifactstore.Manager
	logger    *zap.Logger
}

func newWorkflowEvidenceBuilder(rootPath string, manager *artifactstore.Manager, logger *zap.Logger) *workflowEvidenceBuilder {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &workflowEvidenceBuilder{
		rootPath:  filepath.Join(strings.TrimSpace(rootPath), "evidence"),
		artifacts: manager,
		logger:    logger.With(zap.String("component", "workflow_evidence_builder")),
	}
}

func asRCAWorkflowReport(report any) (RCAWorkflowReport, bool) {
	switch typed := report.(type) {
	case RCAWorkflowReport:
		return typed, true
	case *RCAWorkflowReport:
		if typed == nil {
			return RCAWorkflowReport{}, false
		}
		return *typed, true
	default:
		return RCAWorkflowReport{}, false
	}
}

func (b *workflowEvidenceBuilder) Write(run *DurableRun, trace *AgentTrace, audit []WorkflowAuditRecord, report any) (workflowEvidenceWriteResult, error) {
	if b == nil {
		return workflowEvidenceWriteResult{}, fmt.Errorf("workflow evidence builder is nil")
	}
	if run == nil || strings.TrimSpace(run.RunID) == "" {
		return workflowEvidenceWriteResult{}, fmt.Errorf("durable run is required")
	}
	path := filepath.Join(b.rootPath, sanitizeID(run.RunID)+".json")
	pkg := workflowEvidencePackage{
		Run:                 run,
		Trace:               trace,
		Audit:               append([]WorkflowAuditRecord(nil), audit...),
		Report:              report,
		MessageManifestPath: strings.TrimSpace(run.MessageManifestPath),
		GeneratedAt:         time.Now().UTC(),
	}
	if run.MessageHistoryArtifact != nil {
		copy := *run.MessageHistoryArtifact
		pkg.MessageHistoryArtifact = &copy
	}
	if b.artifacts != nil && run.MessageHistoryArtifact != nil && strings.TrimSpace(run.MessageHistoryArtifact.ArtifactID) != "" {
		if payload, _, err := b.artifacts.Read(context.Background(), run.MessageHistoryArtifact.ArtifactID); err == nil {
			var history AgentMessageHistory
			if unmarshalErr := json.Unmarshal(payload, &history); unmarshalErr == nil {
				pkg.MessageHistory = &history
			}
		}
	}
	if pkg.MessageHistory == nil && strings.TrimSpace(run.MessageManifestPath) != "" {
		if history, err := loadAgentMessageHistoryFromPath(run.MessageManifestPath); err == nil {
			pkg.MessageHistory = &history
		}
	}
	if reportData, ok := asRCAWorkflowReport(report); ok {
		chain := buildWorkflowArtifactChain(nil, reportData, firstNonEmpty(reportData.Status, string(run.Status)))
		pkg.ArtifactChain = &chain
		manifest := buildWorkflowArtifactManifest(chain)
		pkg.ArtifactManifest = &manifest
		manifestRef, manifestErr := b.writeArtifactManifest(run, manifest)
		if manifestErr != nil {
			b.logger.Warn("failed to write workflow artifact manifest", zap.String("run_id", run.RunID), zap.Error(manifestErr))
		} else {
			pkg.ArtifactManifestRef = manifestRef
		}
	}
	raw, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return workflowEvidenceWriteResult{}, err
	}
	if b.artifacts != nil {
		record, err := b.artifacts.Write(context.Background(), artifactstore.WriteRequest{
			ArtifactID:    fmt.Sprintf("evidence-%s", sanitizeID(run.RunID)),
			ArtifactType:  artifactstore.ArtifactTypeEvidencePackage,
			OwnerType:     artifactstore.OwnerTypeWorkflowRun,
			OwnerID:       run.RunID,
			RunID:         run.RunID,
			CollectorID:   run.CollectorID,
			FileExtension: ".json",
			ContentType:   "application/json",
			StorageKey:    filepath.ToSlash(filepath.Join("evidence", run.RunID, "package.json")),
			Metadata: map[string]string{
				"workflow_type": run.WorkflowType,
			},
			Payload: raw,
		})
		if err != nil {
			return workflowEvidenceWriteResult{}, err
		}
		ref := durableArtifactRefFromRecord(record)
		return workflowEvidenceWriteResult{PackageRef: DurableEvidencePackageRef(ref), ArtifactManifest: cloneDurableArtifactRef(pkg.ArtifactManifestRef)}, nil
	}
	if err := os.MkdirAll(b.rootPath, 0o755); err != nil {
		return workflowEvidenceWriteResult{}, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return workflowEvidenceWriteResult{}, err
	}
	return workflowEvidenceWriteResult{PackageRef: DurableEvidencePackageRef{
		ArtifactID:     fmt.Sprintf("evidence-%s", sanitizeID(run.RunID)),
		ArtifactType:   string(artifactstore.ArtifactTypeEvidencePackage),
		OwnerType:      string(artifactstore.OwnerTypeWorkflowRun),
		OwnerID:        run.RunID,
		RunID:          run.RunID,
		CollectorID:    run.CollectorID,
		StorageBackend: "filesystem",
		StorageKey:     filepath.ToSlash(filepath.Join("evidence", run.RunID, "package.json")),
		LocalCachePath: path,
		Path:           path,
		ContentType:    "application/json",
		CreatedAt:      pkg.GeneratedAt,
		UpdatedAt:      pkg.GeneratedAt,
	}, ArtifactManifest: cloneDurableArtifactRef(pkg.ArtifactManifestRef)}, nil
}

func (b *workflowEvidenceBuilder) writeArtifactManifest(run *DurableRun, manifest WorkflowArtifactManifest) (*DurableArtifactRef, error) {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	storageKey := filepath.ToSlash(filepath.Join("evidence", run.RunID, "artifacts", "manifest.json"))
	if b.artifacts != nil {
		record, err := b.artifacts.Write(context.Background(), artifactstore.WriteRequest{
			ArtifactID:     fmt.Sprintf("artifact-manifest-%s", sanitizeID(run.RunID)),
			ArtifactType:   artifactstore.ArtifactTypeIncidentChain,
			OwnerType:      artifactstore.OwnerTypeWorkflowRun,
			OwnerID:        run.RunID,
			RunID:          run.RunID,
			CollectorID:    run.CollectorID,
			FileExtension:  ".json",
			ContentType:    "application/json",
			StorageKey:     storageKey,
			RetentionClass: artifactstore.RetentionClassWorkflowAux,
			Metadata: map[string]string{
				"workflow_type": run.WorkflowType,
				"artifact_kind": "workflow_artifact_manifest",
			},
			Payload: raw,
		})
		if err != nil {
			return nil, err
		}
		ref := durableArtifactRefFromRecord(record)
		return &ref, nil
	}
	path := filepath.Join(b.rootPath, sanitizeID(run.RunID), "artifacts", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, err
	}
	ref := DurableArtifactRef{
		ArtifactID:     fmt.Sprintf("artifact-manifest-%s", sanitizeID(run.RunID)),
		ArtifactType:   string(artifactstore.ArtifactTypeIncidentChain),
		OwnerType:      string(artifactstore.OwnerTypeWorkflowRun),
		OwnerID:        run.RunID,
		RunID:          run.RunID,
		CollectorID:    run.CollectorID,
		StorageBackend: "filesystem",
		StorageKey:     storageKey,
		LocalCachePath: path,
		Path:           path,
		ContentType:    "application/json",
		CreatedAt:      manifest.UpdatedAt,
		UpdatedAt:      manifest.UpdatedAt,
	}
	return &ref, nil
}
