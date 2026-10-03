package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// MemoryAgentRunRepository triển khai in-memory ports.AgentRunRepository cho kiểm thử
type MemoryAgentRunRepository struct {
	mu        sync.RWMutex
	runs      map[string]*domain.AgentRun
	events    map[string][]domain.AgentRunEvent
	toolExecs map[string]*domain.ToolExecutionRecord
	seq       int64
}

// NewMemoryAgentRunRepository khởi tạo in-memory agent run repository
func NewMemoryAgentRunRepository() *MemoryAgentRunRepository {
	return &MemoryAgentRunRepository{
		runs:      make(map[string]*domain.AgentRun),
		events:    make(map[string][]domain.AgentRunEvent),
		toolExecs: make(map[string]*domain.ToolExecutionRecord),
	}
}

var _ ports.AgentRunRepository = (*MemoryAgentRunRepository)(nil)
var _ ports.ToolExecutionLedger = (*MemoryAgentRunRepository)(nil)

func (m *MemoryAgentRunRepository) Create(ctx context.Context, run *domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if run == nil || run.ID == "" {
		return fmt.Errorf("run không hợp lệ")
	}
	if run.TenantID == "" {
		run.TenantID = "default"
	}

	// Kiểm tra tính duy nhất của idempotency_key theo tenant
	if strings.TrimSpace(run.IdempotencyKey) != "" {
		for _, r := range m.runs {
			if r.TenantID == run.TenantID && r.IdempotencyKey == run.IdempotencyKey {
				return ErrIdempotencyConflict
			}
		}
	}

	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	run.UpdatedAt = run.CreatedAt

	copied := *run
	m.runs[run.ID] = &copied
	return nil
}

func (m *MemoryAgentRunRepository) Update(ctx context.Context, run *domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if run == nil || run.ID == "" {
		return fmt.Errorf("run không hợp lệ")
	}

	existing, ok := m.runs[run.ID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, run.ID)
	}

	// State-aware: không đè trạng thái completed/failed lên cancelled
	if existing.Status == domain.RunStatusCancelled && (run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed) {
		return nil
	}

	run.UpdatedAt = time.Now()
	copied := *run
	m.runs[run.ID] = &copied
	return nil
}

func (m *MemoryAgentRunRepository) UpdateWithTransition(ctx context.Context, run *domain.AgentRun, allowedFromStatuses ...domain.AgentRunStatus) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if run == nil || run.ID == "" {
		return false, fmt.Errorf("run không hợp lệ")
	}

	existing, ok := m.runs[run.ID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrRunNotFound, run.ID)
	}

	if len(allowedFromStatuses) > 0 {
		matched := false
		for _, st := range allowedFromStatuses {
			if existing.Status == st {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}

	run.UpdatedAt = time.Now()
	copied := *run
	m.runs[run.ID] = &copied
	return true, nil
}

func (m *MemoryAgentRunRepository) Get(ctx context.Context, runID string) (*domain.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	copied := *run
	copied.Events = append([]domain.AgentRunEvent(nil), m.events[runID]...)
	return &copied, nil
}

func (m *MemoryAgentRunRepository) GetForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.runs[runID]
	if !ok || run.TenantID != tenantID {
		return nil, fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
	}

	copied := *run
	copied.Events = append([]domain.AgentRunEvent(nil), m.events[runID]...)
	return &copied, nil
}

func (m *MemoryAgentRunRepository) List(ctx context.Context, tenantID string, limit, offset int) ([]*domain.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*domain.AgentRun
	for _, r := range m.runs {
		if tenantID != "" {
			if r.TenantID == tenantID {
				copied := *r
				result = append(result, &copied)
			}
		} else {
			copied := *r
			result = append(result, &copied)
		}
	}
	return result, nil
}

func (m *MemoryAgentRunRepository) ListPendingRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*domain.AgentRun
	for _, r := range m.runs {
		if r.Status == domain.RunStatusQueued ||
			r.Status == domain.RunStatusRunning ||
			r.Status == domain.RunStatusWaitingForTool ||
			r.Status == domain.RunStatusWaitingForApproval ||
			r.Status == domain.RunStatusRecovering {
			copied := *r
			result = append(result, &copied)
		}
	}
	return result, nil
}

func (m *MemoryAgentRunRepository) AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	event.ID = m.seq
	if event.TenantID == "" {
		if r, ok := m.runs[event.RunID]; ok && r.TenantID != "" {
			event.TenantID = r.TenantID
		} else {
			event.TenantID = "default"
		}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	m.events[event.RunID] = append(m.events[event.RunID], *event)
	return nil
}

func (m *MemoryAgentRunRepository) GetEvents(ctx context.Context, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.events[runID]
	var res []domain.AgentRunEvent
	for _, ev := range all {
		if ev.ID > afterID {
			res = append(res, ev)
		}
	}
	return res, nil
}

func (m *MemoryAgentRunRepository) GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.runs[runID]
	if !ok || run.TenantID != tenantID {
		return nil, fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
	}

	all := m.events[runID]
	var res []domain.AgentRunEvent
	for _, ev := range all {
		if ev.ID > afterID {
			res = append(res, ev)
		}
	}
	return res, nil
}

func (m *MemoryAgentRunRepository) Cancel(ctx context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed || run.Status == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", ErrInvalidStatusTransition, run.Status)
	}
	now := time.Now()
	run.Status = domain.RunStatusCancelled
	run.StopReason = domain.StopReasonCancelled
	run.UpdatedAt = now
	run.FinishedAt = &now
	return nil
}

func (m *MemoryAgentRunRepository) CancelForTenant(ctx context.Context, tenantID, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[runID]
	if !ok || run.TenantID != tenantID {
		return fmt.Errorf("%w: %s cho tenant %s", ErrRunNotFound, runID, tenantID)
	}
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed || run.Status == domain.RunStatusCancelled {
		return fmt.Errorf("%w: không thể hủy tác vụ đang ở trạng thái %s", ErrInvalidStatusTransition, run.Status)
	}

	now := time.Now()
	run.Status = domain.RunStatusCancelled
	run.StopReason = domain.StopReasonCancelled
	run.UpdatedAt = now
	run.FinishedAt = &now
	return nil
}

func (m *MemoryAgentRunRepository) ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[runID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	now := time.Now()
	// Active lease: ALWAYS false regardless of workerID!
	if run.LeaseUntil != nil && run.LeaseUntil.After(now) {
		return false, nil
	}

	isExpiredOrNull := run.LeaseUntil == nil || !run.LeaseUntil.After(now)
	if run.Status == domain.RunStatusQueued ||
		((run.Status == domain.RunStatusRunning || run.Status == domain.RunStatusRecovering || run.Status == domain.RunStatusWaitingForTool) && isExpiredOrNull) {
		run.Status = domain.RunStatusRunning
		run.WorkerID = workerID
		run.ClaimGeneration++
		lease := now.Add(leaseDuration)
		run.LeaseUntil = &lease
		run.HeartbeatAt = &now
		run.UpdatedAt = now
		return true, nil
	}
	return false, nil
}

func (m *MemoryAgentRunRepository) RenewLease(ctx context.Context, runID, workerID string, claimGeneration int64, leaseDuration time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[runID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	if (run.Status == domain.RunStatusRunning || run.Status == domain.RunStatusRecovering) && run.WorkerID == workerID && run.ClaimGeneration == claimGeneration {
		now := time.Now()
		lease := now.Add(leaseDuration)
		run.LeaseUntil = &lease
		run.HeartbeatAt = &now
		run.UpdatedAt = now
		return true, nil
	}
	return false, nil
}

func (m *MemoryAgentRunRepository) UpdateOwned(ctx context.Context, run *domain.AgentRun, workerID string, claimGeneration int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if run == nil || run.ID == "" {
		return false, fmt.Errorf("run không hợp lệ")
	}

	existing, ok := m.runs[run.ID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrRunNotFound, run.ID)
	}

	if existing.Status == domain.RunStatusCancelled {
		return false, nil
	}

	if existing.WorkerID != workerID || existing.ClaimGeneration != claimGeneration {
		return false, nil // Ownership lost!
	}

	run.UpdatedAt = time.Now()
	run.WorkerID = existing.WorkerID
	run.ClaimGeneration = existing.ClaimGeneration
	copied := *run
	m.runs[run.ID] = &copied
	return true, nil
}

func (m *MemoryAgentRunRepository) FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error) {
	if key == "" {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tenantID == "" {
		tenantID = "default"
	}
	for _, r := range m.runs {
		if r.TenantID == tenantID && r.IdempotencyKey == key {
			copied := *r
			return &copied, nil
		}
	}
	return nil, nil
}

func (m *MemoryAgentRunRepository) AppendOwnedEvent(ctx context.Context, event *domain.AgentRunEvent, workerID string, claimGeneration int64) (bool, error) {
	if event == nil || event.RunID == "" {
		return false, fmt.Errorf("event không hợp lệ hoặc thiếu RunID")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[event.RunID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrRunNotFound, event.RunID)
	}

	if run.WorkerID != workerID || run.ClaimGeneration != claimGeneration {
		return false, nil // Ownership lost!
	}

	if event.TenantID == "" {
		event.TenantID = run.TenantID
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	m.seq++
	event.ID = m.seq
	m.events[event.RunID] = append(m.events[event.RunID], *event)
	return true, nil
}

// ToolExecutionLedger implementation
func (m *MemoryAgentRunRepository) makeToolExecKey(tenantID, runID, toolCallID string) string {
	if tenantID == "" {
		tenantID = "default"
	}
	return tenantID + ":" + runID + ":" + toolCallID
}

func (m *MemoryAgentRunRepository) RecordPlannedOrRunning(ctx context.Context, exec *domain.ToolExecutionRecord) error {
	if exec == nil || exec.RunID == "" || exec.ToolCallID == "" {
		return fmt.Errorf("record không hợp lệ")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.makeToolExecKey(exec.TenantID, exec.RunID, exec.ToolCallID)
	now := time.Now()
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}
	copied := *exec
	m.toolExecs[k] = &copied
	return nil
}

func (m *MemoryAgentRunRepository) RecordFinished(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.makeToolExecKey(tenantID, runID, toolCallID)
	existing, ok := m.toolExecs[k]
	now := time.Now()
	if !ok {
		m.toolExecs[k] = &domain.ToolExecutionRecord{
			TenantID:   tenantID,
			RunID:      runID,
			ToolCallID: toolCallID,
			Status:     status,
			ResultJSON: resultJSON,
			Error:      errStr,
			FinishedAt: &now,
		}
		return nil
	}

	existing.Status = status
	existing.ResultJSON = resultJSON
	existing.Error = errStr
	existing.FinishedAt = &now
	return nil
}

func (m *MemoryAgentRunRepository) GetExecution(ctx context.Context, tenantID, runID, toolCallID string) (*domain.ToolExecutionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	k := m.makeToolExecKey(tenantID, runID, toolCallID)
	existing, ok := m.toolExecs[k]
	if !ok {
		return nil, nil
	}
	copied := *existing
	return &copied, nil
}

func (m *MemoryAgentRunRepository) MarkUnknownAfterRestart(ctx context.Context, tenantID, runID, toolCallID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.makeToolExecKey(tenantID, runID, toolCallID)
	if existing, ok := m.toolExecs[k]; ok {
		existing.Status = domain.ToolExecutionUnknownAfterRestart
	}
	return nil
}

