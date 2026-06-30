package evaluation

func computeSystemPerformanceScorecard(metrics SystemPerformanceMetrics) SystemPerformanceScorecard {
	correctness := average(
		metrics.RootCauseTop1Rate,
		metrics.RootCauseTopKRate,
		metrics.HypothesisSupportCorrectness,
		metrics.ContradictionDetectionRate,
		metrics.RecommendationValidationCorrectness,
		metrics.RemediationVerdictCorrectness,
		metrics.FinalIncidentOutcomeCorrectness,
	)
	closure := average(
		metrics.AnalysisHandoffCoverage,
		metrics.ValidationReportCoverage,
		metrics.ActionPlanCoverage,
		metrics.PostActionValidationCoverage,
		metrics.EvidencePackageCoverage,
		metrics.MemoryWritebackCoverage,
		metrics.RollbackOrCompensationCoverage,
	)
	governance := average(
		metrics.GovernanceCoverage,
		metrics.ApprovalEnforcementRate,
		metrics.DryRunCompliance,
		metrics.ExecutionCategoryEnforcementRate,
		metrics.IdempotencyPreservationRate,
		metrics.AuditCompleteness,
	)
	efficiency := average(
		underThreshold(metrics.EndToEndLatencyMS, 2500),
		underThreshold(metrics.AnalysisAgentLatencyMS, 1200),
		underThreshold(metrics.ValidationAgentLatencyMS, 1200),
		underThreshold(metrics.HandoffSerializationLatencyMS, 250),
		underThreshold(metrics.HandoffParseLatencyMS, 250),
		underThreshold(metrics.ToolCallCount, 12),
		underThreshold(metrics.ToolLatencyMS, 1500),
		underThreshold(metrics.TokenCost, 3000),
	)
	stability := average(
		metrics.ReplayStabilityScore,
		1-metrics.RankingDrift,
		1-metrics.ToolSelectionDrift,
		metrics.VerdictConsistency,
		metrics.MessageReproducibility,
		1-metrics.ValidationLoopDrift,
	)
	collaboration := average(
		metrics.HandoffSchemaValidRate,
		metrics.HandoffParseSuccessRate,
		metrics.HandoffRequiredFieldsCoverage,
		metrics.HandoffTargetExtractionScore,
		metrics.CrossAgentInformationRetentionScore,
		metrics.MessageHistoryIntegrityScore,
		metrics.AgentAgreementScore,
		metrics.ParentChildMessageLinkageCompleteness,
	)
	return SystemPerformanceScorecard{
		Correctness:   correctness,
		Closure:       closure,
		Governance:    governance,
		Efficiency:    efficiency,
		Stability:     stability,
		Collaboration: collaboration,
		OverallScore:  average(correctness, closure, governance, efficiency, stability, collaboration),
	}
}

func underThreshold(value, threshold float64) float64 {
	if threshold <= 0 {
		return 1
	}
	if value <= 0 {
		return 1
	}
	if value <= threshold {
		return 1
	}
	return clamp01(threshold / value)
}

func average(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, item := range values {
		sum += clamp01(item)
	}
	return sum / float64(len(values))
}
