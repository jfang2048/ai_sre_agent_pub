package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
)

func (c *Controller) handleInferenceOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if len(collectorID) > 256 || strings.ContainsAny(collectorID, "\x00\r\n") {
		http.Error(w, "invalid collector_id", http.StatusBadRequest)
		return
	}
	now := time.Now()
	report := inferenceobs.Report{GeneratedAt: now, StaleAfterSeconds: inferenceobs.DefaultConfig().StaleAfter.Seconds(), Endpoints: []inferenceobs.Endpoint{}, GPUs: []inferenceobs.GPU{}}
	if c.inferenceStore != nil {
		report = c.inferenceStore.Snapshot(collectorID, now)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(report)
}
