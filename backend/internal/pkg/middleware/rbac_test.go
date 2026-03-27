package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/security"
	"github.com/stretchr/testify/require"
)

// mockAuditLogger is a simple mock for audit logging
type mockAuditLogger struct {
	events []security.AuditEvent
}

func (m *mockAuditLogger) Log(ctx context.Context, event security.AuditEvent) error {
	m.events = append(m.events, event)
	return nil
}

// TestWithUser validates user context operations
func TestWithUser(t *testing.T) {
	testCases := []struct {
		name  string
		user  *UserContext
		valid bool
	}{
		{
			name: "valid admin user",
			user: &UserContext{
				ID:    "user-1",
				Role:  security.RoleAdmin,
				Email: "admin@example.com",
			},
			valid: true,
		},
		{
			name: "valid SRE user",
			user: &UserContext{
				ID:    "user-2",
				Role:  security.RoleSRE,
				Email: "sre@example.com",
			},
			valid: true,
		},
		{
			name: "valid viewer user",
			user: &UserContext{
				ID:    "user-3",
				Role:  security.RoleViewer,
				Email: "viewer@example.com",
			},
			valid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = WithUser(ctx, tc.user)

			retrieved := GetUserFromContext(ctx)
			require.NotNil(t, retrieved)
			require.Equal(t, tc.user.ID, retrieved.ID)
			require.Equal(t, tc.user.Role, retrieved.Role)
			require.Equal(t, tc.user.Email, retrieved.Email)
		})
	}
}

// TestGetUserFromContextDefaults validates default user behavior
func TestGetUserFromContextDefaults(t *testing.T) {
	ctx := context.Background()

	user := GetUserFromContext(ctx)
	require.NotNil(t, user)
	require.Equal(t, "anonymous", user.ID)
	require.Equal(t, security.RoleViewer, user.Role)
}

// TestHasPermission validates role hierarchy
func TestHasPermission(t *testing.T) {
	testCases := []struct {
		name         string
		userRole     security.UserRole
		requiredRole security.UserRole
		allowed      bool
	}{
		{
			name:         "admin can do admin",
			userRole:     security.RoleAdmin,
			requiredRole: security.RoleAdmin,
			allowed:      true,
		},
		{
			name:         "admin can do SRE",
			userRole:     security.RoleAdmin,
			requiredRole: security.RoleSRE,
			allowed:      true,
		},
		{
			name:         "admin can do viewer",
			userRole:     security.RoleAdmin,
			requiredRole: security.RoleViewer,
			allowed:      true,
		},
		{
			name:         "SRE can do SRE",
			userRole:     security.RoleSRE,
			requiredRole: security.RoleSRE,
			allowed:      true,
		},
		{
			name:         "SRE can do viewer",
			userRole:     security.RoleSRE,
			requiredRole: security.RoleViewer,
			allowed:      true,
		},
		{
			name:         "SRE cannot do admin",
			userRole:     security.RoleSRE,
			requiredRole: security.RoleAdmin,
			allowed:      false,
		},
		{
			name:         "viewer can do viewer",
			userRole:     security.RoleViewer,
			requiredRole: security.RoleViewer,
			allowed:      true,
		},
		{
			name:         "viewer cannot do SRE",
			userRole:     security.RoleViewer,
			requiredRole: security.RoleSRE,
			allowed:      false,
		},
		{
			name:         "viewer cannot do admin",
			userRole:     security.RoleViewer,
			requiredRole: security.RoleAdmin,
			allowed:      false,
		},
		{
			name:         "developer can do viewer",
			userRole:     security.RoleDeveloper,
			requiredRole: security.RoleViewer,
			allowed:      true,
		},
		{
			name:         "developer cannot do SRE",
			userRole:     security.RoleDeveloper,
			requiredRole: security.RoleSRE,
			allowed:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasPermission(tc.userRole, tc.requiredRole)
			require.Equal(t, tc.allowed, result)
		})
	}
}

// TestRBACMiddleware validates RBAC middleware behavior
func TestRBACMiddleware(t *testing.T) {
	testCases := []struct {
		name         string
		userRole     security.UserRole
		requiredRole security.UserRole
		expectStatus int
		expectAudit  int
	}{
		{
			name:         "admin accessing admin endpoint",
			userRole:     security.RoleAdmin,
			requiredRole: security.RoleAdmin,
			expectStatus: http.StatusOK,
			expectAudit:  1,
		},
		{
			name:         "SRE accessing admin endpoint",
			userRole:     security.RoleSRE,
			requiredRole: security.RoleAdmin,
			expectStatus: http.StatusForbidden,
			expectAudit:  1,
		},
		{
			name:         "viewer accessing viewer endpoint",
			userRole:     security.RoleViewer,
			requiredRole: security.RoleViewer,
			expectStatus: http.StatusOK,
			expectAudit:  1,
		},
		{
			name:         "viewer accessing SRE endpoint",
			userRole:     security.RoleViewer,
			requiredRole: security.RoleSRE,
			expectStatus: http.StatusForbidden,
			expectAudit:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAudit := &mockAuditLogger{}

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := RBACMiddleware(tc.requiredRole, mockAudit, next)

			user := &UserContext{
				ID:   "test-user",
				Role: tc.userRole,
			}
			ctx := WithUser(context.Background(), user)

			req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
			require.Len(t, mockAudit.events, tc.expectAudit)

			if tc.expectStatus == http.StatusForbidden {
				require.Equal(t, "forbidden", mockAudit.events[0].Status)
			} else {
				require.Equal(t, "allowed", mockAudit.events[0].Status)
			}
		})
	}
}

// TestRBACMiddlewareAuditLogging validates audit logging behavior
func TestRBACMiddlewareAuditLogging(t *testing.T) {
	mockAudit := &mockAuditLogger{}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RBACMiddleware(security.RoleAdmin, mockAudit, next)

	user := &UserContext{
		ID:   "user-123",
		Role: security.RoleAdmin,
	}
	ctx := WithUser(context.Background(), user)

	req := httptest.NewRequest("POST", "/api/v1/config", nil).WithContext(ctx)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Len(t, mockAudit.events, 1)
	event := mockAudit.events[0]
	require.Equal(t, "user-123", event.Actor)
	require.Equal(t, security.RoleAdmin, event.Role)
	require.Equal(t, "POST", event.Action)
	require.Equal(t, "/api/v1/config", event.Resource)
	require.Equal(t, "allowed", event.Status)
	require.Equal(t, "192.168.1.100:12345", event.IPMessage)
}

// TestRBACHierarchy validates role hierarchy enforcement
func TestRBACHierarchy(t *testing.T) {
	roles := []struct {
		role         security.UserRole
		roleName     string
		canAccess    []security.UserRole
		cannotAccess []security.UserRole
	}{
		{
			role:         security.RoleAdmin,
			roleName:     "admin",
			canAccess:    []security.UserRole{security.RoleAdmin, security.RoleSRE, security.RoleDeveloper, security.RoleViewer},
			cannotAccess: []security.UserRole{},
		},
		{
			role:         security.RoleSRE,
			roleName:     "sre",
			canAccess:    []security.UserRole{security.RoleSRE, security.RoleDeveloper, security.RoleViewer},
			cannotAccess: []security.UserRole{security.RoleAdmin},
		},
		{
			role:         security.RoleDeveloper,
			roleName:     "developer",
			canAccess:    []security.UserRole{security.RoleDeveloper, security.RoleViewer},
			cannotAccess: []security.UserRole{security.RoleSRE, security.RoleAdmin},
		},
		{
			role:         security.RoleViewer,
			roleName:     "viewer",
			canAccess:    []security.UserRole{security.RoleViewer},
			cannotAccess: []security.UserRole{security.RoleDeveloper, security.RoleSRE, security.RoleAdmin},
		},
	}

	for _, tc := range roles {
		t.Run(tc.roleName, func(t *testing.T) {
			for _, allowed := range tc.canAccess {
				t.Run("can_access_"+string(allowed), func(t *testing.T) {
					require.True(t, hasPermission(tc.role, allowed))
				})
			}
			for _, forbidden := range tc.cannotAccess {
				t.Run("cannot_access_"+string(forbidden), func(t *testing.T) {
					require.False(t, hasPermission(tc.role, forbidden))
				})
			}
		})
	}
}

// TestUserContextStructure validates user context structure
func TestUserContextStructure(t *testing.T) {
	user := &UserContext{
		ID:    "user-123",
		Role:  security.RoleAdmin,
		Email: "admin@example.com",
	}

	require.NotEmpty(t, user.ID)
	require.NotEmpty(t, user.Email)
	require.NotEqual(t, security.RoleViewer, user.Role)
}
