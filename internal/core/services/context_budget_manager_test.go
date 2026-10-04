package services_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestContextBudgetManager_ModelAdaptivity(t *testing.T) {
	tc := services.NewTokenCounter(config.TokensConfig{})
	mgr := services.NewContextBudgetManager(tc)

	// Tạo một chuỗi tin nhắn có dung lượng ~20,000 tokens
	longText := strings.Repeat("The distributed database encountered a network partition during leader election. ", 1500)
	messages := []domain.OpenAIMessage{
		{Role: "system", Content: "You are an autonomous engineering agent."},
		{Role: "user", Content: longText},
	}

	// 1. Mô hình nhỏ: 32k context
	smallCaps := domain.ModelCapabilities{
		ModelID:           "mock-small-32k",
		ContextWindow:     32768,
		MaxOutputTokens:   4096,
		SupportsReasoning: false,
	}
	smallBudget := mgr.CalculateBudget(smallCaps, messages, nil, nil)

	// 2. Mô hình lớn: 1M context
	largeCaps := domain.ModelCapabilities{
		ModelID:           "mock-large-1m",
		ContextWindow:     1048576,
		MaxOutputTokens:   16384,
		SupportsReasoning: true,
	}
	largeBudget := mgr.CalculateBudget(largeCaps, messages, nil, nil)

	// Assert: Cùng một lượng token, model nhỏ phải báo Orange hoặc Red (cần nén), trong khi model lớn vẫn ở mức Green!
	if smallBudget.Watermark == domain.WatermarkGreen {
		t.Errorf("expected small 32k model to NOT be Green with ~20k prompt tokens, got %s (utilization: %.2f)", smallBudget.Watermark, smallBudget.UtilizationRatio)
	}
	if !smallBudget.CompactionRequired && !smallBudget.CompactionRecommended && !smallBudget.EmergencyCompactionRequired {
		t.Errorf("expected small 32k model to require compaction")
	}

	if largeBudget.Watermark != domain.WatermarkGreen {
		t.Errorf("expected large 1M model to be Green with ~20k prompt tokens, got %s (utilization: %.2f)", largeBudget.Watermark, largeBudget.UtilizationRatio)
	}
	if largeBudget.CompactionRequired || largeBudget.CompactionRecommended {
		t.Errorf("large 1M model should not prematurely recommend or require compaction")
	}

	// Assert: Usable budget của model lớn phải vượt trội
	if largeBudget.UsableContextBudget <= smallBudget.UsableContextBudget*10 {
		t.Errorf("expected large budget to be significantly larger than small budget")
	}
}

func TestContextBudgetManager_NeverCompactJustBecauseCount(t *testing.T) {
	tc := services.NewTokenCounter(config.TokensConfig{})
	mgr := services.NewContextBudgetManager(tc)

	// 30 tin nhắn rất ngắn (tổng cộng chưa tới 300 tokens)
	var messages []domain.OpenAIMessage
	for i := 0; i < 30; i++ {
		messages = append(messages, domain.OpenAIMessage{
			Role:    "user",
			Content: "ok next",
		})
	}

	caps := domain.ModelCapabilities{
		ModelID:           "gemini-2.5-flash",
		ContextWindow:     1048576,
		MaxOutputTokens:   8192,
		SupportsReasoning: true,
	}

	plan := mgr.CalculateBudgetPlan(services.ContextBudgetInput{
		Capabilities: caps,
		Messages:     messages,
	})

	// Không được yêu cầu compact chỉ vì số lượng messages > 12!
	if plan.CompactionRequired {
		t.Errorf("expected CompactionRequired=false for 30 tiny messages (~150 tokens) on a 1M model, got true")
	}
	if plan.Watermark != domain.WatermarkGreen {
		t.Errorf("expected WatermarkGreen, got %s", plan.Watermark)
	}
}

func TestContextBudgetManager_OutputReserveProtection(t *testing.T) {
	tc := services.NewTokenCounter(config.TokensConfig{})
	mgr := services.NewContextBudgetManager(tc)

	caps := domain.ModelCapabilities{
		ModelID:           "test-model",
		ContextWindow:     131072,
		MaxOutputTokens:   8192,
		SupportsReasoning: true,
	}

	// Case 1: Client requests 2048 output tokens
	reqLimit := 2048
	req := &domain.OpenAIChatRequest{
		MaxCompletionTokens: &reqLimit,
	}
	budget := mgr.CalculateBudget(caps, nil, nil, req)
	if budget.OutputReserveTokens != 2048 {
		t.Errorf("expected output reserve 2048, got %d", budget.OutputReserveTokens)
	}

	// Case 2: Client requests excessively large limit exceeding model support
	tooBig := 65536
	reqOversize := &domain.OpenAIChatRequest{
		MaxCompletionTokens: &tooBig,
	}
	budgetOversize := mgr.CalculateBudget(caps, nil, nil, reqOversize)
	if budgetOversize.OutputReserveTokens > caps.MaxOutputTokens {
		t.Errorf("output reserve %d silently exceeded model capability %d", budgetOversize.OutputReserveTokens, caps.MaxOutputTokens)
	}
}

func TestContextBudgetManager_AdaptiveReservesByComplexity(t *testing.T) {
	tc := services.NewTokenCounter(config.TokensConfig{})
	mgr := services.NewContextBudgetManager(tc)

	caps := domain.ModelCapabilities{
		ModelID:               "reasoning-model",
		ContextWindow:         131072,
		MaxOutputTokens:       16384,
		SupportsReasoning:     true,
		HasThinking:           true,
		ThinkingBudgetSupport: domain.FeatureSupportNative,
	}

	// 1. Trivial task: low/zero reasoning reserve, small output reserve
	trivialPlan := mgr.CalculateBudgetPlan(services.ContextBudgetInput{
		Capabilities:   caps,
		TaskComplexity: "trivial",
	})
	if trivialPlan.ReservedReasoningTokens != 0 {
		t.Errorf("trivial task should have 0 reasoning reserve, got %d", trivialPlan.ReservedReasoningTokens)
	}
	if trivialPlan.ReservedOutputTokens > 2048 {
		t.Errorf("trivial task should have small output reserve <= 2048, got %d", trivialPlan.ReservedOutputTokens)
	}

	// 2. Deep/Repository audit task: high reasoning reserve, large output reserve
	deepPlan := mgr.CalculateBudgetPlan(services.ContextBudgetInput{
		Capabilities:   caps,
		TaskComplexity: "deep",
	})
	if deepPlan.ReservedReasoningTokens < 4096 {
		t.Errorf("deep task should have large reasoning reserve >= 4096, got %d", deepPlan.ReservedReasoningTokens)
	}
	if deepPlan.ReservedOutputTokens < 4096 {
		t.Errorf("deep task should have large output reserve >= 4096, got %d", deepPlan.ReservedOutputTokens)
	}
}

func TestContextBudgetManager_LargeToolSchemaAndSystemPrompt(t *testing.T) {
	tc := services.NewTokenCounter(config.TokensConfig{})
	mgr := services.NewContextBudgetManager(tc)

	caps := domain.ModelCapabilities{
		ModelID:         "tool-model",
		ContextWindow:   65536,
		MaxOutputTokens: 4096,
	}

	// Create large tool schema
	tools := make([]domain.OpenAITool, 15)
	for i := range tools {
		tools[i] = domain.OpenAITool{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:        "analyze_repository_component",
				Description: "Performs deep syntactic and semantic analysis of a component with multiple constraints and arguments.",
				Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}
	}

	sysPrompt := strings.Repeat("You are an enterprise system engineer adhering strictly to security policies. ", 50)

	plan := mgr.CalculateBudgetPlan(services.ContextBudgetInput{
		Capabilities: caps,
		SystemPrompt: sysPrompt,
		Tools:        tools,
	})

	if plan.ToolTokens == 0 {
		t.Errorf("expected tool tokens to be counted, got 0")
	}
	if plan.SystemTokens == 0 {
		t.Errorf("expected system tokens to be counted, got 0")
	}
	if plan.EstimatedInputTokens < plan.ToolTokens+plan.SystemTokens {
		t.Errorf("estimated input tokens should include both tools and system prompt")
	}
}
