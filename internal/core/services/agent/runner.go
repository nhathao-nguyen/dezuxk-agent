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
	chatUseCase    ports.ChatUseCase
	tools          ports.ToolRegistry
	approval       ports.ApprovalProvider
	policyEngine   ports.ToolExecutionService
	memorySvc      ports.MemoryService
	checkpointRepo ports.CheckpointRepository
}

// NewRunner khởi tạo một Agent Runner
func NewRunner(chatUseCase ports.ChatUseCase, tools ports.ToolRegistry, approval ports.ApprovalProvider) *Runner {
	return &Runner{
		chatUseCase: chatUseCase,
		tools:       tools,
		approval:    approval,
	}
}

// SetCheckpointRepository thiết lập repository lưu trữ checkpoint cho Runner
func (r *Runner) SetCheckpointRepository(cp ports.CheckpointRepository) {
	r.checkpointRepo = cp
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

	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 25
	}
	if opts.MaxToolCalls <= 0 {
		opts.MaxToolCalls = 50
	}
	if opts.MaxRepeatedCalls <= 0 {
		opts.MaxRepeatedCalls = 3
	}
	if opts.MaxExecutionDuration <= 0 {
		opts.MaxExecutionDuration = 10 * time.Minute
	}
	if opts.MaxConsecutiveFailures <= 0 {
		opts.MaxConsecutiveFailures = 5
	}

	execCtx, cancelExec := context.WithTimeout(ctx, opts.MaxExecutionDuration)
	defer cancelExec()

	if opts.Model == "" {
		opts.Model = "gemini-3.8-flash"
	}
	if opts.Workspace == "" {
		opts.Workspace = "."
	}

	execCtx = domain.WithWorkspace(execCtx, opts.Workspace)

	var startStep int = 1
	var state *domain.AgentState

	if opts.InitialState != nil {
		state = opts.InitialState
		if state.CurrentStep > 0 {
			startStep = state.CurrentStep + 1
		}
		if state.MaxSteps < opts.MaxSteps {
			state.MaxSteps = opts.MaxSteps
		}
		if opts.TaskID != "" && state.TaskID == "" {
			state.TaskID = opts.TaskID
		}
		if state.Model != "" && opts.Model == "gemini-3.8-flash" {
			opts.Model = state.Model
		}
		if state.Workspace != "" && (opts.Workspace == "." || opts.Workspace == "") {
			opts.Workspace = state.Workspace
		}
		state.UpdatedAt = time.Now()
		state.IsCompleted = false
		state.StopReason = ""
	} else {
		taskID := opts.TaskID
		if taskID == "" {
			taskID = generateTaskID()
		}
		state = &domain.AgentState{
			TaskID:      taskID,
			Goal:        goal,
			Model:       opts.Model,
			Workspace:   opts.Workspace,
			MaxSteps:    opts.MaxSteps,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			IsCompleted: false,
		}
		// Khởi tạo ngữ cảnh hội thoại
		sysPrompt := r.buildSystemPrompt(opts.Workspace, opts.CustomPrompt)
		state.Messages = []domain.OpenAIMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: goal},
		}
	}

	var sb *sandbox.Sandbox
	sandboxMgr := sandbox.GetDefaultManager()
	if opts.UseSandbox {
		createdSb, err := sandboxMgr.CreateSandbox(execCtx, state.TaskID, opts.Workspace)
		if err == nil && createdSb != nil && createdSb.IsGit {
			sb = createdSb
			opts.Workspace = sb.WorktreeWorkspace
			state.WorktreePath = sb.WorktreePath
			state.BranchName = sb.BranchName
			execCtx = domain.WithWorkspace(execCtx, opts.Workspace)
			if opts.OnProgress != nil {
				opts.OnProgress(0, "sandbox_created", fmt.Sprintf("Đã khởi tạo Git Worktree Sandbox: %s (Nhánh: %s)", sb.WorktreePath, sb.BranchName))
			}
		}
	}

	finalizeSandbox := func() {
		if sb == nil {
			return
		}
		diff, _ := sandboxMgr.GetDiff(context.Background(), sb)
		state.GitDiff = diff
		if opts.OnProgress != nil && diff != "" {
			opts.OnProgress(state.CurrentStep, "git_diff", diff)
		}

		if state.IsCompleted && state.StopReason == domain.StopReasonCompleted {
			if opts.AutoMerge {
				_ = sandboxMgr.ApplyMerge(context.Background(), sb)
			}
		} else {
			_ = sandboxMgr.Rollback(context.Background(), sb)
		}
	}
	defer finalizeSandbox()

	openAITools := r.tools.ToOpenAITools()

	var lastToolSig string
	var repeatedToolCount int
	var consecutiveFailures int

	for step := startStep; step <= opts.MaxSteps; step++ {
		state.CurrentStep = step
		state.UpdatedAt = time.Now()

		if execCtx.Err() != nil {
			if execCtx.Err() == context.DeadlineExceeded || (ctx.Err() == nil && execCtx.Err() != nil) {
				state.StopReason = domain.StopReasonTimeout
				state.Error = "Đã vượt quá thời lượng thực thi tối đa (max execution duration)"
			} else {
				state.StopReason = domain.StopReasonCancelled
				state.Error = "Tác vụ đã bị hủy bởi người dùng hoặc hệ thống"
			}
			return state, execCtx.Err()
		}

		if opts.OnProgress != nil {
			opts.OnProgress(step, "thinking", fmt.Sprintf("Bước %d/%d: Đang phân tích và lập kế hoạch...", step, opts.MaxSteps))
		}

		// Tự động nén ngữ cảnh nếu vượt quá 12 tin nhắn (Tier 2 Recall Memory)
		if r.memorySvc != nil && len(state.Messages) > 12 {
			if compacted, err := r.memorySvc.CompactConversation(execCtx, state.Messages, 12); err == nil {
				state.Messages = compacted
			}
		}

		chatReq := &domain.OpenAIChatRequest{
			Model:    opts.Model,
			Messages: state.Messages,
			Tools:    openAITools,
			Stream:   false,
		}

		resp, err := r.chatUseCase.ExecuteChatSync(execCtx, chatReq)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "circuit breaker") || strings.Contains(strings.ToLower(err.Error()), "unavailable") {
				state.StopReason = domain.StopReasonUpstreamUnavailable
			} else if execCtx.Err() == context.DeadlineExceeded {
				state.StopReason = domain.StopReasonTimeout
			} else if execCtx.Err() == context.Canceled {
				state.StopReason = domain.StopReasonCancelled
			} else {
				state.StopReason = "error"
			}
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
			summary, hasLintError := linter.CheckWorkspace(execCtx, opts.Workspace)
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
			state.StopReason = domain.StopReasonCompleted
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

			// 1. Kiểm tra vòng lặp vô hạn (Loop Detection)
			sig := fmt.Sprintf("%s:%s", toolName, strings.TrimSpace(toolArgs))
			if sig == lastToolSig {
				repeatedToolCount++
			} else {
				lastToolSig = sig
				repeatedToolCount = 1
			}

			if repeatedToolCount > opts.MaxRepeatedCalls {
				state.StopReason = domain.StopReasonRepeatedToolLoop
				state.Error = fmt.Sprintf("Phát hiện vòng lặp vô hạn: công cụ '%s' với tham số trùng lặp đã được gọi liên tiếp %d lần.", toolName, repeatedToolCount)
				state.Steps = append(state.Steps, stepRecord)
				return state, nil
			}

			// 2. Kiểm tra tổng số lượt gọi công cụ tối đa
			state.TotalToolCalls++
			if state.TotalToolCalls > opts.MaxToolCalls {
				state.StopReason = domain.StopReasonMaxToolCallsReached
				state.Error = fmt.Sprintf("Đã vượt quá giới hạn tổng số lời gọi công cụ tối đa (%d).", opts.MaxToolCalls)
				state.Steps = append(state.Steps, stepRecord)
				return state, nil
			}

			if opts.OnProgress != nil {
				opts.OnProgress(step, "tool_start", fmt.Sprintf("Thực thi công cụ: %s (%s)", toolName, toolArgs))
			}

			if opts.OnProgress != nil {
				targetTool, exists := r.tools.GetTool(toolName)
				if exists && targetTool.Permission() == domain.PermissionDestructive && opts.Supervised {
					opts.OnProgress(step, "approval_wait", fmt.Sprintf("Chờ xác thực phê duyệt lệnh nhạy cảm: %s", toolName))
				}
			}

			// Thực thi công cụ tuyệt đối thông qua Policy Engine với ngữ cảnh có thời hạn
			toolOutput, execErr := r.getPolicyEngine().ExecuteTool(execCtx, toolName, toolArgs)
			if execErr != nil {
				consecutiveFailures++
				if toolOutput == "" {
					toolOutput = fmt.Sprintf("LỖI THỰC THI [%s]: %v", toolName, execErr)
				}
				if consecutiveFailures >= opts.MaxConsecutiveFailures {
					state.StopReason = domain.StopReasonVerificationFailed
					state.Error = fmt.Sprintf("Đã vượt quá số lỗi công cụ liên tiếp cho phép (%d): %v", opts.MaxConsecutiveFailures, execErr)
					stepRecord.ToolResults = append(stepRecord.ToolResults, toolOutput)
					state.Steps = append(state.Steps, stepRecord)
					return state, nil
				}
			} else {
				consecutiveFailures = 0
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

		if r.checkpointRepo != nil {
			cp := &domain.AgentCheckpoint{
				TenantID:      identity.TenantID,
				TaskID:        state.TaskID,
				NodeKind:      domain.NodeKindExecute,
				StepIndex:     step,
				StateSnapshot: *state,
				PlanSnapshot: domain.TaskPlan{
					Goal: state.Goal,
				},
				CreatedAt: time.Now(),
			}
			_ = r.checkpointRepo.SaveCheckpoint(execCtx, cp)
			if opts.OnProgress != nil {
				opts.OnProgress(step, "checkpoint", fmt.Sprintf("Checkpoint tại bước %d đã được lưu bền vững", step))
			}
		}
	}

	// Hết số bước tối đa
	state.StopReason = domain.StopReasonMaxStepsReached
	state.FinalAnswer = fmt.Sprintf("Đã đạt giới hạn %d bước thực thi tối đa mà chưa nhận được kết luận từ mô hình.", opts.MaxSteps)
	return state, nil
}
