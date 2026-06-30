package controller

import (
	"net/http"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
)

func (c *Controller) registerSecurityHandlers(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/v1/security/findings", c.withCORS(c.handleSecurityFindings))
	mux.HandleFunc("/api/v1/security/dashboard", c.withCORS(c.handleSecurityDashboard))
	mux.HandleFunc("/api/v1/security/trends", c.withCORS(c.handleSecurityTrends))
}

func (c *Controller) handleSecurityFindings(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	evaluator := securityaudit.NewEvaluator(c.ingestStore, c.logIndex)
	if evaluator == nil {
		http.Error(w, "security evaluator unavailable", http.StatusServiceUnavailable)
		return
	}
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	findings := evaluator.Findings(securityaudit.Options{
		CollectorID: strings.TrimSpace(firstNonEmpty(
			r.URL.Query().Get("collector_id"),
			r.URL.Query().Get("collector"),
			r.URL.Query().Get("node"),
		)),
		Severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Window:   window,
		Limit:    limit,
	})

	writeJSON(w, map[string]any{
		"findings":  findings,
		"count":     len(findings),
		"timestamp": time.Now().UTC(),
	})
}

func (c *Controller) handleSecurityDashboard(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	evaluator := securityaudit.NewEvaluator(c.ingestStore, c.logIndex)
	if evaluator == nil {
		http.Error(w, "security evaluator unavailable", http.StatusServiceUnavailable)
		return
	}
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	dashboard := evaluator.Dashboard(securityaudit.Options{
		CollectorID: strings.TrimSpace(firstNonEmpty(
			r.URL.Query().Get("collector_id"),
			r.URL.Query().Get("collector"),
			r.URL.Query().Get("node"),
		)),
		Severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Window:   window,
		Limit:    limit,
	})
	writeJSON(w, dashboard)
}

func (c *Controller) handleSecurityTrends(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	evaluator := securityaudit.NewEvaluator(c.ingestStore, c.logIndex)
	if evaluator == nil {
		http.Error(w, "security evaluator unavailable", http.StatusServiceUnavailable)
		return
	}
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 120
	}
	if limit > 2000 {
		limit = 2000
	}

	dashboard := evaluator.Dashboard(securityaudit.Options{
		CollectorID: strings.TrimSpace(firstNonEmpty(
			r.URL.Query().Get("collector_id"),
			r.URL.Query().Get("collector"),
			r.URL.Query().Get("node"),
		)),
		Window: window,
		Limit:  limit,
	})
	trends := dashboard.Trends
	if len(trends) > limit {
		trends = trends[len(trends)-limit:]
	}

	writeJSON(w, map[string]any{
		"collector_id": strings.TrimSpace(firstNonEmpty(
			r.URL.Query().Get("collector_id"),
			r.URL.Query().Get("collector"),
			r.URL.Query().Get("node"),
		)),
		"window":    window.String(),
		"trends":    trends,
		"count":     len(trends),
		"timestamp": time.Now().UTC(),
	})
}
