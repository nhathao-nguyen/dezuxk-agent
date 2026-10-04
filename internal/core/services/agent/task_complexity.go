package agent

import (
	"fmt"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// TaskComplexityLevel mức độ phức tạp của bài toán
type TaskComplexityLevel string

const (
	ComplexityTrivial  TaskComplexityLevel = "trivial"
	ComplexitySimple   TaskComplexityLevel = "simple"
	ComplexityModerate TaskComplexityLevel = "moderate"
	ComplexityNormal   TaskComplexityLevel = "moderate" // Alias duy trì tương thích
	ComplexityComplex  TaskComplexityLevel = "complex"
	ComplexityDeep     TaskComplexityLevel = "deep"
)

// TaskRiskLevel mức độ rủi ro hệ thống của bài toán
type TaskRiskLevel string

const (
	RiskLow      TaskRiskLevel = "low"
	RiskMedium   TaskRiskLevel = "medium"
	RiskHigh     TaskRiskLevel = "high"
	RiskCritical TaskRiskLevel = "critical"
)

// ComplexityProfile hồ sơ ước lượng độ phức tạp và ngân sách thực thi tương ứng
type ComplexityProfile struct {
	Level                    TaskComplexityLevel `json:"level"`
	Risk                     TaskRiskLevel       `json:"risk"`
	RecommendedReasoning     string              `json:"recommended_reasoning"` // "none", "low", "medium", "high"
	InitialStepBudget        int                 `json:"initial_step_budget"`
	MaxAllowedStepExtensions int                 `json:"max_allowed_step_extensions"`
	RequiresVerification     bool                `json:"requires_verification"`
	RequiresAction           bool                `json:"requires_action"`
	MinPlanningDepth         int                 `json:"min_planning_depth"`
	MaxPlanningDepth         int                 `json:"max_planning_depth"`
	StepExecutionBudget      int                 `json:"step_execution_budget"`
	ConstraintCount          int                 `json:"constraint_count"`
	IsLongContextReasoning   bool                `json:"is_long_context_reasoning"`
}

// ReasoningPolicy chính sách điều phối năng lực suy luận sâu thích ứng
type ReasoningPolicy struct {
	Level                TaskComplexityLevel `json:"level"`
	Mode                 string              `json:"mode"`          // "none", "low", "medium", "high", "deep"
	Effort               string              `json:"effort"`        // "none", "low", "medium", "high"
	BudgetTokens         int                 `json:"budget_tokens"` // 0, 1024, 2048, 4096, 8192
	MinPlanningDepth     int                 `json:"min_planning_depth"`
	MaxPlanningDepth     int                 `json:"max_planning_depth"`
	StepExecutionBudget  int                 `json:"step_execution_budget"`
	VerificationStrength string              `json:"verification_strength"` // "none", "basic", "strict", "exhaustive"
	IsNative             bool                `json:"is_native"`
	IsEmulated           bool                `json:"is_emulated"`
	EmulationPrompt      string              `json:"emulation_prompt,omitempty"`
}

// DetectTaskComplexity ước lượng độ phức tạp bài toán dựa trên nhiều tín hiệu đa chiều
func DetectTaskComplexity(goal string, customPrompt string) ComplexityProfile {
	text := strings.ToLower(strings.TrimSpace(goal + " " + customPrompt))

	lowerGoal := strings.ToLower(strings.TrimSpace(goal))
	isInformational := strings.HasPrefix(lowerGoal, "explain") || strings.HasPrefix(lowerGoal, "giải thích") ||
		strings.HasPrefix(lowerGoal, "tại sao") || strings.HasPrefix(lowerGoal, "what is") ||
		strings.HasPrefix(lowerGoal, "how does") || strings.HasPrefix(lowerGoal, "tìm hiểu") ||
		strings.HasPrefix(lowerGoal, "hướng dẫn") || strings.HasPrefix(lowerGoal, "describe") ||
		strings.HasPrefix(lowerGoal, "so sánh") || strings.HasPrefix(lowerGoal, "khảo sát") ||
		strings.HasPrefix(lowerGoal, "hãy khảo sát") || strings.HasPrefix(lowerGoal, "nghiên cứu") ||
		strings.Contains(lowerGoal, "research") || strings.Contains(lowerGoal, "khảo sát") ||
		strings.Contains(text, "do not attempt to write or edit files") ||
		lowerGoal == "hello" || lowerGoal == "hi" || lowerGoal == "xin chào"

	// Đếm số lượng constraints ("must", "never", "ensure", "do not", "không được", "bắt buộc")
	constraintKeywords := []string{"must", "never", "ensure", "do not", "không được", "bắt buộc", "strictly", "tuyệt đối"}
	constraintCount := 0
	for _, kw := range constraintKeywords {
		constraintCount += strings.Count(text, kw)
	}

	explicitDeepReasoning := strings.Contains(text, "deep reasoning") || strings.Contains(text, "think deeply") ||
		strings.Contains(text, "thorough") || strings.Contains(text, "step by step") ||
		strings.Contains(text, "suy nghĩ kỹ") || strings.Contains(text, "phân tích kỹ")

	isLongContext := len(goal) > 600

	// 1. Phân loại CRITICAL / DEEP: Race condition, phân tán, đồng thời, bảo mật, hạ tầng production, repo-wide audit
	deepKeywords := []string{
		"race condition", "concurrency", "deadlock", "cluster", "fencing",
		"production incident", "takeover", "distributed", "security",
		"vulnerability", "auth", "audit", "memory leak", "refactor repo",
		"multi-node", "failover", "postgres migration", "prove production safety",
		"audit entire repo",
	}
	for _, kw := range deepKeywords {
		if strings.Contains(text, kw) {
			return ComplexityProfile{
				Level:                    ComplexityDeep,
				Risk:                     RiskCritical,
				RecommendedReasoning:     "high",
				InitialStepBudget:        40,
				MaxAllowedStepExtensions: 20,
				RequiresVerification:     !isInformational,
				RequiresAction:           !isInformational,
				MinPlanningDepth:         4,
				MaxPlanningDepth:         12,
				StepExecutionBudget:      25,
				ConstraintCount:          constraintCount,
				IsLongContextReasoning:   isLongContext,
			}
		}
	}

	if explicitDeepReasoning && !isInformational {
		return ComplexityProfile{
			Level:                    ComplexityDeep,
			Risk:                     RiskHigh,
			RecommendedReasoning:     "high",
			InitialStepBudget:        35,
			MaxAllowedStepExtensions: 15,
			RequiresVerification:     true,
			RequiresAction:           true,
			MinPlanningDepth:         3,
			MaxPlanningDepth:         10,
			StepExecutionBudget:      20,
			ConstraintCount:          constraintCount,
			IsLongContextReasoning:   isLongContext,
		}
	}

	// 2. Phân loại COMPLEX / HIGH: Debugging nhiều tệp, sửa lỗi, điều tra, phân tích root cause
	complexKeywords := []string{
		"debug", "investigate", "fix bug", "test failure", "why does",
		"root cause", "troubleshoot", "inconsistent", "regression",
		"fix error", "fix issue", "sửa lỗi", "điều tra", "tìm nguyên nhân",
	}
	for _, kw := range complexKeywords {
		if strings.Contains(text, kw) {
			return ComplexityProfile{
				Level:                    ComplexityComplex,
				Risk:                     RiskHigh,
				RecommendedReasoning:     "high",
				InitialStepBudget:        30,
				MaxAllowedStepExtensions: 15,
				RequiresVerification:     !isInformational,
				RequiresAction:           !isInformational,
				MinPlanningDepth:         3,
				MaxPlanningDepth:         8,
				StepExecutionBudget:      18,
				ConstraintCount:          constraintCount,
				IsLongContextReasoning:   isLongContext,
			}
		}
	}

	// 3. Phân loại MODERATE / MEDIUM: Viết tính năng mới, viết test, cài đặt hàm, sửa đổi logic
	normalKeywords := []string{
		"implement", "add feature", "create", "write test", "build",
		"thêm tính năng", "tạo mới", "cài đặt", "viết hàm",
	}
	for _, kw := range normalKeywords {
		if strings.Contains(text, kw) {
			return ComplexityProfile{
				Level:                    ComplexityModerate,
				Risk:                     RiskMedium,
				RecommendedReasoning:     "medium",
				InitialStepBudget:        20,
				MaxAllowedStepExtensions: 10,
				RequiresVerification:     !isInformational,
				RequiresAction:           !isInformational,
				MinPlanningDepth:         2,
				MaxPlanningDepth:         6,
				StepExecutionBudget:      14,
				ConstraintCount:          constraintCount,
				IsLongContextReasoning:   isLongContext,
			}
		}
	}

	// 4. Phân loại SIMPLE: Đổi tên biến, sửa nhỏ, định dạng, kiểm tra file đơn lẻ
	simpleKeywords := []string{"rename", "đổi tên", "format", "typo", "chính tả", "clean"}
	for _, kw := range simpleKeywords {
		if strings.Contains(text, kw) {
			return ComplexityProfile{
				Level:                    ComplexitySimple,
				Risk:                     RiskLow,
				RecommendedReasoning:     "low",
				InitialStepBudget:        15,
				MaxAllowedStepExtensions: 5,
				RequiresVerification:     false,
				RequiresAction:           true,
				MinPlanningDepth:         1,
				MaxPlanningDepth:         3,
				StepExecutionBudget:      8,
				ConstraintCount:          constraintCount,
				IsLongContextReasoning:   false,
			}
		}
	}

	// 5. Phân loại TRIVIAL / LOW: Lời chào, câu hỏi ngắn đơn giản
	return ComplexityProfile{
		Level:                    ComplexityTrivial,
		Risk:                     RiskLow,
		RecommendedReasoning:     "none",
		InitialStepBudget:        10,
		MaxAllowedStepExtensions: 3,
		RequiresVerification:     false,
		RequiresAction:           false,
		MinPlanningDepth:         1,
		MaxPlanningDepth:         2,
		StepExecutionBudget:      6,
		ConstraintCount:          constraintCount,
		IsLongContextReasoning:   false,
	}
}

// ResolveReasoningPolicy phân giải chính sách suy luận dựa trên năng lực mô hình và hồ sơ bài toán
func ResolveReasoningPolicy(
	caps domain.ModelCapabilities,
	profile ComplexityProfile,
	req *domain.OpenAIChatRequest,
) ReasoningPolicy {
	// Ưu tiên tham số chỉ định tường minh từ request nếu có
	explicitEffort := ""
	var explicitBudget *int
	if req != nil {
		explicitEffort = req.ReasoningEffort
		if req.ThinkingBudget != nil && *req.ThinkingBudget > 0 {
			explicitBudget = req.ThinkingBudget
		} else if req.BudgetTokens != nil && *req.BudgetTokens > 0 {
			explicitBudget = req.BudgetTokens
		}
	}

	level := profile.Level
	mode := "none"
	effort := "none"
	budget := 0
	verificationStrength := "none"

	switch level {
	case ComplexityTrivial:
		mode = "none"
		effort = "none"
		budget = 0
		verificationStrength = "none"
	case ComplexitySimple:
		mode = "low"
		effort = "low"
		budget = 1024
		verificationStrength = "basic"
	case ComplexityModerate:
		mode = "medium"
		effort = "medium"
		budget = 2048
		verificationStrength = "strict"
	case ComplexityComplex:
		mode = "high"
		effort = "high"
		budget = 4096
		verificationStrength = "strict"
	case ComplexityDeep:
		mode = "deep"
		effort = "high"
		budget = 8192
		verificationStrength = "exhaustive"
	}

	if explicitEffort != "" {
		effort = strings.ToLower(explicitEffort)
		mode = effort
	}
	if explicitBudget != nil {
		budget = *explicitBudget
	}

	isNative := false
	isEmulated := false
	emulationPrompt := ""

	// Kiểm tra năng lực mô hình
	if caps.SupportsNativeThinkingBudget() && budget > 0 {
		isNative = true
	} else if caps.SupportsReasoningEffort() && effort != "none" {
		isNative = true
	} else if caps.SupportsThinking() && mode != "none" {
		isNative = true
	} else if mode != "none" && budget > 0 {
		// Mô hình không hỗ trợ native thinking -> Giả lập ở cấp prompt (EMULATED)
		isEmulated = true
		isNative = false
		emulationPrompt = fmt.Sprintf("[REASONING EMULATION - %s]: Analyze the task requirements, edge cases, constraints, and dependencies carefully before taking concrete action.", strings.ToUpper(mode))
	}

	return ReasoningPolicy{
		Level:                level,
		Mode:                 mode,
		Effort:               effort,
		BudgetTokens:         budget,
		MinPlanningDepth:     profile.MinPlanningDepth,
		MaxPlanningDepth:     profile.MaxPlanningDepth,
		StepExecutionBudget:  profile.StepExecutionBudget,
		VerificationStrength: verificationStrength,
		IsNative:             isNative,
		IsEmulated:           isEmulated,
		EmulationPrompt:      emulationPrompt,
	}
}
