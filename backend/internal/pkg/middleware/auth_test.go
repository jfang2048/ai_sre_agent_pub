package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyAuthHeaderRequired(t *testing.T) {
	protected := APIKeyAuth(APIKeyAuthConfig{
		ReadKey: "secret",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			return APIKeyAuthScopeRead
		},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("header auth status=%d, want 200", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status?api_key=secret", nil)
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("query auth status=%d, want 401", w.Code)
	}
}
