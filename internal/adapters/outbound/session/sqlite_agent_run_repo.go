package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// SqliteAgentRunRepository triển khai ports.AgentRunRepository trên cơ sở dữ liệu SQLite
type SqliteAgentRunRepository struct {
	mu sync.Mutex
	db *sql.DB
}

// NewSqliteAgentRunRepository khởi tạo repository lưu trữ tác vụ Agent
func NewSqliteAgentRunRepository(db *sql.DB) (*SqliteAgentRunRepository, error) {
	if db == nil {
		return nil, errors.New("sql.DB không được là nil")
	}

	repo := &SqliteAgentRunRepository{db: db}
	if err := repo.migrate(); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo bảng agent_runs: %w", err)
	}

	return repo, nil
}

var _ ports.AgentRunRepository = (*SqliteAgentRunRepository)(nil)

func (r *SqliteAgentRunRepository) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS agent_runs (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		idempotency_key TEXT,
		goal TEXT NOT NULL,
		status TEXT NOT NULL,
		model TEXT NOT NULL,
		workspace TEXT NOT NULL,
		current_step INTEGER NOT NULL DEFAULT 0,
		max_steps INTEGER NOT NULL DEFAULT 25,
		total_tool_calls INTEGER NOT NULL DEFAULT 0,
		stop_reason TEXT,
		final_answer TEXT,
		error TEXT,
		git_diff TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		finished_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_agent_runs_tenant_idemp ON agent_runs(tenant_id, idempotency_key);
	CREATE INDEX IF NOT EXISTS idx_agent_runs_tenant_status ON agent_runs(tenant_id, status);

	CREATE TABLE IF NOT EXISTS agent_run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		step INTEGER NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_agent_run_events_run ON agent_run_events(run_id, id ASC);
	`
	_, err := r.db.Exec(schema)
	return err
}

func (r *SqliteAgentRunRepository) Create(ctx context.Context, run *domain.AgentRun) error {
	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ hoặc thiếu ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if run.TenantID == "" {
		run.TenantID = "default"
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}

	query := `
	INSERT INTO agent_runs (
		id, tenant_id, idempotency_key, goal, status, model, workspace,
		current_step, max_steps, total_tool_calls, stop_reason, final_answer,
		error, git_diff, created_at, updated_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := r.db.ExecContext(ctx, query,
		run.ID, run.TenantID, run.IdempotencyKey, run.Goal, string(run.Status),
		run.Model, run.Workspace, run.CurrentStep, run.MaxSteps, run.TotalToolCalls,
		run.StopReason, run.FinalAnswer, run.Error, run.GitDiff,
		run.CreatedAt, run.UpdatedAt, run.FinishedAt,
	)
	return err
}

func (r *SqliteAgentRunRepository) Update(ctx context.Context, run *domain.AgentRun) error {
	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ hoặc thiếu ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	run.UpdatedAt = time.Now()

	query := `
	UPDATE agent_runs SET
		status = ?, current_step = ?, total_tool_calls = ?, stop_reason = ?,
		final_answer = ?, error = ?, git_diff = ?, updated_at = ?, finished_at = ?
	WHERE id = ?;
	`
	_, err := r.db.ExecContext(ctx, query,
		string(run.Status), run.CurrentStep, run.TotalToolCalls, run.StopReason,
		run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
		run.ID,
	)
	return err
}

func (r *SqliteAgentRunRepository) Get(ctx context.Context, runID string) (*domain.AgentRun, error) {
	query := `
	SELECT id, tenant_id, idempotency_key, goal, status, model, workspace,
	       current_step, max_steps, total_tool_calls, stop_reason, final_answer,
	       error, git_diff, created_at, updated_at, finished_at
	FROM agent_runs WHERE id = ?;
	`
	var run domain.AgentRun
	var statusStr string
	var idempKey, stopReason, finalAns, errStr, gitDiff sql.NullString
	var finAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, runID).Scan(
		&run.ID, &run.TenantID, &idempKey, &run.Goal, &statusStr, &run.Model, &run.Workspace,
		&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReason, &finalAns,
		&errStr, &gitDiff, &run.CreatedAt, &run.UpdatedAt, &finAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("không tìm thấy agent run với ID: %s", runID)
		}
		return nil, err
	}

	run.Status = domain.AgentRunStatus(statusStr)
	if idempKey.Valid {
		run.IdempotencyKey = idempKey.String
	}
	if stopReason.Valid {
		run.StopReason = stopReason.String
	}
	if finalAns.Valid {
		run.FinalAnswer = finalAns.String
	}
	if errStr.Valid {
		run.Error = errStr.String
	}
	if gitDiff.Valid {
		run.GitDiff = gitDiff.String
	}
	if finAt.Valid {
		run.FinishedAt = &finAt.Time
	}

	events, _ := r.GetEvents(ctx, runID, 0)
	run.Events = events

	return &run, nil
}

func (r *SqliteAgentRunRepository) List(ctx context.Context, tenantID string, limit, offset int) ([]*domain.AgentRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var query string
	var rows *sql.Rows
	var err error

	if tenantID != "" && tenantID != "all" {
		query = `
		SELECT id, tenant_id, idempotency_key, goal, status, model, workspace,
		       current_step, max_steps, total_tool_calls, stop_reason, final_answer,
		       error, git_diff, created_at, updated_at, finished_at
		FROM agent_runs WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID, limit, offset)
	} else {
		query = `
		SELECT id, tenant_id, idempotency_key, goal, status, model, workspace,
		       current_step, max_steps, total_tool_calls, stop_reason, final_answer,
		       error, git_diff, created_at, updated_at, finished_at
		FROM agent_runs ORDER BY created_at DESC LIMIT ? OFFSET ?;
		`
		rows, err = r.db.QueryContext(ctx, query, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.AgentRun
	for rows.Next() {
		var run domain.AgentRun
		var statusStr string
		var idempKey, stopReason, finalAns, errStr, gitDiff sql.NullString
		var finAt sql.NullTime

		if err := rows.Scan(
			&run.ID, &run.TenantID, &idempKey, &run.Goal, &statusStr, &run.Model, &run.Workspace,
			&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReason, &finalAns,
			&errStr, &gitDiff, &run.CreatedAt, &run.UpdatedAt, &finAt,
		); err != nil {
			return nil, err
		}

		run.Status = domain.AgentRunStatus(statusStr)
		if idempKey.Valid {
			run.IdempotencyKey = idempKey.String
		}
		if stopReason.Valid {
			run.StopReason = stopReason.String
		}
		if finalAns.Valid {
			run.FinalAnswer = finalAns.String
		}
		if errStr.Valid {
			run.Error = errStr.String
		}
		if gitDiff.Valid {
			run.GitDiff = gitDiff.String
		}
		if finAt.Valid {
			run.FinishedAt = &finAt.Time
		}

		runs = append(runs, &run)
	}

	return runs, nil
}

func (r *SqliteAgentRunRepository) AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error {
	if event == nil || event.RunID == "" {
		return errors.New("event không hợp lệ hoặc thiếu RunID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	query := `
	INSERT INTO agent_run_events (run_id, step, kind, message, timestamp)
	VALUES (?, ?, ?, ?, ?);
	`
	res, err := r.db.ExecContext(ctx, query, event.RunID, event.Step, event.Kind, event.Message, event.Timestamp)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err == nil {
		event.ID = id
	}
	return nil
}

func (r *SqliteAgentRunRepository) GetEvents(ctx context.Context, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	query := `
	SELECT id, run_id, step, kind, message, timestamp
	FROM agent_run_events
	WHERE run_id = ? AND id > ?
	ORDER BY id ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, runID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.AgentRunEvent
	for rows.Next() {
		var ev domain.AgentRunEvent
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.Step, &ev.Kind, &ev.Message, &ev.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func (r *SqliteAgentRunRepository) Cancel(ctx context.Context, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	query := `
	UPDATE agent_runs
	SET status = ?, stop_reason = ?, updated_at = ?, finished_at = ?
	WHERE id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval');
	`
	_, err := r.db.ExecContext(ctx, query, string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID)
	return err
}

func (r *SqliteAgentRunRepository) FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error) {
	if key == "" {
		return nil, nil
	}
	if tenantID == "" {
		tenantID = "default"
	}

	query := `
	SELECT id, tenant_id, idempotency_key, goal, status, model, workspace,
	       current_step, max_steps, total_tool_calls, stop_reason, final_answer,
	       error, git_diff, created_at, updated_at, finished_at
	FROM agent_runs WHERE tenant_id = ? AND idempotency_key = ? ORDER BY created_at DESC LIMIT 1;
	`
	var run domain.AgentRun
	var statusStr string
	var idempKey, stopReason, finalAns, errStr, gitDiff sql.NullString
	var finAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, tenantID, key).Scan(
		&run.ID, &run.TenantID, &idempKey, &run.Goal, &statusStr, &run.Model, &run.Workspace,
		&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReason, &finalAns,
		&errStr, &gitDiff, &run.CreatedAt, &run.UpdatedAt, &finAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	run.Status = domain.AgentRunStatus(statusStr)
	if idempKey.Valid {
		run.IdempotencyKey = idempKey.String
	}
	if stopReason.Valid {
		run.StopReason = stopReason.String
	}
	if finalAns.Valid {
		run.FinalAnswer = finalAns.String
	}
	if errStr.Valid {
		run.Error = errStr.String
	}
	if gitDiff.Valid {
		run.GitDiff = gitDiff.String
	}
	if finAt.Valid {
		run.FinishedAt = &finAt.Time
	}

	return &run, nil
}
