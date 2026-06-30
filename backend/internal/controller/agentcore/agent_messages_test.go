package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentMessageStoreAppendsDurableOrderedHistory(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.AgentMessageDir = t.TempDir()
	store := NewAgentMessageStore(cfg, nil, zap.NewNop())

	handoff := AnalysisHandoff{
		SchemaVersion:   AnalysisHandoffSchemaVersion,
		Agent:           "analysis_agent",
		CollectorID:     "collector-a",
		IncidentSummary: "checkout latency spike after rollout",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:    "target-1",
			Type:  ValidationTargetHypothesis,
			Title: "rollout regression",
		}},
	}

	analysisRef, manifestRef, err := store.Append("run-message-1", "rca", "analysis_agent", "validation_action_agent", AgentMessageTypeAnalysisHandoff, nil, AnalysisHandoffMessage{
		Handoff: handoff,
	}, "analysis handoff")
	require.NoError(t, err)
	require.NotNil(t, manifestRef)
	require.Equal(t, "0001-analysis-handoff.json", filepath.Base(analysisRef.Path))

	requestRef, manifestRef2, err := store.Append("run-message-1", "rca", "workflow_runtime", "validation_action_agent", AgentMessageTypeValidationRequest, &analysisRef, ValidationRequestMessage{
		AnalysisMessage: analysisRef,
		TargetLimit:     6,
		ReadOnlyOnly:    true,
		RequestedAt:     time.Now().UTC(),
	}, "validation request")
	require.NoError(t, err)
	require.NotNil(t, manifestRef2)
	require.Equal(t, manifestRef.ArtifactID, manifestRef2.ArtifactID)
	require.Equal(t, analysisRef.MessageID, requestRef.ParentMessageID)
	require.Equal(t, analysisRef.MessageID, requestRef.PreviousMessageID)
	require.Equal(t, "0002-validation-request.json", filepath.Base(requestRef.Path))

	history, manifestRef3, err := store.LoadHistory("run-message-1")
	require.NoError(t, err)
	require.NotNil(t, manifestRef3)
	require.Equal(t, manifestRef.ArtifactID, manifestRef3.ArtifactID)
	require.Len(t, history.Messages, 2)
	require.Equal(t, analysisRef.MessageID, history.Messages[0].MessageID)
	require.Equal(t, requestRef.MessageID, history.Messages[1].MessageID)

	envelope, err := store.LoadEnvelope(analysisRef)
	require.NoError(t, err)
	var message AnalysisHandoffMessage
	require.NoError(t, decodeAgentMessagePayload(envelope, AgentMessageTypeAnalysisHandoff, cfg.AgentMessageSchemaVersion, &message))
	require.Equal(t, AnalysisHandoffSchemaVersion, message.Handoff.SchemaVersion)
	require.Equal(t, handoff.CollectorID, message.Handoff.CollectorID)
	require.Equal(t, handoff.IncidentSummary, message.Handoff.IncidentSummary)
	require.Len(t, message.Handoff.SuggestedValidationTargets, 1)
}
