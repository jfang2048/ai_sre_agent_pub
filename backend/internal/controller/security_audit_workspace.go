package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	"go.uber.org/zap"
)

func (c *Controller) initSecurityAudit() error {
	path := strings.TrimSpace(os.Getenv("SRE_SECURITY_AUDIT_PATH"))
	if path == "" {
		return nil
	}
	if !c.auth.Enabled {
		return errors.New("SRE_SECURITY_AUDIT_PATH requires controller API authentication")
	}
	if c.config.HA.Enabled {
		return errors.New("SRE_SECURITY_AUDIT_PATH requires a single controller")
	}
	var err error
	c.securityAudit, err = securityaudit.OpenAuditWorkspace(path)
	if err != nil {
		return fmt.Errorf("open security audit workspace: %w", err)
	}
	if !parseBoolEnv("SRE_SECURITY_AUDIT_LLM_ENABLED", false) {
		return nil
	}
	model, err := analysis.NewLLMClient(analysis.LLMClientConfig{
		Provider: c.config.Analysis.LLMProvider, Model: c.config.Analysis.LLMModel,
		CodePath: "security_audit", Timeout: 15 * time.Second,
	}, c.logger)
	if err != nil {
		c.logger.Warn("security audit model unavailable", zap.Error(err))
	}
	if model != nil && err == nil {
		c.securityAuditModel = model
		c.securityAuditModelSlot = make(chan struct{}, 1)
	}
	return nil
}

func (c *Controller) requireAuditWorkspace(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if c.securityAudit == nil {
		http.Error(w, "security audit workspace disabled", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func readAuditJSON(w http.ResponseWriter, r *http.Request, limit int64, request any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(request)
	if err == nil {
		err = decoder.Decode(new(any))
		if err == io.EOF {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "audit request is too large", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "invalid audit request", http.StatusBadRequest)
	}
	return false
}

func (c *Controller) handleSecurityAuditImport(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) || !c.requireAuditWorkspace(w) {
		return
	}
	var request struct {
		Format     string    `json:"format"`
		Asset      string    `json:"asset"`
		Report     string    `json:"report"`
		ObservedAt time.Time `json:"observed_at"`
	}
	// The report is a JSON string, whose encoded form can be up to six times
	// larger than the decoded report (e.g. with Unicode escapes).
	if !readAuditJSON(w, r, 6*securityaudit.MaxAuditReportBytes+4096, &request) {
		return
	}
	if !securityaudit.ValidAuditAsset(request.Asset) {
		http.Error(w, "invalid audit asset", http.StatusBadRequest)
		return
	}
	if len(request.Report) > securityaudit.MaxAuditReportBytes {
		http.Error(w, "audit report is too large", http.StatusRequestEntityTooLarge)
		return
	}
	now := time.Now().UTC()
	if request.ObservedAt.IsZero() {
		request.ObservedAt = now
	}
	records, err := securityaudit.ParseAuditReport(request.Format, request.Asset, request.Report, request.ObservedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := c.securityAudit.Import(records, now)
	switch {
	case errors.Is(err, securityaudit.ErrAuditTime):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, securityaudit.ErrAuditCapacity):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		c.logger.Error("persist security audit records", zap.Error(err))
		http.Error(w, "security audit storage unavailable", http.StatusServiceUnavailable)
	default:
		writeJSON(w, result)
	}
}

func parseAuditScope(asset, rawWindow string) (time.Duration, error) {
	if asset != "" && !securityaudit.ValidAuditAsset(asset) {
		return 0, errors.New("invalid audit asset")
	}
	if rawWindow == "" {
		return 7 * 24 * time.Hour, nil
	}
	window, err := time.ParseDuration(rawWindow)
	if err != nil || window <= 0 || window > securityaudit.AuditRetention {
		return 0, errors.New("audit window must be positive and at most 720h")
	}
	return window, nil
}

func (c *Controller) handleSecurityAudit(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) || !c.requireAuditWorkspace(w) {
		return
	}
	asset := r.URL.Query().Get("asset")
	window, err := parseAuditScope(asset, r.URL.Query().Get("window"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, c.securityAudit.Report(asset, window, time.Now().UTC()))
}

func (c *Controller) handleSecurityAuditExplain(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) || !c.requireAuditWorkspace(w) {
		return
	}
	var request struct {
		Asset  string `json:"asset"`
		Window string `json:"window"`
	}
	if !readAuditJSON(w, r, 4096, &request) {
		return
	}
	window, err := parseAuditScope(request.Asset, request.Window)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report := c.securityAudit.Report(request.Asset, window, time.Now().UTC())
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var model securityaudit.AuditCompleter
	if c.securityAuditModel != nil {
		select {
		case c.securityAuditModelSlot <- struct{}{}:
			defer func() { <-c.securityAuditModelSlot }()
			model = c.securityAuditModel
		default:
			// The rules summary remains available during another model request.
		}
	}
	writeJSON(w, securityaudit.ExplainAudit(ctx, report, model))
}
