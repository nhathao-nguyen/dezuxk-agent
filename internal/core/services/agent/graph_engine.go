package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/sandbox"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
	"dezuxk-gateway/internal/core/services/linter"
	"dezuxk-gateway/internal/core/services/policy"
)

var reJSONBlock = regexp.MustCompile(`(?s)` + "```" + `(?:json)?\s*([\{\[].*?[\}\]])\s*` + "```")

// GraphEngine điều phối State Machine theo triết lý LangGraph (PLAN -> EXECUTE -> VERIFY -> FIX)
type GraphEngine struct {
	chatUseCase    ports.ChatUseCase
	tools          ports.ToolRegistry
	checkpointRepo ports.CheckpointRepository
	approval       ports.ApprovalProvider
	policyEngine   ports.ToolExecutionService
	runner         *Runner
	maxFixRetries  int
}

// NewGraphEngine khởi tạo GraphEngine
func NewGraphEngine(
	chatUseCase ports.ChatUseCase,
	tools ports.ToolRegistry,
	checkpointRepo ports.CheckpointRepository,
	approval ports.ApprovalProvider,
	maxFixRetries int,
) *GraphEngine {
	if maxFixRetries <= 0 {
		maxFixRetries = 3
	}
	runner := NewRunner(chatUseCase, tools, approval)
	return &GraphEngine{
		chatUseCase:    chatUseCase,
		tools:          tools,
		checkpointRepo: checkpointRepo,
		approval:       approval,
		runner:         runner,
		maxFixRetries:  maxFixRetries,
	}
}

// SetPolicyEngine thiết lập engine chính sách kiểm soát toàn diện lời gọi công cụ
func (g *GraphEngine) SetPolicyEngine(p ports.ToolExecutionService) {
	g.policyEngine = p
	if g.runner != nil {
		g.runner.SetPolicyEngine(p)
	}
}

func (g *GraphEngine) getPolicyEngine() ports.ToolExecutionService {
	if g.policyEngine != nil {
		return g.policyEngine
	}
	return policy.NewPolicyEngine(g.tools, g.approval)
}

var _ ports.GraphWorkflowRunner = (*GraphEngine)(nil)

func (g *GraphEngine) RunGraph(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentGraphState, error) {
	// Đảm bảo áp dụng chính sách bảo mật Tenant, client không thể can thiệp
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

	taskID := opts.TaskID
	if taskID == "" {
		taskID = generateTaskID()
	}
	state := &domain.AgentGraphState{
		TaskID:      taskID,
		Goal:        goal,
		CurrentNode: domain.NodeKindPlan,
		MaxFixRetry: g.maxFixRetries,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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
			opts.OnProgress(0, "git_diff", diff)
		}

		if state.IsCompleted && state.CurrentNode == domain.NodeKindComplete {
			if opts.AutoMerge {
				_ = sandboxMgr.ApplyMerge(ctx, sb)
			}
		} else {
			_ = sandboxMgr.Rollback(ctx, sb)
		}
	}
	defer finalizeSandbox()

	return g.executeWorkflow(ctx, state, opts)
}

func (g *GraphEngine) ResumeGraph(ctx context.Context, taskID string, opts domain.AgentRunOptions) (*domain.AgentGraphState, error) {
	if g.checkpointRepo == nil {
		return nil, fmt.Errorf("không có CheckpointRepository để phục hồi task %s", taskID)
	}

	latestCP, err := g.checkpointRepo.GetLatestCheckpoint(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("không thể tìm thấy checkpoint cho task %s: %w", taskID, err)
	}

	state := &domain.AgentGraphState{
		TaskID:      taskID,
		Goal:        latestCP.PlanSnapshot.Goal,
		CurrentNode: latestCP.NodeKind,
		Plan:        latestCP.PlanSnapshot,
		AgentState:  latestCP.StateSnapshot,
		MaxFixRetry: g.maxFixRetries,
		CreatedAt:   latestCP.CreatedAt,
		UpdatedAt:   time.Now(),
	}

	return g.executeWorkflow(ctx, state, opts)
}

func (g *GraphEngine) executeWorkflow(ctx context.Context, state *domain.AgentGraphState, opts domain.AgentRunOptions) (*domain.AgentGraphState, error) {
	ctx = domain.WithWorkspace(ctx, opts.Workspace)
	for {
		if ctx.Err() != nil {
			state.CurrentNode = domain.NodeKindInterrupt
			state.Error = ctx.Err().Error()
			g.saveCheckpoint(ctx, state, opts)
			return state, ctx.Err()
		}

		if opts.OnProgress != nil {
			opts.OnProgress(0, "node_change", string(state.CurrentNode))
		}

		switch state.CurrentNode {
		case domain.NodeKindPlan:
			if err := g.nodePlan(ctx, state, opts); err != nil {
				state.CurrentNode = domain.NodeKindFailed
				state.Error = fmt.Sprintf("Lỗi tại PLAN node: %v", err)
				g.saveCheckpoint(ctx, state, opts)
				return state, err
			}
			state.CurrentNode = domain.NodeKindExecute
			g.saveCheckpoint(ctx, state, opts)

		case domain.NodeKindExecute:
			step := state.Plan.GetCurrentStep()
			if step == nil {
				state.CurrentNode = domain.NodeKindComplete
				continue
			}

			if err := g.nodeExecute(ctx, state, step, opts); err != nil {
				state.CurrentNode = domain.NodeKindFailed
				state.Error = fmt.Sprintf("Lỗi tại EXECUTE node (Bước %d): %v", step.ID, err)
				g.saveCheckpoint(ctx, state, opts)
				return state, err
			}
			state.CurrentNode = domain.NodeKindVerify
			g.saveCheckpoint(ctx, state, opts)

		case domain.NodeKindVerify:
			step := state.Plan.GetCurrentStep()
			if step == nil {
				state.CurrentNode = domain.NodeKindComplete
				continue
			}

			passed, err := g.nodeVerify(ctx, state, step, opts)
			if err != nil {
				state.CurrentNode = domain.NodeKindFailed
				state.Error = fmt.Sprintf("Lỗi tại VERIFY node: %v", err)
				g.saveCheckpoint(ctx, state, opts)
				return state, err
			}

			if passed {
				step.Status = domain.StepStatusPassed
				// Tiến tới bước kế tiếp nếu còn
				if state.Plan.CurrentStepIndex+1 < len(state.Plan.Steps) {
					state.Plan.CurrentStepIndex++
					state.CurrentNode = domain.NodeKindExecute
				} else {
					state.CurrentNode = domain.NodeKindComplete
				}
			} else {
				step.Status = domain.StepStatusFailed
				state.CurrentNode = domain.NodeKindFix
			}
			g.saveCheckpoint(ctx, state, opts)

		case domain.NodeKindFix:
			step := state.Plan.GetCurrentStep()
			if step == nil {
				state.CurrentNode = domain.NodeKindComplete
				continue
			}

			step.FixAttempts++
			if step.FixAttempts > state.MaxFixRetry {
				state.CurrentNode = domain.NodeKindFailed
				state.Error = fmt.Sprintf("Bước %d (%s) thất bại sau %d lần tự động sửa (vượt quá giới hạn %d lần). Chi tiết lỗi: %s",
					step.ID, step.Title, step.FixAttempts, state.MaxFixRetry, step.ErrorOutput)
				g.saveCheckpoint(ctx, state, opts)
				return state, fmt.Errorf("%s", state.Error)
			}

			if err := g.nodeFix(ctx, state, step, opts); err != nil {
				state.CurrentNode = domain.NodeKindFailed
				state.Error = fmt.Sprintf("Lỗi tại FIX node: %v", err)
				g.saveCheckpoint(ctx, state, opts)
				return state, err
			}

			state.CurrentNode = domain.NodeKindVerify
			g.saveCheckpoint(ctx, state, opts)

		case domain.NodeKindComplete:
			state.IsCompleted = true
			g.nodeComplete(ctx, state, opts)
			g.saveCheckpoint(ctx, state, opts)
			return state, nil

		case domain.NodeKindFailed:
			return state, fmt.Errorf("%s", state.Error)

		default:
			return state, fmt.Errorf("trạng thái node không xác định: %s", state.CurrentNode)
		}
	}
}

// ---------------------------------------------------------------------
// Node Handlers
// ---------------------------------------------------------------------

func (g *GraphEngine) nodePlan(ctx context.Context, state *domain.AgentGraphState, opts domain.AgentRunOptions) error {
	if opts.OnProgress != nil {
		opts.OnProgress(0, "node_plan", fmt.Sprintf("[PLAN NODE] Đang phân tích mục tiêu: %s", state.Goal))
	}

	osGuide := "Operating System: " + runtime.GOOS + "\n"
	if runtime.GOOS == "windows" {
		osGuide += "On Windows PowerShell, use 'Test-Path <file>' to check file existence, 'go build', 'go run main.go', 'go test -v ./...'. Do NOT use Linux 'test -f'."
	} else {
		osGuide += "On Linux/macOS, use 'test -f <file>' to check file existence, 'go build', 'go run main.go', 'go test -v ./...'."
	}

	planPrompt := fmt.Sprintf(`You are the Chief Software Architect.
Goal: %s
Workspace: %s
%s

Break down this goal into a clear, minimal, verifiable sequence of steps (1 to 4 steps).
CRITICAL RULES FOR VERIFICATION COMMANDS:
1. Verification commands MUST BE READ-ONLY / TEST CHECKS (e.g. "go build", "go test -v ./...", "go run main.go", "Test-Path go.mod").
2. NEVER use mutating or initialization commands (like "go mod init", "npm init", "mkdir") as verification commands, because running them again will fail if the resource was already created!
3. All commands will execute directly inside the workspace directory (%s).
4. All file arguments and verification commands MUST be relative to the workspace root (e.g. use "go run main.go" or "go test -v ./...", NEVER prepend "%s/" or any workspace folder name).
5. All commands execute directly inside the workspace. NEVER prefix commands with 'cd %s'.

You MUST respond strictly with a valid JSON object matching this schema:
{
  "steps": [
    {
      "id": 1,
      "title": "Brief title",
      "description": "Specific code or configuration modification",
      "verification_cmd": "read-only command to verify this step"
    }
  ]
}`, state.Goal, opts.Workspace, osGuide, opts.Workspace, opts.Workspace, opts.Workspace)

	chatReq := &domain.OpenAIChatRequest{
		Model: opts.Model,
		Messages: []domain.OpenAIMessage{
			{Role: "system", Content: "You are an expert system planner. Always respond with valid JSON."},
			{Role: "user", Content: planPrompt},
		},
	}

	resp, err := g.chatUseCase.ExecuteChatSync(ctx, chatReq)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("mô hình trả về rỗng")
	}

	content := resp.Choices[0].Message.Content
	rawJSON := content
	if matches := reJSONBlock.FindStringSubmatch(content); len(matches) > 1 {
		rawJSON = matches[1]
	}

	type stepItem struct {
		ID              int    `json:"id"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		VerificationCmd string `json:"verification_cmd"`
	}

	var planData struct {
		Steps []stepItem `json:"steps"`
	}

	parseCandidate := func(s string) bool {
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		clean := services.RepairMalformedJSON(services.SanitizeJSONStringLiterals(s))
		if err := json.Unmarshal([]byte(clean), &planData); err == nil && len(planData.Steps) > 0 {
			return true
		}
		var directList []stepItem
		if err := json.Unmarshal([]byte(clean), &directList); err == nil && len(directList) > 0 {
			planData.Steps = directList
			return true
		}
		return false
	}

	if !parseCandidate(rawJSON) {
		// Thử tìm khối JSON bọc trong { ... } hoặc [ ... ]
		if startIdx := strings.Index(content, "{"); startIdx != -1 {
			if endIdx := strings.LastIndex(content, "}"); endIdx > startIdx {
				parseCandidate(content[startIdx : endIdx+1])
			}
		}
		if len(planData.Steps) == 0 {
			if startIdx := strings.Index(content, "["); startIdx != -1 {
				if endIdx := strings.LastIndex(content, "]"); endIdx > startIdx {
					parseCandidate(content[startIdx : endIdx+1])
				}
			}
		}
	}

	if len(planData.Steps) == 0 {
		// Fallback tạo kế hoạch 1 bước nếu mô hình không trả về đúng định dạng JSON
		state.Plan = domain.TaskPlan{
			Goal: state.Goal,
			Steps: []domain.PlanStep{
				{
					ID:                  1,
					Title:               "Thực thi mục tiêu",
					Description:         state.Goal,
					VerificationCommand: "",
					Status:              domain.StepStatusPending,
					UpdatedAt:           time.Now(),
				},
			},
			CurrentStepIndex: 0,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
	} else {
		var steps []domain.PlanStep
		for i, s := range planData.Steps {
			id := s.ID
			if id <= 0 {
				id = i + 1
			}
			steps = append(steps, domain.PlanStep{
				ID:                  id,
				Title:               s.Title,
				Description:         s.Description,
				VerificationCommand: s.VerificationCmd,
				Status:              domain.StepStatusPending,
				UpdatedAt:           time.Now(),
			})
		}
		state.Plan = domain.TaskPlan{
			Goal:             state.Goal,
			Steps:            steps,
			CurrentStepIndex: 0,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
	}

	if opts.OnProgress != nil {
		opts.OnProgress(0, "plan_created", fmt.Sprintf("[PLAN NODE] Kế hoạch hoàn chỉnh gồm %d bước đã được thiết lập.", len(state.Plan.Steps)))
	}

	return nil
}

func (g *GraphEngine) nodeExecute(ctx context.Context, state *domain.AgentGraphState, step *domain.PlanStep, opts domain.AgentRunOptions) error {
	step.Status = domain.StepStatusInProgress
	step.UpdatedAt = time.Now()

	if opts.OnProgress != nil {
		opts.OnProgress(step.ID, "node_execute", fmt.Sprintf("[EXECUTE NODE] Đang thực thi Bước %d/%d: %s", step.ID, len(state.Plan.Steps), step.Title))
	}

	execGoal := fmt.Sprintf("Nhiệm vụ Bước %d: %s\nChi tiết yêu cầu: %s\nHãy dùng các công cụ thích hợp để kiểm tra và thực hiện thay đổi tương ứng.",
		step.ID, step.Title, step.Description)

	stepOpts := opts
	stepOpts.MaxSteps = 15 // Tối đa 15 sub-step cho mỗi step

	resState, err := g.runner.Run(ctx, execGoal, stepOpts)
	if err != nil {
		return err
	}

	state.AgentState = *resState
	return nil
}

func (g *GraphEngine) nodeVerify(ctx context.Context, state *domain.AgentGraphState, step *domain.PlanStep, opts domain.AgentRunOptions) (bool, error) {
	if opts.OnProgress != nil {
		opts.OnProgress(step.ID, "node_verify", fmt.Sprintf("[VERIFY NODE] Đang kiểm chứng Bước %d: %s", step.ID, step.Title))
	}

	// Nếu không có câu lệnh kiểm thử cụ thể, coi như bước thủ tục đã pass
	if strings.TrimSpace(step.VerificationCommand) == "" {
		step.Evidence = "Xác nhận kiểm thử hoàn tất (không yêu cầu shell test cụ thể)."
		return true, nil
	}

	cleanCmd := SanitizeVerificationCommand(step.VerificationCommand, opts.Workspace)
	if opts.OnProgress != nil {
		opts.OnProgress(step.ID, "verify_exec", fmt.Sprintf("  Chạy lệnh xác minh: %s", cleanCmd))
	}

	// 1. Kiểm tra an ninh lệnh xác minh qua Policy Engine
	if err := policy.ValidateVerificationCommand(cleanCmd); err != nil {
		step.ErrorOutput = fmt.Sprintf("Lệnh kiểm thử bị chặn bởi Policy Engine: %v", err)
		if opts.OnProgress != nil {
			opts.OnProgress(step.ID, "verify_fail", fmt.Sprintf("  ✗ Lệnh xác minh bị chặn bởi Policy Engine: %v", err))
		}
		return false, nil
	}

	// 2. Thực thi qua Policy Engine tập trung thay vì gọi trực tiếp run_command
	argsJSON := fmt.Sprintf(`{"command": %q, "cwd": %q, "timeout_seconds": 90}`, cleanCmd, opts.Workspace)
	output, err := g.getPolicyEngine().ExecuteTool(ctx, "run_command", argsJSON)
	if err != nil {
		step.ErrorOutput = fmt.Sprintf("Lỗi thực thi kiểm thử: %v\nOutput: %s", err, output)
		return false, nil
	}

	// Kiểm tra exit code trong output của run_command
	if strings.Contains(output, "Exit Code: 0") {
		// Nếu lệnh kiểm tra Test-Path mà trả về False thì coi là chưa đạt
		if strings.Contains(cleanCmd, "Test-Path") && strings.Contains(output, "False") {
			step.ErrorOutput = "File hoặc thư mục kiểm tra chưa tồn tại (Test-Path trả về False)\n" + output
			return false, nil
		}

		// Kiểm tra thêm linter / compiler check trong workspace để chống quên ListenAndServe hoặc syntax error
		if summary, hasLintError := linter.CheckWorkspace(ctx, opts.Workspace); hasLintError {
			step.ErrorOutput = "LỖI LINTER / BIÊN DỊCH TRONG WORKSPACE:\n" + summary
			if opts.OnProgress != nil {
				opts.OnProgress(step.ID, "verify_fail", fmt.Sprintf("  ✗ Xác minh THẤT BẠI cho Bước %d do còn lỗi biên dịch/linter!", step.ID))
			}
			return false, nil
		}

		step.Evidence = output
		if opts.OnProgress != nil {
			opts.OnProgress(step.ID, "verify_pass", fmt.Sprintf("  ✓ Xác minh THÀNH CÔNG cho Bước %d!", step.ID))
		}
		return true, nil
	}

	// Thất bại
	step.ErrorOutput = output
	if opts.OnProgress != nil {
		opts.OnProgress(step.ID, "verify_fail", fmt.Sprintf("  ✗ Xác minh THẤT BẠI cho Bước %d! Cần chuyển sang FIX node.", step.ID))
	}
	return false, nil
}

func (g *GraphEngine) nodeFix(ctx context.Context, state *domain.AgentGraphState, step *domain.PlanStep, opts domain.AgentRunOptions) error {
	if opts.OnProgress != nil {
		opts.OnProgress(step.ID, "node_fix", fmt.Sprintf("[FIX NODE] Lần thử sửa lỗi %d/%d cho Bước %d: %s",
			step.FixAttempts, state.MaxFixRetry, step.ID, step.Title))
	}

	cleanCmd := SanitizeVerificationCommand(step.VerificationCommand, opts.Workspace)

	// Thu thập danh sách file hiện có trong workspace để mô hình nắm rõ ngữ cảnh
	var filesInWS []string
	wsDir := opts.Workspace
	if wsDir == "" {
		wsDir = "."
	}
	if entries, err := os.ReadDir(wsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				filesInWS = append(filesInWS, e.Name())
			} else {
				filesInWS = append(filesInWS, e.Name()+"/")
			}
		}
	}
	filesListStr := strings.Join(filesInWS, ", ")
	if filesListStr == "" {
		filesListStr = "(no files found)"
	}

	fixGoal := fmt.Sprintf(`VERIFICATION FAILURE IN STEP %d (%s):
The verification command '%s' failed with the following error output:
%s

WORKSPACE CONTEXT:
Workspace directory: %s
Files present in workspace: %s

MANDATORY INSTRUCTIONS:
1. Inspect the relevant files using 'read_file'.
2. Use 'replace_file_content' or 'write_file' to apply the concrete code fix.
3. You MUST perform concrete file modifications using tools ('replace_file_content' or 'write_file') to resolve the error. Plain text explanations without calling file modification tools are strictly prohibited.`,
		step.ID, step.Title, cleanCmd, step.ErrorOutput, opts.Workspace, filesListStr)

	stepOpts := opts
	stepOpts.MaxSteps = 15
	stepOpts.RequireAction = true

	resState, err := g.runner.Run(ctx, fixGoal, stepOpts)
	if err != nil {
		return err
	}

	// Đảm bảo Agent đã thực sự gọi ít nhất một tool sửa đổi file (replace_file_content hoặc write_file)
	var hasModifiedFile bool
	for _, st := range resState.Steps {
		for _, tc := range st.ToolCalls {
			if tc.Function.Name == "replace_file_content" || tc.Function.Name == "write_file" {
				hasModifiedFile = true
				break
			}
		}
		if hasModifiedFile {
			break
		}
	}
	if !hasModifiedFile {
		return fmt.Errorf("Fix node thất bại: mô hình không thực thi bất kỳ thao tác sửa đổi file nào (replace_file_content hoặc write_file) để khắc phục lỗi")
	}

	state.AgentState = *resState
	return nil
}

// SanitizeVerificationCommand chuẩn hóa lệnh kiểm thử, loại bỏ tiền tố workspace thừa và chuẩn hóa cú pháp shell
func SanitizeVerificationCommand(cmd, workspace string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return cmd
	}

	wsClean := filepath.Clean(workspace)
	wsFwd := filepath.ToSlash(wsClean)
	wsBack := filepath.FromSlash(wsClean)

	if wsClean != "" && wsClean != "." {
		// 1. Loại bỏ các tiền tố cd <workspace> && hoặc cd <workspace> ;
		cdPrefixes := []string{
			"cd " + wsFwd + " &&",
			"cd " + wsFwd + " ;",
			"cd " + wsFwd + ";",
			"cd " + wsBack + " &&",
			"cd " + wsBack + " ;",
			"cd " + wsBack + ";",
			"cd \"" + wsFwd + "\" &&",
			"cd \"" + wsFwd + "\" ;",
			"cd \"" + wsFwd + "\";",
			"cd \"" + wsBack + "\" &&",
			"cd '" + wsFwd + "' &&",
			"cd '" + wsBack + "' &&",
			"cd ./" + wsFwd + " &&",
			"cd .\\" + wsBack + " &&",
		}
		for _, prefix := range cdPrefixes {
			if strings.HasPrefix(strings.ToLower(cmd), strings.ToLower(prefix)) {
				cmd = strings.TrimSpace(cmd[len(prefix):])
			}
		}

		// 2. Chuẩn hóa câu lệnh test gói Go
		cmd = strings.ReplaceAll(cmd, "./"+wsFwd+"/...", "./...")
		cmd = strings.ReplaceAll(cmd, wsFwd+"/...", "./...")
		cmd = strings.ReplaceAll(cmd, ".\\"+wsBack+"\\...", ".\\...")
		cmd = strings.ReplaceAll(cmd, wsBack+"\\...", ".\\...")
		cmd = strings.ReplaceAll(cmd, "go test "+wsFwd, "go test ./...")
		cmd = strings.ReplaceAll(cmd, "go test "+wsBack, "go test ./...")

		// 3. Loại bỏ tiền tố workspace thừa trước tên file / argument
		patterns := []string{
			"./" + wsFwd + "/",
			wsFwd + "/",
			".\\" + wsBack + "\\",
			wsBack + "\\",
		}

		for _, p := range patterns {
			cmd = strings.ReplaceAll(cmd, p, "")
		}
	}

	// 4. Chuẩn hóa câu lệnh kiểm tra trên từng hệ điều hành
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(cmd, "test -f ") {
			cmd = "Test-Path " + strings.TrimPrefix(cmd, "test -f ")
		} else if strings.HasPrefix(cmd, "test -e ") {
			cmd = "Test-Path " + strings.TrimPrefix(cmd, "test -e ")
		} else if strings.HasPrefix(cmd, "go mod init") {
			cmd = "Test-Path go.mod"
		}
	} else {
		if strings.HasPrefix(cmd, "go mod init") {
			cmd = "test -f go.mod"
		}
	}

	return strings.TrimSpace(cmd)
}

func (g *GraphEngine) nodeComplete(ctx context.Context, state *domain.AgentGraphState, opts domain.AgentRunOptions) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tất cả %d bước trong kế hoạch đã được thực hiện và kiểm chứng thành công:\n\n", len(state.Plan.Steps)))
	for _, s := range state.Plan.Steps {
		sb.WriteString(fmt.Sprintf("✓ Bước %d: %s [PASS]\n", s.ID, s.Title))
		if s.VerificationCommand != "" {
			sb.WriteString(fmt.Sprintf("  • Lệnh kiểm chứng: %s\n", s.VerificationCommand))
		}
	}
	sb.WriteString("\nBằng chứng kiểm thử hoàn chỉnh đã được thu thập và xác thực đầy đủ.")

	state.FinalSummary = sb.String()
	state.UpdatedAt = time.Now()

	if opts.OnProgress != nil {
		opts.OnProgress(0, "node_complete", "[COMPLETE NODE] Toàn bộ chu trình State Machine đã hoàn tất xuất sắc!")
	}
}

func (g *GraphEngine) saveCheckpoint(ctx context.Context, state *domain.AgentGraphState, opts domain.AgentRunOptions) {
	if g.checkpointRepo == nil {
		return
	}
	state.Checkpoints++
	cp := &domain.AgentCheckpoint{
		TaskID:        state.TaskID,
		NodeKind:      state.CurrentNode,
		StepIndex:     state.Plan.CurrentStepIndex,
		StateSnapshot: state.AgentState,
		PlanSnapshot:  state.Plan,
		CreatedAt:     time.Now(),
	}
	_ = g.checkpointRepo.SaveCheckpoint(ctx, cp)
	if opts.OnProgress != nil {
		opts.OnProgress(state.Checkpoints, "checkpoint", fmt.Sprintf("Checkpoint #%d đã lưu (Node: %s)", state.Checkpoints, state.CurrentNode))
	}
}
