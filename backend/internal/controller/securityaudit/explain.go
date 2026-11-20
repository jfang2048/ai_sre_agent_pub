package securityaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

type AuditCompleter interface {
	CompletePrompt(context.Context, string) (string, error)
}

type AuditExplanation struct {
	Mode        string   `json:"mode"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type auditCandidate struct {
	Index   int     `json:"index"`
	Signal  string  `json:"signal"`
	Ratio   float64 `json:"observed_ratio,omitempty"`
	Typical float64 `json:"typical_ratio,omitempty"`
	ids     []string
}

// ExplainAudit gives the model only application-defined signals and numeric
// features. The selected option is rendered from verified evidence locally.
func ExplainAudit(ctx context.Context, report AuditReport, model AuditCompleter) AuditExplanation {
	overview := fmt.Sprintf("Reviewed %d records, with %d displayed correlations and %d displayed statistical anomalies.",
		report.Count, len(report.Correlations), len(report.Anomalies))
	candidates := make([]auditCandidate, 0, 16)
	for _, rule := range []string{ruleDeniedThenSensitive, ruleChangeNearFinding} {
		n := 0
		for _, match := range report.Correlations {
			if match.Rule == rule {
				candidates = append(candidates, auditCandidate{Signal: rule, ids: match.EvidenceIDs})
				n++
			}
			if n == 4 {
				break
			}
		}
	}
	for _, anomaly := range report.Anomalies[:min(len(report.Anomalies), 4)] {
		candidates = append(candidates, auditCandidate{Signal: "denial_rate_anomaly",
			Ratio: anomaly.Observed, Typical: anomaly.Baseline, ids: anomaly.EvidenceIDs})
	}
	for _, record := range report.Records {
		if record.Source == "sarif" && (record.Severity == "high" || record.Severity == "critical") {
			candidates = append(candidates, auditCandidate{Signal: "high_severity_scan", ids: []string{record.ID}})
		}
		if len(candidates) >= 16 {
			break
		}
	}
	if len(candidates) == 0 {
		return AuditExplanation{Mode: "rules", Summary: overview, EvidenceIDs: []string{}}
	}
	selectCandidate := func(candidate auditCandidate) AuditExplanation {
		summary := overview
		switch candidate.Signal {
		case ruleDeniedThenSensitive:
			summary += " First review the actor’s denied requests and verify the approved scope of the subsequent sensitive request."
		case ruleChangeNearFinding:
			summary += " Check the deployment inventory and change history to determine whether the scan finding and the sensitive request concern the same workload."
		case "denial_rate_anomaly":
			summary += fmt.Sprintf(" Review the elevated authorization denial rate (%.1f%%, typical %.1f%%) and check for recent access-policy changes or collection gaps.", candidate.Ratio*100, candidate.Typical*100)
		case "high_severity_scan":
			summary += " Confirm the scan finding against the corresponding source or build and check who owns remediation."
		}
		return AuditExplanation{Mode: "rules", Summary: summary, EvidenceIDs: slices.Clone(candidate.ids)}
	}
	fallback := selectCandidate(candidates[0])
	if model == nil || len(candidates) < 2 {
		return fallback
	}
	for i := range candidates {
		candidates[i].Index = i + 1
	}
	data, err := json.Marshal(candidates)
	if err != nil {
		return fallback
	}
	response, err := model.CompletePrompt(ctx,
		"Prioritize one signal for a defensive security engineer to review first. Return only JSON with one integer field called index. Select an index from the following evidence groups: "+string(data))
	if err != nil || len(response) > 256 {
		return fallback
	}
	var index int
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(response), &fields) != nil || len(fields) != 1 ||
		json.Unmarshal(fields["index"], &index) != nil ||
		index < 1 || index > len(candidates) {
		return fallback
	}
	result := selectCandidate(candidates[index-1])
	result.Mode = "llm"
	return result
}
