package services

import (
	"encoding/json"
	"strings"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// ContextBudgetInput chứa tất cả các tham số ngữ cảnh phục vụ việc tính toán ngân sách thích ứng
type ContextBudgetInput struct {
	Capabilities          domain.ModelCapabilities
	SystemPrompt          string
	Messages              []domain.OpenAIMessage
	Tools                 []domain.OpenAITool
	Memory                string
	RetrievedContext      string
	UserRequest           string
	RequestedOutputTokens *int
	TaskComplexity        string // "trivial", "simple", "moderate", "complex", "deep"
	ReasoningBudget       *int
	ReasoningEffort       string
}

// ContextBudgetManager điều phối và tính toán ngân sách token động thích nghi theo năng lực từng mô hình
type ContextBudgetManager struct {
	tokenCounter *TokenCounter
}

// NewContextBudgetManager khởi tạo ContextBudgetManager
func NewContextBudgetManager(tc *TokenCounter) *ContextBudgetManager {
	if tc == nil {
		tc = NewTokenCounter(config.TokensConfig{})
	}
	return &ContextBudgetManager{
		tokenCounter: tc,
	}
}

// CalculateBudgetPlan tính toán kế hoạch ngân sách token toàn diện thích ứng với nhiệm vụ và mô hình
func (m *ContextBudgetManager) CalculateBudgetPlan(input ContextBudgetInput) domain.ContextBudgetPlan {
	caps := input.Capabilities
	window := caps.EffectiveContextWindow()
	complexity := strings.ToLower(strings.TrimSpace(input.TaskComplexity))
	if complexity == "" {
		complexity = "moderate"
	}

	// 1. Tính toán Safety Margin động (5% đến 15% tùy kích cỡ context)
	safetyRatio := 0.10
	if window <= 32768 {
		safetyRatio = 0.15 // Model nhỏ cần margin an toàn lớn hơn
	} else if window >= 200000 {
		safetyRatio = 0.05 // Model lớn có nhiều dung lượng hơn
	}
	safetyMargin := int(float64(window) * safetyRatio)
	if safetyMargin < 1024 {
		safetyMargin = 1024
	}

	// 2. Tính toán Output Reserve thích ứng theo task complexity và requested tokens
	availableForReserves := window - safetyMargin
	outputReserve := m.determineAdaptiveOutputLimit(caps, input.RequestedOutputTokens, complexity, availableForReserves)

	// 3. Tính toán Reasoning Reserve thích ứng theo task complexity và capabilities
	reasoningReserve := m.determineAdaptiveReasoningReserve(caps, input.ReasoningBudget, input.ReasoningEffort, complexity, availableForReserves-outputReserve)

	// 4. Ngân sách khả dụng thực tế dành cho Prompt + History + Tools + Memory
	usableBudget := window - outputReserve - reasoningReserve - safetyMargin
	if usableBudget < 1024 {
		usableBudget = 1024
	}

	// 5. Phân tích chi tiết token các thành phần hiện tại
	var sysTokens, histTokens, toolTokens, summaryTokens int
	for i, msg := range input.Messages {
		msgTokens := m.tokenCounter.CountMessageTokens(msg)
		content := msg.Content

		if msg.Role == "system" {
			if strings.Contains(content, "Summary") || strings.Contains(content, "Auto-Compacted") {
				summaryTokens += msgTokens
			} else {
				sysTokens += msgTokens
			}
		} else if msg.Role == "tool" || len(msg.ToolCalls) > 0 {
			toolTokens += msgTokens
		} else {
			if i < len(input.Messages)-1 {
				histTokens += msgTokens
			} else {
				histTokens += msgTokens
			}
		}
	}

	// Token từ SystemPrompt truyền ngoài nếu chưa nằm trong Messages
	if input.SystemPrompt != "" && sysTokens == 0 {
		sysTokens = m.tokenCounter.CountTextTokens(input.SystemPrompt, false)
	}

	// Token từ Memory
	var memoryTokens int
	if input.Memory != "" {
		memoryTokens = m.tokenCounter.CountTextTokens(input.Memory, false)
	}

	// Token từ RetrievedContext
	var retrievedTokens int
	if input.RetrievedContext != "" {
		retrievedTokens = m.tokenCounter.CountTextTokens(input.RetrievedContext, false)
	}

	// Token từ UserRequest hiện tại nếu truyền riêng
	var userReqTokens int
	if input.UserRequest != "" {
		userReqTokens = m.tokenCounter.CountTextTokens(input.UserRequest, false)
	}

	// Tính toán token cho định nghĩa Tools nếu có
	if len(input.Tools) > 0 {
		toolDefsBytes, err := json.Marshal(input.Tools)
		if err == nil {
			toolTokens += m.tokenCounter.CountTextTokens(string(toolDefsBytes), false)
		}
	}

	estimatedInputTokens := sysTokens + histTokens + toolTokens + summaryTokens + memoryTokens + retrievedTokens + userReqTokens
	remaining := usableBudget - estimatedInputTokens
	if remaining < 0 {
		remaining = 0
	}

	utilization := float64(estimatedInputTokens) / float64(usableBudget)

	// 6. Xác định Watermark động theo kích cỡ cửa sổ ngữ cảnh
	watermark, compactRec, emergencyReq := m.evaluateWatermark(window, utilization)

	// 7. Quyết định Compaction dựa trên ngân sách token, KHÔNG dựa trên số tin nhắn
	compactionRequired := (watermark >= domain.WatermarkOrange) || (estimatedInputTokens >= usableBudget) || (remaining < safetyMargin/2)

	// 8. Tính toán số lượng message raw gần nhất nên giữ lại (RecommendedKeepRecent)
	recommendedKeepRecent := m.calculateRecommendedKeepRecent(window, remaining, emergencyReq)

	// 9. Ngưỡng mục tiêu sau khi nén (CompactionTargetTokens)
	compactionTargetTokens := int(float64(usableBudget) * 0.50)
	if compactionTargetTokens < 1024 {
		compactionTargetTokens = 1024
	}

	vState := caps.VerificationState
	if vState == "" {
		vState = domain.VerificationStateInferred
	}

	return domain.ContextBudgetPlan{
		ModelID:                     caps.ModelID,
		ModelContextLimit:           window,
		SourceOfWindow:              string(caps.SourceOfCapability),
		VerificationState:           vState,
		EstimatedInputTokens:        estimatedInputTokens,
		SystemTokens:                sysTokens,
		HistoryTokens:               histTokens,
		ToolTokens:                  toolTokens,
		SummaryTokens:               summaryTokens,
		MemoryTokens:                memoryTokens,
		RetrievedTokens:             retrievedTokens,
		UserRequestTokens:           userReqTokens,
		ReservedOutputTokens:        outputReserve,
		ReservedReasoningTokens:     reasoningReserve,
		SafetyMarginTokens:          safetyMargin,
		UsableContextBudget:         usableBudget,
		AvailableHistoryTokens:      remaining,
		RemainingBudget:             remaining,
		UtilizationRatio:            utilization,
		Watermark:                   watermark,
		CompactionRequired:          compactionRequired || compactRec,
		EmergencyCompactionRequired: emergencyReq,
		CompactionTargetTokens:      compactionTargetTokens,
		RecommendedKeepRecent:       recommendedKeepRecent,
	}
}

// CalculateBudget tính toán phân bổ ngân sách token chi tiết (tương thích ngược)
func (m *ContextBudgetManager) CalculateBudget(
	caps domain.ModelCapabilities,
	messages []domain.OpenAIMessage,
	tools []domain.OpenAITool,
	req *domain.OpenAIChatRequest,
) domain.ContextBudget {
	var reqMaxTokens *int
	var reasoningBudget *int
	var reasoningEffort string

	if req != nil {
		reqMaxTokens = req.EffectiveMaxTokens()
		if req.ThinkingBudget != nil {
			reasoningBudget = req.ThinkingBudget
		} else if req.BudgetTokens != nil {
			reasoningBudget = req.BudgetTokens
		}
		reasoningEffort = req.ReasoningEffort
	}

	plan := m.CalculateBudgetPlan(ContextBudgetInput{
		Capabilities:          caps,
		Messages:              messages,
		Tools:                 tools,
		RequestedOutputTokens: reqMaxTokens,
		ReasoningBudget:       reasoningBudget,
		ReasoningEffort:       reasoningEffort,
	})

	return domain.ContextBudget{
		ModelID:                     plan.ModelID,
		ModelContextWindow:          plan.ModelContextLimit,
		SourceOfWindow:              plan.SourceOfWindow,
		VerificationState:           plan.VerificationState,
		IsEstimated:                 caps.IsEstimated,
		OutputReserveTokens:         plan.ReservedOutputTokens,
		ReasoningReserveTokens:      plan.ReservedReasoningTokens,
		SafetyMarginTokens:          plan.SafetyMarginTokens,
		UsableContextBudget:         plan.UsableContextBudget,
		CurrentPromptTokens:         plan.EstimatedInputTokens,
		EstimatedInputTokens:        plan.EstimatedInputTokens,
		SystemTokens:                plan.SystemTokens,
		HistoryTokens:               plan.HistoryTokens,
		ToolTokens:                  plan.ToolTokens,
		SummaryTokens:               plan.SummaryTokens,
		MemoryTokens:                plan.MemoryTokens,
		RetrievedTokens:             plan.RetrievedTokens,
		UserRequestTokens:           plan.UserRequestTokens,
		AvailableHistoryTokens:      plan.AvailableHistoryTokens,
		RemainingBudget:             plan.RemainingBudget,
		UtilizationRatio:            plan.UtilizationRatio,
		Watermark:                   plan.Watermark,
		CompactionRecommended:       plan.CompactionRequired,
		CompactionRequired:          plan.CompactionRequired,
		EmergencyCompactionRequired: plan.EmergencyCompactionRequired,
		CompactionTargetTokens:      plan.CompactionTargetTokens,
		RecommendedKeepRecent:       plan.RecommendedKeepRecent,
	}
}

func (m *ContextBudgetManager) determineAdaptiveOutputLimit(
	caps domain.ModelCapabilities,
	requestedLimit *int,
	complexity string,
	availableBudget int,
) int {
	modelSupported := caps.GetMaxOutputTokens()

	limit := 2048
	switch complexity {
	case "trivial":
		limit = 1024
	case "simple":
		limit = 2048
	case "moderate":
		limit = 4096
	case "complex", "deep":
		limit = 8192
	}

	if limit > modelSupported && modelSupported > 0 {
		limit = modelSupported
	}

	if requestedLimit != nil && *requestedLimit > 0 {
		if *requestedLimit < limit || limit <= 0 {
			limit = *requestedLimit
		}
	}

	// Không để output reserve vượt quá 40% ngân sách khả dụng
	maxAllowedReserve := int(float64(availableBudget) * 0.40)
	if limit > maxAllowedReserve && maxAllowedReserve > 1024 {
		limit = maxAllowedReserve
	}
	if limit < 1024 {
		limit = 1024
	}
	return limit
}

func (m *ContextBudgetManager) determineAdaptiveReasoningReserve(
	caps domain.ModelCapabilities,
	requestedBudget *int,
	requestedEffort string,
	complexity string,
	availableBudget int,
) int {
	if !caps.SupportsThinking() {
		return 0
	}

	// Nếu client yêu cầu explicit thinking budget
	if requestedBudget != nil && *requestedBudget > 0 {
		b := *requestedBudget
		if b > availableBudget/3 {
			b = availableBudget / 3
		}
		return b
	}

	effort := strings.ToLower(strings.TrimSpace(requestedEffort))
	switch effort {
	case "none":
		return 0
	case "low":
		return 1024
	case "medium":
		return 2048
	case "high":
		return 4096
	}

	// Ngân sách suy luận thích ứng theo task complexity
	reserve := 2048
	switch complexity {
	case "trivial":
		reserve = 0
	case "simple":
		reserve = 1024
	case "moderate":
		reserve = 2048
	case "complex":
		reserve = 4096
	case "deep":
		reserve = 8192
	}

	if reserve > availableBudget/3 {
		reserve = availableBudget / 3
	}
	return reserve
}

func (m *ContextBudgetManager) calculateRecommendedKeepRecent(window int, remaining int, isEmergency bool) int {
	if isEmergency {
		return 4
	}
	if window >= 500000 {
		// Ngữ cảnh cực lớn: giữ từ 20 đến 35 lượt gần nhất
		if remaining > 100000 {
			return 30
		}
		return 20
	}
	if window >= 100000 {
		// Dòng 128k tiêu chuẩn: giữ 10 đến 14 lượt
		if remaining > 20000 {
			return 12
		}
		return 8
	}
	// Model nhỏ (32k hoặc ít hơn): giữ 4 đến 6 lượt
	return 6
}

// evaluateWatermark đánh giá Watermark động theo kích cỡ context window
func (m *ContextBudgetManager) evaluateWatermark(window int, utilization float64) (domain.ContextWatermark, bool, bool) {
	if window <= 32768 {
		// Model nhỏ: ngưỡng compact sớm hơn
		switch {
		case utilization < 0.40:
			return domain.WatermarkGreen, false, false
		case utilization < 0.65:
			return domain.WatermarkYellow, false, false
		case utilization < 0.80:
			return domain.WatermarkOrange, true, false
		default:
			return domain.WatermarkRed, true, true
		}
	}

	if window >= 200000 {
		// Model lớn: dung lượng dồi dào, delay compaction
		switch {
		case utilization < 0.60:
			return domain.WatermarkGreen, false, false
		case utilization < 0.75:
			return domain.WatermarkYellow, false, false
		case utilization < 0.88:
			return domain.WatermarkOrange, true, false
		default:
			return domain.WatermarkRed, true, true
		}
	}

	// Model trung bình (32k - 200k)
	switch {
	case utilization < 0.50:
		return domain.WatermarkGreen, false, false
	case utilization < 0.70:
		return domain.WatermarkYellow, false, false
	case utilization < 0.85:
		return domain.WatermarkOrange, true, false
	default:
		return domain.WatermarkRed, true, true
	}
}
