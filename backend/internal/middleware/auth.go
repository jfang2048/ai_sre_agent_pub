package middleware

import (
	"net/http"
	"strings"
)

// APIKeyAuth implements API Key authentication
func APIKeyAuth(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health checks or static files if needed
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/web") {
			next.ServeHTTP(w, r)
			return
		}

		// Check Header
		authHeader := r.Header.Get("Authorization")
		key := strings.TrimPrefix(authHeader, "Bearer ")

		// Fallback to query param for ease of use
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}

		if key != apiKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
