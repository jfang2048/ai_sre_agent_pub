package security

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// UserRole defines RBAC roles
type UserRole string

const (
	RoleAdmin     UserRole = "admin"
	RoleSRE       UserRole = "sre"
	RoleDeveloper UserRole = "developer"
	RoleViewer    UserRole = "viewer"
)

// AuditEvent represents a single audit log entry
type AuditEvent struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	Actor     string                 `json:"actor"` // User ID or Service Account
	Role      UserRole               `json:"role"`
	Action    string                 `json:"action"`     // create, update, delete, login
	Resource  string                 `json:"resource"`   // incident/123, config/yaml
	Status    string                 `json:"status"`     // success, failure
	IPMessage string                 `json:"ip_address"` // Source IP
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// AuditLogger defines the interface for recording audit events
type AuditLogger interface {
	Log(ctx context.Context, event AuditEvent) error
}

// ZapAuditLogger logs events to specialized structured logs (tamper-evident if shipped)
type ZapAuditLogger struct {
	logger *zap.Logger
}

func NewZapAuditLogger(logger *zap.Logger) *ZapAuditLogger {
	return &ZapAuditLogger{
		logger: logger.With(zap.String("log_type", "audit")),
	}
}

func (l *ZapAuditLogger) Log(ctx context.Context, event AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Mask PII in metadata before logging
	safeMetadata := MaskPII(event.Metadata)

	l.logger.Info("audit_event",
		zap.String("actor", event.Actor),
		zap.String("role", string(event.Role)),
		zap.String("action", event.Action),
		zap.String("resource", event.Resource),
		zap.String("status", event.Status),
		zap.String("ip", event.IPMessage),
		zap.Any("metadata", safeMetadata),
	)
	return nil
}

// Global PII Masking utility
func MaskPII(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	masked := make(map[string]interface{}, len(data))
	for k, v := range data {
		// Detect sensitive keys
		if isSensitive(k) {
			masked[k] = "***"
		} else {
			masked[k] = v
		}
	}
	return masked
}

func isSensitive(key string) bool {
	sensitiveKeys := []string{"password", "token", "secret", "email", "ssn", "credit_card"}
	for _, sk := range sensitiveKeys {
		if key == sk {
			return true
		}
	}
	return false
}
