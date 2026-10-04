package services_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

func TestCompletionDetector_Analyze(t *testing.T) {
	detector := services.NewCompletionDetector()

	t.Run("Finish reason length", func(t *testing.T) {
		res := detector.Analyze("This is an incomplete text...", "length", "")
		if res.IsComplete {
			t.Errorf("expected incomplete for finish_reason length")
		}
		if res.Reason != "finish_reason_length" {
			t.Errorf("expected finish_reason_length, got %s", res.Reason)
		}
	})

	t.Run("Unclosed markdown code block", func(t *testing.T) {
		text := "Here is the code:\n```go\nfunc Main() {\n    fmt.Println(\"hi\")"
		res := detector.Analyze(text, "stop", "")
		if res.IsComplete {
			t.Errorf("expected incomplete for unclosed code block")
		}
		if res.Reason != "unclosed_code_block" {
			t.Errorf("expected unclosed_code_block, got %s", res.Reason)
		}
		if len(res.UnclosedBlocks) == 0 || res.UnclosedBlocks[0] != "go" {
			t.Errorf("expected unclosed block tag 'go', got %+v", res.UnclosedBlocks)
		}
	})

	t.Run("Incomplete JSON", func(t *testing.T) {
		text := "{\n  \"status\": \"success\",\n  \"items\": [1, 2,"
		res := detector.Analyze(text, "stop", "json")
		if res.IsComplete {
			t.Errorf("expected incomplete for broken JSON")
		}
		if res.Reason != "incomplete_json" {
			t.Errorf("expected incomplete_json, got %s", res.Reason)
		}
	})

	t.Run("Hanging sentence ending with conjunction", func(t *testing.T) {
		text := "We investigated the root cause of the deadlock because"
		res := detector.Analyze(text, "stop", "")
		if res.IsComplete {
			t.Errorf("expected incomplete for hanging conjunction 'because'")
		}
	})

	t.Run("Explicit continued marker", func(t *testing.T) {
		text := "Section 1: Setup\nSection 2: Execution\n(continued...)"
		res := detector.Analyze(text, "stop", "")
		if res.IsComplete {
			t.Errorf("expected incomplete for continued marker")
		}
	})

	t.Run("Complete response", func(t *testing.T) {
		text := "The problem has been resolved successfully. All tests pass."
		res := detector.Analyze(text, "stop", "")
		if !res.IsComplete {
			t.Errorf("expected complete response, got %+v", res)
		}
	})
}

func TestMergeContinuation_OverlapDeduplication(t *testing.T) {
	t.Run("Text overlap deduplication", func(t *testing.T) {
		prior := "We discovered a subtle race condition in the session lease"
		next := "session lease manager when two nodes acquire simultaneously."
		merged := services.MergeContinuation(prior, next)
		expected := "We discovered a subtle race condition in the session lease manager when two nodes acquire simultaneously."
		if merged != expected {
			t.Errorf("merge failed.\nExpected: %q\nGot:      %q", expected, merged)
		}
	})

	t.Run("Code block overlap deduplication", func(t *testing.T) {
		prior := "```go\nfunc CalculateTotal() int {\n\tx := 10\n\ty := 20\n"
		next := "y := 20\n\treturn x + y\n}\n```"
		merged := services.MergeContinuation(prior, next)
		expected := "```go\nfunc CalculateTotal() int {\n\tx := 10\n\ty := 20\n\treturn x + y\n}\n```"
		if merged != expected {
			t.Errorf("code merge failed.\nExpected: %q\nGot:      %q", expected, merged)
		}
	})

	t.Run("No overlap standard concatenation", func(t *testing.T) {
		prior := "Step 1: Inspect the workspace."
		next := "Step 2: Apply the code changes."
		merged := services.MergeContinuation(prior, next)
		if !strings.Contains(merged, "Step 1: Inspect the workspace.") || !strings.Contains(merged, "Step 2: Apply the code changes.") {
			t.Errorf("expected both steps preserved, got %q", merged)
		}
	})
}

type mockContinuationChatUseCase struct {
	round int
}

func (m *mockContinuationChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, w io.Writer, flusher func()) error {
	return nil
}

func (m *mockContinuationChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	m.round++
	if m.round == 1 {
		stopReason := "length"
		return &domain.OpenAIChatResponse{
			ConversationID: "c_cont_test",
			Choices: []domain.OpenAIChoice{
				{
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Here is the implementation:\n```python\ndef process_data(items):\n    result = []\n    for item in items:\n        result.append(item * 2)\n",
					},
					FinishReason: &stopReason,
				},
			},
		}, nil
	}

	stopReason := "stop"
	return &domain.OpenAIChatResponse{
		ConversationID: "c_cont_test",
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: "        result.append(item * 2)\n    return result\n```\nDone!",
				},
				FinishReason: &stopReason,
			},
		},
	}, nil
}

var _ ports.ChatUseCase = (*mockContinuationChatUseCase)(nil)

func TestAutoContinuationService_MultiStepContinuation(t *testing.T) {
	cfg := services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}
	svc := services.NewContinuationService(cfg)
	mockChat := &mockContinuationChatUseCase{}

	req := &domain.OpenAIChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Write python code to double items"},
		},
	}

	initResp, err := mockChat.ExecuteChatSync(context.Background(), req)
	if err != nil {
		t.Fatalf("sync round 1 failed: %v", err)
	}

	finalResp, contCount, err := svc.AutoContinueResponse(context.Background(), mockChat, req, initResp, "Write python code")
	if err != nil {
		t.Fatalf("AutoContinueResponse error: %v", err)
	}

	if contCount != 1 {
		t.Errorf("expected 1 continuation, got %d", contCount)
	}

	finalContent := finalResp.Choices[0].Message.Content
	if !strings.Contains(finalContent, "return result") || !strings.Contains(finalContent, "Done!") {
		t.Errorf("expected final merged code to contain 'return result' and 'Done!', got:\n%s", finalContent)
	}
	// Verify no duplicate line
	count := strings.Count(finalContent, "result.append(item * 2)")
	if count != 1 {
		t.Errorf("expected deduplication to result in 1 occurrence of result.append, got %d in:\n%s", count, finalContent)
	}
}

type mockJSONContinuationChatUseCase struct {
	round int
}

func (m *mockJSONContinuationChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, w io.Writer, flusher func()) error {
	return nil
}

func (m *mockJSONContinuationChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	m.round++
	if m.round == 1 {
		stopReason := "length"
		return &domain.OpenAIChatResponse{
			ConversationID: "c_json_test",
			Choices: []domain.OpenAIChoice{
				{
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "{\n  \"status\": \"success\",\n  \"data\": [{\"id\": 1, \"name\": \"alpha\"},",
					},
					FinishReason: &stopReason,
				},
			},
		}, nil
	}

	stopReason := "stop"
	return &domain.OpenAIChatResponse{
		ConversationID: "c_json_test",
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: " {\"id\": 2, \"name\": \"beta\"}]\n}",
				},
				FinishReason: &stopReason,
			},
		},
	}, nil
}

func TestAutoContinuationService_JSONStructuredContinuation(t *testing.T) {
	cfg := services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 2,
	}
	svc := services.NewContinuationService(cfg)
	mockChat := &mockJSONContinuationChatUseCase{}

	req := &domain.OpenAIChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Return JSON payload"},
		},
		ResponseFormat: "json_object",
	}

	initResp, err := mockChat.ExecuteChatSync(context.Background(), req)
	if err != nil {
		t.Fatalf("round 1 failed: %v", err)
	}

	finalResp, contCount, err := svc.AutoContinueResponse(context.Background(), mockChat, req, initResp, "Return JSON payload")
	if err != nil {
		t.Fatalf("AutoContinueResponse failed: %v", err)
	}

	if contCount != 1 {
		t.Errorf("expected 1 continuation for cut-off JSON, got %d", contCount)
	}

	content := finalResp.Choices[0].Message.Content
	if !strings.Contains(content, "\"status\": \"success\"") || !strings.Contains(content, "\"name\": \"beta\"") || !strings.HasSuffix(strings.TrimSpace(content), "}") {
		t.Errorf("expected complete valid JSON output, got:\n%s", content)
	}
}

type mockAlwaysLengthChatUseCase struct {
	calls int
}

func (m *mockAlwaysLengthChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, w io.Writer, flusher func()) error {
	return nil
}

func (m *mockAlwaysLengthChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	m.calls++
	stopReason := "length"
	return &domain.OpenAIChatResponse{
		ConversationID: "c_always_len",
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: "Chunk part continuing...",
				},
				FinishReason: &stopReason,
			},
		},
	}, nil
}

func TestAutoContinuationService_ExhaustionNoInfiniteLoop(t *testing.T) {
	metrics := domain.NewContractMetrics()
	cfg := services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 2,
	}
	svc := services.NewContinuationService(cfg)
	svc.SetMetrics(metrics)

	mockChat := &mockAlwaysLengthChatUseCase{}
	initResp, _ := mockChat.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{})

	finalResp, contCount, err := svc.AutoContinueResponse(context.Background(), mockChat, &domain.OpenAIChatRequest{}, initResp, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contCount != 2 {
		t.Errorf("expected exactly 2 continuations before budget exhausted, got %d", contCount)
	}
	if finalResp == nil || len(finalResp.Choices) == 0 {
		t.Fatalf("expected valid final response")
	}
	if finalResp.Choices[0].FinishReason == nil || *finalResp.Choices[0].FinishReason != "length" {
		t.Errorf("expected finish_reason 'length' when continuation exhausted, got %v", finalResp.Choices[0].FinishReason)
	}

	snap := metrics.Snapshot()
	if snap.ContinuationsTotal != 2 {
		t.Errorf("expected ContinuationsTotal = 2, got %d", snap.ContinuationsTotal)
	}
	if snap.ContinuationExhaustedTotal != 1 {
		t.Errorf("expected ContinuationExhaustedTotal = 1, got %d", snap.ContinuationExhaustedTotal)
	}
}

func TestAutoContinuationService_ClientCancellation(t *testing.T) {
	cfg := services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}
	svc := services.NewContinuationService(cfg)
	mockChat := &mockAlwaysLengthChatUseCase{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before continuation

	initResp, _ := mockChat.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{})
	_, _, err := svc.AutoContinueResponse(ctx, mockChat, &domain.OpenAIChatRequest{}, initResp, "")
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}
}

func TestStreamOverlapDeduplicator(t *testing.T) {
	t.Run("Deduplicates overlap across chunk boundary", func(t *testing.T) {
		prior := "We discovered a subtle race condition in the Redis coordination layer."
		dedup := services.NewStreamOverlapDeduplicator(prior)

		// First incoming chunk repeats the tail
		d1 := dedup.ProcessDelta("Redis coordination ")
		if d1 != "" {
			t.Errorf("expected empty string while buffering overlap, got %q", d1)
		}

		// Second incoming chunk provides new content and finishes the phrase
		d2 := dedup.ProcessDelta("layer. It was caused by lock contention.")
		expected := " It was caused by lock contention."
		if d2 != expected {
			t.Errorf("expected clean deduplicated delta %q, got %q", expected, d2)
		}

		// Subsequent chunks flow directly without buffering
		d3 := dedup.ProcessDelta(" This is now resolved.")
		if d3 != " This is now resolved." {
			t.Errorf("expected direct pass-through delta, got %q", d3)
		}
	})

	t.Run("Flushes buffer if stream terminates early", func(t *testing.T) {
		prior := "Previous paragraph."
		dedup := services.NewStreamOverlapDeduplicator(prior)

		d1 := dedup.ProcessDelta("Short")
		if d1 != "" {
			t.Errorf("expected empty string while buffering, got %q", d1)
		}

		flushed := dedup.Flush()
		if flushed != "Short" {
			t.Errorf("expected flushed content 'Short', got %q", flushed)
		}
	})
}
