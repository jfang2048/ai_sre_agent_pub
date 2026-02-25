package middleware

import (
	"context"
	"net/http"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/security"
)

type key int

const (
	userContextKey key = iota
)

// UserContext holds authenticated user info
type UserContext struct {
	ID    string
	Role  security.UserRole
	Email string
}

// RBACMiddleware enforces role-based access control
// It assumes authentication has already populated the context (or we integrate logic here)
func RBACMiddleware(requiredRole security.UserRole, audit security.AuditLogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Get User from Context (set by Auth middleware)
		// For demo/simplicity, if simple API Key matches Admin, we assume Admin.
		// Real implementation would look up the key in a DB to find the user.
		user := GetUserFromContext(r.Context())

		// 2. Check Role Hierarchy
		if !hasPermission(user.Role, requiredRole) {
			audit.Log(r.Context(), security.AuditEvent{
				Actor:     user.ID,
				Role:      user.Role,
				Action:    r.Method,
				Resource:  r.URL.Path,
				Status:    "forbidden",
				IPMessage: r.RemoteAddr,
			})
			http.Error(w, "Forbidden: Insufficient Permissions", http.StatusForbidden)
			return
		}

		// 3. Log Success (Access Audit)
		// Only for mutation methods or sensitive paths to avoid spam?
		// Audit requirement says "Access logging".
		audit.Log(r.Context(), security.AuditEvent{
			Actor:     user.ID,
			Role:      user.Role,
			Action:    r.Method,
			Resource:  r.URL.Path,
			Status:    "allowed",
			IPMessage: r.RemoteAddr,
		})

		next.ServeHTTP(w, r)
	})
}

// WithUser adds user to context
func WithUser(ctx context.Context, user *UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// GetUserFromContext retrieves user
func GetUserFromContext(ctx context.Context) *UserContext {
	u, ok := ctx.Value(userContextKey).(*UserContext)
	if !ok {
		// Default to anonymous viewer if not set
		return &UserContext{ID: "anonymous", Role: security.RoleViewer}
	}
	return u
}

// hasPermission checks role hierarchy
func hasPermission(userRole, requiredRole security.UserRole) bool {
	// Simple hierarchy: Admin > SRE > Developer > Viewer
	roles := map[security.UserRole]int{
		security.RoleAdmin:     4,
		security.RoleSRE:       3,
		security.RoleDeveloper: 2,
		security.RoleViewer:    1,
	}

	return roles[userRole] >= roles[requiredRole]
}
