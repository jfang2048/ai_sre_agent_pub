package securityaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseAuditReportSARIF(t *testing.T) {
	const report = `{
		"version": "2.1.0",
		"runs": [{
			"tool": {
				"driver": {"name": "ci-scanner"},
				"extensions": [{"name":"custom-rules","rules":[{"id":"AUDIT-1","defaultConfiguration":{"level":"error"}}]}]
			},
			"artifacts": [{"location":{"uri":"src/main%20file.go?token=SECRET_QUERY"}}],
			"results": [
				{
					"rule":{"index":0,"toolComponent":{"index":0}},
					"locations":[{"physicalLocation":{"artifactLocation":{"index":0},"region":{"startLine":20}}}],
					"message":{"text":"SECRET_MESSAGE"},
					"fingerprints":{"second":"SECRET_FINGERPRINT","first":"stable"}
				},
				{"ruleId":"PASS-1","kind":"pass"}
			]
		}]
	}`
	observed := time.Date(2026, 11, 20, 13, 30, 0, 0, time.FixedZone("CET", 3600))
	records, err := ParseAuditReport("sarif", " repo-a ", report, observed)
	if err != nil || len(records) != 1 {
		t.Fatalf("parse SARIF = %v, %v", records, err)
	}
	got := records[0]
	if got.Asset != "repo-a" || got.Source != "sarif" || got.Kind != "finding" || got.Rule != "AUDIT-1" ||
		got.Severity != "high" || got.Resource != "src/main file.go" || got.ObservedAt != observed.UTC() {
		t.Fatalf("unexpected SARIF record: %+v", got)
	}
	reimport := strings.NewReplacer(
		`"startLine":20`, `"startLine":36`,
		`"second":"SECRET_FINGERPRINT","first":"stable"`, `"first":"stable","second":"SECRET_FINGERPRINT"`,
	).Replace(report)
	same, err := ParseAuditReport("sarif", "repo-a", reimport, observed.Add(time.Hour))
	if err != nil || len(same) != 1 || same[0].ID != got.ID {
		t.Fatalf("fingerprint changed on reimport: %v, %v", same, err)
	}

	for _, scope := range []struct {
		asset  string
		report string
	}{
		{"repo-b", report},
		{"repo-a", strings.Replace(report, "ci-scanner", "another-scanner", 1)},
		{"repo-a", strings.Replace(report, "main%20file.go", "another.go", 1)},
	} {
		other, err := ParseAuditReport("sarif", scope.asset, scope.report, observed)
		if err != nil || len(other) != 1 || other[0].ID == got.ID {
			t.Fatalf("identity merged unrelated scope: %v, %v", other, err)
		}
	}
	encoded, err := json.Marshal(records)
	if err != nil || strings.Contains(string(encoded), "SECRET_") {
		t.Fatalf("SARIF output retained unapproved data: %s, %v", encoded, err)
	}
}

func TestParseAuditReportKubernetes(t *testing.T) {
	eventList := `{
		"apiVersion":"audit.k8s.io/v1","kind":"EventList","items":[
			{"auditID":"INTERNAL_AUDIT_ID","stage":"RequestReceived"},
			{
				"auditID":"INTERNAL_AUDIT_ID","stage":"ResponseComplete","stageTimestamp":"2026-11-20T11:10:12.123456Z",
				"verb":"list","user":{"username":"system:serviceaccount:team:reader","extra":{"private":["SECRET_EXTRA"]}},
				"responseStatus":{"code":200,"message":"SECRET_STATUS"},
				"objectRef":{"resource":"secrets","namespace":"team"},
				"requestURI":"/api/v1/namespaces/team/secrets?token=SECRET_URI",
				"requestObject":{"secret":"SECRET_REQUEST"},"responseObject":{"secret":"SECRET_RESPONSE"}
			},
			{
				"auditID":"second","stage":"ResponseComplete","requestReceivedTimestamp":"2026-11-20T12:10:00Z",
				"verb":"patch","objectRef":{"apiGroup":"rbac.authorization.k8s.io","resource":"rolebindings"},
				"responseStatus":{"code":201}
			},
			{
				"auditID":"third","stage":"ResponseComplete","stageTimestamp":"2026-11-20T12:15:00Z",
				"verb":"get","objectRef":{"resource":"secrets"},"responseStatus":{"code":403}
			}
		]
	}`
	single := `{"apiVersion":"audit.k8s.io/v1","kind":"Event","auditID":"fourth","stage":"Panic","stageTimestamp":"2026-11-20T12:20:00Z"}`
	report := eventList + "\n" + single
	records, err := ParseAuditReport("kubernetes", "cluster-a", report, time.Time{})
	if err != nil || len(records) != 4 {
		t.Fatalf("parse audit list and stream = %v, %v", records, err)
	}
	wantRules := []string{"secret_access", "rbac_change", "authorization_denied", "api_request"}
	for i, record := range records {
		if record.Rule != wantRules[i] || record.Kind != "event" || record.Source != "kubernetes" {
			t.Fatalf("record %d: %+v", i, record)
		}
	}
	first := records[0]
	if first.Actor != "system:serviceaccount:team:reader" || first.Resource != "team/secrets" ||
		first.Status != 200 || first.ObservedAt.Format(time.RFC3339Nano) != "2026-11-20T11:10:12.123456Z" {
		t.Fatalf("unexpected metadata: %+v", first)
	}
	if records[1].ObservedAt.Format(time.RFC3339Nano) != "2026-11-20T12:10:00Z" {
		t.Fatalf("request timestamp fallback missing: %+v", records[1])
	}
	same, err := ParseAuditReport("kubernetes", "cluster-a", single, time.Time{})
	if err != nil || len(same) != 1 || same[0].ID != records[3].ID {
		t.Fatalf("event identity changed with report packaging: %v, %v", same, err)
	}
	other, err := ParseAuditReport("kubernetes", "cluster-b", single, time.Time{})
	if err != nil || len(other) != 1 || other[0].ID == records[3].ID {
		t.Fatalf("event identity merged cluster scope: %v, %v", other, err)
	}
	encoded, err := json.Marshal(records)
	if err != nil || strings.Contains(string(encoded), "SECRET_") || strings.Contains(string(encoded), "INTERNAL_AUDIT_ID") {
		t.Fatalf("Kubernetes output retained unapproved data: %s, %v", encoded, err)
	}
}

func TestParseAuditReportRejectsInvalidOrOversizedBatch(t *testing.T) {
	const event = `{"apiVersion":"audit.k8s.io/v1","kind":"Event","auditID":"id","stage":"ResponseComplete","stageTimestamp":"2026-11-20T12:20:00Z"}`
	const skipped = `{"apiVersion":"audit.k8s.io/v1","kind":"Event","stage":"RequestReceived"}`
	tests := []struct {
		name   string
		format string
		asset  string
		report string
	}{
		{"invalid later audit event", "kubernetes", "cluster", event + "\n{\"apiVersion\":\"SECRET_VALUE\"}"},
		{"invalid later SARIF result", "sarif", "repo", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"test"}},"results":[{"ruleId":"A-1"},{"level":"SECRET_VALUE"}]}]}`},
		{"report size limit", "kubernetes", "cluster", strings.Repeat("x", MaxAuditReportBytes+1)},
		{"record count limit includes skipped events", "kubernetes", "cluster", strings.Repeat(skipped+"\n", MaxAuditReportRecords+1)},
		{"asset size bound", "kubernetes", strings.Repeat("a", maxAuditFieldBytes+1), event},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records, err := ParseAuditReport(test.format, test.asset, test.report, time.Now())
			if records != nil || err == nil || strings.Contains(err.Error(), "SECRET_VALUE") {
				t.Fatalf("invalid report returned records or an unsafe error: %v, %v", records, err)
			}
		})
	}
}
