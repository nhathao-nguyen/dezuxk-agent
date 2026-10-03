package services_test

import (
	"context"
	"encoding/json"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestOpenAIChatRequest_ReasoningEffortAndThinkingBudget(t *testing.T) {
	// 1. Test JSON deserialization of reasoning_effort and thinking_budget
	jsonInput := `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "Hello"}],
		"reasoning_effort": "high",
		"thinking_budget": 8192
	}`

	var req domain.OpenAIChatRequest
	if err := json.Unmarshal([]byte(jsonInput), &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if req.ReasoningEffort != "high" {
		t.Errorf("expected reasoning_effort 'high', got %s", req.ReasoningEffort)
	}
	if req.ThinkingBudget == nil || *req.ThinkingBudget != 8192 {
		t.Errorf("expected thinking_budget 8192, got %v", req.ThinkingBudget)
	}

	// 2. Test deserialization when thinking is an object (Anthropic style)
	jsonThinkingObj := `{
		"model": "gemini-2.5-pro",
		"messages": [{"role": "user", "content": "Solve math"}],
		"thinking": {
			"type": "enabled",
			"budget_tokens": 4096
		}
	}`
	var reqObj domain.OpenAIChatRequest
	if err := json.Unmarshal([]byte(jsonThinkingObj), &reqObj); err != nil {
		t.Fatalf("failed to unmarshal request with thinking object: %v", err)
	}
	if reqObj.Thinking == nil || !*reqObj.Thinking {
		t.Error("expected thinking to be enabled")
	}
	if reqObj.ThinkingBudget == nil || *reqObj.ThinkingBudget != 4096 {
		t.Errorf("expected thinking_budget 4096, got %v", reqObj.ThinkingBudget)
	}

	// 3. Test deserialization when thinking is disabled via object
	jsonThinkingDisabled := `{
		"model": "gemini-2.5-pro",
		"messages": [{"role": "user", "content": "Fast answer"}],
		"thinking": {
			"type": "disabled"
		}
	}`
	var reqDisabled domain.OpenAIChatRequest
	if err := json.Unmarshal([]byte(jsonThinkingDisabled), &reqDisabled); err != nil {
		t.Fatalf("failed to unmarshal request with disabled thinking: %v", err)
	}
	if reqDisabled.Thinking == nil || *reqDisabled.Thinking {
		t.Error("expected thinking to be disabled")
	}
}

func TestChatService_TokenUsageRecording(t *testing.T) {
	memRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(memRepo, "master")

	ctx := context.Background()
	created, err := keyService.CreateKey(ctx, domain.CreateKeyRequest{
		Name:          "Test Token Key",
		Role:          "user",
		MaxTokenQuota: 10000,
	})
	if err != nil {
		t.Fatalf("failed to create key: %v", err)
	}

	// Ghi nhận token usage lần 1
	err = keyService.RecordTokenUsage(ctx, created.ID, 120, 350)
	if err != nil {
		t.Fatalf("failed to record token usage: %v", err)
	}

	// Ghi nhận token usage lần 2
	err = keyService.RecordTokenUsage(ctx, created.ID, 80, 200)
	if err != nil {
		t.Fatalf("failed to record token usage 2: %v", err)
	}

	// Kiểm tra tổng lũy kế trong key
	vKey, err := memRepo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to find key: %v", err)
	}

	if vKey.PromptTokensTotal != 200 {
		t.Errorf("expected prompt tokens 200, got %d", vKey.PromptTokensTotal)
	}
	if vKey.CompletionTokensTotal != 550 {
		t.Errorf("expected completion tokens 550, got %d", vKey.CompletionTokensTotal)
	}
	if vKey.TotalTokens != 750 {
		t.Errorf("expected total tokens 750, got %d", vKey.TotalTokens)
	}

	// Kiểm tra lịch sử ngày
	history, err := keyService.GetTokenUsageHistory(ctx, created.ID, 7)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].TotalTokens != 750 {
		t.Errorf("expected 750 total tokens in history, got %d", history[0].TotalTokens)
	}
	if history[0].RequestCount != 2 {
		t.Errorf("expected 2 requests in history, got %d", history[0].RequestCount)
	}

	// Kiểm tra lịch sử hệ thống
	sysHistory, err := keyService.GetSystemTokenUsageHistory(ctx, 7)
	if err != nil {
		t.Fatalf("failed to get system history: %v", err)
	}
	if len(sysHistory) != 1 {
		t.Fatalf("expected 1 system history entry, got %d", len(sysHistory))
	}
	if sysHistory[0].TotalTokens != 750 {
		t.Errorf("expected 750 total tokens in system history, got %d", sysHistory[0].TotalTokens)
	}
}
