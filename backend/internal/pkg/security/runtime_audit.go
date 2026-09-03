package security

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type RuntimeSecurityStatus string

const (
	RuntimeStatusPass RuntimeSecurityStatus = "pass"
	RuntimeStatusWarn RuntimeSecurityStatus = "warn"
	RuntimeStatusFail RuntimeSecurityStatus = "fail"
)

type RuntimeAuditOptions struct {
	RepoRoot string
}

type RuntimeSecurityCheck struct {
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	Status          RuntimeSecurityStatus `json:"status"`
	Severity        string                `json:"severity"`
	Message         string                `json:"message"`
	Evidence        []string              `json:"evidence,omitempty"`
	Recommendations []string              `json:"recommendations,omitempty"`
}

type RuntimeSecuritySummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type RuntimeSecurityReport struct {
	GeneratedAt time.Time              `json:"generated_at"`
	RepoRoot    string                 `json:"repo_root"`
	Checks      []RuntimeSecurityCheck `json:"checks"`
	Summary     RuntimeSecuritySummary `json:"summary"`
}

func RunRuntimeSecurityAudit(opts RuntimeAuditOptions) (RuntimeSecurityReport, error) {
	root := strings.TrimSpace(opts.RepoRoot)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return RuntimeSecurityReport{}, fmt.Errorf("resolve repo root: %w", err)
	}

	report := RuntimeSecurityReport{
		GeneratedAt: time.Now().UTC(),
		RepoRoot:    absRoot,
		Checks: []RuntimeSecurityCheck{
			checkControllerAuthAndExposure(absRoot),
			checkCollectorTransportSecurity(absRoot),
			checkExternalMetricsCommandSafety(absRoot),
			checkRuntimeFilePermissions(absRoot),
			checkHelmLeastPrivilegeDefaults(absRoot),
			checkEnvVarSecretExposure(),
			checkDockerComposeSecurityPosture(absRoot),
		},
	}
	report.Summary = summarizeChecks(report.Checks)
	return report, nil
}

func (r RuntimeSecurityReport) HasStatus(status RuntimeSecurityStatus) bool {
	for _, check := range r.Checks {
		if check.Status == status {
			return true
		}
	}
	return false
}

func FormatRuntimeSecurityReport(report RuntimeSecurityReport, format string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "markdown", "md":
		return []byte(formatRuntimeReportMarkdown(report)), nil
	case "json":
		return json.MarshalIndent(report, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported format %q (expected markdown or json)", format)
	}
}

func summarizeChecks(checks []RuntimeSecurityCheck) RuntimeSecuritySummary {
	s := RuntimeSecuritySummary{}
	for _, check := range checks {
		switch check.Status {
		case RuntimeStatusPass:
			s.Pass++
		case RuntimeStatusWarn:
			s.Warn++
		case RuntimeStatusFail:
			s.Fail++
		}
	}
	return s
}

func formatRuntimeReportMarkdown(report RuntimeSecurityReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Runtime Security Audit Report\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", report.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Repo root: `%s`\n", report.RepoRoot)
	fmt.Fprintf(&b, "- Summary: pass=%d warn=%d fail=%d\n\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail)

	b.WriteString("| ID | Status | Severity | Title | Message |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			check.ID, check.Status, check.Severity, escapeMarkdownPipe(check.Title), escapeMarkdownPipe(check.Message))
	}
	b.WriteString("\n")

	for _, check := range report.Checks {
		fmt.Fprintf(&b, "## %s - %s\n\n", check.ID, check.Title)
		fmt.Fprintf(&b, "- Status: `%s`\n", check.Status)
		fmt.Fprintf(&b, "- Severity: `%s`\n", check.Severity)
		fmt.Fprintf(&b, "- Message: %s\n", check.Message)
		if len(check.Evidence) > 0 {
			b.WriteString("- Evidence:\n")
			for _, item := range check.Evidence {
				fmt.Fprintf(&b, "  - `%s`\n", item)
			}
		}
		if len(check.Recommendations) > 0 {
			b.WriteString("- Recommendations:\n")
			for _, item := range check.Recommendations {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func escapeMarkdownPipe(input string) string {
	return strings.ReplaceAll(input, "|", "\\|")
}

type controllerRuntimeConfig struct {
	Listen     string `yaml:"listen"`
	GRPCListen string `yaml:"grpc_listen"`
	Auth       struct {
		Enabled         bool   `yaml:"enabled"`
		APIKeyEnv       string `yaml:"api_key_env"`
		ReadAPIKeyEnv   string `yaml:"read_api_key_env"`
		ActionAPIKeyEnv string `yaml:"action_api_key_env"`
	} `yaml:"auth"`
}

func checkControllerAuthAndExposure(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-001",
		Title:    "Controller exposure and API authentication",
		Status:   RuntimeStatusPass,
		Severity: "high",
		Message:  "controller listen surfaces are constrained",
	}

	path := filepath.Join(root, "configs", "controller.yaml")
	var cfg controllerRuntimeConfig
	if err := loadYAMLFile(path, &cfg); err != nil {
		check.Status = RuntimeStatusWarn
		check.Message = "controller config is missing or unreadable; runtime exposure cannot be validated"
		check.Evidence = []string{fmt.Sprintf("%s (%v)", path, err)}
		check.Recommendations = []string{"Provide configs/controller.yaml for automated runtime security validation."}
		return check
	}

	publicHTTP := !isLoopbackListen(cfg.Listen)
	publicGRPC := !isLoopbackListen(cfg.GRPCListen)
	authEnabled := cfg.Auth.Enabled
	sharedEnv := strings.TrimSpace(cfg.Auth.APIKeyEnv)
	readEnv := strings.TrimSpace(cfg.Auth.ReadAPIKeyEnv)
	actionEnv := strings.TrimSpace(cfg.Auth.ActionAPIKeyEnv)
	hasShared := sharedEnv != ""
	hasRead := readEnv != ""
	hasAction := actionEnv != ""

	if publicHTTP && !authEnabled {
		check.Status = RuntimeStatusFail
		check.Message = "controller HTTP listens beyond loopback while API authentication is disabled"
		check.Evidence = append(check.Evidence,
			fmt.Sprintf("listen=%q", cfg.Listen),
			fmt.Sprintf("auth.enabled=%t", cfg.Auth.Enabled),
		)
		check.Recommendations = append(check.Recommendations,
			"Enable auth.enabled and provide auth.api_key_env or split auth.read_api_key_env/auth.action_api_key_env before exposing controller HTTP externally.",
			"Bind listen to 127.0.0.1 in local development unless an authenticated reverse proxy is in front.",
		)
	} else if publicGRPC {
		check.Status = RuntimeStatusWarn
		check.Message = "controller gRPC ingest listens beyond loopback; enforce network isolation and mTLS in deployment"
		check.Evidence = append(check.Evidence, fmt.Sprintf("grpc_listen=%q", cfg.GRPCListen))
		check.Recommendations = append(check.Recommendations,
			"Restrict gRPC ingest to private networks or enforce mTLS/authenticated service-to-service ingress.",
		)
	}

	if authEnabled && !hasShared && !hasRead && !hasAction {
		if check.Status == RuntimeStatusPass {
			check.Status = RuntimeStatusWarn
		}
		check.Message = "controller API auth is enabled but no auth key env is configured"
		check.Evidence = append(check.Evidence, "auth.api_key_env, auth.read_api_key_env, and auth.action_api_key_env are empty")
		check.Recommendations = append(check.Recommendations,
			"Set auth.api_key_env for shared mode, or auth.read_api_key_env and auth.action_api_key_env for split mode.",
		)
	}
	if authEnabled && !hasShared && hasRead && !hasAction {
		if check.Status == RuntimeStatusPass {
			check.Status = RuntimeStatusWarn
		}
		check.Message = "controller API auth is enabled in read-only mode; action endpoints stay blocked until action_api_key_env is configured"
		check.Evidence = append(check.Evidence, fmt.Sprintf("auth.read_api_key_env=%q", readEnv))
		check.Recommendations = append(check.Recommendations,
			"Configure auth.action_api_key_env before exposing controller action endpoints to operators or automation.",
		)
	}
	if authEnabled && hasShared && (hasRead || hasAction) {
		check.Evidence = append(check.Evidence,
			fmt.Sprintf("auth.api_key_env=%q", sharedEnv),
			fmt.Sprintf("auth.read_api_key_env=%q", readEnv),
			fmt.Sprintf("auth.action_api_key_env=%q", actionEnv),
		)
		if check.Status == RuntimeStatusPass {
			check.Status = RuntimeStatusWarn
			check.Message = "controller auth mixes shared and split-key config; prefer one mode to avoid ambiguous operator rollout"
			check.Recommendations = append(check.Recommendations,
				"Use either auth.api_key_env alone for shared mode or auth.read_api_key_env plus auth.action_api_key_env for split mode.",
			)
		}
	}

	return check
}

type collectorRuntimeConfig struct {
	ControllerEndpoints []string `yaml:"controller_endpoints"`
	ExternalMetricsCmd  string   `yaml:"external_metrics_cmd"`
	Transport           struct {
		TLS struct {
			Enabled            bool `yaml:"enabled"`
			InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
	} `yaml:"transport"`
}

func checkCollectorTransportSecurity(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-002",
		Title:    "Collector transport TLS posture",
		Status:   RuntimeStatusPass,
		Severity: "high",
		Message:  "collector transport TLS defaults are acceptable for detected endpoint scope",
	}

	path := filepath.Join(root, "configs", "collector.yaml")
	var cfg collectorRuntimeConfig
	if err := loadYAMLFile(path, &cfg); err != nil {
		check.Status = RuntimeStatusWarn
		check.Message = "collector config is missing or unreadable; transport security cannot be validated"
		check.Evidence = []string{fmt.Sprintf("%s (%v)", path, err)}
		check.Recommendations = []string{"Provide configs/collector.yaml for automated TLS posture checks."}
		return check
	}

	if cfg.Transport.TLS.InsecureSkipVerify {
		check.Status = RuntimeStatusFail
		check.Message = "collector transport allows insecure TLS certificate verification bypass"
		check.Evidence = append(check.Evidence, "transport.tls.insecure_skip_verify=true")
		check.Recommendations = append(check.Recommendations,
			"Disable insecure_skip_verify and supply a trusted CA bundle via transport.tls.ca_file.",
		)
		return check
	}

	publicEndpoints := make([]string, 0)
	for _, endpoint := range cfg.ControllerEndpoints {
		if endpoint == "" {
			continue
		}
		host := hostFromEndpoint(endpoint)
		if host == "" {
			continue
		}
		if !isLoopbackHost(host) {
			publicEndpoints = append(publicEndpoints, endpoint)
		}
	}
	sort.Strings(publicEndpoints)

	if len(publicEndpoints) > 0 && !cfg.Transport.TLS.Enabled {
		check.Status = RuntimeStatusFail
		check.Message = "collector sends telemetry to non-loopback endpoints without TLS enabled"
		check.Evidence = append(check.Evidence, fmt.Sprintf("controller_endpoints=%v", publicEndpoints))
		check.Recommendations = append(check.Recommendations,
			"Enable transport.tls.enabled and configure CA/cert material for remote controller endpoints.",
		)
	}

	return check
}

func checkExternalMetricsCommandSafety(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-003",
		Title:    "External metrics command safety",
		Status:   RuntimeStatusPass,
		Severity: "medium",
		Message:  "external metrics command is disabled or shell-safe",
	}

	path := filepath.Join(root, "configs", "collector.yaml")
	var cfg collectorRuntimeConfig
	if err := loadYAMLFile(path, &cfg); err != nil {
		check.Status = RuntimeStatusWarn
		check.Message = "collector config is missing or unreadable; external command execution cannot be validated"
		check.Evidence = []string{fmt.Sprintf("%s (%v)", path, err)}
		check.Recommendations = []string{"Provide configs/collector.yaml for external command execution checks."}
		return check
	}

	command := strings.TrimSpace(cfg.ExternalMetricsCmd)
	if command == "" {
		return check
	}
	if strings.ContainsAny(command, "|;&><`()$\\\n\r") {
		check.Status = RuntimeStatusFail
		check.Message = "external_metrics_cmd contains shell control operators"
		check.Evidence = append(check.Evidence, fmt.Sprintf("external_metrics_cmd=%q", command))
		check.Recommendations = append(check.Recommendations,
			"Replace shell pipelines/chaining with a dedicated helper binary or script path plus explicit argv.",
		)
		return check
	}

	check.Status = RuntimeStatusWarn
	check.Message = "external_metrics_cmd is enabled; treat executable path and arguments as high-trust runtime input"
	check.Evidence = append(check.Evidence, fmt.Sprintf("external_metrics_cmd=%q", command))
	check.Recommendations = append(check.Recommendations,
		"Pin external_metrics_cmd to an immutable absolute path owned by root and writable only by trusted operators.",
	)
	return check
}

func checkRuntimeFilePermissions(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-004",
		Title:    "Sensitive runtime file permissions",
		Status:   RuntimeStatusPass,
		Severity: "medium",
		Message:  "runtime-sensitive files are not group/world accessible",
	}

	envSensitivePaths := map[string]string{
		"SRE_AGENT_CONFIG":            strings.TrimSpace(os.Getenv("SRE_AGENT_CONFIG")),
		"SRE_COLLECTOR_CONFIG":        strings.TrimSpace(os.Getenv("SRE_COLLECTOR_CONFIG")),
		"SRE_AGENT_MEMORY_FILE":       strings.TrimSpace(os.Getenv("SRE_AGENT_MEMORY_FILE")),
		"SRE_COLLECTOR_TLS_CA_FILE":   strings.TrimSpace(os.Getenv("SRE_COLLECTOR_TLS_CA_FILE")),
		"SRE_COLLECTOR_TLS_CERT_FILE": strings.TrimSpace(os.Getenv("SRE_COLLECTOR_TLS_CERT_FILE")),
		"SRE_COLLECTOR_TLS_KEY_FILE":  strings.TrimSpace(os.Getenv("SRE_COLLECTOR_TLS_KEY_FILE")),
	}

	checked := 0
	for envName, path := range envSensitivePaths {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			check.Status = RuntimeStatusWarn
			check.Evidence = append(check.Evidence, fmt.Sprintf("%s=%s (unreadable: %v)", envName, path, err))
			continue
		}
		checked++
		mode := info.Mode().Perm()
		if mode&0o077 != 0 {
			check.Status = RuntimeStatusFail
			check.Message = "one or more runtime-sensitive files are readable/writable by group or others"
			check.Evidence = append(check.Evidence, fmt.Sprintf("%s=%s mode=%#o", envName, path, mode))
			check.Recommendations = append(check.Recommendations,
				"Set permissions to owner-only (0600 for files, 0700 for containing directories).",
			)
		}
	}

	if checked == 0 && check.Status == RuntimeStatusPass {
		check.Status = RuntimeStatusWarn
		check.Message = "no runtime-sensitive env-backed file paths were provided; permission checks were limited"
		check.Recommendations = append(check.Recommendations,
			"Run security-audit with production-equivalent env vars to validate key/config file permissions.",
		)
	}
	return check
}

type workloadSecurityContext struct {
	RunAsNonRoot             *bool `yaml:"runAsNonRoot"`
	AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
	Capabilities             struct {
		Add  []string `yaml:"add"`
		Drop []string `yaml:"drop"`
	} `yaml:"capabilities"`
}

type chartValuesSecurity struct {
	Namespace struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"namespace"`
	Controller struct {
		SecurityContext workloadSecurityContext `yaml:"securityContext"`
	} `yaml:"controller"`
	Collector struct {
		PrivilegeProfile        string                  `yaml:"privilegeProfile"`
		SecurityContext         workloadSecurityContext `yaml:"securityContext"`
		DeepRuntimeCapabilities []string                `yaml:"deepRuntimeCapabilities"`
	} `yaml:"collector"`
	RBAC struct {
		ControllerClusterRules []struct {
			Verbs []string `yaml:"verbs"`
		} `yaml:"controllerClusterRules"`
	} `yaml:"rbac"`
}

func checkHelmLeastPrivilegeDefaults(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-005",
		Title:    "Helm defaults and least privilege",
		Status:   RuntimeStatusPass,
		Severity: "high",
		Message:  "chart defaults align with least-privilege expectations",
	}

	valuesPath := filepath.Join(root, "deploy", "charts", "sre-agent", "values.yaml")
	var values chartValuesSecurity
	if err := loadYAMLFile(valuesPath, &values); err != nil {
		check.Status = RuntimeStatusWarn
		check.Message = "helm values are missing or unreadable; least-privilege defaults cannot be validated"
		check.Evidence = []string{fmt.Sprintf("%s (%v)", valuesPath, err)}
		check.Recommendations = []string{"Provide deploy/charts/sre-agent/values.yaml for privilege baseline checks."}
		return check
	}

	if strings.EqualFold(strings.TrimSpace(values.Namespace.Labels["pod-security.kubernetes.io/enforce"]), "privileged") {
		check.Status = RuntimeStatusWarn
		check.Message = "namespace default pod-security enforce level is privileged"
		check.Evidence = append(check.Evidence, "namespace.labels.pod-security.kubernetes.io/enforce=privileged")
		check.Recommendations = append(check.Recommendations,
			"Default pod-security enforcement to baseline/restricted and elevate only where low-level probes explicitly require it.",
		)
	}
	controllerSecurity := values.Controller.SecurityContext
	if controllerSecurity.RunAsNonRoot == nil || !*controllerSecurity.RunAsNonRoot {
		check.Status = RuntimeStatusFail
		check.Message = "controller securityContext does not enforce runAsNonRoot"
		check.Evidence = append(check.Evidence, "controller.securityContext.runAsNonRoot is missing or false")
	}
	if controllerSecurity.AllowPrivilegeEscalation == nil || *controllerSecurity.AllowPrivilegeEscalation {
		check.Status = RuntimeStatusFail
		check.Message = "controller securityContext does not explicitly disable privilege escalation"
		check.Evidence = append(check.Evidence, "controller.securityContext.allowPrivilegeEscalation is missing or true")
	}

	dangerousCaps := map[string]struct{}{
		"SYS_ADMIN": {},
		"NET_ADMIN": {},
		"BPF":       {},
		"PERFMON":   {},
	}
	foundCaps := make([]string, 0)
	for _, capName := range controllerSecurity.Capabilities.Add {
		trimmed := strings.TrimSpace(strings.ToUpper(capName))
		if _, ok := dangerousCaps[trimmed]; ok {
			foundCaps = append(foundCaps, trimmed)
		}
	}
	if len(foundCaps) > 0 && check.Status != RuntimeStatusFail {
		check.Status = RuntimeStatusWarn
		check.Message = "chart default capabilities include privileged kernel/network scopes"
		sort.Strings(foundCaps)
		check.Evidence = append(check.Evidence, fmt.Sprintf("controller.securityContext.capabilities.add=%v", foundCaps))
		check.Recommendations = append(check.Recommendations,
			"Keep privileged capabilities empty by default and enable them only in dedicated hardened deployment overlays.",
		)
	}

	mutatingVerbFound := false
	for _, rule := range values.RBAC.ControllerClusterRules {
		for _, verb := range rule.Verbs {
			switch strings.ToLower(strings.TrimSpace(verb)) {
			case "create", "update", "patch", "delete", "deletecollection":
				if check.Status == RuntimeStatusPass {
					check.Status = RuntimeStatusWarn
				}
				check.Message = "chart default RBAC includes mutating verbs"
				check.Evidence = append(check.Evidence, fmt.Sprintf("rbac.controllerClusterRules verb=%q", verb))
				mutatingVerbFound = true
			}
		}
	}
	if mutatingVerbFound {
		check.Recommendations = append(check.Recommendations,
			"Default RBAC to read-only verbs (get/list/watch); gate mutating verbs behind an explicit override flag.",
		)
	}

	collectorSecurity := values.Collector.SecurityContext
	if collectorSecurity.AllowPrivilegeEscalation != nil && *collectorSecurity.AllowPrivilegeEscalation {
		check.Status = RuntimeStatusFail
		check.Message = "collector securityContext allows privilege escalation"
		check.Evidence = append(check.Evidence, "collector.securityContext.allowPrivilegeEscalation=true")
	}
	if strings.EqualFold(strings.TrimSpace(values.Collector.PrivilegeProfile), "deep-runtime") && len(values.Collector.DeepRuntimeCapabilities) > 0 && check.Status == RuntimeStatusPass {
		check.Status = RuntimeStatusWarn
		check.Message = "collector deep-runtime profile requests explicit kernel capabilities"
		check.Evidence = append(check.Evidence, fmt.Sprintf("collector.deepRuntimeCapabilities=%v", values.Collector.DeepRuntimeCapabilities))
		check.Recommendations = append(check.Recommendations,
			"Use the reduced-privilege collector values when deep host telemetry is not required.",
		)
	}

	deployTemplate := filepath.Join(root, "deploy", "charts", "sre-agent", "templates", "deployment.yaml")
	if data, err := os.ReadFile(deployTemplate); err == nil {
		if strings.Contains(string(data), "hostPID: true") {
			if check.Status == RuntimeStatusPass {
				check.Status = RuntimeStatusWarn
			}
			check.Message = "chart deployment template hardcodes hostPID=true"
			check.Evidence = append(check.Evidence, "templates/deployment.yaml contains hostPID: true")
			check.Recommendations = append(check.Recommendations,
				"Make hostPID opt-in and default it to false.",
			)
		}
	}

	return check
}

func loadYAMLFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}

func isLoopbackListen(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host := hostFromEndpoint(addr)
	if host == "" {
		return false
	}
	return isLoopbackHost(host)
}

func hostFromEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.HasPrefix(endpoint, ":") {
		return ""
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return ""
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	return host
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(strings.Trim(host, "[]")))
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// checkEnvVarSecretExposure scans environment variables for sensitive names
// that hold placeholder or suspiciously short values (likely example/default
// values leaked into production).
func checkEnvVarSecretExposure() RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-006",
		Title:    "Environment variable secret exposure",
		Status:   RuntimeStatusPass,
		Severity: "high",
		Message:  "no placeholder or suspicious secret values detected in environment variables",
	}

	sensitivePatterns := []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"}
	placeholderPatterns := []string{"your-", "changeme", "example", "placeholder", "xxx", "TODO", "FIXME"}

	for _, envEntry := range os.Environ() {
		parts := strings.SplitN(envEntry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.ToUpper(parts[0])
		value := strings.TrimSpace(parts[1])

		isSensitiveName := false
		for _, pattern := range sensitivePatterns {
			if strings.Contains(name, pattern) {
				isSensitiveName = true
				break
			}
		}
		if !isSensitiveName || value == "" {
			continue
		}

		// Check for placeholder values
		valueLower := strings.ToLower(value)
		for _, ph := range placeholderPatterns {
			if strings.Contains(valueLower, strings.ToLower(ph)) {
				check.Status = RuntimeStatusFail
				check.Message = "one or more secret env vars contain placeholder values"
				check.Evidence = append(check.Evidence, fmt.Sprintf("%s=<placeholder detected>", parts[0]))
				check.Recommendations = append(check.Recommendations,
					fmt.Sprintf("Replace placeholder value for %s with a real secret.", parts[0]),
				)
				break
			}
		}

		// Very short values (<=3 chars) for secret vars are suspicious
		if len(value) <= 3 && check.Status != RuntimeStatusFail {
			if check.Status == RuntimeStatusPass {
				check.Status = RuntimeStatusWarn
			}
			check.Message = "one or more secret env vars have suspiciously short values"
			check.Evidence = append(check.Evidence, fmt.Sprintf("%s=<value too short: %d chars>", parts[0], len(value)))
		}
	}

	return check
}

// dockerComposeService is a minimal subset for security-relevant docker-compose keys.
type dockerComposeService struct {
	ReadOnly    bool     `yaml:"read_only"`
	SecurityOpt []string `yaml:"security_opt"`
	CapDrop     []string `yaml:"cap_drop"`
	Ports       []string `yaml:"ports"`
}

type dockerComposeFile struct {
	Services map[string]dockerComposeService `yaml:"services"`
}

func checkDockerComposeSecurityPosture(root string) RuntimeSecurityCheck {
	check := RuntimeSecurityCheck{
		ID:       "SEC-RUNTIME-007",
		Title:    "Docker Compose security posture",
		Status:   RuntimeStatusPass,
		Severity: "medium",
		Message:  "docker-compose.yaml follows container hardening best practices",
	}

	path := filepath.Join(root, "docker-compose.yaml")
	var dc dockerComposeFile
	if err := loadYAMLFile(path, &dc); err != nil {
		altPath := filepath.Join(root, "docker-compose.yml")
		if err2 := loadYAMLFile(altPath, &dc); err2 != nil {
			check.Status = RuntimeStatusWarn
			check.Message = "docker-compose file not found; container posture cannot be validated"
			check.Evidence = []string{fmt.Sprintf("%s (%v)", path, err)}
			return check
		}
	}

	missingHardening := false
	exposedPorts := false
	for name, svc := range dc.Services {
		if !svc.ReadOnly {
			if check.Status == RuntimeStatusPass {
				check.Status = RuntimeStatusWarn
			}
			missingHardening = true
			check.Evidence = append(check.Evidence, fmt.Sprintf("service %q missing read_only: true", name))
		}

		hasNoNewPriv := false
		for _, opt := range svc.SecurityOpt {
			if strings.Contains(strings.ToLower(opt), "no-new-privileges") {
				hasNoNewPriv = true
				break
			}
		}
		if !hasNoNewPriv {
			if check.Status == RuntimeStatusPass {
				check.Status = RuntimeStatusWarn
			}
			missingHardening = true
			check.Evidence = append(check.Evidence, fmt.Sprintf("service %q missing security_opt: no-new-privileges", name))
		}

		hasCapDrop := false
		for _, cap := range svc.CapDrop {
			if strings.EqualFold(strings.TrimSpace(cap), "ALL") {
				hasCapDrop = true
				break
			}
		}
		if !hasCapDrop {
			if check.Status == RuntimeStatusPass {
				check.Status = RuntimeStatusWarn
			}
			missingHardening = true
			check.Evidence = append(check.Evidence, fmt.Sprintf("service %q missing cap_drop: [ALL]", name))
		}

		for _, portBinding := range svc.Ports {
			if isBroadHostPortBinding(portBinding) {
				if check.Status == RuntimeStatusPass {
					check.Status = RuntimeStatusWarn
				}
				exposedPorts = true
				check.Evidence = append(check.Evidence, fmt.Sprintf("service %q exposes port binding %q on non-loopback host interface", name, strings.TrimSpace(portBinding)))
			}
		}
	}

	if check.Status != RuntimeStatusPass {
		switch {
		case missingHardening && exposedPorts:
			check.Message = "docker-compose.yaml is missing container hardening settings and exposes service ports broadly"
		case missingHardening:
			check.Message = "docker-compose.yaml is missing container hardening settings"
		case exposedPorts:
			check.Message = "docker-compose.yaml exposes service ports on non-loopback host interfaces"
		}
		if missingHardening {
			check.Recommendations = append(check.Recommendations,
				"Add read_only: true, security_opt: [no-new-privileges:true], and cap_drop: [ALL] to each service.",
			)
		}
		if exposedPorts {
			check.Recommendations = append(check.Recommendations,
				"Bind published ports to loopback (for example 127.0.0.1:8080:8080) or remove host-published ports behind a private ingress.",
			)
		}
	}

	sort.Strings(check.Evidence)
	return check
}

func isBroadHostPortBinding(binding string) bool {
	spec := strings.TrimSpace(binding)
	if spec == "" {
		return false
	}
	if idx := strings.Index(spec, "/"); idx >= 0 {
		spec = spec[:idx]
	}
	if strings.HasPrefix(spec, "[") {
		end := strings.Index(spec, "]")
		if end == -1 {
			return true
		}
		host := strings.TrimSpace(spec[1:end])
		if strings.TrimSpace(spec[end+1:]) == "" {
			return false
		}
		return !isLoopbackHost(host)
	}

	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 0, 1:
		return false
	case 2:
		// hostPort:containerPort publishes on all host interfaces
		return true
	default:
		host := strings.TrimSpace(parts[0])
		if host == "" {
			return true
		}
		return !isLoopbackHost(host)
	}
}
