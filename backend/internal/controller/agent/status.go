package agent

import "strings"

const (
	ActionStatusProposed     = "proposed"
	ActionStatusAcknowledged = "acknowledged"
	ActionStatusInProgress   = "in_progress"
	ActionStatusCompleted    = "completed"
	ActionStatusDismissed    = "dismissed"
	ActionStatusAccepted     = "accepted"
	ActionStatusRejected     = "rejected"
	ActionStatusCanceled     = "canceled"
)

var actionStatusSet = map[string]struct{}{
	ActionStatusProposed:     {},
	ActionStatusAcknowledged: {},
	ActionStatusInProgress:   {},
	ActionStatusCompleted:    {},
	ActionStatusDismissed:    {},
	ActionStatusAccepted:     {},
	ActionStatusRejected:     {},
	ActionStatusCanceled:     {},
}

var actionStatusList = []string{
	ActionStatusProposed,
	ActionStatusAcknowledged,
	ActionStatusInProgress,
	ActionStatusCompleted,
	ActionStatusDismissed,
	ActionStatusAccepted,
	ActionStatusRejected,
	ActionStatusCanceled,
}

func NormalizeActionStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func IsValidActionStatus(status string) bool {
	_, ok := actionStatusSet[NormalizeActionStatus(status)]
	return ok
}

func AllowedActionStatuses() []string {
	out := make([]string, len(actionStatusList))
	copy(out, actionStatusList)
	return out
}
