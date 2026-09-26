package services_test

import (
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestTokenCounter_BasicAndBPE(t *testing.T) {
	cfg := config.TokensConfig{
		Encoding:             "cl100k_base",
		PromptTokenRatio:     0.25,
		CompletionTokenRatio: 0.25,
		ImageTokensPerTile:   258,
	}
	tc := services.NewTokenCounter(cfg)

	// 1. Text token counting
	t1 := tc.CountTextTokens("Hello world", false)
	if t1 < 2 {
		t.Fatalf("expected at least 2 tokens for 'Hello world', got %d", t1)
	}

	// 2. Multilingual / Vietnamese text token counting
	tVi := tc.CountTextTokens("Xin chào, hôm nay thời tiết thế nào?", false)
	if tVi < 5 {
		t.Fatalf("expected >= 5 tokens for Vietnamese sentence, got %d", tVi)
	}

	// 3. Message prompt token counting (with framing overhead)
	msgs := []domain.OpenAIMessage{
		{Role: "user", Content: "Hello world"},
	}
	promptTokens := tc.CountPromptTokens(msgs)
	// Framing: 3 (msg) + 1 (user) + 2 (hello world) + 3 (assistant priming) = 9
	if promptTokens < 7 {
		t.Fatalf("expected prompt tokens >= 7, got %d", promptTokens)
	}

	// 4. Multimodal message with image
	multiMsgs := []domain.OpenAIMessage{
		{
			Role:    "user",
			Content: "What is this?",
			ContentParts: []domain.MessageContentPart{
				{Type: "text", Text: "What is this?"},
				{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: "https://example.com/cat.png"}},
			},
		},
	}
	multiPromptTokens := tc.CountPromptTokens(multiMsgs)
	// Should include 258 image tokens
	if multiPromptTokens < 260 {
		t.Fatalf("expected >= 260 tokens with image, got %d", multiPromptTokens)
	}

	// 5. Completion token counting with text and thinking blocks
	reply := domain.GeminiReply{
		Text: "This is a cat.",
		ThinkingBlocks: []domain.ThoughtBlock{
			{Content: "Analyzing the feline features in the image...", IsThinking: true},
		},
	}
	compTokens := tc.CountCompletionTokens(reply)
	if compTokens < 10 {
		t.Fatalf("expected completion tokens >= 10, got %d", compTokens)
	}

	// 6. CalculateUsage
	usage := tc.CalculateUsage(multiMsgs, reply)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != multiPromptTokens {
		t.Errorf("expected prompt tokens %d, got %d", multiPromptTokens, usage.PromptTokens)
	}
	if usage.CompletionTokens != compTokens {
		t.Errorf("expected completion tokens %d, got %d", compTokens, usage.CompletionTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("expected total tokens = sum, got %d vs %d", usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	}
}

func TestTokenCounter_EstimationMode(t *testing.T) {
	cfg := config.TokensConfig{
		Encoding:             "estimation",
		PromptTokenRatio:     0.25,
		CompletionTokenRatio: 0.25,
		ImageTokensPerTile:   258,
	}
	tc := services.NewTokenCounter(cfg)

	tokens := tc.CountTextTokens("12345678", false)
	// 8 chars * 0.25 = 2 tokens
	if tokens != 2 {
		t.Fatalf("expected 2 tokens in estimation mode, got %d", tokens)
	}
}
