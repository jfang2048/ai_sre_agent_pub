package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// APIKeyAuth implements API Key authentication
func APIKeyAuth(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health checks or static files if needed
		if r.URL.Path == "/health" || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/web") {
			next.ServeHTTP(w, r)
			return
		}

		configuredKey := strings.TrimSpace(apiKey)
		if configuredKey == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Check Header
		authHeader := r.Header.Get("Authorization")
		key := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))

		// Use constant-time comparison to prevent timing-based key leakage
		if subtle.ConstantTimeCompare([]byte(key), []byte(configuredKey)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
