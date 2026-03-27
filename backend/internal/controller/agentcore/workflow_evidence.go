package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

type workflowEvidencePackage struct {
	Run         *DurableRun           `json:"run,omitempty"`
	Trace       *AgentTrace           `json:"trace,omitempty"`
	Audit       []WorkflowAuditRecord `json:"audit,omitempty"`
	Report      any                   `json:"report,omitempty"`
	GeneratedAt time.Time             `json:"generated_at"`
}

type workflowEvidenceBuilder struct {
	rootPath string
	logger   *zap.Logger
}

func newWorkflowEvidenceBuilder(rootPath string, logger *zap.Logger) *workflowEvidenceBuilder {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &workflowEvidenceBuilder{
		rootPath: filepath.Join(strings.TrimSpace(rootPath), "evidence"),
		logger:   logger.With(zap.String("component", "workflow_evidence_builder")),
	}
}

func (b *workflowEvidenceBuilder) Write(run *DurableRun, trace *AgentTrace, audit []WorkflowAuditRecord, report any) (DurableEvidencePackageRef, error) {
	if b == nil {
		return DurableEvidencePackageRef{}, fmt.Errorf("workflow evidence builder is nil")
	}
	if run == nil || strings.TrimSpace(run.RunID) == "" {
		return DurableEvidencePackageRef{}, fmt.Errorf("durable run is required")
	}
	if err := os.MkdirAll(b.rootPath, 0o755); err != nil {
		return DurableEvidencePackageRef{}, err
	}
	path := filepath.Join(b.rootPath, sanitizeID(run.RunID)+".json")
	pkg := workflowEvidencePackage{
		Run:         run,
		Trace:       trace,
		Audit:       append([]WorkflowAuditRecord(nil), audit...),
		Report:      report,
		GeneratedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return DurableEvidencePackageRef{}, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return DurableEvidencePackageRef{}, err
	}
	return DurableEvidencePackageRef{
		Path:        path,
		GeneratedAt: pkg.GeneratedAt,
	}, nil
}
