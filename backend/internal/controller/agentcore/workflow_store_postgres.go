package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	workflowRunStoreBackendPostgres = "postgres"
)

// PostgresDurableStore keeps workflow runtime metadata in PostgreSQL so run
// history, approvals, verification state, and idempotency survive controller
// replacement. Artifact payloads still depend on the configured artifact
// backend and payload root.
type PostgresDurableStore struct {
	db *sql.DB
}

func NewPostgresDurableStore(ctx context.Context, dsn string) (*PostgresDurableStore, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("workflow postgres dsn is empty")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	store, err := NewPostgresDurableStoreWithDB(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func NewPostgresDurableStoreWithDB(ctx context.Context, db *sql.DB) (*PostgresDurableStore, error) {
	if db == nil {
		return nil, fmt.Errorf("workflow postgres db is nil")
	}
	store := &PostgresDurableStore{db: db}
	if err := store.initSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PostgresDurableStore) initSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workflow_runs (
			run_id TEXT PRIMARY KEY,
			workflow_type TEXT NOT NULL,
			collector_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			current_step TEXT NOT NULL DEFAULT '',
			current_stage TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			payload JSONB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_lookup
			ON workflow_runs (workflow_type, collector_id, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS workflow_events (
			event_id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL REFERENCES workflow_runs(run_id) ON DELETE CASCADE,
			workflow_type TEXT NOT NULL DEFAULT '',
			collector_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL,
			event_at TIMESTAMPTZ NOT NULL,
			payload JSONB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_events_run_time
			ON workflow_events (run_id, event_at DESC)`,
		`CREATE TABLE IF NOT EXISTS workflow_idempotency (
			idempotency_key TEXT NOT NULL,
			run_id TEXT NOT NULL REFERENCES workflow_runs(run_id) ON DELETE CASCADE,
			tool_call_id TEXT NOT NULL,
			workflow_type TEXT NOT NULL DEFAULT '',
			collector_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			completed_at TIMESTAMPTZ,
			run_updated_at TIMESTAMPTZ NOT NULL,
			payload JSONB NOT NULL,
			PRIMARY KEY (idempotency_key, run_id, tool_call_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_idempotency_lookup
			ON workflow_idempotency (idempotency_key, run_updated_at DESC, completed_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresDurableStore) SaveRun(ctx context.Context, run *DurableRun) error {
	toStore := cloneDurableRun(run)
	if toStore == nil {
		return fmt.Errorf("run cannot be nil")
	}
	normalizeDurableRun(toStore)
	payload, err := json.Marshal(toStore)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackSQLTx(tx)

	if _, err := tx.ExecContext(ctx, `
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
	`, toStore.RunID, toStore.WorkflowType, toStore.CollectorID, string(toStore.Status), toStore.CurrentStep, toStore.CurrentStage, toStore.CreatedAt, toStore.UpdatedAt, payload); err != nil {
		return err
	}
	if err := syncPostgresIdempotencyRecords(ctx, tx, toStore); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresDurableStore) LoadRun(ctx context.Context, runID string) (*DurableRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT payload FROM workflow_runs WHERE run_id = $1`, strings.TrimSpace(runID))
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", runID)
		}
		return nil, err
	}
	var run DurableRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, err
	}
	if normalizeDurableRun(&run) {
		if err := s.SaveRun(ctx, &run); err != nil {
			return nil, err
		}
	}
	return &run, nil
}

func (s *PostgresDurableStore) AppendEvent(ctx context.Context, runID string, event WorkflowEvent) error {
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Events = append(run.Events, event)
	run.UpdatedAt = time.Now().UTC()
	normalizeDurableRun(run)
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	eventPayload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackSQLTx(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_runs
		SET status = $2, current_step = $3, current_stage = $4, updated_at = $5, payload = $6
		WHERE run_id = $1
	`, run.RunID, string(run.Status), run.CurrentStep, run.CurrentStage, run.UpdatedAt, payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_events (event_id, run_id, workflow_type, collector_id, event_type, event_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (event_id) DO NOTHING
	`, event.EventID, run.RunID, run.WorkflowType, run.CollectorID, event.Type, event.Timestamp, eventPayload); err != nil {
		return err
	}
	if err := syncPostgresIdempotencyRecords(ctx, tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresDurableStore) RecordReplay(ctx context.Context, runID string) (*DurableRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackSQLTx(tx)

	var payload []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT payload FROM workflow_runs WHERE run_id = $1 FOR UPDATE
	`, strings.TrimSpace(runID)).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", runID)
		}
		return nil, err
	}
	var run DurableRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, err
	}
	recordRunReplay(&run)
	payload, err = json.Marshal(&run)
	if err != nil {
		return nil, err
	}
	event := run.Events[len(run.Events)-1]
	eventPayload, err := json.Marshal(event.Payload)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_runs
		SET status = $2, current_step = $3, current_stage = $4, updated_at = $5, payload = $6
		WHERE run_id = $1
	`, run.RunID, string(run.Status), run.CurrentStep, run.CurrentStage, run.UpdatedAt, payload); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_events (event_id, run_id, workflow_type, collector_id, event_type, event_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, event.EventID, run.RunID, run.WorkflowType, run.CollectorID, event.Type, event.Timestamp, eventPayload); err != nil {
		return nil, err
	}
	if err := syncPostgresIdempotencyRecords(ctx, tx, &run); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *PostgresDurableStore) ListRuns(ctx context.Context, filter RunListFilter) ([]*DurableRun, error) {
	query := `SELECT payload FROM workflow_runs`
	args := make([]any, 0, 6)
	where := make([]string, 0, 5)
	if value := strings.TrimSpace(filter.WorkflowType); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf("workflow_type = $%d", len(args)))
	}
	if value := strings.TrimSpace(filter.CollectorID); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf("collector_id = $%d", len(args)))
	}
	if value := strings.TrimSpace(string(filter.Status)); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if !filter.Since.IsZero() {
		args = append(args, filter.Since)
		where = append(where, fmt.Sprintf("updated_at >= $%d", len(args)))
	}
	if !filter.Until.IsZero() {
		args = append(args, filter.Until)
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*DurableRun, 0, maxListCapacity(1, filter.Limit))
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var run DurableRun
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, err
		}
		out = append(out, &run)
	}
	return out, rows.Err()
}

func (s *PostgresDurableStore) FindReusableToolCallByIdempotency(ctx context.Context, key string) (*WorkflowToolCall, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload
		FROM workflow_idempotency
		WHERE idempotency_key = $1
		ORDER BY run_updated_at DESC, completed_at DESC NULLS LAST
	`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var call WorkflowToolCall
		if err := json.Unmarshal(payload, &call); err != nil {
			return nil, err
		}
		if workflowToolCallReusable(call) {
			return &call, nil
		}
	}
	return nil, rows.Err()
}

func (s *PostgresDurableStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func syncPostgresIdempotencyRecords(ctx context.Context, tx *sql.Tx, run *DurableRun) error {
	if run == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_idempotency WHERE run_id = $1`, run.RunID); err != nil {
		return err
	}
	for _, call := range run.ToolCalls {
		key := strings.TrimSpace(call.IdempotencyKey)
		if key == "" {
			continue
		}
		payload, err := json.Marshal(call)
		if err != nil {
			return err
		}
		var completedAt any
		if !call.CompletedAt.IsZero() {
			completedAt = call.CompletedAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workflow_idempotency (
				idempotency_key, run_id, tool_call_id, workflow_type, collector_id, status, completed_at, run_updated_at, payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, key, run.RunID, call.ID, run.WorkflowType, run.CollectorID, call.Status, completedAt, run.UpdatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

func rollbackSQLTx(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func maxListCapacity(values ...int) int {
	best := 0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}
