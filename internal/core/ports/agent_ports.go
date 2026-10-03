package ports

import (
	"context"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

// ApprovalProvider giao diện cho cơ chế Human-in-the-Loop xác nhận hành động
type ApprovalProvider interface {
	RequestApproval(ctx context.Context, req domain.ApprovalRequest) (bool, error)
}

// ToolRegistry giao diện quản lý danh mục và thực thi các công cụ nội tại
type ToolRegistry interface {
	RegisterTool(tool domain.AgentTool)
	GetTool(name string) (domain.AgentTool, bool)
	ListTools() []domain.AgentTool
	ToOpenAITools() []domain.OpenAITool
	Execute(ctx context.Context, name string, argsJSON string) (string, error)
}

// ToolExecutionService định nghĩa ranh giới chính sách tập trung (Policy Engine) kiểm soát toàn bộ lời gọi công cụ
type ToolExecutionService interface {
	ExecuteTool(ctx context.Context, toolName string, argsJSON string) (string, error)
	ValidateToolExecution(ctx context.Context, toolName string, argsJSON string) error
}

// AgentRunner giao diện điều phối vòng lặp tự trị ReAct
type AgentRunner interface {
	Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error)
}

// CheckpointRepository giao diện lưu trữ và phục hồi Snapshot trạng thái Agent bền vững (SQLite)
type CheckpointRepository interface {
	SaveCheckpoint(ctx context.Context, cp *domain.AgentCheckpoint) error
	GetLatestCheckpoint(ctx context.Context, taskID string) (*domain.AgentCheckpoint, error)
	ListCheckpoints(ctx context.Context, taskID string) ([]*domain.AgentCheckpoint, error)
	DeleteCheckpoints(ctx context.Context, taskID string) error
}

// GraphWorkflowRunner giao diện điều phối luồng State Machine (PLAN -> EXECUTE -> VERIFY -> FIX)
type GraphWorkflowRunner interface {
	RunGraph(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentGraphState, error)
	ResumeGraph(ctx context.Context, taskID string, opts domain.AgentRunOptions) (*domain.AgentGraphState, error)
}

// MemoryRepository giao diện lưu trữ và truy vấn bộ nhớ dài hạn Archival Memory (Hybrid FTS5 + Vector)
type MemoryRepository interface {
	Store(ctx context.Context, item *domain.ArchivalMemoryItem) error
	Get(ctx context.Context, key string) (*domain.ArchivalMemoryItem, error)
	SearchHybrid(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error)
	SearchFTS(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error)
	Delete(ctx context.Context, key string) error
	ListAll(ctx context.Context) ([]domain.ArchivalMemoryItem, error)
}

// MemoryService giao diện quản lý tổng hợp 3 tầng bộ nhớ của Agent
type MemoryService interface {
	GetCoreMemory() *domain.CoreMemory
	GetCoreMemoryForContext(ctx context.Context) *domain.CoreMemory
	UpdateCoreMemory(update func(core *domain.CoreMemory))
	UpdateCoreMemoryForContext(ctx context.Context, update func(core *domain.CoreMemory))
	StoreArchival(ctx context.Context, key, content string, tags []string) error
	SearchArchival(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error)
	CompactConversation(ctx context.Context, messages []domain.OpenAIMessage, threshold int) ([]domain.OpenAIMessage, error)
}

// SubagentSupervisor giao diện điều phối và phân bổ nhiệm vụ cho các sub-agents biệt lập
type SubagentSupervisor interface {
	InvokeSubagent(ctx context.Context, task domain.SubagentTask) (*domain.SubagentResult, error)
	RegisterRole(descriptor domain.SubagentDescriptor)
	GetRoleDescriptor(role domain.SubagentRole) (*domain.SubagentDescriptor, bool)
	ListRoles() []domain.SubagentDescriptor
}

// AgentRunRepository giao diện lưu trữ trạng thái bền vững của các tác vụ Agent (Durable Agent Job)
type AgentRunRepository interface {
	Create(ctx context.Context, run *domain.AgentRun) error
	Update(ctx context.Context, run *domain.AgentRun) error
	UpdateWithTransition(ctx context.Context, run *domain.AgentRun, allowedFromStatuses ...domain.AgentRunStatus) (bool, error)
	Get(ctx context.Context, runID string) (*domain.AgentRun, error)
	GetForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error)
	List(ctx context.Context, tenantID string, limit, offset int) ([]*domain.AgentRun, error)
	ListPendingRuns(ctx context.Context) ([]*domain.AgentRun, error)
	AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error
	GetEvents(ctx context.Context, runID string, afterID int64) ([]domain.AgentRunEvent, error)
	GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error)
	Cancel(ctx context.Context, runID string) error
	CancelForTenant(ctx context.Context, tenantID, runID string) error
	ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error)
	RenewLease(ctx context.Context, runID, workerID string, claimGeneration int64, leaseDuration time.Duration) (bool, error)
	AppendOwnedEvent(ctx context.Context, event *domain.AgentRunEvent, workerID string, claimGeneration int64) (bool, error)
	UpdateOwned(ctx context.Context, run *domain.AgentRun, workerID string, claimGeneration int64) (bool, error)
	FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error)
}

// ToolExecutionLedger quản lý nhật ký thực thi công cụ durable để tránh side-effect replay sau crash
type ToolExecutionLedger interface {
	RecordPlannedOrRunning(ctx context.Context, exec *domain.ToolExecutionRecord) error
	RecordFinished(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string) error
	GetExecution(ctx context.Context, tenantID, runID, toolCallID string) (*domain.ToolExecutionRecord, error)
	MarkUnknownAfterRestart(ctx context.Context, tenantID, runID, toolCallID string) error
}

// AgentJobService giao diện điều phối hàng đợi và thực thi tác vụ Agent chạy nền (Async Job Engine)
type AgentJobService interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
	RecoverPendingRuns(ctx context.Context) ([]*domain.AgentRun, error)
	StartRecoveryLoop(ctx context.Context, interval time.Duration)
	ScanRecoverableRuns(ctx context.Context) ([]*domain.AgentRun, error)
	SubmitRun(ctx context.Context, goal string, opts domain.AgentRunOptions, idempotencyKey string) (*domain.AgentRun, error)
	GetRun(ctx context.Context, runID string) (*domain.AgentRun, error)
	GetRunForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error)
	CancelRun(ctx context.Context, runID string) error
	CancelRunForTenant(ctx context.Context, tenantID, runID string) error
	ResumeRun(ctx context.Context, runID string, feedback string) (*domain.AgentRun, error)
	ResumeRunForTenant(ctx context.Context, tenantID, runID string, feedback string) (*domain.AgentRun, error)
	SubscribeEvents(ctx context.Context, runID string) (<-chan domain.AgentRunEvent, func(), error)
	SubscribeEventsForTenant(ctx context.Context, tenantID, runID string) (<-chan domain.AgentRunEvent, func(), error)
}

// DistributedLockProvider giao diện khóa phân tán hỗ trợ môi trường multi-instance
type DistributedLockProvider interface {
	AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, key string) error
}

// SharedStateStore giao diện lưu trữ key-value phân tán giữa nhiều node gateway
type SharedStateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}




