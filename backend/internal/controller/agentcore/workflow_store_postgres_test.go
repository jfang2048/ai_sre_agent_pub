package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPostgresDurableStoreSaveLoadListAndIdempotency(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS workflow_runs`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_lookup`,
		`CREATE TABLE IF NOT EXISTS workflow_events`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_events_run_time`,
		`CREATE TABLE IF NOT EXISTS workflow_idempotency`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_idempotency_lookup`,
	} {
		mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 0))
	}

	store, err := NewPostgresDurableStoreWithDB(context.Background(), db)
	require.NoError(t, err)

	run := &DurableRun{
		RunID:        "run-pg-1",
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Status:       RunStatusSuspended,
		CurrentStep:  "await_approval",
		CurrentStage: "guarded_execution_plan",
		CreatedAt:    time.Now().UTC().Add(-1 * time.Minute),
		UpdatedAt:    time.Now().UTC(),
		ToolCalls: []WorkflowToolCall{
			{
				ID:             "call-1",
				Tool:           ToolRemediation,
				Stage:          "guarded_execution_plan",
				IdempotencyKey: "idem-restart",
				Status:         WorkflowToolOutcomeProposedOnly,
				Policy: ActionPolicyDecision{
					Status:       "proposal_only",
					ProposalOnly: true,
				},
				StartedAt:   time.Now().UTC().Add(-30 * time.Second),
				CompletedAt: time.Now().UTC().Add(-25 * time.Second),
			},
		},
		Steps: []DurableStepRecord{
			{
				StepID: "step-1",
				Approval: &DurableApprovalRecord{
					State:       "pending",
					Actor:       "approver/team-a",
					RequestedAt: time.Now().UTC().Add(-15 * time.Second),
				},
			},
		},
	}
	normalizedRun := cloneDurableRun(run)
	require.NotNil(t, normalizedRun)
	normalizeDurableRun(normalizedRun)
	runPayload, err := json.Marshal(normalizedRun)
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO workflow_runs (
			run_id, workflow_type, collector_id, status, current_step, current_stage, created_at, updated_at, payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (run_id) DO UPDATE SET
			workflow_type = EXCLUDED.workflow_type,
			collector_id = EXCLUDED.collector_id,
			status = EXCLUDED.status,
			current_step = EXCLUDED.current_step,
			current_stage = EXCLUDED.current_stage,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			payload = EXCLUDED.payload
	`)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workflow_idempotency WHERE run_id = $1`)).
		WithArgs(run.RunID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
			INSERT INTO workflow_idempotency (
				idempotency_key, run_id, tool_call_id, workflow_type, collector_id, status, completed_at, run_updated_at, payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`)).
		WithArgs(
			run.ToolCalls[0].IdempotencyKey,
			run.RunID,
			run.ToolCalls[0].ID,
			run.WorkflowType,
			run.CollectorID,
			run.ToolCalls[0].Status,
			sqlmock.AnyArg(),
			run.UpdatedAt,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, store.SaveRun(context.Background(), run))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT payload FROM workflow_runs WHERE run_id = $1`)).
		WithArgs(run.RunID).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(runPayload))
	loaded, err := store.LoadRun(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Equal(t, run.RunID, loaded.RunID)
	require.NotNil(t, loaded.Steps[0].Approval)
	require.Equal(t, "pending", loaded.Steps[0].Approval.State)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT payload FROM workflow_runs WHERE workflow_type = $1 ORDER BY updated_at DESC LIMIT $2`)).
		WithArgs("rca", 10).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(runPayload))
	runs, err := store.ListRuns(context.Background(), RunListFilter{WorkflowType: "rca", Limit: 10})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "collector-a", runs[0].CollectorID)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT payload
		FROM workflow_idempotency
		WHERE idempotency_key = $1
		ORDER BY run_updated_at DESC, completed_at DESC NULLS LAST
	`)).
		WithArgs("idem-restart").
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(runPayloadForCall(t, run.ToolCalls[0])))
	call, err := store.FindReusableToolCallByIdempotency(context.Background(), "idem-restart")
	require.NoError(t, err)
	require.NotNil(t, call)
	require.Equal(t, "call-1", call.ID)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresDurableStoreRecordReplayIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS workflow_runs`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_lookup`,
		`CREATE TABLE IF NOT EXISTS workflow_events`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_events_run_time`,
		`CREATE TABLE IF NOT EXISTS workflow_idempotency`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_idempotency_lookup`,
	} {
		mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	store, err := NewPostgresDurableStoreWithDB(context.Background(), db)
	require.NoError(t, err)

	run := &DurableRun{
		RunID:        "run-pg-replay",
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Status:       RunStatusCompleted,
		CurrentStep:  "finalize",
		CreatedAt:    time.Now().UTC().Add(-time.Minute),
		UpdatedAt:    time.Now().UTC(),
	}
	payload, err := json.Marshal(run)
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT payload FROM workflow_runs WHERE run_id = $1 FOR UPDATE
	`)).
		WithArgs(run.RunID).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(payload))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE workflow_runs
		SET status = $2, current_step = $3, current_stage = $4, updated_at = $5, payload = $6
		WHERE run_id = $1
	`)).
		WithArgs(run.RunID, string(run.Status), run.CurrentStep, run.CurrentStage, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO workflow_events (event_id, run_id, workflow_type, collector_id, event_type, event_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`)).
		WithArgs(sqlmock.AnyArg(), run.RunID, run.WorkflowType, run.CollectorID, "run_replayed", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workflow_idempotency WHERE run_id = $1`)).
		WithArgs(run.RunID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	replayed, err := store.RecordReplay(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, replayed.ReplayCount)
	require.Len(t, replayed.Events, 1)
	require.Equal(t, "run_replayed", replayed.Events[0].Type)
	require.Equal(t, "metadata_only", replayed.Events[0].Payload["semantics"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func runPayloadForCall(t *testing.T, call WorkflowToolCall) []byte {
	t.Helper()
	normalized := call
	normalizeWorkflowToolCall(&normalized)
	payload, err := json.Marshal(normalized)
	require.NoError(t, err)
	return payload
}
