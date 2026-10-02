package domain

// SubagentRole xác định vai trò chuyên biệt của sub-agent
type SubagentRole string

const (
	RoleResearcher SubagentRole = "researcher"
	RoleCoder      SubagentRole = "coder"
	RoleReviewer   SubagentRole = "reviewer"
	RoleGeneral    SubagentRole = "general"
)

// SubagentDescriptor mô tả cấu hình và quyền hạn của một loại sub-agent
type SubagentDescriptor struct {
	Role         SubagentRole `json:"role"`
	Description  string       `json:"description"`
	SystemPrompt string       `json:"system_prompt"`
	AllowedTools []string     `json:"allowed_tools"` // Rỗng nghĩa là cho phép toàn bộ công cụ
	MaxSteps     int          `json:"max_steps"`
}

// SubagentTask chứa thông tin giao việc độc lập cho sub-agent
type SubagentTask struct {
	Role       SubagentRole         `json:"role"`
	Prompt     string               `json:"prompt"`
	Context    string               `json:"context,omitempty"`
	Workspace  string               `json:"workspace,omitempty"`
	Model      string               `json:"model,omitempty"`
	Supervised bool                 `json:"supervised,omitempty"`
	OnProgress StepProgressCallback `json:"-"`
}

// SubagentResult chứa kết quả sau khi sub-agent hoàn thành nhiệm vụ
type SubagentResult struct {
	Role       SubagentRole `json:"role"`
	Prompt     string       `json:"prompt"`
	Summary    string       `json:"summary"`
	StepsCount int          `json:"steps_count"`
	Success    bool         `json:"success"`
	Error      string       `json:"error,omitempty"`
}
