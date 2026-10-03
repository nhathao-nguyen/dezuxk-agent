package ports

import (
	"context"

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



