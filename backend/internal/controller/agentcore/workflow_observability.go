package agent

import (
	"io"
	"os"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type workflowTelemetry struct {
	reasoningStepsTotal         atomic.Uint64
	reasoningFailuresTotal      atomic.Uint64 // generic failures
	reasoningParseFailuresTotal atomic.Uint64
	reasoningValidFailuresTotal atomic.Uint64
	reasoningLLMErrorsTotal     atomic.Uint64
	reasoningBudgetExhaustTotal atomic.Uint64

	confidenceSamplesTotal  atomic.Uint64
	confidenceMilliSum      atomic.Uint64
	tokenCostTotal          atomic.Uint64
	hallucinationProxyTotal atomic.Uint64
	retrievalHitsTotal      atomic.Uint64
	retrievalMissTotal      atomic.Uint64

	actionsExecutedTotal atomic.Uint64
	actionsDryRunTotal   atomic.Uint64
	actionsBlockedTotal  atomic.Uint64

	workflowRunsTotal       atomic.Uint64
	workflowLatencyNanos    atomic.Uint64
	incidentRCARunsTotal    atomic.Uint64
	incidentRCALatencyNanos atomic.Uint64

	remediationsTotal         atomic.Uint64
	remediationsSuccessTotal  atomic.Uint64
	remediationsFailureTotal  atomic.Uint64
	remediationsVerifiedTotal atomic.Uint64
	remediationsRollbackTotal atomic.Uint64
	verificationsTotal        atomic.Uint64
	verificationSuccessTotal  atomic.Uint64
	verificationFailureTotal  atomic.Uint64
	approvalsPendingTotal     atomic.Uint64
	evidencePackagesTotal     atomic.Uint64
	memoryWritebacksTotal     atomic.Uint64
}

func newWorkflowTelemetry() *workflowTelemetry {
	return &workflowTelemetry{}
}

func (t *workflowTelemetry) recordReasoningStep(confidence float64, promptTokens, completionTokens int) {
	if t == nil {
		return
	}
	t.reasoningStepsTotal.Add(1)
	if confidence > 0 {
		t.confidenceSamplesTotal.Add(1)
		milli := uint64(clamp01(confidence) * 1000)
		t.confidenceMilliSum.Add(milli)
	}
	totalTokens := promptTokens + completionTokens
	if totalTokens > 0 {
		t.tokenCostTotal.Add(uint64(totalTokens))
	}
}

func (t *workflowTelemetry) recordReasoningFailure() {
	if t == nil {
		return
	}
	t.reasoningFailuresTotal.Add(1)
}

func (t *workflowTelemetry) recordReasoningParseFailure() {
	if t != nil {
		t.reasoningParseFailuresTotal.Add(1)
		t.reasoningFailuresTotal.Add(1) // also bump generic
	}
}

func (t *workflowTelemetry) recordReasoningValidationFailure() {
	if t != nil {
		t.reasoningValidFailuresTotal.Add(1)
		t.reasoningFailuresTotal.Add(1) // also bump generic
	}
}

func (t *workflowTelemetry) recordReasoningLLMError() {
	if t != nil {
		t.reasoningLLMErrorsTotal.Add(1)
		t.reasoningFailuresTotal.Add(1) // also bump generic
	}
}

func (t *workflowTelemetry) recordReasoningBudgetExhausted() {
	if t != nil {
		t.reasoningBudgetExhaustTotal.Add(1)
		t.reasoningFailuresTotal.Add(1) // also bump generic
	}
}

func (t *workflowTelemetry) recordHallucinationProxy() {
	if t == nil {
		return
	}
	t.hallucinationProxyTotal.Add(1)
}

func (t *workflowTelemetry) recordRetrieval(hitCount int) {
	if t == nil {
		return
	}
	if hitCount > 0 {
		t.retrievalHitsTotal.Add(uint64(hitCount))
	} else {
		t.retrievalMissTotal.Add(1)
	}
}

func (t *workflowTelemetry) recordRemediation(success, verified bool) {
	if t == nil {
		return
	}
	t.remediationsTotal.Add(1)
	if success {
		t.remediationsSuccessTotal.Add(1)
	} else {
		t.remediationsFailureTotal.Add(1)
	}
	if verified {
		t.remediationsVerifiedTotal.Add(1)
	}
}

func (t *workflowTelemetry) recordRollback() {
	if t == nil {
		return
	}
	t.remediationsRollbackTotal.Add(1)
}

func (t *workflowTelemetry) recordActionOutcome(status string) {
	if t == nil {
		return
	}
	switch status {
	case ActionResultExecuted:
		t.actionsExecutedTotal.Add(1)
	case ActionResultDryRun:
		t.actionsDryRunTotal.Add(1)
	case ActionResultFailed, ActionResultBlocked:
		t.actionsBlockedTotal.Add(1)
	}
}

func (t *workflowTelemetry) recordVerification(success bool) {
	if t == nil {
		return
	}
	t.verificationsTotal.Add(1)
	if success {
		t.verificationSuccessTotal.Add(1)
		return
	}
	t.verificationFailureTotal.Add(1)
}

func (t *workflowTelemetry) recordApprovalPending() {
	if t == nil {
		return
	}
	t.approvalsPendingTotal.Add(1)
}

func (t *workflowTelemetry) recordEvidencePackage() {
	if t == nil {
		return
	}
	t.evidencePackagesTotal.Add(1)
}

func (t *workflowTelemetry) recordMemoryWriteback() {
	if t == nil {
		return
	}
	t.memoryWritebacksTotal.Add(1)
}

func (t *workflowTelemetry) recordWorkflowLatency(workflowType string, d time.Duration) {
	if t == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	t.workflowRunsTotal.Add(1)
	t.workflowLatencyNanos.Add(uint64(d))
	if workflowType == "rca" {
		t.incidentRCARunsTotal.Add(1)
		t.incidentRCALatencyNanos.Add(uint64(d))
	}
}

func (t *workflowTelemetry) snapshot() WorkflowMetricsSnapshot {
	if t == nil {
		return WorkflowMetricsSnapshot{}
	}
	confidenceSamples := t.confidenceSamplesTotal.Load()
	avgConfidence := 0.0
	if confidenceSamples > 0 {
		avgConfidence = float64(t.confidenceMilliSum.Load()) / float64(confidenceSamples) / 1000.0
	}
	rcaRuns := t.incidentRCARunsTotal.Load()
	tokenCostPerIncident := 0.0
	if rcaRuns > 0 {
		tokenCostPerIncident = float64(t.tokenCostTotal.Load()) / float64(rcaRuns)
	}
	return WorkflowMetricsSnapshot{
		ReasoningStepsTotal:       t.reasoningStepsTotal.Load(),
		ReasoningFailuresTotal:    t.reasoningFailuresTotal.Load(),
		ReasoningParseFailTotal:   t.reasoningParseFailuresTotal.Load(),
		ReasoningValidFailTotal:   t.reasoningValidFailuresTotal.Load(),
		ReasoningLLMErrorTotal:    t.reasoningLLMErrorsTotal.Load(),
		ReasoningBudgetExhTotal:   t.reasoningBudgetExhaustTotal.Load(),
		AvgConfidence:             avgConfidence,
		TokenCostTotal:            t.tokenCostTotal.Load(),
		TokenCostPerIncident:      tokenCostPerIncident,
		HallucinationProxyTotal:   t.hallucinationProxyTotal.Load(),
		RetrievalHitsTotal:        t.retrievalHitsTotal.Load(),
		RetrievalMissTotal:        t.retrievalMissTotal.Load(),
		ActionsExecutedTotal:      t.actionsExecutedTotal.Load(),
		ActionsDryRunTotal:        t.actionsDryRunTotal.Load(),
		ActionsBlockedTotal:       t.actionsBlockedTotal.Load(),
		WorkflowRunsTotal:         t.workflowRunsTotal.Load(),
		WorkflowLatencySeconds:    float64(t.workflowLatencyNanos.Load()) / float64(time.Second),
		IncidentRCARunsTotal:      rcaRuns,
		IncidentRCALatencySeconds: float64(t.incidentRCALatencyNanos.Load()) / float64(time.Second),
		VerificationsTotal:        t.verificationsTotal.Load(),
		VerificationSuccessTotal:  t.verificationSuccessTotal.Load(),
		VerificationFailureTotal:  t.verificationFailureTotal.Load(),
		ApprovalsPendingTotal:     t.approvalsPendingTotal.Load(),
		CompensationsTotal:        t.remediationsRollbackTotal.Load(),
		EvidencePackagesTotal:     t.evidencePackagesTotal.Load(),
		MemoryWritebacksTotal:     t.memoryWritebacksTotal.Load(),
	}
}

func newWorkflowMetaLogger() *zap.Logger {
	return newWorkflowMetaLoggerWithWriter(os.Stderr)
}

func newWorkflowMetaLoggerWithWriter(w io.Writer) *zap.Logger {
	if w == nil {
		w = io.Discard
	}
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(w),
		zap.InfoLevel,
	)
	return zap.New(core).With(zap.String("component", "agent_workflow_engine"))
}
