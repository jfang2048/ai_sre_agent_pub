package controller

import (
	"fmt"
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
	handleAgentServiceRequest(
		w,
		r,
		parseAgentQueryPayload,
		func(req agentcore.QueryRequest) (agentcore.QueryResponse, error) {
			return c.agentService.Query(r.Context(), req)
		},
		agentQueryErrorStatus,
	)
}

func (c *Controller) handleAgentExecute(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	if !c.requireAgentService(w) {
		return
	}
	handleAgentServiceRequest(
		w,
		r,
		parseAgentExecutePayload,
		func(req agentcore.ExecuteRequest) (agentcore.ExecuteResponse, error) {
			return c.agentService.Execute(r.Context(), req)
		},
		agentExecuteErrorStatus,
	)
}

func handleAgentServiceRequest[Req any, Resp any](
	w http.ResponseWriter,
	r *http.Request,
	parse func(*http.Request) (Req, error),
	run func(Req) (Resp, error),
	errorStatus func(error) int,
) {
	req, err := parse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := run(req)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	writeJSON(w, resp)
}

func agentQueryErrorStatus(err error) int {
	switch {
	case err == agentcore.ErrRateLimited:
		return http.StatusTooManyRequests
	case err == agentcore.ErrBusy, err == agentcore.ErrCircuitOpen:
		return http.StatusServiceUnavailable
	case err == agentcore.ErrLLMTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func agentExecuteErrorStatus(err error) int {
	switch {
	case err == agentcore.ErrActionNotFound:
		return http.StatusNotFound
	case err == agentcore.ErrActionExpired:
		return http.StatusGone
	case err == agentcore.ErrApprovalRequired:
		return http.StatusPreconditionRequired
	case err == agentcore.ErrApprovalInvalid:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func parseAgentQueryPayload(r *http.Request) (agentcore.QueryRequest, error) {
	var payload agentcore.QueryRequest
	if err := decodeJSONBodyStrict(r, &payload, false); err != nil {
		return agentcore.QueryRequest{}, err
	}
	if payload.Query == "" {
		return agentcore.QueryRequest{}, fmt.Errorf("query is required")
	}
	return payload, nil
}

func parseAgentExecutePayload(r *http.Request) (agentcore.ExecuteRequest, error) {
	var payload agentcore.ExecuteRequest
	if err := decodeJSONBodyStrict(r, &payload, false); err != nil {
		return agentcore.ExecuteRequest{}, err
	}
	if payload.ActionID == "" {
		return agentcore.ExecuteRequest{}, fmt.Errorf("action_id is required")
	}
	return payload, nil
}

func decodeStrictJSON(r *http.Request, target any) error {
	return decodeJSONBodyStrict(r, target, false)
}

func (c *Controller) requireAgentService(w http.ResponseWriter) bool {
	if c.agentService != nil {
		return true
	}
	http.Error(w, "agent query service disabled", http.StatusServiceUnavailable)
	return false
}
