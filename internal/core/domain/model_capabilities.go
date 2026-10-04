package domain

import (
	"encoding/json"
	"strings"
	"sync"
)

// FeatureSupportLevel xác định mức độ hỗ trợ của một tính năng cụ thể trên upstream
type FeatureSupportLevel string

const (
	// FeatureSupportNative: Tính năng được hỗ trợ trực tiếp từ upstream wire protocol
	FeatureSupportNative FeatureSupportLevel = "native"
	// FeatureSupportEmulated: Tính năng được Dezuxk Gateway giả lập qua prompt engineering / gateway loop
	FeatureSupportEmulated FeatureSupportLevel = "emulated"
	// FeatureSupportUnsupported: Tính năng không được upstream hỗ trợ
	FeatureSupportUnsupported FeatureSupportLevel = "unsupported"
	// FeatureSupportUnknown: Chưa xác định được mức hỗ trợ của upstream
	FeatureSupportUnknown FeatureSupportLevel = "unknown"
)

// CapabilitySource nguồn gốc của thông tin năng lực mô hình
type CapabilitySource string

const (
	// CapabilitySourceRuntimeDiscovery: Khám phá động qua API / metadata tài khoản upstream thực tế
	CapabilitySourceRuntimeDiscovery CapabilitySource = "runtime_discovery"
	// CapabilitySourceConfigured: Được cấu hình tường minh bởi người dùng / tenant
	CapabilitySourceConfigured CapabilitySource = "configured"
	// CapabilitySourceSafeFallback: Năng lực dự phòng bảo thủ an toàn khi chưa có dữ liệu chính xác
	CapabilitySourceSafeFallback CapabilitySource = "safe_fallback"
)

// CapabilityVerificationState xác định độ tin cậy/xác thực của năng lực (KNOWN, INFERRED, UNKNOWN)
type CapabilityVerificationState string

const (
	// VerificationStateKnown: Năng lực được xác thực chính xác trực tiếp từ upstream discovery hoặc cấu hình rõ ràng
	VerificationStateKnown CapabilityVerificationState = "KNOWN"
	// VerificationStateInferred: Năng lực được suy luận thận trọng dựa trên tier/tên mô hình đã biết
	VerificationStateInferred CapabilityVerificationState = "INFERRED"
	// VerificationStateUnknown: Chưa rõ năng lực, upstream không công bố; hệ thống phải dùng chính sách bảo thủ
	VerificationStateUnknown CapabilityVerificationState = "UNKNOWN"
)

// ModelCapabilities mô tả toàn diện năng lực và giới hạn hoạt động của một mô hình
type ModelCapabilities struct {
	ModelID                           string                      `json:"model_id"`
	ContextWindow                     int                         `json:"context_window"`
	ContextWindowTokens               int                         `json:"context_window_tokens,omitempty"`
	ContextWindowState                CapabilityVerificationState `json:"context_window_state,omitempty"`
	RecommendedContextWindow          int                         `json:"recommended_context_window,omitempty"`
	MaxOutputTokens                   int                         `json:"max_output_tokens"`
	MaxOutputTokensState              CapabilityVerificationState `json:"max_output_tokens_state,omitempty"`
	RecommendedOutputTokens           int                         `json:"recommended_output_tokens,omitempty"`
	HasThinking                       bool                        `json:"supports_thinking"`
	SupportsReasoning                 bool                        `json:"supports_reasoning"` // Alias duy trì tương thích
	ThinkingState                     CapabilityVerificationState `json:"thinking_state,omitempty"`
	NativeThinkingBudget              bool                        `json:"supports_native_thinking_budget"`
	NativeReasoningEffort             bool                        `json:"supports_reasoning_effort"`
	SupportedReasoningModes           []string                    `json:"supported_reasoning_modes,omitempty"` // "none", "low", "medium", "high"
	ReasoningModes                    []string                    `json:"reasoning_modes,omitempty"`           // Alias duy trì tương thích
	SupportsThinkingBudget            bool                        `json:"supports_thinking_budget"`
	ThinkingBudgetSupport             FeatureSupportLevel         `json:"thinking_budget_support"` // Native vs Emulated vs Unsupported vs Unknown
	MaxOutputSupport                  FeatureSupportLevel         `json:"max_output_support"`      // Native vs Emulated vs Unsupported vs Unknown
	SupportsTools                     bool                        `json:"supports_tools"`
	SupportsParallelTools             bool                        `json:"supports_parallel_tools"`
	SupportsStreaming                 bool                        `json:"supports_streaming"`
	SupportsImages                    bool                        `json:"supports_images"`
	SupportsRemoteConversationHistory bool                        `json:"supports_remote_conversation_history"`
	SupportsContextCaching            bool                        `json:"supports_context_caching"`
	SupportsContinuation              bool                        `json:"supports_continuation"`
	SupportsSystemPrompt              bool                        `json:"supports_system_prompt"`
	TokenizerStrategy                 string                      `json:"tokenizer_strategy,omitempty"`
	SourceOfCapability                CapabilitySource            `json:"source_of_capability"`
	VerificationState                 CapabilityVerificationState `json:"verification_state"`
	IsEstimated                       bool                        `json:"is_estimated"` // Đánh dấu nếu context_window là ước lượng bảo thủ
}

// GetContextWindow trả về kích thước cửa sổ ngữ cảnh
func (c ModelCapabilities) GetContextWindow() int {
	if c.ContextWindowTokens > 0 {
		return c.ContextWindowTokens
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow
	}
	return c.EffectiveContextWindow()
}

// GetMaxOutputTokens trả về giới hạn token phản hồi tối đa
func (c ModelCapabilities) GetMaxOutputTokens() int {
	if c.MaxOutputTokens > 0 {
		return c.MaxOutputTokens
	}
	return c.EffectiveMaxOutputTokens()
}

// GetRecommendedOutputTokens trả về giới hạn token khuyến nghị an toàn
func (c ModelCapabilities) GetRecommendedOutputTokens() int {
	if c.RecommendedOutputTokens > 0 {
		return c.RecommendedOutputTokens
	}
	maxTokens := c.GetMaxOutputTokens()
	if maxTokens > 0 {
		return int(float64(maxTokens) * 0.75)
	}
	return 2048
}

// SupportsThinking kiểm tra mô hình có hỗ trợ suy luận mở rộng hay không
func (c ModelCapabilities) SupportsThinking() bool {
	return c.HasThinking || c.SupportsReasoning
}

// SupportsNativeThinkingBudget kiểm tra xem mô hình có hỗ trợ cấu hình ngân sách suy luận nguyên bản hay không
func (c ModelCapabilities) SupportsNativeThinkingBudget() bool {
	return c.NativeThinkingBudget || (c.ThinkingBudgetSupport == FeatureSupportNative)
}

// SupportsReasoningEffort kiểm tra xem mô hình có hỗ trợ mức độ suy luận native (low/medium/high) hay không
func (c ModelCapabilities) SupportsReasoningEffort() bool {
	return c.NativeReasoningEffort
}

// CanContinue kiểm tra mô hình có hỗ trợ cơ chế auto-continuation hay không
func (c ModelCapabilities) CanContinue() bool {
	return c.SupportsContinuation
}

// EffectiveContextWindow trả về kích thước cửa sổ ngữ cảnh an toàn để tính toán budget
func (c ModelCapabilities) EffectiveContextWindow() int {
	if c.RecommendedContextWindow > 0 {
		return c.RecommendedContextWindow
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow
	}
	if c.ContextWindowTokens > 0 {
		return c.ContextWindowTokens
	}
	// Fallback bảo thủ khi không có bất kỳ thông tin nào (Conservative safe fallback)
	if c.ContextWindowState == VerificationStateUnknown {
		return 16384
	}
	return 32768
}

// EffectiveMaxOutputTokens trả về giới hạn token output tối đa khả dụng
func (c ModelCapabilities) EffectiveMaxOutputTokens() int {
	if c.MaxOutputTokens > 0 {
		return c.MaxOutputTokens
	}
	if c.MaxOutputTokensState == VerificationStateUnknown {
		return 2048
	}
	// Giới hạn an toàn mặc định cho LLM
	return 4096
}

// SupportsReasoningMode kiểm tra xem mô hình có hỗ trợ reasoning mode cụ thể không
func (c ModelCapabilities) SupportsReasoningMode(mode string) bool {
	if !c.SupportsThinking() {
		return strings.EqualFold(mode, "none") || mode == ""
	}
	modes := c.SupportedReasoningModes
	if len(modes) == 0 {
		modes = c.ReasoningModes
	}
	if len(modes) == 0 {
		return true
	}
	for _, m := range modes {
		if strings.EqualFold(m, mode) {
			return true
		}
	}
	return false
}

// ResolveCapability kiểm tra capability cụ thể theo domain enum
func (c ModelCapabilities) ResolveCapability(cap ModelCapability) bool {
	switch cap {
	case CapChat:
		return true
	case CapThinking:
		return c.SupportsThinking()
	case CapToolUse:
		return c.SupportsTools
	case CapImage:
		return c.SupportsImages
	default:
		return false
	}
}

// ModelCapabilityRegistry quản lý hồ sơ năng lực mô hình, tự động thích nghi theo thứ tự ưu tiên:
// 1. Runtime discovery
// 2. Configured capability
// 3. Safe fallback defaults
type ModelCapabilityRegistry struct {
	mu         sync.RWMutex
	discovered map[string]ModelCapabilities
	configured map[string]ModelCapabilities
}

// NewModelCapabilityRegistry khởi tạo registry
func NewModelCapabilityRegistry() *ModelCapabilityRegistry {
	return &ModelCapabilityRegistry{
		discovered: make(map[string]ModelCapabilities),
		configured: make(map[string]ModelCapabilities),
	}
}

var defaultCapabilityRegistry = NewModelCapabilityRegistry()

// GetGlobalCapabilityRegistry trả về global capability registry của hệ thống
func GetGlobalCapabilityRegistry() *ModelCapabilityRegistry {
	return defaultCapabilityRegistry
}

// GetCapabilities tra cứu ModelCapabilities cho modelID
func GetCapabilities(modelID string) ModelCapabilities {
	return defaultCapabilityRegistry.Resolve(modelID, nil)
}

// ResolveCapabilities tra cứu ModelCapabilities dựa trên ModelRegistry nếu có
func (mr *ModelRegistry) ResolveCapabilities(modelID string) (ModelCapabilities, bool) {
	if mr == nil {
		return GetCapabilities(modelID), false
	}
	desc, ok := mr.Get(modelID)
	if ok {
		return defaultCapabilityRegistry.Resolve(modelID, &desc), true
	}
	return defaultCapabilityRegistry.Resolve(modelID, nil), false
}

// ResolveCapability tra cứu ModelCapabilities trực tiếp từ ModelRegistry
func (mr *ModelRegistry) ResolveCapability(modelID string) ModelCapabilities {
	if mr == nil {
		return GetCapabilities(modelID)
	}
	caps, _ := mr.ResolveCapabilities(modelID)
	return caps
}

// RegisterDiscovered ghi nhận năng lực khám phá được từ runtime
func (r *ModelCapabilityRegistry) RegisterDiscovered(caps ModelCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	caps.SourceOfCapability = CapabilitySourceRuntimeDiscovery
	if caps.VerificationState == "" {
		caps.VerificationState = VerificationStateKnown
	}
	if caps.ContextWindowState == "" && caps.ContextWindow > 0 {
		caps.ContextWindowState = VerificationStateKnown
	}
	if caps.MaxOutputTokensState == "" && caps.MaxOutputTokens > 0 {
		caps.MaxOutputTokensState = VerificationStateKnown
	}
	if caps.ThinkingState == "" {
		caps.ThinkingState = VerificationStateKnown
	}
	r.discovered[caps.ModelID] = caps
	r.discovered[strings.ToLower(caps.ModelID)] = caps
}

// RegisterConfigured ghi nhận năng lực từ cấu hình
func (r *ModelCapabilityRegistry) RegisterConfigured(caps ModelCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	caps.SourceOfCapability = CapabilitySourceConfigured
	if caps.VerificationState == "" {
		caps.VerificationState = VerificationStateKnown
	}
	r.configured[caps.ModelID] = caps
	r.configured[strings.ToLower(caps.ModelID)] = caps
}

// Resolve tìm kiếm năng lực mô hình theo thứ nguyên ưu tiên: runtime discovery > configured > safe fallback
func (r *ModelCapabilityRegistry) Resolve(modelID string, desc *ModelDescriptor) ModelCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normID := strings.ToLower(strings.TrimSpace(modelID))

	// 1. Ưu tiên cao nhất: Runtime discovered capability
	if caps, ok := r.discovered[normID]; ok {
		return caps
	}

	// 1b. Kiểm tra nếu trong descriptor metadata có lưu capabilities từ runtime discovery
	if desc != nil && desc.Metadata != nil {
		if rawCaps, exists := desc.Metadata["capabilities_profile"]; exists {
			var parsed ModelCapabilities
			b, err := json.Marshal(rawCaps)
			if err == nil && json.Unmarshal(b, &parsed) == nil && parsed.ContextWindow > 0 {
				parsed.SourceOfCapability = CapabilitySourceRuntimeDiscovery
				parsed.VerificationState = VerificationStateKnown
				return parsed
			}
		}
	}

	// 2. Ưu tiên thứ hai: Configured capability
	if caps, ok := r.configured[normID]; ok {
		return caps
	}

	// 3. Fallback an toàn (Safe Fallback) được suy luận bảo thủ từ ModelDescriptor
	return DefaultFallbackCapabilities(modelID, desc)
}

// DefaultFallbackCapabilities suy luận năng lực mô hình bảo thủ dựa trên thông tin sẵn có
func DefaultFallbackCapabilities(modelID string, desc *ModelDescriptor) ModelCapabilities {
	normID := strings.ToLower(modelID)

	supportsTools := true
	supportsImages := true
	supportsStreaming := true
	supportsRemoteHistory := true
	supportsContinuation := true
	supportsSystemPrompt := true
	supportsParallelTools := false

	// Năng lực suy luận (Reasoning)
	supportsReasoning := false
	thinkingState := VerificationStateUnknown
	if desc != nil && desc.HasCapability(CapThinking) {
		supportsReasoning = true
		thinkingState = VerificationStateInferred
	} else if strings.Contains(normID, "thinking") || strings.Contains(normID, "pro") {
		supportsReasoning = true
		thinkingState = VerificationStateInferred
	}

	// Ước lượng bảo thủ Context Window:
	// Không tự bịa nếu hoàn toàn không có cơ sở; nếu mô hình lạ, đánh dấu UNKNOWN
	contextWindow := 32768
	windowState := VerificationStateUnknown
	isEstimated := true
	verificationState := VerificationStateUnknown

	if strings.Contains(normID, "flash") || strings.Contains(normID, "pro") || strings.Contains(normID, "gemini") {
		// Dòng Gemini thường có context window từ 128k đến 1M. Chọn 128k làm mức an toàn conservative
		contextWindow = 131072
		windowState = VerificationStateInferred
		verificationState = VerificationStateInferred
	} else if strings.Contains(normID, "gpt-4") || strings.Contains(normID, "claude") {
		contextWindow = 131072
		windowState = VerificationStateInferred
		verificationState = VerificationStateInferred
	}

	maxOutput := 8192
	outputState := VerificationStateInferred
	if strings.Contains(normID, "flash") {
		maxOutput = 8192
	} else if windowState == VerificationStateUnknown {
		maxOutput = 4096
		outputState = VerificationStateUnknown
	}

	return ModelCapabilities{
		ModelID:                           modelID,
		ContextWindow:                     contextWindow,
		ContextWindowTokens:               contextWindow,
		ContextWindowState:                windowState,
		RecommendedContextWindow:          int(float64(contextWindow) * 0.85), // Dành 15% safety margin
		MaxOutputTokens:                   maxOutput,
		MaxOutputTokensState:              outputState,
		RecommendedOutputTokens:           int(float64(maxOutput) * 0.75),
		HasThinking:                       supportsReasoning,
		SupportsReasoning:                 supportsReasoning,
		ThinkingState:                     thinkingState,
		NativeThinkingBudget:              false,
		NativeReasoningEffort:             supportsReasoning,
		SupportedReasoningModes:           []string{"none", "low", "medium", "high"},
		ReasoningModes:                    []string{"none", "low", "medium", "high"},
		SupportsThinkingBudget:            false, // Gemini Web chưa hỗ trợ native budget payload -> đánh dấu emulated
		ThinkingBudgetSupport:             FeatureSupportEmulated,
		MaxOutputSupport:                  FeatureSupportEmulated, // Native Gemini Web không có verified max_output slot -> đánh dấu emulated
		SupportsTools:                     supportsTools,
		SupportsParallelTools:             supportsParallelTools,
		SupportsStreaming:                 supportsStreaming,
		SupportsImages:                    supportsImages,
		SupportsRemoteConversationHistory: supportsRemoteHistory,
		SupportsContextCaching:            false,
		SupportsContinuation:              supportsContinuation,
		SupportsSystemPrompt:              supportsSystemPrompt,
		TokenizerStrategy:                 "approximate_bpe",
		SourceOfCapability:                CapabilitySourceSafeFallback,
		VerificationState:                 verificationState,
		IsEstimated:                       isEstimated,
	}
}
