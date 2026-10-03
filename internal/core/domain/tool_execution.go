package domain

import (
	"time"
)

// ToolExecutionStatus trạng thái vòng đời của một lần thực thi công cụ
type ToolExecutionStatus string

const (
	ToolExecutionPlanned             ToolExecutionStatus = "planned"
	ToolExecutionRunning             ToolExecutionStatus = "running"
	ToolExecutionSucceeded           ToolExecutionStatus = "succeeded"
	ToolExecutionFailed              ToolExecutionStatus = "failed"
	ToolExecutionUnknownAfterRestart ToolExecutionStatus = "unknown_after_restart"
)

// ToolExecutionRecord bản ghi lưu vết thực thi bền vững của công cụ
type ToolExecutionRecord struct {
	TenantID        string              `json:"tenant_id"`
	RunID           string              `json:"run_id"`
	ToolCallID      string              `json:"tool_call_id"`
	ToolName        string              `json:"tool_name"`
	ArgsHash        string              `json:"args_hash"`
	Status          ToolExecutionStatus `json:"status"`
	ResultJSON      string              `json:"result_json,omitempty"`
	Error           string              `json:"error,omitempty"`
	WorkerID        string              `json:"worker_id,omitempty"`
	ClaimGeneration int64               `json:"claim_generation"`
	StartedAt       *time.Time          `json:"started_at,omitempty"`
	FinishedAt      *time.Time          `json:"finished_at,omitempty"`
}

// ToolExecutionSemantics đặc tả ngữ nghĩa an toàn và tính idempotent của công cụ
type ToolExecutionSemantics struct {
	Idempotent bool `json:"idempotent"`
	ReadOnly   bool `json:"read_only"`
}

// ToolSemanticsProvider giao diện công cụ cung cấp ngữ nghĩa thực thi
type ToolSemanticsProvider interface {
	ExecutionSemantics() ToolExecutionSemantics
}

// ResolveToolSemantics suy diễn ngữ nghĩa an toàn của công cụ
func ResolveToolSemantics(tool AgentTool) ToolExecutionSemantics {
	if tool == nil {
		return ToolExecutionSemantics{ReadOnly: false, Idempotent: false}
	}
	if provider, ok := tool.(ToolSemanticsProvider); ok {
		return provider.ExecutionSemantics()
	}
	perm := tool.Permission()
	if perm == PermissionSafe || perm == PermissionRead {
		return ToolExecutionSemantics{ReadOnly: true, Idempotent: true}
	}
	return ToolExecutionSemantics{ReadOnly: false, Idempotent: false}
}
