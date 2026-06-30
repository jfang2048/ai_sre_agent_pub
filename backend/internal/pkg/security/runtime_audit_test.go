package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRuntimeSecurityAuditFlagsInsecureDefaults(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "configs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "deploy", "charts", "sre-agent", "templates"), 0o755))

	controller := `listen: ":8080"
grpc_listen: ":9090"
auth:
  enabled: false
`
	collector := `controller_endpoints:
  - "controller.example.com:9090"
transport:
  tls:
    enabled: false
    insecure_skip_verify: false
external_metrics_cmd: "echo ok; id"
`
	values := `namespace:
  labels:
    pod-security.kubernetes.io/enforce: privileged
securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    add: ["SYS_ADMIN"]
rbac:
  clusterRules:
    - verbs: ["get", "list", "watch", "delete"]
`
	template := "spec:\n  template:\n    spec:\n      hostPID: true\n"

	compose := `services:
  web:
    image: nginx
`

	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "controller.yaml"), []byte(controller), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "collector.yaml"), []byte(collector), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "values.yaml"), []byte(values), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "templates", "deployment.yaml"), []byte(template), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))

	report, err := RunRuntimeSecurityAudit(RuntimeAuditOptions{RepoRoot: root})
	require.NoError(t, err)
	require.True(t, report.HasStatus(RuntimeStatusFail))

	require.Equal(t, RuntimeStatusFail, findCheck(report, "SEC-RUNTIME-001").Status)
	require.Equal(t, RuntimeStatusFail, findCheck(report, "SEC-RUNTIME-002").Status)
	require.Equal(t, RuntimeStatusFail, findCheck(report, "SEC-RUNTIME-003").Status)
	require.Equal(t, RuntimeStatusWarn, findCheck(report, "SEC-RUNTIME-005").Status)
	require.Equal(t, RuntimeStatusWarn, findCheck(report, "SEC-RUNTIME-007").Status)
}

func TestRunRuntimeSecurityAuditPassesHardenedConfig(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "configs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "deploy", "charts", "sre-agent", "templates"), 0o755))

	controller := `listen: "127.0.0.1:8080"
grpc_listen: "127.0.0.1:9090"
auth:
  enabled: true
  api_key_env: "SRE_AGENT_CONTROLLER_API_KEY"
`
	collector := `controller_endpoints:
  - "127.0.0.1:9090"
transport:
  tls:
    enabled: false
    insecure_skip_verify: false
external_metrics_cmd: ""
`
	values := `namespace:
  labels:
    pod-security.kubernetes.io/enforce: baseline
securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    add: []
rbac:
  clusterRules:
    - verbs: ["get", "list", "watch"]
`
	template := "spec:\n  template:\n    spec:\n      hostPID: false\n"

	compose := `services:
  controller:
    image: sre-controller
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
`

	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "controller.yaml"), []byte(controller), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "collector.yaml"), []byte(collector), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "values.yaml"), []byte(values), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "templates", "deployment.yaml"), []byte(template), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))

	report, err := RunRuntimeSecurityAudit(RuntimeAuditOptions{RepoRoot: root})
	require.NoError(t, err)
	require.False(t, report.HasStatus(RuntimeStatusFail))
	require.Equal(t, RuntimeStatusPass, findCheck(report, "SEC-RUNTIME-001").Status)
	require.Equal(t, RuntimeStatusPass, findCheck(report, "SEC-RUNTIME-002").Status)
	require.Equal(t, RuntimeStatusPass, findCheck(report, "SEC-RUNTIME-003").Status)
	require.Equal(t, RuntimeStatusPass, findCheck(report, "SEC-RUNTIME-005").Status)
	require.Equal(t, RuntimeStatusPass, findCheck(report, "SEC-RUNTIME-007").Status)
}

func TestRunRuntimeSecurityAuditWarnsOnReadOnlyControllerAuth(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "configs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "deploy", "charts", "sre-agent", "templates"), 0o755))

	controller := `listen: "0.0.0.0:8080"
grpc_listen: "127.0.0.1:9090"
auth:
  enabled: true
  read_api_key_env: "SRE_AGENT_CONTROLLER_READ_API_KEY"
`
	collector := `controller_endpoints:
  - "127.0.0.1:9090"
transport:
  tls:
    enabled: false
    insecure_skip_verify: false
external_metrics_cmd: ""
`
	values := `namespace:
  labels:
    pod-security.kubernetes.io/enforce: baseline
securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    add: []
rbac:
  clusterRules:
    - verbs: ["get", "list", "watch"]
`
	template := "spec:\n  template:\n    spec:\n      hostPID: false\n"
	compose := `services:
  controller:
    image: sre-controller
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
`

	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "controller.yaml"), []byte(controller), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configs", "collector.yaml"), []byte(collector), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "values.yaml"), []byte(values), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deploy", "charts", "sre-agent", "templates", "deployment.yaml"), []byte(template), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))

	report, err := RunRuntimeSecurityAudit(RuntimeAuditOptions{RepoRoot: root})
	require.NoError(t, err)

	check := findCheck(report, "SEC-RUNTIME-001")
	require.Equal(t, RuntimeStatusWarn, check.Status)
	require.Contains(t, check.Message, "read-only mode")
}

func TestCheckEnvVarSecretExposureDetectsPlaceholder(t *testing.T) {
	t.Setenv("TEST_SECRET_API_KEY", "your-api-key-here")
	check := checkEnvVarSecretExposure()
	require.Equal(t, RuntimeStatusFail, check.Status)
	require.Contains(t, check.Evidence[0], "TEST_SECRET_API_KEY")
}

func TestCheckEnvVarSecretExposurePassesWithRealValues(t *testing.T) {
	t.Setenv("TEST_SECRET_TOKEN", strings.Repeat("a8f29b3c", 5))
	check := checkEnvVarSecretExposure()
	// Should not flag a 40-char hex string as suspicious
	require.NotEqual(t, RuntimeStatusFail, check.Status)
}

func TestCheckDockerComposeSecurityPostureUnhardened(t *testing.T) {
	root := t.TempDir()
	compose := `services:
  web:
    image: nginx
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))
	check := checkDockerComposeSecurityPosture(root)
	require.Equal(t, RuntimeStatusWarn, check.Status)
	require.True(t, len(check.Evidence) > 0)
}

func TestCheckDockerComposeSecurityPostureHardened(t *testing.T) {
	root := t.TempDir()
	compose := `services:
  web:
    image: nginx
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))
	check := checkDockerComposeSecurityPosture(root)
	require.Equal(t, RuntimeStatusPass, check.Status)
}

func TestCheckDockerComposeSecurityPostureWarnsOnBroadPortBinding(t *testing.T) {
	root := t.TempDir()
	compose := `services:
  controller:
    image: sre-controller
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "8080:8080"
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte(compose), 0o644))
	check := checkDockerComposeSecurityPosture(root)
	require.Equal(t, RuntimeStatusWarn, check.Status)
	require.Contains(t, check.Message, "exposes service ports")
	require.Contains(t, strings.Join(check.Evidence, "\n"), "8080:8080")
}

func TestIsBroadHostPortBinding(t *testing.T) {
	testCases := []struct {
		name    string
		binding string
		exposed bool
	}{
		{name: "container only port", binding: "8080", exposed: false},
		{name: "broad host binding", binding: "8080:8080", exposed: true},
		{name: "loopback ipv4 binding", binding: "127.0.0.1:8080:8080", exposed: false},
		{name: "loopback localhost binding", binding: "localhost:8080:8080", exposed: false},
		{name: "broad explicit ip", binding: "0.0.0.0:8080:8080", exposed: true},
		{name: "loopback ipv6 binding", binding: "[::1]:8080:8080", exposed: false},
		{name: "non-loopback ipv6 binding", binding: "[2001:db8::2]:8080:8080", exposed: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.exposed, isBroadHostPortBinding(tc.binding))
		})
	}
}

func findCheck(report RuntimeSecurityReport, id string) RuntimeSecurityCheck {
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	return RuntimeSecurityCheck{}
}
