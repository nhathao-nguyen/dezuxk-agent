package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// PostgresAgentRunRepository triển khai ports.AgentRunRepository và ports.ToolExecutionLedger trên PostgreSQL
type PostgresAgentRunRepository struct {
	pool *pgxpool.Pool
}

var _ ports.AgentRunRepository = (*PostgresAgentRunRepository)(nil)
var _ ports.ToolExecutionLedger = (*PostgresAgentRunRepository)(nil)

// NewPostgresAgentRunRepository khởi tạo PostgreSQL Agent Run Repository
func NewPostgresAgentRunRepository(pool *pgxpool.Pool) (*PostgresAgentRunRepository, error) {
	if pool == nil {
		return nil, errors.New("pgxpool không được để nil")
	}
	return &PostgresAgentRunRepository{pool: pool}, nil
}

const selectRunCols = `
	id, tenant_id, COALESCE(idempotency_key, ''), goal, status, model, workspace,
	current_step, max_steps, total_tool_calls, COALESCE(stop_reason, ''),
	COALESCE(final_answer, ''), COALESCE(error, ''), COALESCE(git_diff, ''),
	COALESCE(parent_run_id, ''), COALESCE(resume_from_run_id, ''),
	COALESCE(worker_id, ''), claim_generation, COALESCE(billing_key_id, ''),
	lease_until, heartbeat_at, COALESCE(security_context, ''), COALESCE(execution_config, ''),
	created_at, updated_at, finished_at
`

func (r *PostgresAgentRunRepository) scanAgentRun(row pgRowScanner) (*domain.AgentRun, error) {
	var (
		run               domain.AgentRun
		statusStr         string
		stopReasonStr     string
		leaseUntil        *time.Time
		heartbeatAt       *time.Time
		finishedAt        *time.Time
		secCtxJSON        string
		execCfgJSON       string
		idempotencyKeyStr string
	)

	err := row.Scan(
		&run.ID, &run.TenantID, &idempotencyKeyStr, &run.Goal, &statusStr, &run.Model, &run.Workspace,
		&run.CurrentStep, &run.MaxSteps, &run.TotalToolCalls, &stopReasonStr,
		&run.FinalAnswer, &run.Error, &run.GitDiff,
		&run.ParentRunID, &run.ResumeFromRunID,
		&run.WorkerID, &run.ClaimGeneration, &run.BillingKeyID,
		&leaseUntil, &heartbeatAt, &secCtxJSON, &execCfgJSON,
		&run.CreatedAt, &run.UpdatedAt, &finishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, session.ErrRunNotFound
		}
		return nil, err
	}

	run.Status = domain.AgentRunStatus(statusStr)
	run.StopReason = stopReasonStr
	run.IdempotencyKey = idempotencyKeyStr
	run.LeaseUntil = leaseUntil
	run.HeartbeatAt = heartbeatAt
	run.FinishedAt = finishedAt

	if secCtxJSON != "" {
		var sc domain.AgentSecurityContext
		if err := json.Unmarshal([]byte(secCtxJSON), &sc); err == nil {
			run.SecurityContext = &sc
		}
	}
	if execCfgJSON != "" {
		var ec domain.AgentExecutionConfig
		if err := json.Unmarshal([]byte(execCfgJSON), &ec); err == nil {
			run.ExecutionConfig = &ec
		}
	}

	return &run, nil
}

func (r *PostgresAgentRunRepository) Create(ctx context.Context, run *domain.AgentRun) error {
	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ hoặc thiếu ID")
	}

	var secCtxJSON any = nil
	if run.SecurityContext != nil {
		b, err := json.Marshal(run.SecurityContext)
		if err == nil {
			secCtxJSON = string(b)
		}
	}

	var execCfgJSON any = nil
	if run.ExecutionConfig != nil {
		b, err := json.Marshal(run.ExecutionConfig)
		if err == nil {
			execCfgJSON = string(b)
		}
	}

	cleanKey := strings.TrimSpace(run.IdempotencyKey)
	var idempKeyVal any = nil
	if cleanKey != "" {
		idempKeyVal = cleanKey
	}

	tenantID := run.TenantID
	if tenantID == "" {
		tenantID = "default"
	}

	now := time.Now()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = now
	}

	query := `
	INSERT INTO agent_runs (
		id, tenant_id, idempotency_key, goal, status, model, workspace,
		current_step, max_steps, total_tool_calls, stop_reason,
		final_answer, error, git_diff, parent_run_id, resume_from_run_id,
		worker_id, claim_generation, billing_key_id, lease_until, heartbeat_at,
		security_context, execution_config, created_at, updated_at, finished_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11,
		$12, $13, $14, $15, $16,
		$17, $18, $19, $20, $21,
		$22, $23, $24, $25, $26
	);
	`

	_, err := r.pool.Exec(ctx, query,
		run.ID, tenantID, idempKeyVal, run.Goal, string(run.Status), run.Model, run.Workspace,
		run.CurrentStep, run.MaxSteps, run.TotalToolCalls, string(run.StopReason),
		run.FinalAnswer, run.Error, run.GitDiff, run.ParentRunID, run.ResumeFromRunID,
		run.WorkerID, run.ClaimGeneration, run.BillingKeyID, run.LeaseUntil, run.HeartbeatAt,
		secCtxJSON, execCfgJSON, run.CreatedAt, run.UpdatedAt, run.FinishedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return session.ErrIdempotencyConflict
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return session.ErrIdempotencyConflict
		}
		return err
	}
	return nil
}

func (r *PostgresAgentRunRepository) Update(ctx context.Context, run *domain.AgentRun) error {
	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ hoặc thiếu ID")
	}

	run.UpdatedAt = time.Now()

	query := `
	UPDATE agent_runs SET
		goal = $1, status = $2, model = $3, workspace = $4,
		current_step = $5, max_steps = $6, total_tool_calls = $7, stop_reason = $8,
		final_answer = $9, error = $10, git_diff = $11, worker_id = $12,
		claim_generation = $13, billing_key_id = $14, lease_until = $15, heartbeat_at = $16,
		updated_at = $17, finished_at = $18
	WHERE id = $19;
	`
	res, err := r.pool.Exec(ctx, query,
		run.Goal, string(run.Status), run.Model, run.Workspace,
		run.CurrentStep, run.MaxSteps, run.TotalToolCalls, string(run.StopReason),
		run.FinalAnswer, run.Error, run.GitDiff, run.WorkerID,
		run.ClaimGeneration, run.BillingKeyID, run.LeaseUntil, run.HeartbeatAt,
		run.UpdatedAt, run.FinishedAt, run.ID,
	)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return session.ErrRunNotFound
	}
	return nil
}

func (r *PostgresAgentRunRepository) UpdateWithTransition(ctx context.Context, run *domain.AgentRun, allowedFromStatuses ...domain.AgentRunStatus) (bool, error) {
	if run == nil || run.ID == "" {
		return false, errors.New("run không hợp lệ hoặc thiếu ID")
	}

	run.UpdatedAt = time.Now()

	var allowed []string
	for _, s := range allowedFromStatuses {
		allowed = append(allowed, string(s))
	}

	query := `
	UPDATE agent_runs SET
		status = $1, current_step = $2, total_tool_calls = $3, stop_reason = $4,
		final_answer = $5, error = $6, git_diff = $7, updated_at = $8, finished_at = $9
	WHERE id = $10 AND status = ANY($11::text[]);
	`
	res, err := r.pool.Exec(ctx, query,
		string(run.Status), run.CurrentStep, run.TotalToolCalls, string(run.StopReason),
		run.FinalAnswer, run.Error, run.GitDiff, run.UpdatedAt, run.FinishedAt,
		run.ID, allowed,
	)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

func (r *PostgresAgentRunRepository) Get(ctx context.Context, runID string) (*domain.AgentRun, error) {
	query := `SELECT ` + selectRunCols + ` FROM agent_runs WHERE id = $1 LIMIT 1`
	row := r.pool.QueryRow(ctx, query, runID)
	return r.scanAgentRun(row)
}

func (r *PostgresAgentRunRepository) GetForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error) {
	query := `SELECT ` + selectRunCols + ` FROM agent_runs WHERE id = $1 AND tenant_id = $2 LIMIT 1`
	row := r.pool.QueryRow(ctx, query, runID, tenantID)
	return r.scanAgentRun(row)
}

func (r *PostgresAgentRunRepository) List(ctx context.Context, tenantID string, limit, offset int) ([]*domain.AgentRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var query string
	var args []any
	if tenantID == "" || tenantID == "admin" {
		query = `SELECT ` + selectRunCols + ` FROM agent_runs ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = []any{limit, offset}
	} else {
		query = `SELECT ` + selectRunCols + ` FROM agent_runs WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []any{tenantID, limit, offset}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.AgentRun
	for rows.Next() {
		run, scanErr := r.scanAgentRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *PostgresAgentRunRepository) ListPendingRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	query := `SELECT ` + selectRunCols + ` FROM agent_runs WHERE status IN ('queued', 'running', 'recovering', 'waiting_for_tool') ORDER BY created_at ASC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.AgentRun
	for rows.Next() {
		run, scanErr := r.scanAgentRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *PostgresAgentRunRepository) AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error {
	if event == nil || event.RunID == "" {
		return errors.New("event không hợp lệ hoặc thiếu RunID")
	}

	if event.TenantID == "" {
		var tid string
		_ = r.pool.QueryRow(ctx, "SELECT tenant_id FROM agent_runs WHERE id = $1", event.RunID).Scan(&tid)
		if tid != "" {
			event.TenantID = tid
		} else {
			event.TenantID = "default"
		}
	}

	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	query := `
	INSERT INTO agent_run_events (tenant_id, run_id, step, kind, message, timestamp)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id;
	`
	return r.pool.QueryRow(ctx, query, event.TenantID, event.RunID, event.Step, event.Kind, event.Message, ts).Scan(&event.ID)
}

func (r *PostgresAgentRunRepository) GetEvents(ctx context.Context, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	query := `
	SELECT id, tenant_id, run_id, step, kind, message, timestamp
	FROM agent_run_events
	WHERE run_id = $1 AND id > $2
	ORDER BY id ASC;
	`
	rows, err := r.pool.Query(ctx, query, runID, afterID)
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
	return events, rows.Err()
}

func (r *PostgresAgentRunRepository) GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	query := `
	SELECT e.id, e.tenant_id, e.run_id, e.step, e.kind, e.message, e.timestamp
	FROM agent_run_events e
	JOIN agent_runs r ON r.id = e.run_id
	WHERE e.run_id = $1 AND r.tenant_id = $2 AND e.id > $3
	ORDER BY e.id ASC;
	`
	rows, err := r.pool.Query(ctx, query, runID, tenantID, afterID)
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
	return events, rows.Err()
}

func (r *PostgresAgentRunRepository) Cancel(ctx context.Context, runID string) error {
	var currentStatus string
	err := r.pool.QueryRow(ctx, "SELECT status FROM agent_runs WHERE id = $1", runID).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", session.ErrRunNotFound, runID)
		}
		return err
	}

	st := domain.AgentRunStatus(currentStatus)
	if st == domain.RunStatusCompleted || st == domain.RunStatusFailed || st == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", session.ErrInvalidStatusTransition, currentStatus)
	}

	query := `
	UPDATE agent_runs
	SET status = 'cancelled', stop_reason = 'cancelled', updated_at = NOW(), finished_at = NOW()
	WHERE id = $1 AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
	`
	_, err = r.pool.Exec(ctx, query, runID)
	return err
}

func (r *PostgresAgentRunRepository) CancelForTenant(ctx context.Context, tenantID, runID string) error {
	var currentTenant, currentStatus string
	err := r.pool.QueryRow(ctx, "SELECT tenant_id, status FROM agent_runs WHERE id = $1", runID).Scan(&currentTenant, &currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s cho tenant %s", session.ErrRunNotFound, runID, tenantID)
		}
		return err
	}

	if currentTenant != tenantID {
		return fmt.Errorf("%w: %s cho tenant %s", session.ErrRunNotFound, runID, tenantID)
	}

	st := domain.AgentRunStatus(currentStatus)
	if st == domain.RunStatusCompleted || st == domain.RunStatusFailed || st == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", session.ErrInvalidStatusTransition, currentStatus)
	}

	query := `
	UPDATE agent_runs
	SET status = 'cancelled', stop_reason = 'cancelled', updated_at = NOW(), finished_at = NOW()
	WHERE id = $1 AND tenant_id = $2 AND status IN ('queued', 'running', 'waiting_for_tool', 'waiting_for_approval', 'recovering');
	`
	_, err = r.pool.Exec(ctx, query, runID, tenantID)
	return err
}

// ClaimRun sử dụng PostgreSQL atomic statement kết hợp database time NOW()
// ngăn ngừa clock skew giữa các node Gateway và đảm bảo single worker ownership duy nhất.
func (r *PostgresAgentRunRepository) ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error) {
	leaseSeconds := int64(leaseDuration.Seconds())
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}

	query := `
	UPDATE agent_runs
	SET status = 'running',
	    worker_id = $2,
	    claim_generation = claim_generation + 1,
	    lease_until = NOW() + make_interval(secs => $3::float8),
	    heartbeat_at = NOW(),
	    updated_at = NOW()
	WHERE id = $1 AND (
		status = 'queued' 
		OR (status IN ('running', 'recovering', 'waiting_for_tool') AND (lease_until IS NULL OR lease_until <= NOW()))
	)
	RETURNING claim_generation;
	`
	var newGen int64
	err := r.pool.QueryRow(ctx, query, runID, workerID, float64(leaseSeconds)).Scan(&newGen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return newGen > 0, nil
}

func (r *PostgresAgentRunRepository) RenewLease(ctx context.Context, runID, workerID string, claimGeneration int64, leaseDuration time.Duration) (bool, error) {
	leaseSeconds := int64(leaseDuration.Seconds())
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}

	query := `
	UPDATE agent_runs
	SET lease_until = NOW() + make_interval(secs => $4::float8),
	    heartbeat_at = NOW(),
	    updated_at = NOW()
	WHERE id = $1 AND worker_id = $2 AND claim_generation = $3 AND status IN ('running', 'recovering');
	`
	res, err := r.pool.Exec(ctx, query, runID, workerID, claimGeneration, float64(leaseSeconds))
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

// UpdateOwned áp dụng fencing token: chỉ worker sở hữu run hiện tại (đúng worker_id và claim_generation)
// mới được phép cập nhật trạng thái. Nếu rows affected = 0 => worker đã mất quyền sở hữu (zombie worker).
func (r *PostgresAgentRunRepository) UpdateOwned(ctx context.Context, run *domain.AgentRun, workerID string, claimGeneration int64) (bool, error) {
	if run == nil || run.ID == "" {
		return false, errors.New("run không hợp lệ hoặc thiếu ID")
	}

	run.UpdatedAt = time.Now()

	query := `
	UPDATE agent_runs SET
		status = $1, current_step = $2, total_tool_calls = $3, stop_reason = $4,
		final_answer = $5, error = $6, git_diff = $7, updated_at = NOW(), finished_at = $8
	WHERE id = $9 AND worker_id = $10 AND claim_generation = $11 AND status != 'cancelled';
	`
	res, err := r.pool.Exec(ctx, query,
		string(run.Status), run.CurrentStep, run.TotalToolCalls, string(run.StopReason),
		run.FinalAnswer, run.Error, run.GitDiff, run.FinishedAt,
		run.ID, workerID, claimGeneration,
	)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

func (r *PostgresAgentRunRepository) FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error) {
	cleanKey := strings.TrimSpace(key)
	if cleanKey == "" {
		return nil, nil
	}
	if tenantID == "" {
		tenantID = "default"
	}

	query := `SELECT ` + selectRunCols + ` FROM agent_runs WHERE tenant_id = $1 AND idempotency_key = $2 ORDER BY created_at DESC LIMIT 1;`
	row := r.pool.QueryRow(ctx, query, tenantID, cleanKey)
	run, err := r.scanAgentRun(row)
	if err != nil {
		if errors.Is(err, session.ErrRunNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return run, nil
}

// AppendOwnedEvent kiểm tra quyền sở hữu fencing token trước khi ghi event để ngăn zombie worker spam
func (r *PostgresAgentRunRepository) AppendOwnedEvent(ctx context.Context, event *domain.AgentRunEvent, workerID string, claimGeneration int64) (bool, error) {
	if event == nil || event.RunID == "" {
		return false, errors.New("event không hợp lệ hoặc thiếu RunID")
	}

	if event.TenantID == "" {
		var tid string
		_ = r.pool.QueryRow(ctx, "SELECT tenant_id FROM agent_runs WHERE id = $1", event.RunID).Scan(&tid)
		if tid != "" {
			event.TenantID = tid
		} else {
			event.TenantID = "default"
		}
	}

	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	query := `
	INSERT INTO agent_run_events (tenant_id, run_id, step, kind, message, timestamp)
	SELECT $1, $2, $3, $4, $5, $6
	WHERE EXISTS (
		SELECT 1
		FROM agent_runs
		WHERE id = $7
		  AND worker_id = $8
		  AND claim_generation = $9
	)
	RETURNING id;
	`
	var id int64
	err := r.pool.QueryRow(ctx, query,
		event.TenantID, event.RunID, event.Step, event.Kind, event.Message, ts,
		event.RunID, workerID, claimGeneration,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	event.ID = id
	return true, nil
}

// ValidateOwnership kiểm tra tức thời xem worker_id và claim_generation còn nắm giữ lease hợp lệ hay không
func (r *PostgresAgentRunRepository) ValidateOwnership(ctx context.Context, runID, workerID string, claimGeneration int64) (bool, error) {
	if runID == "" || workerID == "" || claimGeneration <= 0 {
		return false, nil
	}
	query := `
	SELECT 1 FROM agent_runs
	WHERE id = $1 AND worker_id = $2 AND claim_generation = $3
	  AND status IN ('running', 'recovering', 'waiting_for_tool')
	  AND (lease_until IS NULL OR lease_until > NOW())
	LIMIT 1;
	`
	var dummy int
	err := r.pool.QueryRow(ctx, query, runID, workerID, claimGeneration).Scan(&dummy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ToolExecutionLedger implementation
func (r *PostgresAgentRunRepository) RecordPlannedOrRunning(ctx context.Context, exec *domain.ToolExecutionRecord) error {
	if exec == nil || exec.RunID == "" || exec.ToolCallID == "" {
		return errors.New("record không hợp lệ hoặc thiếu RunID/ToolCallID")
	}

	if exec.TenantID == "" {
		exec.TenantID = "default"
	}
	now := time.Now()
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}

	query := `
	INSERT INTO agent_tool_executions (
		tenant_id, run_id, tool_call_id, tool_name, args_hash, status, result_json, error,
		worker_id, claim_generation, started_at, finished_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8,
		$9, $10, $11, $12
	)
	ON CONFLICT (tenant_id, run_id, tool_call_id) DO UPDATE SET
		status = EXCLUDED.status,
		worker_id = EXCLUDED.worker_id,
		claim_generation = EXCLUDED.claim_generation,
		started_at = EXCLUDED.started_at;
	`
	_, err := r.pool.Exec(ctx, query,
		exec.TenantID, exec.RunID, exec.ToolCallID, exec.ToolName, exec.ArgsHash,
		string(exec.Status), exec.ResultJSON, exec.Error, exec.WorkerID, exec.ClaimGeneration,
		exec.StartedAt, exec.FinishedAt,
	)
	return err
}

// RecordPlannedOrRunningOwned áp dụng Fencing Token: từ chối ghi nhận nếu worker mất quyền sở hữu hoặc lease hết hạn
func (r *PostgresAgentRunRepository) RecordPlannedOrRunningOwned(ctx context.Context, exec *domain.ToolExecutionRecord, workerID string, claimGeneration int64) (bool, error) {
	if exec == nil || exec.RunID == "" || exec.ToolCallID == "" {
		return false, errors.New("record không hợp lệ hoặc thiếu RunID/ToolCallID")
	}

	if exec.TenantID == "" {
		exec.TenantID = "default"
	}
	now := time.Now()
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}
	exec.WorkerID = workerID
	exec.ClaimGeneration = claimGeneration

	// Đảm bảo nguyên tử: chỉ INSERT/UPDATE nếu agent_runs vẫn thuộc về worker và lease còn hiệu lực
	query := `
	INSERT INTO agent_tool_executions (
		tenant_id, run_id, tool_call_id, tool_name, args_hash, status, result_json, error,
		worker_id, claim_generation, started_at, finished_at
	)
	SELECT $1::text, $2::text, $3::text, $4::text, $5::text, $6::text, $7::text, $8::text, $9::text, $10::bigint, $11::timestamptz, $12::timestamptz
	WHERE EXISTS (
		SELECT 1 FROM agent_runs
		WHERE id = $2::text AND worker_id = $9::text AND claim_generation = $10::bigint
		  AND status IN ('running', 'recovering', 'waiting_for_tool')
		  AND (lease_until IS NULL OR lease_until > NOW())
	)
	ON CONFLICT (tenant_id, run_id, tool_call_id) DO UPDATE SET
		status = EXCLUDED.status,
		worker_id = EXCLUDED.worker_id,
		claim_generation = EXCLUDED.claim_generation,
		started_at = EXCLUDED.started_at
	WHERE EXISTS (
		SELECT 1 FROM agent_runs
		WHERE id = EXCLUDED.run_id AND worker_id = EXCLUDED.worker_id AND claim_generation = EXCLUDED.claim_generation
		  AND status IN ('running', 'recovering', 'waiting_for_tool')
		  AND (lease_until IS NULL OR lease_until > NOW())
	);
	`
	res, err := r.pool.Exec(ctx, query,
		exec.TenantID, exec.RunID, exec.ToolCallID, exec.ToolName, exec.ArgsHash,
		string(exec.Status), exec.ResultJSON, exec.Error, workerID, claimGeneration,
		exec.StartedAt, exec.FinishedAt,
	)
	if err != nil {
		return false, err
	}
	if res.RowsAffected() == 0 {
		return false, nil // Ownership lost or lease expired!
	}
	return true, nil
}

func (r *PostgresAgentRunRepository) RecordFinished(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string) error {
	if tenantID == "" {
		tenantID = "default"
	}

	query := `
	UPDATE agent_tool_executions SET
		status = $1, result_json = $2, error = $3, finished_at = NOW()
	WHERE tenant_id = $4 AND run_id = $5 AND tool_call_id = $6;
	`
	res, err := r.pool.Exec(ctx, query, string(status), resultJSON, errStr, tenantID, runID, toolCallID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("không tìm thấy bản ghi thực thi công cụ %s để cập nhật kết quả", toolCallID)
	}
	return nil
}

// RecordFinishedOwned cập nhật kết quả công cụ với điều kiện fencing token hợp lệ
func (r *PostgresAgentRunRepository) RecordFinishedOwned(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string, workerID string, claimGeneration int64) (bool, error) {
	if tenantID == "" {
		tenantID = "default"
	}

	query := `
	UPDATE agent_tool_executions SET
		status = $1::text, result_json = $2::text, error = $3::text, finished_at = NOW()
	WHERE tenant_id = $4::text AND run_id = $5::text AND tool_call_id = $6::text
	  AND (worker_id = $7::text OR worker_id = '')
	  AND (claim_generation = $8::bigint OR claim_generation = 0)
	  AND EXISTS (
		SELECT 1 FROM agent_runs
		WHERE id = $5::text AND worker_id = $7::text AND claim_generation = $8::bigint
		  AND status != 'cancelled'
	  );
	`
	res, err := r.pool.Exec(ctx, query, string(status), resultJSON, errStr, tenantID, runID, toolCallID, workerID, claimGeneration)
	if err != nil {
		return false, err
	}
	if res.RowsAffected() == 0 {
		return false, nil
	}
	return true, nil
}

func (r *PostgresAgentRunRepository) GetExecution(ctx context.Context, tenantID, runID, toolCallID string) (*domain.ToolExecutionRecord, error) {
	if tenantID == "" {
		tenantID = "default"
	}

	query := `
	SELECT tenant_id, run_id, tool_call_id, tool_name, COALESCE(args_hash, ''),
	       status, COALESCE(result_json, ''), COALESCE(error, ''), COALESCE(worker_id, ''),
	       claim_generation, started_at, finished_at
	FROM agent_tool_executions
	WHERE tenant_id = $1 AND run_id = $2 AND tool_call_id = $3
	LIMIT 1;
	`
	row := r.pool.QueryRow(ctx, query, tenantID, runID, toolCallID)

	var (
		rec       domain.ToolExecutionRecord
		statusStr string
	)
	err := row.Scan(
		&rec.TenantID, &rec.RunID, &rec.ToolCallID, &rec.ToolName, &rec.ArgsHash,
		&statusStr, &rec.ResultJSON, &rec.Error, &rec.WorkerID,
		&rec.ClaimGeneration, &rec.StartedAt, &rec.FinishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rec.Status = domain.ToolExecutionStatus(statusStr)
	return &rec, nil
}

func (r *PostgresAgentRunRepository) MarkUnknownAfterRestart(ctx context.Context, tenantID, runID, toolCallID string) error {
	if tenantID == "" {
		tenantID = "default"
	}

	query := `
	UPDATE agent_tool_executions SET
		status = 'unknown_after_restart', error = 'Tác vụ bị gián đoạn do máy chủ tắt trong khi công cụ đang chạy'
	WHERE tenant_id = $1 AND run_id = $2 AND tool_call_id = $3 AND status = 'running';
	`
	_, err := r.pool.Exec(ctx, query, tenantID, runID, toolCallID)
	return err
}
