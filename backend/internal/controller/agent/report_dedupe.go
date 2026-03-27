package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/predictive"
)

type reportStorageDecision struct {
	persisted  bool
	suppressed bool
	refreshed  bool
	actions    []ActionDecision
	report     Report
}

type compactReportSignature struct {
	NodeName    string   `json:"node_name"`
	Summary     string   `json:"summary"`
	Findings    []string `json:"findings,omitempty"`
	Forecasts   []string `json:"forecasts,omitempty"`
	Predictions []string `json:"predictions,omitempty"`
	Actions     []string `json:"actions,omitempty"`
	Processes   []string `json:"processes,omitempty"`
	Logs        []string `json:"logs,omitempty"`
	RCAs        []string `json:"rcas,omitempty"`
	LLM         []string `json:"llm,omitempty"`
}

func (e *Engine) storeReport(report Report) reportStorageDecision {
	if e == nil {
		return reportStorageDecision{}
	}

	var decision reportStorageDecision
	var persistReport *Report
	e.mu.Lock()
	list := append([]Report(nil), e.reports[report.NodeName]...)
	if e.cfg.SuppressUnchangedReports && len(list) > 0 && e.cfg.ReportRefreshInterval > 0 {
		last := list[len(list)-1]
		if report.GeneratedAt.Sub(last.GeneratedAt) <= e.cfg.ReportRefreshInterval && semanticReportFingerprint(last) == semanticReportFingerprint(report) {
			report.ID = last.ID
			report.Actions = append([]ActionDecision(nil), last.Actions...)
			list[len(list)-1] = report
			e.reports[report.NodeName] = list
			for _, action := range report.Actions {
				e.actions[action.ID] = action
			}
			e.reportSuppressedTotal++
			e.reportRefreshedTotal++
			decision = reportStorageDecision{
				suppressed: true,
				refreshed:  true,
				actions:    append([]ActionDecision(nil), report.Actions...),
				report:     report,
			}
			e.mu.Unlock()
			return decision
		}
	}
	list = append(list, report)
	if len(list) > e.cfg.MaxReports {
		list = list[len(list)-e.cfg.MaxReports:]
	}
	e.reports[report.NodeName] = list
	for _, action := range report.Actions {
		e.actions[action.ID] = action
	}
	if e.cfg.MaxActions > 0 && len(e.actions) > e.cfg.MaxActions {
		e.pruneActions()
	}
	decision = reportStorageDecision{
		persisted: true,
		actions:   append([]ActionDecision(nil), report.Actions...),
		report:    report,
	}
	persistReport = &report
	e.mu.Unlock()

	if persistReport != nil && e.persist != nil {
		_ = e.persist.SaveReport(*persistReport)
		for _, action := range decision.actions {
			_ = e.persist.SaveAction(action)
		}
	}

	return decision
}

func (e *Engine) shouldLogPredictiveFinding(now time.Time, finding predictive.Finding) bool {
	if e == nil {
		return true
	}
	if e.cfg.PredictiveLogCooldown <= 0 {
		return true
	}
	key := predictiveFindingSuppressionKey(finding)
	e.mu.Lock()
	defer e.mu.Unlock()
	if last, ok := e.predictiveLogSeen[key]; ok && now.Sub(last) < e.cfg.PredictiveLogCooldown {
		e.predictiveLogSuppressedTotal++
		return false
	}
	e.predictiveLogSeen[key] = now
	if len(e.predictiveLogSeen) > 512 {
		cutoff := now.Add(-2 * e.cfg.PredictiveLogCooldown)
		for k, seenAt := range e.predictiveLogSeen {
			if seenAt.Before(cutoff) {
				delete(e.predictiveLogSeen, k)
			}
		}
	}
	return true
}

func (e *Engine) Status() ReportEngineStatus {
	if e == nil {
		return ReportEngineStatus{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	reportCount := 0
	for _, list := range e.reports {
		reportCount += len(list)
	}
	return ReportEngineStatus{
		Enabled:                      true,
		SuppressUnchangedReports:     e.cfg.SuppressUnchangedReports,
		ReportRefreshInterval:        e.cfg.ReportRefreshInterval.String(),
		PredictiveLogCooldown:        e.cfg.PredictiveLogCooldown.String(),
		ReportsStored:                reportCount,
		ActionsStored:                len(e.actions),
		ReportSuppressedTotal:        e.reportSuppressedTotal,
		ReportRefreshedTotal:         e.reportRefreshedTotal,
		PredictiveLogSuppressedTotal: e.predictiveLogSuppressedTotal,
		ActionDryRunTotal:            e.actionDryRunTotal,
		ActionExecuteTotal:           e.actionExecuteTotal,
		ActionBlockedTotal:           e.actionBlockedTotal,
	}
}

func semanticReportFingerprint(report Report) string {
	signature := compactReportSignature{
		NodeName:    report.NodeName,
		Summary:     strings.TrimSpace(report.Summary),
		Findings:    normalizedStrings(report.Findings),
		Forecasts:   normalizedStrings(report.Forecasts),
		Predictions: normalizedStrings(compactPredictionSignals(report.Predictions)),
		Actions:     normalizedStrings(compactActionSignals(report.Actions)),
		Processes:   normalizedStrings(compactProcessSignals(report.Evidence.Processes)),
		Logs:        normalizedStrings(compactLogSignals(report.Evidence.Logs)),
		RCAs:        normalizedStrings(compactRCASignals(report.RCAs)),
		LLM:         normalizedStrings(compactLLMSignals(report.LLM)),
	}
	body, err := json.Marshal(signature)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:16])
}

func predictiveFindingSuppressionKey(f predictive.Finding) string {
	currentBucket := int64(f.CurrentValue * 10)
	forecastBucket := int64(f.ForecastValue * 10)
	baselineBucket := int64(f.BaselineValue * 10)
	return strings.Join([]string{
		strings.TrimSpace(f.AssetID),
		strings.TrimSpace(f.Metric),
		strings.TrimSpace(f.PredictiveSLO),
		strings.TrimSpace(f.HazardClass),
		strings.TrimSpace(f.Severity),
		strings.TrimSpace(f.Title),
		strings.TrimSpace(f.ControlReference),
		strconvFormatInt(currentBucket),
		strconvFormatInt(forecastBucket),
		strconvFormatInt(baselineBucket),
	}, "|")
}

func compactPredictionSignals(in []predictive.Finding) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, strings.Join([]string{
			strings.TrimSpace(item.Metric),
			strings.TrimSpace(item.PredictiveSLO),
			strings.TrimSpace(item.Severity),
			strings.TrimSpace(item.HazardClass),
			strings.TrimSpace(item.Title),
		}, "|"))
	}
	return out
}

func compactActionSignals(in []ActionDecision) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, strings.Join([]string{
			strings.TrimSpace(item.Type),
			strings.TrimSpace(item.Reason),
			strings.TrimSpace(item.Priority),
			strings.TrimSpace(item.Status),
			boolToken(item.Safe),
		}, "|"))
	}
	return out
}

func compactProcessSignals(in []analysis.ProcessSummary) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(in), 3))
	for _, item := range in[:minInt(len(in), 3)] {
		out = append(out, strings.Join([]string{
			strings.TrimSpace(item.Name),
			strconvFormatInt(int64(item.PID)),
			strconvFormatInt(int64(item.CPUPercent / 5)),
			strconvFormatInt(int64(item.RSSBytes / (256 * 1024 * 1024))),
		}, "|"))
	}
	return out
}

func compactLogSignals(in []analysis.LogSummary) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(in), 3))
	for _, item := range in[:minInt(len(in), 3)] {
		out = append(out, strings.Join([]string{
			strings.TrimSpace(item.Fingerprint),
			strconvFormatInt(int64(item.Count / 25)),
		}, "|"))
	}
	return out
}

func compactRCASignals(in []analysis.RootCauseAnalysis) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(in), 3))
	for _, item := range in[:minInt(len(in), 3)] {
		out = append(out, strings.Join([]string{
			strings.TrimSpace(item.Symptom),
			strings.TrimSpace(item.RootCause),
			strings.TrimSpace(item.AnalysisMethod),
		}, "|"))
	}
	return out
}

func compactLLMSignals(in *LLMInsight) []string {
	if in == nil {
		return nil
	}
	return []string{
		strings.TrimSpace(in.Summary),
		strings.TrimSpace(in.RootCause),
		strconvFormatInt(int64(in.Confidence * 10)),
	}
}

func normalizedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func boolToken(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func strconvFormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
