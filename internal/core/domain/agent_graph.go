package domain

import (
	"time"
)

// GraphNodeKind định danh các trạng thái trong đồ thị State Machine (LangGraph-inspired)
type GraphNodeKind string

const (
	NodeKindPlan      GraphNodeKind = "PLAN"      // Lập kế hoạch phân rã nhiệm vụ thành Todo Checklist
	NodeKindExecute   GraphNodeKind = "EXECUTE"   // Thực thi mã nguồn và công cụ cho bước hiện tại
	NodeKindVerify    GraphNodeKind = "VERIFY"    // Chạy kiểm thử / linter để thu thập bằng chứng xác minh
	NodeKindFix       GraphNodeKind = "FIX"       // Chẩn đoán lỗi và áp dụng bản vá tự sửa sai
	NodeKindComplete  GraphNodeKind = "COMPLETE"  // Hoàn thành với đầy đủ bằng chứng đã xác minh
	NodeKindFailed    GraphNodeKind = "FAILED"    // Thất bại sau khi đã vượt quá số lần Fix tối đa
	NodeKindInterrupt GraphNodeKind = "INTERRUPT" // Tạm dừng chờ người dùng phê duyệt hoặc can thiệp
)

// PlanStepStatus trạng thái của từng bước trong kế hoạch
type PlanStepStatus string

const (
	StepStatusPending    PlanStepStatus = "pending"
	StepStatusInProgress PlanStepStatus = "in_progress"
	StepStatusPassed     PlanStepStatus = "passed"
	StepStatusFailed     PlanStepStatus = "failed"
)

// PlanStep đại diện cho một bước có thể đo lường và kiểm chứng trong kế hoạch
type PlanStep struct {
	ID                  int            `json:"id"`
	Title               string         `json:"title"`
	Description         string         `json:"description"`
	VerificationCommand string         `json:"verification_cmd,omitempty"` // Lệnh kiểm thử (ví dụ: go test ./...)
	Status              PlanStepStatus `json:"status"`
	Evidence            string         `json:"evidence,omitempty"`     // Bằng chứng kiểm thử tươi mới khi PASS
	ErrorOutput         string         `json:"error_output,omitempty"` // Log lỗi stdout/stderr khi FAIL
	FixAttempts         int            `json:"fix_attempts"`           // Số lần đã thử tự động sửa
	UpdatedAt           time.Time      `json:"updated_at"`
}

// TaskPlan đại diện cho kế hoạch tổng thể gồm danh sách các bước tuần tự
type TaskPlan struct {
	Goal             string     `json:"goal"`
	Steps            []PlanStep `json:"steps"`
	CurrentStepIndex int        `json:"current_step_index"` // 0-indexed trỏ tới bước đang làm
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// IsAllPassed kiểm tra xem toàn bộ các bước đã PASS chưa
func (p *TaskPlan) IsAllPassed() bool {
	if len(p.Steps) == 0 {
		return false
	}
	for _, s := range p.Steps {
		if s.Status != StepStatusPassed {
			return false
		}
	}
	return true
}

// GetCurrentStep trả về con trỏ tới bước hiện tại đang xử lý
func (p *TaskPlan) GetCurrentStep() *PlanStep {
	if p.CurrentStepIndex >= 0 && p.CurrentStepIndex < len(p.Steps) {
		return &p.Steps[p.CurrentStepIndex]
	}
	return nil
}

// AgentCheckpoint snapshot trạng thái lưu bền vững vào SQLite để phục vụ Resume / Time-Travel
type AgentCheckpoint struct {
	ID            int64         `json:"id"`
	TenantID      string        `json:"tenant_id,omitempty"`
	TaskID        string        `json:"task_id"`
	NodeKind      GraphNodeKind `json:"node_kind"`
	StepIndex     int           `json:"step_index"`
	StateSnapshot AgentState    `json:"state_snapshot"`
	PlanSnapshot  TaskPlan      `json:"plan_snapshot"`
	CreatedAt     time.Time     `json:"created_at"`
}

// AgentGraphState trạng thái tổng hợp của luồng Workflow State Machine
type AgentGraphState struct {
	TaskID       string        `json:"task_id"`
	Goal         string        `json:"goal"`
	CurrentNode  GraphNodeKind `json:"current_node"`
	Plan         TaskPlan      `json:"plan"`
	AgentState   AgentState    `json:"agent_state"`
	MaxFixRetry  int           `json:"max_fix_retry"` // Mặc định 3 lần tự sửa lỗi trước khi dừng
	Checkpoints  int           `json:"checkpoints_count"`
	IsCompleted  bool          `json:"is_completed"`
	FinalSummary string        `json:"final_summary"`
	Error        string        `json:"error,omitempty"`
	GitDiff      string        `json:"git_diff,omitempty"`      // Git Diff sau khi workflow hoàn thành
	WorktreePath string        `json:"worktree_path,omitempty"` // Thư mục sandbox nếu có
	BranchName   string        `json:"branch_name,omitempty"`   // Nhánh git tạm nếu có
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}
