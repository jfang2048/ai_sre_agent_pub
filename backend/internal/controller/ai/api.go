// Package ai provides API handlers for the AI module.
package ai

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"go.uber.org/zap"
)

// APIHandler handles AI-related API requests
type APIHandler struct {
	module *Module
	logger *zap.Logger
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(module *Module, logger *zap.Logger) *APIHandler {
	return &APIHandler{
		module: module,
		logger: logger.With(zap.String("component", "ai_api")),
	}
}

// RegisterHandlers registers API endpoints
func (h *APIHandler) RegisterHandlers(mux *http.ServeMux, corsWrapper func(http.HandlerFunc) http.HandlerFunc) {
	// Analysis endpoints
	mux.HandleFunc("/api/v1/ai/analyze", corsWrapper(h.handleAnalyze))
	mux.HandleFunc("/api/v1/ai/results", corsWrapper(h.handleResults))
	mux.HandleFunc("/api/v1/ai/results/", corsWrapper(h.handleResultsByNode))
	mux.HandleFunc("/api/v1/ai/stats", corsWrapper(h.handleStats))
	mux.HandleFunc("/api/v1/ai/ingest", corsWrapper(h.handleIngest))
	mux.HandleFunc("/api/v1/ai/feedback", corsWrapper(h.handleFeedback))

	h.logger.Info("AI API handlers registered")
}

// handleAnalyze handles POST /api/v1/ai/analyze
func (h *APIHandler) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Convert to internal format
	data := h.convertRequest(req)

	// Analyze synchronously
	result, err := h.module.Analyze(r.Context(), data)
	if err != nil {
		h.logger.Error("analysis failed", zap.Error(err))
		h.errorResponse(w, "Analysis failed", http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, AnalyzeResponse{
		Result:    result,
		Timestamp: time.Now(),
	})
}

// handleResults handles GET /api/v1/ai/results
func (h *APIHandler) handleResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse limit parameter
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	results := h.module.GetRecentResults(limit)

	h.jsonResponse(w, ResultsResponse{
		Results:   results,
		Count:     len(results),
		Timestamp: time.Now(),
	})
}

// handleResultsByNode handles GET /api/v1/ai/results/{node}
func (h *APIHandler) handleResultsByNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract node name from path
	nodeName := r.URL.Path[len("/api/v1/ai/results/"):]
	if nodeName == "" {
		h.errorResponse(w, "Node name required", http.StatusBadRequest)
		return
	}

	results := h.module.GetResultsByNode(nodeName)

	h.jsonResponse(w, ResultsResponse{
		Results:   results,
		Count:     len(results),
		Timestamp: time.Now(),
	})
}

// handleStats handles GET /api/v1/ai/stats
func (h *APIHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := h.module.Stats()

	h.jsonResponse(w, StatsResponse{
		Stats:     stats,
		Timestamp: time.Now(),
	})
}

// handleIngest handles POST /api/v1/ai/ingest
func (h *APIHandler) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Convert and ingest
	data := h.convertRequest(AnalyzeRequest{
		NodeName:   req.NodeName,
		Metrics:    req.Metrics,
		Logs:       req.Logs,
		Context:    req.Context,
		K8sContext: req.K8sContext,
	})

	if err := h.module.Ingest(r.Context(), data); err != nil {
		h.logger.Error("ingest failed", zap.Error(err))
		h.errorResponse(w, "Ingest failed", http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, IngestResponse{
		Queued:    true,
		Timestamp: time.Now(),
	})
}

// handleFeedback handles POST /api/v1/ai/feedback
func (h *APIHandler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate
	if req.AnalysisID == "" || (req.Rating != "useful" && req.Rating != "not_useful") {
		h.errorResponse(w, "Invalid feedback: require analysis_id and rating (useful/not_useful)", http.StatusBadRequest)
		return
	}

	// Log feedback for offline training
	h.logger.Info("received user feedback",
		zap.String("event", "ai_feedback"),
		zap.String("analysis_id", req.AnalysisID),
		zap.String("rating", req.Rating), // useful, not_useful, neutral
		zap.String("comments", req.Comments),
		zap.String("corrected_cause", req.CorrectedCause),
	)

	h.jsonResponse(w, map[string]string{"status": "recorded"})
}

// ============================================================================
// Request/Response Types
// ============================================================================

// AnalyzeRequest is the request body for analysis
type AnalyzeRequest struct {
	NodeName   string            `json:"node_name"`
	Metrics    []MetricDataAPI   `json:"metrics"`
	Logs       []LogEntryAPI     `json:"logs,omitempty"`
	Context    map[string]string `json:"context,omitempty"`
	K8sContext *K8sMetadataAPI   `json:"k8s,omitempty"`
}

// MetricDataAPI represents a metric in the API
type MetricDataAPI struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// LogEntryAPI represents a log entry in the API
type LogEntryAPI struct {
	Message   string            `json:"message"`
	Level     string            `json:"level"`
	Timestamp string            `json:"timestamp"`
	Source    string            `json:"source,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// K8sMetadataAPI represents Kubernetes context
type K8sMetadataAPI struct {
	Namespace      string            `json:"namespace,omitempty"`
	PodName        string            `json:"pod_name,omitempty"`
	ContainerName  string            `json:"container_name,omitempty"`
	NodeName       string            `json:"node_name,omitempty"`
	DeploymentName string            `json:"deployment_name,omitempty"`
	ServiceName    string            `json:"service_name,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// AnalyzeResponse is the response from analysis
type AnalyzeResponse struct {
	Result    *AnalysisResult `json:"result"`
	Timestamp time.Time       `json:"timestamp"`
}

// ResultsResponse is the response with analysis results
type ResultsResponse struct {
	Results   []AnalysisResult `json:"results"`
	Count     int              `json:"count"`
	Timestamp time.Time        `json:"timestamp"`
}

// StatsResponse is the response with module stats
type StatsResponse struct {
	Stats     ModuleStats `json:"stats"`
	Timestamp time.Time   `json:"timestamp"`
}

// IngestRequest is the request for data ingestion
type IngestRequest struct {
	NodeName   string            `json:"node_name"`
	Metrics    []MetricDataAPI   `json:"metrics"`
	Logs       []LogEntryAPI     `json:"logs,omitempty"`
	Context    map[string]string `json:"context,omitempty"`
	K8sContext *K8sMetadataAPI   `json:"k8s,omitempty"`
}

// IngestResponse is the response from ingestion
type IngestResponse struct {
	Queued    bool      `json:"queued"`
	Timestamp time.Time `json:"timestamp"`
}

// FeedbackRequest is the request body for user feedback
type FeedbackRequest struct {
	AnalysisID     string `json:"analysis_id"`
	Rating         string `json:"rating"` // useful, not_useful
	Comments       string `json:"comments,omitempty"`
	CorrectedCause string `json:"corrected_cause,omitempty"`
}

// ============================================================================
// Helpers
// ============================================================================

func (h *APIHandler) convertRequest(req AnalyzeRequest) *queue.DataPoint {
	metrics := make([]queue.MetricData, 0, len(req.Metrics))
	for _, m := range req.Metrics {
		ts, _ := time.Parse(time.RFC3339, m.Timestamp)
		metrics = append(metrics, queue.MetricData{
			Name:      m.Name,
			Value:     m.Value,
			Timestamp: ts,
			Labels:    m.Labels,
		})
	}

	logs := make([]queue.LogEntry, 0, len(req.Logs))
	for _, l := range req.Logs {
		ts, _ := time.Parse(time.RFC3339, l.Timestamp)
		logs = append(logs, queue.LogEntry{
			Message:   l.Message,
			Level:     l.Level,
			Timestamp: ts,
			Source:    l.Source,
			Labels:    l.Labels,
		})
	}

	var k8s *queue.K8sMetadata
	if req.K8sContext != nil {
		k8s = &queue.K8sMetadata{
			Namespace:      req.K8sContext.Namespace,
			PodName:        req.K8sContext.PodName,
			ContainerName:  req.K8sContext.ContainerName,
			NodeName:       req.K8sContext.NodeName,
			DeploymentName: req.K8sContext.DeploymentName,
			ServiceName:    req.K8sContext.ServiceName,
			Labels:         req.K8sContext.Labels,
		}
	}

	return &queue.DataPoint{
		NodeName:   req.NodeName,
		Timestamp:  time.Now(),
		Metrics:    metrics,
		Logs:       logs,
		Context:    req.Context,
		K8sContext: k8s,
	}
}

func (h *APIHandler) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *APIHandler) errorResponse(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
