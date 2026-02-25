package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
)

func (c *Controller) handleAgentQuery(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireAgentService(w) {
		return
	}

	req, err := parseAgentQueryPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := c.agentService.Query(r.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case err == agentcore.ErrRateLimited:
			status = http.StatusTooManyRequests
		case err == agentcore.ErrBusy:
			status = http.StatusServiceUnavailable
		case err == agentcore.ErrLLMTimeout:
			status = http.StatusGatewayTimeout
		case err == agentcore.ErrCircuitOpen:
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, resp)
}

func (c *Controller) handleAgentExecute(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireAgentService(w) {
		return
	}

	req, err := parseAgentExecutePayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := c.agentService.Execute(r.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case err == agentcore.ErrActionNotFound:
			status = http.StatusNotFound
		case err == agentcore.ErrActionExpired:
			status = http.StatusGone
		case err == agentcore.ErrApprovalRequired:
			status = http.StatusPreconditionRequired
		case err == agentcore.ErrApprovalInvalid:
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, resp)
}

func parseAgentQueryPayload(r *http.Request) (agentcore.QueryRequest, error) {
	var payload agentcore.QueryRequest
	if err := decodeStrictJSON(r, &payload); err != nil {
		return agentcore.QueryRequest{}, err
	}
	if payload.Query == "" {
		return agentcore.QueryRequest{}, fmt.Errorf("query is required")
	}
	return payload, nil
}

func parseAgentExecutePayload(r *http.Request) (agentcore.ExecuteRequest, error) {
	var payload agentcore.ExecuteRequest
	if err := decodeStrictJSON(r, &payload); err != nil {
		return agentcore.ExecuteRequest{}, err
	}
	if payload.ActionID == "" {
		return agentcore.ExecuteRequest{}, fmt.Errorf("action_id is required")
	}
	return payload, nil
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid payload")
	}
	return nil
}

func (c *Controller) requireAgentService(w http.ResponseWriter) bool {
	if c.agentService != nil {
		return true
	}
	http.Error(w, "agent query service disabled", http.StatusServiceUnavailable)
	return false
}
