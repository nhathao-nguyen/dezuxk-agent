package domain

import (
	"context"
	"encoding/json"
	"time"
)

// PermissionLevel phân loại mức độ an toàn của công cụ
type PermissionLevel string

const (
	// PermissionSafe: Đọc file, tra cứu, liệt kê thư mục -> Tự động thực thi không cần hỏi
	PermissionSafe            PermissionLevel = "safe"
	PermissionRead            PermissionLevel = "read"
	PermissionWrite           PermissionLevel = "write"
	PermissionExecute         PermissionLevel = "execute"
	PermissionNetwork         PermissionLevel = "network"
	PermissionExternalSideEff PermissionLevel = "external_side_effect"
	// PermissionDestructive: Ghi file, xóa file, chạy shell command -> Yêu cầu người dùng phê duyệt trong chế độ Supervised
	PermissionDestructive      PermissionLevel = "destructive"
	PermissionRequiresApproval PermissionLevel = "requires_approval"
)

// IsDestructiveOrRequiresApproval kiểm tra mức độ phân quyền có đòi hỏi xác nhận từ con người không
func (p PermissionLevel) IsDestructiveOrRequiresApproval() bool {
	switch p {
	case PermissionDestructive, PermissionRequiresApproval, PermissionWrite, PermissionExecute:
		return true
	default:
		return false
	}
}

// AgentTool định nghĩa một công cụ có thể thực thi bởi Agent
type AgentTool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Permission() PermissionLevel
	Execute(ctx context.Context, argsJSON string) (string, error)
}

// ApprovalRequest yêu cầu phê duyệt từ Human-in-the-Loop
type ApprovalRequest struct {
	ToolName    string    `json:"tool_name"`
	Arguments   string    `json:"arguments"`
	Description string    `json:"description"`
	Permission  PermissionLevel `json:"permission"`
	RequestedAt time.Time `json:"requested_at"`
}

// StepProgressCallback hàm callback nhận thông báo tiến trình từng bước
type StepProgressCallback func(step int, kind string, message string)

// Stop Reasons chuẩn theo production-grade spec
const (
	StopReasonCompleted           = "completed"
	StopReasonMaxStepsReached     = "max_steps_reached"
	StopReasonMaxToolCallsReached = "max_tool_calls_reached"
	StopReasonRepeatedToolLoop    = "repeated_tool_loop"
	StopReasonTimeout             = "timeout"
	StopReasonCancelled           = "cancelled"
	StopReasonUpstreamUnavailable = "upstream_unavailable"
	StopReasonPolicyDenied        = "policy_denied"
	StopReasonVerificationFailed  = "verification_failed"
)

// AgentRunOptions tùy chọn khi khởi chạy Agent
type AgentRunOptions struct {
	Model                  string               `json:"model"`
	MaxSteps               int                  `json:"max_steps"`
	MaxToolCalls           int                  `json:"max_tool_calls,omitempty"`           // Giới hạn tổng số tool calls cho cả run (mặc định 50)
	MaxRepeatedCalls       int                  `json:"max_repeated_calls,omitempty"`        // Giới hạn số lần gọi trùng lặp tool+args (mặc định 3)
	MaxExecutionDuration   time.Duration        `json:"max_execution_duration,omitempty"`    // Giới hạn thời lượng chạy tối đa cho cả run (mặc định 10 phút)
	MaxConsecutiveFailures int                  `json:"max_consecutive_failures,omitempty"`  // Giới hạn lỗi liên tiếp của tool (mặc định 5)
	Supervised             bool                 `json:"supervised"`                          // Bán tự trị: yêu cầu xác nhận khi gặp lệnh destructive
	Workspace              string               `json:"workspace"`
	CustomPrompt           string               `json:"custom_prompt,omitempty"`             // Tùy biến system prompt nếu có
	RequireAction          bool                 `json:"require_action,omitempty"`           // Bắt buộc phải có tool call để sửa đổi thực tế, không chấp nhận text suông
	UseSandbox             bool                 `json:"use_sandbox,omitempty"`              // Tự động tạo git worktree sandbox (.dezuxk/worktrees/<task_id>)
	AutoMerge              bool                 `json:"auto_merge,omitempty"`               // Tự động merge vào nhánh chính khi hoàn tất thành công
	OnProgress             StepProgressCallback `json:"-"`
}

// AgentStep ghi lại một bước thực thi trong chu kỳ ReAct
type AgentStep struct {
	StepIndex        int              `json:"step_index"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	AssistantMessage string           `json:"assistant_message,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolResults      []string         `json:"tool_results,omitempty"`
	Timestamp        time.Time        `json:"timestamp"`
}

// AgentState lưu trữ toàn bộ trạng thái vòng đời của một tác vụ Agent
type AgentState struct {
	TaskID         string          `json:"task_id"`
	Goal           string          `json:"goal"`
	Model          string          `json:"model"`
	Workspace      string          `json:"workspace"`
	Messages       []OpenAIMessage `json:"messages"`
	Steps          []AgentStep     `json:"steps"`
	CurrentStep    int             `json:"current_step"`
	MaxSteps       int             `json:"max_steps"`
	TotalToolCalls int             `json:"total_tool_calls"`
	IsCompleted    bool            `json:"is_completed"`
	StopReason     string          `json:"stop_reason"` // Xem các hằng số StopReason
	FinalAnswer    string          `json:"final_answer"`
	Error          string          `json:"error,omitempty"`
	GitDiff        string          `json:"git_diff,omitempty"`     // Git Diff sau khi tác vụ hoàn thành
	WorktreePath   string          `json:"worktree_path,omitempty"` // Thư mục sandbox nếu có
	BranchName     string          `json:"branch_name,omitempty"`   // Nhánh git tạm nếu có
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
