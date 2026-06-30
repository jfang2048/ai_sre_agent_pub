package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkflowMemoryStoreAppendAndQuery(t *testing.T) {
	store := NewWorkflowMemoryStore(t.TempDir(), nil, zap.NewNop())
	ref, err := store.Append(WorkflowMemoryRecord{
		RecordID:            "incident-1",
		WorkflowID:          "wf-1",
		IncidentID:          "inc-1",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "GPU timeout after rollout",
		Summary:             "GPU jobs timed out after a rollout",
		MostLikelyCause:     "driver mismatch",
		ResolutionSummary:   "rolled back the driver change",
		VerificationSummary: "latency returned to baseline",
		Signals:             []string{"gpu", "timeout", "latency"},
		Actions:             []string{"rollback driver"},
		Tags:                []string{"gpu", "rollout"},
		CreatedAt:           time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, ref.ArtifactID)

	hits := store.Query("gpu timeout after rollout", "historical_incident", "collector-a", 3)
	require.NotEmpty(t, hits)
	require.Equal(t, "incident_memory", hits[0].SourceType)
	require.Equal(t, "historical_incident", hits[0].KnowledgeType)
	require.Contains(t, hits[0].Snippet, "rolled back")
}

func TestWorkflowMemoryStoreQueryIncludesEffectSummaryAndExecutionCategory(t *testing.T) {
	store := NewWorkflowMemoryStore(t.TempDir(), nil, zap.NewNop())
	_, err := store.Append(WorkflowMemoryRecord{
		RecordID:            "incident-effect-1",
		WorkflowID:          "wf-effect-1",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "Checkout rollback verified",
		Summary:             "Rollback restored checkout after rollout regression",
		VerificationSummary: "post-action state improved: risk 0.81 -> 0.28",
		ActionOutcomes: []WorkflowMemoryActionOutcome{{
			Action:             "rollback checkout-v2",
			ActionCategory:     "rollback_revision",
			ExecutionCategory:  "probable_containment",
			ActuatorSafetyTier: "impacting",
			Mode:               "verified",
			Validated:          true,
			EffectSummary:      "post-action state improved: risk 0.81 -> 0.28",
			BeforeRisk:         0.81,
			AfterRisk:          0.28,
			Success:            true,
		}},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	hits := store.Query("checkout rollback verified", "historical_incident", "collector-a", 3)
	require.NotEmpty(t, hits)
	require.Equal(t, "probable_containment", hits[0].Metadata["execution_category"])
	require.Equal(t, "impacting", hits[0].Metadata["actuator_safety_tier"])
	require.Equal(t, "verified", hits[0].Metadata["action_mode"])
	require.Equal(t, "true", hits[0].Metadata["validated"])
	require.Equal(t, "0.81", hits[0].Metadata["before_risk"])
	require.Equal(t, "0.28", hits[0].Metadata["after_risk"])
	require.Contains(t, hits[0].Metadata["effect_summary"], "risk 0.81 -> 0.28")
}

func TestWorkflowMemoryStoreQueryPreservesAgentMessageMetadata(t *testing.T) {
	store := NewWorkflowMemoryStore(t.TempDir(), nil, zap.NewNop())
	_, err := store.Append(WorkflowMemoryRecord{
		RecordID:     "incident-msg-1",
		WorkflowID:   "wf-msg-1",
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Status:       "resolved",
		Title:        "Checkout rollback validated",
		Summary:      "Rollback restored checkout after rollout regression",
		Metadata: map[string]string{
			"agent_message_protocol":       "json_file_history",
			"agent_message_manifest_path":  "/tmp/messages/run-1/history.json",
			"validation_result_message_id": "msg-run-1-0003",
		},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	hits := store.Query("rollback validated", "historical_incident", "collector-a", 3)
	require.NotEmpty(t, hits)
	require.Equal(t, "json_file_history", hits[0].Metadata["agent_message_protocol"])
	require.Equal(t, "/tmp/messages/run-1/history.json", hits[0].Metadata["agent_message_manifest_path"])
	require.Equal(t, "msg-run-1-0003", hits[0].Metadata["validation_result_message_id"])
}

func TestWorkflowMemoryRecordToIncidentMemoryCarriesVerifiedEffectDetails(t *testing.T) {
	record := WorkflowMemoryRecord{
		RecordID:            "incident-effect-2",
		WorkflowID:          "wf-effect-2",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Title:               "Checkout rollback validated",
		Summary:             "Rollback restored checkout after a bad rollout",
		VerificationSummary: "post-action state improved: risk 0.76 -> 0.24",
		ActionOutcomes: []WorkflowMemoryActionOutcome{{
			ActionID:           "action-1",
			Action:             "rollback checkout-v2",
			ActionCategory:     "rollback_revision",
			ExecutionCategory:  "probable_containment",
			ActuatorSafetyTier: "impacting",
			Mode:               "verified",
			Status:             "succeeded",
			Verification:       "confirmed",
			Validated:          true,
			Success:            true,
			Useful:             true,
			EffectSummary:      "post-action state improved: risk 0.76 -> 0.24",
			BeforeRisk:         0.76,
			AfterRisk:          0.24,
		}},
		CreatedAt: time.Now().UTC(),
	}

	incident := record.toIncidentMemory()
	require.Len(t, incident.ActionOutcomes, 1)
	require.Equal(t, "rollback_revision", incident.ActionOutcomes[0].ActionCategory)
	require.Equal(t, "probable_containment", incident.ActionOutcomes[0].ExecutionCategory)
	require.Equal(t, "impacting", incident.ActionOutcomes[0].ActuatorSafetyTier)
	require.Equal(t, "verified", incident.ActionOutcomes[0].Mode)
	require.True(t, incident.ActionOutcomes[0].Validated)
	require.Equal(t, 0.76, incident.ActionOutcomes[0].BeforeRisk)
	require.Equal(t, 0.24, incident.ActionOutcomes[0].AfterRisk)
	require.Contains(t, incident.ActionOutcomes[0].EffectSummary, "risk 0.76 -> 0.24")
}

func TestIncidentMemoryActionOutcomesPreferActionContractMetadata(t *testing.T) {
	steps := []AgentPlanStep{{
		ID:               "step-1",
		Title:            "Rollback checkout",
		Status:           "verified",
		VerificationNote: "post-action state improved: risk 0.64 -> 0.22",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC(),
		Query: map[string]string{
			"action":              "legacy raw action",
			"action_intent":       "legacy_intent",
			"validation_category": "probable_containment",
		},
		ActionContract: &ValidationActionContract{
			ID:                 "contract-1",
			Intent:             "rollback_revision",
			ActionCategory:     "rollback",
			Summary:            "rollback checkout-v2",
			ExecutionCategory:  "probable_containment",
			ValidationCategory: "probable_containment",
			ActuatorSafetyTier: "impacting",
			TargetScope:        "service:checkout",
		},
		Verified: true,
	}}

	outcomes := incidentMemoryActionOutcomes(steps)
	require.Len(t, outcomes, 1)
	require.Equal(t, "rollback checkout-v2", outcomes[0].Action)
	require.Equal(t, "rollback", outcomes[0].ActionCategory)
	require.Equal(t, "rollback_revision", outcomes[0].ActionIntent)
	require.Equal(t, "probable_containment", outcomes[0].ExecutionCategory)
	require.Equal(t, "probable_containment", outcomes[0].ValidationCategory)
	require.Equal(t, "impacting", outcomes[0].ActuatorSafetyTier)
	require.Equal(t, "service:checkout", outcomes[0].TargetScope)
	require.Equal(t, "post-action state improved: risk 0.64 -> 0.22", outcomes[0].EffectSummary)
}

func TestWorkflowMemoryActionOutcomesMergeGovernanceAndPostActionEffect(t *testing.T) {
	state := &workflowState{
		dryRun: true,
		validationReport: ValidationActionReport{
			ActionCandidates: []ValidationActionCandidate{{
				ID:               "action-1",
				RecommendationID: "rec-1",
				Category:         "probable_containment",
				Summary:          "rollback checkout-v2",
				ActionIntent:     "rollback_revision",
				ActionCategory:   "rollback",
				RequiresApproval: true,
				ActionContract: &ValidationActionContract{
					ID:                 "contract-1",
					Intent:             "rollback_revision",
					ActionCategory:     "rollback",
					Summary:            "rollback checkout-v2",
					ExecutionCategory:  "probable_containment",
					ValidationCategory: "probable_containment",
					ActuatorSafetyTier: "impacting",
					TargetScope:        "service:checkout",
					BlastRadiusNotes:   []string{"target scope: service:checkout"},
				},
			}},
			SelectedAction:             &ValidationActionCandidate{ID: "action-1"},
			ValidatedRecommendationIDs: []string{"rec-1"},
			Governance: &ValidationGovernanceTrace{
				ActuatorSafetyTier: "impacting",
				ExecutionEligible:  false,
				ProposalOnly:       true,
				ApprovalState:      "approved",
				RollbackStatus:     "not_needed",
				BlastRadiusNotes:   []string{"human approval remains mandatory before any non-read-only execution"},
			},
		},
		planSteps: []AgentPlanStep{{
			ID:               "step-1",
			Tool:             ToolRemediation,
			Title:            "Rollback checkout",
			Status:           "verified",
			VerificationNote: "post-action state improved: risk 0.64 -> 0.22",
			Verified:         true,
			StartedAt:        time.Now().UTC(),
			CompletedAt:      time.Now().UTC(),
			Query: map[string]string{
				"action":              "legacy raw action",
				"action_intent":       "rollback_revision",
				"action_category":     "rollback",
				"validation_category": "probable_containment",
				"safety_tier":         "impacting",
				"proposal_only":       "true",
				"execution_eligible":  "false",
				"scope":               "service:checkout",
				"approval_state":      "approved",
				"requires_approval":   "true",
				"dry_run":             "true",
			},
			ActionContract: &ValidationActionContract{
				ID:                 "contract-1",
				Intent:             "rollback_revision",
				ActionCategory:     "rollback",
				Summary:            "rollback checkout-v2",
				ExecutionCategory:  "probable_containment",
				ValidationCategory: "probable_containment",
				ActuatorSafetyTier: "impacting",
				TargetScope:        "service:checkout",
				BlastRadiusNotes:   []string{"target scope: service:checkout"},
			},
		}},
		postActionEffect: &PostActionValidationSummary{
			ActionID:          "contract-1",
			Verdict:           ValidationVerdictConfirmed,
			Summary:           "post-action state improved: risk 0.64 -> 0.22",
			ExecutionCategory: "probable_containment",
			BeforeRisk:        0.64,
			AfterRisk:         0.22,
			Comparison: &ValidationEffectComparison{
				Comparable:  true,
				Incomplete:  true,
				MissingData: []string{"logs"},
			},
		},
	}

	outcomes := workflowMemoryActionOutcomes(state)
	require.Len(t, outcomes, 1)
	require.Equal(t, "contract-1", outcomes[0].ActionContractID)
	require.Equal(t, "rollback checkout-v2", outcomes[0].Action)
	require.Equal(t, "rollback_revision", outcomes[0].ActionIntent)
	require.Equal(t, "rollback", outcomes[0].ActionCategory)
	require.Equal(t, "probable_containment", outcomes[0].ExecutionCategory)
	require.Equal(t, "probable_containment", outcomes[0].ValidationCategory)
	require.Equal(t, "impacting", outcomes[0].ActuatorSafetyTier)
	require.Equal(t, "service:checkout", outcomes[0].TargetScope)
	require.True(t, outcomes[0].ProposalOnly)
	require.False(t, outcomes[0].ExecutionEligible)
	require.Equal(t, "approved", outcomes[0].ApprovalState)
	require.True(t, outcomes[0].ApprovalRequired)
	require.True(t, outcomes[0].DryRun)
	require.True(t, outcomes[0].Selected)
	require.True(t, outcomes[0].CandidateValidated)
	require.Equal(t, "confirmed", outcomes[0].PostActionVerdict)
	require.Equal(t, "not_needed", outcomes[0].RollbackStatus)
	require.True(t, outcomes[0].EffectComparable)
	require.True(t, outcomes[0].EffectIncomplete)
	require.Contains(t, outcomes[0].EffectMissingData, "logs")
	require.Contains(t, outcomes[0].BlastRadiusNotes, "target scope: service:checkout")
	require.Contains(t, outcomes[0].BlastRadiusNotes, "human approval remains mandatory before any non-read-only execution")
	require.Equal(t, 0.64, outcomes[0].BeforeRisk)
	require.Equal(t, 0.22, outcomes[0].AfterRisk)
}
