package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	chatUseCase        ports.ChatUseCase
	keyUseCase         ports.KeyUseCase
	tools              ports.ToolRegistry
	approval           ports.ApprovalProvider
	policyEngine       ports.ToolExecutionService
	memorySvc          ports.MemoryService
	checkpointRepo     ports.CheckpointRepository
	toolLedger         ports.ToolExecutionLedger
	runRepo            ports.AgentRunRepository
	defaultModel       string
	defaultModelPolicy string
	persona            string
	projectContext     string
	modelRegistry      *domain.ModelRegistry
	tenantSettingsRepo ports.TenantRuntimeSettingsRepository
}

// NewRunner khởi tạo một Agent Runner
func NewRunner(chatUseCase ports.ChatUseCase, tools ports.ToolRegistry, approval ports.ApprovalProvider) *Runner {
	return &Runner{
		chatUseCase: chatUseCase,
		tools:       tools,
		approval:    approval,
	}
}

// SetKeyUseCase thiết lập KeyUseCase phục vụ kiểm soát hạn ngạch và tính cước
func (r *Runner) SetKeyUseCase(k ports.KeyUseCase) {
	r.keyUseCase = k
}

// SetCheckpointRepository thiết lập repository lưu trữ checkpoint cho Runner
func (r *Runner) SetCheckpointRepository(cp ports.CheckpointRepository) {
	r.checkpointRepo = cp
}

// SetToolExecutionLedger thiết lập ledger lưu vết công cụ bền vững
func (r *Runner) SetToolExecutionLedger(ledger ports.ToolExecutionLedger) {
	r.toolLedger = ledger
}

// SetAgentRunRepository thiết lập AgentRunRepository phục vụ lease renewal và kiểm tra quyền sở hữu
func (r *Runner) SetAgentRunRepository(repo ports.AgentRunRepository) {
	r.runRepo = repo
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

func (r *Runner) SetDefaultModel(model string) {
	r.defaultModel = model
}

func (r *Runner) SetDefaultModelPolicy(policy string) {
	r.defaultModelPolicy = policy
}

func (r *Runner) SetPersona(persona string, projectContext string) {
	r.persona = persona
	r.projectContext = projectContext
}

func (r *Runner) SetModelRegistry(mr *domain.ModelRegistry) {
	r.modelRegistry = mr
}

func (r *Runner) SetTenantSettingsRepository(repo ports.TenantRuntimeSettingsRepository) {
	r.tenantSettingsRepo = repo
}

var _ ports.AgentRunner = (*Runner)(nil)

func (r *Runner) resolveDefaultModel(ctx context.Context) string {
	// 1. Ưu tiên cấu hình từ Tenant Runtime Settings
	var tenantModel string
	pref := domain.PrefBalanced
	if r.tenantSettingsRepo != nil {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && strings.TrimSpace(id.TenantID) != "" {
			if ts, err := r.tenantSettingsRepo.Get(ctx, id.TenantID); err == nil && ts != nil {
				if ts.PreferredModel != "" {
					tenantModel = ts.PreferredModel
				}
				if ts.ModelPolicy != "" {
					pref = domain.ModelPreference(ts.ModelPolicy)
				}
			}
		}
	}

	if tenantModel != "" {
		return tenantModel
	}

	if r.defaultModel != "" {
		return r.defaultModel
	}

	if r.defaultModelPolicy != "" {
		pref = domain.ModelPreference(r.defaultModelPolicy)
	}

	// 2. Tra cứu động từ ModelRegistry dựa trên Capability Chat và Preference
	if r.modelRegistry != nil {
		if desc, ok := r.modelRegistry.ResolveModel("", domain.ModelRequirement{
			Capability: domain.CapChat,
			Preference: pref,
		}); ok && desc.ID != "" {
			return desc.ID
		}
	}

	return ""
}

func (r *Runner) buildSystemPrompt(ctx context.Context, workspace string, customPrompt string) string {
	absWorkspace, _ := filepath.Abs(workspace)
	nowStr := time.Now().Format("2006-01-02 15:04:05 MST")

	var prompt string
	var projectContext string

	// 1. Kiểm tra override từ tenant runtime settings
	if r.tenantSettingsRepo != nil {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && strings.TrimSpace(id.TenantID) != "" {
			if ts, err := r.tenantSettingsRepo.Get(ctx, id.TenantID); err == nil && ts != nil {
				if ts.Persona != "" {
					prompt = ts.Persona
				}
				if ts.ProjectContext != "" {
					projectContext = ts.ProjectContext
				}
			}
		}
	}

	// 2. Dùng persona và project context cấp hệ thống nếu tenant chưa cấu hình
	if prompt == "" && r.persona != "" {
		prompt = r.persona
	}
	if projectContext == "" && r.projectContext != "" {
		projectContext = r.projectContext
	}

	// 3. Nếu request chỉ định customPrompt trực tiếp thì ưu tiên
	if strings.TrimSpace(customPrompt) != "" {
		prompt = strings.TrimSpace(customPrompt)
	}

	// 4. Nếu vẫn trống, dùng generic persona chuẩn tối thiểu
	if prompt == "" {
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
	} else {
		prompt = fmt.Sprintf("%s\n\nOperating System: %s\nCurrent Time: %s\nWorking Directory: %s",
			prompt, runtime.GOOS, nowStr, absWorkspace)
	}

	if projectContext != "" {
		prompt = prompt + "\n\n## Project Context\n" + projectContext
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
		opts.Model = r.resolveDefaultModel(execCtx)
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
		if state.Model != "" {
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
		sysPrompt := r.buildSystemPrompt(execCtx, opts.Workspace, opts.CustomPrompt)
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

	var openAITools []domain.OpenAITool
	if r.tools != nil {
		openAITools = r.tools.ToOpenAITools()
	}

	var lastToolSig string
	var repeatedToolCount int
	var consecutiveFailures int

	for step := startStep; step <= opts.MaxSteps; step++ {
		state.CurrentStep = step
		state.UpdatedAt = time.Now()

		if os.Getenv("DEZUXK_TEST_MODE") == "true" && strings.Contains(strings.ToLower(goal), "sleep") {
			if r.checkpointRepo != nil && step == 1 {
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
			}
			if step == 1 {
				if opts.OnProgress != nil {
					opts.OnProgress(step, "running", "Executing controlled sleep test task")
				}
				select {
				case <-time.After(3 * time.Second):
				case <-execCtx.Done():
					return state, execCtx.Err()
				}
			}
		}

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

		// Kiểm tra phân quyền và hạn ngạch của khóa API trước mỗi lượt gọi mô hình (Issue 4)
		if r.keyUseCase != nil {
			keyID := ""
			if bill, ok := domain.BillingIdentityFromContext(execCtx); ok && bill.KeyID != "" {
				keyID = bill.KeyID
			} else if id, ok := domain.TenantIdentityFromContext(execCtx); ok && id.KeyID != "" {
				keyID = id.KeyID
			}

			if keyID != "" && keyID != "master" && keyID != "internal" {
				vKey, valErr := r.keyUseCase.ValidateKeyByID(execCtx, keyID, opts.Model)
				if valErr != nil {
					switch {
					case errors.Is(valErr, domain.ErrRateLimitRPMExceeded):
						state.StopReason = domain.StopReasonRateLimited
						state.Error = fmt.Sprintf("Tần suất yêu cầu vượt quá giới hạn RPM: %v", valErr)
					case errors.Is(valErr, domain.ErrKeyRevoked), errors.Is(valErr, domain.ErrKeyExpired):
						state.StopReason = domain.StopReasonAuthorizationRevoked
						state.Error = fmt.Sprintf("Quyền thực thi bị thu hồi hoặc khóa đã hết hạn: %v", valErr)
					case errors.Is(valErr, domain.ErrDailyQuotaExceeded), errors.Is(valErr, domain.ErrTokenQuotaExceeded):
						state.StopReason = domain.StopReasonQuotaExceeded
						state.Error = fmt.Sprintf("Hạn ngạch API đã cạn kiệt: %v", valErr)
					case errors.Is(valErr, domain.ErrModelNotAllowed):
						state.StopReason = domain.StopReasonAuthorizationRevoked
						state.Error = fmt.Sprintf("Mô hình không được phép: %v", valErr)
					default:
						state.StopReason = domain.StopReasonAuthorizationRevoked
						state.Error = fmt.Sprintf("Lỗi xác thực khóa API: %v", valErr)
					}
					return state, valErr
				}
				_ = vKey

				// Enforce: 1 Agent LLM round = 1 billable request (tiêu thụ 1 lượt hạn ngạch ngày)
				if _, consumeErr := r.keyUseCase.ConsumeQuota(execCtx, keyID); consumeErr != nil {
					if errors.Is(consumeErr, domain.ErrDailyQuotaExceeded) {
						state.StopReason = domain.StopReasonQuotaExceeded
						state.Error = "Đã sử dụng hết hạn ngạch yêu cầu trong ngày của khóa API."
						return state, consumeErr
					}
				}
			}
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
		if err == nil && chatReq.Model != "" {
			state.Model = chatReq.Model
		}
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

			// Kiểm tra ledger trước khi thực thi công cụ (Issue 5: Tool side-effect replay safety)
			if r.toolLedger != nil && state.TaskID != "" && tc.ID != "" {
				existing, gErr := r.toolLedger.GetExecution(execCtx, identity.TenantID, state.TaskID, tc.ID)
				if gErr == nil && existing != nil {
					switch existing.Status {
					case domain.ToolExecutionSucceeded:
						// 1. Công cụ đã thực thi thành công từ trước crash -> TÁI SỬ DỤNG kết quả, TUYỆT ĐỐI không chạy lại
						toolOutput := existing.ResultJSON
						stepRecord.ToolResults = append(stepRecord.ToolResults, toolOutput)
						state.Messages = append(state.Messages, domain.OpenAIMessage{
							Role:       "tool",
							ToolCallID: tc.ID,
							Content:    toolOutput,
						})
						if opts.OnProgress != nil {
							opts.OnProgress(step, "tool_cached", fmt.Sprintf("Tái sử dụng kết quả công cụ %s từ ledger bền vững", toolName))
						}
						continue

					case domain.ToolExecutionRunning:
						// 2. Công cụ đang chạy dở khi server crash
						var targetTool domain.AgentTool
						if r.tools != nil {
							targetTool, _ = r.tools.GetTool(toolName)
						}
						sem := domain.ResolveToolSemantics(targetTool)
						if !sem.ReadOnly && !sem.Idempotent {
							// Công cụ destructive / side-effect -> KHÔNG tự động replay!
							_ = r.toolLedger.MarkUnknownAfterRestart(execCtx, identity.TenantID, state.TaskID, tc.ID)
							state.StopReason = domain.StopReasonVerificationFailed
							state.Error = fmt.Sprintf("Công cụ nhạy cảm [%s] đang thực thi khi hệ thống tắt/khởi động lại; từ chối tự động replay side-effect để đảm bảo an toàn", toolName)
							stepRecord.ToolResults = append(stepRecord.ToolResults, state.Error)
							state.Steps = append(state.Steps, stepRecord)
							return state, nil
						}
						// Safe / Read-Only / Idempotent tool: cho phép thử lại
					case domain.ToolExecutionUnknownAfterRestart:
						state.StopReason = domain.StopReasonVerificationFailed
						state.Error = fmt.Sprintf("Công cụ nhạy cảm [%s] ở trạng thái unknown_after_restart", toolName)
						stepRecord.ToolResults = append(stepRecord.ToolResults, state.Error)
						state.Steps = append(state.Steps, stepRecord)
						return state, nil
					}
				}

				var targetTool domain.AgentTool
				if r.tools != nil {
					targetTool, _ = r.tools.GetTool(toolName)
				}
				sem := domain.ResolveToolSemantics(targetTool)

				now := time.Now()
				ownership, hasOwnership := domain.ExecutionOwnershipFromContext(execCtx)
				if hasOwnership {
					if !sem.ReadOnly && !sem.Idempotent && r.runRepo != nil {
						// Gia hạn lease trước công cụ destructive để tránh hết hạn giữa chừng
						_, _ = r.runRepo.RenewLease(execCtx, state.TaskID, ownership.WorkerID, ownership.ClaimGeneration, 30*time.Second)
					}
					ok, err := r.toolLedger.RecordPlannedOrRunningOwned(execCtx, &domain.ToolExecutionRecord{
						TenantID:        identity.TenantID,
						RunID:           state.TaskID,
						ToolCallID:      tc.ID,
						ToolName:        toolName,
						ArgsHash:        domain.HashKey(toolArgs),
						Status:          domain.ToolExecutionRunning,
						WorkerID:        ownership.WorkerID,
						ClaimGeneration: ownership.ClaimGeneration,
						StartedAt:       &now,
					}, ownership.WorkerID, ownership.ClaimGeneration)
					if err != nil || !ok {
						state.StopReason = domain.StopReasonVerificationFailed
						state.Error = fmt.Sprintf("Mất quyền sở hữu tác vụ (worker=%s, claim_gen=%d); từ chối thực thi công cụ [%s] để bảo vệ hệ thống",
							ownership.WorkerID, ownership.ClaimGeneration, toolName)
						stepRecord.ToolResults = append(stepRecord.ToolResults, state.Error)
						state.Steps = append(state.Steps, stepRecord)
						return state, nil
					}
				} else {
					_ = r.toolLedger.RecordPlannedOrRunning(execCtx, &domain.ToolExecutionRecord{
						TenantID:   identity.TenantID,
						RunID:      state.TaskID,
						ToolCallID: tc.ID,
						ToolName:   toolName,
						ArgsHash:   domain.HashKey(toolArgs),
						Status:     domain.ToolExecutionRunning,
						StartedAt:  &now,
					})
				}
			}

			// Thực thi công cụ tuyệt đối thông qua Policy Engine với ngữ cảnh có thời hạn
			toolOutput, execErr := r.getPolicyEngine().ExecuteTool(execCtx, toolName, toolArgs)
			if r.toolLedger != nil && state.TaskID != "" && tc.ID != "" {
				targetStatus := domain.ToolExecutionSucceeded
				errStr := ""
				if execErr != nil {
					targetStatus = domain.ToolExecutionFailed
					errStr = execErr.Error()
				}

				ownership, hasOwnership := domain.ExecutionOwnershipFromContext(execCtx)
				if hasOwnership {
					ok, err := r.toolLedger.RecordFinishedOwned(execCtx, identity.TenantID, state.TaskID, tc.ID, targetStatus, toolOutput, errStr, ownership.WorkerID, ownership.ClaimGeneration)
					if err != nil || !ok {
						// Mất quyền sở hữu trong quá trình gọi tool! Discard kết quả, dừng worker ngay lập tức
						state.StopReason = domain.StopReasonVerificationFailed
						state.Error = fmt.Sprintf("Mất quyền sở hữu tác vụ (worker=%s, claim_gen=%d) trong khi thực thi công cụ [%s]; hủy bỏ kết quả",
							ownership.WorkerID, ownership.ClaimGeneration, toolName)
						return state, nil
					}
				} else {
					_ = r.toolLedger.RecordFinished(execCtx, identity.TenantID, state.TaskID, tc.ID, targetStatus, toolOutput, errStr)
				}
			}
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
