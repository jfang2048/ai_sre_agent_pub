package controller

import (
	"net/http"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/middleware"
)

func controllerRequestScope(r *http.Request) middleware.APIKeyAuthScope {
	if r == nil {
		return middleware.APIKeyAuthScopePublic
	}
	if r.Method == http.MethodOptions {
		return middleware.APIKeyAuthScopePublic
	}

	path := strings.TrimSpace(r.URL.Path)
	if path == "" {
		return middleware.APIKeyAuthScopePublic
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
		return middleware.APIKeyAuthScopePublic
	}

	if !strings.HasPrefix(path, "/api/") {
		return middleware.APIKeyAuthScopePublic
	}

	switch {
	case path == "/api/v1/agent/query",
		path == "/api/v1/rag/query":
		return middleware.APIKeyAuthScopeRead
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return middleware.APIKeyAuthScopeRead
	default:
		return middleware.APIKeyAuthScopeAction
	}
}
