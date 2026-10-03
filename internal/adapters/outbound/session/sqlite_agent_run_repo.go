package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var (
	ErrRunNotFound           = errors.New("agent run not found")
	ErrIdempotencyConflict   = errors.New("idempotency conflict: a run with the same key already exists")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
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
		parent_run_id TEXT,
		resume_from_run_id TEXT,
		worker_id TEXT,
		lease_until DATETIME,
		heartbeat_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		finished_at DATETIME
	);
	CREATE UNIQUE INDEX IF NOT EXISTS uidx_agent_runs_tenant_idemp 
		ON agent_runs(tenant_id, idempotency_key) 
		WHERE idempotency_key IS NOT NULL AND idempotency_key != '';
	CREATE INDEX IF NOT EXISTS idx_agent_runs_tenant_status ON agent_runs(tenant_id, status);
	CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status);

	CREATE TABLE IF NOT EXISTS agent_run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		run_id TEXT NOT NULL,
		step INTEGER NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_agent_run_events_tenant_run ON agent_run_events(tenant_id, run_id, id ASC);
	CREATE INDEX IF NOT EXISTS idx_agent_run_events_run ON agent_run_events(run_id, id ASC);
	`
	if _, err := r.db.Exec(schema); err != nil {
		return err
	}

	// Chạy migration bổ sung cho các database đã tồn tại từ phiên bản cũ
	columns := []string{
		"ALTER TABLE agent_runs ADD COLUMN parent_run_id TEXT;",
		"ALTER TABLE agent_runs ADD COLUMN resume_from_run_id TEXT;",
		"ALTER TABLE agent_runs ADD COLUMN worker_id TEXT;",
		"ALTER TABLE agent_runs ADD COLUMN lease_until DATETIME;",
		"ALTER TABLE agent_runs ADD COLUMN heartbeat_at DATETIME;",
		"ALTER TABLE agent_run_events ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';",
	}
	for _, alter := range columns {
		_, _ = r.db.Exec(alter)
	}

	// Đảm bảo UNIQUE index được kích hoạt
	_, _ = r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uidx_agent_runs_tenant_idemp 
		ON agent_runs(tenant_id, idempotency_key) 
		WHERE idempotency_key IS NOT NULL AND idempotency_key != '';`)
	return nil
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

	var idempKeyVal any = nil
	if strings.TrimSpace(run.IdempotencyKey) != "" {
		idempKeyVal = strings.TrimSpace(run.IdempotencyKey)
	}

	query := `
	INSERT INTO agent_runs (
		id, tenant_id, idempotency_key, goal, status, model, workspace,
		current_step, max_steps, total_tool_calls, stop_reason, final_answer,
		error, git_diff, parent_run_id, resume_from_run_id, worker_id, lease_until, heartbeat_at,
		created_at, updated_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := r.db.ExecContext(ctx, query,
		run.ID, run.TenantID, idempKeyVal, run.Goal, string(run.Status),
		run.Model, run.Workspace, run.CurrentStep, run.MaxSteps, run.TotalToolCalls,
		run.StopReason, run.FinalAnswer, run.Error, run.GitDiff,
		run.ParentRunID, run.ResumeFromRunID, run.WorkerID, run.LeaseUntil, run.HeartbeatAt,
		run.CreatedAt, run.UpdatedAt, run.FinishedAt,
	)
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "unique") || strings.Contains(errLower, "constraint") {
			return ErrIdempotencyConflict
		}
		return err
	}
	return nil
}

func (r *SqliteAgentRunRepository) Update(ctx context.Context, run *domain.AgentRun) error {
	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ hoặc thiếu ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	run.UpdatedAt = time.Now()

	// State-aware update: Không cho phép worker overwrite sang completed/failed nếu tác vụ đã bị cancelled
	var query string
	var args []any
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed {
		query = `
		UPDATE agent_runs SET
			status = ?, current_step = ?, total_tool_calls = ?, stop_reason = ?,
			final_answer = ?, error = ?, git_diff = ?, updated_at = ?, finished_at = ?
		WHERE id = ? AND status != 'cancelled';
		`
		args = []any{
			string(run.Status), run.CurrentStep, run.TotalToolCalls, run.StopReason,
			run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
			run.ID,
		}
	} else {
		query = `
		UPDATE agent_runs SET
			status = ?, current_step = ?, total_tool_calls = ?, stop_reason = ?,
			final_answer = ?, error = ?, git_diff = ?, updated_at = ?, finished_at = ?
		WHERE id = ?;
		`
		args = []any{
			string(run.Status), run.CurrentStep, run.TotalToolCalls, run.StopReason,
			run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
			run.ID,
		}
	}

	_, err := r.db.ExecContext(ctx, query, args...)
	return err
}

func (r *SqliteAgentRunRepository) UpdateWithTransition(ctx context.Context, run *domain.AgentRun, allowedFromStatuses ...domain.AgentRunStatus) (bool, error) {
	if run == nil || run.ID == "" {
		return false, errors.New("run không hợp lệ hoặc thiếu ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	run.UpdatedAt = time.Now()

	query := `
	UPDATE agent_runs SET
		status = ?, current_step = ?, total_tool_calls = ?, stop_reason = ?,
		final_answer = ?, error = ?, git_diff = ?, updated_at = ?, finished_at = ?
	WHERE id = ?
	`
	args := []any{
		string(run.Status), run.CurrentStep, run.TotalToolCalls, run.StopReason,
		run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
		run.ID,
	}

	if len(allowedFromStatuses) > 0 {
		placeholders := make([]string, len(allowedFromStatuses))
		for i, st := range allowedFromStatuses {
			placeholders[i] = "?"
			args = append(args, string(st))
		}
		query += fmt.Sprintf(" AND status IN (%s);", strings.Join(placeholders, ","))
	} else {
		query += ";"
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func scanAgentRun(scanner interface{ Scan(dest ...any) error }) (*domain.AgentRun, error) {
	var run domain.AgentRun
	var statusStr string
	var idempKey, stopReason, finalAns, errStr, gitDiff sql.NullString
	var parentID, resumeID, workerID sql.NullString
	var leaseUntil, heartbeatAt, finAt sql.NullTime

	err := scanner.Scan(
		&run.ID, &run.TenantID, &idempKey, &run.Goal, &statusStr, &run.Model, &run.Workspace,
		&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReason, &finalAns,
		&errStr, &gitDiff, &parentID, &resumeID, &workerID, &leaseUntil, &heartbeatAt,
		&run.CreatedAt, &run.UpdatedAt, &finAt,
	)
	if err != nil {
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
	if parentID.Valid {
		run.ParentRunID = parentID.String
	}
	if resumeID.Valid {
		run.ResumeFromRunID = resumeID.String
	}
	if workerID.Valid {
		run.WorkerID = workerID.String
	}
	if leaseUntil.Valid {
		run.LeaseUntil = &leaseUntil.Time
	}
	if heartbeatAt.Valid {
		run.HeartbeatAt = &heartbeatAt.Time
	}
	if finAt.Valid {
		run.FinishedAt = &finAt.Time
	}

	return &run, nil
}

const selectRunCols = `id, tenant_id, idempotency_key, goal, status, model, workspace,
	current_step, max_steps, total_tool_calls, stop_reason, final_answer,
	error, git_diff, parent_run_id, resume_from_run_id, worker_id, lease_until, heartbeat_at,
	created_at, updated_at, finished_at`

func (r *SqliteAgentRunRepository) Get(ctx context.Context, runID string) (*domain.AgentRun, error) {
	query := fmt.Sprintf("SELECT %s FROM agent_runs WHERE id = ?;", selectRunCols)
	row := r.db.QueryRowContext(ctx, query, runID)
	run, err := scanAgentRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
		}
		return nil, err
	}

	events, _ := r.GetEvents(ctx, runID, 0)
	run.Events = events
	return run, nil
}

func (r *SqliteAgentRunRepository) GetForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error) {
	var query string
	var args []any

	if tenantID != "" && tenantID != "all" {
		query = fmt.Sprintf("SELECT %s FROM agent_runs WHERE id = ? AND tenant_id = ?;", selectRunCols)
		args = []any{runID, tenantID}
	} else {
		query = fmt.Sprintf("SELECT %s FROM agent_runs WHERE id = ?;", selectRunCols)
		args = []any{runID}
	}

	row := r.db.QueryRowContext(ctx, query, args...)
	run, err := scanAgentRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
		}
		return nil, err
	}

	events, _ := r.GetEventsForTenant(ctx, tenantID, runID, 0)
	run.Events = events
	return run, nil
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
		query = fmt.Sprintf("SELECT %s FROM agent_runs WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;", selectRunCols)
		rows, err = r.db.QueryContext(ctx, query, tenantID, limit, offset)
	} else {
		query = fmt.Sprintf("SELECT %s FROM agent_runs ORDER BY created_at DESC LIMIT ? OFFSET ?;", selectRunCols)
		rows, err = r.db.QueryContext(ctx, query, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.AgentRun
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	return runs, nil
}

func (r *SqliteAgentRunRepository) ListPendingRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	query := fmt.Sprintf(`SELECT %s FROM agent_runs 
		WHERE status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering') 
		ORDER BY created_at ASC;`, selectRunCols)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.AgentRun
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (r *SqliteAgentRunRepository) AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error {
	if event == nil || event.RunID == "" {
		return errors.New("event không hợp lệ hoặc thiếu RunID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if event.TenantID == "" {
		var tid string
		_ = r.db.QueryRowContext(ctx, "SELECT tenant_id FROM agent_runs WHERE id = ?", event.RunID).Scan(&tid)
		if tid != "" {
			event.TenantID = tid
		} else {
			event.TenantID = "default"
		}
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	query := `
	INSERT INTO agent_run_events (tenant_id, run_id, step, kind, message, timestamp)
	VALUES (?, ?, ?, ?, ?, ?);
	`
	res, err := r.db.ExecContext(ctx, query, event.TenantID, event.RunID, event.Step, event.Kind, event.Message, event.Timestamp)
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
	SELECT id, tenant_id, run_id, step, kind, message, timestamp
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
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.RunID, &ev.Step, &ev.Kind, &ev.Message, &ev.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func (r *SqliteAgentRunRepository) GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	var query string
	var args []any

	if tenantID != "" && tenantID != "all" {
		query = `
		SELECT e.id, e.tenant_id, e.run_id, e.step, e.kind, e.message, e.timestamp
		FROM agent_run_events e
		JOIN agent_runs r ON r.id = e.run_id
		WHERE e.run_id = ? AND r.tenant_id = ? AND e.id > ?
		ORDER BY e.id ASC;
		`
		args = []any{runID, tenantID, afterID}
	} else {
		query = `
		SELECT e.id, e.tenant_id, e.run_id, e.step, e.kind, e.message, e.timestamp
		FROM agent_run_events e
		WHERE e.run_id = ? AND e.id > ?
		ORDER BY e.id ASC;
		`
		args = []any{runID, afterID}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.AgentRunEvent
	for rows.Next() {
		var ev domain.AgentRunEvent
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.RunID, &ev.Step, &ev.Kind, &ev.Message, &ev.Timestamp); err != nil {
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
	WHERE id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
	`
	_, err := r.db.ExecContext(ctx, query, string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID)
	return err
}

func (r *SqliteAgentRunRepository) CancelForTenant(ctx context.Context, tenantID, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	var query string
	var args []any

	if tenantID != "" && tenantID != "all" {
		query = `
		UPDATE agent_runs
		SET status = ?, stop_reason = ?, updated_at = ?, finished_at = ?
		WHERE id = ? AND tenant_id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
		`
		args = []any{string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID, tenantID}
	} else {
		query = `
		UPDATE agent_runs
		SET status = ?, stop_reason = ?, updated_at = ?, finished_at = ?
		WHERE id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
		`
		args = []any{string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID}
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Kiểm tra xem run có tồn tại cho tenant không
		var exists int
		checkQuery := "SELECT COUNT(1) FROM agent_runs WHERE id = ?"
		checkArgs := []any{runID}
		if tenantID != "" && tenantID != "all" {
			checkQuery += " AND tenant_id = ?"
			checkArgs = append(checkArgs, tenantID)
		}
		_ = r.db.QueryRowContext(ctx, checkQuery, checkArgs...).Scan(&exists)
		if exists == 0 {
			return fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
		}
	}
	return nil
}

func (r *SqliteAgentRunRepository) ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	leaseUntil := now.Add(leaseDuration)

	query := `
	UPDATE agent_runs
	SET status = 'running', worker_id = ?, lease_until = ?, heartbeat_at = ?, updated_at = ?
	WHERE id = ? AND (status = 'queued' OR (status = 'running' AND lease_until < ?));
	`
	res, err := r.db.ExecContext(ctx, query, workerID, leaseUntil, now, now, runID, now)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *SqliteAgentRunRepository) FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error) {
	if key == "" {
		return nil, nil
	}
	if tenantID == "" {
		tenantID = "default"
	}

	query := fmt.Sprintf(`SELECT %s FROM agent_runs WHERE tenant_id = ? AND idempotency_key = ? ORDER BY created_at DESC LIMIT 1;`, selectRunCols)
	row := r.db.QueryRowContext(ctx, query, tenantID, key)
	run, err := scanAgentRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return run, nil
}
