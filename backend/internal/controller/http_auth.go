package controller

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/identity"
)

type controllerAuthCounters struct {
	httpAuthnFailures atomic.Uint64
	httpAuthzFailures atomic.Uint64
}

type controllerRoutePolicy struct {
	Public       bool
	ReadLike     bool
	RequiredRole identity.Role
}

var errHTTPAuthFailed = errors.New("http authentication failed")
var errHTTPAuthzFailed = errors.New("http authorization failed")

func controllerRoutePolicyForRequest(r *http.Request) controllerRoutePolicy {
	if r == nil {
		return controllerRoutePolicy{Public: true}
	}
	if r.Method == http.MethodOptions {
		return controllerRoutePolicy{Public: true}
	}

	path := strings.TrimSpace(r.URL.Path)
	if path == "" {
		return controllerRoutePolicy{Public: true}
	}
	switch {
	case path == "/health",
		path == "/healthz",
		path == "/readyz",
		path == "/metrics",
		path == "/api/v1/health",
		path == "/",
		strings.HasPrefix(path, "/web"),
		strings.HasPrefix(path, "/assets/"):
		return controllerRoutePolicy{Public: true}
	}
	if !strings.HasPrefix(path, "/api/") {
		return controllerRoutePolicy{Public: true}
	}
	if path == "/api/v1/agent/query" || path == "/api/v1/rag/query" {
		return controllerRoutePolicy{ReadLike: true, RequiredRole: identity.RoleViewer}
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return controllerRoutePolicy{ReadLike: true, RequiredRole: identity.RoleViewer}
	}

	switch {
	case path == "/api/v1/controller/actions/approve",
		isIncidentActionPath(path, "approve"):
		return controllerRoutePolicy{RequiredRole: identity.RoleApprover}
	case path == "/api/v1/controller/actions/dry-run",
		path == "/api/v1/controller/actions/execute",
		path == "/api/v1/controller/actions/rollback",
		path == "/api/v1/controller/incidents/intake",
		path == "/api/v1/controller/agent/runs",
		isControllerAgentRunStopPath(path),
		path == "/api/v1/agent/execute",
		strings.HasPrefix(path, "/api/v1/agent/actions/"),
		isIncidentActionPath(path, "execute"),
		isIncidentActionPath(path, "rollback"),
		path == "/api/v1/logs/ingest":
		return controllerRoutePolicy{RequiredRole: identity.RoleOperator}
	default:
		return controllerRoutePolicy{RequiredRole: identity.RoleAdmin}
	}
}

func isIncidentActionPath(path, action string) bool {
	path = strings.TrimSpace(path)
	suffix := "/" + strings.TrimSpace(action)
	return strings.HasPrefix(path, "/api/v1/agent/incidents/") &&
		strings.Contains(path, "/actions/") &&
		strings.HasSuffix(path, suffix)
}

func isControllerAgentRunStopPath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(path, "/api/v1/controller/agent/runs/") && strings.HasSuffix(path, "/stop")
}

func (c *Controller) wrapHTTPAuthentication(next http.Handler) http.Handler {
	if c == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := controllerRoutePolicyForRequest(r)
		if policy.Public {
			next.ServeHTTP(w, r)
			return
		}
		if !c.auth.Enabled {
			next.ServeHTTP(w, r.WithContext(identity.WithContext(r.Context(), anonymousHTTPIdentity(r))))
			return
		}

		actor, err := c.authenticateHTTPRequest(r)
		if err != nil {
			c.authCounters.httpAuthnFailures.Add(1)
			if shouldAuditMutationRequest(r) {
				c.appendControllerAudit(r, ControllerAuditRecord{
					Action:   "http_authenticate",
					Resource: strings.TrimSpace(r.URL.Path),
					Status:   "unauthorized",
					Output:   err.Error(),
				})
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !authorizeControllerRequest(actor, policy.RequiredRole) {
			c.authCounters.httpAuthzFailures.Add(1)
			if shouldAuditMutationRequest(r) {
				c.appendControllerAudit(r.WithContext(identity.WithContext(r.Context(), actor)), ControllerAuditRecord{
					Action:   "http_authorize",
					Resource: strings.TrimSpace(r.URL.Path),
					Status:   "forbidden",
					Output:   errHTTPAuthzFailed.Error(),
				})
			}
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithContext(r.Context(), actor)))
	})
}

func (c *Controller) authenticateHTTPRequest(r *http.Request) (*identity.Identity, error) {
	if c == nil {
		return nil, errHTTPAuthFailed
	}
	sourceIP := remoteIPFromRequest(r)
	switch c.auth.Mode {
	case ControllerAuthModeToken:
		token := bearerTokenFromRequest(r)
		if token == "" {
			return nil, errHTTPAuthFailed
		}
		return c.verifyBearerActor(token, c.auth.TokenAudience, identity.AuthnMethodBearer, sourceIP)
	case ControllerAuthModeMixed:
		if token := bearerTokenFromRequest(r); token != "" {
			return c.verifyBearerActor(token, c.auth.TokenAudience, identity.AuthnMethodBearer, sourceIP)
		}
		return c.identityFromAPIKey(r, sourceIP)
	case ControllerAuthModeAPIKey:
		return c.identityFromAPIKey(r, sourceIP)
	default:
		return nil, errHTTPAuthFailed
	}
}

func (c *Controller) verifyBearerActor(token string, audience string, method identity.AuthnMethod, sourceIP string) (*identity.Identity, error) {
	if c == nil || strings.TrimSpace(c.auth.TokenSecret) == "" {
		return nil, errHTTPAuthFailed
	}
	return identity.VerifyToken([]byte(c.auth.TokenSecret), token, identity.VerifyOptions{
		Issuer:   c.auth.TokenIssuer,
		Audience: audience,
		Method:   method,
		SourceIP: sourceIP,
	})
}

func (c *Controller) identityFromAPIKey(r *http.Request, sourceIP string) (*identity.Identity, error) {
	if c == nil {
		return nil, errHTTPAuthFailed
	}
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" && c.auth.Mode == ControllerAuthModeAPIKey {
		key = bearerTokenFromRequest(r)
	}
	if key == "" {
		return nil, errHTTPAuthFailed
	}
	switch {
	case compareControllerSecret(key, c.auth.ActionKey):
		return &identity.Identity{
			Subject:     "compat-api-key-action",
			ActorType:   identity.ActorTypeService,
			Roles:       []identity.Role{identity.RoleAdmin},
			AuthnMethod: identity.AuthnMethodAPIKey,
			SourceIP:    sourceIP,
			Audience:    c.auth.TokenAudience,
		}, nil
	case compareControllerSecret(key, c.auth.ReadKey):
		return &identity.Identity{
			Subject:     "compat-api-key-read",
			ActorType:   identity.ActorTypeService,
			Roles:       []identity.Role{identity.RoleViewer},
			AuthnMethod: identity.AuthnMethodAPIKey,
			SourceIP:    sourceIP,
			Audience:    c.auth.TokenAudience,
		}, nil
	default:
		return nil, errHTTPAuthFailed
	}
}

func authorizeControllerRequest(actor *identity.Identity, required identity.Role) bool {
	if required == "" {
		return actor != nil
	}
	if actor == nil {
		return false
	}
	if actor.HasAnyRole(identity.RoleAdmin) {
		return true
	}
	switch required {
	case identity.RoleViewer:
		return actor.HasAnyRole(identity.RoleViewer, identity.RoleOperator, identity.RoleApprover)
	case identity.RoleOperator:
		return actor.HasAnyRole(identity.RoleOperator)
	case identity.RoleApprover:
		return actor.HasAnyRole(identity.RoleApprover)
	case identity.RoleCollector:
		return actor.HasAnyRole(identity.RoleCollector)
	default:
		return false
	}
}

func anonymousHTTPIdentity(r *http.Request) *identity.Identity {
	return &identity.Identity{
		Subject:     "anonymous",
		ActorType:   identity.ActorTypeUnknown,
		AuthnMethod: identity.AuthnMethodAnonymous,
		SourceIP:    remoteIPFromRequest(r),
	}
}

func bearerTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return ""
	}
	return strings.TrimSpace(authHeader[7:])
}

func remoteIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func compareControllerSecret(candidate, configured string) bool {
	candidate = strings.TrimSpace(candidate)
	configured = strings.TrimSpace(configured)
	if candidate == "" || configured == "" {
		return false
	}
	if len(candidate) != len(configured) {
		return false
	}
	return subtleTokenCompare(candidate, configured) == 1
}
