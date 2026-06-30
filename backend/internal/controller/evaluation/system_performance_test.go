package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
	"github.com/stretchr/testify/require"
)

func TestComputeSystemPerformanceScorecardAggregatesDimensions(t *testing.T) {
	metrics := SystemPerformanceMetrics{
		RootCauseTop1Rate:                     1,
		RootCauseTopKRate:                     1,
		HypothesisSupportCorrectness:          0.8,
		ContradictionDetectionRate:            0.9,
		RecommendationValidationCorrectness:   0.75,
		RemediationVerdictCorrectness:         1,
		FinalIncidentOutcomeCorrectness:       0.9,
		AnalysisHandoffCoverage:               1,
		ValidationReportCoverage:              1,
		ActionPlanCoverage:                    1,
		PostActionValidationCoverage:          1,
		EvidencePackageCoverage:               1,
		MemoryWritebackCoverage:               1,
		RollbackOrCompensationCoverage:        1,
		GovernanceCoverage:                    1,
		ApprovalEnforcementRate:               1,
		DryRunCompliance:                      1,
		ExecutionCategoryEnforcementRate:      1,
		IdempotencyPreservationRate:           1,
		AuditCompleteness:                     1,
		EndToEndLatencyMS:                     1200,
		AnalysisAgentLatencyMS:                500,
		ValidationAgentLatencyMS:              400,
		HandoffSerializationLatencyMS:         80,
		HandoffParseLatencyMS:                 30,
		ToolCallCount:                         4,
		ToolLatencyMS:                         600,
		TokenCost:                             0,
		ReplayStabilityScore:                  0.95,
		RankingDrift:                          0.05,
		ToolSelectionDrift:                    0.10,
		VerdictConsistency:                    1,
		MessageReproducibility:                1,
		ValidationLoopDrift:                   0.05,
		HandoffSchemaValidRate:                1,
		HandoffParseSuccessRate:               1,
		HandoffRequiredFieldsCoverage:         1,
		HandoffTargetExtractionScore:          1,
		CrossAgentInformationRetentionScore:   0.9,
		MessageHistoryIntegrityScore:          1,
		AgentAgreementScore:                   1,
		ParentChildMessageLinkageCompleteness: 1,
	}

	scorecard := computeSystemPerformanceScorecard(metrics)
	require.GreaterOrEqual(t, scorecard.Correctness, 0.90)
	require.GreaterOrEqual(t, scorecard.Closure, 0.99)
	require.GreaterOrEqual(t, scorecard.Governance, 0.99)
	require.GreaterOrEqual(t, scorecard.Efficiency, 0.85)
	require.GreaterOrEqual(t, scorecard.Stability, 0.90)
	require.GreaterOrEqual(t, scorecard.Collaboration, 0.95)
	require.GreaterOrEqual(t, scorecard.OverallScore, 0.93)
}

func TestEvaluateSystemPerformanceCaseExtractsGovernanceAndCommunicationMetrics(t *testing.T) {
	base := time.Now().UTC().Add(-2 * time.Minute)
	handoff := agentcore.AnalysisHandoff{
		Agent:           "analysis_agent",
		CreatedAt:       base,
		IncidentSummary: "memory pressure on checkout-api",
		Hypotheses:      []agentcore.RCAHypothesis{{ID: "hyp-1", Title: "memory leak"}},
		HypothesisPackets: []agentcore.AnalysisHypothesisHandoff{{
			HypothesisID: "hyp-1",
			Rank:         1,
			Title:        "memory leak",
		}},
		SuggestedValidationTargets: []agentcore.ValidationTarget{{
			ID:                    "target-1",
			Type:                  agentcore.ValidationTargetHypothesis,
			Title:                 "validate memory leak",
			HypothesisID:          "hyp-1",
			ExecutionCategory:     "read_only_validation",
			ReadOnly:              true,
			SuggestedTools:        []agentcore.ToolName{agentcore.ToolMetrics},
			SupportingEvidenceIDs: []string{"ev-1"},
		}},
		BoundedActionCandidates: []agentcore.ValidationActionCandidate{{
			ID:               "act-1",
			Summary:          "restart the leaking service",
			Category:         "read_only_validation",
			ActionIntent:     "restart_workload",
			DryRunDefault:    true,
			RequiresApproval: true,
			ActionCategory:   "containment",
		}},
	}
	contract := &agentcore.ValidationActionContract{
		ID:                 "contract-1",
		Intent:             "restart_workload",
		ActionCategory:     "containment",
		ExecutionCategory:  "read_only_validation",
		ValidationCategory: "read_only_validation",
		DryRunDefault:      true,
		RequiresApproval:   true,
	}
	history := writeSystemPerfMessageHistory(t, handoff)
	report := agentcore.RCAWorkflowReport{
		WorkflowID:                     "wf-1",
		EvidencePackagePath:            "/tmp/evidence.json",
		MessageManifestPath:            filepath.Join(filepath.Dir(history[0].Path), "history.json"),
		AnalysisHandoff:                handoff,
		MessageHistory:                 history,
		LatestAnalysisHandoffMessage:   cloneAgentMessageRef(&history[0]),
		LatestValidationRequestMessage: cloneAgentMessageRef(&history[1]),
		LatestValidationResultMessage:  cloneAgentMessageRef(&history[2]),
		Stages: []agentcore.PipelineStageResult{
			{Name: "collect_signals", StartedAt: base, CompletedAt: base.Add(150 * time.Millisecond)},
			{Name: "analysis_handoff_finalize", StartedAt: base.Add(200 * time.Millisecond), CompletedAt: base.Add(260 * time.Millisecond)},
			{Name: "validation_action_react_loop", StartedAt: base.Add(300 * time.Millisecond), CompletedAt: base.Add(500 * time.Millisecond)},
			{Name: "post_action_validation", StartedAt: base.Add(550 * time.Millisecond), CompletedAt: base.Add(620 * time.Millisecond)},
		},
		Validation: agentcore.ValidationActionReport{
			Agent:                 "validation_action_agent",
			Mode:                  "bounded_react",
			StartedAt:             base.Add(300 * time.Millisecond),
			CompletedAt:           base.Add(500 * time.Millisecond),
			HandoffParseLatencyMS: 12,
			Targets: []agentcore.ValidationTarget{{
				ID:                "target-1",
				Type:              agentcore.ValidationTargetHypothesis,
				Title:             "validate memory leak",
				HypothesisID:      "hyp-1",
				ExecutionCategory: "read_only_validation",
			}},
			Results: []agentcore.ValidationTargetResult{{
				TargetID:     "target-1",
				TargetType:   agentcore.ValidationTargetHypothesis,
				HypothesisID: "hyp-1",
				Verdict:      agentcore.ValidationVerdictConfirmed,
				ToolSequence: []agentcore.ToolName{agentcore.ToolMetrics},
				Summary:      "rss growth confirmed",
				Confidence:   0.88,
			}},
			SourceAnalysisMessage:   cloneAgentMessageRef(&history[0]),
			SourceValidationRequest: cloneAgentMessageRef(&history[1]),
			Governance: &agentcore.ValidationGovernanceTrace{
				ExecutionCategory: "read_only_validation",
				ApprovalState:     "pending",
				StepID:            "step-1",
			},
			SelectedAction: &agentcore.ValidationActionCandidate{
				ID:             "act-1",
				Summary:        "restart the leaking service",
				ActionContract: contract,
			},
			SelectedActionContract: contract,
			ActionSummary:          []string{"selected guarded action candidate"},
			PostActionValidation: &agentcore.PostActionValidationSummary{
				Verdict:           agentcore.ValidationVerdictInsufficientEvidence,
				Summary:           "no guarded remediation executed; validation agent stayed read-only",
				FallbackMode:      "not_executed",
				ExecutionCategory: "read_only_validation",
			},
		},
	}
	run := &agentcore.DurableRun{
		RunID:               "wf-1",
		Status:              agentcore.RunStatusCompleted,
		MessageManifestPath: report.MessageManifestPath,
		MessageHistory:      history,
		ToolCalls: []agentcore.WorkflowToolCall{{
			ID:                "call-1",
			Tool:              agentcore.ToolMetrics,
			Actor:             "validation_action_agent",
			Stage:             "validation_action_react_loop",
			DryRun:            true,
			ExecutionCategory: "read_only_validation",
			IdempotencyKey:    "idem-1",
			StartedAt:         base.Add(320 * time.Millisecond),
			CompletedAt:       base.Add(360 * time.Millisecond),
		}},
		Steps: []agentcore.DurableStepRecord{{
			StepID:            "step-1",
			Stage:             "guarded_execution_plan",
			Status:            "planned",
			ExecutionCategory: "read_only_validation",
			ActionContract:    contract,
			Approval: &agentcore.DurableApprovalRecord{
				State: "pending",
			},
			StartedAt:   base.Add(400 * time.Millisecond),
			CompletedAt: base.Add(450 * time.Millisecond),
		}},
		Validation:    &report.Validation,
		MemoryRecords: []string{"mem-1"},
		EvidencePackage: &agentcore.DurableEvidencePackageRef{
			ArtifactID:     "evidence-memory-leak-trend",
			ArtifactType:   "evidence_package",
			StorageBackend: "filesystem",
			StorageKey:     "evidence/memory_leak_trend/package.json",
			LocalCachePath: report.EvidencePackagePath,
			Path:           report.EvidencePackagePath,
			CreatedAt:      base.Add(time.Second),
			UpdatedAt:      base.Add(time.Second),
		},
		Events: []agentcore.WorkflowEvent{{EventID: "evt-1", Type: "run.started", Timestamp: base}},
	}
	execution := eval.WorkflowCaseExecution{
		Result: eval.WorkflowCaseResult{
			ID:                            "memory_leak_trend",
			RootCauseTop1:                 true,
			RootCauseTop3:                 true,
			RecommendationCoverageWithRAG: 1,
			AnalysisHandoffRecorded:       true,
			ValidationReportRecorded:      true,
			EvidencePackageGenerated:      true,
			MemoryWriteback:               true,
			GovernanceCoverage:            1,
			TopRootCause:                  "memory leak",
			TopRecommendation:             "inspect top RSS processes",
			Passed:                        true,
		},
		Report:     report,
		DurableRun: run,
	}
	contractCase := SystemPerformanceCase{
		ID:             "memory_leak_full_runtime",
		IncidentType:   "memory",
		IncidentCaseID: "memory_leak_trend",
		Description:    "memory full runtime",
		ExpectedGovernance: SystemPerformanceGovernanceExpectation{
			RequireMessageProtocol:    true,
			RequireValidationAgent:    true,
			ExpectDryRun:              true,
			ExpectedValidationMode:    "bounded_react",
			ExpectedExecutionCategory: "read_only_validation",
		},
		ExpectedPostAction: SystemPerformancePostActionExpectation{
			ExpectedVerdictAny: []string{"insufficient_evidence"},
			ExpectedFallback:   "not_executed",
		},
		ExpectedArtifacts: SystemPerformanceArtifactExpectation{
			RequireAnalysisHandoff:      true,
			RequireValidationReport:     true,
			RequireActionPlan:           true,
			RequirePostActionValidation: true,
			RequireEvidencePackage:      true,
			RequireMemoryWriteback:      true,
			RequiredMessageTypes:        []string{"analysis_handoff", "validation_request", "validation_result"},
		},
	}

	result := evaluateSystemPerformanceCase(contractCase, []eval.WorkflowCaseExecution{execution, execution})
	require.True(t, result.Passed, result.Failures)
	require.Equal(t, 1.0, result.Metrics.MessageHistoryIntegrityScore)
	require.Equal(t, 1.0, result.Metrics.ApprovalEnforcementRate)
	require.Equal(t, 1.0, result.Metrics.DryRunCompliance)
	require.Equal(t, 1.0, result.Metrics.ExecutionCategoryEnforcementRate)
	require.Equal(t, 1.0, result.Metrics.HandoffSchemaValidRate)
	require.Equal(t, 1.0, result.Metrics.HandoffParseSuccessRate)
	require.Equal(t, 1.0, result.Metrics.HandoffTargetExtractionScore)
	require.GreaterOrEqual(t, result.Scorecard.Collaboration, 0.95)
	require.GreaterOrEqual(t, result.Scorecard.Governance, 0.95)
}

func TestCompareSystemPerformanceReportsTracksDeltas(t *testing.T) {
	current := SystemPerformanceReport{
		GeneratedAt: time.Now().UTC(),
		Metrics: SystemPerformanceMetrics{
			EndToEndLatencyMS:                     900,
			GovernanceCoverage:                    0.95,
			MessageHistoryIntegrityScore:          1,
			HandoffSchemaValidRate:                1,
			HandoffParseSuccessRate:               1,
			CrossAgentInformationRetentionScore:   0.9,
			ParentChildMessageLinkageCompleteness: 1,
		},
		Scorecard: SystemPerformanceScorecard{
			Correctness:   0.9,
			Closure:       0.9,
			Governance:    0.95,
			Efficiency:    0.9,
			Stability:     0.9,
			Collaboration: 0.95,
			OverallScore:  0.916,
		},
	}
	baseline := current
	baseline.GeneratedAt = current.GeneratedAt.Add(-time.Hour)
	baseline.Metrics.EndToEndLatencyMS = 1100
	baseline.Metrics.GovernanceCoverage = 0.90
	baseline.Metrics.MessageHistoryIntegrityScore = 0.8
	baseline.Scorecard.OverallScore = 0.85

	comparison := compareSystemPerformanceReports(current, baseline, "/tmp/baseline.json")
	require.Equal(t, "/tmp/baseline.json", comparison.BaselinePath)
	require.InDelta(t, 0.066, comparison.ScoreDeltas["overall_score"], 0.001)
	require.InDelta(t, -200, comparison.LatencyDeltas["end_to_end_latency_ms"], 0.001)
	require.InDelta(t, 0.05, comparison.GovernanceDeltas["governance_coverage"], 0.001)
	require.InDelta(t, 0.2, comparison.CollaborationDeltas["message_history_integrity_score"], 0.001)
}

func TestSystemPerformanceReportJSONRoundTrip(t *testing.T) {
	report := SystemPerformanceReport{
		SchemaVersion: "system-performance/v1",
		GeneratedAt:   time.Now().UTC(),
		Scope:         eval.ScopeFast,
		ReplayRuns:    2,
		Scorecard:     SystemPerformanceScorecard{OverallScore: 0.9},
	}
	raw, err := report.JSON()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	loaded, err := loadSystemPerformanceReport(path)
	require.NoError(t, err)
	require.Equal(t, report.SchemaVersion, loaded.SchemaVersion)
	require.Equal(t, report.Scope, loaded.Scope)
	require.InDelta(t, report.Scorecard.OverallScore, loaded.Scorecard.OverallScore, 0.0001)
}

func TestRunSystemPerformanceFastOneCase(t *testing.T) {
	report, err := RunSystemPerformance(context.Background(), SystemPerformanceOptions{
		Scope:      eval.ScopeFast,
		ReplayRuns: 2,
		CaseIDs:    []string{"memory_leak_full_runtime"},
	})
	require.NoError(t, err)
	require.True(t, report.Passed, report.Text())
	require.Len(t, report.Cases, 1)
	require.GreaterOrEqual(t, report.Scorecard.OverallScore, 0.75, report.Text())
	require.GreaterOrEqual(t, report.Metrics.HandoffSchemaValidRate, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Metrics.MessageHistoryIntegrityScore, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Metrics.CrossAgentInformationRetentionScore, 0.50, report.Text())
	require.NotEmpty(t, report.LatestPath)
	require.NotEmpty(t, report.HistoryPath)
	_, err = os.Stat(report.LatestPath)
	require.NoError(t, err)
	_, err = os.Stat(report.HistoryPath)
	require.NoError(t, err)

	latest, err := loadSystemPerformanceReport(report.LatestPath)
	require.NoError(t, err)
	require.Equal(t, report.LatestPath, latest.LatestPath)
	require.Equal(t, report.HistoryPath, latest.HistoryPath)
}

func writeSystemPerfMessageHistory(t *testing.T, handoff agentcore.AnalysisHandoff) []agentcore.AgentMessageRef {
	t.Helper()
	dir := t.TempDir()
	writeEnvelope := func(seq int, messageType agentcore.AgentMessageType, parent, previous string, payload any) agentcore.AgentMessageRef {
		t.Helper()
		payloadRaw, err := json.Marshal(payload)
		require.NoError(t, err)
		envelope := agentcore.AgentMessageEnvelope{
			Header: agentcore.AgentMessageHeader{
				SchemaVersion:     "agent-message/v1",
				MessageID:         "msg-run-000" + string(rune('0'+seq)),
				RunID:             "wf-1",
				WorkflowType:      "incident_rca",
				FromAgent:         "analysis_agent",
				ToAgent:           "validation_action_agent",
				MessageType:       messageType,
				CreatedAt:         time.Now().UTC(),
				ParentMessageID:   parent,
				PreviousMessageID: previous,
				Sequence:          seq,
			},
			Body: agentcore.AgentMessageBody{
				PayloadSummary: "test",
				ContentHash:    agentMessageContentHash(payloadRaw),
				Payload:        payloadRaw,
			},
		}
		path := filepath.Join(dir, filepath.Base(filepath.Join(dir, ""))+time.Now().Format("150405"))
		path = filepath.Join(dir, filepath.Clean(
			func() string {
				switch messageType {
				case agentcore.AgentMessageTypeAnalysisHandoff:
					return "0001-analysis-handoff.json"
				case agentcore.AgentMessageTypeValidationRequest:
					return "0002-validation-request.json"
				default:
					return "0003-validation-result.json"
				}
			}(),
		))
		raw, err := json.MarshalIndent(envelope, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o644))
		return agentcore.AgentMessageRef{
			MessageID:         envelope.Header.MessageID,
			RunID:             envelope.Header.RunID,
			WorkflowType:      envelope.Header.WorkflowType,
			FromAgent:         envelope.Header.FromAgent,
			ToAgent:           envelope.Header.ToAgent,
			MessageType:       envelope.Header.MessageType,
			Sequence:          envelope.Header.Sequence,
			CreatedAt:         envelope.Header.CreatedAt,
			ParentMessageID:   envelope.Header.ParentMessageID,
			PreviousMessageID: envelope.Header.PreviousMessageID,
			Path:              path,
			PayloadSummary:    envelope.Body.PayloadSummary,
			ContentHash:       envelope.Body.ContentHash,
		}
	}

	ref1 := writeEnvelope(1, agentcore.AgentMessageTypeAnalysisHandoff, "", "", agentcore.AnalysisHandoffMessage{Handoff: handoff})
	ref2 := writeEnvelope(2, agentcore.AgentMessageTypeValidationRequest, ref1.MessageID, ref1.MessageID, agentcore.ValidationRequestMessage{
		AnalysisMessage: ref1,
		TargetLimit:     1,
		ReadOnlyOnly:    true,
		RequestedAt:     time.Now().UTC(),
	})
	ref3 := writeEnvelope(3, agentcore.AgentMessageTypeValidationResult, ref2.MessageID, ref2.MessageID, agentcore.ValidationResultMessage{
		Report: agentcore.ValidationActionReport{Agent: "validation_action_agent", Mode: "bounded_react"},
	})
	history := agentcore.AgentMessageHistory{
		SchemaVersion: "agent-message/v1",
		RunID:         "wf-1",
		WorkflowType:  "incident_rca",
		UpdatedAt:     time.Now().UTC(),
		Messages:      []agentcore.AgentMessageRef{ref1, ref2, ref3},
	}
	raw, err := json.MarshalIndent(history, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "history.json"), raw, 0o644))
	return []agentcore.AgentMessageRef{ref1, ref2, ref3}
}
