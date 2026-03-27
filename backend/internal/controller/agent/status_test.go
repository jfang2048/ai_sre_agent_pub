package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeActionStatus(t *testing.T) {
	require.Equal(t, ActionStatusInProgress, NormalizeActionStatus(" In_Progress "))
}

func TestIsValidActionStatus(t *testing.T) {
	require.True(t, IsValidActionStatus("ACKNOWLEDGED"))
	require.True(t, IsValidActionStatus(ActionStatusCompleted))
	require.False(t, IsValidActionStatus("queued"))
}

func TestAllowedActionStatusesIncludesCoreFlow(t *testing.T) {
	statuses := AllowedActionStatuses()
	require.Contains(t, statuses, ActionStatusProposed)
	require.Contains(t, statuses, ActionStatusAcknowledged)
	require.Contains(t, statuses, ActionStatusInProgress)
	require.Contains(t, statuses, ActionStatusCompleted)
	require.Contains(t, statuses, ActionStatusDismissed)
}
