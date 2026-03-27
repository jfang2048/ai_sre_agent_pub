package eval

import "time"

// Scope controls which evaluation subsets are executed.
type Scope string

const (
	ScopeFast       Scope = "fast"
	ScopeRegression Scope = "regression"
	ScopeBenchmark  Scope = "benchmark"
)

// RetrievalCaseFile is the on-disk schema for retrieval evaluation inputs.
type RetrievalCaseFile struct {
	SchemaVersion string          `json:"schema_version"`
	Cases         []RetrievalCase `json:"cases"`
}

// RetrievalCase defines a deterministic retrieval evaluation query.
type RetrievalCase struct {
	ID                     string   `json:"id"`
	Suites                 []string `json:"suites"`
	Description            string   `json:"description"`
	Query                  string   `json:"query"`
	NoisyQuery             string   `json:"noisy_query,omitempty"`
	Intent                 string   `json:"intent,omitempty"`
	TopK                   int      `json:"top_k,omitempty"`
	ExpectedPaths          []string `json:"expected_paths"`
	ExpectedKnowledgeTypes []string `json:"expected_knowledge_types,omitempty"`
	ExpectedCaseTypes      []string `json:"expected_case_types,omitempty"`
	ExpectedSignals        []string `json:"expected_signals,omitempty"`
	MinRecallAtK           float64  `json:"min_recall_at_k,omitempty"`
	MinPrecisionAtK        float64  `json:"min_precision_at_k,omitempty"`
	MinNoisyRecallAtK      float64  `json:"min_noisy_recall_at_k,omitempty"`
}

// IncidentCaseFile is the on-disk schema for workflow-level evaluation inputs.
type IncidentCaseFile struct {
	SchemaVersion string         `json:"schema_version"`
	Cases         []IncidentCase `json:"cases"`
}

// AnomalyCaseFile is the on-disk schema for anomaly-classification evaluation inputs.
type AnomalyCaseFile struct {
	SchemaVersion string        `json:"schema_version"`
	Cases         []AnomalyCase `json:"cases"`
}

// IncidentCase defines one end-to-end workflow scenario.
type IncidentCase struct {
	ID            string               `json:"id"`
	Suites        []string             `json:"suites"`
	Description   string               `json:"description"`
	CollectorID   string               `json:"collector_id"`
	Query         string               `json:"query"`
	Trigger       string               `json:"trigger"`
	WindowMinutes int                  `json:"window_minutes"`
	Scenario      TelemetryScenario    `json:"scenario"`
	Expected      IncidentExpectations `json:"expected"`
}

// AnomalyCase defines one anomaly-classification scenario.
type AnomalyCase struct {
	ID            string               `json:"id"`
	Suites        []string             `json:"suites"`
	Category      string               `json:"category"`
	Description   string               `json:"description"`
	CollectorID   string               `json:"collector_id"`
	Service       string               `json:"service"`
	SignalID      string               `json:"signal_id"`
	WindowMinutes int                  `json:"window_minutes"`
	Scenario      TelemetryScenario    `json:"scenario"`
	History       []HistoricalScenario `json:"history,omitempty"`
	Peers         []PeerScenario       `json:"peers,omitempty"`
	Expected      AnomalyExpectations  `json:"expected"`
}

// TelemetryScenario defines synthetic telemetry generation for one collector.
type TelemetryScenario struct {
	DurationMinutes int                `json:"duration_minutes"`
	StepMinutes     int                `json:"step_minutes"`
	MetricSeries    []MetricSeriesSpec `json:"metric_series"`
	Processes       []ProcessFixture   `json:"processes,omitempty"`
	Logs            []LogSeriesSpec    `json:"logs,omitempty"`
}

// HistoricalScenario repeats one telemetry pattern into the past.
type HistoricalScenario struct {
	Repeat     int               `json:"repeat,omitempty"`
	EveryHours int               `json:"every_hours,omitempty"`
	Scenario   TelemetryScenario `json:"scenario"`
}

// PeerScenario seeds a same-service or neighboring collector into the current fleet snapshot.
type PeerScenario struct {
	CollectorID string            `json:"collector_id"`
	Service     string            `json:"service"`
	Scenario    TelemetryScenario `json:"scenario"`
}

// MetricSeriesSpec defines one synthetic time series.
type MetricSeriesSpec struct {
	Name     string    `json:"name"`
	Mode     string    `json:"mode,omitempty"`
	Value    float64   `json:"value,omitempty"`
	Start    float64   `json:"start,omitempty"`
	Step     float64   `json:"step,omitempty"`
	Min      float64   `json:"min,omitempty"`
	Max      float64   `json:"max,omitempty"`
	Sequence []float64 `json:"sequence,omitempty"`
}

// ProcessFixture seeds process attribution data.
type ProcessFixture struct {
	PID       int32   `json:"pid"`
	Name      string  `json:"name"`
	CPU       float64 `json:"cpu_percent"`
	RSSBytes  uint64  `json:"rss_bytes"`
	IOReadBPS float64 `json:"io_read_bps"`
	IOWrtBPS  float64 `json:"io_write_bps"`
}

// LogSeriesSpec seeds deterministic log fingerprints and raw log-index events.
type LogSeriesSpec struct {
	Every       int    `json:"every"`
	Offset      int    `json:"offset,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Example     string `json:"example"`
	Level       string `json:"level,omitempty"`
	Service     string `json:"service,omitempty"`
	Process     string `json:"process,omitempty"`
	CountStart  uint64 `json:"count_start,omitempty"`
	CountStep   uint64 `json:"count_step,omitempty"`
}

// IncidentExpectations declares the golden behavior for one workflow case.
type IncidentExpectations struct {
	ExpectedTrendKeys                 []string `json:"expected_trend_keys,omitempty"`
	ExpectedEventCategories           []string `json:"expected_event_categories,omitempty"`
	ExpectedToolCalls                 []string `json:"expected_tool_calls,omitempty"`
	RootCauseAny                      []string `json:"root_cause_any,omitempty"`
	FaultDomains                      []string `json:"fault_domains,omitempty"`
	RequiredEvidence                  []string `json:"required_evidence,omitempty"`
	RequiredRecommendationSubstrings  []string `json:"required_recommendation_substrings,omitempty"`
	ForbiddenRecommendationSubstrings []string `json:"forbidden_recommendation_substrings,omitempty"`
	RequiredRetrievalPaths            []string `json:"required_retrieval_paths,omitempty"`
	QueryShouldUseRAG                 bool     `json:"query_should_use_rag,omitempty"`
	RAGShouldImproveRecommendations   bool     `json:"rag_should_improve_recommendations,omitempty"`
}

// AnomalyExpectations declares the golden behavior for one anomaly case.
type AnomalyExpectations struct {
	Label       string   `json:"label"`
	ReasonAny   []string `json:"reason_any,omitempty"`
	Disposition string   `json:"disposition"`
	Triggered   bool     `json:"triggered"`
}

// RunOptions controls one evaluation run.
type RunOptions struct {
	Scope             Scope
	RepoRoot          string
	JudgeExplanations bool
	JudgeCaseLimit    int
	JudgeBatchSize    int
}

// Report is the aggregate evaluation output.
type Report struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Scope       Scope                `json:"scope"`
	Retrieval   RetrievalSuiteReport `json:"retrieval"`
	Anomaly     AnomalySuiteReport   `json:"anomaly"`
	Workflow    WorkflowSuiteReport  `json:"workflow"`
	Passed      bool                 `json:"passed"`
}

// RetrievalSuiteReport summarizes retrieval-only metrics.
type RetrievalSuiteReport struct {
	Cases            []RetrievalCaseResult `json:"cases"`
	CasesRun         int                   `json:"cases_run"`
	CasesPassed      int                   `json:"cases_passed"`
	RecallAtK        float64               `json:"recall_at_k"`
	PrecisionAtK     float64               `json:"precision_at_k"`
	ContextPrecision float64               `json:"context_precision"`
	ContextRecall    float64               `json:"context_recall"`
	SignalCoverage   float64               `json:"signal_coverage"`
	IntentAccuracy   float64               `json:"intent_accuracy"`
	NoiseRobustness  float64               `json:"noise_robustness"`
	AverageLatencyMS float64               `json:"average_latency_ms"`
	FailedCaseIDs    []string              `json:"failed_case_ids,omitempty"`
}

// RetrievalCaseResult reports one retrieval-case outcome.
type RetrievalCaseResult struct {
	ID                string   `json:"id"`
	Description       string   `json:"description"`
	TopK              int      `json:"top_k"`
	RecallAtK         float64  `json:"recall_at_k"`
	PrecisionAtK      float64  `json:"precision_at_k"`
	ContextPrecision  float64  `json:"context_precision"`
	ContextRecall     float64  `json:"context_recall"`
	SignalCoverage    float64  `json:"signal_coverage"`
	IntentMatched     bool     `json:"intent_matched"`
	NoisyRecallAtK    float64  `json:"noisy_recall_at_k,omitempty"`
	LatencyMS         int64    `json:"latency_ms"`
	TopSourcePaths    []string `json:"top_source_paths,omitempty"`
	TopKnowledgeTypes []string `json:"top_knowledge_types,omitempty"`
	Passed            bool     `json:"passed"`
	Failures          []string `json:"failures,omitempty"`
}

// AnomalySuiteReport summarizes anomaly-classification quality.
type AnomalySuiteReport struct {
	Cases               []AnomalyCaseResult   `json:"cases"`
	CasesRun            int                   `json:"cases_run"`
	CasesPassed         int                   `json:"cases_passed"`
	Accuracy            float64               `json:"accuracy"`
	Precision           float64               `json:"precision"`
	Recall              float64               `json:"recall"`
	F1                  float64               `json:"f1"`
	FalsePositiveRate   float64               `json:"false_positive_rate"`
	FalseNegativeRate   float64               `json:"false_negative_rate"`
	DispositionAccuracy float64               `json:"disposition_accuracy"`
	ConfusionMatrix     []AnomalyConfusionRow `json:"confusion_matrix,omitempty"`
	PerClass            []AnomalyClassMetrics `json:"per_class,omitempty"`
	ExplanationJudge    *AnomalyJudgeReport   `json:"explanation_judge,omitempty"`
	FailedCaseIDs       []string              `json:"failed_case_ids,omitempty"`
}

// AnomalyCaseResult reports one anomaly-classification case outcome.
type AnomalyCaseResult struct {
	ID                   string   `json:"id"`
	Category             string   `json:"category"`
	SignalID             string   `json:"signal_id"`
	Description          string   `json:"description"`
	ExpectedLabel        string   `json:"expected_label"`
	PredictedLabel       string   `json:"predicted_label"`
	ExpectedDisposition  string   `json:"expected_disposition"`
	PredictedDisposition string   `json:"predicted_disposition"`
	ExpectedTriggered    bool     `json:"expected_triggered"`
	PredictedTriggered   bool     `json:"predicted_triggered"`
	ReasonMatched        bool     `json:"reason_matched"`
	Explanation          string   `json:"explanation,omitempty"`
	CrossSignalSupport   []string `json:"cross_signal_support,omitempty"`
	SuppressionFactor    float64  `json:"suppression_factor,omitempty"`
	MemorySamples        int      `json:"memory_samples,omitempty"`
	Passed               bool     `json:"passed"`
	Failures             []string `json:"failures,omitempty"`
}

// AnomalyJudgeReport summarizes optional LLM-judge agreement on anomaly explanations.
type AnomalyJudgeReport struct {
	Cases         []AnomalyJudgeCaseResult `json:"cases"`
	CasesRun      int                      `json:"cases_run"`
	CasesPassed   int                      `json:"cases_passed"`
	AgreementRate float64                  `json:"agreement_rate"`
	AverageScore  float64                  `json:"average_score"`
	FailedCaseIDs []string                 `json:"failed_case_ids,omitempty"`
}

// AnomalyJudgeCaseResult reports one LLM-judge decision on an anomaly explanation.
type AnomalyJudgeCaseResult struct {
	ID                   string  `json:"id"`
	ExpectedLabel        string  `json:"expected_label"`
	PredictedLabel       string  `json:"predicted_label"`
	ExpectedDisposition  string  `json:"expected_disposition"`
	PredictedDisposition string  `json:"predicted_disposition"`
	Explanation          string  `json:"explanation,omitempty"`
	Passed               bool    `json:"passed"`
	Score                float64 `json:"score"`
	Rationale            string  `json:"rationale,omitempty"`
	Error                string  `json:"error,omitempty"`
}

// AnomalyConfusionRow is one expected-label row of the confusion matrix.
type AnomalyConfusionRow struct {
	Expected    string         `json:"expected"`
	Predictions map[string]int `json:"predictions"`
}

// AnomalyClassMetrics summarizes one class in the confusion matrix.
type AnomalyClassMetrics struct {
	Label             string  `json:"label"`
	Support           int     `json:"support"`
	Precision         float64 `json:"precision"`
	Recall            float64 `json:"recall"`
	F1                float64 `json:"f1"`
	FalsePositiveRate float64 `json:"false_positive_rate"`
	FalseNegativeRate float64 `json:"false_negative_rate"`
}

// WorkflowSuiteReport summarizes RCA and recommendation metrics.
type WorkflowSuiteReport struct {
	Cases                     []WorkflowCaseResult `json:"cases"`
	CasesRun                  int                  `json:"cases_run"`
	CasesPassed               int                  `json:"cases_passed"`
	RootCauseAccuracyAt1      float64              `json:"root_cause_accuracy_at_1"`
	RootCauseAccuracyAt3      float64              `json:"root_cause_accuracy_at_3"`
	FaultDomainAccuracy       float64              `json:"fault_domain_accuracy"`
	EvidenceCoverage          float64              `json:"evidence_coverage"`
	TrajectoryAccuracy        float64              `json:"trajectory_accuracy"`
	QueryPathAccuracy         float64              `json:"query_path_accuracy"`
	RecommendationCorrectness float64              `json:"recommendation_correctness"`
	RecommendationSafety      float64              `json:"recommendation_safety"`
	GroundedCommandRate       float64              `json:"grounded_command_rate"`
	RAGImprovementRate        float64              `json:"rag_improvement_rate"`
	GovernanceCoverage        float64              `json:"governance_coverage"`
	VerificationCoverage      float64              `json:"verification_coverage"`
	AnalysisHandoffCoverage   float64              `json:"analysis_handoff_coverage"`
	ValidationReportCoverage  float64              `json:"validation_report_coverage"`
	ValidationLoopCoverage    float64              `json:"validation_loop_coverage"`
	DurableRunCoverage        float64              `json:"durable_run_coverage"`
	EvidencePackageCoverage   float64              `json:"evidence_package_coverage"`
	MemoryWritebackCoverage   float64              `json:"memory_writeback_coverage"`
	FailedCaseIDs             []string             `json:"failed_case_ids,omitempty"`
}

// WorkflowCaseResult reports one workflow scenario outcome.
type WorkflowCaseResult struct {
	ID                            string   `json:"id"`
	Description                   string   `json:"description"`
	RootCauseTop1                 bool     `json:"root_cause_top1"`
	RootCauseTop3                 bool     `json:"root_cause_top3"`
	FaultDomainMatched            bool     `json:"fault_domain_matched"`
	EvidenceCoverage              float64  `json:"evidence_coverage"`
	TrajectoryScore               float64  `json:"trajectory_score"`
	QueryUsedRAG                  bool     `json:"query_used_rag"`
	QueryRAGPathRecall            float64  `json:"query_rag_path_recall"`
	RecommendationCoverageNoRAG   float64  `json:"recommendation_coverage_no_rag"`
	RecommendationCoverageWithRAG float64  `json:"recommendation_coverage_with_rag"`
	RecommendationSafety          bool     `json:"recommendation_safety"`
	GroundedCommandRate           float64  `json:"grounded_command_rate"`
	RAGImproved                   bool     `json:"rag_improved"`
	DurableRunRecorded            bool     `json:"durable_run_recorded"`
	EvidencePackageGenerated      bool     `json:"evidence_package_generated"`
	MemoryWriteback               bool     `json:"memory_writeback"`
	GovernanceCoverage            float64  `json:"governance_coverage"`
	VerificationCoverage          float64  `json:"verification_coverage"`
	AnalysisHandoffRecorded       bool     `json:"analysis_handoff_recorded"`
	ValidationReportRecorded      bool     `json:"validation_report_recorded"`
	ValidationLoopsRecorded       bool     `json:"validation_loops_recorded"`
	TopRootCause                  string   `json:"top_root_cause,omitempty"`
	TopRecommendation             string   `json:"top_recommendation,omitempty"`
	RetrievalPaths                []string `json:"retrieval_paths,omitempty"`
	Failures                      []string `json:"failures,omitempty"`
	Passed                        bool     `json:"passed"`
}
