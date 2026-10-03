package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// PostgresCheckpointRepository lưu trữ các snapshot trạng thái Agent trong PostgreSQL
type PostgresCheckpointRepository struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
}

var _ ports.CheckpointRepository = (*PostgresCheckpointRepository)(nil)

// NewPostgresCheckpointRepository khởi tạo repository lưu trữ checkpoint
func NewPostgresCheckpointRepository(pool *pgxpool.Pool) (*PostgresCheckpointRepository, error) {
	if pool == nil {
		return nil, errors.New("pgxpool không được là nil")
	}
	return &PostgresCheckpointRepository{pool: pool}, nil
}

func (r *PostgresCheckpointRepository) SaveCheckpoint(ctx context.Context, cp *domain.AgentCheckpoint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cp.TenantID == "" {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			cp.TenantID = id.TenantID
		} else {
			cp.TenantID = "default"
		}
	}

	stateJSON, err := json.Marshal(cp.StateSnapshot)
	if err != nil {
		return fmt.Errorf("không thể mã hóa state_snapshot: %w", err)
	}

	planJSON, err := json.Marshal(cp.PlanSnapshot)
	if err != nil {
		return fmt.Errorf("không thể mã hóa plan_snapshot: %w", err)
	}

	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}

	query := `
	INSERT INTO agent_checkpoints (tenant_id, task_id, node_kind, step_index, state_snapshot_json, plan_snapshot, created_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING id;
	`
	err = r.pool.QueryRow(ctx, query, cp.TenantID, cp.TaskID, string(cp.NodeKind), cp.StepIndex, string(stateJSON), string(planJSON), cp.CreatedAt).Scan(&cp.ID)
	if err != nil {
		return fmt.Errorf("lỗi khi lưu checkpoint vào PostgreSQL: %w", err)
	}

	return nil
}

func (r *PostgresCheckpointRepository) GetLatestCheckpoint(ctx context.Context, taskID string) (*domain.AgentCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	var args []any

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if !hasID || identity.Role != "admin" {
		tenantID := "default"
		if hasID && identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot_json, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = $1 AND tenant_id = $2
		ORDER BY step_index DESC, created_at DESC, id DESC
		LIMIT 1;
		`
		args = []any{taskID, tenantID}
	} else {
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot_json, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = $1
		ORDER BY step_index DESC, created_at DESC, id DESC
		LIMIT 1;
		`
		args = []any{taskID}
	}

	row := r.pool.QueryRow(ctx, query, args...)

	var cp domain.AgentCheckpoint
	var nodeKindStr string
	var stateJSON, planJSON string

	err := row.Scan(&cp.ID, &cp.TenantID, &cp.TaskID, &nodeKindStr, &cp.StepIndex, &stateJSON, &planJSON, &cp.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("không tìm thấy checkpoint nào cho task %s", taskID)
		}
		return nil, fmt.Errorf("lỗi khi đọc checkpoint từ PostgreSQL: %w", err)
	}

	cp.NodeKind = domain.GraphNodeKind(nodeKindStr)
	if err := json.Unmarshal([]byte(stateJSON), &cp.StateSnapshot); err != nil {
		return nil, fmt.Errorf("lỗi giải mã state_snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(planJSON), &cp.PlanSnapshot); err != nil {
		return nil, fmt.Errorf("lỗi giải mã plan_snapshot: %w", err)
	}

	return &cp, nil
}

func (r *PostgresCheckpointRepository) ListCheckpoints(ctx context.Context, taskID string) ([]*domain.AgentCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	var args []any

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if !hasID || identity.Role != "admin" {
		tenantID := "default"
		if hasID && identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot_json, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = $1 AND tenant_id = $2
		ORDER BY step_index ASC, created_at ASC;
		`
		args = []any{taskID, tenantID}
	} else {
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot_json, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = $1
		ORDER BY step_index ASC, created_at ASC;
		`
		args = []any{taskID}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lỗi truy vấn danh sách checkpoints: %w", err)
	}
	defer rows.Close()

	var list []*domain.AgentCheckpoint
	for rows.Next() {
		var cp domain.AgentCheckpoint
		var nodeKindStr string
		var stateJSON, planJSON string

		if err := rows.Scan(&cp.ID, &cp.TenantID, &cp.TaskID, &nodeKindStr, &cp.StepIndex, &stateJSON, &planJSON, &cp.CreatedAt); err != nil {
			return nil, fmt.Errorf("lỗi scan checkpoint từ PostgreSQL: %w", err)
		}

		cp.NodeKind = domain.GraphNodeKind(nodeKindStr)
		_ = json.Unmarshal([]byte(stateJSON), &cp.StateSnapshot)
		_ = json.Unmarshal([]byte(planJSON), &cp.PlanSnapshot)

		list = append(list, &cp)
	}

	return list, rows.Err()
}

func (r *PostgresCheckpointRepository) DeleteCheckpoints(ctx context.Context, taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	var args []any

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if !hasID || identity.Role != "admin" {
		tenantID := "default"
		if hasID && identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `DELETE FROM agent_checkpoints WHERE task_id = $1 AND tenant_id = $2;`
		args = []any{taskID, tenantID}
	} else {
		query = `DELETE FROM agent_checkpoints WHERE task_id = $1;`
		args = []any{taskID}
	}

	_, err := r.pool.Exec(ctx, query, args...)
	return err
}
