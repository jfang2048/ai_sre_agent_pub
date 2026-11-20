package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSecurityAuditImportQueryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.json")
	t.Setenv("SRE_SECURITY_AUDIT_PATH", path)
	t.Setenv("SRE_SECURITY_AUDIT_LLM_ENABLED", "false")
	ctrl := &Controller{
		logger: zap.NewNop(),
		auth: ResolvedAuthConfig{
			Enabled: true, Mode: ControllerAuthModeAPIKey, ReadKey: "test-read", ActionKey: "test-action",
		},
	}
	require.NoError(t, ctrl.initSecurityAudit())
	mux := http.NewServeMux()
	ctrl.registerSecurityHandlers(mux)
	handler := ctrl.wrapHTTPAuthentication(ctrl.wrapHTTPHandler(mux))

	request := func(method, path, key, body string, status int) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/security/audit"+path, strings.NewReader(body))
		req.Header.Set("X-API-Key", key)
		handler.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	now := time.Now().UTC().Add(-time.Minute)
	const scan = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"scanner"}},"results":[{"ruleId":"rule-1","level":"error","message":{"text":"raw-message-should-be-omitted"}}]}]}`
	wrapper := func(format, asset, report string) string {
		input, err := json.Marshal(map[string]any{"format": format, "asset": asset, "report": report, "observed_at": now})
		require.NoError(t, err)
		return string(input)
	}
	scanBody := wrapper("sarif", "prod", scan)
	request(http.MethodGet, "", "", "", http.StatusUnauthorized)
	request(http.MethodPost, "/import", "test-read", scanBody, http.StatusForbidden)
	require.JSONEq(t, `{"imported":1,"updated":0}`, request(http.MethodPost, "/import", "test-action", scanBody, http.StatusOK))
	require.JSONEq(t, `{"imported":0,"updated":0}`, request(http.MethodPost, "/import", "test-action", scanBody, http.StatusOK))

	event, err := json.Marshal(map[string]any{
		"apiVersion": "audit.k8s.io/v1", "kind": "Event", "auditID": "k8s-evidence",
		"stage": "ResponseComplete", "stageTimestamp": now.Add(-time.Second),
		"verb": "get", "objectRef": map[string]string{"resource": "secrets", "name": "example"},
		"responseStatus": map[string]int{"code": 200},
		"responseObject": map[string]string{"body": "raw-message-should-be-omitted"},
	})
	require.NoError(t, err)
	request(http.MethodPost, "/import", "test-action", wrapper("kubernetes", "prod", string(event)+"\ninvalid"), http.StatusBadRequest)
	request(http.MethodPost, "/import", "test-action", wrapper("kubernetes", "other", string(event)), http.StatusOK)
	request(http.MethodPost, "/import", "test-action", wrapper("kubernetes", "prod", string(event)), http.StatusOK)
	store, err := securityaudit.OpenAuditWorkspace(path)
	require.NoError(t, err)
	ctrl.securityAudit = store

	encoded := request(http.MethodGet, "?asset=prod", "test-read", "", http.StatusOK)
	require.NotContains(t, encoded, "raw-message-should-be-omitted")
	var report securityaudit.AuditReport
	require.NoError(t, json.Unmarshal([]byte(encoded), &report))
	require.Equal(t, 2, report.Count)
	require.Len(t, report.Correlations, 1)
	require.ElementsMatch(t, []string{report.Records[0].ID, report.Records[1].ID}, report.Correlations[0].EvidenceIDs)
	require.JSONEq(t, `{"records":[],"count":0,"correlations":[],"anomalies":[]}`,
		request(http.MethodGet, "?asset=missing", "test-read", "", http.StatusOK))
	request(http.MethodGet, "?window=721h", "test-read", "", http.StatusBadRequest)
	require.Contains(t, request(http.MethodPost, "/explain", "test-action", `{"asset":"prod"}`, http.StatusOK), `"mode":"rules"`)

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.URL.Path != "/chat/completions" ||
			!strings.Contains(string(body), "high_severity_scan") ||
			strings.Contains(string(body), "raw-message-should-be-omitted") ||
			strings.Contains(string(body), report.Records[0].ID) ||
			strings.Contains(string(body), "rule-1") {
			t.Error("model received an unexpected prompt")
			http.Error(w, "invalid prompt", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"index\":2}"}}]}`)
	}))
	t.Cleanup(provider.Close)
	t.Setenv("SRE_SECURITY_AUDIT_LLM_ENABLED", "true")
	t.Setenv("SRE_AGENT_LLM_PROVIDER", "openai")
	t.Setenv("SRE_AGENT_LLM_API_KEY", "local-test-key")
	t.Setenv("SRE_AGENT_LLM_BASE_URL", provider.URL)
	require.NoError(t, ctrl.initSecurityAudit())
	result := request(http.MethodPost, "/explain", "test-action", `{"asset":"prod"}`, http.StatusOK)
	require.Contains(t, result, `"mode":"llm"`)
	require.Contains(t, result, report.Records[0].ID)
}
