package agent

import (
	"context"
	"fmt"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/linter"
)

// VerificationStatus trạng thái thẩm định mức độ hoàn thành nhiệm vụ
type VerificationStatus string

const (
	StatusComplete          VerificationStatus = "COMPLETE"
	StatusIncomplete        VerificationStatus = "INCOMPLETE"
	StatusNeedsVerification VerificationStatus = "NEEDS_VERIFICATION"
	StatusNeedsMoreTools    VerificationStatus = "NEEDS_MORE_TOOLS"
	StatusNeedsMoreWork     VerificationStatus = "NEEDS_MORE_WORK"
	StatusBlocked           VerificationStatus = "BLOCKED"
)

// VerificationResult kết quả thẩm định hoàn tất của CompletionVerifier
type VerificationResult struct {
	Status              VerificationStatus `json:"status"`
	Reason              string             `json:"reason"`
	SuggestedPrompt     string             `json:"suggested_prompt,omitempty"`
	MissingRequirements []string           `json:"missing_requirements,omitempty"`
	RepairPlanSteps     []domain.PlanStep  `json:"repair_plan_steps,omitempty"`
	UnappliedFix        bool               `json:"unapplied_fix,omitempty"`
	LinterFailed        bool               `json:"linter_failed,omitempty"`
	Unverified          bool               `json:"unverified,omitempty"`
	TestFailed          bool               `json:"test_failed,omitempty"`
}

// CompletionVerifier thẩm định độ tin cậy và sự hoàn chỉnh của phản hồi Agent
// Loại bỏ hoàn toàn giả định ngây thơ "không gọi tool hoặc plan hết bước = hoàn thành"
type CompletionVerifier struct {
	maxRepairRounds int
}

// NewCompletionVerifier tạo mới CompletionVerifier
func NewCompletionVerifier() *CompletionVerifier {
	return &CompletionVerifier{
		maxRepairRounds: 3,
	}
}

// SetMaxRepairRounds cấu hình số vòng sửa chữa tối đa
func (v *CompletionVerifier) SetMaxRepairRounds(rounds int) {
	if rounds <= 0 {
		rounds = 3
	}
	v.maxRepairRounds = rounds
}

// VerifyGoalAndPlan đối chiếu toàn diện Original Goal + Constraints + Plan + Evidence
func (v *CompletionVerifier) VerifyGoalAndPlan(
	ctx context.Context,
	goal string,
	plan *domain.TaskPlan,
	state *domain.AgentState,
	opts domain.AgentRunOptions,
	repairRound int,
) VerificationResult {
	// 1. Kiểm tra giới hạn anti-loop
	maxRounds := v.maxRepairRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	if repairRound >= maxRounds {
		return VerificationResult{
			Status: StatusBlocked,
			Reason: fmt.Sprintf("Đã đạt giới hạn số vòng thẩm định sửa chữa tối đa (%d rounds), dừng lại để chống lặp vô hạn.", maxRounds),
		}
	}

	profile := DetectTaskComplexity(goal, opts.CustomPrompt)
	lowerGoal := strings.ToLower(goal)

	// 2. Thu thập dữ liệu thực tế từ State
	modificationTools := map[string]bool{
		"replace_file_content": true,
		"write_file":           true,
		"write_to_file":        true,
		"patch_file":           true,
		"edit_file":            true,
	}

	var modifiedFiles []string
	var commandsRun []string
	var lastCmdOutput string
	var lastCmdFailed bool

	if state != nil {
		for _, step := range state.Steps {
			for i, tc := range step.ToolCalls {
				if modificationTools[tc.Function.Name] {
					modifiedFiles = append(modifiedFiles, tc.Function.Arguments)
				}
				if tc.Function.Name == "run_command" || tc.Function.Name == "execute_command" {
					commandsRun = append(commandsRun, tc.Function.Arguments)
					if i < len(step.ToolResults) {
						lastCmdOutput = step.ToolResults[i]
						lowerOut := strings.ToLower(lastCmdOutput)
						if strings.Contains(lowerOut, "fail") || strings.Contains(lowerOut, "exit status 1") || strings.Contains(lowerOut, "panic:") {
							lastCmdFailed = true
						} else if strings.Contains(lowerOut, "exit code: 0") || strings.Contains(lowerOut, "pass") || strings.Contains(lowerOut, "ok ") {
							lastCmdFailed = false
						}
					}
				}
			}
		}
	}

	// 3. Phân tích đối chiếu yêu cầu mục tiêu (Original User Goal & Requirements Coverage)
	// Bóc tách các thành phần con nếu có phân cách bởi '+' hoặc 'and' hoặc dấu phẩy
	var missingReqs []string
	goalParts := splitGoalRequirements(goal)

	// Kiểm tra từng requirement con xem đã có trong kế hoạch hoặc bằng chứng chưa
	for _, part := range goalParts {
		partLower := strings.ToLower(strings.TrimSpace(part))
		if len(partLower) < 4 {
			continue
		}

		partAddressed := false
		// Kiểm tra trong Plan
		if plan != nil {
			for _, s := range plan.Steps {
				sText := strings.ToLower(s.Title + " " + s.Description + " " + s.VerificationCommand)
				if strings.Contains(sText, partLower) || requirementKeywordsMatch(partLower, sText) {
					partAddressed = true
					break
				}
			}
		}

		// Kiểm tra trong Commands Run hoặc Files Modified
		if !partAddressed {
			for _, cmd := range commandsRun {
				if strings.Contains(strings.ToLower(cmd), partLower) || requirementKeywordsMatch(partLower, strings.ToLower(cmd)) {
					partAddressed = true
					break
				}
			}
		}

		if !partAddressed {
			missingReqs = append(missingReqs, strings.TrimSpace(part))
		}
	}

	if len(missingReqs) > 0 {
		var repairSteps []domain.PlanStep
		for i, req := range missingReqs {
			repairSteps = append(repairSteps, domain.PlanStep{
				ID:                  len(plan.Steps) + i + 1,
				Title:               fmt.Sprintf("Bổ sung yêu cầu còn thiếu: %s", req),
				Description:         fmt.Sprintf("Hoàn thành yêu cầu từ mục tiêu ban đầu chưa được giải quyết: %s", req),
				VerificationCommand: deriveVerificationCmdForReq(req, opts.Workspace),
				Status:              domain.StepStatusPending,
			})
		}
		return VerificationResult{
			Status:              StatusIncomplete,
			MissingRequirements: missingReqs,
			RepairPlanSteps:     repairSteps,
			Reason:              fmt.Sprintf("Kế hoạch ban đầu bỏ sót %d yêu cầu từ mục tiêu: %s", len(missingReqs), strings.Join(missingReqs, ", ")),
			SuggestedPrompt: fmt.Sprintf("[MISSING REQUIREMENTS]: The following user requirements were omitted and remain incomplete: %s. "+
				"Please complete these remaining items before concluding the task.", strings.Join(missingReqs, ", ")),
		}
	}

	// 4. Kiểm tra mã nguồn có lỗi linter/biên dịch không
	if len(modifiedFiles) > 0 && opts.Workspace != "" {
		summary, hasLintError := linter.CheckWorkspace(ctx, opts.Workspace)
		if hasLintError {
			return VerificationResult{
				Status:       StatusIncomplete,
				LinterFailed: true,
				Reason:       "Không thể hoàn tất vì mã nguồn còn lỗi cú pháp/biên dịch trong workspace",
				SuggestedPrompt: fmt.Sprintf("[LỖI BIÊN DỊCH / LINTER TRƯỚC KHI HOÀN TẤT]: Mã nguồn chưa thể biên dịch thành công:\n%s\n"+
					"Hãy sửa lỗi và đảm bảo mã nguồn biên dịch thành công.", summary),
			}
		}
	}

	// 5. Kiểm tra kiểm thử (Test / Verification check)
	mustVerify := (profile.Level == ComplexityDeep || profile.Level == ComplexityComplex) &&
		(strings.Contains(lowerGoal, "test") || strings.Contains(lowerGoal, "verify") ||
			strings.Contains(lowerGoal, "kiểm tra") || strings.Contains(lowerGoal, "fix") ||
			strings.Contains(lowerGoal, "sửa lỗi") || strings.Contains(lowerGoal, "bug"))

	if mustVerify && len(modifiedFiles) > 0 {
		if len(commandsRun) == 0 {
			return VerificationResult{
				Status:     StatusNeedsVerification,
				Unverified: true,
				Reason:     "Đã sửa đổi mã nguồn nhưng chưa chạy lệnh kiểm thử để xác nhận",
				SuggestedPrompt: "[VERIFICATION REQUIRED]: Code modifications were made, but no test or verification command was executed. " +
					"Please run relevant unit tests via 'run_command' to confirm your fix works and does not break anything.",
			}
		}

		if lastCmdFailed {
			return VerificationResult{
				Status:     StatusNeedsVerification,
				TestFailed: true,
				Reason:     fmt.Sprintf("Lệnh kiểm thử gần nhất báo lỗi hoặc không vượt qua:\n%s", lastCmdOutput),
				SuggestedPrompt: fmt.Sprintf("[TEST FAILED]: The latest verification command reported failures or errors:\n%s\n"+
					"Please inspect the error, adjust the code, and re-run tests until they pass.", lastCmdOutput),
			}
		}
	}

	return VerificationResult{
		Status: StatusComplete,
		Reason: "Nhiệm vụ đã hoàn thành và thỏa mãn đầy đủ các điều kiện xác thực từ mục tiêu ban đầu.",
	}
}

// Verify thẩm định toàn diện trạng thái trước khi cho phép Agent dừng lại (dùng trong ReAct loop)
func (v *CompletionVerifier) Verify(
	ctx context.Context,
	goal string,
	state *domain.AgentState,
	assistantMsg domain.OpenAIMessage,
	opts domain.AgentRunOptions,
) VerificationResult {
	content := strings.TrimSpace(assistantMsg.Content)
	profile := DetectTaskComplexity(goal, opts.CustomPrompt)

	// 1. Kiểm tra phản hồi bị cắt cụt (Hanging / Cut-off check)
	if cutOffReason, isCutOff := v.checkCutOffResponse(content); isCutOff {
		return VerificationResult{
			Status: StatusIncomplete,
			Reason: fmt.Sprintf("Phản hồi của Agent bị cắt cụt giữa chừng: %s", cutOffReason),
			SuggestedPrompt: "[SYSTEM NOTICE - CUT-OFF DETECTED]: Your previous response was interrupted and cut off mid-thought. " +
				"Please continue your response from where you left off and finish the explanation or remaining actions.",
		}
	}

	// 2. Kiểm tra tuyên bố dự định nhưng không gọi công cụ (Trailing Intent Check)
	if intentReason, hasUninvoked := v.checkUninvokedIntent(content); hasUninvoked {
		return VerificationResult{
			Status: StatusNeedsMoreTools,
			Reason: fmt.Sprintf("Mô hình tuyên bố dự định thực thi nhưng không phát sinh tool calls: %s", intentReason),
			SuggestedPrompt: fmt.Sprintf("[SYSTEM NOTICE - UNINVOKED ACTION]: You stated an intention to perform an action ('%s'), "+
				"but did not emit any tool calls. If more steps are needed, call the appropriate tools now.", intentReason),
		}
	}

	// 3. Kiểm tra sửa đổi tệp thực tế (RequireAction / Unapplied Fix Check)
	modificationTools := map[string]bool{
		"replace_file_content": true,
		"write_file":           true,
		"write_to_file":        true,
		"patch_file":           true,
		"edit_file":            true,
	}

	hasModifiedFile := false
	var lastCommandResult string
	var hasRunCommand bool

	if state != nil {
		for _, step := range state.Steps {
			for i, tc := range step.ToolCalls {
				if modificationTools[tc.Function.Name] {
					hasModifiedFile = true
				}
				if tc.Function.Name == "run_command" || tc.Function.Name == "execute_command" {
					hasRunCommand = true
					if i < len(step.ToolResults) {
						lastCommandResult = step.ToolResults[i]
					}
				}
			}
		}
	}

	mustApplyAction := opts.RequireAction || profile.RequiresAction
	if mustApplyAction && !hasModifiedFile {
		return VerificationResult{
			Status:       StatusIncomplete,
			UnappliedFix: true,
			Reason:       "Nhiệm vụ yêu cầu sửa đổi code/tệp nhưng Agent chưa gọi công cụ chỉnh sửa tệp nào",
			SuggestedPrompt: "[ACTION REQUIRED]: You must use tools such as 'replace_file_content' or 'write_file' " +
				"to modify the code and apply the fix. Explaining in plain text without applying changes is strictly prohibited. " +
				"Please use the tools to modify the files now.",
		}
	}

	// 4. Kiểm tra Linter / Trình biên dịch (Compiler / Linter Verification)
	if hasModifiedFile && opts.Workspace != "" {
		summary, hasLintError := linter.CheckWorkspace(ctx, opts.Workspace)
		if hasLintError {
			return VerificationResult{
				Status:       StatusIncomplete,
				LinterFailed: true,
				Reason:       "Không thể hoàn tất vì mã nguồn còn lỗi cú pháp/biên dịch",
				SuggestedPrompt: fmt.Sprintf("[LỖI BIÊN DỊCH / LINTER TRƯỚC KHI HOÀN TẤT]: Mã nguồn chưa thể biên dịch thành công:\n%s\n"+
					"Hãy sử dụng các công cụ để sửa lỗi và đảm bảo mã nguồn biên dịch thành công trước khi kết thúc.", summary),
			}
		}
	}

	// 5. Kiểm tra chạy thử nghiệm / Xác thực (Verification / Test Check)
	lowerGoal := strings.ToLower(goal)
	mustVerify := (profile.Level == ComplexityDeep || profile.Level == ComplexityComplex) &&
		(strings.Contains(lowerGoal, "test") || strings.Contains(lowerGoal, "verify") ||
			strings.Contains(lowerGoal, "kiểm tra") || strings.Contains(lowerGoal, "fix") ||
			strings.Contains(lowerGoal, "sửa lỗi") || strings.Contains(lowerGoal, "bug"))
	if mustVerify && hasModifiedFile {
		if !hasRunCommand {
			return VerificationResult{
				Status:     StatusNeedsVerification,
				Unverified: true,
				Reason:     "Đã sửa đổi mã nguồn nhưng chưa chạy lệnh kiểm thử (run_command) để xác thực",
				SuggestedPrompt: "[VERIFICATION REQUIRED]: Code modifications were made, but no test or verification command was executed. " +
					"Please run relevant unit tests or verification commands via 'run_command' to confirm your fix works and does not break anything.",
			}
		}

		lowerLastCmd := strings.ToLower(lastCommandResult)
		if strings.Contains(lowerLastCmd, "fail") || strings.Contains(lowerLastCmd, "exit status 1") || strings.Contains(lowerLastCmd, "panic:") {
			return VerificationResult{
				Status:     StatusNeedsVerification,
				TestFailed: true,
				Reason:     "Lệnh kiểm thử gần nhất báo lỗi hoặc không vượt qua",
				SuggestedPrompt: fmt.Sprintf("[TEST FAILED]: The latest verification command reported failures or errors:\n%s\n"+
					"Please inspect the error, adjust the code, and re-run tests until they pass.", lastCommandResult),
			}
		}
	}

	// 6. Kiểm tra bế tắc / Blocked
	if v.checkBlockedResponse(content) {
		return VerificationResult{
			Status: StatusBlocked,
			Reason: "Agent báo cáo bế tắc hoặc thiếu quyền hạn không thể tự khắc phục",
		}
	}

	return VerificationResult{
		Status: StatusComplete,
		Reason: "Nhiệm vụ đã hoàn thành và thỏa mãn đầy đủ các điều kiện xác thực",
	}
}

func splitGoalRequirements(goal string) []string {
	// Tách theo dấu '+' hoặc 'and' hoặc dấu phẩy
	var parts []string
	if strings.Contains(goal, "+") {
		raw := strings.Split(goal, "+")
		for _, r := range raw {
			if trimmed := strings.TrimSpace(r); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		return parts
	}

	// Tách theo ' and '
	if strings.Contains(strings.ToLower(goal), " and ") {
		raw := strings.Split(goal, " and ")
		for _, r := range raw {
			if trimmed := strings.TrimSpace(r); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		return parts
	}

	return []string{goal}
}

func requirementKeywordsMatch(req, text string) bool {
	words := strings.Fields(req)
	matchCount := 0
	for _, w := range words {
		w = strings.Trim(strings.ToLower(w), ",.;:()")
		if len(w) > 3 && strings.Contains(text, w) {
			matchCount++
		}
	}
	return matchCount >= len(words)/2 && matchCount > 0
}

func deriveVerificationCmdForReq(req, workspace string) string {
	lower := strings.ToLower(req)
	if strings.Contains(lower, "test") {
		return "go test -v ./..."
	}
	return ""
}

// checkCutOffResponse kiểm tra dấu hiệu phản hồi bị ngắt ngang
func (v *CompletionVerifier) checkCutOffResponse(content string) (string, bool) {
	if content == "" {
		return "Phản hồi rỗng", true
	}

	// Kiểm tra code block chưa đóng (số lượng ``` lẻ)
	codeBlockCount := strings.Count(content, "```")
	if codeBlockCount%2 != 0 {
		return "Khối mã (code block) markdown chưa được đóng", true
	}

	// Kiểm tra câu kết thúc lơ lửng
	trimmed := strings.TrimRight(content, " \t\r\n")
	hangingConnectors := []string{
		"and", "then", "because", "so", "with", "to", "or", "but", "while",
		"và", "nhưng", "vì", "để", "sau đó", "tiếp theo là", "cụ thể:",
		"for example:", "such as:", "including:", "following:",
	}
	lower := strings.ToLower(trimmed)
	for _, conn := range hangingConnectors {
		if strings.HasSuffix(lower, " "+conn) || lower == conn {
			return fmt.Sprintf("Câu kết thúc lơ lửng bằng từ nối '%s'", conn), true
		}
	}

	if strings.HasSuffix(trimmed, "...") || strings.HasSuffix(trimmed, "…") {
		if strings.Contains(lower, "continue") || strings.Contains(lower, "next") || strings.Contains(lower, "loading") {
			return "Kết thúc bằng dấu ba chấm tiếp diễn", true
		}
	}

	return "", false
}

// checkUninvokedIntent kiểm tra xem văn bản có nói "tôi sẽ chạy..." nhưng không gọi tool không
func (v *CompletionVerifier) checkUninvokedIntent(content string) (string, bool) {
	lower := strings.ToLower(content)
	tail := lower
	if len(tail) > 300 {
		tail = tail[len(tail)-300:]
	}

	intentPatterns := []string{
		"i will now run", "i will run", "let me run", "let me execute",
		"i will now execute", "i will now check", "let me check",
		"i will modify", "i will now apply", "next, i will",
		"bây giờ tôi sẽ chạy", "tôi sẽ kiểm tra", "tôi sẽ thực thi",
		"sau đây tôi sẽ gọi", "hãy để tôi kiểm tra",
	}

	for _, pat := range intentPatterns {
		if strings.Contains(tail, pat) {
			return pat, true
		}
	}

	return "", false
}

// checkBlockedResponse kiểm tra phản hồi báo bế tắc
func (v *CompletionVerifier) checkBlockedResponse(content string) bool {
	lower := strings.ToLower(content)
	blockedPatterns := []string{
		"i cannot proceed because",
		"permission denied and cannot be resolved",
		"unable to complete the task due to missing credentials",
		"không thể tiếp tục do thiếu thông tin xác thực",
	}
	for _, p := range blockedPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
