package domain

import (
	"encoding/json"
	"time"
)

// AgentRunStatus trạng thái vòng đời của Agent Run
type AgentRunStatus string

const (
	RunStatusQueued             AgentRunStatus = "queued"
	RunStatusRunning            AgentRunStatus = "running"
	RunStatusWaitingForTool     AgentRunStatus = "waiting_for_tool"
	RunStatusWaitingForApproval AgentRunStatus = "waiting_for_approval"
	RunStatusRecovering         AgentRunStatus = "recovering"
	RunStatusInterrupted        AgentRunStatus = "interrupted"
	RunStatusCompleted          AgentRunStatus = "completed"
	RunStatusFailed             AgentRunStatus = "failed"
	RunStatusCancelled          AgentRunStatus = "cancelled"
)

const (
	StopReasonServerRestart          = "server_restart"
	StopReasonReauthorizationRequired = "reauthorization_required"
	StopReasonAuthorizationRevoked   = "authorization_revoked"
	StopReasonQuotaExceeded          = "quota_exceeded"
	StopReasonOwnershipLost          = "ownership_lost"
)

// AgentExecutionConfig lưu trữ snapshot cấu hình thực thi của AgentRun để phục hồi đầy đủ ngữ cảnh sau restart
type AgentExecutionConfig struct {
	MaxSteps               int           `json:"max_steps,omitempty"`
	MaxToolCalls           int           `json:"max_tool_calls,omitempty"`
	MaxRepeatedCalls       int           `json:"max_repeated_calls,omitempty"`
	MaxExecutionDuration   time.Duration `json:"max_execution_duration,omitempty"`
	MaxConsecutiveFailures int           `json:"max_consecutive_failures,omitempty"`
	Supervised             bool          `json:"supervised,omitempty"`
	Workspace              string        `json:"workspace,omitempty"`
	RequireAction          bool          `json:"require_action,omitempty"`
	UseSandbox             bool          `json:"use_sandbox,omitempty"`
	AutoMerge              bool          `json:"auto_merge,omitempty"`
	CustomPrompt           string        `json:"custom_prompt,omitempty"`
	Model                  string        `json:"model,omitempty"`
}

// AgentRun đại diện cho một bản ghi tác vụ bền vững (Durable Agent Job)
type AgentRun struct {
	ID              string                `json:"id"`
	TenantID        string                `json:"tenant_id,omitempty"`
	IdempotencyKey  string                `json:"idempotency_key,omitempty"`
	Goal            string                `json:"goal"`
	Status          AgentRunStatus        `json:"status"`
	Model           string                `json:"model"`
	Workspace       string                `json:"workspace"`
	CurrentStep     int                   `json:"current_step"`
	MaxSteps        int                   `json:"max_steps"`
	TotalToolCalls  int                   `json:"total_tool_calls"`
	StopReason      string                `json:"stop_reason,omitempty"`
	FinalAnswer     string                `json:"final_answer,omitempty"`
	Error           string                `json:"error,omitempty"`
	GitDiff         string                `json:"git_diff,omitempty"`
	ParentRunID     string                `json:"parent_run_id,omitempty"`
	ResumeFromRunID string                `json:"resume_from_run_id,omitempty"`
	WorkerID        string                `json:"worker_id,omitempty"`
	ClaimGeneration int64                 `json:"claim_generation"`
	BillingKeyID    string                `json:"billing_key_id,omitempty"`
	LeaseUntil      *time.Time            `json:"lease_until,omitempty"`
	HeartbeatAt     *time.Time            `json:"heartbeat_at,omitempty"`
	SecurityContext *AgentSecurityContext `json:"security_context,omitempty"`
	ExecutionConfig *AgentExecutionConfig `json:"execution_config,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	FinishedAt      *time.Time            `json:"finished_at,omitempty"`
	Events          []AgentRunEvent       `json:"events,omitempty"`
}

// AgentRunEvent ghi nhận một sự kiện tiến trình trong chu kỳ chạy tác vụ
type AgentRunEvent struct {
	ID        int64     `json:"id"`
	TenantID  string    `json:"tenant_id,omitempty"`
	RunID     string    `json:"run_id"`
	Step      int       `json:"step"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ToolCall đại diện cho cấu trúc chuẩn hóa nội bộ của một lời gọi công cụ
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult đại diện cho kết quả thực thi công cụ chuẩn hóa nội bộ
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	Error      string `json:"error,omitempty"`
}
