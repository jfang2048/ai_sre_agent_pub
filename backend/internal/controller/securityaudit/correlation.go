package securityaudit

import (
	"math"
	"slices"
	"strings"
	"time"
)

const (
	ruleDeniedThenSensitive = "denials_then_sensitive_request"
	ruleChangeNearFinding   = "scan_finding_near_sensitive_request"
)

type AuditReport struct {
	Records      []AuditRecord      `json:"records"`
	Count        int                `json:"count"`
	Correlations []AuditCorrelation `json:"correlations"`
	Anomalies    []AuditAnomaly     `json:"anomalies"`
}

type AuditCorrelation struct {
	ID          string   `json:"id"`
	Rule        string   `json:"rule"`
	Asset       string   `json:"asset"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type AuditAnomaly struct {
	Asset       string    `json:"asset"`
	Bucket      time.Time `json:"bucket"`
	Observed    float64   `json:"observed"`
	Baseline    float64   `json:"baseline"`
	Score       float64   `json:"score"`
	Samples     int       `json:"samples"`
	EvidenceIDs []string  `json:"evidence_ids"`
}

func analyzeAudit(records []AuditRecord, from, now time.Time) AuditReport {
	return AuditReport{
		Correlations: correlateAudit(records),
		Anomalies:    auditAnomalies(records, from, now),
	}
}

func correlateAudit(records []AuditRecord) []AuditCorrelation {
	type actorKey struct{ asset, actor string }
	denials := make(map[actorKey][]AuditRecord)
	lastFinding := make(map[string]AuditRecord)
	lastSensitive := make(map[string]AuditRecord)
	out := make([]AuditCorrelation, 0)
	add := func(rule string, record AuditRecord, ids []string, summary string) {
		out = append(out, AuditCorrelation{
			ID: auditID(rule, record.ID), Rule: rule, Asset: record.Asset,
			Summary: summary, EvidenceIDs: ids,
		})
	}
	for _, record := range records {
		if record.Source == "sarif" && (record.Severity == "high" || record.Severity == "critical") {
			if event, ok := lastSensitive[record.Asset]; ok && record.ObservedAt.Sub(event.ObservedAt) <= time.Hour {
				add(ruleChangeNearFinding, record, []string{event.ID, record.ID}, "A high-severity scan finding and a sensitive Kubernetes request were observed within one hour on the same mapped asset.")
			}
			lastFinding[record.Asset] = record
			continue
		}
		if record.Source != "kubernetes" {
			continue
		}
		key := actorKey{record.Asset, record.Actor}
		if record.Rule == "authorization_denied" && record.Actor != "" {
			recent := denials[key]
			if len(recent) == 5 {
				recent = slices.Delete(recent, 0, 1)
			}
			denials[key] = append(recent, record)
		}
		if record.Rule != "rbac_change" && record.Rule != "secret_access" {
			continue
		}
		if recent := denials[key]; len(recent) == 5 &&
			record.ObservedAt.After(recent[4].ObservedAt) &&
			record.ObservedAt.Sub(recent[0].ObservedAt) <= 15*time.Minute {
			ids := make([]string, 0, 6)
			for _, denied := range recent {
				ids = append(ids, denied.ID)
			}
			add(ruleDeniedThenSensitive, record, append(ids, record.ID), "Five denied requests preceded a successful sensitive Kubernetes request by the same actor and asset within 15 minutes.")
			delete(denials, key)
		}
		if finding, ok := lastFinding[record.Asset]; ok && record.ObservedAt.Sub(finding.ObservedAt) <= time.Hour {
			add(ruleChangeNearFinding, record, []string{finding.ID, record.ID}, "A high-severity scan finding and a sensitive Kubernetes request were observed within one hour on the same mapped asset.")
		}
		lastSensitive[record.Asset] = record
	}
	slices.Reverse(out)
	return out[:min(len(out), 100)]
}

type auditHour struct {
	asset    string
	at       time.Time
	total    int
	denied   int
	evidence []string
}

// Train a rolling baseline from the prior available complete UTC hours per
// asset. The sample minimum and scale floor stabilize sparse and flat series.
func auditAnomalies(records []AuditRecord, from, now time.Time) []AuditAnomaly {
	type key struct {
		asset string
		at    time.Time
	}
	hours := make(map[key]*auditHour)
	for _, record := range records {
		at := record.ObservedAt.UTC().Truncate(time.Hour)
		if record.Source != "kubernetes" || at.Before(from) || !at.Before(now.Truncate(time.Hour)) {
			continue
		}
		k := key{record.Asset, at}
		hour := hours[k]
		if hour == nil {
			hour = &auditHour{asset: k.asset, at: k.at}
			hours[k] = hour
		}
		hour.total++
		if record.Rule == "authorization_denied" {
			hour.denied++
			if len(hour.evidence) < 5 {
				hour.evidence = append(hour.evidence, record.ID)
			}
		}
	}
	ordered := make([]*auditHour, 0, len(hours))
	for _, hour := range hours {
		if hour.total >= 10 {
			ordered = append(ordered, hour)
		}
	}
	slices.SortFunc(ordered, func(a, b *auditHour) int {
		if order := a.at.Compare(b.at); order != 0 {
			return order
		}
		return strings.Compare(a.asset, b.asset)
	})
	type observation struct {
		at   time.Time
		rate float64
	}
	history := make(map[string][]observation)
	out := make([]AuditAnomaly, 0)
	for _, hour := range ordered {
		prior := history[hour.asset]
		for len(prior) > 0 && hour.at.Sub(prior[0].at) > 48*time.Hour {
			prior = prior[1:]
		}
		rate := float64(hour.denied) / float64(hour.total)
		if len(prior) >= 8 && hour.denied >= 3 {
			values := make([]float64, 0, len(prior))
			for _, sample := range prior {
				values = append(values, sample.rate)
			}
			baseline := medianRate(values)
			for i, value := range values {
				values[i] = math.Abs(value - baseline)
			}
			score := (rate - baseline) / max(1.4826*medianRate(values), 0.05)
			if score >= 3.5 {
				out = append(out, AuditAnomaly{Asset: hour.asset, Bucket: hour.at, Observed: rate,
					Baseline: baseline, Score: score, Samples: len(prior), EvidenceIDs: hour.evidence})
			}
		}
		history[hour.asset] = append(prior, observation{at: hour.at, rate: rate})
	}
	slices.Reverse(out)
	return out[:min(len(out), 50)]
}

func medianRate(values []float64) float64 {
	slices.Sort(values)
	return (values[(len(values)-1)/2] + values[len(values)/2]) / 2
}
