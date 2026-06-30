package agent

import (
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type workflowTelemetry struct {
	mu sync.RWMutex

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

	skillInvocations        map[string]uint64
	skillLowYield           map[string]uint64
	skillPolicyBlocks       map[string]uint64
	skillApprovalRequired   map[string]uint64
	skillDurationSecondsSum map[string]float64
	skillDurationCount      map[string]uint64
	skillScoreSum           map[string]float64
	skillScoreCount         map[string]uint64
	adaptiveStops           map[string]uint64
	ragSkillCalls           map[string]uint64
	artifactPersistFailures map[string]uint64
	replayValidationFailure map[string]uint64
}

func newWorkflowTelemetry() *workflowTelemetry {
	return &workflowTelemetry{
		skillInvocations:        map[string]uint64{},
		skillLowYield:           map[string]uint64{},
		skillPolicyBlocks:       map[string]uint64{},
		skillApprovalRequired:   map[string]uint64{},
		skillDurationSecondsSum: map[string]float64{},
		skillDurationCount:      map[string]uint64{},
		skillScoreSum:           map[string]float64{},
		skillScoreCount:         map[string]uint64{},
		adaptiveStops:           map[string]uint64{},
		ragSkillCalls:           map[string]uint64{},
		artifactPersistFailures: map[string]uint64{},
		replayValidationFailure: map[string]uint64{},
	}
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

func (t *workflowTelemetry) recordReasoningFailureKind(counter *atomic.Uint64) {
	if t == nil {
		return
	}
	if counter != nil {
		counter.Add(1)
	}
	t.reasoningFailuresTotal.Add(1)
}

func (t *workflowTelemetry) recordReasoningParseFailure() {
	t.recordReasoningFailureKind(&t.reasoningParseFailuresTotal)
}

func (t *workflowTelemetry) recordReasoningValidationFailure() {
	t.recordReasoningFailureKind(&t.reasoningValidFailuresTotal)
}

func (t *workflowTelemetry) recordReasoningLLMError() {
	t.recordReasoningFailureKind(&t.reasoningLLMErrorsTotal)
}

func (t *workflowTelemetry) recordReasoningBudgetExhausted() {
	t.recordReasoningFailureKind(&t.reasoningBudgetExhaustTotal)
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

func (t *workflowTelemetry) recordSkillInvocation(tool ToolName, family, status, mode string, duration time.Duration) {
	if t == nil {
		return
	}
	labels := []string{string(tool), firstNonEmpty(family, "generic"), firstNonEmpty(status, "unknown"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic)}
	key := workflowMetricKey(labels...)
	t.mu.Lock()
	t.skillInvocations[key]++
	durationKey := workflowMetricKey(string(tool), firstNonEmpty(family, "generic"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))
	t.skillDurationSecondsSum[durationKey] += maxFloat(duration.Seconds(), 0)
	t.skillDurationCount[durationKey]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordSkillLowYield(tool ToolName, family, mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.skillLowYield[workflowMetricKey(string(tool), firstNonEmpty(family, "generic"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordSkillPolicyBlock(tool ToolName, reason, mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.skillPolicyBlocks[workflowMetricKey(string(tool), firstNonEmpty(reason, "blocked"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordSkillApprovalRequired(tool ToolName, mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.skillApprovalRequired[workflowMetricKey(string(tool), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordSkillScore(tool ToolName, family, mode string, score float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	key := workflowMetricKey(string(tool), firstNonEmpty(family, "generic"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))
	t.skillScoreSum[key] += score
	t.skillScoreCount[key]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordAdaptiveStop(reason, mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.adaptiveStops[workflowMetricKey(firstNonEmpty(reason, "unknown"), firstNonEmpty(mode, WorkflowRuntimeModeLegacyDeterministic))]++
	t.mu.Unlock()
}

func (t *workflowTelemetry) recordRAGSkillCall(intent, status string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.ragSkillCalls[workflowMetricKey(firstNonEmpty(intent, "general"), firstNonEmpty(status, "unknown"))]++
	t.mu.Unlock()
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
		SkillInvocations:          t.counterSamplesLocked(t.skillInvocations, []string{"skill", "family", "status", "mode"}),
		SkillLowYield:             t.counterSamplesLocked(t.skillLowYield, []string{"skill", "family", "mode"}),
		SkillPolicyBlocks:         t.counterSamplesLocked(t.skillPolicyBlocks, []string{"skill", "reason", "mode"}),
		SkillApprovalRequired:     t.counterSamplesLocked(t.skillApprovalRequired, []string{"skill", "mode"}),
		SkillDurations:            t.histogramSamplesLocked(t.skillDurationSecondsSum, t.skillDurationCount, []string{"skill", "family", "mode"}),
		SkillScores:               t.histogramSamplesLocked(t.skillScoreSum, t.skillScoreCount, []string{"skill", "family", "mode"}),
		AdaptiveStops:             t.counterSamplesLocked(t.adaptiveStops, []string{"reason", "mode"}),
		RAGSkillCalls:             t.counterSamplesLocked(t.ragSkillCalls, []string{"intent", "status"}),
		ArtifactPersistFailures:   t.counterSamplesLocked(t.artifactPersistFailures, []string{"kind"}),
		ReplayValidationFailures:  t.counterSamplesLocked(t.replayValidationFailure, []string{"reason"}),
	}
}

func (t *workflowTelemetry) counterSamplesLocked(values map[string]uint64, labelNames []string) []WorkflowMetricSample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	keys := sortedMetricKeys(values)
	out := make([]WorkflowMetricSample, 0, len(keys))
	for _, key := range keys {
		out = append(out, WorkflowMetricSample{Labels: workflowMetricLabels(key, labelNames), Value: values[key]})
	}
	return out
}

func (t *workflowTelemetry) histogramSamplesLocked(sums map[string]float64, counts map[string]uint64, labelNames []string) []WorkflowMetricHistogramSample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	keys := sortedMetricKeys(counts)
	out := make([]WorkflowMetricHistogramSample, 0, len(keys))
	for _, key := range keys {
		out = append(out, WorkflowMetricHistogramSample{Labels: workflowMetricLabels(key, labelNames), Count: counts[key], Sum: sums[key]})
	}
	return out
}

func workflowMetricKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			part = "unknown"
		}
		cleaned = append(cleaned, strings.ReplaceAll(part, "|", "_"))
	}
	return strings.Join(cleaned, "|")
}

func sortedMetricKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func workflowMetricLabels(key string, labelNames []string) map[string]string {
	parts := strings.Split(key, "|")
	labels := make(map[string]string, len(labelNames))
	for idx, name := range labelNames {
		value := "unknown"
		if idx < len(parts) && strings.TrimSpace(parts[idx]) != "" {
			value = parts[idx]
		}
		labels[name] = value
	}
	return labels
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
