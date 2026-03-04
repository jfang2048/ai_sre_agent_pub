package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type storageRetentionRequest struct {
	NodeRetention         string `json:"node_retention"`
	HistorySamplesPerNode int    `json:"history_samples_per_node"`
}

func (c *Controller) handleStorageStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}

	resp := map[string]interface{}{
		"storage":   c.ingestStore.Stats(),
		"timestamp": time.Now().UTC(),
	}
	if c.logIndex != nil {
		resp["logs"] = c.logIndex.Stats()
	}
	if c.gpuStore != nil {
		resp["gpu"] = c.config.GPU
	}
	writeJSON(w, resp)
}

func (c *Controller) handleStorageRetention(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch) {
		return
	}
	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]interface{}{
			"storage":   c.ingestStore.Stats(),
			"timestamp": time.Now().UTC(),
		})
		return
	}
	if !c.requireActiveController(w) {
		return
	}

	req, err := parseStorageRetentionPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := c.ingestStore.SetRetention(req.NodeRetention, req.HistorySamplesPerNode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":    "updated",
		"storage":   c.ingestStore.Stats(),
		"timestamp": time.Now().UTC(),
	})
}

type parsedStorageRetention struct {
	NodeRetention         time.Duration
	HistorySamplesPerNode int
}

func parseStorageRetentionPayload(r *http.Request) (parsedStorageRetention, error) {
	var payload storageRetentionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if err == io.EOF {
			return parsedStorageRetention{}, errInvalidRetention("request body required")
		}
		return parsedStorageRetention{}, errInvalidRetention("invalid payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return parsedStorageRetention{}, errInvalidRetention("invalid payload")
	}

	rawRetention := strings.TrimSpace(payload.NodeRetention)
	if rawRetention == "" {
		rawRetention = strings.TrimSpace(r.URL.Query().Get("node_retention"))
	}
	if rawRetention == "" {
		return parsedStorageRetention{}, errInvalidRetention("node_retention is required")
	}
	retention, err := time.ParseDuration(rawRetention)
	if err != nil || retention <= 0 {
		return parsedStorageRetention{}, errInvalidRetention("node_retention must be a valid duration > 0")
	}

	history := payload.HistorySamplesPerNode
	if history <= 0 {
		if raw := strings.TrimSpace(r.URL.Query().Get("history_samples_per_node")); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
				history = parsed
			}
		}
	}
	if history <= 0 {
		return parsedStorageRetention{}, errInvalidRetention("history_samples_per_node must be > 0")
	}

	return parsedStorageRetention{
		NodeRetention:         retention,
		HistorySamplesPerNode: history,
	}, nil
}

func errInvalidRetention(message string) error {
	return &retentionPayloadError{message: message}
}

type retentionPayloadError struct {
	message string
}

func (e *retentionPayloadError) Error() string {
	return e.message
}
