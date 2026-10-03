package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var (
	ErrRunNotFound             = errors.New("agent run not found")
	ErrIdempotencyConflict     = errors.New("idempotency conflict: a run with the same key already exists")
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

func getTableColumns(db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s);", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func addColumnIfNotExists(db *sql.DB, table, colName, colDef string) error {
	cols, err := getTableColumns(db, table)
	if err != nil {
		return err
	}
	if cols[strings.ToLower(colName)] {
		return nil
	}
	query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s;", table, colName, colDef)
	if _, err := db.Exec(query); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("không thể thêm cột %s vào bảng %s: %w", colName, table, err)
	}
	return nil
}

func (r *SqliteAgentRunRepository) migrate() error {
	// 1. CREATE TABLE IF NOT EXISTS (tạo cấu trúc cơ bản nếu chưa có)
	createRuns := `
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
		claim_generation INTEGER NOT NULL DEFAULT 0,
		billing_key_id TEXT,
		lease_until DATETIME,
		heartbeat_at DATETIME,
		security_context TEXT,
		execution_config TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		finished_at DATETIME
	);`
	if _, err := r.db.Exec(createRuns); err != nil {
		return fmt.Errorf("lỗi khởi tạo bảng agent_runs: %w", err)
	}

	createEvents := `
	CREATE TABLE IF NOT EXISTS agent_run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		run_id TEXT NOT NULL,
		step INTEGER NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);`
	if _, err := r.db.Exec(createEvents); err != nil {
		return fmt.Errorf("lỗi khởi tạo bảng agent_run_events: %w", err)
	}

	createToolExecutions := `
	CREATE TABLE IF NOT EXISTS agent_tool_executions (
		tenant_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		args_hash TEXT,
		status TEXT NOT NULL,
		result_json TEXT,
		error TEXT,
		worker_id TEXT,
		claim_generation INTEGER,
		started_at DATETIME,
		finished_at DATETIME,
		PRIMARY KEY (tenant_id, run_id, tool_call_id)
	);`
	if _, err := r.db.Exec(createToolExecutions); err != nil {
		return fmt.Errorf("lỗi khởi tạo bảng agent_tool_executions: %w", err)
	}

	// 2, 3, 4. Kiểm tra PRAGMA table_info và ALTER TABLE ADD COLUMN cho các trường thiếu
	runColsToAdd := []struct {
		name string
		def  string
	}{
		{"tenant_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"idempotency_key", "TEXT"},
		{"parent_run_id", "TEXT"},
		{"resume_from_run_id", "TEXT"},
		{"worker_id", "TEXT"},
		{"claim_generation", "INTEGER NOT NULL DEFAULT 0"},
		{"billing_key_id", "TEXT"},
		{"lease_until", "DATETIME"},
		{"heartbeat_at", "DATETIME"},
		{"security_context", "TEXT"},
		{"execution_config", "TEXT"},
	}
	for _, col := range runColsToAdd {
		if err := addColumnIfNotExists(r.db, "agent_runs", col.name, col.def); err != nil {
			return err
		}
	}

	eventColsToAdd := []struct {
		name string
		def  string
	}{
		{"tenant_id", "TEXT NOT NULL DEFAULT 'default'"},
	}
	for _, col := range eventColsToAdd {
		if err := addColumnIfNotExists(r.db, "agent_run_events", col.name, col.def); err != nil {
			return err
		}
	}

	// 5. Backfill: giá trị default đã được SQLite áp dụng qua DEFAULT 'default'

	// 6. CREATE INDEX IF NOT EXISTS (sau khi tất cả các cột đã được đảm bảo tồn tại)
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_agent_runs_tenant_status ON agent_runs(tenant_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status);",
		"CREATE INDEX IF NOT EXISTS idx_agent_run_events_tenant_run ON agent_run_events(tenant_id, run_id, id ASC);",
		"CREATE INDEX IF NOT EXISTS idx_agent_run_events_run ON agent_run_events(run_id, id ASC);",
	}
	for _, idx := range indexes {
		if _, err := r.db.Exec(idx); err != nil {
			return fmt.Errorf("lỗi tạo index: %w", err)
		}
	}

	// 7. CREATE UNIQUE INDEX IF NOT EXISTS
	uniqueIdx := `CREATE UNIQUE INDEX IF NOT EXISTS uidx_agent_runs_tenant_idemp 
		ON agent_runs(tenant_id, idempotency_key) 
		WHERE idempotency_key IS NOT NULL AND idempotency_key != '';`
	if _, err := r.db.Exec(uniqueIdx); err != nil {
		return fmt.Errorf("lỗi tạo unique index: %w", err)
	}

	// 8. Verify schema
	runsCols, err := getTableColumns(r.db, "agent_runs")
	if err != nil {
		return fmt.Errorf("lỗi kiểm tra schema agent_runs: %w", err)
	}
	for _, col := range runColsToAdd {
		if !runsCols[strings.ToLower(col.name)] {
			return fmt.Errorf("xác thực schema thất bại: thiếu cột %s trong bảng agent_runs", col.name)
		}
	}

	eventCols, err := getTableColumns(r.db, "agent_run_events")
	if err != nil {
		return fmt.Errorf("lỗi kiểm tra schema agent_run_events: %w", err)
	}
	for _, col := range eventColsToAdd {
		if !eventCols[strings.ToLower(col.name)] {
			return fmt.Errorf("xác thực schema thất bại: thiếu cột %s trong bảng agent_run_events", col.name)
		}
	}

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

	var secCtxVal any = nil
	if run.SecurityContext != nil {
		if b, err := json.Marshal(run.SecurityContext); err == nil {
			secCtxVal = string(b)
		}
	}

	var execCfgVal any = nil
	if run.ExecutionConfig != nil {
		if b, err := json.Marshal(run.ExecutionConfig); err == nil {
			execCfgVal = string(b)
		}
	}

	query := `
	INSERT INTO agent_runs (
		id, tenant_id, idempotency_key, goal, status, model, workspace,
		current_step, max_steps, total_tool_calls, stop_reason, final_answer,
		error, git_diff, parent_run_id, resume_from_run_id, worker_id, claim_generation, billing_key_id,
		lease_until, heartbeat_at, security_context, execution_config, created_at, updated_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := r.db.ExecContext(ctx, query,
		run.ID, run.TenantID, idempKeyVal, run.Goal, string(run.Status),
		run.Model, run.Workspace, run.CurrentStep, run.MaxSteps, run.TotalToolCalls,
		run.StopReason, run.FinalAnswer, run.Error, run.GitDiff,
		run.ParentRunID, run.ResumeFromRunID, run.WorkerID, run.ClaimGeneration, run.BillingKeyID,
		run.LeaseUntil, run.HeartbeatAt,
		secCtxVal, execCfgVal, run.CreatedAt, run.UpdatedAt, run.FinishedAt,
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
	var parentID, resumeID, workerID, billingKeyID sql.NullString
	var leaseUntil, heartbeatAt, finAt sql.NullTime
	var secCtxStr, execCfgStr sql.NullString

	err := scanner.Scan(
		&run.ID, &run.TenantID, &idempKey, &run.Goal, &statusStr, &run.Model, &run.Workspace,
		&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReason, &finalAns,
		&errStr, &gitDiff, &parentID, &resumeID, &workerID, &run.ClaimGeneration, &billingKeyID,
		&leaseUntil, &heartbeatAt, &secCtxStr, &execCfgStr, &run.CreatedAt, &run.UpdatedAt, &finAt,
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
	if billingKeyID.Valid {
		run.BillingKeyID = billingKeyID.String
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
	if secCtxStr.Valid && secCtxStr.String != "" {
		var sec domain.AgentSecurityContext
		if err := json.Unmarshal([]byte(secCtxStr.String), &sec); err == nil {
			run.SecurityContext = &sec
		}
	}
	if execCfgStr.Valid && execCfgStr.String != "" {
		var cfg domain.AgentExecutionConfig
		if err := json.Unmarshal([]byte(execCfgStr.String), &cfg); err == nil {
			run.ExecutionConfig = &cfg
		}
	}

	return &run, nil
}

const selectRunCols = `id, tenant_id, idempotency_key, goal, status, model, workspace,
	current_step, max_steps, total_tool_calls, stop_reason, final_answer,
	error, git_diff, parent_run_id, resume_from_run_id, worker_id, claim_generation, billing_key_id,
	lease_until, heartbeat_at, security_context, execution_config, created_at, updated_at, finished_at`

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
	query := fmt.Sprintf("SELECT %s FROM agent_runs WHERE id = ? AND tenant_id = ?;", selectRunCols)
	row := r.db.QueryRowContext(ctx, query, runID, tenantID)
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

	if tenantID != "" {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *SqliteAgentRunRepository) GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	query := `
	SELECT e.id, e.tenant_id, e.run_id, e.step, e.kind, e.message, e.timestamp
	FROM agent_run_events e
	JOIN agent_runs r ON r.id = e.run_id
	WHERE e.run_id = ? AND r.tenant_id = ? AND e.id > ?
	ORDER BY e.id ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, runID, tenantID, afterID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *SqliteAgentRunRepository) Cancel(ctx context.Context, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var currentStatus string
	err := r.db.QueryRowContext(ctx, "SELECT status FROM agent_runs WHERE id = ?", runID).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
		}
		return err
	}

	st := domain.AgentRunStatus(currentStatus)
	if st == domain.RunStatusCompleted || st == domain.RunStatusFailed || st == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", ErrInvalidStatusTransition, currentStatus)
	}

	now := time.Now()
	query := `
	UPDATE agent_runs
	SET status = ?, stop_reason = ?, updated_at = ?, finished_at = ?
	WHERE id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
	`
	_, err = r.db.ExecContext(ctx, query, string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID)
	return err
}

func (r *SqliteAgentRunRepository) CancelForTenant(ctx context.Context, tenantID, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var currentTenant, currentStatus string
	err := r.db.QueryRowContext(ctx, "SELECT tenant_id, status FROM agent_runs WHERE id = ?", runID).Scan(&currentTenant, &currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
		}
		return err
	}

	if currentTenant != tenantID {
		return fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
	}

	st := domain.AgentRunStatus(currentStatus)
	if st == domain.RunStatusCompleted || st == domain.RunStatusFailed || st == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", ErrInvalidStatusTransition, currentStatus)
	}

	now := time.Now()
	query := `
	UPDATE agent_runs
	SET status = ?, stop_reason = ?, updated_at = ?, finished_at = ?
	WHERE id = ? AND tenant_id = ? AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
	`
	_, err = r.db.ExecContext(ctx, query, string(domain.RunStatusCancelled), domain.StopReasonCancelled, now, now, runID, tenantID)
	return err
}

func (r *SqliteAgentRunRepository) ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	leaseUntil := now.Add(leaseDuration)

	query := `
	UPDATE agent_runs
	SET status = 'running', worker_id = ?, claim_generation = claim_generation + 1, lease_until = ?, heartbeat_at = ?, updated_at = ?
	WHERE id = ? AND (
		status = 'queued' 
		OR (status IN ('running', 'recovering', 'waiting_for_tool') AND (lease_until IS NULL OR lease_until <= ?))
	);
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

func (r *SqliteAgentRunRepository) RenewLease(ctx context.Context, runID, workerID string, claimGeneration int64, leaseDuration time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	leaseUntil := now.Add(leaseDuration)

	query := `
	UPDATE agent_runs
	SET lease_until = ?, heartbeat_at = ?, updated_at = ?
	WHERE id = ? AND worker_id = ? AND claim_generation = ? AND status IN ('running', 'recovering');
	`
	res, err := r.db.ExecContext(ctx, query, leaseUntil, now, now, runID, workerID, claimGeneration)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *SqliteAgentRunRepository) UpdateOwned(ctx context.Context, run *domain.AgentRun, workerID string, claimGeneration int64) (bool, error) {
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
	WHERE id = ? AND worker_id = ? AND claim_generation = ? AND status != 'cancelled';
	`
	args := []any{
		string(run.Status), run.CurrentStep, run.TotalToolCalls, run.StopReason,
		run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
		run.ID, workerID, claimGeneration,
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

func (r *SqliteAgentRunRepository) AppendOwnedEvent(ctx context.Context, event *domain.AgentRunEvent, workerID string, claimGeneration int64) (bool, error) {
	if event == nil || event.RunID == "" {
		return false, errors.New("event không hợp lệ hoặc thiếu RunID")
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
	SELECT ?, ?, ?, ?, ?, ?
	WHERE EXISTS (
		SELECT 1
		FROM agent_runs
		WHERE id = ?
		  AND worker_id = ?
		  AND claim_generation = ?
	);
	`
	res, err := r.db.ExecContext(ctx, query, event.TenantID, event.RunID, event.Step, event.Kind, event.Message, event.Timestamp, event.RunID, workerID, claimGeneration)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	id, err := res.LastInsertId()
	if err == nil {
		event.ID = id
	}
	return true, nil
}

// ToolExecutionLedger implementation
func (r *SqliteAgentRunRepository) RecordPlannedOrRunning(ctx context.Context, exec *domain.ToolExecutionRecord) error {
	if exec == nil || exec.RunID == "" || exec.ToolCallID == "" {
		return errors.New("record không hợp lệ hoặc thiếu RunID/ToolCallID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if exec.TenantID == "" {
		exec.TenantID = "default"
	}
	now := time.Now()
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}

	query := `
	INSERT INTO agent_tool_executions (tenant_id, run_id, tool_call_id, tool_name, args_hash, status, result_json, error, worker_id, claim_generation, started_at, finished_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, run_id, tool_call_id) DO UPDATE SET
		status = excluded.status,
		worker_id = excluded.worker_id,
		claim_generation = excluded.claim_generation,
		started_at = excluded.started_at;
	`
	_, err := r.db.ExecContext(ctx, query,
		exec.TenantID, exec.RunID, exec.ToolCallID, exec.ToolName, exec.ArgsHash,
		string(exec.Status), exec.ResultJSON, exec.Error, exec.WorkerID, exec.ClaimGeneration,
		exec.StartedAt, exec.FinishedAt,
	)
	return err
}

func (r *SqliteAgentRunRepository) RecordFinished(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tenantID == "" {
		tenantID = "default"
	}
	now := time.Now()
	query := `
	UPDATE agent_tool_executions
	SET status = ?, result_json = ?, error = ?, finished_at = ?
	WHERE tenant_id = ? AND run_id = ? AND tool_call_id = ?;
	`
	_, err := r.db.ExecContext(ctx, query, string(status), resultJSON, errStr, now, tenantID, runID, toolCallID)
	return err
}

func (r *SqliteAgentRunRepository) GetExecution(ctx context.Context, tenantID, runID, toolCallID string) (*domain.ToolExecutionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tenantID == "" {
		tenantID = "default"
	}
	query := `
	SELECT tenant_id, run_id, tool_call_id, tool_name, args_hash, status, result_json, error, worker_id, claim_generation, started_at, finished_at
	FROM agent_tool_executions
	WHERE tenant_id = ? AND run_id = ? AND tool_call_id = ?;
	`
	row := r.db.QueryRowContext(ctx, query, tenantID, runID, toolCallID)

	var rec domain.ToolExecutionRecord
	var (
		argsHash, resultJSON, errStr, workerID sql.NullString
		claimGen                               sql.NullInt64
		startedAt, finishedAt                  sql.NullTime
		statusStr                              string
	)

	err := row.Scan(
		&rec.TenantID, &rec.RunID, &rec.ToolCallID, &rec.ToolName,
		&argsHash, &statusStr, &resultJSON, &errStr,
		&workerID, &claimGen, &startedAt, &finishedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	rec.Status = domain.ToolExecutionStatus(statusStr)
	if argsHash.Valid {
		rec.ArgsHash = argsHash.String
	}
	if resultJSON.Valid {
		rec.ResultJSON = resultJSON.String
	}
	if errStr.Valid {
		rec.Error = errStr.String
	}
	if workerID.Valid {
		rec.WorkerID = workerID.String
	}
	if claimGen.Valid {
		rec.ClaimGeneration = claimGen.Int64
	}
	if startedAt.Valid {
		rec.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		rec.FinishedAt = &finishedAt.Time
	}

	return &rec, nil
}

func (r *SqliteAgentRunRepository) MarkUnknownAfterRestart(ctx context.Context, tenantID, runID, toolCallID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tenantID == "" {
		tenantID = "default"
	}
	query := `
	UPDATE agent_tool_executions
	SET status = 'unknown_after_restart'
	WHERE tenant_id = ? AND run_id = ? AND tool_call_id = ?;
	`
	_, err := r.db.ExecContext(ctx, query, tenantID, runID, toolCallID)
	return err
}

var _ ports.AgentRunRepository = (*SqliteAgentRunRepository)(nil)
var _ ports.ToolExecutionLedger = (*SqliteAgentRunRepository)(nil)
