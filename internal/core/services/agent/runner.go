package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/sandbox"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services/linter"
	"dezuxk-gateway/internal/core/services/policy"
)

func generateTaskID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "task_default"
	}
	return "task_" + hex.EncodeToString(b)
}

// Runner triển khai ports.AgentRunner
type Runner struct {
	chatUseCase  ports.ChatUseCase
	tools        ports.ToolRegistry
	approval     ports.ApprovalProvider
	policyEngine ports.ToolExecutionService
	memorySvc    ports.MemoryService
}

// NewRunner khởi tạo một Agent Runner
func NewRunner(chatUseCase ports.ChatUseCase, tools ports.ToolRegistry, approval ports.ApprovalProvider) *Runner {
	return &Runner{
		chatUseCase: chatUseCase,
		tools:       tools,
		approval:    approval,
	}
}

// SetPolicyEngine thiết lập engine chính sách điều phối kiểm soát tool
func (r *Runner) SetPolicyEngine(p ports.ToolExecutionService) {
	r.policyEngine = p
}

func (r *Runner) getPolicyEngine() ports.ToolExecutionService {
	if r.policyEngine != nil {
		return r.policyEngine
	}
	return policy.NewPolicyEngine(r.tools, r.approval)
}

// SetMemoryService liên kết hệ thống bộ nhớ 3 tầng vào Runner
func (r *Runner) SetMemoryService(m ports.MemoryService) {
	r.memorySvc = m
}

var _ ports.AgentRunner = (*Runner)(nil)

func (r *Runner) buildSystemPrompt(workspace string, customPrompt string) string {
	absWorkspace, _ := filepath.Abs(workspace)
	nowStr := time.Now().Format("2006-01-02 15:04:05 MST")

	var prompt string
	if strings.TrimSpace(customPrompt) != "" {
		prompt = fmt.Sprintf("%s\n\nOperating System: %s\nCurrent Time: %s\nWorking Directory: %s",
			strings.TrimSpace(customPrompt), runtime.GOOS, nowStr, absWorkspace)
	} else {
		prompt = fmt.Sprintf(`You are Dezuxk Autonomous Agent, an expert AI software engineering agent.
Operating System: %s
Current Time: %s
Working Directory: %s

## Operational Rules & Methodology
1. Goal-Driven & Systematic: Break down complex problems, formulate a plan, and execute step-by-step.
2. Inspect First: Before writing or modifying any code or files, always inspect existing code, files, or project structure using tools (use 'grep_code' for fast regex codebase search, 'read_symbol_definition' to read functions/structs by name without knowing line numbers, and 'read_file' to view file ranges).
3. Precise Modifications: Prefer replacing specific content blocks (replace_file_content) instead of blindly rewriting whole files.
4. Verify Everything: After making changes or when solving bugs, run tests or verification commands via run_command to prove that the solution works. Never claim completion without verification evidence.
5. Autonomous Completion: When you have successfully verified the solution and achieved the goal, provide your final response directly in text to conclude the task. Do NOT call tools after concluding.`,
			runtime.GOOS, nowStr, absWorkspace)
	}

	if r.memorySvc != nil {
		core := r.memorySvc.GetCoreMemory()
		if core != nil {
			prompt = prompt + "\n\n" + core.FormatPrompt()
		}
	}

	return prompt
}

func (r *Runner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, fmt.Errorf("mục tiêu nhiệm vụ (goal) không được để trống")
	}

	// Đảm bảo chính sách phân quyền Tenant được áp dụng và client không thể bypass
	identity, hasIdentity := domain.TenantIdentityFromContext(ctx)
	if !hasIdentity {
		identity = domain.DefaultInternalIdentity()
		if opts.Supervised {
			identity.RequireApproval = true
		}
		ctx = domain.ContextWithTenantIdentity(ctx, identity)
	}

	opts.MaxSteps = identity.EffectiveMaxSteps(opts.MaxSteps)
	if identity.EnforceSandbox {
		opts.UseSandbox = true
	}
	if !identity.AutoMergeAllowed {
		opts.AutoMerge = false
	}
	if identity.RequireApproval {
		opts.Supervised = true
	}

	if opts.Model == "" {
		opts.Model = "gemini-3.8-flash"
	}
	if opts.Workspace == "" {
		opts.Workspace = "."
	}

	ctx = domain.WithWorkspace(ctx, opts.Workspace)

	state := &domain.AgentState{
		TaskID:      generateTaskID(),
		Goal:        goal,
		Model:       opts.Model,
		Workspace:   opts.Workspace,
		MaxSteps:    opts.MaxSteps,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IsCompleted: false,
	}

	var sb *sandbox.Sandbox
	sandboxMgr := sandbox.GetDefaultManager()
	if opts.UseSandbox {
		createdSb, err := sandboxMgr.CreateSandbox(ctx, state.TaskID, opts.Workspace)
		if err == nil && createdSb != nil && createdSb.IsGit {
			sb = createdSb
			opts.Workspace = sb.WorktreeWorkspace
			state.WorktreePath = sb.WorktreePath
			state.BranchName = sb.BranchName
			ctx = domain.WithWorkspace(ctx, opts.Workspace)
			if opts.OnProgress != nil {
				opts.OnProgress(0, "sandbox_created", fmt.Sprintf("Đã khởi tạo Git Worktree Sandbox: %s (Nhánh: %s)", sb.WorktreePath, sb.BranchName))
			}
		}
	}

	finalizeSandbox := func() {
		if sb == nil {
			return
		}
		diff, _ := sandboxMgr.GetDiff(ctx, sb)
		state.GitDiff = diff
		if opts.OnProgress != nil && diff != "" {
			opts.OnProgress(state.CurrentStep, "git_diff", diff)
		}

		if state.IsCompleted && state.StopReason == "completed" {
			if opts.AutoMerge {
				_ = sandboxMgr.ApplyMerge(ctx, sb)
			}
		} else {
			_ = sandboxMgr.Rollback(ctx, sb)
		}
	}
	defer finalizeSandbox()

	// Khởi tạo ngữ cảnh hội thoại
	sysPrompt := r.buildSystemPrompt(opts.Workspace, opts.CustomPrompt)
	state.Messages = []domain.OpenAIMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: goal},
	}

	openAITools := r.tools.ToOpenAITools()

	for step := 1; step <= opts.MaxSteps; step++ {
		state.CurrentStep = step
		state.UpdatedAt = time.Now()

		if ctx.Err() != nil {
			state.StopReason = "interrupted"
			state.Error = ctx.Err().Error()
			return state, ctx.Err()
		}

		if opts.OnProgress != nil {
			opts.OnProgress(step, "thinking", fmt.Sprintf("Bước %d/%d: Đang phân tích và lập kế hoạch...", step, opts.MaxSteps))
		}

		// Tự động nén ngữ cảnh nếu vượt quá 12 tin nhắn (Tier 2 Recall Memory)
		if r.memorySvc != nil && len(state.Messages) > 12 {
			if compacted, err := r.memorySvc.CompactConversation(ctx, state.Messages, 12); err == nil {
				state.Messages = compacted
			}
		}

		chatReq := &domain.OpenAIChatRequest{
			Model:    opts.Model,
			Messages: state.Messages,
			Tools:    openAITools,
			Stream:   false,
		}

		resp, err := r.chatUseCase.ExecuteChatSync(ctx, chatReq)
		if err != nil {
			state.StopReason = "error"
			state.Error = fmt.Sprintf("Lỗi gọi mô hình tại bước %d: %v", step, err)
			return state, err
		}

		if len(resp.Choices) == 0 {
			state.StopReason = "error"
			state.Error = "Mô hình trả về phản hồi rỗng"
			return state, fmt.Errorf("không có phản hồi từ mô hình")
		}

		choice := resp.Choices[0]
		assistantMsg := choice.Message

		stepRecord := domain.AgentStep{
			StepIndex:        step,
			ReasoningContent: assistantMsg.ReasoningContent,
			AssistantMessage: assistantMsg.Content,
			ToolCalls:        assistantMsg.ToolCalls,
			Timestamp:        time.Now(),
		}

		if assistantMsg.ReasoningContent != "" && opts.OnProgress != nil {
			opts.OnProgress(step, "reasoning", assistantMsg.ReasoningContent)
		}

		// Nếu mô hình không gọi công cụ nào
		if len(assistantMsg.ToolCalls) == 0 {
			// Kiểm tra nếu RequireAction được bật:
			// Cần kiểm tra xem đã có thao tác sửa đổi file thực tế (replace_file_content hoặc write_file) nào được thực thi chưa
			hasModified := false
			for _, prevStep := range state.Steps {
				for _, tc := range prevStep.ToolCalls {
					if tc.Function.Name == "replace_file_content" || tc.Function.Name == "write_file" {
						hasModified = true
						break
					}
				}
				if hasModified {
					break
				}
			}

			if opts.RequireAction && !hasModified {
				state.Messages = append(state.Messages, assistantMsg)
				state.Messages = append(state.Messages, domain.OpenAIMessage{
					Role:    "user",
					Content: "[ACTION REQUIRED]: You must use 'replace_file_content' or 'write_file' to modify the code and apply the fix. Plain text explanations or only reading files without applying changes is strictly prohibited. Use tools to modify the code now.",
				})
				if opts.OnProgress != nil {
					opts.OnProgress(step, "enforce_action", "Mô hình trả về text mà chưa áp dụng sửa code -> Yêu cầu gọi công cụ sửa tệp.")
				}
				continue
			}

			// Tự động chạy compiler check / linter trước khi cho phép báo cáo hoàn tất
			summary, hasLintError := linter.CheckWorkspace(ctx, opts.Workspace)
			if hasLintError && step < opts.MaxSteps {
				state.Messages = append(state.Messages, assistantMsg)
				state.Messages = append(state.Messages, domain.OpenAIMessage{
					Role:    "user",
					Content: fmt.Sprintf("[LỖI BIÊN DỊCH / LINTER TRƯỚC KHI HOÀN TẤT]: Không thể hoàn tất vì mã nguồn còn lỗi cú pháp/biên dịch:\n%s\nHãy sử dụng các công cụ để sửa lỗi và đảm bảo mã nguồn biên dịch thành công trước khi hoàn thành.", summary),
				})
				if opts.OnProgress != nil {
					opts.OnProgress(step, "linter_fail", "Phát hiện lỗi biên dịch trước khi hoàn tất -> Yêu cầu mô hình sửa lỗi.")
				}
				continue
			}

			state.IsCompleted = true
			state.StopReason = "completed"
			state.FinalAnswer = assistantMsg.Content
			state.Steps = append(state.Steps, stepRecord)
			state.Messages = append(state.Messages, assistantMsg)

			if opts.OnProgress != nil {
				opts.OnProgress(step, "completed", "Nhiệm vụ đã hoàn thành xuất sắc!")
			}
			return state, nil
		}

		// Thêm tin nhắn của assistant vào lịch sử
		state.Messages = append(state.Messages, assistantMsg)

		// Xử lý từng lời gọi công cụ
		for _, tc := range assistantMsg.ToolCalls {
			toolName := tc.Function.Name
			toolArgs := tc.Function.Arguments

			if opts.OnProgress != nil {
				opts.OnProgress(step, "tool_start", fmt.Sprintf("Thực thi công cụ: %s (%s)", toolName, toolArgs))
			}

			if opts.OnProgress != nil {
				targetTool, exists := r.tools.GetTool(toolName)
				if exists && targetTool.Permission() == domain.PermissionDestructive && opts.Supervised {
					opts.OnProgress(step, "approval_wait", fmt.Sprintf("Chờ xác thực phê duyệt lệnh nhạy cảm: %s", toolName))
				}
			}

			// Thực thi công cụ tuyệt đối thông qua Policy Engine
			toolOutput, execErr := r.getPolicyEngine().ExecuteTool(ctx, toolName, toolArgs)
			if execErr != nil {
				if toolOutput == "" {
					toolOutput = fmt.Sprintf("LỖI THỰC THI [%s]: %v", toolName, execErr)
				}
			}

			stepRecord.ToolResults = append(stepRecord.ToolResults, toolOutput)
			state.Messages = append(state.Messages, domain.OpenAIMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    toolOutput,
			})

			if opts.OnProgress != nil {
				opts.OnProgress(step, "tool_end", fmt.Sprintf("Công cụ %s hoàn thành: %s", toolName, toolOutput))
			}
		}

		state.Steps = append(state.Steps, stepRecord)
	}

	// Hết số bước tối đa
	state.StopReason = "max_steps_reached"
	state.FinalAnswer = fmt.Sprintf("Đã đạt giới hạn %d bước thực thi tối đa mà chưa nhận được kết luận từ mô hình.", opts.MaxSteps)
	return state, nil
}
