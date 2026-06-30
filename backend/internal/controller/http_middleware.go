package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/middleware"
)

type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCaptureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCaptureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusCaptureWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (c *Controller) wrapHTTPHandler(next http.Handler) http.Handler {
	handler := next
	handler = c.guardWriteSensitiveRequests(handler)
	if c.config.API.ActionRateLimitEnabled {
		handler = middleware.RateLimit(middleware.RateLimitConfig{
			Enabled: c.config.API.ActionRateLimitEnabled,
			RPS:     c.config.API.ActionRateLimitRPS,
			Burst:   c.config.API.ActionRateLimitBurst,
			Skip:    c.shouldSkipActionAPIRateLimit,
			OnReject: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "controller action api rate limit exceeded", http.StatusTooManyRequests)
			},
		}, handler)
	}
	if c.config.API.RateLimitEnabled {
		handler = middleware.RateLimit(middleware.RateLimitConfig{
			Enabled: c.config.API.RateLimitEnabled,
			RPS:     c.config.API.RateLimitRPS,
			Burst:   c.config.API.RateLimitBurst,
			Skip:    c.shouldSkipAPIRateLimit,
			OnReject: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "controller api rate limit exceeded", http.StatusTooManyRequests)
			},
		}, handler)
	}
	if c.config.API.AuditMutations {
		handler = c.auditMutationRequests(handler)
	}
	return handler
}

func (c *Controller) guardWriteSensitiveRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.shouldRequireActiveControllerRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !c.requireActiveController(w) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Controller) shouldSkipAPIRateLimit(r *http.Request) bool {
	if r == nil {
		return true
	}
	path := strings.TrimSpace(r.URL.Path)
	if path == "" {
		return true
	}
	switch {
	case path == "/health",
		path == "/healthz",
		path == "/readyz",
		path == "/metrics",
		path == "/api/v1/health",
		strings.HasPrefix(path, "/web"),
		strings.HasPrefix(path, "/assets/"),
		path == "/api/v1/logs/ingest":
		return true
	}
	return !strings.HasPrefix(path, "/api/")
}

func (c *Controller) shouldSkipActionAPIRateLimit(r *http.Request) bool {
	if r == nil {
		return true
	}
	return controllerRequestScope(r) != middleware.APIKeyAuthScopeAction
}

func (c *Controller) shouldRequireActiveControllerRequest(r *http.Request) bool {
	if r == nil || r.Method == http.MethodOptions {
		return false
	}
	return controllerRequestScope(r) == middleware.APIKeyAuthScopeAction
}

func (c *Controller) auditMutationRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil || !shouldAuditMutationRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		rec := &statusCaptureWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		statusCode := rec.statusCode()
		c.appendControllerAudit(r, ControllerAuditRecord{
			Action:   "http_mutation_request",
			Resource: strings.TrimSpace(r.URL.Path),
			Status:   auditStatusFromHTTPCode(statusCode),
			Input: map[string]string{
				"method":      r.Method,
				"status_code": strconv.Itoa(statusCode),
				"remote_addr": strings.TrimSpace(r.RemoteAddr),
				"auth_scope":  string(controllerRequestScope(r)),
				"ha_role":     string(c.haState().Role),
				"ha_active":   strconv.FormatBool(c.haState().Active),
			},
			Output:       http.StatusText(statusCode),
			ApprovalGate: strings.Contains(strings.ToLower(r.URL.Path), "/approve"),
		})
	})
}

func shouldAuditMutationRequest(r *http.Request) bool {
	if r == nil || r.Method == http.MethodOptions {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(r.URL.Path), "/api/") {
		return false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func auditStatusFromHTTPCode(code int) string {
	switch {
	case code >= 200 && code < 400:
		return "success"
	case code == http.StatusUnauthorized ||
		code == http.StatusForbidden ||
		code == http.StatusTooManyRequests ||
		code == http.StatusServiceUnavailable:
		return "blocked"
	case code >= 400 && code < 500:
		return "rejected"
	default:
		return "error"
	}
}
