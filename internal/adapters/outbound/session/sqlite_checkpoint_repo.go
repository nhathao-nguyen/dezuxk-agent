package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// SqliteCheckpointRepository lưu trữ các snapshot trạng thái Agent trong SQLite
type SqliteCheckpointRepository struct {
	mu sync.Mutex
	db *sql.DB
}

// NewSqliteCheckpointRepository khởi tạo repository lưu trữ checkpoint
func NewSqliteCheckpointRepository(db *sql.DB) (*SqliteCheckpointRepository, error) {
	if db == nil {
		return nil, errors.New("sql.DB không được là nil")
	}

	repo := &SqliteCheckpointRepository{db: db}
	if err := repo.migrate(); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo bảng agent_checkpoints: %w", err)
	}

	return repo, nil
}

var _ ports.CheckpointRepository = (*SqliteCheckpointRepository)(nil)

func (r *SqliteCheckpointRepository) migrate() error {
	// 1. CREATE TABLE IF NOT EXISTS
	schema := `
	CREATE TABLE IF NOT EXISTS agent_checkpoints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		task_id TEXT NOT NULL,
		node_kind TEXT NOT NULL,
		step_index INTEGER NOT NULL,
		state_snapshot TEXT NOT NULL,
		plan_snapshot TEXT NOT NULL,
		created_at DATETIME NOT NULL
	);`
	if _, err := r.db.Exec(schema); err != nil {
		return fmt.Errorf("lỗi tạo bảng agent_checkpoints: %w", err)
	}

	// 2, 3, 4. Kiểm tra PRAGMA table_info và ALTER TABLE nếu thiếu tenant_id
	if err := addColumnIfNotExists(r.db, "agent_checkpoints", "tenant_id", "TEXT NOT NULL DEFAULT 'default'"); err != nil {
		return err
	}

	// 5. CREATE INDEX IF NOT EXISTS (sau khi chắc chắn cột tenant_id tồn tại)
	if _, err := r.db.Exec("CREATE INDEX IF NOT EXISTS idx_agent_checkpoints_task ON agent_checkpoints(tenant_id, task_id, id DESC);"); err != nil {
		return fmt.Errorf("lỗi tạo index cho agent_checkpoints: %w", err)
	}

	return nil
}

func (r *SqliteCheckpointRepository) SaveCheckpoint(ctx context.Context, cp *domain.AgentCheckpoint) error {
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
	INSERT INTO agent_checkpoints (tenant_id, task_id, node_kind, step_index, state_snapshot, plan_snapshot, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?);
	`
	res, err := r.db.ExecContext(ctx, query, cp.TenantID, cp.TaskID, string(cp.NodeKind), cp.StepIndex, string(stateJSON), string(planJSON), cp.CreatedAt)
	if err != nil {
		return fmt.Errorf("lỗi khi lưu checkpoint vào SQLite: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		cp.ID = id
	}

	return nil
}

func (r *SqliteCheckpointRepository) GetLatestCheckpoint(ctx context.Context, taskID string) (*domain.AgentCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	var args []any

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if hasID && identity.Role != "admin" {
		tenantID := "default"
		if identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = ? AND tenant_id = ?
		ORDER BY id DESC
		LIMIT 1;
		`
		args = []any{taskID, tenantID}
	} else {
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = ?
		ORDER BY id DESC
		LIMIT 1;
		`
		args = []any{taskID}
	}

	row := r.db.QueryRowContext(ctx, query, args...)

	var cp domain.AgentCheckpoint
	var nodeKindStr string
	var stateJSON, planJSON string

	err := row.Scan(&cp.ID, &cp.TenantID, &cp.TaskID, &nodeKindStr, &cp.StepIndex, &stateJSON, &planJSON, &cp.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("không tìm thấy checkpoint nào cho task %s", taskID)
		}
		return nil, fmt.Errorf("lỗi khi đọc checkpoint từ SQLite: %w", err)
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

func (r *SqliteCheckpointRepository) ListCheckpoints(ctx context.Context, taskID string) ([]*domain.AgentCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	var args []any

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if hasID && identity.Role != "admin" {
		tenantID := "default"
		if identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = ? AND tenant_id = ?
		ORDER BY id ASC;
		`
		args = []any{taskID, tenantID}
	} else {
		query = `
		SELECT id, tenant_id, task_id, node_kind, step_index, state_snapshot, plan_snapshot, created_at
		FROM agent_checkpoints
		WHERE task_id = ?
		ORDER BY id ASC;
		`
		args = []any{taskID}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
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
			return nil, err
		}

		cp.NodeKind = domain.GraphNodeKind(nodeKindStr)
		_ = json.Unmarshal([]byte(stateJSON), &cp.StateSnapshot)
		_ = json.Unmarshal([]byte(planJSON), &cp.PlanSnapshot)
		list = append(list, &cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lỗi đọc dòng checkpoints: %w", err)
	}

	return list, nil
}

func (r *SqliteCheckpointRepository) DeleteCheckpoints(ctx context.Context, taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var query string
	identity, hasID := domain.TenantIdentityFromContext(ctx)
	if hasID && identity.Role != "admin" {
		tenantID := "default"
		if identity.TenantID != "" {
			tenantID = identity.TenantID
		}
		query = `DELETE FROM agent_checkpoints WHERE task_id = ? AND tenant_id = ?;`
		_, err := r.db.ExecContext(ctx, query, taskID, tenantID)
		return err
	}

	query = `DELETE FROM agent_checkpoints WHERE task_id = ?;`
	_, err := r.db.ExecContext(ctx, query, taskID)
	return err
}

// MemoryCheckpointRepository cài đặt in-memory cho testing hoặc fallback
type MemoryCheckpointRepository struct {
	mu          sync.RWMutex
	checkpoints map[string][]*domain.AgentCheckpoint
	idSeq       int64
}

func NewMemoryCheckpointRepository() *MemoryCheckpointRepository {
	return &MemoryCheckpointRepository{
		checkpoints: make(map[string][]*domain.AgentCheckpoint),
	}
}

var _ ports.CheckpointRepository = (*MemoryCheckpointRepository)(nil)

func (m *MemoryCheckpointRepository) SaveCheckpoint(ctx context.Context, cp *domain.AgentCheckpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cp.TenantID == "" {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			cp.TenantID = id.TenantID
		} else {
			cp.TenantID = "default"
		}
	}

	m.idSeq++
	cp.ID = m.idSeq
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}

	m.checkpoints[cp.TaskID] = append(m.checkpoints[cp.TaskID], cp)
	return nil
}

func (m *MemoryCheckpointRepository) GetLatestCheckpoint(ctx context.Context, taskID string) (*domain.AgentCheckpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	callerTenant := "default"
	isAdmin := false
	if hasID {
		if identity.Role == "admin" {
			isAdmin = true
		}
		if identity.TenantID != "" {
			callerTenant = identity.TenantID
		}
	}

	list := m.checkpoints[taskID]
	for i := len(list) - 1; i >= 0; i-- {
		cp := list[i]
		if isAdmin || cp.TenantID == callerTenant {
			return cp, nil
		}
	}
	return nil, fmt.Errorf("không tìm thấy checkpoint cho task %s", taskID)
}

func (m *MemoryCheckpointRepository) ListCheckpoints(ctx context.Context, taskID string) ([]*domain.AgentCheckpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	callerTenant := "default"
	isAdmin := false
	if hasID {
		if identity.Role == "admin" {
			isAdmin = true
		}
		if identity.TenantID != "" {
			callerTenant = identity.TenantID
		}
	}

	var res []*domain.AgentCheckpoint
	for _, cp := range m.checkpoints[taskID] {
		if isAdmin || cp.TenantID == callerTenant {
			res = append(res, cp)
		}
	}
	return res, nil
}

func (m *MemoryCheckpointRepository) DeleteCheckpoints(ctx context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	identity, hasID := domain.TenantIdentityFromContext(ctx)
	callerTenant := "default"
	isAdmin := false
	if hasID {
		if identity.Role == "admin" {
			isAdmin = true
		}
		if identity.TenantID != "" {
			callerTenant = identity.TenantID
		}
	}

	if isAdmin {
		delete(m.checkpoints, taskID)
		return nil
	}

	var remaining []*domain.AgentCheckpoint
	for _, cp := range m.checkpoints[taskID] {
		if cp.TenantID != callerTenant {
			remaining = append(remaining, cp)
		}
	}
	if len(remaining) == 0 {
		delete(m.checkpoints, taskID)
	} else {
		m.checkpoints[taskID] = remaining
	}
	return nil
}
