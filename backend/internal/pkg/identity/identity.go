package identity

import (
	"context"
	"strings"
	"time"
)

type ActorType string

const (
	ActorTypeUnknown  ActorType = "unknown"
	ActorTypeUser     ActorType = "user"
	ActorTypeService  ActorType = "service"
	ActorTypeInternal ActorType = "internal"
)

type Role string

const (
	RoleViewer    Role = "viewer"
	RoleOperator  Role = "operator"
	RoleApprover  Role = "approver"
	RoleAdmin     Role = "admin"
	RoleCollector Role = "collector"
)

type AuthnMethod string

const (
	AuthnMethodAnonymous  AuthnMethod = "anonymous"
	AuthnMethodBearer     AuthnMethod = "bearer_token"
	AuthnMethodAPIKey     AuthnMethod = "api_key"
	AuthnMethodIngestAuth AuthnMethod = "ingest_bearer_token"
)

type Identity struct {
	Subject     string      `json:"subject"`
	ActorType   ActorType   `json:"actor_type"`
	Roles       []Role      `json:"roles,omitempty"`
	AuthnMethod AuthnMethod `json:"authn_method"`
	IssuedAt    time.Time   `json:"issued_at,omitempty"`
	ExpiresAt   time.Time   `json:"expires_at,omitempty"`
	SourceIP    string      `json:"source_ip,omitempty"`
	Audience    string      `json:"audience,omitempty"`
	CollectorID string      `json:"collector_id,omitempty"`
}

func (id *Identity) HasAnyRole(required ...Role) bool {
	if id == nil || len(required) == 0 {
		return false
	}
	for _, have := range id.Roles {
		for _, need := range required {
			if have == need {
				return true
			}
		}
	}
	return false
}

func (id *Identity) RolesCSV() string {
	if id == nil || len(id.Roles) == 0 {
		return ""
	}
	parts := make([]string, 0, len(id.Roles))
	for _, role := range id.Roles {
		parts = append(parts, string(role))
	}
	return strings.Join(parts, ",")
}

func NormalizeActorType(raw string) ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user":
		return ActorTypeUser
	case "service":
		return ActorTypeService
	case "internal":
		return ActorTypeInternal
	default:
		return ActorTypeUnknown
	}
}

func NormalizeRoles(raw []string) []Role {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[Role]struct{}, len(raw))
	out := make([]Role, 0, len(raw))
	for _, item := range raw {
		role := NormalizeRole(item)
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	return out
}

func NormalizeRole(raw string) Role {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RoleViewer), "read_only_analyst", "analyst":
		return RoleViewer
	case string(RoleOperator):
		return RoleOperator
	case string(RoleApprover):
		return RoleApprover
	case string(RoleAdmin):
		return RoleAdmin
	case string(RoleCollector):
		return RoleCollector
	default:
		return ""
	}
}

func ParseRolesCSV(raw string) []Role {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	return NormalizeRoles(values)
}

type contextKey string

const identityContextKey contextKey = "controller_identity"

func WithContext(ctx context.Context, id *Identity) context.Context {
	if id == nil {
		return ctx
	}
	return context.WithValue(ctx, identityContextKey, id)
}

func FromContext(ctx context.Context) (*Identity, bool) {
	if ctx == nil {
		return nil, false
	}
	id, ok := ctx.Value(identityContextKey).(*Identity)
	if !ok || id == nil {
		return nil, false
	}
	return id, true
}
