package domain

// ContextWatermark biểu thị mức độ lấp đầy của ngữ cảnh hoạt động
type ContextWatermark string

const (
	// WatermarkGreen: Ngữ cảnh an toàn, không cần nén hay loại bỏ dữ liệu
	WatermarkGreen ContextWatermark = "green"
	// WatermarkYellow: Ngữ cảnh đang tăng, ưu tiên lọc và xếp hạng dữ liệu truy xuất
	WatermarkYellow ContextWatermark = "yellow"
	// WatermarkOrange: Ngữ cảnh cao, bắt buộc nén có cấu trúc (Structured Compaction) cho lịch sử cũ
	WatermarkOrange ContextWatermark = "orange"
	// WatermarkRed: Ngữ cảnh gần ngưỡng an toàn, bắt buộc nén khẩn cấp để tránh tràn context
	WatermarkRed ContextWatermark = "red"
)

// ContextBudget biểu diễn ngân sách token chi tiết cho một lượt gọi mô hình (giữ tương thích)
type ContextBudget struct {
	ModelID                     string                      `json:"model_id"`
	ModelContextWindow          int                         `json:"model_context_window"`
	SourceOfWindow              string                      `json:"source_of_window"`
	VerificationState           CapabilityVerificationState `json:"verification_state,omitempty"`
	IsEstimated                 bool                        `json:"is_estimated"`
	OutputReserveTokens         int                         `json:"output_reserve_tokens"`
	ReasoningReserveTokens      int                         `json:"reasoning_reserve_tokens"`
	SafetyMarginTokens          int                         `json:"safety_margin_tokens"`
	UsableContextBudget         int                         `json:"usable_context_budget"`
	CurrentPromptTokens         int                         `json:"current_prompt_tokens"`
	EstimatedInputTokens        int                         `json:"estimated_input_tokens,omitempty"`
	SystemTokens                int                         `json:"system_tokens"`
	HistoryTokens               int                         `json:"history_tokens"`
	ToolTokens                  int                         `json:"tool_tokens"`
	SummaryTokens               int                         `json:"summary_tokens"`
	MemoryTokens                int                         `json:"memory_tokens,omitempty"`
	RetrievedTokens             int                         `json:"retrieved_tokens"`
	UserRequestTokens           int                         `json:"user_request_tokens,omitempty"`
	AvailableHistoryTokens      int                         `json:"available_history_tokens,omitempty"`
	RemainingBudget             int                         `json:"remaining_budget"`
	UtilizationRatio            float64                     `json:"utilization_ratio"`
	Watermark                   ContextWatermark            `json:"watermark"`
	CompactionRecommended       bool                        `json:"compaction_recommended"`
	CompactionRequired          bool                        `json:"compaction_required"`
	EmergencyCompactionRequired bool                        `json:"emergency_compaction_required"`
	CompactionTargetTokens      int                         `json:"compaction_target_tokens,omitempty"`
	RecommendedKeepRecent       int                         `json:"recommended_keep_recent,omitempty"`
}

// ContextBudgetPlan là bản kế hoạch ngân sách token toàn diện trả về từ ContextBudgetManager
type ContextBudgetPlan struct {
	ModelID                     string                      `json:"model_id"`
	ModelContextLimit           int                         `json:"model_context_limit"`
	SourceOfWindow              string                      `json:"source_of_window"`
	VerificationState           CapabilityVerificationState `json:"verification_state"`
	EstimatedInputTokens        int                         `json:"estimated_input_tokens"`
	SystemTokens                int                         `json:"system_tokens"`
	HistoryTokens               int                         `json:"history_tokens"`
	ToolTokens                  int                         `json:"tool_tokens"`
	SummaryTokens               int                         `json:"summary_tokens"`
	MemoryTokens                int                         `json:"memory_tokens"`
	RetrievedTokens             int                         `json:"retrieved_tokens"`
	UserRequestTokens           int                         `json:"user_request_tokens"`
	ReservedOutputTokens        int                         `json:"reserved_output_tokens"`
	ReservedReasoningTokens     int                         `json:"reserved_reasoning_tokens"`
	SafetyMarginTokens          int                         `json:"safety_margin_tokens"`
	UsableContextBudget         int                         `json:"usable_context_budget"`
	AvailableHistoryTokens      int                         `json:"available_history_tokens"`
	RemainingBudget             int                         `json:"remaining_budget"`
	UtilizationRatio            float64                     `json:"utilization_ratio"`
	Watermark                   ContextWatermark            `json:"watermark"`
	CompactionRequired          bool                        `json:"compaction_required"`
	EmergencyCompactionRequired bool                        `json:"emergency_compaction_required"`
	CompactionTargetTokens      int                         `json:"compaction_target_tokens"`
	RecommendedKeepRecent       int                         `json:"recommended_keep_recent"`
}

// ToPlan chuyển đổi ContextBudget sang ContextBudgetPlan
func (b ContextBudget) ToPlan() ContextBudgetPlan {
	compRequired := b.CompactionRequired || b.CompactionRecommended
	target := b.CompactionTargetTokens
	if target <= 0 && b.UsableContextBudget > 0 {
		target = int(float64(b.UsableContextBudget) * 0.50)
	}
	keepRecent := b.RecommendedKeepRecent
	if keepRecent <= 0 {
		keepRecent = 6
	}
	inputTokens := b.EstimatedInputTokens
	if inputTokens <= 0 {
		inputTokens = b.CurrentPromptTokens
	}
	return ContextBudgetPlan{
		ModelID:                     b.ModelID,
		ModelContextLimit:           b.ModelContextWindow,
		SourceOfWindow:              b.SourceOfWindow,
		VerificationState:           b.VerificationState,
		EstimatedInputTokens:        inputTokens,
		SystemTokens:                b.SystemTokens,
		HistoryTokens:               b.HistoryTokens,
		ToolTokens:                  b.ToolTokens,
		SummaryTokens:               b.SummaryTokens,
		MemoryTokens:                b.MemoryTokens,
		RetrievedTokens:             b.RetrievedTokens,
		UserRequestTokens:           b.UserRequestTokens,
		ReservedOutputTokens:        b.OutputReserveTokens,
		ReservedReasoningTokens:     b.ReasoningReserveTokens,
		SafetyMarginTokens:          b.SafetyMarginTokens,
		UsableContextBudget:         b.UsableContextBudget,
		AvailableHistoryTokens:      b.RemainingBudget,
		RemainingBudget:             b.RemainingBudget,
		UtilizationRatio:            b.UtilizationRatio,
		Watermark:                   b.Watermark,
		CompactionRequired:          compRequired,
		EmergencyCompactionRequired: b.EmergencyCompactionRequired,
		CompactionTargetTokens:      target,
		RecommendedKeepRecent:       keepRecent,
	}
}
