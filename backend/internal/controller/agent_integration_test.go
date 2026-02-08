package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	"github.com/stretchr/testify/require"
)

func TestParseAgentActionUpdatePayloadNormalizesStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":" COMPLETED ","note":" done "}`))

	status, note, err := parseAgentActionUpdatePayload(req)
	require.NoError(t, err)
	require.Equal(t, agent.ActionStatusCompleted, status)
	require.Equal(t, "done", note)
}

func TestParseAgentActionUpdatePayloadRejectsInvalidStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":"queued"}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseAgentActionUpdatePayloadRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":"completed","extra":1}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseAgentActionUpdatePayloadRequiresStatusOrNote(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"note":"   "}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseNodePath(t *testing.T) {
	node, err := parseNodePath("/api/v1/agent/reports/node-a/latest")
	require.NoError(t, err)
	require.Equal(t, "node-a", node)

	_, err = parseNodePath("/api/v1/agent/reports/node-a/sub")
	require.Error(t, err)

	_, err = parseNodePath("/api/v1/agent/reports/")
	require.Error(t, err)
}
