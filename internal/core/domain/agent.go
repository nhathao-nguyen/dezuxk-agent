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
	PermissionSafe PermissionLevel = "safe"
	// PermissionDestructive: Ghi file, xóa file, chạy shell command -> Yêu cầu người dùng phê duyệt trong chế độ Supervised
	PermissionDestructive PermissionLevel = "destructive"
)

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

// AgentRunOptions tùy chọn khi khởi chạy Agent
type AgentRunOptions struct {
	Model         string               `json:"model"`
	MaxSteps      int                  `json:"max_steps"`
	Supervised    bool                 `json:"supervised"` // Bán tự trị: yêu cầu xác nhận khi gặp lệnh destructive
	Workspace     string               `json:"workspace"`
	CustomPrompt  string               `json:"custom_prompt,omitempty"` // Tùy biến system prompt nếu có
	RequireAction bool                 `json:"require_action,omitempty"` // Bắt buộc phải có tool call để sửa đổi thực tế, không chấp nhận text suông
	UseSandbox    bool                 `json:"use_sandbox,omitempty"`    // Tự động tạo git worktree sandbox (.dezuxk/worktrees/<task_id>)
	AutoMerge     bool                 `json:"auto_merge,omitempty"`     // Tự động merge vào nhánh chính khi hoàn tất thành công
	OnProgress    StepProgressCallback `json:"-"`
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
	TaskID       string          `json:"task_id"`
	Goal         string          `json:"goal"`
	Model        string          `json:"model"`
	Workspace    string          `json:"workspace"`
	Messages     []OpenAIMessage `json:"messages"`
	Steps        []AgentStep     `json:"steps"`
	CurrentStep  int             `json:"current_step"`
	MaxSteps     int             `json:"max_steps"`
	IsCompleted  bool            `json:"is_completed"`
	StopReason   string          `json:"stop_reason"` // "completed" | "max_steps" | "interrupted" | "error"
	FinalAnswer  string          `json:"final_answer"`
	Error        string          `json:"error,omitempty"`
	GitDiff      string          `json:"git_diff,omitempty"`      // Git Diff sau khi tác vụ hoàn thành
	WorktreePath string          `json:"worktree_path,omitempty"`  // Thư mục sandbox nếu có
	BranchName   string          `json:"branch_name,omitempty"`    // Nhánh git tạm nếu có
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
