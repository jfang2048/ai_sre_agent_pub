package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type APIKeyAuthScope string

const (
	APIKeyAuthScopePublic APIKeyAuthScope = "public"
	APIKeyAuthScopeRead   APIKeyAuthScope = "read"
	APIKeyAuthScopeAction APIKeyAuthScope = "action"
)

type APIKeyAuthConfig struct {
	ReadKey         string
	ActionKey       string
	ScopeForRequest func(*http.Request) APIKeyAuthScope
}

// APIKeyAuth enforces read and action API credentials on top of a caller-defined
// request scope classifier. Action credentials can always read; read-only mode
// blocks action scopes with 403.
func APIKeyAuth(cfg APIKeyAuthConfig, next http.Handler) http.Handler {
	readKey := strings.TrimSpace(cfg.ReadKey)
	actionKey := strings.TrimSpace(cfg.ActionKey)
	scopeForRequest := cfg.ScopeForRequest
	if scopeForRequest == nil {
		scopeForRequest = defaultAPIKeyAuthScope
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := scopeForRequest(r)
		if scope == APIKeyAuthScopePublic {
			next.ServeHTTP(w, r)
			return
		}

		if scope == APIKeyAuthScopeAction && actionKey == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		key := requestAPIKey(r)
		if key == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		authorized := false
		switch scope {
		case APIKeyAuthScopeAction:
			authorized = compareAPIKeys(key, actionKey)
		case APIKeyAuthScopeRead:
			authorized = compareAPIKeys(key, readKey) || compareAPIKeys(key, actionKey)
		default:
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !authorized {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func defaultAPIKeyAuthScope(r *http.Request) APIKeyAuthScope {
	if r == nil {
		return APIKeyAuthScopePublic
	}

	path := strings.TrimSpace(r.URL.Path)
	switch {
	case r.Method == http.MethodOptions,
		path == "/health",
		path == "/healthz",
		path == "/readyz",
		path == "/metrics",
		path == "/api/v1/health",
		strings.HasPrefix(path, "/web"),
		strings.HasPrefix(path, "/assets/"):
		return APIKeyAuthScopePublic
	default:
		return APIKeyAuthScopeRead
	}
}

func requestAPIKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	if header := strings.TrimSpace(r.Header.Get("X-API-Key")); header != "" {
		return header
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return authHeader
}

func compareAPIKeys(candidate, configured string) bool {
	configured = strings.TrimSpace(configured)
	candidate = strings.TrimSpace(candidate)
	if configured == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(configured)) == 1
}
