package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/causalgraph"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/changeintel"
	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"
)

// WorkflowEngine runs deterministic, tool-driven joint-risk and RCA workflows.
type WorkflowEngine struct {
	cfg        WorkflowConfig
	logger     *zap.Logger
	store      *ingest.MemoryStore
	history    ingest.MetricHistoryProvider
	logIndex   *logindex.Index
	topology   TopologyProvider
	tools      *workflowToolManager
	metrics    *metricsQueryTool
	changes    *changeQueryTool
	knowledge  *knowledgeRetrievalTool
	ragQuery   *knowledgeRetrievalTool
	historical *knowledgeRetrievalTool
	runbooks   *knowledgeRetrievalTool
	similar    *knowledgeRetrievalTool
	llm        llmClient
	llmLimiter *rate.Limiter
	metaLogger *zap.Logger
	telemetry  *workflowTelemetry

	processTree             *ProcessTree
	baseline                *BaselineEngine
	behaviorMemory          *BehavioralMemoryStore
	traceStore              *TraceStore
	proposedActions         *ProposedActionStore
	orchestrator            *DurableOrchestrator
	playbookRunner          *PlaybookRunner
	memoryStore             *WorkflowMemoryStore
	toolExperience          *ToolExperienceMemoryStore
	changeStore             *changeintel.Store
	evidenceBuilder         *workflowEvidenceBuilder
	messageStore            *AgentMessageStore
	artifactManager         *artifactstore.Manager
	rag                     rag.KnowledgeBase
	collectorProfileApplier CollectorProfileApplier
	durability              WorkflowDurabilityStatus
	artifactStatus          ArtifactDurabilityStatus
	cacheMu                 sync.Mutex
	recentRuns              map[string]workflowCacheEntry
	inFlightRuns            map[string]*workflowInFlight

	mu          sync.RWMutex
	riskReports []JointRiskAssessment
	rcaReports  []RCAWorkflowReport
	incidents   []AgentIncidentReport
	audits      []WorkflowAuditRecord
}

// NewWorkflowEngine creates a deterministic workflow engine with explicit tools.
func NewWorkflowEngine(cfg WorkflowConfig, store *ingest.MemoryStore, idx *logindex.Index, topology TopologyProvider, logger *zap.Logger) *WorkflowEngine {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = WorkflowConfigFromEnv(cfg)
	cfg = normalizeWorkflowConfig(cfg)
	cfg = applyWorkflowRuntimePaths(cfg)

	engine := &WorkflowEngine{
		cfg:             cfg,
		logger:          logger.With(zap.String("component", "agent_workflow_engine")),
		store:           store,
		history:         store,
		logIndex:        idx,
		topology:        topology,
		processTree:     NewProcessTree(65536),
		baseline:        NewBaselineEngine(DefaultBaselineConfig()),
		behaviorMemory:  NewBehavioralMemoryStore(cfg.BehaviorMemory, logger),
		traceStore:      NewTraceStore(500),
		proposedActions: NewProposedActionStore(200),
		orchestrator:    nil, // will be initialized below
		recentRuns:      make(map[string]workflowCacheEntry, cfg.RequestDedupeEntries),
		inFlightRuns:    make(map[string]*workflowInFlight),
		riskReports:     make([]JointRiskAssessment, 0, 64),
		rcaReports:      make([]RCAWorkflowReport, 0, 64),
		incidents:       make([]AgentIncidentReport, 0, 64),
		audits:          make([]WorkflowAuditRecord, 0, cfg.AuditRetention),
		llmLimiter:      rate.NewLimiter(rate.Limit(cfg.LLMRateLimitRPS), cfg.LLMRateBurst),
		metaLogger:      newWorkflowMetaLogger(),
		telemetry:       newWorkflowTelemetry(),
		durability: WorkflowDurabilityStatus{
			Enabled:              true,
			Backend:              "in_memory",
			ConfiguredBackend:    cfg.WorkflowStoreBackend,
			Persistent:           false,
			LocalFirst:           true,
			Shared:               false,
			Mode:                 "memory",
			ApprovalStateBackend: "memory",
			IdempotencyBackend:   "memory",
			StorePath:            cfg.WorkflowStorePath,
			DataPath:             cfg.WorkflowDataPath,
			MessageDir:           cfg.AgentMessageDir,
		},
		artifactStatus: ArtifactDurabilityStatus{
			Enabled:                       true,
			MetadataBackend:               cfg.ArtifactMetadataBackend,
			MetadataPersistent:            false,
			MetadataShared:                false,
			PayloadBackend:                cfg.ArtifactPayloadBackend,
			PayloadRootPath:               cfg.ArtifactPayloadRootPath,
			PayloadShared:                 cfg.ArtifactPayloadShared,
			PayloadSharedSurvivable:       artifactstore.SharedPayloadBackend(cfg.ArtifactPayloadBackend),
			PayloadContainer:              cfg.ArtifactPayloadS3Bucket,
			PayloadPrefix:                 cfg.ArtifactPayloadS3Prefix,
			AddressingMode:                "stable_keys",
			LocalCacheActive:              cfg.ArtifactPayloadBackend == "filesystem",
			GCEnabled:                     cfg.ArtifactGCEnabled,
			GCInterval:                    cfg.ArtifactGCInterval.String(),
			GCBatchSize:                   cfg.ArtifactGCBatchSize,
			MessageHistoryMetadataBackend: cfg.ArtifactMetadataBackend,
			EvidenceMetadataBackend:       cfg.ArtifactMetadataBackend,
			IncidentMemoryMetadataBackend: cfg.ArtifactMetadataBackend,
			RAGMetadataBackend:            "local_runtime_only",
			RAGIndexBackend:               "local_file",
		},
	}

	var wfStore DurableStore
	wfStore, engine.durability = initializeWorkflowDurableStore(cfg, engine.durability, engine.logger)
	if wfStore == nil {
		wfStore = NewInMemoryDurableStore()
	}
	engine.artifactManager, engine.artifactStatus = initializeArtifactManager(cfg, engine.artifactStatus, engine.logger)
	engine.orchestrator = NewDurableOrchestrator(wfStore, logger)
	engine.memoryStore = NewWorkflowMemoryStore(cfg.WorkflowDataPath, engine.artifactManager, logger)
	if runtimeModeEnablesExperienceMemory(cfg) {
		engine.toolExperience = NewToolExperienceMemoryStore(cfg.WorkflowDataPath, logger)
	}
	engine.changeStore = changeintel.NewStore(cfg.WorkflowDataPath, logger)
	engine.evidenceBuilder = newWorkflowEvidenceBuilder(cfg.WorkflowDataPath, engine.artifactManager, logger)
	engine.messageStore = NewAgentMessageStore(cfg, engine.artifactManager, logger)

	engine.knowledge = &knowledgeRetrievalTool{
		name:        ToolKnowledge,
		description: "Hybrid lexical plus embedding retrieval over the local dataset-backed knowledge base.",
		intent:      "general",
		memory:      engine.memoryStore,
	}
	engine.ragQuery = &knowledgeRetrievalTool{
		name:        ToolRAGQuery,
		description: "General-purpose RAG query against normalized local SRE knowledge.",
		intent:      "general",
		memory:      engine.memoryStore,
	}
	engine.historical = &knowledgeRetrievalTool{
		name:           ToolHistoricalIncident,
		description:    "Retrieve prior incidents and RCA analogies from the local knowledge base.",
		intent:         "historical_incident",
		knowledgeTypes: []string{"historical_incident", "question_pattern"},
		caseTypes:      []string{"historical_incident", "operational_qa"},
		memory:         engine.memoryStore,
	}
	engine.runbooks = &knowledgeRetrievalTool{
		name:           ToolRunbookRetrieval,
		description:    "Retrieve runbook fragments, troubleshooting guides, and remediation procedures.",
		intent:         "runbook",
		knowledgeTypes: []string{"runbook"},
		caseTypes:      []string{"runbook"},
		memory:         engine.memoryStore,
	}
	engine.similar = &knowledgeRetrievalTool{
		name:           ToolSimilarCase,
		description:    "Retrieve similar weak-signal combinations and latent-risk patterns.",
		intent:         "joint_risk",
		knowledgeTypes: []string{"historical_incident", "question_pattern", "runbook"},
		caseTypes:      []string{"historical_incident", "operational_qa", "runbook"},
		memory:         engine.memoryStore,
	}
	engine.metrics = &metricsQueryTool{store: store, history: store}
	engine.changes = &changeQueryTool{store: engine.changeStore, index: idx, nodes: store}
	engine.tools = newWorkflowToolManager(engine.logger,
		engine.metrics,
		&logsQueryTool{index: idx, store: store},
		engine.changes,
		&deploymentHistoryTool{store: engine.changeStore, index: idx, nodes: store},
		&configStateTool{store: store, changes: engine.changes},
		engine.knowledge,
		engine.ragQuery,
		engine.historical,
		engine.runbooks,
		engine.similar,
		&topologyQueryTool{provider: topology, store: store},
		&memoryPressureTool{store: store},
		&connectivityCheckTool{store: store},
		&dnsCheckTool{index: idx, store: store},
		&serviceHealthTool{store: store},
		&securityCheckTool{store: store, index: idx},
		&ebpfQueryTool{store: store},
		&gpuQueryTool{store: store},
		&securityGraphTool{store: store},
		&processLineageTool{store: store},
		&kubernetesResourceTool{store: store},
		&containerRevisionTool{store: store},
		&storageHealthTool{store: store},
		&networkBlastRadiusTool{provider: topology, store: store},
		&actionOutcomeTool{memory: engine.memoryStore},
		&profilingTriggerTool{cfg: cfg},
		&remediationActionTool{cfg: cfg},
	)
	engine.tools.orchestrator = engine.orchestrator
	engine.tools.cfg = engine.cfg
	engine.llm = newWorkflowLLMClient(cfg, logger)
	engine.behaviorMemory.SetHistoryProvider(engine.history)

	runner, err := NewPlaybookRunner(cfg.ActionRunner, logger)
	if err != nil {
		engine.logger.Error("failed to create playbook runner", zap.Error(err))
	} else {
		engine.playbookRunner = runner
		if t, ok := engine.tools.tools[ToolRemediation].(*remediationActionTool); ok {
			t.runner = runner
		}
	}

	return engine
}

func initializeWorkflowDurableStore(cfg WorkflowConfig, status WorkflowDurabilityStatus, logger *zap.Logger) (DurableStore, WorkflowDurabilityStatus) {
	backend := strings.ToLower(strings.TrimSpace(cfg.WorkflowStoreBackend))
	if backend == "" {
		backend = "bbolt"
	}
	status.ConfiguredBackend = backend
	switch backend {
	case "postgres":
		store, err := NewPostgresDurableStore(context.Background(), cfg.WorkflowStorePostgresDSN)
		if err == nil {
			status.Backend = workflowRunStoreBackendPostgres
			status.Persistent = true
			status.LocalFirst = false
			status.Shared = true
			status.Mode = "shared"
			status.ApprovalStateBackend = workflowRunStoreBackendPostgres
			status.IdempotencyBackend = workflowRunStoreBackendPostgres
			if logger != nil {
				logger.Info("using shared workflow durable store", zap.String("backend", workflowRunStoreBackendPostgres))
			}
			return store, status
		}
		if logger != nil {
			logger.Error("failed to open shared workflow durable store, falling back to in-memory", zap.Error(err))
		}
		status.Backend = "in_memory"
		status.Persistent = false
		status.LocalFirst = false
		status.Shared = false
		status.Mode = "memory"
		status.FallbackActive = true
		status.ApprovalStateBackend = "memory"
		status.IdempotencyBackend = "memory"
		status.LastError = err.Error()
		return NewInMemoryDurableStore(), status
	case "memory":
		status.Backend = "in_memory"
		status.Persistent = false
		status.LocalFirst = true
		status.Shared = false
		status.Mode = "memory"
		status.ApprovalStateBackend = "memory"
		status.IdempotencyBackend = "memory"
		return NewInMemoryDurableStore(), status
	default:
		if cfg.WorkflowStorePath != "" {
			store, err := NewBoltDurableStore(cfg.WorkflowStorePath)
			if err == nil {
				status.Backend = "embedded_bbolt"
				status.Persistent = true
				status.LocalFirst = true
				status.Shared = false
				status.Mode = "local"
				status.ApprovalStateBackend = "embedded_bbolt"
				status.IdempotencyBackend = "embedded_bbolt"
				if logger != nil {
					logger.Info("using controller-local workflow durable store", zap.String("path", cfg.WorkflowStorePath))
				}
				return store, status
			}
			if logger != nil {
				logger.Error("failed to open persistent workflow store, falling back to in-memory", zap.Error(err))
			}
			status.Backend = "in_memory"
			status.Persistent = false
			status.LocalFirst = true
			status.Shared = false
			status.Mode = "memory"
			status.FallbackActive = true
			status.ApprovalStateBackend = "memory"
			status.IdempotencyBackend = "memory"
			status.LastError = err.Error()
			return NewInMemoryDurableStore(), status
		}
		status.Backend = "in_memory"
		status.Persistent = false
		status.LocalFirst = true
		status.Shared = false
		status.Mode = "memory"
		status.ApprovalStateBackend = "memory"
		status.IdempotencyBackend = "memory"
		return NewInMemoryDurableStore(), status
	}
}

func initializeArtifactManager(cfg WorkflowConfig, status ArtifactDurabilityStatus, logger *zap.Logger) (*artifactstore.Manager, ArtifactDurabilityStatus) {
	artifactCfg := artifactstore.Config{
		MetadataBackend:       cfg.ArtifactMetadataBackend,
		MetadataPath:          cfg.ArtifactMetadataPath,
		MetadataPostgresDSN:   cfg.ArtifactMetadataPostgresDSN,
		PayloadBackend:        cfg.ArtifactPayloadBackend,
		PayloadRootPath:       cfg.ArtifactPayloadRootPath,
		PayloadShared:         cfg.ArtifactPayloadShared,
		PayloadS3Endpoint:     cfg.ArtifactPayloadS3Endpoint,
		PayloadS3Region:       cfg.ArtifactPayloadS3Region,
		PayloadS3Bucket:       cfg.ArtifactPayloadS3Bucket,
		PayloadS3Prefix:       cfg.ArtifactPayloadS3Prefix,
		PayloadS3AccessKey:    cfg.ArtifactPayloadS3AccessKey,
		PayloadS3SecretKey:    cfg.ArtifactPayloadS3SecretKey,
		PayloadS3SessionToken: cfg.ArtifactPayloadS3SessionToken,
		PayloadS3PathStyle:    cfg.ArtifactPayloadS3PathStyle,
		PayloadS3Insecure:     cfg.ArtifactPayloadS3Insecure,
		GCEnabled:             cfg.ArtifactGCEnabled,
		GCInterval:            cfg.ArtifactGCInterval,
		GCBatchSize:           cfg.ArtifactGCBatchSize,
	}
	manager, runtimeStatus, err := artifactstore.NewManager(context.Background(), artifactCfg, logger)
	if err != nil {
		status.MetadataBackend = artifactCfg.MetadataBackend
		status.MetadataPersistent = false
		status.MetadataShared = false
		status.PayloadBackend = artifactCfg.PayloadBackend
		status.PayloadRootPath = artifactCfg.PayloadRootPath
		status.PayloadShared = false
		status.PayloadSharedSurvivable = false
		status.PayloadContainer = artifactCfg.PayloadS3Bucket
		status.PayloadPrefix = artifactCfg.PayloadS3Prefix
		status.AddressingMode = "stable_keys"
		status.LocalCacheActive = artifactCfg.PayloadBackend == "filesystem"
		status.GCEnabled = artifactCfg.GCEnabled
		status.GCInterval = artifactCfg.GCInterval.String()
		status.GCBatchSize = artifactCfg.GCBatchSize
		status.MessageHistoryMetadataBackend = artifactCfg.MetadataBackend
		status.EvidenceMetadataBackend = artifactCfg.MetadataBackend
		status.IncidentMemoryMetadataBackend = artifactCfg.MetadataBackend
		status.LastError = err.Error()
		return nil, status
	}
	status.Enabled = true
	status.MetadataBackend = runtimeStatus.MetadataBackend
	status.MetadataPersistent = runtimeStatus.MetadataPersistent
	status.MetadataShared = runtimeStatus.MetadataShared
	status.PayloadBackend = runtimeStatus.PayloadBackend
	status.PayloadRootPath = runtimeStatus.PayloadRootPath
	status.PayloadShared = runtimeStatus.PayloadShared
	status.PayloadSharedSurvivable = runtimeStatus.PayloadSharedSurvivable
	status.PayloadContainer = runtimeStatus.PayloadContainer
	status.PayloadPrefix = runtimeStatus.PayloadPrefix
	status.AddressingMode = runtimeStatus.AddressingMode
	status.LocalCacheActive = runtimeStatus.LocalCacheActive
	status.GCEnabled = runtimeStatus.GCEnabled
	status.GCInterval = runtimeStatus.GCInterval
	status.GCBatchSize = runtimeStatus.GCBatchSize
	status.GCLastRunAt = runtimeStatus.GCLastRunAt
	status.GCArtifactsScanned = runtimeStatus.GCArtifactsScanned
	status.GCArtifactsDeleted = runtimeStatus.GCArtifactsDeleted
	status.GCDeleteFailures = runtimeStatus.GCDeleteFailures
	status.GCOrphanedMetadata = runtimeStatus.GCOrphanedMetadata
	status.MessageHistoryMetadataBackend = runtimeStatus.MetadataBackend
	status.EvidenceMetadataBackend = runtimeStatus.MetadataBackend
	status.IncidentMemoryMetadataBackend = runtimeStatus.MetadataBackend
	if status.RAGMetadataBackend == "" {
		status.RAGMetadataBackend = "local_runtime_only"
	}
	if status.RAGIndexBackend == "" {
		status.RAGIndexBackend = "local_file"
	}
	status.LastError = runtimeStatus.LastError
	return manager, status
}

// DurabilityStatus returns the workflow runtime durability mode that actually
// came up after local store initialization.
func (e *WorkflowEngine) DurabilityStatus() WorkflowDurabilityStatus {
	if e == nil {
		return WorkflowDurabilityStatus{
			Enabled:    false,
			Backend:    "disabled",
			LocalFirst: true,
		}
	}
	return e.durability
}

// ArtifactStatus reports where workflow-adjacent artifacts are addressed and stored.
func (e *WorkflowEngine) ArtifactStatus() ArtifactDurabilityStatus {
	if e == nil {
		return ArtifactDurabilityStatus{
			Enabled:         false,
			MetadataBackend: "disabled",
			PayloadBackend:  "disabled",
		}
	}
	if e.artifactManager != nil {
		runtimeStatus := e.artifactManager.Status()
		status := e.artifactStatus
		status.Enabled = runtimeStatus.Enabled
		status.MetadataBackend = runtimeStatus.MetadataBackend
		status.MetadataPersistent = runtimeStatus.MetadataPersistent
		status.MetadataShared = runtimeStatus.MetadataShared
		status.PayloadBackend = runtimeStatus.PayloadBackend
		status.PayloadRootPath = runtimeStatus.PayloadRootPath
		status.PayloadShared = runtimeStatus.PayloadShared
		status.PayloadSharedSurvivable = runtimeStatus.PayloadSharedSurvivable
		status.PayloadContainer = runtimeStatus.PayloadContainer
		status.PayloadPrefix = runtimeStatus.PayloadPrefix
		status.AddressingMode = runtimeStatus.AddressingMode
		status.LocalCacheActive = runtimeStatus.LocalCacheActive
		status.GCEnabled = runtimeStatus.GCEnabled
		status.GCInterval = runtimeStatus.GCInterval
		status.GCBatchSize = runtimeStatus.GCBatchSize
		status.GCLastRunAt = runtimeStatus.GCLastRunAt
		status.GCArtifactsScanned = runtimeStatus.GCArtifactsScanned
		status.GCArtifactsDeleted = runtimeStatus.GCArtifactsDeleted
		status.GCDeleteFailures = runtimeStatus.GCDeleteFailures
		status.GCOrphanedMetadata = runtimeStatus.GCOrphanedMetadata
		status.LastError = runtimeStatus.LastError
		return status
	}
	return e.artifactStatus
}

// Close releases workflow-engine resources that outlive individual requests.
func (e *WorkflowEngine) Close() error {
	if e == nil {
		return nil
	}
	var err error
	if e.orchestrator != nil && e.orchestrator.store != nil {
		if closer, ok := e.orchestrator.store.(interface{ Close() error }); ok {
			if closeErr := closer.Close(); closeErr != nil {
				err = closeErr
			}
		}
	}
	if e.artifactManager != nil {
		if closeErr := e.artifactManager.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

// SetKnowledgeBase injects a shared persistent RAG knowledge base into the workflow tool registry.
func (e *WorkflowEngine) SetKnowledgeBase(kb rag.KnowledgeBase) {
	if e == nil {
		return
	}
	e.rag = kb
	for _, tool := range []*knowledgeRetrievalTool{e.knowledge, e.ragQuery, e.historical, e.runbooks, e.similar} {
		if tool != nil {
			tool.kb = kb
		}
	}
	if kb != nil {
		stats := kb.Stats()
		e.artifactStatus.RAGMetadataBackend = "local_runtime_only"
		switch backend := strings.TrimSpace(stats.VectorBackend); backend {
		case "", "local":
			e.artifactStatus.RAGIndexBackend = "local_file"
		default:
			e.artifactStatus.RAGIndexBackend = backend
		}
	}
}

// SetMetricHistoryProvider overrides trend-history reads while leaving node snapshots in memory.
func (e *WorkflowEngine) SetMetricHistoryProvider(provider ingest.MetricHistoryProvider) {
	if e == nil || provider == nil {
		return
	}
	e.history = provider
	if e.metrics != nil {
		e.metrics.history = provider
	}
	if e.behaviorMemory != nil {
		e.behaviorMemory.SetHistoryProvider(provider)
	}
}

// WorkflowConfigFromEnv loads environment overrides for workflow behavior.
func WorkflowConfigFromEnv(base WorkflowConfig) WorkflowConfig {
	cfg := normalizeWorkflowConfig(base)
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ENABLED")); raw != "" {
		cfg.Enabled = parseBool(raw, cfg.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_RUNTIME_MODE")); raw != "" {
		cfg.RuntimeMode = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_RUNTIME_ENABLED")); raw != "" {
		cfg.AdaptiveRuntimeEnabled = parseBool(raw, cfg.AdaptiveRuntimeEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUTONOMOUS_TOOL_SELECTION_ENABLED")); raw != "" {
		cfg.AutonomousToolSelectionEnabled = parseBool(raw, cfg.AutonomousToolSelectionEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_PLANNER_CRITIC_ENABLED")); raw != "" {
		cfg.PlannerCriticEnabled = parseBool(raw, cfg.PlannerCriticEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_TOOL_EXPERIENCE_MEMORY_ENABLED")); raw != "" {
		cfg.ToolExperienceMemoryEnabled = parseBool(raw, cfg.ToolExperienceMemoryEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_CHEAP_FIRST_SELECTION_ENABLED")); raw != "" {
		cfg.CheapFirstSelectionEnabled = parseBool(raw, cfg.CheapFirstSelectionEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_WINDOW")); raw != "" {
		cfg.DefaultWindow = parseDuration(raw, cfg.DefaultWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_WINDOW")); raw != "" {
		cfg.MaxWindow = parseDuration(raw, cfg.MaxWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_SAMPLES")); raw != "" {
		cfg.MaxSamples = parseInt(raw, cfg.MaxSamples)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_SIGNALS")); raw != "" {
		cfg.MaxSignals = parseInt(raw, cfg.MaxSignals)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_HYPOTHESES")); raw != "" {
		cfg.MaxHypotheses = parseInt(raw, cfg.MaxHypotheses)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_PLAN_ITERATIONS")); raw != "" {
		cfg.MaxPlanIterations = parseInt(raw, cfg.MaxPlanIterations)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_PLAN_STEPS")); raw != "" {
		cfg.MaxPlanSteps = parseInt(raw, cfg.MaxPlanSteps)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_ITERATIONS")); raw != "" {
		cfg.AdaptiveMaxIterations = parseInt(raw, cfg.AdaptiveMaxIterations)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_TOOL_CALLS")); raw != "" {
		cfg.AdaptiveMaxToolCalls = parseInt(raw, cfg.AdaptiveMaxToolCalls)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_SAME_TOOL_RETRIES")); raw != "" {
		cfg.AdaptiveMaxSameToolRetries = parseInt(raw, cfg.AdaptiveMaxSameToolRetries)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_HYPOTHESIS_REWRITES")); raw != "" {
		cfg.AdaptiveMaxHypothesisRewrites = parseInt(raw, cfg.AdaptiveMaxHypothesisRewrites)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_NO_PROGRESS_ROUNDS")); raw != "" {
		cfg.AdaptiveMaxNoProgressRounds = parseInt(raw, cfg.AdaptiveMaxNoProgressRounds)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_MAX_PLATEAU_ROUNDS")); raw != "" {
		cfg.AdaptiveMaxPlateauRounds = parseInt(raw, cfg.AdaptiveMaxPlateauRounds)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_NO_PROGRESS_ROUNDS")); raw != "" {
		cfg.MaxNoProgressRounds = parseInt(raw, cfg.MaxNoProgressRounds)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_UNCERTAINTY_PLATEAU_ROUNDS")); raw != "" {
		cfg.MaxUncertaintyPlateauRounds = parseInt(raw, cfg.MaxUncertaintyPlateauRounds)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_PARALLEL_READ_ONLY_LIMIT")); raw != "" {
		cfg.AdaptiveParallelReadOnlyLimit = parseInt(raw, cfg.AdaptiveParallelReadOnlyLimit)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADAPTIVE_TIME_BUDGET")); raw != "" {
		cfg.AdaptiveTimeBudget = parseDuration(raw, cfg.AdaptiveTimeBudget)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUDIT_RETENTION")); raw != "" {
		cfg.AuditRetention = parseInt(raw, cfg.AuditRetention)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INCIDENT_RETENTION")); raw != "" {
		cfg.IncidentRetention = parseInt(raw, cfg.IncidentRetention)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_DRY_RUN")); raw != "" {
		cfg.DryRun = parseBool(raw, cfg.DryRun)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_DATA_PATH")); raw != "" {
		cfg.WorkflowDataPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_STORE_BACKEND")); raw != "" {
		cfg.WorkflowStoreBackend = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_STORE_PATH")); raw != "" {
		cfg.WorkflowStorePath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_POSTGRES_DSN")); raw != "" {
		cfg.WorkflowStorePostgresDSN = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_METADATA_BACKEND")); raw != "" {
		cfg.ArtifactMetadataBackend = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_METADATA_PATH")); raw != "" {
		cfg.ArtifactMetadataPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_POSTGRES_DSN")); raw != "" {
		cfg.ArtifactMetadataPostgresDSN = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_PAYLOAD_BACKEND")); raw != "" {
		cfg.ArtifactPayloadBackend = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_PAYLOAD_ROOT_PATH")); raw != "" {
		cfg.ArtifactPayloadRootPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_PAYLOAD_SHARED")); raw != "" {
		cfg.ArtifactPayloadShared = parseBool(raw, cfg.ArtifactPayloadShared)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_ENDPOINT")); raw != "" {
		cfg.ArtifactPayloadS3Endpoint = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_REGION")); raw != "" {
		cfg.ArtifactPayloadS3Region = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_BUCKET")); raw != "" {
		cfg.ArtifactPayloadS3Bucket = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_PREFIX")); raw != "" {
		cfg.ArtifactPayloadS3Prefix = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_ACCESS_KEY")); raw != "" {
		cfg.ArtifactPayloadS3AccessKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_SECRET_KEY")); raw != "" {
		cfg.ArtifactPayloadS3SecretKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_SESSION_TOKEN")); raw != "" {
		cfg.ArtifactPayloadS3SessionToken = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_PATH_STYLE")); raw != "" {
		cfg.ArtifactPayloadS3PathStyle = parseBool(raw, cfg.ArtifactPayloadS3PathStyle)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_S3_INSECURE")); raw != "" {
		cfg.ArtifactPayloadS3Insecure = parseBool(raw, cfg.ArtifactPayloadS3Insecure)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_GC_ENABLED")); raw != "" {
		cfg.ArtifactGCEnabled = parseBool(raw, cfg.ArtifactGCEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_GC_INTERVAL")); raw != "" {
		cfg.ArtifactGCInterval = parseDuration(raw, cfg.ArtifactGCInterval)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ARTIFACT_GC_BATCH_SIZE")); raw != "" {
		cfg.ArtifactGCBatchSize = parseInt(raw, cfg.ArtifactGCBatchSize)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MESSAGE_PROTOCOL_ENABLED")); raw != "" {
		cfg.AgentMessageProtocolEnabled = parseBool(raw, cfg.AgentMessageProtocolEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MESSAGE_DIR")); raw != "" {
		cfg.AgentMessageDir = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MESSAGE_PRETTY_JSON")); raw != "" {
		cfg.AgentMessagePrettyJSON = parseBool(raw, cfg.AgentMessagePrettyJSON)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MESSAGE_HISTORY_LIMIT")); raw != "" {
		cfg.AgentMessageHistoryLimit = parseInt(raw, cfg.AgentMessageHistoryLimit)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MESSAGE_SCHEMA_VERSION")); raw != "" {
		cfg.AgentMessageSchemaVersion = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_ACTION_RUNNER_DRY_RUN")); raw != "" {
		cfg.ActionRunner.DryRun = parseBool(raw, cfg.ActionRunner.DryRun)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_ACTION_RUNNER_ALLOW_UNSAFE")); raw != "" {
		cfg.ActionRunner.AllowUnsafe = parseBool(raw, cfg.ActionRunner.AllowUnsafe)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REQUIRE_APPROVAL")); raw != "" {
		cfg.RequireApproval = parseBool(raw, cfg.RequireApproval)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ALLOW_PROFILING_EXEC")); raw != "" {
		cfg.AllowProfilingExec = parseBool(raw, cfg.AllowProfilingExec)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ALLOW_REMEDIATION_EXEC")); raw != "" {
		cfg.AllowRemediationExec = parseBool(raw, cfg.AllowRemediationExec)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUTO_ESCALATE_HIGH_RISK")); raw != "" {
		cfg.AutoEscalateOnHighRisk = parseBool(raw, cfg.AutoEscalateOnHighRisk)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_PROFILING_COMMAND")); raw != "" {
		cfg.ProfilingCommand = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED")); raw != "" {
		cfg.InsightsEnabled = parseBool(raw, cfg.InsightsEnabled)
	} else if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_LLM_ENABLED")); raw != "" {
		cfg.InsightsEnabled = parseBool(raw, cfg.InsightsEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_PROVIDER")); raw != "" {
		cfg.InsightsProvider = raw
	} else if raw := strings.TrimSpace(os.Getenv(analysis.EnvLLMProvider)); raw != "" {
		cfg.InsightsProvider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_MODEL")); raw != "" {
		cfg.InsightsModel = raw
	} else if raw := strings.TrimSpace(os.Getenv(analysis.EnvLLMModel)); raw != "" {
		cfg.InsightsModel = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_API_KEY_ENV")); raw != "" {
		cfg.InsightsAPIKeyEnv = raw
	} else if strings.TrimSpace(cfg.InsightsAPIKeyEnv) == "" {
		cfg.InsightsAPIKeyEnv = analysis.EnvLLMAPIKey
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_TIMEOUT")); raw != "" {
		cfg.LLMTimeout = parseDuration(raw, cfg.LLMTimeout)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_RATE_LIMIT_RPS")); raw != "" {
		cfg.LLMRateLimitRPS = parseFloat(raw, cfg.LLMRateLimitRPS)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_RATE_BURST")); raw != "" {
		cfg.LLMRateBurst = parseInt(raw, cfg.LLMRateBurst)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADVANCED_REASONING_ENABLED")); raw != "" {
		cfg.AdvancedReasoningEnabled = parseBool(raw, cfg.AdvancedReasoningEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADVANCED_REASONING_TIMEOUT")); raw != "" {
		cfg.AdvancedReasoningTimeout = parseDuration(raw, cfg.AdvancedReasoningTimeout)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADVANCED_REASONING_MAX_BRANCHES")); raw != "" {
		cfg.AdvancedReasoningMaxBranches = parseInt(raw, cfg.AdvancedReasoningMaxBranches)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ADVANCED_REASONING_AMBIGUITY_THRESHOLD")); raw != "" {
		cfg.AdvancedReasoningAmbiguityThreshold = parseFloat(raw, cfg.AdvancedReasoningAmbiguityThreshold)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REASONING_TOKEN_BUDGET")); raw != "" {
		cfg.ReasoningTokenBudget = parseInt(raw, cfg.ReasoningTokenBudget)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_REFINE_ITERATIONS")); raw != "" {
		cfg.MaxRefineIterations = parseInt(raw, cfg.MaxRefineIterations)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REFINE_CONFIDENCE_THRESHOLD")); raw != "" {
		cfg.RefineConfidenceThreshold = parseFloat(raw, cfg.RefineConfidenceThreshold)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_DEGRADED_MODE_POLICY")); raw != "" {
		cfg.DegradedModePolicy = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_HIGH_RISK_THRESHOLD")); raw != "" {
		cfg.HighRiskScoreThreshold = parseFloat(raw, cfg.HighRiskScoreThreshold)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MEDIUM_RISK_THRESHOLD")); raw != "" {
		cfg.MediumRiskThreshold = parseFloat(raw, cfg.MediumRiskThreshold)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REQUEST_DEDUPE_TTL")); raw != "" {
		cfg.RequestDedupeTTL = parseDuration(raw, cfg.RequestDedupeTTL)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REQUEST_DEDUPE_ENTRIES")); raw != "" {
		cfg.RequestDedupeEntries = parseInt(raw, cfg.RequestDedupeEntries)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_TOOL_RETRY_COUNT")); raw != "" {
		cfg.ToolRetryCount = parseInt(raw, cfg.ToolRetryCount)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_POLICY_VERSION")); raw != "" {
		cfg.PolicyVersion = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_VERIFICATION_WINDOW")); raw != "" {
		cfg.VerificationWindow = parseDuration(raw, cfg.VerificationWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUTO_ROLLBACK_ON_VERIFICATION_FAILURE")); raw != "" {
		cfg.AutoRollbackOnVerificationFailure = parseBool(raw, cfg.AutoRollbackOnVerificationFailure)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_ENABLED")); raw != "" {
		cfg.BehaviorMemory.Enabled = parseBool(raw, cfg.BehaviorMemory.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_LONG_WINDOW")); raw != "" {
		cfg.BehaviorMemory.LongWindow = parseDuration(raw, cfg.BehaviorMemory.LongWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_MIN_SAMPLES")); raw != "" {
		cfg.BehaviorMemory.MinSamples = parseInt(raw, cfg.BehaviorMemory.MinSamples)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_MIN_RECURRING_BURSTS")); raw != "" {
		cfg.BehaviorMemory.MinRecurringBursts = parseInt(raw, cfg.BehaviorMemory.MinRecurringBursts)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_CACHE_ENTRIES")); raw != "" {
		cfg.BehaviorMemory.CacheEntries = parseInt(raw, cfg.BehaviorMemory.CacheEntries)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_CACHE_TTL")); raw != "" {
		cfg.BehaviorMemory.CacheTTL = parseDuration(raw, cfg.BehaviorMemory.CacheTTL)
	}
	return normalizeWorkflowConfig(cfg)
}

func applyWorkflowRuntimePaths(cfg WorkflowConfig) WorkflowConfig {
	if !runningUnderGoTestBinary() {
		return cfg
	}
	def := DefaultWorkflowConfig()
	testRoot := filepath.Join(os.TempDir(), "ai_sre_agent_workflow_tests", sanitizeID(strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")))
	if cfg.WorkflowDataPath == def.WorkflowDataPath {
		cfg.WorkflowDataPath = filepath.Join(testRoot, "workflows")
	}
	if cfg.WorkflowStorePath == def.WorkflowStorePath {
		cfg.WorkflowStorePath = filepath.Join(testRoot, "workflow_runs.db")
	}
	if cfg.ArtifactMetadataPath == def.ArtifactMetadataPath {
		cfg.ArtifactMetadataPath = filepath.Join(testRoot, "workflows", "artifacts.db")
	}
	if cfg.ArtifactPayloadBackend == "filesystem" && cfg.ArtifactPayloadRootPath == def.ArtifactPayloadRootPath {
		cfg.ArtifactPayloadRootPath = filepath.Join(testRoot, "workflows")
	}
	if cfg.AgentMessageDir == def.AgentMessageDir {
		cfg.AgentMessageDir = filepath.Join(testRoot, "messages")
	}
	return cfg
}

func runningUnderGoTestBinary() bool {
	return strings.HasSuffix(filepath.Base(os.Args[0]), ".test")
}

// EvaluateJointRisk executes the deterministic joint-risk workflow.
func (e *WorkflowEngine) EvaluateJointRisk(ctx context.Context, req WorkflowRequest) (JointRiskAssessment, error) {
	if e == nil || !e.cfg.Enabled {
		return JointRiskAssessment{}, fmt.Errorf("workflow engine disabled")
	}
	started := time.Now().UTC()
	cacheKey, cachedRisk, _, hit, err := e.beginWorkflowRun(ctx, "joint_risk", req)
	if err != nil {
		return JointRiskAssessment{}, err
	}
	if hit {
		e.auditCachedWorkflow(cachedRisk.WorkflowID, "joint_risk", cachedRisk.CollectorID, req, "reused recent joint-risk result")
		return cachedRisk, nil
	}
	defer e.finishWorkflowRun(cacheKey)

	state := e.newWorkflowState("joint_risk", req)
	e.logWorkflowEvent(zap.InfoLevel, "workflow.started", map[string]any{
		"trace_id":      state.workflowID,
		"workflow_type": "joint_risk",
		"collector_id":  state.collectorID,
		"window":        state.window.String(),
		"trigger":       state.trigger,
		"dry_run":       state.dryRun,
	})

	durableRun, err := e.orchestrator.StartRunWithRequest(ctx, state.workflowID, "joint_risk", state.collectorID, req)
	if err != nil {
		e.logger.Error("failed to start durable run", zap.Error(err))
	} else {
		state.durableRun = durableRun
	}
	pipeline := deterministicPipeline{
		name: "joint_risk",
		steps: []pipelineStep{
			{name: "collect_signals", run: e.stepCollectSignals},
			{name: "score_signals", run: e.stepScoreSignals},
			{name: "correlate_signals", run: e.stepCorrelateSignals},
			{name: "recommendation_generation", run: e.stepJointRiskRecommendations},
			{name: "llm_analysis", run: e.stepLLMAnalysis},
			{name: "finalize", run: e.stepFinalizeJointRisk},
		},
	}

	if err := pipeline.run(ctx, state); err != nil {
		e.recordWorkflowFailure(state.workflowID, "joint_risk", state.collectorID, started, err)
		if state.durableRun != nil {
			_ = e.orchestrator.FailRun(ctx, state.workflowID, err)
		}
		return JointRiskAssessment{}, err
	}
	report := state.risk
	if e.shouldEscalateFromJointRisk(report) {
		rcaReq := WorkflowRequest{
			WorkflowType: "rca",
			CollectorID:  report.CollectorID,
			Window:       state.window,
			Limit:        state.limit,
			Trigger:      "joint_risk_escalation",
			DryRun:       &state.dryRun,
		}
		if rcaReport, err := e.BuildRCAWorkflow(ctx, rcaReq); err == nil {
			report.Escalated = true
			report.EscalationReason = "risk threshold crossed and multi-signal co-occurrence confirmed"
			report.IncidentID = rcaReport.IncidentID
		} else {
			report.EscalationReason = "escalation attempt failed"
		}
	}
	if state.durableRun != nil {
		_ = e.orchestrator.CompleteRun(ctx, state.workflowID, report)
		if ref, _ := e.attachEvidencePackage(ctx, state.workflowID, report); strings.TrimSpace(ref.ArtifactID) != "" {
			report.EvidencePackagePath = firstNonEmpty(ref.LocalCachePath, ref.Path)
			report.EvidencePackageArtifact = &ref
		}
	}
	e.recordJointRisk(report)
	e.cacheJointRisk(cacheKey, report)
	e.recordWorkflowSuccess("joint_risk", state.workflowID, state.collectorID, started, map[string]any{
		"risk_level":          report.RiskLevel,
		"risk_score":          report.RiskScore,
		"retrieval_decisions": len(report.RetrievalDecisions),
		"recommendations":     len(report.Recommendations),
	})
	return report, nil
}

// BuildRCAWorkflow executes the deterministic RCA pipeline.
func (e *WorkflowEngine) BuildRCAWorkflow(ctx context.Context, req WorkflowRequest) (RCAWorkflowReport, error) {
	if e == nil || !e.cfg.Enabled {
		return RCAWorkflowReport{}, fmt.Errorf("workflow engine disabled")
	}
	started := time.Now().UTC()
	cacheKey, _, cachedRCA, hit, err := e.beginWorkflowRun(ctx, "rca", req)
	if err != nil {
		return RCAWorkflowReport{}, err
	}
	if hit {
		e.auditCachedWorkflow(cachedRCA.WorkflowID, "rca", cachedRCA.CollectorID, req, "reused recent RCA workflow result")
		return cachedRCA, nil
	}
	defer e.finishWorkflowRun(cacheKey)

	state := e.newWorkflowState("rca", req)
	e.logWorkflowEvent(zap.InfoLevel, "workflow.started", map[string]any{
		"trace_id":      state.workflowID,
		"workflow_type": "rca",
		"collector_id":  state.collectorID,
		"window":        state.window.String(),
		"trigger":       state.trigger,
		"dry_run":       state.dryRun,
	})

	durableRun, err := e.orchestrator.StartRunWithRequest(ctx, state.workflowID, "rca", state.collectorID, req)
	if err != nil {
		e.logger.Error("failed to start durable run", zap.Error(err))
	} else {
		state.durableRun = durableRun
	}
	pipeline := deterministicPipeline{
		name: "rca",
		steps: []pipelineStep{
			{name: "broad_observation", run: e.stepBroadObservation},
			{name: "incident_synthesis", run: e.stepIncidentSynthesis},
			{name: "scene_classification", run: e.stepSceneClassification},
			{name: "collection_plan_compilation", run: e.stepCompileCollectionPlan},
			{name: "targeted_recollection", run: e.stepTargetedRecollection},
			{name: "context_gathering", run: e.stepGatherRCAContext},
			{name: "hypothesis_generation", run: e.stepGenerateHypotheses},
			{name: "hypothesis_update_from_recollection", run: e.stepUpdateHypothesesFromRecollection},
			{name: "evidence_collection", run: e.stepCollectEvidence},
			{name: "llm_analysis", run: e.stepLLMAnalysis},
		},
	}
	if adaptiveRuntimeEnabled(e.cfg) {
		pipeline.steps = append(pipeline.steps, pipelineStep{name: adaptiveRuntimeStage, run: e.stepAdaptiveRuntimeLoop})
	}
	pipeline.steps = append(pipeline.steps, pipelineStep{name: "analysis_handoff_finalize", run: e.stepAnalysisHandoffFinalize})
	if e.cfg.ValidationAgentEnabled {
		pipeline.steps = append(pipeline.steps,
			pipelineStep{name: "validation_action_react_loop", run: e.stepValidationActionLoop},
			pipelineStep{name: "recommendation_generation", run: e.stepRCARecommendations},
			pipelineStep{name: "guarded_execution_plan", run: e.stepGuardedExecutionPlan},
			pipelineStep{name: "post_action_validation", run: e.stepPostActionValidation},
			pipelineStep{name: "finalize", run: e.stepFinalizeRCA},
		)
	} else {
		pipeline.steps = append(pipeline.steps,
			pipelineStep{name: "plan_act_verify_loop", run: e.stepPlanActVerifyLoop},
			pipelineStep{name: "recommendation_generation", run: e.stepRCARecommendations},
			pipelineStep{name: "guarded_execution_plan", run: e.stepGuardedExecutionPlan},
			pipelineStep{name: "finalize", run: e.stepFinalizeRCA},
		)
	}

	if err := pipeline.run(ctx, state); err != nil {
		e.recordWorkflowFailure(state.workflowID, "rca", state.collectorID, started, err)
		if state.durableRun != nil {
			_ = e.orchestrator.FailRun(ctx, state.workflowID, err)
		}
		return RCAWorkflowReport{}, err
	}
	report := state.rca
	if state.durableRun != nil {
		_ = e.orchestrator.CompleteRun(ctx, state.workflowID, report)
		if ref, manifestRef := e.attachEvidencePackage(ctx, state.workflowID, report); strings.TrimSpace(ref.ArtifactID) != "" {
			report.EvidencePackagePath = firstNonEmpty(ref.LocalCachePath, ref.Path)
			report.EvidencePackageArtifact = &ref
			if manifestRef != nil {
				report.ArtifactManifestPath = firstNonEmpty(manifestRef.LocalCachePath, manifestRef.Path)
				report.ArtifactManifestArtifact = manifestRef
			}
		}
	}
	e.recordRCA(report)
	e.recordIncident(report)
	e.cacheRCA(cacheKey, report)
	e.recordWorkflowSuccess("rca", state.workflowID, state.collectorID, started, map[string]any{
		"incident_id":        report.IncidentID,
		"status":             report.Status,
		"hypotheses":         len(report.Hypotheses),
		"recommendations":    len(report.Recommendations),
		"retrieval_docs":     len(report.RetrievedDocs),
		"retrieval_runbooks": len(report.RetrievedRunbooks),
	})
	return report, nil
}

// JointRiskReports returns recent joint-risk reports.
func (e *WorkflowEngine) JointRiskReports(limit int, collectorID string) []JointRiskAssessment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]JointRiskAssessment, 0, len(e.riskReports))
	collectorID = strings.TrimSpace(collectorID)
	for _, report := range e.riskReports {
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sortJointRiskReportsByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RCAReports returns recent structured RCA workflow reports.
func (e *WorkflowEngine) RCAReports(limit int, collectorID string) []RCAWorkflowReport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]RCAWorkflowReport, 0, len(e.rcaReports))
	collectorID = strings.TrimSpace(collectorID)
	for _, report := range e.rcaReports {
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sortRCAReportsByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// AuditRecords returns recent tool/action audit records.
func (e *WorkflowEngine) AuditRecords(limit int, workflowID string) []WorkflowAuditRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()

	workflowID = strings.TrimSpace(workflowID)
	out := make([]WorkflowAuditRecord, 0, len(e.audits))
	for _, entry := range e.audits {
		if workflowID != "" && !strings.EqualFold(entry.WorkflowID, workflowID) {
			continue
		}
		out = append(out, entry)
	}
	sortAuditByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ToolRegistry returns explicit versioned workflow tools.
func (e *WorkflowEngine) ToolRegistry() []WorkflowToolDescriptor {
	if e == nil || e.tools == nil {
		return nil
	}
	return e.tools.registry()
}

// ToolContracts returns the richer contract registry used by adaptive scoring and query shaping.
func (e *WorkflowEngine) ToolContracts() []ToolContract {
	if e == nil || e.tools == nil || e.tools.contracts == nil {
		return nil
	}
	return e.tools.contracts.List()
}

// DurableRuns returns persisted durable workflow runs.
func (e *WorkflowEngine) DurableRuns(ctx context.Context, workflowType string, limit int) ([]*DurableRun, error) {
	if e == nil || e.orchestrator == nil {
		return nil, nil
	}
	return e.orchestrator.ListRuns(ctx, workflowType, limit)
}

// DurableRun returns one persisted durable workflow run.
func (e *WorkflowEngine) DurableRun(ctx context.Context, runID string) (*DurableRun, error) {
	if e == nil || e.orchestrator == nil {
		return nil, fmt.Errorf("durable orchestrator unavailable")
	}
	return e.orchestrator.GetRun(ctx, runID)
}

// ArtifactChain returns the compact durable incident artifact chain for one run.
func (e *WorkflowEngine) ArtifactChain(ctx context.Context, runID string) (WorkflowArtifactChain, *DurableArtifactRef, error) {
	if e == nil {
		return WorkflowArtifactChain{}, nil, fmt.Errorf("workflow engine unavailable")
	}
	run, err := e.DurableRun(ctx, runID)
	if err != nil {
		return WorkflowArtifactChain{}, nil, err
	}
	if run == nil || run.EvidencePackage == nil {
		return WorkflowArtifactChain{}, nil, fmt.Errorf("artifact chain not found")
	}
	ref := cloneDurableArtifactRef((*DurableArtifactRef)(run.EvidencePackage))
	raw, err := e.ReadArtifact(ctx, (*DurableArtifactRef)(run.EvidencePackage))
	if err != nil {
		return WorkflowArtifactChain{}, ref, err
	}
	pkg, err := decodeWorkflowEvidencePackage(raw)
	if err != nil {
		return WorkflowArtifactChain{}, ref, err
	}
	if pkg.ArtifactChain == nil {
		return WorkflowArtifactChain{}, ref, fmt.Errorf("artifact chain not found")
	}
	return *pkg.ArtifactChain, ref, nil
}

func (e *WorkflowEngine) ArtifactManifest(ctx context.Context, runID string) (WorkflowArtifactManifest, *DurableArtifactRef, error) {
	if e == nil {
		return WorkflowArtifactManifest{}, nil, fmt.Errorf("workflow engine unavailable")
	}
	run, err := e.DurableRun(ctx, runID)
	if err != nil {
		return WorkflowArtifactManifest{}, nil, err
	}
	if run == nil {
		return WorkflowArtifactManifest{}, nil, fmt.Errorf("artifact manifest not found")
	}
	if run.ArtifactManifestArtifact != nil {
		ref := cloneDurableArtifactRef(run.ArtifactManifestArtifact)
		raw, err := e.ReadArtifact(ctx, run.ArtifactManifestArtifact)
		if err != nil {
			return WorkflowArtifactManifest{}, ref, err
		}
		var manifest WorkflowArtifactManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return WorkflowArtifactManifest{}, ref, err
		}
		return manifest, ref, nil
	}
	if run.EvidencePackage == nil {
		return WorkflowArtifactManifest{}, nil, fmt.Errorf("artifact manifest not found")
	}
	ref := cloneDurableArtifactRef((*DurableArtifactRef)(run.EvidencePackage))
	raw, err := e.ReadArtifact(ctx, (*DurableArtifactRef)(run.EvidencePackage))
	if err != nil {
		return WorkflowArtifactManifest{}, ref, err
	}
	pkg, err := decodeWorkflowEvidencePackage(raw)
	if err != nil {
		return WorkflowArtifactManifest{}, ref, err
	}
	if pkg.ArtifactManifest == nil {
		return WorkflowArtifactManifest{}, ref, fmt.Errorf("artifact manifest not found")
	}
	if pkg.ArtifactManifestRef != nil {
		ref = cloneDurableArtifactRef(pkg.ArtifactManifestRef)
	}
	return *pkg.ArtifactManifest, ref, nil
}

// ReadArtifact resolves a durable artifact reference through shared metadata
// first. Local cache paths are used only as a compatibility fallback.
func (e *WorkflowEngine) ReadArtifact(ctx context.Context, ref *DurableArtifactRef) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("workflow engine unavailable")
	}
	if ref == nil {
		return nil, fmt.Errorf("artifact reference is nil")
	}
	if e.artifactManager != nil && strings.TrimSpace(ref.ArtifactID) != "" {
		payload, _, err := e.artifactManager.Read(ctx, ref.ArtifactID)
		if err == nil {
			return payload, nil
		}
	}
	path := firstNonEmpty(ref.LocalCachePath, ref.Path)
	if path == "" {
		return nil, fmt.Errorf("artifact payload is unavailable for %s", strings.TrimSpace(ref.ArtifactID))
	}
	return os.ReadFile(path)
}

// IncidentReports returns recent agentic incident investigations.
func (e *WorkflowEngine) IncidentReports(limit int, status, collectorID string) []AgentIncidentReport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	status = strings.ToLower(strings.TrimSpace(status))
	collectorID = strings.TrimSpace(collectorID)
	out := make([]AgentIncidentReport, 0, len(e.incidents))
	for _, report := range e.incidents {
		if status != "" && strings.ToLower(strings.TrimSpace(report.Status)) != status {
			continue
		}
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenedAt.After(out[j].OpenedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// IncidentReport fetches one incident record.
func (e *WorkflowEngine) IncidentReport(incidentID string) (AgentIncidentReport, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	incidentID = strings.TrimSpace(incidentID)
	if incidentID == "" {
		return AgentIncidentReport{}, false
	}
	for _, report := range e.incidents {
		if strings.EqualFold(report.IncidentID, incidentID) {
			return report, true
		}
	}
	return AgentIncidentReport{}, false
}

// RefreshPotentialRiskFindings runs proactive joint-risk scans and updates findings source reports.
func (e *WorkflowEngine) RefreshPotentialRiskFindings(ctx context.Context, req WorkflowRequest) error {
	if e == nil || !e.cfg.Enabled {
		return fmt.Errorf("workflow engine disabled")
	}
	req.WorkflowType = "joint_risk"
	if strings.TrimSpace(req.CollectorID) != "" {
		_, err := e.EvaluateJointRisk(ctx, req)
		return err
	}

	maxCollectors := req.Limit
	if maxCollectors <= 0 {
		maxCollectors = 8
	}
	if maxCollectors > 24 {
		maxCollectors = 24
	}
	collectors := e.collectorIDs(maxCollectors)
	if len(collectors) == 0 {
		_, err := e.EvaluateJointRisk(ctx, req)
		return err
	}

	var firstErr error
	for _, collectorID := range collectors {
		scanReq := req
		scanReq.CollectorID = collectorID
		if _, err := e.EvaluateJointRisk(ctx, scanReq); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PotentialRiskFindings returns ranked proactive risk findings.
func (e *WorkflowEngine) PotentialRiskFindings(limit int, collectorID string) []PotentialRiskFinding {
	reports := e.JointRiskReports(0, collectorID)
	findings := make([]PotentialRiskFinding, 0, len(reports))
	for _, report := range reports {
		scope := topFindingScope(report.ScopeRisks, report.CollectorID)
		signals := make([]PotentialRiskSignal, 0, minInt(8, len(report.Signals)))
		for _, signal := range report.Signals {
			if !signal.Triggered {
				continue
			}
			signals = append(signals, PotentialRiskSignal{
				Name:         signal.Name,
				Scope:        signal.Scope,
				Entity:       signal.Entity,
				Severity:     signal.Severity,
				Current:      signal.Current,
				Baseline:     signal.Baseline,
				DeltaPercent: signal.DeltaPercent,
				Score:        signal.Score,
				Evidence:     append([]string{}, signal.Evidence...),
			})
			if len(signals) >= 8 {
				break
			}
		}
		suggestions := make([]string, 0, 8)
		for _, signal := range signals {
			suggestions = append(suggestions, fmt.Sprintf(
				"validate %s on %s/%s: current %.3f vs baseline %.3f (delta %.1f%%, score %.2f)",
				signal.Name,
				firstNonEmpty(signal.Scope, "node"),
				firstNonEmpty(signal.Entity, report.CollectorID, "fleet"),
				signal.Current,
				signal.Baseline,
				signal.DeltaPercent,
				signal.Score,
			))
			if len(suggestions) >= 4 {
				break
			}
		}
		for _, event := range report.InvestigationEvents {
			suggestions = append(suggestions, event.Title)
			if strings.TrimSpace(event.Summary) != "" {
				suggestions = append(suggestions, event.Summary)
			}
			if len(suggestions) >= 6 {
				break
			}
		}
		for _, trend := range report.TrendAssessments {
			if !trend.Triggered {
				continue
			}
			suggestions = append(suggestions, trend.OperatorHint)
			if len(suggestions) >= 6 {
				break
			}
		}
		for _, co := range report.Cooccurrences {
			suggestions = append(suggestions, fmt.Sprintf(
				"verify co-occurrence on %s/%s in %s: %s (corr %.2f, combined %.2f)",
				firstNonEmpty(co.Scope, "node"),
				firstNonEmpty(co.Entity, report.CollectorID, "fleet"),
				co.Window,
				strings.Join(co.Signals, "+"),
				co.Correlation,
				co.CombinedScore,
			))
			if len(suggestions) >= 6 {
				break
			}
		}
		for _, recommendation := range report.Recommendations {
			if strings.TrimSpace(recommendation.Summary) == "" {
				continue
			}
			suggestions = append(suggestions, recommendation.Summary)
			if len(suggestions) >= 6 {
				break
			}
		}
		confidence := clamp01(report.RiskScore + 0.03*float64(len(report.Cooccurrences)))
		riskSummary := strings.TrimSpace(report.Summary)
		if len(report.InvestigationEvents) > 0 {
			topEvent := report.InvestigationEvents[0]
			riskSummary = fmt.Sprintf(
				"%s | probable issue: %s | cause candidate: %s",
				firstNonEmpty(riskSummary, "potential latent risk detected"),
				topEvent.Title,
				firstNonEmpty(topEvent.ProbableCause, "multi-signal degradation"),
			)
		} else if len(signals) > 0 {
			top := signals[0]
			riskSummary = fmt.Sprintf(
				"%s | lead signal: %s on %s/%s current %.3f baseline %.3f delta %.1f%%",
				firstNonEmpty(riskSummary, "potential latent risk detected"),
				top.Name,
				firstNonEmpty(top.Scope, "node"),
				firstNonEmpty(top.Entity, report.CollectorID, "fleet"),
				top.Current,
				top.Baseline,
				top.DeltaPercent,
			)
		}
		findings = append(findings, PotentialRiskFinding{
			ID:                          fmt.Sprintf("risk-%s", sanitizeID(report.WorkflowID)),
			CollectorID:                 report.CollectorID,
			RiskSummary:                 riskSummary,
			TimeWindow:                  report.Window,
			Scope:                       scope,
			ConfidenceScore:             confidence,
			ContributingSignals:         signals,
			TrendAssessments:            append([]TrendAssessment(nil), report.TrendAssessments...),
			InvestigationEvents:         append([]InvestigationEvent(nil), report.InvestigationEvents...),
			SuggestedInvestigationSteps: dedupeStrings(suggestions),
			Correlations:                append([]JointRiskCooccurrence{}, report.Cooccurrences...),
			Series:                      append([]RiskSeries{}, report.Series...),
			GeneratedAt:                 report.GeneratedAt,
			RetrievedDocs:               append([]RetrievedDocumentEvidence(nil), report.RetrievedDocs...),
			RetrievedCases:              append([]RetrievedDocumentEvidence(nil), report.RetrievedCases...),
			RetrievedRunbooks:           append([]RetrievedDocumentEvidence(nil), report.RetrievedRunbooks...),
			SimilarIncidentPatterns:     append([]RetrievedDocumentEvidence(nil), report.SimilarIncidentPatterns...),
			RetrievalSummary:            report.RetrievalSummary,
			RetrievalEvidenceIDs:        append([]string(nil), report.RetrievalEvidenceIDs...),
			RetrievalConfidence:         report.RetrievalConfidence,
			RetrievalDecisions:          append([]RetrievalDecision(nil), report.RetrievalDecisions...),
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].ConfidenceScore == findings[j].ConfidenceScore {
			return findings[i].GeneratedAt.After(findings[j].GeneratedAt)
		}
		return findings[i].ConfidenceScore > findings[j].ConfidenceScore
	})
	if limit > 0 && len(findings) > limit {
		findings = findings[:limit]
	}
	return findings
}

// ControlPlaneSummary returns a compact status view of the latest workflow-derived control-plane evidence.
func (e *WorkflowEngine) ControlPlaneSummary() WorkflowControlPlaneSummary {
	if e == nil || !e.cfg.Enabled {
		return WorkflowControlPlaneSummary{Enabled: false}
	}

	e.mu.RLock()
	joint := append([]JointRiskAssessment(nil), e.riskReports...)
	rca := append([]RCAWorkflowReport(nil), e.rcaReports...)
	incidents := append([]AgentIncidentReport(nil), e.incidents...)
	e.mu.RUnlock()

	sortJointRiskReportsByTime(joint)
	sortRCAReportsByTime(rca)

	summary := WorkflowControlPlaneSummary{
		Enabled:          true,
		JointRiskReports: len(joint),
		RCAReports:       len(rca),
		Incidents:        len(incidents),
	}

	for _, report := range joint {
		switch strings.ToLower(strings.TrimSpace(report.RiskLevel)) {
		case "high", "critical":
			summary.HighRiskReports++
		}
	}

	if len(joint) > 0 {
		latest := joint[0]
		summary.LatestCollectorID = latest.CollectorID
		summary.LatestJointRiskAt = latest.GeneratedAt
		summary.LatestRiskLevel = firstNonEmpty(latest.RiskLevel, latest.Severity)
		summary.TriggeredTrends = countTriggeredTrends(latest.TrendAssessments)
		summary.InvestigationEvents = len(latest.InvestigationEvents)
		summary.WeakSignalClusters = countInvestigationEventsByCategory(latest.InvestigationEvents, "weak_signal_cluster")
		summary.RetrievalDecisions = len(latest.RetrievalDecisions)
		summary.RetrievalSkipped = countSkippedRetrievalDecisions(latest.RetrievalDecisions)
		summary.RecommendationCount = len(latest.Recommendations)
		if len(latest.InvestigationEvents) > 0 {
			summary.TopEventTitle = truncateString(latest.InvestigationEvents[0].Title, 120)
			summary.ProbableCause = truncateString(latest.InvestigationEvents[0].ProbableCause, 160)
		}
		if strings.TrimSpace(latest.Summary) != "" {
			summary.LatestIncidentSummary = truncateString(latest.Summary, 180)
		}
		summary.TopRecommendation = truncateString(topRecommendationSummary(latest.Recommendations), 160)
		if decision, ok := firstRetrievalDecision(latest.RetrievalDecisions); ok {
			summary.TopRetrievalIntent = decision.Intent
			summary.TopRetrievalQuery = truncateString(decision.Query, 160)
			summary.TopRetrievalSkipReason = truncateString(decision.SkipReason, 120)
		}
	}

	if len(rca) > 0 {
		latest := rca[0]
		summary.LatestRCAAt = latest.GeneratedAt
		if summary.LatestCollectorID == "" {
			summary.LatestCollectorID = latest.CollectorID
		}
		if summary.LatestIncidentSummary == "" {
			summary.LatestIncidentSummary = truncateString(
				firstNonEmpty(
					latest.StructuredReport.IncidentSummary,
					latest.SynthesizedIncident.Summary,
					latest.Context.IncidentSummary,
				),
				180,
			)
		}
		if summary.TopEventTitle == "" && len(latest.Context.InvestigationEvents) > 0 {
			summary.TopEventTitle = truncateString(latest.Context.InvestigationEvents[0].Title, 120)
			summary.ProbableCause = truncateString(
				firstNonEmpty(latest.Context.InvestigationEvents[0].ProbableCause, summary.ProbableCause),
				160,
			)
		}
		if summary.TriggeredTrends == 0 {
			summary.TriggeredTrends = countTriggeredTrends(latest.Context.TrendAssessments)
		}
		if summary.InvestigationEvents == 0 {
			summary.InvestigationEvents = len(latest.Context.InvestigationEvents)
		}
		if summary.WeakSignalClusters == 0 {
			summary.WeakSignalClusters = countInvestigationEventsByCategory(latest.Context.InvestigationEvents, "weak_signal_cluster")
		}
		if summary.RetrievalDecisions == 0 {
			summary.RetrievalDecisions = len(latest.Context.RetrievalDecisions)
			summary.RetrievalSkipped = countSkippedRetrievalDecisions(latest.Context.RetrievalDecisions)
		}
		if summary.RecommendationCount == 0 {
			summary.RecommendationCount = len(latest.Recommendations)
		}
		if summary.TopRetrievalIntent == "" {
			if decision, ok := firstRetrievalDecision(latest.Context.RetrievalDecisions); ok {
				summary.TopRetrievalIntent = decision.Intent
				summary.TopRetrievalQuery = truncateString(decision.Query, 160)
				summary.TopRetrievalSkipReason = truncateString(decision.SkipReason, 120)
			}
		}
		if summary.TopRecommendation == "" {
			summary.TopRecommendation = truncateString(topRecommendationSummary(latest.Recommendations), 160)
		}
		if summary.ProbableCause == "" {
			summary.ProbableCause = truncateString(
				firstNonEmpty(latest.StructuredReport.MostLikelyCause, latest.SynthesizedIncident.CandidateRootCauseCluster),
				160,
			)
		}
	}

	return summary
}

// Metrics returns workflow-engine observability counters for controller /metrics export.
func (e *WorkflowEngine) Metrics() WorkflowMetricsSnapshot {
	if e == nil || e.telemetry == nil {
		return WorkflowMetricsSnapshot{}
	}
	return e.telemetry.snapshot()
}

func countTriggeredTrends(items []TrendAssessment) int {
	total := 0
	for _, item := range items {
		if item.Triggered {
			total++
		}
	}
	return total
}

func countSkippedRetrievalDecisions(items []RetrievalDecision) int {
	total := 0
	for _, item := range items {
		if item.Skipped {
			total++
		}
	}
	return total
}

func firstRetrievalDecision(items []RetrievalDecision) (RetrievalDecision, bool) {
	if len(items) == 0 {
		return RetrievalDecision{}, false
	}
	for _, item := range items {
		if strings.TrimSpace(item.Query) != "" || strings.TrimSpace(item.Intent) != "" || item.Skipped {
			return item, true
		}
	}
	return items[0], true
}

type pipelineStep struct {
	name string
	run  func(context.Context, *workflowState) error
}

type deterministicPipeline struct {
	name  string
	steps []pipelineStep
}

func (p deterministicPipeline) run(ctx context.Context, state *workflowState) error {
	for _, step := range p.steps {
		stage := state.beginPipelineStage(ctx, step.name)
		if err := step.run(ctx, state); err != nil {
			return state.failPipelineStage(ctx, &stage, err)
		}
		state.completePipelineStage(ctx, &stage)
	}
	return nil
}

type workflowState struct {
	engine       *WorkflowEngine
	workflowType string
	workflowID   string
	collectorID  string
	window       time.Duration
	limit        int
	trigger      string
	dryRun       bool
	now          time.Time

	stages      []PipelineStageResult
	toolCalls   []WorkflowToolCall
	limitations []string

	metricsData      metricsToolData
	logsData         logsToolData
	topoData         topologyToolData
	security         securityToolData
	ebpf             ebpfToolData
	gpu              gpuToolData
	secGraph         securityGraphToolData
	lineage          processLineageToolData
	profiling        profilingToolData
	knowledge        knowledgeToolData
	changes          changeToolData
	telemetryQuality PromptTelemetryQuality

	incident              IncidentSynthesis
	riskSignals           []JointRiskSignal
	riskSeries            []RiskSeries
	trendAssessments      []TrendAssessment
	investigationEvents   []InvestigationEvent
	cooccurrences         []JointRiskCooccurrence
	scopeRisks            []ScopeRisk
	recommendation        []WorkflowRecommendation
	baselineDrifts        []BaselineDrift
	retrievedDocs         []RetrievedDocumentEvidence
	retrievedCases        []RetrievedDocumentEvidence
	retrievedRunbooks     []RetrievedDocumentEvidence
	similarPatterns       []RetrievedDocumentEvidence
	incidentMemoryMatches []RetrievedDocumentEvidence
	retrievalSummary      string
	retrievalEvidenceIDs  []string
	retrievalConfidence   float64
	retrievalDecisions    []RetrievalDecision
	changeLinks           []RCAChangeLink
	behavioralAssessments []BehavioralSignalAssessment
	adaptiveBaselines     []AdaptiveBaselineInsight

	hypotheses                  []RCAHypothesis
	hypothesisUpdates           []HypothesisUpdate
	evidence                    []RCAEvidence
	corr                        []RCACorrelation
	proposedActions             []*ProposedAction
	causal                      causalgraph.Analysis
	analysisHandoff             AnalysisHandoff
	validationReport            ValidationActionReport
	beforeActionState           *ValidationEvidenceSnapshot
	postActionEffect            *PostActionValidationSummary
	selectedAction              *ValidationActionCandidate
	selectedContract            *ValidationActionContract
	sceneClassification         SceneClassification
	collectionPlan              CollectionPlan
	recollectionResults         []RecollectionResult
	evidenceGapState            EvidenceGapState
	escalationDecision          EscalationDecision
	remainingBudget             InvestigationBudgetState
	sceneProfileStatus          *CollectorProfileStatus
	recollectionToolResults     []recollectionToolResult
	adaptiveState               *AdaptiveRuntimeState
	adaptiveDialogue            []AdaptiveDialogueTurn
	adaptiveToolDecisions       []AdaptiveToolDecision
	adaptiveArtifacts           []AdaptiveArtifact
	adaptiveNormalizedResults   []NormalizedToolResult
	adaptiveScopeHints          []string
	adaptiveNextToolFamilyHints []string
	messageManifestPath         string
	messageHistory              []AgentMessageRef
	analysisHandoffMessage      *AgentMessageRef
	validationRequestMessage    *AgentMessageRef
	validationResultMessage     *AgentMessageRef
	actionDecisionMessage       *AgentMessageRef
	postActionValidationMessage *AgentMessageRef
	compensationMessage         *AgentMessageRef

	planSteps      []AgentPlanStep
	planRevisions  []AgentPlanRevision
	planIterations int
	planReplans    int
	stepsExecuted  int
	stepsVerified  int
	planCompleted  bool
	planStopReason string

	risk JointRiskAssessment
	rca  RCAWorkflowReport

	llmAnalysis *LLMAnalysisResult
	durableRun  *DurableRun
}

type workflowCacheEntry struct {
	workflowType string
	expiresAt    time.Time
	risk         JointRiskAssessment
	rca          RCAWorkflowReport
}

type workflowInFlight struct {
	done chan struct{}
}

type normalizedWorkflowRequest struct {
	collectorID string
	window      time.Duration
	limit       int
	trigger     string
	dryRun      bool
}

func (e *WorkflowEngine) normalizeWorkflowRequest(req WorkflowRequest) normalizedWorkflowRequest {
	window := req.Window
	if window <= 0 {
		window = e.cfg.DefaultWindow
	}
	if window > e.cfg.MaxWindow {
		window = e.cfg.MaxWindow
	}
	limit := req.Limit
	if limit <= 0 {
		limit = e.cfg.MaxSamples
	}
	if limit > e.cfg.MaxSamples {
		limit = e.cfg.MaxSamples
	}
	dryRun := e.cfg.DryRun
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	return normalizedWorkflowRequest{
		collectorID: strings.TrimSpace(req.CollectorID),
		window:      window,
		limit:       limit,
		trigger:     strings.TrimSpace(req.Trigger),
		dryRun:      dryRun,
	}
}

func (e *WorkflowEngine) beginWorkflowRun(ctx context.Context, workflowType string, req WorkflowRequest) (string, JointRiskAssessment, RCAWorkflowReport, bool, error) {
	if e == nil {
		return "", JointRiskAssessment{}, RCAWorkflowReport{}, false, fmt.Errorf("workflow engine is nil")
	}
	key := e.workflowRequestKey(workflowType, req)
	for {
		e.cacheMu.Lock()
		e.pruneWorkflowCacheLocked(time.Now().UTC())
		if entry, ok := e.recentRuns[key]; ok {
			e.cacheMu.Unlock()
			return key, entry.risk, entry.rca, true, nil
		}
		if inflight, ok := e.inFlightRuns[key]; ok {
			done := inflight.done
			e.cacheMu.Unlock()
			select {
			case <-ctx.Done():
				return key, JointRiskAssessment{}, RCAWorkflowReport{}, false, ctx.Err()
			case <-done:
				continue
			}
		}
		e.inFlightRuns[key] = &workflowInFlight{done: make(chan struct{})}
		e.cacheMu.Unlock()
		return key, JointRiskAssessment{}, RCAWorkflowReport{}, false, nil
	}
}

func (e *WorkflowEngine) finishWorkflowRun(key string) {
	if e == nil || strings.TrimSpace(key) == "" {
		return
	}
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if inflight, ok := e.inFlightRuns[key]; ok {
		delete(e.inFlightRuns, key)
		close(inflight.done)
	}
}

func (e *WorkflowEngine) cacheJointRisk(key string, report JointRiskAssessment) {
	if e == nil || strings.TrimSpace(key) == "" || e.cfg.RequestDedupeTTL <= 0 {
		return
	}
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	e.recentRuns[key] = workflowCacheEntry{
		workflowType: "joint_risk",
		expiresAt:    time.Now().UTC().Add(e.cfg.RequestDedupeTTL),
		risk:         report,
	}
	e.pruneWorkflowCacheLocked(time.Now().UTC())
}

func (e *WorkflowEngine) cacheRCA(key string, report RCAWorkflowReport) {
	if e == nil || strings.TrimSpace(key) == "" || e.cfg.RequestDedupeTTL <= 0 {
		return
	}
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	e.recentRuns[key] = workflowCacheEntry{
		workflowType: "rca",
		expiresAt:    time.Now().UTC().Add(e.cfg.RequestDedupeTTL),
		rca:          report,
	}
	e.pruneWorkflowCacheLocked(time.Now().UTC())
}

func (e *WorkflowEngine) pruneWorkflowCacheLocked(now time.Time) {
	for key, entry := range e.recentRuns {
		if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
			delete(e.recentRuns, key)
		}
	}
	maxEntries := e.cfg.RequestDedupeEntries
	if maxEntries <= 0 || len(e.recentRuns) <= maxEntries {
		return
	}
	for len(e.recentRuns) > maxEntries {
		oldestKey := ""
		var oldestExpiry time.Time
		for key, entry := range e.recentRuns {
			if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
				oldestKey = key
				oldestExpiry = entry.expiresAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(e.recentRuns, oldestKey)
	}
}

func (e *WorkflowEngine) workflowRequestKey(workflowType string, req WorkflowRequest) string {
	normalized := e.normalizeWorkflowRequest(req)
	return fmt.Sprintf(
		"%s|collector=%s|window=%s|limit=%d|trigger=%s|dry_run=%t",
		strings.TrimSpace(strings.ToLower(workflowType)),
		strings.TrimSpace(strings.ToLower(normalized.collectorID)),
		normalized.window,
		normalized.limit,
		strings.TrimSpace(strings.ToLower(normalized.trigger)),
		normalized.dryRun,
	)
}

func (e *WorkflowEngine) auditCachedWorkflow(workflowID, workflowType, collectorID string, req WorkflowRequest, summary string) {
	if e == nil || strings.TrimSpace(workflowID) == "" {
		return
	}
	normalized := e.normalizeWorkflowRequest(req)
	e.audit(
		workflowID,
		workflowType,
		"cache",
		"workflow.cache_hit",
		"success",
		collectorID,
		normalized.dryRun,
		false,
		true,
		map[string]string{
			"window":  normalized.window.String(),
			"limit":   fmt.Sprintf("%d", normalized.limit),
			"trigger": normalized.trigger,
		},
		summary,
		nil,
	)
}

func (e *WorkflowEngine) newWorkflowState(workflowType string, req WorkflowRequest) *workflowState {
	normalized := e.normalizeWorkflowRequest(req)

	return &workflowState{
		engine:                  e,
		workflowType:            workflowType,
		workflowID:              newQueryID(),
		collectorID:             strings.TrimSpace(req.CollectorID),
		window:                  normalized.window,
		limit:                   normalized.limit,
		trigger:                 normalized.trigger,
		dryRun:                  normalized.dryRun,
		now:                     time.Now().UTC(),
		stages:                  make([]PipelineStageResult, 0, 8),
		toolCalls:               make([]WorkflowToolCall, 0, 12),
		limitations:             make([]string, 0, 8),
		riskSignals:             make([]JointRiskSignal, 0, 24),
		riskSeries:              make([]RiskSeries, 0, 16),
		trendAssessments:        make([]TrendAssessment, 0, 12),
		investigationEvents:     make([]InvestigationEvent, 0, 10),
		cooccurrences:           make([]JointRiskCooccurrence, 0, 8),
		scopeRisks:              make([]ScopeRisk, 0, 16),
		recommendation:          make([]WorkflowRecommendation, 0, 12),
		baselineDrifts:          make([]BaselineDrift, 0, 8),
		retrievedDocs:           make([]RetrievedDocumentEvidence, 0, 8),
		retrievedCases:          make([]RetrievedDocumentEvidence, 0, 6),
		retrievedRunbooks:       make([]RetrievedDocumentEvidence, 0, 6),
		similarPatterns:         make([]RetrievedDocumentEvidence, 0, 6),
		incidentMemoryMatches:   make([]RetrievedDocumentEvidence, 0, 4),
		retrievalDecisions:      make([]RetrievalDecision, 0, 8),
		changeLinks:             make([]RCAChangeLink, 0, 6),
		behavioralAssessments:   make([]BehavioralSignalAssessment, 0, 8),
		adaptiveBaselines:       make([]AdaptiveBaselineInsight, 0, 8),
		hypotheses:              make([]RCAHypothesis, 0, 12),
		hypothesisUpdates:       make([]HypothesisUpdate, 0, 16),
		evidence:                make([]RCAEvidence, 0, 24),
		corr:                    make([]RCACorrelation, 0, 12),
		proposedActions:         make([]*ProposedAction, 0, 12),
		planSteps:               make([]AgentPlanStep, 0, 12),
		planRevisions:           make([]AgentPlanRevision, 0, 8),
		recollectionResults:     make([]RecollectionResult, 0, maxSceneRecollectionRounds),
		recollectionToolResults: make([]recollectionToolResult, 0, 8),
		planIterations:          1,
	}
}

func (s *workflowState) stageSummary(name string) string {
	switch name {
	case "collect_signals", "anomaly_detection", "broad_observation":
		return fmt.Sprintf("tools=%d signals=%d", len(s.toolCalls), len(s.riskSignals))
	case "score_signals":
		return fmt.Sprintf("series=%d score=%.2f", len(s.riskSeries), s.risk.RiskScore)
	case "correlate_signals":
		return fmt.Sprintf("cooccurrences=%d", len(s.cooccurrences))
	case "recommendation_generation":
		if s.workflowType == "joint_risk" {
			return fmt.Sprintf("recommendations=%d", len(s.recommendation))
		}
		return fmt.Sprintf("hypotheses=%d recommendations=%d", len(s.hypotheses), len(s.recommendation))
	case "context_gathering":
		metricCount := 0
		if s.metricsData.Node != nil {
			metricCount = len(s.metricsData.Node.Metrics)
		}
		return fmt.Sprintf("metrics=%d logs=%d retrieved_docs=%d", metricCount, len(s.logsData.Snippets), len(s.retrievedDocs))
	case "incident_synthesis":
		return fmt.Sprintf("signals=%d scope=%d confidence=%.2f", len(s.incident.GroupedSignals), len(s.incident.ImpactedScope), s.incident.Confidence)
	case "scene_classification":
		return fmt.Sprintf("scene=%s confidence=%.2f", s.sceneClassification.SceneFamily, s.sceneClassification.Confidence)
	case "collection_plan_compilation":
		return fmt.Sprintf("plan=%s modules=%d round=%d", s.collectionPlan.PlanID, len(s.collectionPlan.TargetCollectorsOrModules), s.collectionPlan.RoundIndex)
	case "targeted_recollection":
		return fmt.Sprintf("rounds=%d gaps=%d", len(s.recollectionResults), len(s.evidenceGapState.MissingEvidence))
	case "hypothesis_update_from_recollection":
		return fmt.Sprintf("hypotheses=%d scene=%s", len(s.hypotheses), s.sceneClassification.SceneFamily)
	case "hypothesis_generation":
		return fmt.Sprintf("hypotheses=%d", len(s.hypotheses))
	case "evidence_collection":
		return fmt.Sprintf("evidence=%d knowledge=%d", len(s.evidence), len(s.retrievedDocs))
	case "adaptive_runtime_loop":
		if s.adaptiveState != nil {
			return fmt.Sprintf("adaptive_iterations=%d tool_calls=%d stop=%s", s.adaptiveState.Iteration, s.adaptiveState.ToolCalls, firstNonEmpty(s.adaptiveState.StopReason, "checkpointed"))
		}
		return "adaptive runtime skipped"
	case "analysis_handoff_finalize":
		return fmt.Sprintf("handoff_hypotheses=%d validation_targets=%d", len(s.analysisHandoff.Hypotheses), len(s.analysisHandoff.SuggestedValidationTargets))
	case "validation_action_react_loop":
		return fmt.Sprintf("targets=%d tool_calls=%d stop=%s", len(s.validationReport.Results), s.validationReport.ToolCalls, firstNonEmpty(s.validationReport.StopReason, "completed"))
	case "plan_act_verify_loop":
		return fmt.Sprintf("iterations=%d replans=%d executed=%d verified=%d", s.planIterations, s.planReplans, s.stepsExecuted, s.stepsVerified)
	case "guarded_execution_plan":
		return "guarded actions prepared"
	case "post_action_validation":
		if s.validationReport.PostActionValidation != nil {
			return s.validationReport.PostActionValidation.Summary
		}
		return "no post-action validation recorded"
	case "finalize":
		return "report materialized"
	default:
		return "completed"
	}
}

func (s *workflowState) callTool(ctx context.Context, stage string, tool ToolName, query map[string]string, stepID ...string) (workflowToolResult, error) {
	return s.callToolAs(ctx, stage, actorForWorkflowStage(stage), tool, query, stepID...)
}

func actorForWorkflowStage(stage string) string {
	switch strings.TrimSpace(stage) {
	case "collect_signals", "score_signals", "correlate_signals", "broad_observation", "incident_synthesis", "scene_classification", "collection_plan_compilation", "targeted_recollection", "context_gathering", "hypothesis_generation", "hypothesis_update_from_recollection", "evidence_collection", "llm_analysis", "analysis_handoff_finalize":
		return "analysis_agent"
	case "adaptive_runtime_loop":
		return "adaptive_planner"
	case "validation_action_react_loop", "post_action_validation", "guarded_execution_plan":
		return "validation_action_agent"
	default:
		return "workflow_engine"
	}
}

func (s *workflowState) callToolAs(ctx context.Context, stage, actor string, tool ToolName, query map[string]string, stepID ...string) (workflowToolResult, error) {
	request := workflowToolRequest{
		WorkflowID:  s.workflowID,
		Workflow:    s.workflowType,
		Stage:       stage,
		Actor:       firstNonEmpty(strings.TrimSpace(actor), actorForWorkflowStage(stage)),
		StepID:      firstNonEmpty(stepID...),
		CollectorID: s.collectorID,
		Window:      s.window,
		Limit:       s.limit,
		Query:       query,
		DryRun:      s.dryRun,
	}
	call, result, err := s.engine.tools.call(ctx, request, tool)
	s.toolCalls = append(s.toolCalls, call)
	if s.engine.orchestrator != nil && s.durableRun != nil {
		_ = s.engine.orchestrator.RecordToolCall(ctx, s.workflowID, call)
		if request.StepID != "" {
			_ = s.engine.orchestrator.AttachPolicy(ctx, s.workflowID, request.StepID, DurablePolicyRecord{
				Decision:      call.Policy,
				PolicyVersion: call.PolicyVersion,
				RiskTag:       call.RiskTag,
				EvaluatedAt:   call.CompletedAt,
			})
			if call.ApprovalState != "" {
				_ = s.engine.orchestrator.AttachApproval(ctx, s.workflowID, request.StepID, DurableApprovalRecord{
					State:       call.ApprovalState,
					RequestedAt: call.StartedAt,
				})
			}
		}
	}

	status := workflowToolStoredOutcome(call)
	if status == "" {
		status = "success"
	}
	summary := call.Summary
	if err != nil {
		status = workflowToolStoredOutcome(call)
		if status == "" {
			status = "failed"
		}
		summary = err.Error()
	}
	if s.engine != nil && s.engine.telemetry != nil {
		family := ""
		if s.engine.tools != nil && s.engine.tools.contracts != nil {
			if contract, ok := s.engine.tools.contracts.Get(tool); ok {
				family = string(contract.CapabilityFamily)
			}
		}
		s.engine.telemetry.recordSkillInvocation(tool, family, firstNonEmpty(call.Outcome, call.InvocationStatus, status), s.engine.cfg.RuntimeMode, call.CompletedAt.Sub(call.StartedAt))
		if call.Policy.Status == "blocked" || call.InvocationStatus == "blocked" {
			s.engine.telemetry.recordSkillPolicyBlock(tool, firstNonEmpty(call.Policy.Reason, call.ErrorMessage, "blocked"), s.engine.cfg.RuntimeMode)
		}
		if call.Policy.RequiresApproval || call.InvocationStatus == "approval_required" {
			s.engine.telemetry.recordSkillApprovalRequired(tool, s.engine.cfg.RuntimeMode)
		}
	}
	auditInput := map[string]string{
		"tool":             string(tool),
		"selected_skill":   string(tool),
		"tool_call_status": workflowToolInvocationStatus(call),
		"policy_verdict":   firstNonEmpty(call.Policy.Status, "allowed"),
		"approval_state":   call.ApprovalState,
		"evidence_ids":     call.ID,
	}
	if s.adaptiveState != nil {
		auditInput["iteration"] = strconv.Itoa(s.adaptiveState.Iteration)
		auditInput["objective"] = s.adaptiveState.Objective
	}
	s.engine.audit(
		s.workflowID,
		s.workflowType,
		stage,
		"tool.call",
		status,
		s.collectorID,
		s.dryRun,
		s.engine.cfg.RequireApproval,
		workflowToolCallApproved(call),
		auditInput,
		summary,
		err,
	)
	if err != nil {
		return workflowToolResult{}, err
	}
	return result, nil
}

func (s *workflowState) applyKnowledgeResult(result workflowToolResult) {
	data, ok := result.Data.(knowledgeToolData)
	if !ok {
		return
	}
	s.knowledge = data
	if strings.TrimSpace(data.Summary) != "" {
		if strings.TrimSpace(s.retrievalSummary) == "" {
			s.retrievalSummary = data.Summary
		} else if !strings.Contains(strings.ToLower(s.retrievalSummary), strings.ToLower(data.Summary)) {
			s.retrievalSummary = s.retrievalSummary + "; " + data.Summary
		}
	}
	if data.Confidence > 0 {
		s.retrievalConfidence = maxFloat(s.retrievalConfidence, data.Confidence)
	}
	if s.engine != nil && s.engine.telemetry != nil {
		s.engine.telemetry.recordRetrieval(len(data.Hits))
		status := "miss"
		if len(data.Hits) > 0 {
			status = "hit"
		}
		s.engine.telemetry.recordRAGSkillCall(data.Intent, status)
	}
	if len(data.Hits) > 0 {
		s.retrievedDocs = mergeRetrievedDocumentEvidence(s.retrievedDocs, data.Hits)
		s.retrievedCases = categorizeRetrievedEvidence(s.retrievedDocs, "historical_incident", "operational_qa")
		s.retrievedRunbooks = categorizeRetrievedEvidence(s.retrievedDocs, "runbook")
		s.similarPatterns = categorizeRetrievedEvidence(s.retrievedDocs, "question_pattern", "historical_incident")
		s.incidentMemoryMatches = filterRetrievedEvidenceBySourceType(s.retrievedDocs, "incident_memory")
	}
	if len(data.EvidenceIDs) > 0 {
		s.retrievalEvidenceIDs = dedupeStrings(append(s.retrievalEvidenceIDs, data.EvidenceIDs...))
	}
}

func mergeRetrievedDocumentEvidence(base, incoming []RetrievedDocumentEvidence) []RetrievedDocumentEvidence {
	if len(incoming) == 0 {
		return append([]RetrievedDocumentEvidence(nil), base...)
	}
	best := make(map[string]RetrievedDocumentEvidence, len(base)+len(incoming))
	order := make([]string, 0, len(base)+len(incoming))
	record := func(item RetrievedDocumentEvidence) {
		key := strings.TrimSpace(firstNonEmpty(item.ChunkID, item.DocID, item.EvidenceID))
		if key == "" {
			return
		}
		if current, ok := best[key]; ok {
			if item.Score <= current.Score {
				return
			}
		} else {
			order = append(order, key)
		}
		best[key] = item
	}
	for _, item := range base {
		record(item)
	}
	for _, item := range incoming {
		record(item)
	}
	out := make([]RetrievedDocumentEvidence, 0, len(best))
	for _, key := range order {
		if item, ok := best[key]; ok {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func categorizeRetrievedEvidence(in []RetrievedDocumentEvidence, values ...string) []RetrievedDocumentEvidence {
	if len(in) == 0 || len(values) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	out := make([]RetrievedDocumentEvidence, 0, len(in))
	for _, item := range in {
		knowledgeType := strings.ToLower(strings.TrimSpace(item.KnowledgeType))
		caseType := strings.ToLower(strings.TrimSpace(item.CaseType))
		if _, ok := allowed[knowledgeType]; ok {
			out = append(out, item)
			continue
		}
		if _, ok := allowed[caseType]; ok {
			out = append(out, item)
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func filterRetrievedEvidenceBySourceType(in []RetrievedDocumentEvidence, sourceType string) []RetrievedDocumentEvidence {
	if len(in) == 0 || strings.TrimSpace(sourceType) == "" {
		return nil
	}
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	out := make([]RetrievedDocumentEvidence, 0, len(in))
	for _, item := range in {
		if strings.EqualFold(strings.TrimSpace(item.SourceType), sourceType) {
			out = append(out, item)
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func (s *workflowState) refreshDerivedEvidence() {
	if s == nil {
		return
	}
	s.riskSeries = buildRiskSeries(s.metricsData.History, s.logsData)
	s.behavioralAssessments = nil
	if s.engine != nil && s.engine.behaviorMemory != nil {
		s.behavioralAssessments = s.engine.behaviorMemory.Evaluate(BehavioralMemoryRequest{
			CollectorID: s.collectorID,
			Node:        s.metricsData.Node,
			Fleet:       s.metricsData.Fleet,
			Series:      s.riskSeries,
			Logs:        s.logsData,
			Security:    s.security,
			EBPF:        s.ebpf,
			ChangeLinks: s.changeLinks,
			Now:         s.now,
		})
	}
	behaviorIndex := behavioralAssessmentIndex(s.behavioralAssessments)
	s.riskSignals = buildRiskSignals(s.collectorID, s.riskSeries, s.security, s.ebpf, behaviorIndex)
	s.scopeRisks = buildScopeRisks(s.metricsData, s.riskSignals)
	s.cooccurrences = buildCooccurrences(s.riskSignals, s.riskSeries, s.window, s.collectorID)
}

func (s *workflowState) refreshInvestigationContext() {
	if s == nil {
		return
	}
	s.refreshDerivedEvidence()
	s.refreshTelemetryQuality()
	current := map[string]float64{}
	if s.metricsData.Node != nil {
		current = s.metricsData.Node.Metrics
	}
	s.trendAssessments = buildTrendAssessments(s.collectorID, s.riskSeries, current, s.metricsData.History, s.window, behavioralAssessmentIndex(s.behavioralAssessments))
	if s.engine != nil && s.engine.baseline != nil && s.collectorID != "" {
		s.baselineDrifts = syncBaselineMetrics(s.engine.baseline, s.collectorID, s.metricsData.History, current, s.now)
	} else {
		s.baselineDrifts = s.baselineDrifts[:0]
	}
	s.investigationEvents = buildInvestigationEvents(s.collectorID, s.trendAssessments, s.cooccurrences, s.baselineDrifts)
}

func (s *workflowState) applyToolResult(_ ToolName, result workflowToolResult) {
	if s == nil {
		return
	}
	refreshEvidence := false
	switch data := result.Data.(type) {
	case metricsToolData:
		if data.Node != nil || len(data.History) > 0 || len(s.metricsData.History) == 0 {
			s.metricsData = data
			s.collectorID = firstNonEmpty(s.collectorID, data.CollectorID)
			refreshEvidence = true
		}
	case logsToolData:
		if data.Errors > 0 || data.Warnings > 0 || len(data.Snippets) > 0 || len(s.logsData.Snippets) == 0 {
			s.logsData = data
			refreshEvidence = true
		}
	case changeToolData:
		if len(data.Events) > 0 || len(s.changes.Events) == 0 {
			s.applyChangeResult(result)
		}
	case topologyToolData:
		if len(data.Snapshot.Nodes) > 0 || len(data.Snapshot.Edges) > 0 || strings.TrimSpace(data.Snapshot.Summary) != "" || strings.TrimSpace(s.topoData.Snapshot.Summary) == "" {
			s.topoData = data
		}
	case securityToolData:
		if len(data.Findings) > 0 || len(data.SuspiciousPortCandidates) > 0 || len(data.WeakPermissionHints) > 0 || len(s.security.Findings) == 0 {
			s.security = data
			refreshEvidence = true
		}
	case ebpfToolData:
		if len(data.RuntimeEvents) > 0 || len(data.SyscallStatistics) > 0 || len(s.ebpf.RuntimeEvents) == 0 {
			s.ebpf = data
			refreshEvidence = true
		}
	case gpuToolData:
		if len(data.Metrics) > 0 || len(data.TopProcesses) > 0 || len(s.gpu.Metrics) == 0 {
			s.gpu = data
			refreshEvidence = true
		}
	case securityGraphToolData:
		if len(data.Nodes) > 0 || len(data.Edges) > 0 || len(s.secGraph.Nodes) == 0 {
			s.secGraph = data
		}
	case processLineageToolData:
		if len(data.Nodes) > 0 || len(data.Edges) > 0 || len(data.Paths) > 0 || len(s.lineage.Paths) == 0 {
			s.lineage = data
		}
	case profilingToolData:
		s.profiling = data
	case knowledgeToolData:
		s.applyKnowledgeResult(result)
	}
	if refreshEvidence {
		s.refreshInvestigationContext()
		s.adaptiveBaselines = deriveAdaptiveBaselineInsights(s)
	}
}

func (s *workflowState) refreshTelemetryQuality() {
	if s == nil {
		return
	}
	metrics := map[string]float64{}
	processes := []PromptProcess(nil)
	logs := []PromptLog(nil)
	if s.metricsData.Node != nil {
		metrics = cloneMetricMap(s.metricsData.Node.Metrics)
		processes = summarizeProcesses(s.metricsData.Node.Processes, 5)
		logs = summarizeLogs(s.metricsData.Node.Logs, 5)
	}
	for name, value := range s.gpu.Metrics {
		metrics[name] = value
	}
	if len(logs) == 0 {
		logs = workflowPromptLogsFromToolData(s.logsData, 5)
	}
	staleAfter := 2 * time.Minute
	if s.engine != nil && s.engine.cfg.MaxTelemetryAge > 0 {
		staleAfter = s.engine.cfg.MaxTelemetryAge
	}
	queryAt := s.now
	if queryAt.IsZero() {
		queryAt = time.Now().UTC()
	}
	s.telemetryQuality = assessPromptTelemetryQuality(s.metricsData.Node, metrics, processes, logs, queryAt, staleAfter)
}

func workflowPromptLogsFromToolData(data logsToolData, limit int) []PromptLog {
	if limit <= 0 {
		limit = 5
	}
	type row struct {
		fingerprint string
		count       uint64
		example     string
	}
	rows := make([]row, 0, len(data.Snippets))
	index := make(map[string]int, len(data.Snippets))
	for _, snippet := range data.Snippets {
		example := strings.TrimSpace(snippet)
		if example == "" {
			continue
		}
		fingerprint := sanitizeID(example)
		if fingerprint == "" {
			fingerprint = "workflow-log-snippet"
		}
		if pos, ok := index[fingerprint]; ok {
			rows[pos].count++
			continue
		}
		index[fingerprint] = len(rows)
		rows = append(rows, row{fingerprint: fingerprint, count: 1, example: example})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count == rows[j].count {
			return rows[i].fingerprint < rows[j].fingerprint
		}
		return rows[i].count > rows[j].count
	})
	out := make([]PromptLog, 0, minInt(len(rows), limit))
	for _, item := range rows {
		out = append(out, PromptLog{
			Fingerprint: item.fingerprint,
			Count:       item.count,
			Example:     item.example,
		})
		if len(out) >= limit {
			return out
		}
	}
	if len(out) == 0 {
		total := data.Total
		if total == 0 {
			total = data.Errors + data.Warnings
		}
		if total > 0 || len(data.RecentDeploys) > 0 {
			count := total
			if count == 0 {
				count = 1
			}
			out = append(out, PromptLog{
				Fingerprint: "workflow-log-query",
				Count:       count,
				Example:     firstNonEmpty(firstString(data.Snippets), firstString(data.RecentDeploys), "workflow log evidence was available"),
			})
		}
	}
	return out
}

func workflowLimitations(state *workflowState) []string {
	if state == nil {
		return nil
	}
	limitations := append([]string{}, state.limitations...)
	limitations = append(limitations, telemetryQualityLimitations(state.telemetryQuality)...)
	return dedupeStrings(limitations)
}

func telemetryQualityLimitations(quality PromptTelemetryQuality) []string {
	limitations := make([]string, 0, 6)
	switch quality.State {
	case "unavailable":
		limitations = append(limitations, "Workflow telemetry was unavailable for this analysis window")
	case "stale":
		limitations = append(limitations, fmt.Sprintf("Workflow telemetry age %.0fs exceeded stale threshold", quality.FreshnessAgeSeconds))
	case "delayed":
		limitations = append(limitations, fmt.Sprintf("Workflow telemetry ingest delay %.0fs exceeded the normal bound", quality.IngestDelaySeconds))
	case "degraded":
		limitations = append(limitations, fmt.Sprintf("Workflow telemetry coverage is degraded (coverage %.0f%%)", quality.CoveragePercent))
	}
	limitations = append(limitations, sanitizeStrings(quality.BlindSpots)...)
	if len(quality.MissingSignals) > 0 {
		limitations = append(limitations, "Missing critical signals: "+strings.Join(quality.MissingSignals, ", "))
	}
	return dedupeStrings(limitations)
}

func telemetryQualityReason(quality PromptTelemetryQuality) string {
	switch quality.State {
	case "fresh":
		return fmt.Sprintf("telemetry is fresh with %.0f%% critical-signal coverage", quality.CoveragePercent)
	case "delayed":
		return fmt.Sprintf("telemetry is delayed by %.0fs, so fast-moving symptoms may already have shifted", quality.IngestDelaySeconds)
	case "degraded":
		return firstNonEmpty(firstString(quality.BlindSpots), fmt.Sprintf("telemetry coverage is degraded at %.0f%%", quality.CoveragePercent))
	case "stale":
		return fmt.Sprintf("telemetry is stale at %.0fs old, so flat signals may be misleading", quality.FreshnessAgeSeconds)
	case "unavailable":
		return firstNonEmpty(firstString(quality.BlindSpots), "telemetry was unavailable during the workflow window")
	default:
		return ""
	}
}

func telemetryConfidenceCeiling(quality PromptTelemetryQuality) float64 {
	if quality.Confidence <= 0 {
		return 1
	}
	return clamp01(0.2 + quality.Confidence*0.8)
}

func applyTelemetryConfidenceLimit(confidence float64, quality PromptTelemetryQuality) float64 {
	confidence = clamp01(confidence)
	if quality.Confidence <= 0 {
		return confidence
	}
	return math.Min(confidence, telemetryConfidenceCeiling(quality))
}

func buildKnowledgeQuery(state *workflowState, phase string, tool ToolName) (map[string]string, RetrievalDecision) {
	if state == nil {
		return nil, RetrievalDecision{}
	}
	intent := "general"
	switch tool {
	case ToolRunbookRetrieval:
		intent = "runbook"
	case ToolHistoricalIncident:
		intent = "historical_incident"
	case ToolSimilarCase:
		intent = "joint_risk"
	}

	decision := RetrievalDecision{
		Phase:  phase,
		Tool:   string(tool),
		Intent: intent,
	}
	addPart := func(parts *[]string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		*parts = append(*parts, value)
	}

	parts := make([]string, 0, 24)
	meaningful := 0
	addPart(&parts, state.workflowType)
	addPart(&parts, phase)
	addPart(&parts, state.collectorID)
	addPart(&parts, state.trigger)
	if strings.TrimSpace(state.incident.Summary) != "" {
		addPart(&parts, state.incident.Summary)
		meaningful++
	}
	for _, event := range state.investigationEvents {
		addPart(&parts, event.Title)
		addPart(&parts, event.ProbableCause)
		addPart(&parts, event.RetrievalHint)
		decision.EvidenceSignals = append(decision.EvidenceSignals, event.SupportingSignals...)
		meaningful++
		if meaningful >= 4 {
			break
		}
	}
	for _, trend := range state.trendAssessments {
		if !trend.Triggered && trend.Confidence < 0.6 {
			continue
		}
		addPart(&parts, trend.Display)
		addPart(&parts, trend.Trend)
		addPart(&parts, trend.OperatorHint)
		decision.EvidenceSignals = append(decision.EvidenceSignals, trend.Display)
		meaningful++
		if meaningful >= 6 {
			break
		}
	}
	if meaningful < 3 {
		for _, signal := range topTriggeredSignals(state.riskSignals, 4) {
			addPart(&parts, signal.Name)
			addPart(&parts, signal.Entity)
			decision.EvidenceSignals = append(decision.EvidenceSignals, signal.Name)
			meaningful++
			if meaningful >= 5 {
				break
			}
		}
	}
	for _, snippet := range state.logsData.Snippets {
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		addPart(&parts, truncateString(snippet, 160))
		meaningful++
		if meaningful >= 7 {
			break
		}
	}
	for _, finding := range state.security.Findings {
		addPart(&parts, truncateString(finding, 120))
		meaningful++
		if meaningful >= 8 {
			break
		}
	}
	for _, deploy := range state.logsData.RecentDeploys {
		addPart(&parts, deploy)
		meaningful++
		if meaningful >= 9 {
			break
		}
	}
	decision.EvidenceSignals = dedupeStrings(decision.EvidenceSignals)
	query := strings.TrimSpace(strings.Join(dedupeStrings(parts), " "))
	if meaningful == 0 || len(query) < 24 {
		decision.Skipped = true
		decision.SkipReason = "no ranked investigation evidence or operational context"
		return nil, decision
	}
	decision.Query = truncateString(query, 420)
	return map[string]string{
		"query":  decision.Query,
		"top_k":  "5",
		"intent": intent,
	}, decision
}

func (e *WorkflowEngine) stepCollectSignals(ctx context.Context, state *workflowState) error {
	metricsResult, err := state.callTool(ctx, "collect_signals", ToolMetrics, nil)
	if err != nil {
		return err
	}
	metricsData, ok := metricsResult.Data.(metricsToolData)
	if !ok {
		return fmt.Errorf("metrics tool returned invalid payload")
	}
	state.metricsData = metricsData
	state.collectorID = firstNonEmpty(state.collectorID, metricsData.CollectorID)

	logsResult, err := state.callTool(ctx, "collect_signals", ToolLogs, map[string]string{"query": "error warn timeout deployment"})
	if err != nil {
		state.limitations = append(state.limitations, "log query tool unavailable")
	} else if logsData, ok := logsResult.Data.(logsToolData); ok {
		state.logsData = logsData
	}

	changeQuery := map[string]string{
		"query":            firstNonEmpty(strings.Join(state.logsData.RecentDeploys, " "), state.trigger, "deploy rollout config driver feature flag"),
		"incident_summary": firstNonEmpty(state.trigger, strings.Join(state.logsData.Snippets, " ")),
		"scope":            firstNonEmpty(state.collectorID, "fleet"),
	}
	changeResult, err := state.callTool(ctx, "collect_signals", ToolChangeQuery, changeQuery)
	if err != nil {
		state.limitations = append(state.limitations, "change intelligence unavailable")
	} else if changeData, ok := changeResult.Data.(changeToolData); ok {
		state.changes = changeData
		state.changeLinks = changeLinksFromToolData(changeData)
	}

	topologyResult, err := state.callTool(ctx, "collect_signals", ToolTopology, nil)
	if err != nil {
		state.limitations = append(state.limitations, "topology query unavailable")
	} else if topologyData, ok := topologyResult.Data.(topologyToolData); ok {
		state.topoData = topologyData
	}

	securityResult, err := state.callTool(ctx, "collect_signals", ToolSecurity, nil)
	if err != nil {
		state.limitations = append(state.limitations, "security tool unavailable")
	} else if securityData, ok := securityResult.Data.(securityToolData); ok {
		state.security = securityData
	}

	ebpfResult, err := state.callTool(ctx, "collect_signals", ToolEBPFQuery, nil)
	if err != nil {
		state.limitations = append(state.limitations, "ebpf query tool unavailable")
	} else if ebpfData, ok := ebpfResult.Data.(ebpfToolData); ok {
		state.ebpf = ebpfData
	}

	gpuResult, err := state.callTool(ctx, "collect_signals", ToolGPU, nil)
	if err != nil {
		state.limitations = append(state.limitations, "gpu query tool unavailable")
	} else if gpuData, ok := gpuResult.Data.(gpuToolData); ok {
		state.gpu = gpuData
	}

	secGraphResult, err := state.callTool(ctx, "collect_signals", ToolSecurityGraph, nil)
	if err != nil {
		state.limitations = append(state.limitations, "security graph tool unavailable")
	} else if graphData, ok := secGraphResult.Data.(securityGraphToolData); ok {
		state.secGraph = graphData
	}

	lineageResult, err := state.callTool(ctx, "collect_signals", ToolProcessLineage, nil)
	if err != nil {
		state.limitations = append(state.limitations, "process lineage tool unavailable")
	} else if lineageData, ok := lineageResult.Data.(processLineageToolData); ok {
		state.lineage = lineageData
	}

	state.refreshInvestigationContext()
	state.adaptiveBaselines = deriveAdaptiveBaselineInsights(state)
	if len(state.riskSignals) == 0 {
		state.limitations = append(state.limitations, "insufficient historical metrics for weighted risk scoring")
	}
	for _, tool := range []ToolName{ToolSimilarCase, ToolRunbookRetrieval} {
		query, decision := buildKnowledgeQuery(state, "collect_signals", tool)
		state.retrievalDecisions = append(state.retrievalDecisions, decision)
		if decision.Skipped {
			continue
		}
		knowledgeResult, err := state.callTool(ctx, "collect_signals", tool, query)
		if err != nil {
			state.limitations = append(state.limitations, fmt.Sprintf("%s unavailable", tool))
			continue
		}
		state.applyKnowledgeResult(knowledgeResult)
	}
	return nil
}

func (e *WorkflowEngine) stepScoreSignals(_ context.Context, state *workflowState) error {
	if len(state.riskSignals) == 0 {
		state.risk.RiskScore = 0
		state.risk.RiskLevel = "low"
		state.risk.Summary = "joint risk low: no significant co-occurring low-severity signals"
		state.risk.ActionableWhy = "insufficient active signals in the selected window"
		return nil
	}
	total := 0.0
	active := 0
	for _, signal := range state.riskSignals {
		total += signal.Score
		if signal.Triggered {
			active++
		}
	}
	if active >= 2 {
		total += 0.04 * float64(active-1)
	}
	if len(state.cooccurrences) > 0 {
		total += 0.06 * float64(len(state.cooccurrences))
	}
	if score := strongestChangeScore(state.changeLinks); score > 0 {
		total += 0.08 * score
	}
	if hasBehavioralAnomaly(state.riskSignals) && hasResourceAnomaly(state.riskSignals) {
		total += 0.09
	}
	total = clamp01(total)

	level := "low"
	if total >= e.cfg.HighRiskScoreThreshold {
		level = "high"
	} else if total >= e.cfg.MediumRiskThreshold {
		level = "medium"
	}

	summary := fmt.Sprintf("joint risk %s (score %.2f) across %d active signals", level, total, active)
	actionable := "no strong co-occurrence detected"
	if len(state.cooccurrences) > 0 {
		top := state.cooccurrences[0]
		actionable = fmt.Sprintf("combined risk is high because signals %s co-occurred within window %s on scope %s/%s",
			strings.Join(top.Signals, "+"), top.Window, top.Scope, top.Entity)
	}
	if actionable == "no strong co-occurrence detected" && len(state.changeLinks) > 0 {
		top := state.changeLinks[0]
		actionable = fmt.Sprintf("risk is elevated because recent %s change %q overlaps the incident window with score %.2f",
			top.Category, top.Summary, top.CorrelationScore)
	}
	if len(state.cooccurrences) == 0 && active >= 3 {
		names := make([]string, 0, 3)
		for _, signal := range state.riskSignals {
			if !signal.Triggered {
				continue
			}
			names = append(names, signal.Name)
			if len(names) >= 3 {
				break
			}
		}
		actionable = fmt.Sprintf("combined risk is elevated because signals %s co-occurred within %s on scope node/%s",
			strings.Join(names, "+"), state.window.String(), firstNonEmpty(state.collectorID, "fleet"))
	}

	state.risk.RiskScore = total
	state.risk.RiskLevel = level
	state.risk.Summary = summary
	state.risk.ActionableWhy = actionable
	return nil
}

func (e *WorkflowEngine) stepIncidentSynthesis(_ context.Context, state *workflowState) error {
	state.incident = SynthesizeIncident(state)
	if strings.TrimSpace(state.incident.Summary) == "" {
		state.incident.Summary = "incident synthesis did not find a stable signal cluster"
	}
	if strings.TrimSpace(state.incident.CandidateRootCauseCluster) == "" && len(state.changeLinks) > 0 {
		state.incident.CandidateRootCauseCluster = firstNonEmpty(state.changeLinks[0].HypothesisHint, state.changeLinks[0].Summary)
	}
	if state.incident.Confidence <= 0 {
		state.incident.Confidence = clamp01(state.risk.RiskScore)
	}
	return nil
}

func (e *WorkflowEngine) stepCorrelateSignals(_ context.Context, state *workflowState) error {
	if len(state.cooccurrences) == 0 {
		state.cooccurrences = buildFallbackCooccurrence(state.riskSignals, state.window, state.collectorID)
	}
	return nil
}

func (e *WorkflowEngine) stepJointRiskRecommendations(ctx context.Context, state *workflowState) error {
	recs := make([]WorkflowRecommendation, 0, 8)
	if trend, ok := topTriggeredTrend(state.trendAssessments); ok {
		details := strings.TrimSpace(strings.Join([]string{trend.Summary, trend.Forecast}, " "))
		recs = append(recs, recommendationFromFields(
			"risk-trend-1",
			"immediate_investigation",
			priorityForSeverity(trend.Severity),
			fmt.Sprintf("Validate deteriorating trend: %s on %s", trend.Display, firstNonEmpty(trend.Entity, state.collectorID, "fleet")),
			truncateString(details, 240),
			fmt.Sprintf("%s/%s", firstNonEmpty(trend.Scope, "node"), firstNonEmpty(trend.Entity, state.collectorID, "fleet")),
			checksForTrendAssessment(trend),
			true, true, false, true,
			"read-only trend validation",
			"",
			"single-variable deterioration is an early warning path that can surface trouble before a hard threshold breach",
			"narrows the next investigation step before the trend becomes an incident",
			priorityForSeverity(trend.Severity),
			trend.Confidence,
			[]string{trend.ID},
		))
	}
	topSignals := topTriggeredSignals(state.riskSignals, 4)
	for i, signal := range topSignals {
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("risk-check-%d", i+1),
			"immediate_investigation",
			priorityForSeverity(signal.Severity),
			fmt.Sprintf("Inspect %s pressure on %s", signal.Name, signal.Entity),
			strings.Join(signal.Evidence, " "),
			fmt.Sprintf("%s/%s", signal.Scope, signal.Entity),
			[]string{"validate current metric against baseline", "confirm pressure source from top process list"},
			true, true, false, true,
			"read-only diagnostic check",
			"",
			"joint-risk scoring flagged this signal as a top contributor",
			"reduces uncertainty around the strongest weak signal before escalation",
			priorityForSeverity(signal.Severity),
			clamp01(signal.Score),
			[]string{fmt.Sprintf("ev-%s", sanitizeID(signal.ID))},
		))
	}
	if cluster, ok := topWeakSignalCluster(state.cooccurrences); ok {
		recs = append(recs, recommendationFromFields(
			"risk-cluster-1",
			"immediate_investigation",
			priorityForConfidence(cluster.CombinedScore),
			fmt.Sprintf("Validate correlated weak-signal cluster on %s", firstNonEmpty(cluster.Entity, state.collectorID, "fleet")),
			truncateString(firstNonEmpty(cluster.ActionableCause, cluster.Explanation), 240),
			fmt.Sprintf("%s/%s", firstNonEmpty(cluster.Scope, "node"), firstNonEmpty(cluster.Entity, state.collectorID, "fleet")),
			checksForSignalCluster(cluster),
			true, true, false, true,
			"read-only correlation validation",
			"",
			"multiple moderate signals together can predict degradation before any single metric reaches a catastrophic threshold",
			"reduces the chance of missing a latent multi-signal failure mode",
			priorityForConfidence(cluster.CombinedScore),
			clamp01(cluster.CombinedScore),
			[]string{cluster.ID},
		))
	}

	if state.risk.RiskLevel == "high" || state.risk.RiskLevel == "medium" {
		profileResult, err := state.callTool(ctx, "recommendation_generation", ToolProfiling, map[string]string{
			"reason": state.risk.ActionableWhy,
		})
		if err == nil {
			if profile, ok := profileResult.Data.(profilingToolData); ok {
				state.profiling = profile
				recs = append(recs, recommendationFromFields(
					"risk-profile-1",
					"immediate_investigation",
					"medium",
					"Trigger short profile capture for additional runtime evidence",
					fmt.Sprintf("Command: %s", profile.Command),
					firstNonEmpty(state.collectorID, "fleet"),
					[]string{"confirm low-overhead profile window", "capture profile during active pressure period"},
					false, true, e.cfg.RequireApproval, true,
					"stop profiling command and discard capture",
					"profiling changes runtime cost and must stay approval-gated",
					"high or medium joint-risk needs a runtime evidence expansion path",
					"improves confidence before containment or remediation",
					"medium",
					0.64,
					nil,
				))
			}
		}
	}

	if strings.TrimSpace(state.retrievalSummary) != "" {
		recs = append(recs, recommendationFromFields(
			"risk-knowledge-summary",
			"immediate_investigation",
			"medium",
			"Use retrieved historical knowledge to scope next checks",
			state.retrievalSummary,
			"knowledge_base",
			[]string{"compare current co-occurring signals with retrieved runbooks or historical cases", "cite retrieved evidence IDs in operator notes"},
			true, true, false, true,
			"read-only knowledge review",
			"",
			"historical or runbook evidence can narrow the next operator action",
			"reduces repeated investigation work",
			"low",
			state.retrievalConfidence,
			state.retrievalEvidenceIDs,
		))
	}
	for i, hit := range state.similarPatterns {
		if i >= 2 {
			break
		}
		checks := appendKnowledgeChecks(
			[]string{
				"validate whether the retrieved pattern matches the active co-occurring signals",
				"capture any repeated escalation markers before execution",
			},
			hit,
			1,
			1,
		)
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("risk-pattern-%d", i+1),
			"immediate_investigation",
			"medium",
			fmt.Sprintf("Review similar weak-signal pattern: %s", firstNonEmpty(hit.Title, hit.SourcePath)),
			fmt.Sprintf("%s [%s] %s", hit.SourcePath, hit.EvidenceID, truncateString(firstNonEmpty(hit.Summary, hit.Snippet), 220)),
			"knowledge_base",
			checks,
			true, true, false, true,
			"read-only knowledge review",
			"",
			"similar incident patterns can explain weak signals before they become a noisy outage",
			"improves recommendation quality and operator confidence",
			"low",
			hit.Score,
			[]string{hit.EvidenceID},
		))
	}
	for i, hit := range state.retrievedRunbooks {
		if i >= 1 {
			break
		}
		checks := appendKnowledgeChecks(
			[]string{"capture runbook prerequisites", "reuse the same checks if this weak-signal combination escalates"},
			hit,
			2,
			2,
		)
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("risk-runbook-%d", i+1),
			"structural_prevention",
			"medium",
			fmt.Sprintf("Stage the matching runbook before escalation: %s", firstNonEmpty(hit.Title, hit.SourcePath)),
			fmt.Sprintf("%s", truncateString(firstNonEmpty(strings.Join(hit.RemediationSteps, " · "), hit.Snippet), 220)),
			"knowledge_base",
			checks,
			true, true, false, true,
			"read-only runbook preparation",
			"",
			"pre-staging the right runbook reduces operator scramble if the risk becomes an incident",
			"reduces MTTR if the current weak-signal cluster turns into a real outage",
			"low",
			hit.Score,
			[]string{hit.EvidenceID},
		))
	}

	state.recommendation = dedupeRecommendations(recs)
	if len(state.recommendation) > 10 {
		state.recommendation = state.recommendation[:10]
	}

	for _, rec := range state.recommendation {
		e.audit(state.workflowID, state.workflowType, "recommendation_generation", "recommendation.generated", "success", state.collectorID, state.dryRun, rec.RequiresApproval, true,
			map[string]string{"recommendation_id": rec.ID, "priority": rec.Priority},
			rec.Summary,
			nil,
		)
	}
	return nil
}

func (e *WorkflowEngine) stepFinalizeJointRisk(ctx context.Context, state *workflowState) error {
	mergeLLMIntoState(state)
	insights := e.insightsStatus()

	var contribSignals []ContributingSignal
	for _, sig := range state.riskSignals {
		if !sig.Triggered {
			continue
		}
		contribSignals = append(contribSignals, ContributingSignal{
			SignalID:   sig.ID,
			SignalType: sig.Scope,
			Value:      sig.Current,
			Weight:     sig.Weight,
			Source:     sig.Entity,
		})
	}

	var impactedScope []string
	for _, sr := range state.scopeRisks {
		impactedScope = append(impactedScope, fmt.Sprintf("%s/%s", sr.Scope, sr.Entity))
	}

	confidence := clamp01(state.risk.RiskScore * 0.8)
	if len(state.cooccurrences) > 0 {
		confidence = clamp01(confidence + 0.1)
	}
	if len(contribSignals) >= 3 {
		confidence = clamp01(confidence + 0.05)
	}
	confidence = applyTelemetryConfidenceLimit(confidence, state.telemetryQuality)

	var recToolCalls []string
	switch state.risk.RiskLevel {
	case "high":
		recToolCalls = []string{"profiling_trigger", "process_lineage", "trace_query"}
	case "medium":
		recToolCalls = []string{"metrics_query", "logs_query"}
	}

	for _, d := range state.baselineDrifts {
		contribSignals = append(contribSignals, ContributingSignal{
			SignalID:   fmt.Sprintf("baseline_drift_%s_%s", d.Dimension, d.Metric),
			SignalType: "baseline_drift",
			Value:      d.Current,
			Weight:     0.15,
			Source:     d.CollectorID,
		})
	}

	if e.processTree != nil {
		for _, a := range e.processTree.DetectAbnormalLineage() {
			contribSignals = append(contribSignals, ContributingSignal{
				SignalID:   fmt.Sprintf("lineage_anomaly_pid_%d", a.PID),
				SignalType: "process_lineage",
				Value:      1.0,
				Weight:     0.2,
				Source:     a.Binary,
			})
		}
	}

	state.risk = JointRiskAssessment{
		WorkflowID:              state.workflowID,
		PipelineVersion:         workflowPipelineVersion,
		CollectorID:             state.collectorID,
		Scope:                   "node",
		Window:                  state.window.String(),
		GeneratedAt:             state.now,
		RiskScore:               state.risk.RiskScore,
		RiskLevel:               state.risk.RiskLevel,
		Summary:                 state.risk.Summary,
		ActionableWhy:           state.risk.ActionableWhy,
		Signals:                 topRiskSignals(state.riskSignals, e.cfg.MaxSignals),
		TrendAssessments:        append([]TrendAssessment(nil), state.trendAssessments...),
		InvestigationEvents:     append([]InvestigationEvent(nil), state.investigationEvents...),
		Cooccurrences:           state.cooccurrences,
		ScopeRisks:              state.scopeRisks,
		Series:                  state.riskSeries,
		Recommendations:         state.recommendation,
		Stages:                  append([]PipelineStageResult(nil), state.stages...),
		ToolCalls:               append([]WorkflowToolCall(nil), state.toolCalls...),
		Limitations:             workflowLimitations(state),
		Insights:                insights,
		LLMAnalysis:             state.llmAnalysis,
		ContributingSignals:     contribSignals,
		CorrelatedTimeWindow:    &TimeWindow{Start: state.now.Add(-state.window), End: state.now},
		ImpactedScope:           impactedScope,
		Confidence:              confidence,
		RecommendedToolCalls:    recToolCalls,
		Severity:                state.risk.RiskLevel,
		TelemetryQuality:        state.telemetryQuality,
		RetrievedDocs:           append([]RetrievedDocumentEvidence(nil), state.retrievedDocs...),
		RetrievedCases:          append([]RetrievedDocumentEvidence(nil), state.retrievedCases...),
		RetrievedRunbooks:       append([]RetrievedDocumentEvidence(nil), state.retrievedRunbooks...),
		SimilarIncidentPatterns: append([]RetrievedDocumentEvidence(nil), state.similarPatterns...),
		RetrievalSummary:        state.retrievalSummary,
		RetrievalEvidenceIDs:    append([]string(nil), state.retrievalEvidenceIDs...),
		RetrievalConfidence:     state.retrievalConfidence,
		RetrievalDecisions:      append([]RetrievalDecision(nil), state.retrievalDecisions...),
		ChangeLinks:             append([]RCAChangeLink(nil), state.changeLinks...),
		BehavioralAssessments:   append([]BehavioralSignalAssessment(nil), state.behavioralAssessments...),
		AdaptiveBaselines:       append([]AdaptiveBaselineInsight(nil), state.adaptiveBaselines...),
		IncidentMemoryMatches:   append([]RetrievedDocumentEvidence(nil), state.incidentMemoryMatches...),
	}
	if state.risk.ActionableWhy == "" {
		state.risk.ActionableWhy = "no actionable multi-signal co-occurrence found"
	}

	// Record trace
	if e.traceStore != nil {
		normalizedEvidence := buildNormalizedWorkflowEvidence(state)
		actions := []*ProposedAction(nil)
		if e.proposedActions != nil && len(state.recommendation) > 0 {
			actions = GenerateProposedActions(state.workflowID, state.collectorID, state.recommendation, state.risk.RiskScore)
		}
		var reasoningReview *LLMReasoningReview
		if state.llmAnalysis != nil {
			reasoningReview = state.llmAnalysis.Review
		}
		trace := &AgentTrace{
			TraceID:               state.workflowID,
			WorkflowType:          "joint_risk",
			CollectorID:           state.collectorID,
			EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
			StartedAt:             state.now,
			CompletedAt:           time.Now().UTC(),
			Status:                "completed",
			ToolCalls:             append([]WorkflowToolCall(nil), state.toolCalls...),
			Recommendations:       append([]WorkflowRecommendation(nil), state.recommendation...),
			ReasoningReview:       reasoningReview,
			ProposedActions:       derefProposedActions(actions),
			NormalizedEvidence:    evidencev1.CloneRecords(normalizedEvidence),
			Stages:                append([]PipelineStageResult(nil), state.stages...),
			FinalRiskScore:        state.risk.RiskScore,
			Summary:               state.risk.Summary,
			RiskTimeline: []RiskTimelineEntry{
				{Timestamp: state.now, RiskScore: state.risk.RiskScore, RiskLevel: state.risk.RiskLevel, Source: "joint_risk"},
			},
		}
		e.traceStore.RecordTrace(trace)
	}

	e.persistJointRiskArtifacts(ctx, state)

	// Generate proposed actions from recommendations
	if e.proposedActions != nil && len(state.recommendation) > 0 {
		actions := GenerateProposedActions(state.workflowID, state.collectorID, state.recommendation, state.risk.RiskScore)
		for _, a := range actions {
			e.proposedActions.RecordAction(a)
		}
	}

	return nil
}

func (e *WorkflowEngine) stepGatherRCAContext(ctx context.Context, state *workflowState) error {
	for _, tool := range []ToolName{ToolHistoricalIncident, ToolRunbookRetrieval} {
		query, decision := buildKnowledgeQuery(state, "context_gathering", tool)
		state.retrievalDecisions = append(state.retrievalDecisions, decision)
		if decision.Skipped {
			continue
		}
		knowledgeResult, err := state.callTool(ctx, "context_gathering", tool, query)
		if err != nil {
			state.limitations = append(state.limitations, fmt.Sprintf("%s unavailable during RCA context gathering", tool))
			continue
		}
		state.applyKnowledgeResult(knowledgeResult)
	}

	metrics := map[string]float64{}
	if state.metricsData.Node != nil {
		metrics = cloneMetricMap(state.metricsData.Node.Metrics)
	}
	topMetrics := topMetricMap(metrics, 12)
	topProcesses := make([]string, 0, 6)
	if state.metricsData.Node != nil {
		for _, process := range topProcessResources(state.metricsData.Node, 6) {
			topProcesses = append(topProcesses, processDisplayName(process))
		}
	}
	if len(state.gpu.TopProcesses) > 0 {
		topProcesses = append(topProcesses, state.gpu.TopProcesses...)
		topProcesses = dedupeStrings(topProcesses)
	}

	kernelSignals := make([]string, 0, 8)
	for _, signal := range state.riskSignals {
		if !signal.Triggered {
			continue
		}
		if strings.Contains(strings.ToLower(signal.Name), "io") || strings.Contains(strings.ToLower(signal.Name), "retransmit") || strings.Contains(strings.ToLower(signal.Name), "cpu") {
			kernelSignals = append(kernelSignals, signal.Name)
		}
	}
	for _, summary := range state.ebpf.RuntimeEventSummaries {
		kernelSignals = append(kernelSignals, summary)
		if len(kernelSignals) >= 12 {
			break
		}
	}
	kernelSignals = dedupeStrings(kernelSignals)
	traceSummary := append([]string{}, state.ebpf.RuntimeEventSummaries...)
	if len(traceSummary) > 10 {
		traceSummary = traceSummary[:10]
	}

	topoSummary := strings.TrimSpace(state.topoData.Snapshot.Summary)
	if topoSummary == "" {
		topoSummary = "topology unavailable"
	}

	state.rca.Context = RCAContext{
		CollectorID:             state.collectorID,
		Window:                  state.window.String(),
		IncidentSummary:         state.incident.Summary,
		ImpactedScope:           append([]string{}, state.incident.ImpactedScope...),
		TopMetrics:              topMetrics,
		TrendAssessments:        append([]TrendAssessment(nil), state.trendAssessments...),
		InvestigationEvents:     append([]InvestigationEvent(nil), state.investigationEvents...),
		GPUSummary:              cloneMetricMap(state.gpu.Metrics),
		TopProcesses:            topProcesses,
		KernelSignals:           kernelSignals,
		TraceSummary:            traceSummary,
		RecentDeploys:           state.logsData.RecentDeploys,
		SecurityFindings:        dedupeStrings(append([]string{}, state.security.Findings...)),
		RecentChanges:           append([]RCAChangeLink(nil), state.changeLinks...),
		TopologySummary:         topoSummary,
		BehavioralAssessments:   append([]BehavioralSignalAssessment(nil), state.behavioralAssessments...),
		AdaptiveBaselines:       append([]AdaptiveBaselineInsight(nil), state.adaptiveBaselines...),
		TelemetryQuality:        state.telemetryQuality,
		RetrievalSummary:        state.retrievalSummary,
		RetrievalDecisions:      append([]RetrievalDecision(nil), state.retrievalDecisions...),
		SceneClassification:     state.sceneClassification,
		CollectionPlanSummary:   summarizeCollectionPlan(state.collectionPlan),
		RecollectionRound:       len(state.recollectionResults),
		RemainingBudget:         state.remainingBudget,
		EvidenceGoalsStillUnmet: append([]string(nil), state.evidenceGapState.EvidenceGoalsStillUnmet...),
	}

	anomalies := make([]string, 0, len(state.riskSignals))
	for _, signal := range state.riskSignals {
		if signal.Triggered {
			anomalies = append(anomalies, fmt.Sprintf("%s on %s (%.1f%% delta)", signal.Name, signal.Entity, signal.DeltaPercent))
		}
	}
	for _, event := range state.investigationEvents {
		anomalies = append(anomalies, firstNonEmpty(event.Title, event.Summary))
		if len(anomalies) >= 10 {
			break
		}
	}
	if len(anomalies) == 0 {
		anomalies = append(anomalies, "no strong anomalies detected from weighted risk model")
	}
	state.rca.Anomalies = dedupeStrings(anomalies)
	return nil
}

func (e *WorkflowEngine) stepPlanActVerifyLoop(ctx context.Context, state *workflowState) error {
	ensureInitialHypotheses(state)
	state.planSteps = buildInitialPlanSteps(state)
	if len(state.planSteps) == 0 {
		state.planCompleted = true
		state.planStopReason = "no plan steps generated"
		return nil
	}
	state.planRevisions = append(state.planRevisions, AgentPlanRevision{
		Iteration: state.planIterations,
		Reason:    "initial investigation plan",
		CreatedAt: time.Now().UTC(),
		Steps:     append([]AgentPlanStep(nil), state.planSteps...),
	})
	if state.durableRun != nil {
		_ = e.orchestrator.RecordPlanRevision(ctx, state.workflowID, state.planRevisions[len(state.planRevisions)-1])
		for _, planned := range state.planSteps {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "plan_act_verify_loop", planned)
		}
	}

	maxStepExec := maxInt(1, e.cfg.MaxPlanIterations*e.cfg.MaxPlanSteps)
	for idx := 0; idx < len(state.planSteps); idx++ {
		if state.stepsExecuted >= maxStepExec {
			state.planStopReason = "execution budget reached"
			break
		}

		step := &state.planSteps[idx]
		if step.Status == "completed" || step.Status == "failed" || step.Status == "skipped" {
			continue
		}
		step.Status = "running"
		step.StartedAt = time.Now().UTC()
		if state.durableRun != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "plan_act_verify_loop", *step)
		}
		result, err := state.callTool(ctx, "plan_act_verify_loop", step.Tool, step.Query, step.ID)
		step.CompletedAt = time.Now().UTC()
		step.ToolVersion = latestToolVersion(state.toolCalls, step.Tool)
		var lastCall WorkflowToolCall
		if len(state.toolCalls) > 0 {
			lastCall = state.toolCalls[len(state.toolCalls)-1]
		}
		if step.Tool == ToolRemediation {
			toolStatus := "unknown"
			if res, ok := result.Data.(ActionResult); ok {
				toolStatus = res.Status
			} else if data, ok := result.Data.(remediationToolData); ok {
				toolStatus = data.Mode
			}

			if toolStatus == ActionResultExecuted {
				state.stepsExecuted++
				step.Status = "executed"
				step.ResultSummary = result.Summary
				e.telemetry.recordRemediation(true, false) // intermediate status

				if res, ok := result.Data.(ActionResult); ok {
					step.OriginalAction = &ActionSpec{
						ID:              res.ID,
						Type:            res.Type,
						Command:         step.Query["action"],
						Namespace:       step.Query["scope"],
						RollbackCommand: step.Query["rollback"],
					}
				}
			} else {
				// If remediation tool call itself failed or was skipped, record it as such
				e.telemetry.recordRemediation(false, false)
			}
		} else {
			state.stepsExecuted++
		}

		if err != nil {
			if lastCall.Policy.RequiresApproval && lastCall.ApprovalState == "pending" {
				step.Status = "approval_required"
				step.Verified = false
				step.ResultSummary = truncateString(lastCall.ErrorMessage, 220)
				step.VerificationNote = "execution paused awaiting approval"
				if state.durableRun != nil {
					_ = e.orchestrator.AttachApproval(ctx, state.workflowID, step.ID, DurableApprovalRecord{
						State:       "pending",
						Reason:      lastCall.Policy.Reason,
						RequestedAt: step.CompletedAt,
					})
					_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "plan_act_verify_loop", *step)
					_ = e.orchestrator.SuspendRun(ctx, state.workflowID, lastCall.Policy.Reason)
				}
				if e.telemetry != nil {
					e.telemetry.recordApprovalPending()
				}
				state.planStopReason = "awaiting approval"
				break
			}
			step.Status = "failed"
			step.Verified = false
			step.ResultSummary = truncateString(err.Error(), 220)
			step.VerificationNote = "tool execution failed"
			if state.durableRun != nil {
				_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "plan_act_verify_loop", *step)
			}
			e.audit(
				state.workflowID,
				state.workflowType,
				"plan_act_verify_loop",
				"plan.step_failed",
				"failed",
				state.collectorID,
				state.dryRun,
				e.cfg.RequireApproval,
				false,
				map[string]string{"step_id": step.ID, "tool": string(step.Tool)},
				step.ResultSummary,
				err,
			)
			e.tryPlanRevision(state, step, "tool failure")
			continue
		}

		state.applyToolResult(step.Tool, result)
		updateHypothesesFromToolResult(state, *step, result)

		// Verification Phase
		if step.Tool == ToolRemediation && !state.dryRun {
			e.logger.Info("waiting for verification window", zap.String("run_id", state.workflowID), zap.Duration("window", e.cfg.VerificationWindow))
			verifyStarted := time.Now().UTC()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.cfg.VerificationWindow):
			}

			// Re-evaluate risk to verify remediation
			verification := e.verifyRemediationEffect(ctx, state)
			state.postActionEffect = &verification
			if verification.Verdict == ValidationVerdictConfirmed {
				step.Verified = true
				step.Status = "verified"
				step.VerificationNote = verification.Summary
				if state.durableRun != nil {
					_ = e.orchestrator.RecordVerification(ctx, state.workflowID, step.ID, DurableVerificationRecord{
						Outcome:      "resolved",
						Success:      true,
						Objective:    "verify remediation impact",
						Note:         step.VerificationNote,
						Window:       e.cfg.VerificationWindow.String(),
						PreviousRisk: verification.BeforeRisk,
						CurrentRisk:  verification.AfterRisk,
						EvidenceIDs:  dedupeStrings(append(append([]string(nil), step.EvidenceIDs...), append(verification.SupportingEvidenceIDs, verification.ContradictingEvidenceIDs...)...)),
						StartedAt:    verifyStarted,
						CompletedAt:  time.Now().UTC(),
					})
				}
				e.recordSuccessfulRemediation(ctx, state, step)
				e.telemetry.recordRemediation(true, true)
				if e.telemetry != nil {
					e.telemetry.recordVerification(true)
				}
			} else {
				step.Status = "verification_failed"
				step.VerificationNote = verification.Summary
				if state.durableRun != nil {
					outcome := "unresolved"
					if verification.Verdict == ValidationVerdictContradicted {
						outcome = "contradicted"
					} else if verification.Verdict == ValidationVerdictPartiallySupported {
						outcome = "partial"
					}
					_ = e.orchestrator.RecordVerification(ctx, state.workflowID, step.ID, DurableVerificationRecord{
						Outcome:      outcome,
						Success:      false,
						Objective:    "verify remediation impact",
						Note:         step.VerificationNote,
						Window:       e.cfg.VerificationWindow.String(),
						PreviousRisk: verification.BeforeRisk,
						CurrentRisk:  verification.AfterRisk,
						EvidenceIDs:  dedupeStrings(append(append([]string(nil), step.EvidenceIDs...), append(verification.SupportingEvidenceIDs, verification.ContradictingEvidenceIDs...)...)),
						StartedAt:    verifyStarted,
						CompletedAt:  time.Now().UTC(),
					})
				}
				if e.telemetry != nil {
					e.telemetry.recordVerification(false)
				}

				// Optional: Trigger compensation if unsafe/failed
				if e.cfg.AutoRollbackOnVerificationFailure {
					e.stepCompensate(ctx, state, step)
				}
			}
		} else {
			verified, note, evidenceIDs := verifyPlanStep(*step, result, state)
			step.Verified = verified
			step.VerificationNote = note
			step.EvidenceIDs = append(step.EvidenceIDs, evidenceIDs...)
			if state.durableRun != nil {
				_ = e.orchestrator.RecordVerification(ctx, state.workflowID, step.ID, DurableVerificationRecord{
					Outcome:     map[bool]string{true: "verified", false: "inconclusive"}[verified],
					Success:     verified,
					Objective:   fmt.Sprintf("verify %s result", step.Tool),
					Note:        note,
					EvidenceIDs: append([]string(nil), evidenceIDs...),
					StartedAt:   step.StartedAt,
					CompletedAt: time.Now().UTC(),
				})
			}
			if e.telemetry != nil {
				e.telemetry.recordVerification(verified)
			}
			if verified {
				step.Status = "verified"
			} else {
				step.Status = "completed" // for non-remediation steps, failure to verify might not mean step failed
			}
		}

		step.ResultSummary = truncateString(result.Summary, 220)

		if step.Verified {
			state.stepsVerified++
		} else if step.Required {
			e.tryPlanRevision(state, step, step.VerificationNote)
		}
		if state.durableRun != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "plan_act_verify_loop", *step)
		}

		stepStatus := "success"
		if !step.Verified {
			stepStatus = "partial"
		}
		e.audit(
			state.workflowID,
			state.workflowType,
			"plan_act_verify_loop",
			"plan.step_verified",
			stepStatus,
			state.collectorID,
			state.dryRun,
			e.cfg.RequireApproval,
			step.Verified,
			map[string]string{"step_id": step.ID, "tool": string(step.Tool)},
			truncateString(step.VerificationNote, 220),
			nil,
		)

		if stop, reason := shouldStopPlanLoop(state); stop {
			state.planCompleted = len(state.hypotheses) > 0 && state.hypotheses[0].Confidence >= 0.82
			state.planStopReason = reason
			break
		}
	}

	requiredPlanned, requiredVerified := summarizeRequiredPlanSteps(state.planSteps)
	state.planCompleted = requiredPlanned == 0 || requiredVerified == requiredPlanned
	if state.planStopReason == "" {
		if state.planCompleted {
			state.planStopReason = "required evidence verified"
		} else if state.stepsExecuted == 0 {
			state.planStopReason = "no plan steps executed"
		} else {
			state.planStopReason = "plan executed with unresolved verification gaps"
		}
	}
	return nil
}

func buildInitialPlanSteps(state *workflowState) []AgentPlanStep {
	if state == nil {
		return nil
	}
	needs := derivePlanNeeds(state)
	steps := make([]AgentPlanStep, 0, 8)
	addStep := func(id, title, objective string, tool ToolName, query map[string]string, required bool) {
		steps = append(steps, AgentPlanStep{
			ID:        id,
			Order:     len(steps) + 1,
			Iteration: 1,
			Title:     title,
			Objective: objective,
			Tool:      tool,
			Query:     query,
			Required:  required,
			Status:    "pending",
		})
	}

	addStep(
		"plan-metrics",
		"Collect metrics evidence",
		"Validate system/process/kernel pressure deltas against baseline.",
		ToolMetrics,
		map[string]string{"include": "system,process,kernel_ebpf"},
		true,
	)
	if needs.logs {
		addStep(
			"plan-logs",
			"Correlate logs with anomaly window",
			"Confirm error/warn bursts and deployment correlation.",
			ToolLogs,
			map[string]string{"query": "error warn timeout deploy restart oom permission"},
			true,
		)
	}
	if needs.changes {
		addStep(
			"plan-change-intel",
			"Correlate recent operational changes",
			"Check whether deployments, config changes, driver updates, or feature flags align with the incident window and scope.",
			ToolChangeQuery,
			map[string]string{
				"query":            firstNonEmpty(strings.Join(changeSummaries(state.changeLinks), " "), state.incident.Summary, "deploy rollout config driver feature flag"),
				"incident_summary": firstNonEmpty(state.incident.Summary, state.trigger),
				"scope":            firstNonEmpty(state.collectorID, "fleet"),
			},
			true,
		)
	}
	if needs.knowledge {
		similarQuery, similarDecision := buildKnowledgeQuery(state, "plan_act_verify_loop", ToolSimilarCase)
		runbookQuery, runbookDecision := buildKnowledgeQuery(state, "plan_act_verify_loop", ToolRunbookRetrieval)
		state.retrievalDecisions = append(state.retrievalDecisions, similarDecision, runbookDecision)
		if !similarDecision.Skipped {
			addStep(
				"plan-similar-case",
				"Retrieve similar incident patterns",
				"Pull historical cases and weak-signal analogies that match the current signal mix.",
				ToolSimilarCase,
				similarQuery,
				false,
			)
		}
		if !runbookDecision.Skipped {
			addStep(
				"plan-runbook",
				"Retrieve concrete runbooks",
				"Pull dataset-backed runbooks and remediation guidance that match the current evidence.",
				ToolRunbookRetrieval,
				runbookQuery,
				false,
			)
		}
	}
	if needs.topology {
		addStep(
			"plan-topology",
			"Scope topology impact",
			"Map node/pod/service blast radius in current window.",
			ToolTopology,
			nil,
			false,
		)
	}
	if needs.security {
		addStep(
			"plan-security",
			"Check security and misconfiguration signals",
			"Confirm or discard security-related contributors.",
			ToolSecurity,
			nil,
			true,
		)
	}
	if needs.ebpf {
		addStep(
			"plan-ebpf",
			"Query eBPF runtime behavior",
			"Collect syscall/process/network/file behavior anomalies from kernel-level telemetry.",
			ToolEBPFQuery,
			nil,
			true,
		)
	}
	if needs.gpu {
		addStep(
			"plan-gpu",
			"Inspect GPU pressure and attribution",
			"Validate whether device saturation or GPU process behavior contributes to the incident.",
			ToolGPU,
			nil,
			false,
		)
	}
	if needs.securityGraph {
		addStep(
			"plan-security-graph",
			"Build security evidence graph",
			"Map process-to-port/IP/path edges and detect suspicious graph links.",
			ToolSecurityGraph,
			nil,
			false,
		)
	}
	if needs.lineage {
		addStep(
			"plan-lineage",
			"Reconstruct process lineage",
			"Validate parent-child process chains and blast-radius lineage paths.",
			ToolProcessLineage,
			nil,
			false,
		)
	}
	if needs.profiling {
		addStep(
			"plan-profile",
			"Prepare bounded profile capture",
			"Collect extra runtime evidence when high-risk signals compound.",
			ToolProfiling,
			map[string]string{"reason": firstNonEmpty(state.risk.ActionableWhy, derivePlanPriority(state)+"-risk evidence expansion")},
			false,
		)
	}
	return steps
}

type planNeeds struct {
	logs          bool
	changes       bool
	knowledge     bool
	topology      bool
	security      bool
	ebpf          bool
	gpu           bool
	securityGraph bool
	lineage       bool
	profiling     bool
}

func derivePlanNeeds(state *workflowState) planNeeds {
	if state == nil {
		return planNeeds{}
	}
	priority := derivePlanPriority(state)
	hasLogs := len(state.logsData.Snippets) > 0 || len(state.logsData.RecentDeploys) > 0 || len(state.logsData.SecurityHints) > 0
	hasSecurityContext := len(state.security.Findings) > 0 || len(state.security.StructuredFindings) > 0 || len(state.logsData.SecurityHints) > 0
	hasRuntimeBehavior := len(state.ebpf.RuntimeEvents) > 0 || len(state.ebpf.RuntimeEventSummaries) > 0 || hasBehavioralAnomaly(state.riskSignals)
	multiScope := len(state.scopeRisks) > 1
	missingTopology := len(state.topoData.Snapshot.Nodes) == 0
	return planNeeds{
		logs:          expectsLogBurst(state) || hasLogs,
		changes:       len(state.changeLinks) > 0 || len(state.logsData.RecentDeploys) > 0 || strings.Contains(strings.ToLower(state.incident.Summary), "deploy") || strings.Contains(strings.ToLower(state.incident.CandidateRootCauseCluster), "driver"),
		knowledge:     len(state.riskSignals) > 0 || hasLogs || hasSecurityContext,
		topology:      multiScope || missingTopology || len(state.logsData.RecentDeploys) > 0,
		security:      hasSecurityContext || hasRuntimeBehavior,
		ebpf:          hasRuntimeBehavior || hasSecurityContext || hasBehavioralAnomaly(state.riskSignals),
		gpu:           len(state.gpu.Metrics) > 0 || strings.Contains(strings.ToLower(state.incident.CandidateRootCauseCluster), "gpu"),
		securityGraph: hasSecurityContext || len(state.ebpf.RuntimeEvents) > 0,
		lineage:       hasSecurityContext || len(state.ebpf.ProcessGraph.Edges) > 0 || len(state.lineage.Paths) > 0,
		profiling:     priority == "high",
	}
}

func derivePlanPriority(state *workflowState) string {
	if state == nil {
		return "low"
	}
	if strings.EqualFold(strings.TrimSpace(state.risk.RiskLevel), "high") || strings.EqualFold(strings.TrimSpace(state.risk.RiskLevel), "critical") {
		return "high"
	}
	if strings.EqualFold(strings.TrimSpace(state.risk.RiskLevel), "medium") {
		return "medium"
	}
	active := topTriggeredSignals(state.riskSignals, 6)
	highSignals := 0
	mediumSignals := 0
	for _, signal := range active {
		switch strings.ToLower(strings.TrimSpace(signal.Severity)) {
		case "critical", "high":
			highSignals++
		case "medium":
			mediumSignals++
		}
	}
	switch {
	case state.security.CriticalFindings > 0:
		return "high"
	case state.security.HighFindings > 0 && hasBehavioralAnomaly(state.riskSignals):
		return "high"
	case highSignals >= 2:
		return "high"
	case hasBehavioralAnomaly(state.riskSignals) && hasResourceAnomaly(state.riskSignals):
		return "high"
	case state.security.HighFindings > 0 || highSignals == 1 || mediumSignals >= 2 || len(state.ebpf.RuntimeEvents) > 0:
		return "medium"
	default:
		return "low"
	}
}

func verifyPlanStep(step AgentPlanStep, result workflowToolResult, state *workflowState) (bool, string, []string) {
	evidenceIDs := []string{fmt.Sprintf("ev-%s", sanitizeID(step.ID))}
	switch step.Tool {
	case ToolMetrics:
		data, ok := result.Data.(metricsToolData)
		if !ok {
			return false, "invalid metrics payload", evidenceIDs
		}
		if data.Node == nil || len(data.History) < 3 {
			return false, "insufficient metric history for verification", evidenceIDs
		}
		if len(state.riskSignals) > 0 && len(topTriggeredSignals(state.riskSignals, 3)) == 0 {
			return false, "metric anomalies did not support current hypotheses", evidenceIDs
		}
		return true, fmt.Sprintf("metrics verified with %d samples", len(data.History)), evidenceIDs
	case ToolLogs:
		data, ok := result.Data.(logsToolData)
		if !ok {
			return false, "invalid log payload", evidenceIDs
		}
		if expectsLogBurst(state) && data.Errors+data.Warnings == 0 && len(data.Snippets) == 0 {
			return false, "log burst hypothesis not supported by current logs", evidenceIDs
		}
		if data.Errors+data.Warnings == 0 && len(data.Snippets) == 0 {
			return false, "no matching logs in selected window", evidenceIDs
		}
		return true, fmt.Sprintf("logs verified errors=%d warnings=%d", data.Errors, data.Warnings), evidenceIDs
	case ToolChangeQuery:
		data, ok := result.Data.(changeToolData)
		if !ok {
			return false, "invalid change-intel payload", evidenceIDs
		}
		if len(data.Events) == 0 {
			return false, "no recent changes correlated with the incident window", evidenceIDs
		}
		return true, fmt.Sprintf("change intelligence verified matches=%d", len(data.Events)), evidenceIDs
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		data, ok := result.Data.(knowledgeToolData)
		if !ok {
			return false, "invalid knowledge payload", evidenceIDs
		}
		if len(data.Hits) == 0 {
			return false, fmt.Sprintf("%s returned no matching evidence", knowledgeToolLabel(step.Tool)), evidenceIDs
		}
		return true, fmt.Sprintf("%s hits=%d confidence=%.2f", knowledgeToolLabel(step.Tool), len(data.Hits), data.Confidence), append(evidenceIDs, data.EvidenceIDs...)
	case ToolTopology:
		data, ok := result.Data.(topologyToolData)
		if !ok {
			return false, "invalid topology payload", evidenceIDs
		}
		if len(data.Snapshot.Nodes) == 0 && strings.TrimSpace(data.Snapshot.Summary) == "" {
			return false, "topology data unavailable", evidenceIDs
		}
		return true, fmt.Sprintf("topology verified nodes=%d edges=%d", len(data.Snapshot.Nodes), len(data.Snapshot.Edges)), evidenceIDs
	case ToolSecurity:
		data, ok := result.Data.(securityToolData)
		if !ok {
			return false, "invalid security payload", evidenceIDs
		}
		if len(data.Findings) == 0 {
			return true, "security check completed with no major findings", evidenceIDs
		}
		return true, fmt.Sprintf("security findings=%d", len(data.Findings)), evidenceIDs
	case ToolEBPFQuery:
		data, ok := result.Data.(ebpfToolData)
		if !ok {
			return false, "invalid ebpf payload", evidenceIDs
		}
		if len(data.RuntimeEvents) == 0 && len(data.SyscallStatistics) == 0 {
			return false, "no ebpf runtime evidence in selected window", evidenceIDs
		}
		return true, fmt.Sprintf("ebpf events=%d syscalls=%d", len(data.RuntimeEvents), len(data.SyscallStatistics)), append(evidenceIDs, data.EvidenceIDs...)
	case ToolGPU:
		data, ok := result.Data.(gpuToolData)
		if !ok {
			return false, "invalid gpu payload", evidenceIDs
		}
		if len(data.Metrics) == 0 {
			return false, "gpu telemetry unavailable", evidenceIDs
		}
		return true, data.Summary, append(evidenceIDs, data.EvidenceIDs...)
	case ToolSecurityGraph:
		data, ok := result.Data.(securityGraphToolData)
		if !ok {
			return false, "invalid security graph payload", evidenceIDs
		}
		if len(data.Nodes) == 0 || len(data.Edges) == 0 {
			return false, "security graph lacks actionable edges", evidenceIDs
		}
		return true, fmt.Sprintf("security graph nodes=%d edges=%d", len(data.Nodes), len(data.Edges)), evidenceIDs
	case ToolProcessLineage:
		data, ok := result.Data.(processLineageToolData)
		if !ok {
			return false, "invalid process lineage payload", evidenceIDs
		}
		if len(data.Nodes) == 0 || len(data.Edges) == 0 {
			return false, "process lineage unavailable", evidenceIDs
		}
		return true, fmt.Sprintf("lineage nodes=%d edges=%d", len(data.Nodes), len(data.Edges)), evidenceIDs
	case ToolProfiling:
		data, ok := result.Data.(profilingToolData)
		if !ok {
			return false, "invalid profiling payload", evidenceIDs
		}
		if strings.Contains(strings.ToLower(data.Mode), "blocked") {
			return false, "profiling plan blocked by policy", evidenceIDs
		}
		return true, data.Message, evidenceIDs
	case ToolRemediation:
		data, ok := result.Data.(remediationToolData)
		if !ok {
			return false, "invalid remediation payload", evidenceIDs
		}
		return true, data.Summary, evidenceIDs
	default:
		return true, "step completed", evidenceIDs
	}
}

func (e *WorkflowEngine) verifyRemediationEffect(ctx context.Context, state *workflowState) PostActionValidationSummary {
	before := state.beforeActionState
	beforeRisk := maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses))
	if before != nil {
		beforeRisk = before.RiskScore
	}

	req := WorkflowRequest{
		CollectorID: state.collectorID,
		Window:      10 * time.Minute,
	}
	report, err := e.EvaluateJointRisk(ctx, req)
	after, snapErr := captureValidationEvidenceSnapshot(ctx, state, "after_action")
	resolved := false
	afterRisk := beforeRisk
	if err == nil {
		afterRisk = report.RiskScore
		resolved = report.RiskScore < e.cfg.MediumRiskThreshold
	}
	if after != nil {
		after.RiskScore = afterRisk
	}
	if before != nil && before.RiskScore == 0 {
		before.RiskScore = beforeRisk
	}

	input := validationEffectInput{
		ActionID:          "",
		ExecutionCategory: "",
		Before:            before,
		After:             after,
		BeforeRisk:        beforeRisk,
		AfterRisk:         afterRisk,
		Resolved:          resolved,
	}
	if state.selectedContract != nil {
		input.ActionID = state.selectedContract.ID
		input.ExecutionCategory = state.selectedContract.ExecutionCategory
	} else if state.selectedAction != nil {
		input.ActionID = state.selectedAction.ID
		input.ExecutionCategory = state.selectedAction.Category
	}
	switch {
	case err == nil && snapErr == nil:
		return summarizeValidationEffect(input)
	case err != nil && snapErr != nil:
		input.FallbackMode = "joint_risk_and_snapshot_unavailable"
		input.Note = fmt.Sprintf("post-action verification failed: joint risk=%v; snapshot=%v", err, snapErr)
		return summarizeValidationEffect(input)
	case err != nil:
		input.FallbackMode = "snapshot_only"
		input.Note = fmt.Sprintf("joint risk rerun failed; using snapshot-only comparison: %v", err)
		return summarizeValidationEffect(input)
	default:
		input.FallbackMode = "risk_only"
		input.Note = fmt.Sprintf("post-action snapshots unavailable; using risk-only comparison: %v", snapErr)
		return summarizeValidationEffect(input)
	}
}

func (e *WorkflowEngine) stepCompensate(ctx context.Context, state *workflowState, step *AgentPlanStep) {
	e.logger.Warn("triggering compensation for step", zap.String("step_id", step.ID), zap.String("run_id", state.workflowID))
	started := time.Now().UTC()
	beforeRollback, _ := captureValidationEvidenceSnapshot(ctx, state, "before_rollback")

	// Implement actual rollback if runner is available
	rollbackAction := cloneActionSpec(step.OriginalAction)
	if rollbackAction == nil && step.ActionContract != nil && strings.TrimSpace(step.ActionContract.Rollback.Command) != "" {
		rollbackAction = &ActionSpec{
			ID:               fmt.Sprintf("%s-rollback", step.ID),
			Type:             "shell",
			Command:          step.ActionContract.Rollback.Command,
			Namespace:        step.ActionContract.Target.Namespace,
			Name:             step.ActionContract.Target.Name,
			Safe:             false,
			RequiresApproval: true,
			Metadata: map[string]string{
				"execution_category": normalizeValidationCategory(firstNonEmpty(step.ActionContract.ExecutionCategory, "rollback")),
				"action_intent":      firstNonEmpty(step.ActionContract.Intent, "rollback_revision"),
			},
		}
	}
	if e.playbookRunner != nil && rollbackAction != nil && (rollbackAction.RollbackCommand != "" || rollbackAction.Command != "") {
		if rollbackAction.RollbackCommand == "" {
			rollbackAction.RollbackCommand = rollbackAction.Command
		}
		res := e.playbookRunner.ExecuteRollback(ctx, *rollbackAction, e.cfg.DryRun)
		if res.Status == ActionResultExecuted {
			e.logger.Info("compensation successful", zap.String("step_id", step.ID), zap.String("output", res.Output))
			afterRollback, _ := captureValidationEvidenceSnapshot(ctx, state, "after_rollback")
			if report, err := e.EvaluateJointRisk(ctx, WorkflowRequest{CollectorID: state.collectorID, Window: 10 * time.Minute}); err == nil && afterRollback != nil {
				afterRollback.RiskScore = report.RiskScore
			}
			rollbackSummary := summarizeValidationEffect(validationEffectInput{
				ActionID:          step.ID + "-rollback",
				ExecutionCategory: normalizeValidationCategory(firstNonEmpty(step.Query["validation_category"], "probable_containment")),
				Before:            beforeRollback,
				After:             afterRollback,
				BeforeRisk:        snapshotRisk(beforeRollback),
				AfterRisk:         snapshotRisk(afterRollback),
				Note:              "rollback verification summary",
			})
			if e.orchestrator != nil {
				e.orchestrator.LogEvent(ctx, state.workflowID, "compensation_successful", map[string]any{
					"step_id":             step.ID,
					"output":              res.Output,
					"execution_category":  normalizeValidationCategory(firstNonEmpty(step.Query["validation_category"], "probable_containment")),
					"rollback_validation": rollbackSummary.Summary,
				})
				_ = e.orchestrator.RecordCompensation(ctx, state.workflowID, step.ID, DurableCompensationRecord{
					Status:      "executed",
					Summary:     firstNonEmpty(rollbackSummary.Summary, res.Output),
					StartedAt:   started,
					CompletedAt: time.Now().UTC(),
				})
			}
			step.Query["rollback_status"] = "reverted"
			step.Query["rollback_summary"] = firstNonEmpty(rollbackSummary.Summary, res.Output)
			step.VerificationNote = firstNonEmpty(step.VerificationNote, rollbackSummary.Summary)
			step.Status = "reverted"
			updateValidationGovernanceRollback(&state.validationReport, "reverted")
		} else {
			e.logger.Error("compensation failed", zap.String("step_id", step.ID), zap.String("error", res.Error))
			if e.orchestrator != nil {
				e.orchestrator.LogEvent(ctx, state.workflowID, "compensation_failed", map[string]any{
					"step_id": step.ID,
					"error":   res.Error,
				})
				_ = e.orchestrator.RecordCompensation(ctx, state.workflowID, step.ID, DurableCompensationRecord{
					Status:      "failed",
					Error:       res.Error,
					StartedAt:   started,
					CompletedAt: time.Now().UTC(),
				})
			}
			step.Query["rollback_status"] = "rollback_failed"
			step.Query["rollback_summary"] = res.Error
			step.Status = "rollback_failed"
			updateValidationGovernanceRollback(&state.validationReport, "rollback_failed")
		}
	} else if e.orchestrator != nil {
		e.orchestrator.LogEvent(ctx, state.workflowID, "compensation_skipped", map[string]any{
			"step_id": step.ID,
			"reason":  "no_rollback_command",
		})
		_ = e.orchestrator.RecordCompensation(ctx, state.workflowID, step.ID, DurableCompensationRecord{
			Status:      "skipped",
			Error:       "no rollback command",
			StartedAt:   started,
			CompletedAt: time.Now().UTC(),
		})
		step.Query["rollback_status"] = "rollback_skipped"
		step.Query["rollback_summary"] = "no rollback command"
		updateValidationGovernanceRollback(&state.validationReport, "rollback_skipped")
	}
	e.emitCompensationResultMessage(ctx, state, step)
	e.telemetry.recordRollback()
}

func (e *WorkflowEngine) recordSuccessfulRemediation(ctx context.Context, state *workflowState, step *AgentPlanStep) {
	if e == nil || e.memoryStore == nil || state == nil || step == nil {
		return
	}
	actionName := firstNonEmpty(step.Query["action"], step.Title)
	actionCategory := strings.TrimSpace(firstNonEmpty(step.Query["action_intent"], step.Query["validation_category"]))
	executionCategory := normalizeValidationCategory(step.Query["validation_category"])
	validationCategory := normalizeValidationCategory(step.Query["validation_category"])
	actuatorSafetyTier := normalizeActuatorSafetyTier(step.Query["safety_tier"])
	targetScope := strings.TrimSpace(step.Query["scope"])
	actionIntent := strings.TrimSpace(step.Query["action_intent"])
	contractID := ""
	blastRadiusNotes := []string{}
	if step.ActionContract != nil {
		contractID = strings.TrimSpace(step.ActionContract.ID)
		actionName = firstNonEmpty(step.ActionContract.Summary, step.ActionContract.Intent, actionName)
		actionIntent = firstNonEmpty(step.ActionContract.Intent, actionIntent)
		actionCategory = strings.TrimSpace(firstNonEmpty(step.ActionContract.ActionCategory, step.ActionContract.Intent, actionCategory))
		executionCategory = normalizeValidationCategory(firstNonEmpty(step.ActionContract.ExecutionCategory, executionCategory))
		validationCategory = normalizeValidationCategory(firstNonEmpty(step.ActionContract.ValidationCategory, validationCategory, executionCategory))
		actuatorSafetyTier = normalizeActuatorSafetyTier(firstNonEmpty(step.ActionContract.ActuatorSafetyTier, actuatorSafetyTier))
		targetScope = strings.TrimSpace(firstNonEmpty(step.ActionContract.TargetScope, step.ActionContract.Target.Scope, targetScope))
		blastRadiusNotes = append([]string(nil), step.ActionContract.BlastRadiusNotes...)
	}
	effectSummary := ""
	beforeRisk := 0.0
	afterRisk := 0.0
	verified := step.Verified
	postActionVerdict := ""
	effectComparable := false
	effectIncomplete := false
	effectMissingData := []string{}
	if state.postActionEffect != nil {
		effectSummary = state.postActionEffect.Summary
		beforeRisk = state.postActionEffect.BeforeRisk
		afterRisk = state.postActionEffect.AfterRisk
		verified = state.postActionEffect.Verdict == ValidationVerdictConfirmed
		postActionVerdict = string(state.postActionEffect.Verdict)
		if state.postActionEffect.Comparison != nil {
			effectComparable = state.postActionEffect.Comparison.Comparable
			effectIncomplete = state.postActionEffect.Comparison.Incomplete
			effectMissingData = append([]string(nil), state.postActionEffect.Comparison.MissingData...)
		}
	}
	metadata := map[string]string{
		"execution_category":  executionCategory,
		"validation_category": validationCategory,
		"target_scope":        targetScope,
		"validated":           strconv.FormatBool(verified),
		"before_risk":         fmt.Sprintf("%.2f", beforeRisk),
		"after_risk":          fmt.Sprintf("%.2f", afterRisk),
		"effect_summary":      truncateString(effectSummary, 220),
		"post_action_verdict": postActionVerdict,
	}
	for key, value := range workflowMessageMetadata(state) {
		metadata[key] = value
	}
	ref, err := e.memoryStore.Append(WorkflowMemoryRecord{
		RecordID:            fmt.Sprintf("%s-remediation", sanitizeID(step.ID)),
		WorkflowID:          state.workflowID,
		IncidentID:          state.rca.IncidentID,
		WorkflowType:        state.workflowType,
		CollectorID:         state.collectorID,
		Status:              "resolved",
		Title:               fmt.Sprintf("Successful remediation on %s", state.collectorID),
		Summary:             firstNonEmpty(effectSummary, step.ResultSummary),
		RootCauseEntity:     firstNonEmpty(state.causal.SuspectedRootCauseEntity, topHypothesisTitle(state.hypotheses)),
		MostLikelyCause:     topHypothesisTitle(state.hypotheses),
		ResolutionSummary:   firstNonEmpty(step.ResultSummary, actionName),
		VerificationSummary: firstNonEmpty(effectSummary, step.VerificationNote),
		CausalPath:          append([]string(nil), state.causal.CausePath...),
		ImpactScope:         append([]string(nil), state.incident.ImpactedScope...),
		Signals:             topTriggeredSignalNames(state.riskSignals, 6),
		Actions:             []string{actionName},
		EvidenceIDs:         append([]string(nil), step.EvidenceIDs...),
		Tags:                []string{"remediation", "closed_loop", string(step.Tool)},
		Hypotheses:          append([]RCAHypothesis(nil), state.hypotheses...),
		PlanSteps:           append([]AgentPlanStep(nil), state.planSteps...),
		ActionOutcomes: []WorkflowMemoryActionOutcome{{
			ActionContractID:   contractID,
			ActionID:           step.ID,
			Action:             actionName,
			ActionIntent:       actionIntent,
			ActionCategory:     actionCategory,
			ExecutionCategory:  executionCategory,
			ValidationCategory: validationCategory,
			ActuatorSafetyTier: actuatorSafetyTier,
			TargetScope:        targetScope,
			Mode:               remediationModeFromStep(step),
			Status:             step.Status,
			ProposalOnly:       parseBoolFromString(step.Query["proposal_only"], false),
			ExecutionEligible:  parseBoolFromString(step.Query["execution_eligible"], true),
			ApprovalState: firstNonEmpty(step.Query["approval_state"], func() string {
				if state.validationReport.Governance != nil {
					return state.validationReport.Governance.ApprovalState
				}
				return ""
			}()),
			ApprovalRequired:   parseBoolFromString(step.Query["requires_approval"], step.ActionContract != nil && step.ActionContract.RequiresApproval),
			DryRun:             parseBoolFromString(step.Query["dry_run"], false),
			Selected:           true,
			CandidateValidated: state.validationReport.Governance != nil && state.validationReport.Governance.ActionCandidateID != "",
			Verification:       firstNonEmpty(effectSummary, step.VerificationNote),
			PostActionVerdict:  postActionVerdict,
			RollbackStatus:     "not_needed",
			RollbackSummary:    strings.TrimSpace(step.Query["rollback_summary"]),
			Validated:          verified,
			Success:            true,
			Useful:             true,
			EffectSummary:      effectSummary,
			EffectComparable:   effectComparable,
			EffectIncomplete:   effectIncomplete,
			EffectMissingData:  append([]string(nil), effectMissingData...),
			BlastRadiusNotes:   dedupeStrings(blastRadiusNotes),
			BeforeRisk:         beforeRisk,
			AfterRisk:          afterRisk,
			ExecutedAt:         step.StartedAt,
			CompletedAt:        step.CompletedAt,
		}},
		Metadata: metadata,
	})
	if err != nil {
		e.logger.Warn("failed to write remediation memory", zap.Error(err))
		return
	}
	if e.telemetry != nil {
		e.telemetry.recordMemoryWriteback()
	}
	if e.orchestrator != nil {
		_ = e.orchestrator.AppendMemoryRecord(ctx, state.workflowID, ref)
	}
}

func snapshotRisk(snapshot *ValidationEvidenceSnapshot) float64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.RiskScore
}

func remediationModeFromStep(step *AgentPlanStep) string {
	if step == nil {
		return ""
	}
	switch step.Status {
	case "verified":
		return "verified"
	case "partially_verified":
		return "partially_verified"
	case "approval_required":
		return "planned_only"
	case "dry_run":
		return "dry_run"
	case "reverted":
		return "reverted"
	}
	if step.Verified {
		return "executed"
	}
	return strings.TrimSpace(step.Status)
}

func (e *WorkflowEngine) persistJointRiskArtifacts(ctx context.Context, state *workflowState) {
	if e == nil || state == nil || state.durableRun == nil {
		return
	}
	world := e.currentWorldModel(state)
	if e.orchestrator != nil {
		_ = e.orchestrator.AttachWorldModel(ctx, state.workflowID, world)
	}
}

func (e *WorkflowEngine) persistRCAArtifacts(ctx context.Context, state *workflowState) {
	if e == nil || state == nil || state.durableRun == nil {
		return
	}
	world := e.currentWorldModel(state)
	if e.orchestrator != nil {
		_ = e.orchestrator.AttachWorldModel(ctx, state.workflowID, world)
	}
	if e.memoryStore != nil {
		metadata := workflowMessageMetadata(state)
		ref, err := e.memoryStore.Append(WorkflowMemoryRecord{
			RecordID:            fmt.Sprintf("%s-incident", sanitizeID(state.workflowID)),
			WorkflowID:          state.workflowID,
			IncidentID:          state.rca.IncidentID,
			WorkflowType:        state.workflowType,
			CollectorID:         state.collectorID,
			Status:              state.rca.Status,
			Title:               firstNonEmpty(state.rca.StructuredReport.IncidentSummary, state.rca.StructuredReport.MostLikelyCause, state.rca.WorkflowID),
			Summary:             state.rca.StructuredReport.IncidentSummary,
			RootCauseEntity:     firstNonEmpty(state.rca.SuspectedRootCauseEntity, state.rca.StructuredReport.SuspectedRootCauseEntity),
			MostLikelyCause:     state.rca.StructuredReport.MostLikelyCause,
			ResolutionSummary:   strings.Join(recommendationSummaries(state.rca.Recommendations), " | "),
			VerificationSummary: state.rca.AgentLoop.StopReason,
			CausalPath:          append([]string(nil), state.rca.CausalPath...),
			ImpactScope:         append([]string(nil), state.rca.ImpactScope...),
			LessonsLearned:      dedupeStrings(append([]string(nil), state.rca.StructuredReport.RecommendedNextSteps...)),
			Signals:             topTriggeredSignalNames(state.riskSignals, 8),
			Actions:             recommendationSummaries(state.rca.Recommendations),
			EvidenceIDs:         append([]string(nil), state.rca.RetrievalEvidenceIDs...),
			Tags:                dedupeStrings(append([]string{"incident_memory", state.rca.Status}, world.Scope...)),
			Timeline:            append([]RCATimelineEvent(nil), state.rca.StructuredReport.Timeline...),
			Hypotheses:          append([]RCAHypothesis(nil), state.hypotheses...),
			PlanSteps:           append([]AgentPlanStep(nil), state.planSteps...),
			ActionOutcomes:      workflowMemoryActionOutcomes(state),
			Metadata:            metadata,
		})
		if err == nil {
			if e.telemetry != nil {
				e.telemetry.recordMemoryWriteback()
			}
			if e.orchestrator != nil {
				_ = e.orchestrator.AppendMemoryRecord(ctx, state.workflowID, ref)
			}
		} else {
			e.logger.Warn("failed to persist RCA memory", zap.Error(err))
		}
	}
}

func (e *WorkflowEngine) attachEvidencePackage(ctx context.Context, workflowID string, report any) (DurableArtifactRef, *DurableArtifactRef) {
	if e == nil || e.evidenceBuilder == nil || e.orchestrator == nil || strings.TrimSpace(workflowID) == "" {
		return DurableArtifactRef{}, nil
	}
	run, err := e.orchestrator.GetRun(ctx, workflowID)
	if err != nil {
		return DurableArtifactRef{}, nil
	}
	var trace *AgentTrace
	if e.traceStore != nil {
		if stored, ok := e.traceStore.GetTrace(workflowID); ok {
			trace = stored
		}
	}
	writeResult, err := e.evidenceBuilder.Write(run, trace, e.AuditRecords(400, workflowID), report)
	if err != nil {
		e.logger.Warn("failed to write workflow evidence package", zap.String("workflow_id", workflowID), zap.Error(err))
		return DurableArtifactRef{}, nil
	}
	_ = e.orchestrator.AttachEvidencePackage(ctx, workflowID, writeResult.PackageRef)
	if writeResult.ArtifactManifest != nil {
		_ = e.orchestrator.AttachArtifactManifest(ctx, workflowID, *writeResult.ArtifactManifest)
	}
	if e.telemetry != nil {
		e.telemetry.recordEvidencePackage()
	}
	return DurableArtifactRef(writeResult.PackageRef), cloneDurableArtifactRef(writeResult.ArtifactManifest)
}

func (e *WorkflowEngine) currentWorldModel(state *workflowState) DurableWorldModel {
	if state == nil {
		return DurableWorldModel{}
	}
	scope := make([]string, 0, len(state.scopeRisks))
	for _, item := range state.scopeRisks {
		if strings.TrimSpace(item.Entity) == "" {
			continue
		}
		scope = append(scope, item.Entity)
		if len(scope) >= 8 {
			break
		}
	}
	return DurableWorldModel{
		Summary:         firstNonEmpty(state.rca.Context.TopologySummary, state.topoData.Snapshot.Summary),
		Scope:           dedupeStrings(scope),
		DownstreamNodes: e.calculateDownstreamNodes(state.collectorID, state.topoData),
		RecentChanges:   dedupeStrings(append(changeSummaries(state.changeLinks), state.logsData.RecentDeploys...)),
		Topology:        state.topoData.Snapshot,
	}
}

func (e *WorkflowEngine) calculateDownstreamNodes(collectorID string, topo topologyToolData) []string {
	if collectorID == "" || len(topo.Snapshot.Edges) == 0 {
		return nil
	}
	// Find nodes that depend on the current node (Source -> Target where Source is collectorID)
	downstream := make([]string, 0)
	seen := make(map[string]bool)
	for _, edge := range topo.Snapshot.Edges {
		if edge.Source == collectorID && !seen[edge.Target] {
			downstream = append(downstream, edge.Target)
			seen[edge.Target] = true
		}
	}
	return downstream
}

func knowledgeToolLabel(tool ToolName) string {
	switch tool {
	case ToolHistoricalIncident:
		return "historical incident retrieval"
	case ToolRunbookRetrieval:
		return "runbook retrieval"
	case ToolSimilarCase:
		return "similar-case retrieval"
	case ToolRAGQuery:
		return "RAG query"
	case ToolKnowledge:
		return "knowledge retrieval"
	default:
		return "RAG retrieval"
	}
}

func summarizeRequiredPlanSteps(steps []AgentPlanStep) (planned int, verified int) {
	for _, step := range steps {
		if !step.Required || strings.TrimSpace(step.SupersededBy) != "" {
			continue
		}
		planned++
		if step.Verified {
			verified++
		}
	}
	return planned, verified
}

func expectsLogBurst(state *workflowState) bool {
	if state == nil {
		return false
	}
	for _, signal := range state.riskSignals {
		if signal.Triggered && signal.ID == "log_burst" {
			return true
		}
	}
	return false
}

func (e *WorkflowEngine) tryPlanRevision(state *workflowState, failedStep *AgentPlanStep, reason string) bool {
	if e == nil || state == nil || failedStep == nil {
		return false
	}
	if state.planReplans >= maxInt(0, e.cfg.MaxPlanIterations-1) {
		return false
	}
	if len(state.planSteps) >= e.cfg.MaxPlanSteps {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "verification gap"
	}

	next := AgentPlanStep{
		ID:        fmt.Sprintf("%s-r%d", failedStep.ID, state.planReplans+1),
		Order:     len(state.planSteps) + 1,
		Iteration: state.planIterations + 1,
		Status:    "pending",
	}
	switch failedStep.Tool {
	case ToolMetrics:
		next.Title = "Expand metrics window"
		next.Objective = "Re-check hypotheses using broader historical baseline."
		next.Tool = ToolMetrics
		next.Query = map[string]string{"mode": "extended", "window": "2h"}
	case ToolLogs:
		next.Title = "Broaden log search"
		next.Objective = "Look for disconfirming/supporting errors across services."
		next.Tool = ToolLogs
		next.Query = map[string]string{"query": "error warn panic timeout refused deploy rollback"}
	case ToolChangeQuery:
		next.Title = "Expand change correlation window"
		next.Objective = "Re-check deployments, config changes, and runtime upgrades using a wider window."
		next.Tool = ToolChangeQuery
		next.Query = map[string]string{
			"query":            firstNonEmpty(state.incident.Summary, "deploy rollout config driver feature flag"),
			"incident_summary": firstNonEmpty(state.incident.Summary, state.trigger),
			"scope":            firstNonEmpty(state.collectorID, "fleet"),
		}
	case ToolTopology:
		next.Title = "Rebuild topology mapping"
		next.Objective = "Refresh blast radius mapping from latest nodes/pods/services."
		next.Tool = ToolTopology
	case ToolSecurity:
		next.Title = "Collect remediation dry-run plan"
		next.Objective = "Prepare guarded remediation option with rollback details."
		next.Tool = ToolRemediation
		next.Query = map[string]string{"action": "targeted_restart_candidate", "scope": firstNonEmpty(state.collectorID, "fleet")}
	case ToolEBPFQuery:
		next.Title = "Expand eBPF query window"
		next.Objective = "Re-check behavior anomalies using a wider runtime window."
		next.Tool = ToolEBPFQuery
		next.Query = map[string]string{"window": "2h", "mode": "extended"}
	case ToolGPU:
		next.Title = "Refresh GPU pressure snapshot"
		next.Objective = "Collect a fresh GPU utilization and process attribution snapshot."
		next.Tool = ToolGPU
	case ToolSecurityGraph:
		next.Title = "Rebuild security graph with broader scope"
		next.Objective = "Collect additional process-to-port/IP/path relationships."
		next.Tool = ToolSecurityGraph
	case ToolProcessLineage:
		next.Title = "Refresh process lineage snapshot"
		next.Objective = "Reconstruct lineage after latest telemetry refresh."
		next.Tool = ToolProcessLineage
	default:
		next.Title = "Capture bounded profile"
		next.Objective = "Gather additional runtime evidence for unresolved hypotheses."
		next.Tool = ToolProfiling
		next.Query = map[string]string{"reason": reason}
	}
	next.Required = failedStep.Required

	state.planReplans++
	state.planIterations++
	failedStep.SupersededBy = next.ID
	state.planSteps = append(state.planSteps, next)
	revision := AgentPlanRevision{
		Iteration: state.planIterations,
		Reason:    truncateString(reason, 220),
		CreatedAt: time.Now().UTC(),
		Steps:     append([]AgentPlanStep(nil), state.planSteps...),
	}
	state.planRevisions = append(state.planRevisions, revision)
	if state.durableRun != nil {
		_ = e.orchestrator.RecordPlanRevision(context.Background(), state.workflowID, revision)
		_ = e.orchestrator.RecordStepState(context.Background(), state.workflowID, "plan_act_verify_loop", *failedStep)
		_ = e.orchestrator.RecordStepState(context.Background(), state.workflowID, "plan_act_verify_loop", next)
	}
	e.audit(
		state.workflowID,
		state.workflowType,
		"plan_act_verify_loop",
		"plan.revised",
		"success",
		state.collectorID,
		state.dryRun,
		e.cfg.RequireApproval,
		true,
		map[string]string{"failed_step": failedStep.ID, "new_step": next.ID},
		revision.Reason,
		nil,
	)
	return true
}

func (e *WorkflowEngine) stepGenerateHypotheses(_ context.Context, state *workflowState) error {
	if len(state.hypotheses) == 0 {
		state.hypotheses = generateDeterministicHypotheses(state)
	}
	rerankHypotheses(state)
	return nil
}

func (e *WorkflowEngine) stepCollectEvidence(_ context.Context, state *workflowState) error {
	evidence := make([]RCAEvidence, 0, 24)
	for _, signal := range state.incident.GroupedSignals {
		evidence = append(evidence, RCAEvidence{
			ID:         firstNonEmpty(signal.EvidenceIDs...),
			Kind:       "incident_signal",
			Source:     signal.Source,
			Scope:      signal.Scope,
			Entity:     signal.Entity,
			Summary:    signal.Summary,
			MetricName: signal.SignalType,
			Value:      signal.Score,
			Timestamp:  signal.LastObserved,
		})
	}
	for _, signal := range topTriggeredSignals(state.riskSignals, 8) {
		evidence = append(evidence, RCAEvidence{
			ID:         fmt.Sprintf("ev-%s", sanitizeID(signal.ID)),
			Kind:       "metric_delta",
			Source:     "metrics_query",
			Scope:      signal.Scope,
			Entity:     signal.Entity,
			Summary:    fmt.Sprintf("%s moved from baseline %.3f to %.3f", signal.Name, signal.Baseline, signal.Current),
			MetricName: signal.Name,
			Value:      signal.Current,
			Baseline:   signal.Baseline,
			Delta:      signal.DeltaPercent,
			Timestamp:  signal.LastObservedAt,
		})
	}
	for _, signal := range state.riskSignals {
		if signal.Triggered || signal.Score < 0.03 {
			continue
		}
		evidence = append(evidence, RCAEvidence{
			ID:         fmt.Sprintf("ev-disconfirm-%s", sanitizeID(signal.ID)),
			Kind:       "near_baseline_signal",
			Source:     "metrics_query",
			Scope:      signal.Scope,
			Entity:     signal.Entity,
			Summary:    fmt.Sprintf("%s remained near baseline on %s", signal.Name, signal.Entity),
			MetricName: signal.Name,
			Value:      signal.Current,
			Baseline:   signal.Baseline,
			Delta:      signal.DeltaPercent,
			Timestamp:  signal.LastObservedAt,
		})
	}

	for idx, snippet := range state.logsData.Snippets {
		evidence = append(evidence, RCAEvidence{
			ID:        fmt.Sprintf("ev-log-%d", idx+1),
			Kind:      "log_snippet",
			Source:    "logs_query",
			Scope:     "service",
			Entity:    firstNonEmpty(state.collectorID, "fleet"),
			Summary:   "recent log anomaly",
			Snippet:   snippet,
			Timestamp: state.now,
		})
		if idx >= 5 {
			break
		}
	}

	for idx, co := range state.cooccurrences {
		evidence = append(evidence, RCAEvidence{
			ID:        fmt.Sprintf("ev-corr-%d", idx+1),
			Kind:      "correlation",
			Source:    "joint_risk",
			Scope:     co.Scope,
			Entity:    co.Entity,
			Summary:   co.Explanation,
			Timestamp: state.now,
		})
		state.corr = append(state.corr, RCACorrelation{
			ID:          co.ID,
			Scope:       co.Scope,
			Entity:      co.Entity,
			Signals:     append([]string{}, co.Signals...),
			Coefficient: co.Correlation,
			Summary:     co.Explanation,
		})
	}

	for i, event := range state.ebpf.RuntimeEvents {
		evidence = append(evidence, RCAEvidence{
			ID:        firstNonEmpty(event.EvidenceID, fmt.Sprintf("ev-ebpf-%d", i+1)),
			Kind:      "runtime_security_event",
			Source:    "trace_query",
			Scope:     firstNonEmpty(event.NodeScope, "node"),
			Entity:    firstNonEmpty(event.PID, state.collectorID),
			Summary:   firstNonEmpty(event.Description, fmt.Sprintf("%s %s", event.Category, event.Type)),
			Snippet:   fmt.Sprintf("severity=%s confidence=%.2f category=%s type=%s", event.Severity, event.Confidence, event.Category, event.Type),
			Timestamp: event.Timestamp,
		})
		if i >= 11 {
			break
		}
	}

	for _, hit := range state.retrievedDocs {
		evidence = append(evidence, RCAEvidence{
			ID:        hit.EvidenceID,
			Kind:      "knowledge_retrieval",
			Source:    "knowledge_retrieval",
			Scope:     "knowledge_base",
			Entity:    hit.SourcePath,
			Summary:   firstNonEmpty(hit.Title, hit.SourcePath),
			Snippet:   hit.Snippet,
			Timestamp: state.now,
		})
	}
	for _, link := range state.changeLinks {
		evidence = append(evidence, RCAEvidence{
			ID:        firstNonEmpty(link.ChangeID, fmt.Sprintf("ev-change-%s", sanitizeID(link.Summary))),
			Kind:      "change_event",
			Source:    firstNonEmpty(link.Source, "change_intel"),
			Scope:     firstNonEmpty(link.Scope, "change"),
			Entity:    firstNonEmpty(link.Entity, state.collectorID),
			Summary:   firstNonEmpty(link.Summary, link.Category),
			Snippet:   truncateString(firstNonEmpty(link.ImpactSummary, link.HypothesisHint), 200),
			Value:     link.CorrelationScore,
			Timestamp: link.StartedAt,
		})
	}
	for _, insight := range state.adaptiveBaselines {
		evidence = append(evidence, RCAEvidence{
			ID:         fmt.Sprintf("ev-baseline-%s", sanitizeID(insight.Dimension+"-"+insight.Metric)),
			Kind:       "adaptive_baseline",
			Source:     "adaptive_baseline",
			Scope:      "workload",
			Entity:     firstNonEmpty(insight.Entity, state.collectorID),
			Summary:    insight.Explanation,
			MetricName: insight.Metric,
			Value:      insight.Current,
			Baseline:   insight.Baseline,
			Delta:      insight.DeltaPercent,
			Timestamp:  state.now,
		})
	}
	for metric, value := range state.gpu.Metrics {
		evidence = append(evidence, RCAEvidence{
			ID:         fmt.Sprintf("ev-gpu-%s", sanitizeID(metric)),
			Kind:       "gpu_metric",
			Source:     "gpu_query",
			Scope:      "gpu",
			Entity:     firstNonEmpty(state.collectorID, "fleet"),
			Summary:    fmt.Sprintf("%s = %.2f", metric, value),
			MetricName: metric,
			Value:      value,
			Timestamp:  state.now,
		})
		if len(state.gpu.Metrics) > 6 && len(evidence) >= 32 {
			break
		}
	}

	assignEvidenceToHypotheses(state.hypotheses, evidence)
	state.evidence = evidence
	state.causal = buildCausalAnalysis(state)
	rerankHypotheses(state)
	return nil
}

func (e *WorkflowEngine) stepAnalysisHandoffFinalize(ctx context.Context, state *workflowState) error {
	if len(state.recommendation) == 0 {
		state.recommendation = buildRCARecommendations(state)
	}
	state.analysisHandoff = buildAnalysisHandoff(state)
	if e.cfg.AgentMessageProtocolEnabled {
		e.emitAnalysisHandoffMessage(ctx, state, state.analysisHandoff)
		e.emitValidationRequestMessage(ctx, state)
	}
	if state.durableRun != nil {
		_ = e.orchestrator.AttachAnalysisHandoff(ctx, state.workflowID, state.analysisHandoff)
	}
	return nil
}

func (e *WorkflowEngine) stepValidationActionLoop(ctx context.Context, state *workflowState) error {
	if !e.cfg.ValidationAgentEnabled {
		state.validationReport = ValidationActionReport{
			SchemaVersion:          ValidationActionReportSchemaVersion,
			Agent:                  "validation_action_agent",
			IncidentID:             firstNonEmpty(state.rca.IncidentID, state.workflowID),
			CorrelationID:          firstNonEmpty(state.workflowID, state.rca.TraceID),
			Mode:                   "disabled",
			StartedAt:              time.Now().UTC(),
			CompletedAt:            time.Now().UTC(),
			StopReason:             "validation agent disabled",
			DegradedFallbackReason: "config disabled",
		}
		return nil
	}
	if state.analysisHandoff.Agent == "" {
		state.analysisHandoff = buildAnalysisHandoff(state)
	}
	agent := newValidationActionAgent(e.cfg, e.logger)
	report := agent.Run(ctx, state)
	if !e.cfg.AgentMessageProtocolEnabled && report.SourceAnalysisMessage == nil {
		report.SourceAnalysisMessage = cloneAgentMessageRef(state.analysisHandoffMessage)
		report.SourceValidationRequest = cloneAgentMessageRef(state.validationRequestMessage)
	}
	state.validationReport = report
	applyValidationReport(state, report)
	state.planIterations = report.Iterations
	state.stepsExecuted = report.ToolCalls
	state.stepsVerified = countValidatedTargets(report.Results)
	state.planCompleted, state.planStopReason = summarizeValidationProgress(report, e.cfg.ValidationConfidenceThreshold)
	state.planSteps = validationResultsToPlanSteps(report)
	state.planRevisions = []AgentPlanRevision{{
		Iteration: 1,
		Reason:    "validation targets from analysis handoff",
		CreatedAt: report.StartedAt,
		Steps:     append([]AgentPlanStep(nil), state.planSteps...),
	}}
	if e.cfg.AgentMessageProtocolEnabled {
		ref := e.emitValidationResultMessage(ctx, state, report)
		if ref != nil {
			report.ResultMessage = cloneAgentMessageRef(ref)
			state.validationReport.ResultMessage = cloneAgentMessageRef(ref)
		}
	}
	if state.durableRun != nil {
		for _, record := range report.LoopRecords {
			_ = e.orchestrator.RecordValidationLoop(ctx, state.workflowID, record)
		}
		_ = e.orchestrator.AttachValidationReport(ctx, state.workflowID, state.validationReport)
	}
	return nil
}

func countValidatedTargets(results []ValidationTargetResult) int {
	count := 0
	for _, result := range results {
		if result.Verdict == ValidationVerdictConfirmed || result.Verdict == ValidationVerdictPartiallySupported {
			count++
		}
	}
	return count
}

func summarizeValidationProgress(report ValidationActionReport, threshold float64) (bool, string) {
	stop := firstNonEmpty(report.StopReason, "validation completed")
	lowerStop := strings.ToLower(stop)
	switch {
	case strings.Contains(lowerStop, "budget"), strings.Contains(lowerStop, "timeout"), strings.Contains(lowerStop, "approval"):
		if report.Confidence < threshold || countValidatedTargets(report.Results) == 0 || len(report.UnresolvedUncertainty) > 0 {
			return false, "confidence remained too low"
		}
		return false, stop
	case len(report.Results) == 0:
		if report.Confidence < threshold {
			return false, "confidence remained too low"
		}
		return false, "no validation targets executed"
	case len(report.UnresolvedUncertainty) > 0:
		if report.Confidence < threshold {
			return false, "confidence remained too low"
		}
		return false, "validation completed with unresolved uncertainty"
	default:
		if report.Confidence < threshold {
			return false, "confidence remained too low"
		}
		return true, stop
	}
}

func validationResultsToPlanSteps(report ValidationActionReport) []AgentPlanStep {
	steps := make([]AgentPlanStep, 0, len(report.Results))
	for idx, result := range report.Results {
		tool := ToolMetrics
		if len(result.ToolSequence) > 0 {
			tool = result.ToolSequence[0]
		}
		verificationNote := firstNonEmpty(result.StopReason, result.Summary)
		if result.Verdict == ValidationVerdictInsufficientEvidence {
			verificationNote = firstNonEmpty(result.Summary, result.StopReason)
		}
		lowerNote := strings.ToLower(verificationNote)
		verified := result.Verdict == ValidationVerdictConfirmed || result.Verdict == ValidationVerdictPartiallySupported
		if slicesContainsString(result.EvidenceGaps, "metric_baseline") || strings.Contains(lowerNote, "insufficient metric history") {
			tool = ToolMetrics
			verified = false
			if !strings.Contains(lowerNote, "insufficient metric history") {
				verificationNote = "insufficient metric history to validate the current target"
				lowerNote = verificationNote
			}
		}
		steps = append(steps, AgentPlanStep{
			ID:               firstNonEmpty(result.TargetID, fmt.Sprintf("validation-%d", idx+1)),
			Order:            idx + 1,
			Iteration:        1,
			Title:            result.Title,
			Objective:        result.Summary,
			Tool:             tool,
			Required:         result.TargetType != ValidationTargetRecommendation,
			Status:           string(result.Verdict),
			ResultSummary:    result.Summary,
			Verified:         verified,
			VerificationNote: verificationNote,
			EvidenceIDs:      append([]string(nil), result.SupportingEvidenceIDs...),
		})
	}
	return steps
}

func buildRCARecommendations(state *workflowState) []WorkflowRecommendation {
	recs := make([]WorkflowRecommendation, 0, 12)
	if state == nil {
		return recs
	}
	for _, hypothesis := range state.hypotheses {
		checks := append([]string{}, checksForHypothesis(hypothesis.Title)...)
		checks = append(checks, hypothesis.RecommendedActions...)
		checks = dedupeStrings(checks)
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("rca-check-%d", hypothesis.Rank),
			"immediate_investigation",
			priorityForConfidence(hypothesis.Confidence),
			fmt.Sprintf("Validate hypothesis: %s", hypothesis.Title),
			hypothesis.Description,
			firstNonEmpty(state.collectorID, "fleet"),
			checks,
			true, true, false, true,
			"read-only validation step",
			"",
			"top-ranked hypothesis needs explicit verification before containment",
			"converts the current leading explanation into verified evidence",
			priorityForConfidence(hypothesis.Confidence),
			hypothesis.Confidence,
			append([]string(nil), hypothesis.EvidenceIDs...),
		))
	}

	if len(state.hypotheses) == 0 {
		recs = append(recs, recommendationFromFields(
			"rca-check-fallback",
			"immediate_investigation",
			"medium",
			"Expand evidence window and collect additional telemetry",
			"current evidence does not strongly support a single hypothesis",
			firstNonEmpty(state.collectorID, "fleet"),
			[]string{"increase window to 2h", "capture process-level CPU/IO counters", "refresh topology snapshot"},
			true, true, false, true,
			"none",
			"",
			"current evidence is insufficient for a stable diagnosis",
			"improves hypothesis confidence or rules out false leads",
			"medium",
			0.42,
			nil,
		))
	}

	if len(state.incident.ImpactedScope) > 0 && incidentSeverity(state.incident.GroupedSignals, state.incident.Severity) != "low" {
		recs = append(recs, recommendationFromFields(
			"rca-contain-1",
			"probable_containment",
			"high",
			"Reduce blast radius while the RCA is still open",
			state.incident.Summary,
			strings.Join(state.incident.ImpactedScope, ", "),
			[]string{"isolate the narrowest failing component first", "rate-limit retries or pause rollout only after approval"},
			false, true, true, true,
			"revert rollout isolation or retry policy change immediately if healthy traffic regresses",
			"containment changes can affect production traffic and require approval",
			"multi-signal incidents should be contained before they cascade across the topology",
			"limits customer impact during investigation",
			"high",
			state.incident.Confidence,
			nil,
		))
	}
	if len(state.changeLinks) > 0 {
		top := state.changeLinks[0]
		recs = append(recs, recommendationFromFields(
			"rca-change-link",
			"immediate_investigation",
			priorityForConfidence(top.CorrelationScore),
			fmt.Sprintf("Validate the recent %s change before broader remediation", top.Category),
			firstNonEmpty(top.ImpactSummary, top.Summary),
			firstNonEmpty(top.Scope, state.collectorID, "fleet"),
			[]string{"confirm exact rollout/config/driver delta", "compare with previous revision", "prepare the rollback path before executing any reversal"},
			false, true, true, true,
			rollbackHintForChangeCategory(top.Category),
			"recent operational changes can be causal and need approval-aware rollback planning",
			"temporal adjacency and scope overlap make the recent change a plausible trigger",
			"narrows the highest-value containment path without skipping verification",
			priorityForConfidence(top.CorrelationScore),
			top.CorrelationScore,
			compactStrings(top.ChangeID),
		))
	}
	if len(state.incidentMemoryMatches) > 0 {
		top := state.incidentMemoryMatches[0]
		recs = append(recs, recommendationFromFields(
			"rca-memory-match",
			"immediate_investigation",
			"medium",
			fmt.Sprintf("Cross-check against prior incident memory: %s", firstNonEmpty(top.Title, top.SourcePath)),
			truncateString(firstNonEmpty(top.Summary, top.Snippet), 220),
			"incident_memory",
			[]string{"compare the top root cause and action outcomes", "prefer previously verified rollback paths if the evidence still matches"},
			true, true, false, true,
			"read-only historical comparison",
			"",
			"durable incident memory can reduce repeated mistakes and unsafe replays",
			"reuses verified operator learning and prior action outcomes",
			"low",
			top.Score,
			[]string{top.EvidenceID},
		))
	}
	if strings.TrimSpace(state.retrievalSummary) != "" {
		recs = append(recs, recommendationFromFields(
			"rca-knowledge-summary",
			"immediate_investigation",
			"medium",
			"Cross-check RCA against retrieved historical cases and runbooks",
			state.retrievalSummary,
			"knowledge_base",
			[]string{"match the top hypothesis against retrieved cases or manuals", "carry forward retrieved evidence IDs into incident notes"},
			true, true, false, true,
			"read-only knowledge review",
			"",
			"retrieved knowledge can confirm whether this incident pattern is already understood",
			"reduces repeat mistakes and improves remediation precision",
			"low",
			state.retrievalConfidence,
			state.retrievalEvidenceIDs,
		))
	}
	for i, hit := range state.retrievedCases {
		if i >= 3 {
			break
		}
		checks := appendKnowledgeChecks(
			[]string{"compare the top hypothesis with the retrieved likely causes", "confirm whether the same trigger conditions exist in the current incident"},
			hit,
			2,
			1,
		)
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("rca-case-%d", i+1),
			"immediate_investigation",
			"medium",
			fmt.Sprintf("Compare the incident against similar case: %s", firstNonEmpty(hit.Title, hit.SourcePath)),
			fmt.Sprintf("%s [%s] %s", hit.SourcePath, hit.EvidenceID, truncateString(firstNonEmpty(strings.Join(hit.LikelyCauses, " · "), hit.Snippet), 220)),
			"knowledge_base",
			checks,
			true, true, false, true,
			"read-only knowledge review",
			"",
			"historical analogies can strengthen or disprove the leading root-cause hypothesis",
			"improves operator next-step quality",
			"low",
			hit.Score,
			[]string{hit.EvidenceID},
		))
	}
	for i, hit := range state.retrievedRunbooks {
		if i >= 2 {
			break
		}
		checks := appendKnowledgeChecks(
			[]string{"verify preconditions before applying any runbook action", "document which retrieved step matched the validated hypothesis"},
			hit,
			2,
			2,
		)
		recs = append(recs, recommendationFromFields(
			fmt.Sprintf("rca-runbook-%d", i+1),
			"medium_term_remediation",
			"medium",
			fmt.Sprintf("Apply the relevant runbook only after hypothesis verification: %s", firstNonEmpty(hit.Title, hit.SourcePath)),
			fmt.Sprintf("%s [%s] %s", hit.SourcePath, hit.EvidenceID, truncateString(firstNonEmpty(strings.Join(hit.RemediationSteps, " · "), hit.Snippet), 220)),
			"knowledge_base",
			checks,
			true, true, false, true,
			"revert to the last known-good operator baseline if the runbook does not fit current evidence",
			"",
			"retrieved runbooks should become concrete remediation guidance only after the diagnosis is grounded",
			"turns historical knowledge into actionable remediation without skipping verification",
			"low",
			hit.Score,
			[]string{hit.EvidenceID},
		))
	}
	recs = dedupeRecommendations(recs)
	if len(recs) > 12 {
		recs = recs[:12]
	}
	return recs
}

func (e *WorkflowEngine) stepRCARecommendations(_ context.Context, state *workflowState) error {
	recs := buildRCARecommendations(state)
	if state.validationReport.Agent != "" {
		recs = filterRecommendationsWithValidation(recs, state.validationReport)
	}
	state.recommendation = recs

	for _, rec := range state.recommendation {
		e.audit(state.workflowID, state.workflowType, "recommendation_generation", "recommendation.generated", "success", state.collectorID, state.dryRun, rec.RequiresApproval, true,
			map[string]string{"recommendation_id": rec.ID, "priority": rec.Priority},
			rec.Summary,
			nil,
		)
	}
	return nil
}

func filterRecommendationsWithValidation(recs []WorkflowRecommendation, report ValidationActionReport) []WorkflowRecommendation {
	if len(recs) == 0 || len(report.Results) == 0 {
		return recs
	}
	validated := make(map[string]struct{}, len(report.ValidatedRecommendationIDs))
	rejected := make(map[string]struct{}, len(report.RejectedRecommendationIDs))
	for _, id := range report.ValidatedRecommendationIDs {
		validated[id] = struct{}{}
	}
	for _, id := range report.RejectedRecommendationIDs {
		rejected[id] = struct{}{}
	}
	out := make([]WorkflowRecommendation, 0, len(recs))
	for _, rec := range recs {
		if _, blocked := rejected[rec.ID]; blocked {
			continue
		}
		if len(validated) == 0 {
			out = append(out, rec)
			continue
		}
		if _, ok := validated[rec.ID]; ok || !strings.HasPrefix(rec.ID, "rca-") {
			out = append(out, rec)
		}
	}
	if len(out) == 0 {
		return recs
	}
	return out
}

func (e *WorkflowEngine) stepPostActionValidation(ctx context.Context, state *workflowState) error {
	if !e.cfg.ValidationAgentEnabled {
		return nil
	}
	if state.validationReport.Agent == "" {
		state.validationReport = ValidationActionReport{
			SchemaVersion:          ValidationActionReportSchemaVersion,
			Agent:                  "validation_action_agent",
			IncidentID:             firstNonEmpty(state.rca.IncidentID, state.workflowID),
			CorrelationID:          firstNonEmpty(state.workflowID, state.rca.TraceID),
			Mode:                   "deterministic_report",
			StartedAt:              time.Now().UTC(),
			CompletedAt:            time.Now().UTC(),
			StopReason:             "validation loop unavailable",
			DegradedFallbackReason: "validation report missing",
		}
	}
	var step *AgentPlanStep
	if state.validationReport.Governance != nil {
		step = findPlanStepByID(state, state.validationReport.Governance.StepID)
	}
	if step == nil {
		state.validationReport.PostActionValidation = &PostActionValidationSummary{
			Verdict:           ValidationVerdictInsufficientEvidence,
			ExecutionCategory: normalizeValidationCategory("read_only_validation"),
			FallbackMode:      "not_executed",
			Summary:           "no guarded remediation executed; validation agent stayed read-only",
		}
		appendValidationActionNote(&state.validationReport, state.validationReport.PostActionValidation.Summary)
		e.emitPostActionValidationMessage(ctx, state)
		if state.durableRun != nil {
			_ = e.orchestrator.AttachValidationReport(ctx, state.workflowID, state.validationReport)
		}
		return nil
	}
	updateValidationGovernanceFromStep(&state.validationReport, step)
	if !guardedActionExecuted(step) {
		state.validationReport.PostActionValidation = &PostActionValidationSummary{
			ActionID:          firstNonEmpty(contractIDFromStep(step), step.ID),
			ExecutionCategory: normalizeValidationCategory(firstNonEmpty(step.Query["validation_category"], "read_only_validation")),
			FallbackMode:      "not_executed",
			Verdict:           ValidationVerdictInsufficientEvidence,
			Summary:           guardedActionSkippedSummary(step),
			BeforeRisk:        snapshotRisk(state.beforeActionState),
			BeforeSnapshot:    state.beforeActionState,
		}
		appendValidationActionNote(&state.validationReport, state.validationReport.PostActionValidation.Summary)
		e.emitPostActionValidationMessage(ctx, state)
		if state.durableRun != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "post_action_validation", *step)
			_ = e.orchestrator.AttachValidationReport(ctx, state.workflowID, state.validationReport)
		}
		return nil
	}

	summary := state.postActionEffect
	if summary == nil {
		effect := e.verifyRemediationEffect(ctx, state)
		summary = &effect
	}
	state.postActionEffect = summary
	state.validationReport.PostActionValidation = summary
	step.VerificationNote = summary.Summary
	step.Query["post_action_verdict"] = string(summary.Verdict)
	if summary.Comparison != nil && len(summary.Comparison.MissingData) > 0 {
		step.Query["effect_missing_data"] = strings.Join(summary.Comparison.MissingData, ",")
	}
	switch summary.Verdict {
	case ValidationVerdictConfirmed:
		step.Verified = true
		step.Status = "verified"
		state.stepsVerified++
		if e.telemetry != nil {
			e.telemetry.recordVerification(true)
		}
		e.recordSuccessfulRemediation(ctx, state, step)
	case ValidationVerdictPartiallySupported:
		step.Status = "partially_verified"
		if e.telemetry != nil {
			e.telemetry.recordVerification(false)
		}
	default:
		step.Status = "verification_failed"
		if e.telemetry != nil {
			e.telemetry.recordVerification(false)
		}
		if e.cfg.AutoRollbackOnVerificationFailure {
			e.stepCompensate(ctx, state, step)
		}
	}
	updateValidationGovernanceFromStep(&state.validationReport, step)
	appendValidationActionNote(&state.validationReport, state.validationReport.PostActionValidation.Summary)
	e.emitPostActionValidationMessage(ctx, state)
	if state.durableRun != nil {
		_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "post_action_validation", *step)
		outcome := "unresolved"
		switch summary.Verdict {
		case ValidationVerdictConfirmed:
			outcome = "resolved"
		case ValidationVerdictPartiallySupported:
			outcome = "partial"
		case ValidationVerdictContradicted:
			outcome = "contradicted"
		}
		_ = e.orchestrator.RecordVerification(ctx, state.workflowID, step.ID, DurableVerificationRecord{
			Outcome:      outcome,
			Verdict:      string(summary.Verdict),
			Success:      summary.Verdict == ValidationVerdictConfirmed,
			Objective:    "verify remediation impact",
			Note:         summary.Summary,
			Window:       e.cfg.PostActionValidationWindow.String(),
			PreviousRisk: summary.BeforeRisk,
			CurrentRisk:  summary.AfterRisk,
			EvidenceIDs:  dedupeStrings(append(append([]string(nil), step.EvidenceIDs...), append(summary.SupportingEvidenceIDs, summary.ContradictingEvidenceIDs...)...)),
			Comparison:   summary.Comparison,
			StartedAt:    nonZeroTime(step.StartedAt, time.Now().UTC()),
			CompletedAt:  time.Now().UTC(),
		})
		_ = e.orchestrator.AttachValidationReport(ctx, state.workflowID, state.validationReport)
	}
	return nil
}

func (e *WorkflowEngine) stepGuardedExecutionPlan(ctx context.Context, state *workflowState) error {
	profileResult, err := state.callTool(ctx, "guarded_execution_plan", ToolProfiling, map[string]string{
		"reason":              "rca evidence collection",
		"validation_category": "profiling",
	})
	if err != nil {
		state.limitations = append(state.limitations, "profiling tool unavailable")
	} else if profile, ok := profileResult.Data.(profilingToolData); ok {
		state.profiling = profile
		state.recommendation = append(state.recommendation, recommendationFromFields(
			"rca-profile-capture",
			"immediate_investigation",
			"medium",
			"Capture bounded profile for final confirmation",
			fmt.Sprintf("Command: %s", profile.Command),
			firstNonEmpty(state.collectorID, "fleet"),
			[]string{"run only during active incident", "store artifact with incident ID"},
			false, true, e.cfg.RequireApproval, true,
			"stop capture and remove temporary profile files",
			"profiling introduces runtime overhead and requires approval",
			"profiling is the last bounded evidence step before containment or remediation",
			"raises confidence in the final RCA",
			"medium",
			0.68,
			nil,
		))
		e.audit(state.workflowID, state.workflowType, "guarded_execution_plan", "guarded.action_planned", "success", state.collectorID, state.dryRun, e.cfg.RequireApproval, false,
			map[string]string{"command": profile.Command},
			profile.Message,
			nil,
		)
	}

	candidate := selectedGuardedActionCandidate(state)
	state.escalationDecision = buildEscalationDecision(state)
	if candidate == nil {
		if state.escalationDecision.Escalate {
			e.applyGuardedHumanEscalation(ctx, state, firstNonEmpty(state.escalationDecision.Reason, "human escalation required before any further action"), nil)
			return nil
		}
		note := "no validated action candidate passed the second-agent filter; guarded execution stayed read-only"
		appendValidationActionNote(&state.validationReport, note)
		if state.validationReport.Governance == nil {
			state.validationReport.Governance = &ValidationGovernanceTrace{
				ActionSummary:      note,
				ExecutionCategory:  "read_only_validation",
				ValidationCategory: "read_only_validation",
				ActuatorSafetyTier: "read_only",
				ExecutionEligible:  true,
				DryRun:             true,
				DryRunState:        "read_only_validation",
				RequiresApproval:   false,
				PolicyStatus:       "not_applicable",
				ApprovalState:      "not_required",
				StepStatus:         "not_selected",
			}
		} else {
			state.validationReport.Governance.ActionSummary = firstNonEmpty(state.validationReport.Governance.ActionSummary, note)
			state.validationReport.Governance.ExecutionCategory = firstNonEmpty(state.validationReport.Governance.ExecutionCategory, "read_only_validation")
			state.validationReport.Governance.ValidationCategory = firstNonEmpty(state.validationReport.Governance.ValidationCategory, "read_only_validation")
			state.validationReport.Governance.ActuatorSafetyTier = firstNonEmpty(state.validationReport.Governance.ActuatorSafetyTier, "read_only")
			state.validationReport.Governance.ExecutionEligible = true
			state.validationReport.Governance.DryRun = true
			state.validationReport.Governance.DryRunState = firstNonEmpty(state.validationReport.Governance.DryRunState, "read_only_validation")
			state.validationReport.Governance.PolicyStatus = firstNonEmpty(state.validationReport.Governance.PolicyStatus, "not_applicable")
			state.validationReport.Governance.ApprovalState = firstNonEmpty(state.validationReport.Governance.ApprovalState, "not_required")
			state.validationReport.Governance.StepStatus = firstNonEmpty(state.validationReport.Governance.StepStatus, "not_selected")
		}
		e.emitActionDecisionMessage(ctx, state, nil)
		return nil
	}
	downstream := e.calculateDownstreamNodes(state.collectorID, state.topoData)
	diagnosticContract := candidate.DiagnosticContract
	if diagnosticContract == nil {
		built := buildDiagnosticActionContract(*candidate, state)
		diagnosticContract = &built
	}
	contract := compileValidationActionContract(*diagnosticContract, state.collectorID, downstream)
	safetyDecision := evaluateActuatorExecutionPolicy(e.cfg, &contract, state.dryRun)
	if state.escalationDecision.Escalate && !safetyDecision.ProposalOnly {
		e.applyGuardedHumanEscalation(ctx, state, firstNonEmpty(state.escalationDecision.Reason, "human escalation required before any further action"), nil)
		return nil
	}
	selected := *candidate
	selected.Category = contract.ExecutionCategory
	selected.ActionIntent = contract.Intent
	selected.ActuatorSafetyTier = contract.ActuatorSafetyTier
	selected.BlastRadiusEstimate = contract.BlastRadiusEstimate
	selected.BlastRadiusScope = append([]string(nil), contract.BlastRadiusScope...)
	selected.DiagnosticContract = diagnosticContract
	selected.ActionContract = &contract
	state.selectedAction = &selected
	state.selectedContract = &contract
	state.validationReport.SelectedAction = &selected
	state.validationReport.SelectedDiagnosticContract = diagnosticContract
	setValidationGovernanceFromContract(&state.validationReport, &selected, &contract, safetyDecision, state.dryRun)
	appendValidationActionNote(&state.validationReport, fmt.Sprintf("selected guarded action candidate: %s", selected.Summary))
	step := ensureGuardedActionPlanStep(state, selected, contract, safetyDecision)
	updateValidationGovernanceFromStep(&state.validationReport, step)
	appendGuardedActionRevision(state, "guarded action candidate selected")
	if state.durableRun != nil {
		_ = e.orchestrator.RecordPlanRevision(ctx, state.workflowID, state.planRevisions[len(state.planRevisions)-1])
		_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
	}

	if safetyDecision.ProposalOnly {
		msg := firstNonEmpty(safetyDecision.Reason, fmt.Sprintf("%s action stayed proposal-only", safetyDecision.SafetyTier))
		state.limitations = append(state.limitations, msg)
		step.Status = "proposal_only"
		step.ResultSummary = msg
		step.Query["safety_tier"] = safetyDecision.SafetyTier
		step.Query["proposal_only"] = "true"
		step.Query["execution_eligible"] = "false"
		step.Query["policy_status"] = safetyDecision.Status
		step.Query["policy_reason"] = msg
		step.Query["approval_state"] = "not_applicable"
		step.CompletedAt = time.Now().UTC()
		updateValidationGovernanceFromStep(&state.validationReport, step)
		if state.validationReport.Governance != nil {
			state.validationReport.Governance.PolicyStatus = safetyDecision.Status
			state.validationReport.Governance.PolicyReason = msg
			state.validationReport.Governance.ProposalOnly = true
			state.validationReport.Governance.ExecutionEligible = false
			state.validationReport.Governance.ApprovalState = "not_applicable"
		}
		if state.durableRun != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
			_ = e.orchestrator.AttachPolicy(ctx, state.workflowID, step.ID, DurablePolicyRecord{
				Decision:      safetyDecision,
				PolicyVersion: e.cfg.PolicyVersion,
				RiskTag:       "execution",
				EvaluatedAt:   time.Now().UTC(),
			})
		}
		appendValidationActionNote(&state.validationReport, msg)
		e.emitActionDecisionMessage(ctx, state, step)
		return nil
	}
	beforeSnapshot, snapshotErr := captureValidationEvidenceSnapshot(ctx, state, "before_action")
	if snapshotErr != nil {
		state.limitations = append(state.limitations, "pre-action snapshot unavailable")
	} else {
		state.beforeActionState = beforeSnapshot
	}
	downstreamStr := strings.Join(contract.BlastRadiusScope, ",")
	step.StartedAt = time.Now().UTC()
	step.Query["dry_run"] = fmt.Sprintf("%t", state.dryRun || contract.DryRunDefault)
	step.Query["requires_approval"] = fmt.Sprintf("%t", contract.RequiresApproval)
	step.Query["dry_run_state"] = contract.DryRunState
	step.Query["safety_tier"] = safetyDecision.SafetyTier
	step.Query["proposal_only"] = fmt.Sprintf("%t", safetyDecision.ProposalOnly)
	step.Query["execution_eligible"] = fmt.Sprintf("%t", safetyDecision.ExecutionEligible)
	if state.durableRun != nil {
		_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
	}

	remediationResult, remediationErr := state.callTool(ctx, "guarded_execution_plan", ToolRemediation, map[string]string{
		"action":              firstNonEmpty(contract.Intent, contract.Summary),
		"action_contract":     encodeValidationActionContract(contract),
		"scope":               firstNonEmpty(contract.Target.Scope, state.collectorID, "fleet"),
		"downstream_nodes":    downstreamStr,
		"rollback":            firstNonEmpty(contract.Rollback.Summary, "restore previous known-good revision"),
		"validation_category": contract.ExecutionCategory,
	}, step.ID)
	step.CompletedAt = time.Now().UTC()
	var lastCall WorkflowToolCall
	if len(state.toolCalls) > 0 {
		lastCall = state.toolCalls[len(state.toolCalls)-1]
	}
	updateValidationGovernanceFromToolCall(&state.validationReport, step, lastCall)
	if remediationErr != nil {
		state.limitations = append(state.limitations, "remediation planning unavailable")
		step.Status = firstNonEmpty(map[string]string{"pending": "approval_required"}[lastCall.ApprovalState], map[string]string{"blocked": "blocked", "proposal_only": "proposal_only"}[lastCall.Policy.Status], "failed")
		step.ResultSummary = truncateString(remediationErr.Error(), 220)
		step.Query["approval_state"] = lastCall.ApprovalState
		step.Query["requires_approval"] = fmt.Sprintf("%t", lastCall.Policy.RequiresApproval || contract.RequiresApproval)
		if state.durableRun != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
		}
		appendValidationActionNote(&state.validationReport, fmt.Sprintf("guarded remediation blocked: %v", remediationErr))
		e.emitActionDecisionMessage(ctx, state, step)
		return nil
	}
	remediationData, ok := remediationResult.Data.(remediationToolData)
	if !ok {
		return nil
	}
	step.Status = remediationStepStatus(remediationData, lastCall)
	step.ResultSummary = remediationData.Summary
	step.ToolVersion = lastCall.ToolVersion
	step.ActionContract = cloneValidationActionContract(&remediationData.Contract)
	step.Query["action"] = remediationData.Action
	step.Query["action_intent"] = remediationData.ActionIntent
	step.Query["action_category"] = remediationData.Contract.ActionCategory
	step.Query["scope"] = firstNonEmpty(remediationData.Contract.TargetScope, remediationData.Contract.Target.Scope, step.Query["scope"])
	step.Query["validation_category"] = remediationData.ExecutionCategory
	step.Query["approval_state"] = lastCall.ApprovalState
	step.Query["requires_approval"] = fmt.Sprintf("%t", remediationData.RequiresApproval)
	step.Query["dry_run"] = fmt.Sprintf("%t", remediationData.DryRun)
	step.Query["dry_run_state"] = remediationData.Contract.DryRunState
	updateValidationGovernanceFromStep(&state.validationReport, step)
	if state.durableRun != nil {
		_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
	}
	state.stepsExecuted++
	state.recommendation = append(state.recommendation, recommendationFromFields(
		"rca-remediation-plan",
		"medium_term_remediation",
		"medium",
		fmt.Sprintf("Guarded remediation plan: %s", firstNonEmpty(remediationData.Action, remediationData.ActionIntent)),
		remediationData.Summary,
		firstNonEmpty(state.collectorID, "fleet"),
		append([]string{"run dry-run first", "require explicit approval", "execute single scoped change", "validate rollback plan before execution"}, remediationData.Preconditions...),
		false, true, true, remediationData.Reversible,
		remediationData.RollbackPlan,
		"remediation changes production state and remains approval-gated",
		"the leading RCA hypothesis has a concrete containment or recovery path",
		"restores service health if the hypothesis is correct",
		"medium",
		maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)),
		evidenceIDsFromTopHypotheses(state.hypotheses, 2),
	))
	appendValidationActionNote(&state.validationReport, remediationData.Summary)
	e.audit(state.workflowID, state.workflowType, "guarded_execution_plan", "guarded.remediation_planned", "success", state.collectorID, state.dryRun, true, false,
		map[string]string{"action": remediationData.Action, "validation_category": remediationData.ExecutionCategory, "action_intent": remediationData.ActionIntent, "action_category": remediationData.Contract.ActionCategory, "step_id": step.ID},
		remediationData.Summary,
		nil,
	)
	e.emitActionDecisionMessage(ctx, state, step)
	return nil
}

func (e *WorkflowEngine) applyGuardedHumanEscalation(ctx context.Context, state *workflowState, note string, step *AgentPlanStep) {
	if state == nil {
		return
	}
	note = firstNonEmpty(note, "human escalation required before any further action")
	appendValidationActionNote(&state.validationReport, note)
	if state.validationReport.Governance == nil {
		state.validationReport.Governance = &ValidationGovernanceTrace{}
	}
	state.validationReport.Governance.PolicyStatus = "human_escalation"
	state.validationReport.Governance.PolicyReason = note
	state.validationReport.Governance.ExecutionEligible = false
	state.validationReport.Governance.ProposalOnly = true
	state.validationReport.Governance.ExecutionCategory = "read_only_validation"
	state.validationReport.Governance.ValidationCategory = "read_only_validation"
	if step != nil {
		step.Status = "escalated"
		step.ResultSummary = note
		updateValidationGovernanceFromStep(&state.validationReport, step)
		if state.durableRun != nil && e != nil && e.orchestrator != nil {
			_ = e.orchestrator.RecordStepState(ctx, state.workflowID, "guarded_execution_plan", *step)
		}
	}
	if e != nil {
		e.emitActionDecisionMessage(ctx, state, step)
	}
}

func (e *WorkflowEngine) stepFinalizeRCA(ctx context.Context, state *workflowState) error {
	mergeLLMIntoState(state)
	sort.Slice(state.hypotheses, func(i, j int) bool {
		if state.hypotheses[i].Confidence == state.hypotheses[j].Confidence {
			return state.hypotheses[i].Title < state.hypotheses[j].Title
		}
		return state.hypotheses[i].Confidence > state.hypotheses[j].Confidence
	})
	for i := range state.hypotheses {
		state.hypotheses[i].Rank = i + 1
	}
	if len(state.hypotheses) > e.cfg.MaxHypotheses {
		state.hypotheses = state.hypotheses[:e.cfg.MaxHypotheses]
	}

	for i := range state.hypotheses {
		if len(state.hypotheses[i].EvidenceIDs) > 10 {
			state.hypotheses[i].EvidenceIDs = state.hypotheses[i].EvidenceIDs[:10]
		}
	}

	repro := map[string]string{
		"pipeline":          workflowPipelineVersion,
		"runtime_mode":      e.cfg.RuntimeMode,
		"workflow_type":     state.workflowType,
		"collector_id":      state.collectorID,
		"window":            state.window.String(),
		"dry_run":           strconv.FormatBool(state.dryRun),
		"tool_calls":        strconv.Itoa(len(state.toolCalls)),
		"deterministic":     "true",
		"timestamp_utc":     state.now.Format(time.RFC3339Nano),
		"insights_provider": e.cfg.InsightsProvider,
	}

	if strings.TrimSpace(state.trigger) == "" {
		state.trigger = "anomaly"
	}
	unresolvedGaps := unresolvedGapsFromState(state)
	state.recommendation = dedupeRecommendations(state.recommendation)
	if e.proposedActions != nil && len(state.recommendation) > 0 {
		state.proposedActions = GenerateProposedActions(state.workflowID, state.collectorID, state.recommendation, maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)))
		for _, action := range state.proposedActions {
			e.proposedActions.RecordAction(action)
		}
	}
	structured := buildStructuredRCAReport(state)
	incidentStatus := "open"
	if len(state.hypotheses) == 0 && strings.Contains(strings.ToLower(strings.Join(state.rca.Anomalies, " ")), "no strong anomalies") && state.planCompleted && len(unresolvedGaps) == 0 {
		incidentStatus = "closed"
	} else if !state.planCompleted || len(unresolvedGaps) > 0 {
		incidentStatus = "investigating"
	}
	if state.escalationDecision.Escalate {
		incidentStatus = "escalated"
	}
	incidentID := fmt.Sprintf("inc-%s", sanitizeID(state.workflowID))
	normalizedEvidence := buildNormalizedWorkflowEvidence(state)
	rcaReport := RCAWorkflowReport{
		WorkflowID:            state.workflowID,
		PipelineVersion:       workflowPipelineVersion,
		EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
		IncidentID:            incidentID,
		TraceID:               state.workflowID,
		Status:                incidentStatus,
		CollectorID:           state.collectorID,
		Trigger:               state.trigger,
		GeneratedAt:           state.now,
		SynthesizedIncident:   state.incident,
		Context:               state.rca.Context,
		Anomalies:             append([]string{}, state.rca.Anomalies...),
		Correlations:          append([]RCACorrelation{}, state.corr...),
		Hypotheses:            append([]RCAHypothesis{}, state.hypotheses...),
		Evidence:              append([]RCAEvidence{}, state.evidence...),
		NormalizedEvidence:    evidencev1.CloneRecords(normalizedEvidence),
		Recommendations:       append([]WorkflowRecommendation{}, state.recommendation...),
		ProposedActions:       derefProposedActions(state.proposedActions),
		AgentLoop: AgentLoopSummary{
			Mode:          firstNonEmpty(validationLoopMode(state.validationReport, e.cfg.ValidationAgentEnabled), "plan_act_verify"),
			Iterations:    maxInt(state.planIterations, 1),
			Replans:       state.planReplans,
			StepsPlanned:  len(state.planSteps),
			StepsExecuted: state.stepsExecuted,
			StepsVerified: state.stepsVerified,
			Completed:     state.planCompleted,
			StopReason:    firstNonEmpty(state.planStopReason, "completed"),
			PlanSteps:     append([]AgentPlanStep{}, state.planSteps...),
			PlanRevisions: append([]AgentPlanRevision{}, state.planRevisions...),
		},
		SuspectedRootCauseEntity: firstNonEmpty(state.causal.SuspectedRootCauseEntity, structured.SuspectedRootCauseEntity),
		CausalPath:               append([]string(nil), structured.CausalPath...),
		ImpactPath:               append([]string(nil), state.causal.ImpactPath...),
		ImpactScope:              append([]string(nil), structured.ImpactScope...),
		Uncertainty:              append([]UncertaintyComponent(nil), structured.Uncertainty...),
		EvidenceProvenance:       append([]EvidenceProvenance(nil), structured.EvidenceProvenance...),
		ChangeLinks:              append([]RCAChangeLink(nil), state.changeLinks...),
		BehavioralAssessments:    append([]BehavioralSignalAssessment(nil), state.behavioralAssessments...),
		AdaptiveBaselines:        append([]AdaptiveBaselineInsight(nil), state.adaptiveBaselines...),
		IncidentMemoryMatches:    append([]RetrievedDocumentEvidence(nil), state.incidentMemoryMatches...),
		AdaptiveRuntime:          state.adaptiveState,
		AdaptiveDialogue:         append([]AdaptiveDialogueTurn(nil), state.adaptiveDialogue...),
		AdaptiveToolDecisions:    append([]AdaptiveToolDecision(nil), state.adaptiveToolDecisions...),
		AdaptiveArtifacts:        append([]AdaptiveArtifact(nil), state.adaptiveArtifacts...),
		StructuredReport:         structured,
		Stages:                   append([]PipelineStageResult{}, state.stages...),
		ToolCalls:                append([]WorkflowToolCall{}, state.toolCalls...),
		Reproducibility:          repro,
		UnresolvedGaps:           unresolvedGaps,
		Limitations:              workflowLimitations(state),
		Insights:                 e.insightsStatus(),
		LLMAnalysis:              state.llmAnalysis,
		TelemetryQuality:         state.telemetryQuality,
		RetrievedDocs:            append([]RetrievedDocumentEvidence(nil), state.retrievedDocs...),
		RetrievedCases:           append([]RetrievedDocumentEvidence(nil), state.retrievedCases...),
		RetrievedRunbooks:        append([]RetrievedDocumentEvidence(nil), state.retrievedRunbooks...),
		SimilarIncidentPatterns:  append([]RetrievedDocumentEvidence(nil), state.similarPatterns...),
		RetrievalSummary:         state.retrievalSummary,
		RetrievalEvidenceIDs:     append([]string(nil), state.retrievalEvidenceIDs...),
		RetrievalConfidence:      state.retrievalConfidence,
		SceneClassification:      state.sceneClassification,
		CollectionPlan:           state.collectionPlan,
		RecollectionResults:      append([]RecollectionResult(nil), state.recollectionResults...),
		EvidenceGapState:         state.evidenceGapState,
		EscalationDecision:       state.escalationDecision,
		AnalysisHandoff:          state.analysisHandoff,
		Validation:               state.validationReport,
	}
	rcaReport.Artifacts = buildWorkflowArtifactChain(state, rcaReport, incidentStatus)
	state.rca = rcaReport
	syncRCAMessageRefs(&state.rca, state)
	if e.traceStore != nil {
		var reasoningReview *LLMReasoningReview
		if state.llmAnalysis != nil {
			reasoningReview = state.llmAnalysis.Review
		}
		trace := &AgentTrace{
			TraceID:               state.workflowID,
			WorkflowType:          "rca",
			CollectorID:           state.collectorID,
			EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
			StartedAt:             state.now,
			CompletedAt:           time.Now().UTC(),
			Status:                incidentStatus,
			Incident:              &state.incident,
			PlanVersions:          append([]AgentPlanRevision(nil), state.planRevisions...),
			ToolCalls:             append([]WorkflowToolCall(nil), state.toolCalls...),
			HypothesisUpdates:     append([]HypothesisUpdate(nil), state.hypothesisUpdates...),
			Recommendations:       append([]WorkflowRecommendation(nil), state.recommendation...),
			ReasoningReview:       reasoningReview,
			ProposedActions:       derefProposedActions(state.proposedActions),
			NormalizedEvidence:    evidencev1.CloneRecords(normalizedEvidence),
			Stages:                append([]PipelineStageResult(nil), state.stages...),
			FinalRiskScore:        maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)),
			Summary:               structured.IncidentSummary,
			UnresolvedGaps:        unresolvedGaps,
			RiskTimeline: []RiskTimelineEntry{
				{Timestamp: state.now, RiskScore: maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)), RiskLevel: riskLevelFromConfidence(maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses))), Source: "rca"},
			},
		}
		e.traceStore.RecordTrace(trace)
	}
	e.persistRCAArtifacts(ctx, state)
	return nil
}

func buildStructuredRCAReport(state *workflowState) RCAStructuredReport {
	if state == nil {
		return RCAStructuredReport{}
	}
	symptoms := dedupeStrings(append([]string{}, state.rca.Anomalies...))
	for _, event := range state.investigationEvents {
		symptoms = append(symptoms, firstNonEmpty(event.Title, event.Symptom))
		if len(symptoms) >= 10 {
			break
		}
	}
	scope := []string{}
	for _, row := range state.scopeRisks {
		if strings.TrimSpace(row.Entity) == "" || strings.EqualFold(row.Entity, "n/a") {
			continue
		}
		scope = append(scope, fmt.Sprintf("%s/%s", row.Scope, row.Entity))
	}
	scope = dedupeStrings(scope)
	if len(scope) > 8 {
		scope = scope[:8]
	}

	mostLikely := "insufficient evidence"
	confidence := 0.35
	if len(state.hypotheses) > 0 {
		mostLikely = state.hypotheses[0].Title
		confidence = state.hypotheses[0].Confidence
	}
	confidence = applyTelemetryConfidenceLimit(confidence, state.telemetryQuality)
	suspectedEntity := firstNonEmpty(state.causal.SuspectedRootCauseEntity, mostLikely)
	supporting := []string{}
	for _, trend := range state.trendAssessments {
		if !trend.Triggered {
			continue
		}
		supporting = append(supporting, fmt.Sprintf("%s (%s, delta %.1f%%)", trend.Display, trend.Trend, trend.DeltaPercent))
		if len(supporting) >= 4 {
			break
		}
	}
	for _, signal := range topTriggeredSignals(state.riskSignals, 6) {
		supporting = append(supporting, fmt.Sprintf("%s (delta %.1f%%)", signal.Name, signal.DeltaPercent))
	}
	disconfirming := []string{}
	for _, signal := range state.riskSignals {
		if signal.BehavioralClassification == "expected_recurring_burst" && signal.SuppressionReason != "" {
			disconfirming = append(disconfirming, signal.SuppressionReason)
			continue
		}
		if signal.Triggered {
			continue
		}
		if signal.Score >= 0.04 {
			disconfirming = append(disconfirming, fmt.Sprintf("%s remained near baseline", signal.Name))
		}
	}
	if len(disconfirming) > 6 {
		disconfirming = disconfirming[:6]
	}

	timeline := mergeRCATimelineEvents(state.stages, state.planSteps)
	impactScope := dedupeStrings(append([]string{}, state.incident.ImpactedScope...))
	if len(impactScope) == 0 {
		impactScope = append(impactScope, scope...)
	}
	changeLinks := append([]RCAChangeLink(nil), state.changeLinks...)
	if len(changeLinks) > 4 {
		changeLinks = changeLinks[:4]
	}
	incidentMemoryMatches := append([]RetrievedDocumentEvidence(nil), state.incidentMemoryMatches...)
	if len(incidentMemoryMatches) > 3 {
		incidentMemoryMatches = incidentMemoryMatches[:3]
	}

	return RCAStructuredReport{
		IncidentSummary:          state.incident.Summary,
		Symptoms:                 symptoms,
		Timeline:                 timeline,
		Scope:                    scope,
		MostLikelyCause:          mostLikely,
		SuspectedRootCauseEntity: suspectedEntity,
		CausalPath:               append([]string(nil), state.causal.CausePath...),
		ImpactScope:              impactScope,
		SupportingSignals:        dedupeStrings(supporting),
		DisconfirmingSignals:     dedupeStrings(disconfirming),
		Confidence:               clamp01(confidence),
		Uncertainty:              buildUncertaintyComponents(state),
		EvidenceProvenance:       buildEvidenceProvenance(state),
		ChangeLinks:              changeLinks,
		IncidentMemoryMatches:    incidentMemoryMatches,
		UnresolvedGaps:           unresolvedGapsFromState(state),
		RecommendedNextSteps:     recommendationSummariesByCategory(state.recommendation, "immediate_investigation", 6),
		SafeRemediations:         recommendationSummariesByCategory(state.recommendation, "medium_term_remediation", 4),
	}
}

func validationLoopMode(report ValidationActionReport, enabled bool) string {
	if report.Agent != "" {
		return firstNonEmpty(report.Mode, "validation_action_agent")
	}
	if enabled {
		return "validation_action_agent"
	}
	return ""
}

func mergeRCATimelineEvents(stages []PipelineStageResult, steps []AgentPlanStep) []RCATimelineEvent {
	stageEvents := make([]RCATimelineEvent, 0, len(stages))
	for _, stage := range stages {
		ts := stage.CompletedAt
		if ts.IsZero() {
			ts = stage.StartedAt
		}
		stageEvents = append(stageEvents, RCATimelineEvent{
			Timestamp: ts,
			Phase:     stage.Name,
			Summary:   truncateString(stage.Summary, 220),
		})
	}
	stepEvents := make([]RCATimelineEvent, 0, len(steps))
	for _, step := range steps {
		ts := step.CompletedAt
		if ts.IsZero() {
			ts = step.StartedAt
		}
		stepEvents = append(stepEvents, RCATimelineEvent{
			Timestamp: ts,
			Phase:     "plan_step",
			Summary:   truncateString(fmt.Sprintf("%s [%s] %s", step.Title, step.Status, step.VerificationNote), 220),
		})
	}

	if !sort.SliceIsSorted(stageEvents, func(i, j int) bool {
		return !stageEvents[j].Timestamp.Before(stageEvents[i].Timestamp)
	}) {
		sort.Slice(stageEvents, func(i, j int) bool {
			return stageEvents[i].Timestamp.Before(stageEvents[j].Timestamp)
		})
	}
	if !sort.SliceIsSorted(stepEvents, func(i, j int) bool {
		return !stepEvents[j].Timestamp.Before(stepEvents[i].Timestamp)
	}) {
		sort.Slice(stepEvents, func(i, j int) bool {
			return stepEvents[i].Timestamp.Before(stepEvents[j].Timestamp)
		})
	}

	timeline := make([]RCATimelineEvent, 0, len(stageEvents)+len(stepEvents))
	i, j := 0, 0
	for i < len(stageEvents) && j < len(stepEvents) {
		if stepEvents[j].Timestamp.Before(stageEvents[i].Timestamp) {
			timeline = append(timeline, stepEvents[j])
			j++
			continue
		}
		timeline = append(timeline, stageEvents[i])
		i++
	}
	timeline = append(timeline, stageEvents[i:]...)
	timeline = append(timeline, stepEvents[j:]...)
	return timeline
}

func (e *WorkflowEngine) recordJointRisk(report JointRiskAssessment) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.riskReports = append([]JointRiskAssessment{report}, e.riskReports...)
	if len(e.riskReports) > e.cfg.AuditRetention {
		e.riskReports = e.riskReports[:e.cfg.AuditRetention]
	}
}

func (e *WorkflowEngine) recordRCA(report RCAWorkflowReport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rcaReports = append([]RCAWorkflowReport{report}, e.rcaReports...)
	if len(e.rcaReports) > e.cfg.AuditRetention {
		e.rcaReports = e.rcaReports[:e.cfg.AuditRetention]
	}
}

func (e *WorkflowEngine) recordIncident(report RCAWorkflowReport) {
	if e == nil {
		return
	}
	openedAt := report.GeneratedAt
	if openedAt.IsZero() {
		openedAt = time.Now().UTC()
	}
	mostLikely := strings.TrimSpace(report.StructuredReport.MostLikelyCause)
	if mostLikely == "" && len(report.Hypotheses) > 0 {
		mostLikely = report.Hypotheses[0].Title
	}
	confidence := report.StructuredReport.Confidence
	if confidence <= 0 && len(report.Hypotheses) > 0 {
		confidence = report.Hypotheses[0].Confidence
	}
	status := strings.TrimSpace(report.Status)
	if status == "" {
		status = "open"
	}
	summary := "agentic investigation generated"
	if strings.TrimSpace(report.SynthesizedIncident.Summary) != "" {
		summary = truncateString(report.SynthesizedIncident.Summary, 220)
	} else if len(report.Anomalies) > 0 {
		summary = truncateString(report.Anomalies[0], 220)
	}
	incidentID := strings.TrimSpace(report.IncidentID)
	if incidentID == "" {
		incidentID = fmt.Sprintf("inc-%s", sanitizeID(report.WorkflowID))
	}
	timeline := append([]RCATimelineEvent{}, report.StructuredReport.Timeline...)
	if len(timeline) == 0 {
		for _, stage := range report.Stages {
			eventTime := stage.CompletedAt
			if eventTime.IsZero() {
				eventTime = stage.StartedAt
			}
			timeline = append(timeline, RCATimelineEvent{
				Timestamp: eventTime,
				Phase:     stage.Name,
				Summary:   truncateString(stage.Summary, 220),
			})
		}
	}
	record := AgentIncidentReport{
		IncidentID:                        incidentID,
		WorkflowID:                        report.WorkflowID,
		TraceID:                           firstNonEmpty(report.TraceID, report.WorkflowID),
		EvidenceSchemaVersion:             report.EvidenceSchemaVersion,
		EvidencePackagePath:               report.EvidencePackagePath,
		EvidencePackageArtifact:           cloneDurableArtifactRef(report.EvidencePackageArtifact),
		Status:                            status,
		Source:                            firstNonEmpty(report.Trigger, "agentic_rca"),
		CollectorID:                       report.CollectorID,
		OpenedAt:                          openedAt,
		RiskLevel:                         riskLevelFromConfidence(confidence),
		RiskScore:                         clamp01(confidence),
		Summary:                           summary,
		MostLikelyCause:                   mostLikely,
		Confidence:                        confidence,
		SynthesizedIncident:               report.SynthesizedIncident,
		Symptoms:                          append([]string{}, report.Anomalies...),
		Timeline:                          timeline,
		Evidence:                          append([]RCAEvidence{}, report.Evidence...),
		NormalizedEvidence:                evidencev1.CloneRecords(report.NormalizedEvidence),
		Hypotheses:                        append([]RCAHypothesis{}, report.Hypotheses...),
		Recommendations:                   append([]WorkflowRecommendation{}, report.Recommendations...),
		ProposedActions:                   append([]ProposedAction{}, report.ProposedActions...),
		AgentLoop:                         report.AgentLoop,
		AnalysisHandoff:                   report.AnalysisHandoff,
		Validation:                        report.Validation,
		MessageManifestPath:               report.MessageManifestPath,
		MessageHistoryArtifact:            cloneDurableArtifactRef(report.MessageHistoryArtifact),
		MessageHistory:                    cloneAgentMessageHistoryRefs(report.MessageHistory),
		LatestAnalysisHandoffMessage:      cloneAgentMessageRef(report.LatestAnalysisHandoffMessage),
		LatestValidationRequestMessage:    cloneAgentMessageRef(report.LatestValidationRequestMessage),
		LatestValidationResultMessage:     cloneAgentMessageRef(report.LatestValidationResultMessage),
		LatestActionDecisionMessage:       cloneAgentMessageRef(report.LatestActionDecisionMessage),
		LatestPostActionValidationMessage: cloneAgentMessageRef(report.LatestPostActionValidationMessage),
		LatestCompensationMessage:         cloneAgentMessageRef(report.LatestCompensationMessage),
		SuspectedRootCauseEntity:          firstNonEmpty(report.SuspectedRootCauseEntity, report.StructuredReport.SuspectedRootCauseEntity),
		CausalPath:                        append([]string{}, report.CausalPath...),
		ImpactScope:                       append([]string{}, report.ImpactScope...),
		ChangeLinks:                       append([]RCAChangeLink{}, report.ChangeLinks...),
		IncidentMemoryMatches:             append([]RetrievedDocumentEvidence{}, report.IncidentMemoryMatches...),
		Artifacts:                         report.Artifacts,
		UnresolvedGaps:                    append([]string{}, report.UnresolvedGaps...),
		SceneClassification:               report.SceneClassification,
		CollectionPlan:                    report.CollectionPlan,
		RecollectionResults:               append([]RecollectionResult{}, report.RecollectionResults...),
		EvidenceGapState:                  report.EvidenceGapState,
		EscalationDecision:                report.EscalationDecision,
		LLMAnalysis:                       report.LLMAnalysis,
		TelemetryQuality:                  report.TelemetryQuality,
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	updated := false
	for i := range e.incidents {
		if strings.EqualFold(e.incidents[i].IncidentID, record.IncidentID) {
			e.incidents[i] = record
			updated = true
			break
		}
	}
	if !updated {
		e.incidents = append([]AgentIncidentReport{record}, e.incidents...)
	}
	if len(e.incidents) > e.cfg.IncidentRetention {
		e.incidents = e.incidents[:e.cfg.IncidentRetention]
	}
}

func (e *WorkflowEngine) audit(
	workflowID, workflowType, stage, action, status, collectorID string,
	dryRun, requiresApproval, approved bool,
	input map[string]string,
	summary string,
	err error,
) {
	detailStatus := strings.TrimSpace(status)
	status = workflowAuditStoredOutcome(status)
	if status == "" {
		status = firstNonEmpty(detailStatus, WorkflowToolOutcomeExecutedSuccess)
	}
	record := WorkflowAuditRecord{
		ID:                newQueryID(),
		TraceID:           workflowID,
		WorkflowID:        workflowID,
		IncidentID:        firstNonEmpty(input["incident_id"], fmt.Sprintf("inc-%s", sanitizeID(workflowID))),
		CorrelationID:     firstNonEmpty(input["correlation_id"], workflowID),
		WorkflowType:      workflowType,
		RuntimeMode:       e.cfg.RuntimeMode,
		Stage:             stage,
		Action:            action,
		Objective:         strings.TrimSpace(input["objective"]),
		CollectorID:       collectorID,
		DryRun:            dryRun,
		RequiresApproval:  requiresApproval,
		Approved:          approved,
		PolicyVerdict:     strings.TrimSpace(input["policy_verdict"]),
		ApprovalState:     strings.TrimSpace(input["approval_state"]),
		ExecutionCategory: executionCategoryFromToolQuery(input),
		ActionIntent:      actionIntentFromToolQuery(input),
		Status:            status,
		Outcome:           status,
		DetailStatus:      detailStatus,
		Input:             cloneStringMap(input),
		OutputSummary:     truncateString(summary, 220),
		Timestamp:         time.Now().UTC(),
	}
	if record.Input != nil {
		record.ToolCallStatus = strings.TrimSpace(record.Input["tool_call_status"])
		record.SelectedSkill = ToolName(strings.TrimSpace(firstNonEmpty(record.Input["selected_skill"], record.Input["tool"])))
		record.Tool = record.SelectedSkill
		record.SelectedScore = strings.TrimSpace(record.Input["selected_score"])
		record.StopReason = strings.TrimSpace(record.Input["stop_reason"])
		record.BranchKind = strings.TrimSpace(record.Input["branch_kind"])
		record.ResultQuality = strings.TrimSpace(record.Input["result_quality"])
		record.LowYield = strings.EqualFold(strings.TrimSpace(record.Input["low_yield"]), "true")
		if raw := strings.TrimSpace(record.Input["candidate_skill_count"]); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
				record.CandidateSkillCount = parsed
			}
		}
		if raw := strings.TrimSpace(record.Input["iteration"]); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
				record.Iteration = parsed
			}
		}
		record.InputArtifactIDs = splitAuditCSV(record.Input["input_artifact_ids"])
		record.OutputArtifactIDs = splitAuditCSV(record.Input["output_artifact_ids"])
		record.EvidenceIDs = splitAuditCSV(record.Input["evidence_ids"])
	}
	if err != nil {
		record.ErrorMessage = truncateString(err.Error(), 220)
		record.ErrorClass = "error"
	}
	normalizeWorkflowAuditRecord(&record)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.audits = append([]WorkflowAuditRecord{record}, e.audits...)
	if len(e.audits) > e.cfg.AuditRetention {
		e.audits = e.audits[:e.cfg.AuditRetention]
	}
	e.logAuditRecord(record)
}

func splitAuditCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return dedupeStrings(out)
}

func (e *WorkflowEngine) insightsStatus() WorkflowInsightsStatus {
	keyEnv := strings.TrimSpace(e.cfg.InsightsAPIKeyEnv)
	keyConfigured := false
	if keyEnv != "" {
		keyConfigured = strings.TrimSpace(os.Getenv(keyEnv)) != ""
	}
	mode := "disabled"
	if e.cfg.InsightsEnabled {
		mode = "active"
		if !keyConfigured {
			mode = "stub"
		}
	}
	return WorkflowInsightsStatus{
		Enabled:          e.cfg.InsightsEnabled,
		Provider:         e.cfg.InsightsProvider,
		Model:            e.cfg.InsightsModel,
		APIKeyEnv:        keyEnv,
		APIKeyConfigured: keyConfigured,
		Mode:             mode,
	}
}

func (e *WorkflowEngine) logWorkflowEvent(level zapcore.Level, event string, fields map[string]any) {
	if e == nil || e.metaLogger == nil {
		return
	}
	zapFields := make([]zap.Field, 0, len(fields)+1)
	zapFields = append(zapFields, zap.String("event", event))
	for key, value := range fields {
		zapFields = append(zapFields, zap.Any(key, value))
	}
	e.metaLogger.Log(level, event, zapFields...)
}

func (e *WorkflowEngine) logAuditRecord(record WorkflowAuditRecord) {
	level := zap.InfoLevel
	if workflowToolOutcomeIsFailure(record.Status) || strings.EqualFold(record.Status, "failed") {
		level = zap.WarnLevel
	}
	fields := map[string]any{
		"trace_id":          record.TraceID,
		"workflow_id":       record.WorkflowID,
		"workflow_type":     record.WorkflowType,
		"stage":             record.Stage,
		"action":            record.Action,
		"collector_id":      record.CollectorID,
		"dry_run":           record.DryRun,
		"requires_approval": record.RequiresApproval,
		"approved":          record.Approved,
		"status":            record.Status,
		"outcome":           record.Outcome,
		"detail_status":     record.DetailStatus,
		"summary":           record.OutputSummary,
	}
	if record.ToolCallStatus != "" {
		fields["tool_call_status"] = record.ToolCallStatus
	}
	if record.ErrorMessage != "" {
		fields["error"] = record.ErrorMessage
	}
	e.logWorkflowEvent(level, "workflow.audit", fields)
}

func (e *WorkflowEngine) recordWorkflowSuccess(workflowType, traceID, collectorID string, started time.Time, fields map[string]any) {
	duration := time.Since(started)
	if e.telemetry != nil {
		e.telemetry.recordWorkflowLatency(workflowType, duration)
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["trace_id"] = traceID
	fields["workflow_type"] = workflowType
	fields["collector_id"] = collectorID
	fields["latency_seconds"] = duration.Seconds()
	e.logWorkflowEvent(zap.InfoLevel, "workflow.completed", fields)
}

func (e *WorkflowEngine) recordWorkflowFailure(traceID, workflowType, collectorID string, started time.Time, err error) {
	duration := time.Since(started)
	if e.telemetry != nil {
		e.telemetry.recordWorkflowLatency(workflowType, duration)
	}
	fields := map[string]any{
		"trace_id":        traceID,
		"workflow_type":   workflowType,
		"collector_id":    collectorID,
		"latency_seconds": duration.Seconds(),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	e.logWorkflowEvent(zap.WarnLevel, "workflow.failed", fields)
}

func latestToolVersion(calls []WorkflowToolCall, tool ToolName) string {
	for idx := len(calls) - 1; idx >= 0; idx-- {
		if calls[idx].Tool == tool {
			return strings.TrimSpace(calls[idx].ToolVersion)
		}
	}
	return ""
}

func remediationActionForHypothesis(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.Contains(title, "cpu"):
		return "scale_or_rebalance_cpu"
	case strings.Contains(title, "memory"):
		return "restart_memory_hotspot_workload"
	case strings.Contains(title, "io"), strings.Contains(title, "storage"):
		return "reduce_io_contention"
	case strings.Contains(title, "network"), strings.Contains(title, "retransmit"):
		return "isolate_network_hotspot"
	case strings.Contains(title, "security"):
		return "lockdown_misconfigured_workload"
	default:
		return "targeted_restart_candidate"
	}
}

func (e *WorkflowEngine) collectorIDs(limit int) []string {
	if e == nil || e.store == nil {
		return nil
	}
	nodes := e.store.Snapshot()
	type row struct {
		id        string
		updatedAt time.Time
	}
	rows := make([]row, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		id := strings.TrimSpace(node.CollectorID)
		if id == "" {
			continue
		}
		rows = append(rows, row{id: id, updatedAt: node.UpdatedAt})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].updatedAt.After(rows[j].updatedAt)
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, 0, len(rows))
	for _, item := range rows {
		out = append(out, item.id)
	}
	return out
}

// ProcessTree returns the runtime process graph.
func (e *WorkflowEngine) ProcessTree() *ProcessTree {
	if e == nil {
		return nil
	}
	return e.processTree
}

// BaselineEngine returns the behavioral baseline engine.
func (e *WorkflowEngine) BaselineEngine() *BaselineEngine {
	if e == nil {
		return nil
	}
	return e.baseline
}

// TraceStore returns the agent trace store.
func (e *WorkflowEngine) TraceStoreRef() *TraceStore {
	if e == nil {
		return nil
	}
	return e.traceStore
}

// ProposedActionStore returns the proposed action store.
func (e *WorkflowEngine) ProposedActionStoreRef() *ProposedActionStore {
	if e == nil {
		return nil
	}
	return e.proposedActions
}

func topFindingScope(scopes []ScopeRisk, collectorID string) string {
	bestScope := ""
	bestEntity := ""
	bestScore := -1.0
	for _, scope := range scopes {
		entity := strings.TrimSpace(scope.Entity)
		if entity == "" || strings.EqualFold(entity, "n/a") {
			continue
		}
		if scope.Score > bestScore {
			bestScore = scope.Score
			bestScope = strings.TrimSpace(scope.Scope)
			bestEntity = entity
		}
	}
	if bestScope != "" && bestEntity != "" {
		return fmt.Sprintf("%s/%s", bestScope, bestEntity)
	}
	if strings.TrimSpace(collectorID) != "" {
		return "node/" + collectorID
	}
	return "node/fleet"
}

func topHypothesisTitle(hypotheses []RCAHypothesis) string {
	if len(hypotheses) == 0 {
		return ""
	}
	return strings.TrimSpace(hypotheses[0].Title)
}

func topTriggeredSignalNames(signals []JointRiskSignal, limit int) []string {
	items := topTriggeredSignals(signals, limit)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name)
	}
	return dedupeStrings(out)
}

func riskLevelFromConfidence(confidence float64) string {
	switch {
	case confidence >= 0.72:
		return "high"
	case confidence >= 0.45:
		return "medium"
	default:
		return "low"
	}
}

func hasBehavioralAnomaly(signals []JointRiskSignal) bool {
	for _, signal := range signals {
		if !signal.Triggered {
			continue
		}
		id := strings.ToLower(signal.ID)
		if strings.Contains(id, "ebpf") || strings.Contains(id, "security") || strings.Contains(id, "behavior") {
			return true
		}
	}
	return false
}

func hasResourceAnomaly(signals []JointRiskSignal) bool {
	for _, signal := range signals {
		if !signal.Triggered {
			continue
		}
		id := strings.ToLower(signal.ID)
		if strings.Contains(id, "cpu") ||
			strings.Contains(id, "memory") ||
			strings.Contains(id, "io") ||
			strings.Contains(id, "retransmit") ||
			strings.Contains(id, "softnet") {
			return true
		}
	}
	return false
}

func (e *WorkflowEngine) shouldEscalateFromJointRisk(report JointRiskAssessment) bool {
	if e == nil || !e.cfg.AutoEscalateOnHighRisk {
		return false
	}
	if !strings.EqualFold(report.RiskLevel, "high") {
		return false
	}
	if report.RiskScore < e.cfg.HighRiskScoreThreshold {
		return false
	}
	collectorID := strings.TrimSpace(report.CollectorID)
	if collectorID == "" {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, incident := range e.incidents {
		if !strings.EqualFold(incident.CollectorID, collectorID) {
			continue
		}
		if !strings.EqualFold(incident.Status, "open") {
			continue
		}
		if time.Since(incident.OpenedAt) <= 15*time.Minute {
			return false
		}
	}
	return true
}

func buildRiskSeries(history []ingest.MetricHistorySample, logs logsToolData) []RiskSeries {
	if len(history) == 0 {
		return []RiskSeries{}
	}
	profiles := riskSignalProfiles()
	specs := riskSeriesSpecs()
	out := make([]RiskSeries, 0, len(specs)+1)
	for _, item := range specs {
		points := make([]RiskSeriesPoint, 0, len(history))
		for _, sample := range history {
			value, ok := item.extract(sample.Metrics)
			if !ok {
				continue
			}
			points = append(points, RiskSeriesPoint{Timestamp: sample.Timestamp, Value: value})
		}
		if len(points) < 3 {
			continue
		}
		baseline := baselineValue(points)
		latest := points[len(points)-1].Value
		acc := acceleration(points)
		deltaPct := percentChange(baseline, latest)
		slope := averageSlopePerMinute(points)
		profile := profiles[item.key]
		breaches := thresholdBreaches(points, profile.medium)
		persistence := trailingPersistence(points, profile.medium, baseline)
		trend, triggered := classifySeriesTrend(latest, baseline, slope, acc, breaches, persistence, profile)
		out = append(out, RiskSeries{
			Key:               item.key,
			Display:           item.display,
			Unit:              item.unit,
			Category:          item.category,
			Latest:            latest,
			Baseline:          baseline,
			DeltaPercent:      deltaPct,
			SlopePerMinute:    slope,
			Acceleration:      acc,
			ThresholdBreaches: breaches,
			PersistencePoints: persistence,
			Trend:             trend,
			Triggered:         triggered,
			ThresholdValue:    profile.high,
			Points:            points,
		})
	}

	if len(logs.Timeline) > 0 {
		points := make([]RiskSeriesPoint, 0, len(logs.Timeline))
		for _, bucket := range logs.Timeline {
			value := float64(bucket.Errors + bucket.Warnings)
			points = append(points, RiskSeriesPoint{Timestamp: bucket.End, Value: value})
		}
		if len(points) >= 3 {
			baseline := baselineValue(points)
			latest := points[len(points)-1].Value
			acc := acceleration(points)
			deltaPct := percentChange(baseline, latest)
			slope := averageSlopePerMinute(points)
			profile := profiles["log_burst"]
			breaches := thresholdBreaches(points, profile.medium)
			persistence := trailingPersistence(points, profile.medium, baseline)
			trend, triggered := classifySeriesTrend(latest, baseline, slope, acc, breaches, persistence, profile)
			out = append(out, RiskSeries{
				Key:               "log_burst",
				Display:           "Error/warn burst",
				Unit:              "count",
				Category:          "service",
				Latest:            latest,
				Baseline:          baseline,
				DeltaPercent:      deltaPct,
				SlopePerMinute:    slope,
				Acceleration:      acc,
				ThresholdBreaches: breaches,
				PersistencePoints: persistence,
				Trend:             trend,
				Triggered:         triggered,
				ThresholdValue:    profile.high,
				Points:            points,
			})
		}
	}

	if memorySeries := findSeriesByKey(out, "memory_pressure"); memorySeries != nil &&
		len(memorySeries.Points) >= 4 &&
		(memorySeries.Latest >= 60 || math.Abs(memorySeries.DeltaPercent) >= 12) {
		ratePoints := make([]RiskSeriesPoint, 0, len(memorySeries.Points)-1)
		for i := 1; i < len(memorySeries.Points); i++ {
			prev := memorySeries.Points[i-1]
			curr := memorySeries.Points[i]
			dtMin := curr.Timestamp.Sub(prev.Timestamp).Minutes()
			if dtMin <= 0 {
				continue
			}
			rate := (curr.Value - prev.Value) / dtMin
			if rate < 0 {
				rate = 0
			}
			ratePoints = append(ratePoints, RiskSeriesPoint{
				Timestamp: curr.Timestamp,
				Value:     rate,
			})
		}
		if len(ratePoints) >= 3 {
			baseline := baselineValue(ratePoints)
			latest := ratePoints[len(ratePoints)-1].Value
			acc := acceleration(ratePoints)
			deltaPct := percentChange(baseline, latest)
			slope := averageSlopePerMinute(ratePoints)
			profile := profiles["memory_leak_rate"]
			breaches := thresholdBreaches(ratePoints, profile.medium)
			persistence := trailingPersistence(ratePoints, profile.medium, baseline)
			trend, triggered := classifySeriesTrend(latest, baseline, slope, acc, breaches, persistence, profile)
			out = append(out, RiskSeries{
				Key:               "memory_leak_rate",
				Display:           "Memory leak rate",
				Unit:              "percent_per_minute",
				Category:          "runtime",
				Latest:            latest,
				Baseline:          baseline,
				DeltaPercent:      deltaPct,
				SlopePerMinute:    slope,
				Acceleration:      acc,
				ThresholdBreaches: breaches,
				PersistencePoints: persistence,
				Trend:             trend,
				Triggered:         triggered,
				ThresholdValue:    profile.high,
				Points:            ratePoints,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func buildRiskSignals(collectorID string, series []RiskSeries, security securityToolData, ebpf ebpfToolData, behavioral map[string]BehavioralSignalAssessment) []JointRiskSignal {
	thresholds := riskSignalProfiles()
	out := make([]JointRiskSignal, 0, len(series)+1)
	for _, item := range series {
		t, ok := thresholds[item.Key]
		if !ok {
			continue
		}
		thrScore := thresholdScore(item.Latest, t.medium, t.high)
		deltaPct := percentChange(item.Baseline, item.Latest)
		deltaScore := clamp01(deltaPct / 60.0)
		accScore := clamp01(item.Acceleration / maxFloat(math.Abs(item.Baseline)*0.15, 1.0))
		rawScore := t.weight * (0.55*thrScore + 0.30*deltaScore + 0.15*accScore)
		score := rawScore
		triggered := item.Triggered || thrScore > 0 || deltaScore >= 0.4
		classification := ""
		suppressionFactor := 0.0
		suppressionReason := ""
		if assessment, ok := behavioral[item.Key]; ok {
			classification = assessment.Classification
			suppressionFactor = assessment.SuppressionFactor
			suppressionReason = assessment.Explanation
			switch assessment.Classification {
			case "expected_recurring_burst":
				score = rawScore * (1 - assessment.SuppressionFactor)
				if !hasCorroboratingIncidentEvidence(assessment.CrossSignalSupport) {
					triggered = false
				}
			case "correlated_anomaly":
				score = clamp01(rawScore + t.weight*0.08*assessment.Confidence)
				triggered = true
			case "confirmed_anomaly":
				score = clamp01(rawScore + t.weight*0.12*assessment.Confidence)
				triggered = true
			}
		}
		sev := signalSeverity(score, t.weight)
		evidence := []string{
			fmt.Sprintf("latest=%.3f baseline=%.3f", item.Latest, item.Baseline),
			fmt.Sprintf("delta=%.1f%% slope=%.3f/min acceleration=%.3f", deltaPct, item.SlopePerMinute, item.Acceleration),
		}
		if suppressionReason != "" {
			evidence = append(evidence, suppressionReason)
		}
		lastTS := item.Points[len(item.Points)-1].Timestamp
		out = append(out, JointRiskSignal{
			ID:                       item.Key,
			Name:                     item.Display,
			Scope:                    t.scope,
			Entity:                   firstNonEmpty(collectorID, "fleet"),
			Severity:                 sev,
			Weight:                   t.weight,
			Current:                  item.Latest,
			Baseline:                 item.Baseline,
			DeltaPercent:             deltaPct,
			Acceleration:             item.Acceleration,
			OriginalScore:            rawScore,
			Score:                    score,
			Triggered:                triggered,
			BehavioralClassification: classification,
			SuppressionFactor:        suppressionFactor,
			SuppressionReason:        suppressionReason,
			Evidence:                 evidence,
			LastObservedAt:           lastTS,
		})
	}

	if security.Score > 0 {
		delta := security.Score * 100
		triggered := security.Score >= 0.2
		evidence := append([]string{}, security.Findings...)
		evidence = append(evidence, security.SuspiciousPortCandidates...)
		evidence = append(evidence, security.WeakPermissionHints...)
		if len(evidence) > 5 {
			evidence = evidence[:5]
		}
		out = append(out, JointRiskSignal{
			ID:             "security_exposure",
			Name:           "Security exposure",
			Scope:          "node",
			Entity:         firstNonEmpty(collectorID, "fleet"),
			Severity:       signalSeverity(security.Score*0.08, 0.08),
			Weight:         0.08,
			Current:        security.Score,
			Baseline:       0,
			DeltaPercent:   delta,
			Acceleration:   0,
			Score:          clamp01(security.Score * 0.08),
			Triggered:      triggered,
			Evidence:       evidence,
			LastObservedAt: time.Now().UTC(),
		})
	}

	if ebpf.BehaviorScore > 0 {
		evidence := append([]string{}, ebpf.RuntimeEventSummaries...)
		evidence = append(evidence, ebpf.EvidenceIDs...)
		if len(evidence) > 8 {
			evidence = evidence[:8]
		}
		weight := 0.16
		rawScore := clamp01(ebpf.BehaviorScore * weight)
		score := rawScore
		triggered := ebpf.BehaviorScore >= 0.25 || ebpf.EventRate >= 2
		classification := ""
		suppressionFactor := 0.0
		suppressionReason := ""
		if assessment, ok := behavioral["ebpf_behavior_anomaly"]; ok {
			classification = assessment.Classification
			suppressionFactor = assessment.SuppressionFactor
			suppressionReason = assessment.Explanation
			switch assessment.Classification {
			case "expected_recurring_burst":
				score = rawScore * (1 - assessment.SuppressionFactor)
				if !hasCorroboratingIncidentEvidence(assessment.CrossSignalSupport) {
					triggered = false
				}
			case "correlated_anomaly":
				score = clamp01(rawScore + weight*0.08*assessment.Confidence)
				triggered = true
			case "confirmed_anomaly":
				score = clamp01(rawScore + weight*0.12*assessment.Confidence)
				triggered = true
			}
			evidence = append(evidence, suppressionReason)
		}
		out = append(out, JointRiskSignal{
			ID:                       "ebpf_behavior_anomaly",
			Name:                     "eBPF behavior anomaly",
			Scope:                    "runtime",
			Entity:                   firstNonEmpty(collectorID, "fleet"),
			Severity:                 signalSeverity(score, weight),
			Weight:                   weight,
			Current:                  ebpf.BehaviorScore,
			Baseline:                 0.1,
			DeltaPercent:             percentChange(0.1, ebpf.BehaviorScore),
			Acceleration:             ebpf.EventRate,
			OriginalScore:            rawScore,
			Score:                    score,
			Triggered:                triggered,
			BehavioralClassification: classification,
			SuppressionFactor:        suppressionFactor,
			SuppressionReason:        suppressionReason,
			Evidence:                 evidence,
			LastObservedAt:           time.Now().UTC(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func buildScopeRisks(data metricsToolData, signals []JointRiskSignal) []ScopeRisk {
	out := make([]ScopeRisk, 0, 12)
	nodeEntity := firstNonEmpty(data.CollectorID, "fleet")
	nodeScore := 0.0
	nodeSignals := make([]string, 0, 6)
	for _, signal := range signals {
		if signal.Scope == "node" {
			nodeScore += signal.Score
			if signal.Triggered {
				nodeSignals = append(nodeSignals, signal.Name)
			}
		}
	}
	out = append(out, ScopeRisk{
		Scope:       "node",
		Entity:      nodeEntity,
		Score:       clamp01(nodeScore),
		TopSignals:  dedupeStrings(nodeSignals),
		Explanation: "node-level weighted risk from CPU/memory/IO/network/security signals",
	})

	processAdded := false
	if data.Node != nil {
		for _, process := range topProcessResources(data.Node, 4) {
			score := clamp01(processPressureScore(process) / 100.0)
			signals := []string{}
			for category, total := range process.CategoryTotals {
				if total > 0 {
					signals = append(signals, category)
				}
			}
			if process.LogErrors > 0 || process.LogWarnings > 0 {
				signals = append(signals, "logs")
			}
			out = append(out, ScopeRisk{
				Scope:       "process",
				Entity:      processDisplayName(process),
				Score:       score,
				TopSignals:  dedupeStrings(signals),
				Explanation: "process-level attribution from per-process CPU/IO/network/log counters",
			})
			processAdded = true
		}
	}
	if !processAdded {
		out = append(out, ScopeRisk{Scope: "process", Entity: "n/a", Score: 0, Explanation: "no process attribution available"})
	}

	podScores := aggregateScopeFromProcesses(data.Node, func(p *ingest.ProcessResourceSample) string {
		return strings.TrimSpace(p.PodUID)
	})
	if len(podScores) == 0 {
		out = append(out, ScopeRisk{Scope: "pod", Entity: "n/a", Score: 0, Explanation: "pod attribution unavailable"})
	} else {
		for _, row := range podScores {
			out = append(out, row)
		}
	}

	serviceScores := aggregateScopeFromProcesses(data.Node, func(p *ingest.ProcessResourceSample) string {
		return firstNonEmpty(strings.TrimSpace(p.Job), strings.TrimSpace(p.Name))
	})
	if len(serviceScores) == 0 {
		out = append(out, ScopeRisk{Scope: "service", Entity: "n/a", Score: 0, Explanation: "service attribution unavailable"})
	} else {
		for _, row := range serviceScores {
			row.Scope = "service"
			out = append(out, row)
		}
	}

	if len(data.Fleet) > 0 {
		clusterScore := 0.0
		hotNodes := []string{}
		for _, node := range data.Fleet {
			if node == nil {
				continue
			}
			score := simpleNodePressureScore(node.Metrics)
			clusterScore += score
			if score >= 0.45 {
				hotNodes = append(hotNodes, firstNonEmpty(node.CollectorID, node.Hostname))
			}
		}
		clusterScore = clusterScore / maxFloat(float64(len(data.Fleet)), 1)
		out = append(out, ScopeRisk{
			Scope:       "cluster",
			Entity:      "fleet",
			Score:       clamp01(clusterScore),
			TopSignals:  hotNodes,
			Explanation: "cluster risk aggregated across node pressure snapshots",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope == out[j].Scope {
			return out[i].Score > out[j].Score
		}
		return out[i].Scope < out[j].Scope
	})
	if len(out) > 18 {
		out = out[:18]
	}
	return out
}

func aggregateScopeFromProcesses(node *ingest.NodeSnapshot, keyFn func(*ingest.ProcessResourceSample) string) []ScopeRisk {
	if node == nil || len(node.ProcessResources) == 0 || keyFn == nil {
		return nil
	}
	totals := map[string]float64{}
	counts := map[string]int{}
	signals := map[string][]string{}
	for _, process := range node.ProcessResources {
		if process == nil {
			continue
		}
		key := strings.TrimSpace(keyFn(process))
		if key == "" {
			continue
		}
		score := processPressureScore(process) / 100
		totals[key] += score
		counts[key]++
		for category, value := range process.CategoryTotals {
			if value > 0 {
				signals[key] = append(signals[key], category)
			}
		}
	}
	rows := make([]ScopeRisk, 0, len(totals))
	for entity, total := range totals {
		count := counts[entity]
		score := total / maxFloat(float64(count), 1)
		rows = append(rows, ScopeRisk{
			Scope:       "pod",
			Entity:      entity,
			Score:       clamp01(score),
			TopSignals:  dedupeStrings(signals[entity]),
			Explanation: "aggregated from attributed process signals",
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].Entity < rows[j].Entity
		}
		return rows[i].Score > rows[j].Score
	})
	if len(rows) > 4 {
		rows = rows[:4]
	}
	return rows
}

func buildCooccurrences(signals []JointRiskSignal, series []RiskSeries, window time.Duration, collectorID string) []JointRiskCooccurrence {
	active := topTriggeredSignals(signals, 8)
	if len(active) < 2 {
		return nil
	}
	valuesByKey := map[string][]float64{}
	for _, item := range series {
		vals := make([]float64, 0, len(item.Points))
		for _, point := range item.Points {
			vals = append(vals, point.Value)
		}
		valuesByKey[item.Key] = vals
	}

	out := make([]JointRiskCooccurrence, 0, 8)
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			a := active[i]
			b := active[j]
			corr := pairCorrelation(valuesByKey[a.ID], valuesByKey[b.ID])
			if math.Abs(corr) < 0.35 {
				corr = fallbackSignalCorrelation(a, b)
			}
			if math.Abs(corr) < 0.30 {
				continue
			}
			combined := clamp01((a.Score + b.Score) * (1 + math.Abs(corr)*0.25))
			out = append(out, JointRiskCooccurrence{
				ID:            fmt.Sprintf("co-%s-%s", sanitizeID(a.ID), sanitizeID(b.ID)),
				Scope:         "node",
				Entity:        firstNonEmpty(collectorID, "fleet"),
				Window:        window.String(),
				Signals:       []string{a.Name, b.Name},
				Correlation:   corr,
				CombinedScore: combined,
				Explanation: fmt.Sprintf("%s and %s co-move in the same risk window (corr=%.2f)",
					a.Name, b.Name, corr),
				ActionableCause: fmt.Sprintf("combined risk is high because signals %s+%s co-occurred within window %s on scope node/%s",
					a.Name, b.Name, window.String(), firstNonEmpty(collectorID, "fleet")),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CombinedScore == out[j].CombinedScore {
			return out[i].ID < out[j].ID
		}
		return out[i].CombinedScore > out[j].CombinedScore
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func buildFallbackCooccurrence(signals []JointRiskSignal, window time.Duration, collectorID string) []JointRiskCooccurrence {
	active := topTriggeredSignals(signals, 3)
	if len(active) < 2 {
		return nil
	}
	names := make([]string, 0, len(active))
	total := 0.0
	for _, signal := range active {
		names = append(names, signal.Name)
		total += signal.Score
	}
	return []JointRiskCooccurrence{{
		ID:            "co-fallback",
		Scope:         "node",
		Entity:        firstNonEmpty(collectorID, "fleet"),
		Window:        window.String(),
		Signals:       names,
		Correlation:   0.3,
		CombinedScore: clamp01(total),
		Explanation:   "multiple low-severity signals were active in the same window",
		ActionableCause: fmt.Sprintf("combined risk is elevated because signals %s co-occurred within window %s on scope node/%s",
			strings.Join(names, "+"), window.String(), firstNonEmpty(collectorID, "fleet")),
	}}
}

func topTriggeredSignals(signals []JointRiskSignal, limit int) []JointRiskSignal {
	rows := make([]JointRiskSignal, 0, len(signals))
	for _, signal := range signals {
		if signal.Triggered {
			rows = append(rows, signal)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Score > rows[j].Score
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func topRiskSignals(signals []JointRiskSignal, limit int) []JointRiskSignal {
	rows := append([]JointRiskSignal(nil), signals...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Score > rows[j].Score
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func topMetricMap(metrics map[string]float64, limit int) map[string]float64 {
	if len(metrics) == 0 {
		return map[string]float64{}
	}
	type kv struct {
		key   string
		value float64
	}
	rows := make([]kv, 0, len(metrics))
	for key, value := range metrics {
		rows = append(rows, kv{key: key, value: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		return math.Abs(rows[i].value) > math.Abs(rows[j].value)
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		out[row.key] = row.value
	}
	return out
}

func assignEvidenceToHypotheses(hypotheses []RCAHypothesis, evidence []RCAEvidence) {
	for idx := range hypotheses {
		ids := make([]string, 0, 6)
		contradicting := make([]string, 0, 4)
		title := strings.ToLower(hypotheses[idx].Title)
		for _, item := range evidence {
			summary := strings.ToLower(item.Summary)
			snippet := strings.ToLower(item.Snippet)
			metric := strings.ToLower(item.MetricName)
			domainMatch := false
			if strings.Contains(summary, title) || strings.Contains(snippet, title) || strings.Contains(metric, title) {
				if item.Kind == "near_baseline_signal" {
					contradicting = append(contradicting, item.ID)
				} else {
					ids = append(ids, item.ID)
				}
				continue
			}
			if strings.Contains(title, "cpu") && strings.Contains(metric, "cpu") {
				domainMatch = true
			} else if strings.Contains(title, "memory") && strings.Contains(metric, "memory") {
				domainMatch = true
			} else if strings.Contains(title, "io") && (strings.Contains(metric, "io") || strings.Contains(metric, "disk")) {
				domainMatch = true
			} else if strings.Contains(title, "network") && (strings.Contains(metric, "net") || strings.Contains(metric, "retransmit")) {
				domainMatch = true
			} else if strings.Contains(title, "gpu") && strings.Contains(metric, "gpu") {
				domainMatch = true
			} else if strings.Contains(title, "deploy") && strings.Contains(snippet, "deploy") {
				domainMatch = true
			} else if strings.Contains(title, "security") && strings.Contains(summary, "security") {
				domainMatch = true
			}
			if domainMatch && item.Kind == "near_baseline_signal" {
				contradicting = append(contradicting, item.ID)
			} else if domainMatch {
				ids = append(ids, item.ID)
			}
		}
		if len(ids) > 0 {
			hypotheses[idx].EvidenceIDs = dedupeStrings(ids)
		}
		if len(contradicting) > 0 {
			hypotheses[idx].ContradictingEvidenceIDs = dedupeStrings(contradicting)
		}
	}
}

func dedupeRecommendations(in []WorkflowRecommendation) []WorkflowRecommendation {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]WorkflowRecommendation, 0, len(in))
	for _, item := range in {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = strings.TrimSpace(item.Summary)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func hypothesisTitleFromSignal(signal string) string {
	low := strings.ToLower(signal)
	switch {
	case strings.Contains(low, "cpu"):
		return "cpu scheduling contention"
	case strings.Contains(low, "memory"):
		return "memory pressure and reclaim"
	case strings.Contains(low, "latency") || strings.Contains(low, "io"):
		return "storage io bottleneck"
	case strings.Contains(low, "retransmit") || strings.Contains(low, "softnet"):
		return "network congestion or packet loss"
	case strings.Contains(low, "log"):
		return "service-level error burst"
	case strings.Contains(low, "security"):
		return "security exposure or permission drift"
	default:
		return "distributed resource contention"
	}
}

func checksForHypothesis(title string) []string {
	low := strings.ToLower(title)
	switch {
	case strings.Contains(low, "cpu"):
		return []string{"inspect run queue and blocked tasks", "verify hottest processes and throttling", "compare recent deployment CPU requests"}
	case strings.Contains(low, "memory"):
		return []string{"inspect top RSS processes", "check swap/oom counters", "verify memory limits and working set"}
	case strings.Contains(low, "storage") || strings.Contains(low, "io"):
		return []string{"inspect disk queue depth and latency", "identify io-heavy processes", "check writeback/page-cache pressure"}
	case strings.Contains(low, "network"):
		return []string{"inspect retransmit/drop counters", "validate pod/service topology hops", "check recent connection churn"}
	case strings.Contains(low, "deploy"):
		return []string{"verify rollout status", "compare version-specific errors", "consider canary rollback in dry-run"}
	case strings.Contains(low, "security"):
		return []string{"review suspicious open ports", "verify permissions and policy changes", "check audit log anomalies"}
	default:
		return []string{"extend evidence window", "collect process + kernel signals", "validate topology blast radius"}
	}
}

func priorityForSeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "high", "critical":
		return "high"
	case "medium", "warning":
		return "medium"
	default:
		return "low"
	}
}

func priorityForConfidence(confidence float64) string {
	switch {
	case confidence >= 0.75:
		return "high"
	case confidence >= 0.45:
		return "medium"
	default:
		return "low"
	}
}

func signalSeverity(score, weight float64) string {
	normalized := 0.0
	if weight > 0 {
		normalized = score / weight
	}
	switch {
	case normalized >= 0.75:
		return "high"
	case normalized >= 0.35:
		return "medium"
	default:
		return "low"
	}
}

func simpleNodePressureScore(metrics map[string]float64) float64 {
	cpu := metricValue(metrics, "node_cpu_usage_percent")
	mem := 0.0
	if total := metricValue(metrics, "node_memory_MemTotal_bytes"); total > 0 {
		used := metricValue(metrics, "node_memory_Used_bytes")
		if used <= 0 {
			avail := metricValue(metrics, "node_memory_MemAvailable_bytes")
			if avail > 0 {
				used = total - avail
			}
		}
		if used > 0 {
			mem = clampPercent(used / total * 100)
		}
	}
	ioLat := metricValue(metrics, "node_disk_request_latency_p99_seconds") * 1000
	retrans := metricValue(metrics, "node_tcp_retransmit_ratio")
	return clamp01(
		0.30*thresholdScore(cpu, 65, 88) +
			0.26*thresholdScore(mem, 72, 90) +
			0.24*thresholdScore(ioLat, 20, 80) +
			0.20*thresholdScore(retrans, 0.005, 0.02),
	)
}

func baselineValue(points []RiskSeriesPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	split := len(points) * 2 / 3
	if split <= 1 {
		split = len(points) - 1
	}
	if split <= 0 {
		return points[0].Value
	}
	sum := 0.0
	for i := 0; i < split; i++ {
		sum += points[i].Value
	}
	return sum / float64(split)
}

func acceleration(points []RiskSeriesPoint) float64 {
	if len(points) < 3 {
		return 0
	}
	last := points[len(points)-1]
	prev := points[len(points)-2]
	before := points[len(points)-3]
	step1 := last.Value - prev.Value
	step0 := prev.Value - before.Value
	return step1 - step0
}

func findSeriesByKey(series []RiskSeries, key string) *RiskSeries {
	for i := range series {
		if strings.EqualFold(strings.TrimSpace(series[i].Key), strings.TrimSpace(key)) {
			return &series[i]
		}
	}
	return nil
}

func pairCorrelation(a, b []float64) float64 {
	n := minInt(len(a), len(b))
	if n < 3 {
		return 0
	}
	a = a[len(a)-n:]
	b = b[len(b)-n:]

	meanA := 0.0
	meanB := 0.0
	for i := 0; i < n; i++ {
		meanA += a[i]
		meanB += b[i]
	}
	meanA /= float64(n)
	meanB /= float64(n)

	num := 0.0
	denA := 0.0
	denB := 0.0
	for i := 0; i < n; i++ {
		da := a[i] - meanA
		db := b[i] - meanB
		num += da * db
		denA += da * da
		denB += db * db
	}
	if denA <= 0 || denB <= 0 {
		return 0
	}
	return num / math.Sqrt(denA*denB)
}

func fallbackSignalCorrelation(a, b JointRiskSignal) float64 {
	if a.Triggered && b.Triggered {
		sameDirection := 1.0
		if (a.DeltaPercent < 0 && b.DeltaPercent > 0) || (a.DeltaPercent > 0 && b.DeltaPercent < 0) {
			sameDirection = -1.0
		}
		return 0.35 * sameDirection
	}
	return 0
}

func extractMetric(name string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		v, ok := metrics[name]
		return v, ok
	}
}

func extractMemoryPercent(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	if v, ok := metrics["memory_used_percent"]; ok {
		return v, true
	}
	total, ok := metrics["node_memory_MemTotal_bytes"]
	if !ok || total <= 0 {
		return 0, false
	}
	if used, ok := metrics["node_memory_Used_bytes"]; ok {
		return clampPercent(used / total * 100), true
	}
	if avail, ok := metrics["node_memory_MemAvailable_bytes"]; ok {
		used := total - avail
		if used < 0 {
			used = 0
		}
		return clampPercent(used / total * 100), true
	}
	return 0, false
}

func extractIOLatencyMS(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	if v, ok := metrics["node_disk_request_latency_p99_seconds"]; ok {
		return v * 1000, true
	}
	if v, ok := metrics["node_disk_avg_request_latency_seconds"]; ok {
		return v * 1000, true
	}
	if v, ok := metrics["probe_core_disk_await_ms"]; ok {
		return v, true
	}
	return 0, false
}

func extractNetworkThroughputBPS(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	rx := 0.0
	tx := 0.0
	ok := false
	for _, key := range []string{
		"node_network_total_receive_bytes_per_second",
		"node_network_receive_bytes_per_second",
	} {
		if value, exists := metrics[key]; exists {
			rx = value
			ok = true
			break
		}
	}
	for _, key := range []string{
		"node_network_total_transmit_bytes_per_second",
		"node_network_transmit_bytes_per_second",
	} {
		if value, exists := metrics[key]; exists {
			tx = value
			ok = true
			break
		}
	}
	if !ok {
		return 0, false
	}
	return rx + tx, true
}

func thresholdScore(value, medium, high float64) float64 {
	if high <= medium {
		if value >= high {
			return 1
		}
		return 0
	}
	if value <= medium {
		return 0
	}
	if value >= high {
		return 1
	}
	return (value - medium) / (high - medium)
}

func percentChange(base, current float64) float64 {
	if math.Abs(base) < 1e-9 {
		if math.Abs(current) < 1e-9 {
			return 0
		}
		if current > 0 {
			return 100
		}
		return -100
	}
	return (current - base) / math.Abs(base) * 100
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseBool(raw string, fallback bool) bool {
	norm := strings.ToLower(strings.TrimSpace(raw))
	switch norm {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func parseFloat(raw string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return value
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}
