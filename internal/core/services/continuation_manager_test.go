package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
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

type multiRoundStreamCodec struct {
	mu           sync.Mutex
	rounds       []domain.GeminiReply
	deltas       [][]string
	errors       []error
	onDeltaHook  func(round int, delta string)
	currentRound int
}

func (m *multiRoundStreamCodec) MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error) {
	return domain.OutboundAttempt{
		Path:        "/test",
		Body:        "test",
		ContentType: "application/x-www-form-urlencoded",
	}, nil
}

func (m *multiRoundStreamCodec) DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	return m.DematerializeChatStream(ctx, resp, metrics, onDelta, nil)
}

func (m *multiRoundStreamCodec) DematerializeChatStream(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onContent func(delta, convID string) error, onReasoning func(delta, convID string) error) (domain.GeminiReply, error) {
	m.mu.Lock()
	rIndex := m.currentRound
	m.currentRound++
	m.mu.Unlock()

	if rIndex < len(m.errors) && m.errors[rIndex] != nil {
		return domain.GeminiReply{}, m.errors[rIndex]
	}

	if rIndex >= len(m.rounds) {
		return domain.GeminiReply{Text: "default fallback", ConversationID: "c_def"}, nil
	}
	reply := m.rounds[rIndex]
	if rIndex < len(m.deltas) {
		for _, d := range m.deltas[rIndex] {
			if m.onDeltaHook != nil {
				m.onDeltaHook(rIndex, d)
			}
			if onContent != nil {
				if err := onContent(d, reply.ConversationID); err != nil {
					return reply, err
				}
			}
		}
	}
	return reply, nil
}

type configurableTransport struct {
	doRequest func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error)
}

func (c configurableTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (c configurableTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (c configurableTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	if c.doRequest != nil {
		return c.doRequest(ctx, account, service, method, path, body, contentType)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func TestStreamAutoContinuation_Section16(t *testing.T) {
	// Test A: 2 generations with finish_reason=length then stop -> combined output "Part one...Part two." with only 1 final [DONE].
	t.Run("TestA_TwoGenerations_SingleFinalDone", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()
		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{Text: "Part one... (continued...)", ConversationID: "c_stream_a"},
				{Text: "Part two. All done.", ConversationID: "c_stream_a"},
			},
			deltas: [][]string{
				{"Part one... (continued...)"},
				{"Part two. All done."},
			},
		}
		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)

		var buf strings.Builder
		flushed := false
		flusher := func() { flushed = true }

		req := &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Tell story"},
			},
		}

		err := chatService.ExecuteChatStream(context.Background(), req, &buf, flusher)
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if !flushed {
			t.Errorf("expected flusher to be called")
		}

		out := buf.String()
		if !strings.Contains(out, "Part one...") {
			t.Errorf("expected stream to contain Part one..., got: %s", out)
		}
		if !strings.Contains(out, "Part two. All done.") {
			t.Errorf("expected stream to contain Part two. All done., got: %s", out)
		}

		// Verify EXACTLY 1 [DONE] at the very end
		doneCount := strings.Count(out, "data: [DONE]\n\n")
		if doneCount != 1 {
			t.Fatalf("expected exactly 1 'data: [DONE]', found %d", doneCount)
		}
		if !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
			t.Errorf("expected stream to end with 'data: [DONE]'")
		}
		if codec.currentRound != 2 {
			t.Errorf("expected 2 rounds executed, got %d", codec.currentRound)
		}
	})

	// Test B: Overlap deduplication across chunk boundary -> "The Redis coordination layer handles locks." without duplicate "coordination"
	t.Run("TestB_OverlapDeduplication_AcrossBoundary", func(t *testing.T) {
		prior := "The Redis coordination layer "
		dedup := services.NewStreamOverlapDeduplicator(prior)

		// First incoming chunk repeats tail
		d1 := dedup.ProcessDelta("coordination layer ")
		if d1 != "" {
			t.Errorf("expected buffering of overlap, got %q", d1)
		}

		// Second chunk introduces fresh text
		d2 := dedup.ProcessDelta("handles locks efficiently.")
		if !strings.Contains(d2, "handles locks efficiently.") {
			t.Errorf("expected clean suffix pass-through, got %q", d2)
		}

		// Merged text check
		merged := services.MergeContinuation(prior, "coordination layer handles locks.")
		expected := "The Redis coordination layer handles locks."
		if merged != expected {
			t.Errorf("expected %q, got %q", expected, merged)
		}
		if strings.Count(merged, "coordination") != 1 {
			t.Errorf("expected exactly 1 'coordination', got %d in %q", strings.Count(merged, "coordination"), merged)
		}
	})

	// Test C: Unclosed markdown block completed by round 2
	t.Run("TestC_UnclosedMarkdownBlock_Completed", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()
		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{Text: "Here is the code:\n```go\nfunc Main() {\n\tprintln(1)\n", ConversationID: "c_stream_c"},
				{Text: "\tprintln(1)\n}\n```\nDone!", ConversationID: "c_stream_c"},
			},
			deltas: [][]string{
				{"Here is the code:\n```go\nfunc Main() {\n\tprintln(1)\n"},
				{"\tprintln(1)\n}\n```\nDone!"},
			},
		}
		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)

		var buf strings.Builder
		err := chatService.ExecuteChatStream(context.Background(), &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Write go func"},
			},
		}, &buf, nil)
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "```go") || !strings.Contains(out, "println(1)") || !strings.Contains(out, "Done!") {
			t.Errorf("expected full completed markdown block, got: %s", out)
		}
		if codec.currentRound != 2 {
			t.Errorf("expected 2 rounds to complete markdown block, got %d", codec.currentRound)
		}
	})

	// Test D: Incomplete JSON closed by round 2
	t.Run("TestD_IncompleteJSON_Closed", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()
		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{Text: "{\n  \"status\": \"success\",\n  \"items\": [1, 2,", ConversationID: "c_stream_d"},
				{Text: " 3, 4]\n}", ConversationID: "c_stream_d"},
			},
			deltas: [][]string{
				{"{\n  \"status\": \"success\",\n  \"items\": [1, 2,"},
				{" 3, 4]\n}"},
			},
		}
		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)

		var buf strings.Builder
		err := chatService.ExecuteChatStream(context.Background(), &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Generate json"},
			},
			ResponseFormat: "json_object",
		}, &buf, nil)
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "status") || !strings.Contains(out, "success") || !strings.Contains(out, "3, 4") {
			t.Errorf("expected completed JSON, got: %s", out)
		}
		if codec.currentRound != 2 {
			t.Errorf("expected 2 rounds to complete JSON, got %d", codec.currentRound)
		}
	})

	// Test E: Max continuation exhaustion -> stops at maxCont with finish_reason length and continuation_exhausted_total++
	t.Run("TestE_MaxContinuationExhaustion", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()
		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{Text: "Part 1 (continued...)", ConversationID: "c_stream_e"},
				{Text: "Part 2 (continued...)", ConversationID: "c_stream_e"},
				{Text: "Part 3 (continued...)", ConversationID: "c_stream_e"},
				{Text: "Part 4 (continued...)", ConversationID: "c_stream_e"},
			},
			deltas: [][]string{
				{"Part 1 (continued...)"},
				{"Part 2 (continued...)"},
				{"Part 3 (continued...)"},
				{"Part 4 (continued...)"},
			},
		}
		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
		chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
			Enabled:          true,
			MaxContinuations: 2,
		}))
		chatService.SetMetrics(metrics)

		var buf strings.Builder
		err := chatService.ExecuteChatStream(context.Background(), &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Never ending story"},
			},
		}, &buf, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Initial round + 2 continuations = 3 rounds total
		if codec.currentRound != 3 {
			t.Errorf("expected exactly 3 rounds (1 initial + 2 continuations), got %d", codec.currentRound)
		}

		out := buf.String()
		// Final finish_reason must be "length"
		if !strings.Contains(out, `"finish_reason":"length"`) {
			t.Errorf("expected final finish_reason 'length' on exhaustion, got: %s", out)
		}

		snap := metrics.Snapshot()
		if snap.ContinuationsTotal != 2 {
			t.Errorf("expected ContinuationsTotal = 2, got %d", snap.ContinuationsTotal)
		}
		if snap.ContinuationExhaustedTotal != 1 {
			t.Errorf("expected ContinuationExhaustedTotal = 1, got %d", snap.ContinuationExhaustedTotal)
		}
	})

	// Test F: Client disconnect (ctx.Done()) cancels upstream, no subsequent continuation
	t.Run("TestF_ClientDisconnect_CancelsUpstream", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Client disconnected immediately

		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{Text: "Part 1 (continued...)", ConversationID: "c_stream_f"},
			},
			deltas: [][]string{
				{"Part 1 (continued...)"},
			},
		}
		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)

		var buf strings.Builder
		err := chatService.ExecuteChatStream(ctx, &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Story"},
			},
		}, &buf, nil)

		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled")) {
			t.Errorf("expected context.Canceled error, got: %v", err)
		}
	})

	// Test G: Slow stream with chunks before idle deadline -> no idle timeout
	t.Run("TestG_SlowStream_DoesNotIdleTimeout", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pr.Close()

		ctx := google.WithStreamIdleTimeout(context.Background(), 100*time.Millisecond)
		tracker := google.NewStreamMetricsTracker()
		ctx = google.WithStreamMetricsTracker(ctx, tracker)

		go func() {
			defer pw.Close()
			for i := 1; i <= 3; i++ {
				text := fmt.Sprintf("chunk%d", i)
				inner := []any{
					nil, []any{"c_slow", "r_slow"}, nil, nil,
					[]any{[]any{"rc_slow", []any{text}}},
				}
				b, _ := json.Marshal(inner)
				line, _ := json.Marshal([][]any{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b)}})
				_, _ = pw.Write(append(line, '\n'))
				time.Sleep(30 * time.Millisecond) // within 100ms idle window
			}
		}()

		var deltas []string
		reply, err := google.ReadGeminiStreamWithThinking(ctx, pr, nil, func(d, c string) error {
			deltas = append(deltas, d)
			return nil
		}, nil)

		if err != nil {
			t.Fatalf("unexpected error on slow stream: %v", err)
		}
		if reply.Text != "chunk3" && !strings.Contains(reply.Text, "chunk") {
			t.Errorf("unexpected reply text: %q", reply.Text)
		}
	})

	// Test H: Stream idle timeout -> ErrStreamIdleTimeout and stream_idle_timeouts_total++
	t.Run("TestH_StreamIdleTimeout_IncrementsMetric", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pr.Close()

		ctx := google.WithStreamIdleTimeout(context.Background(), 50*time.Millisecond)
		metrics := domain.NewContractMetrics()

		go func() {
			sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_h\",\"r_h\"],null,null,[[\"rc_h\",[\"Hello\"]]]]"]]` + "\n"
			_, _ = pw.Write([]byte(sampleLine))
			// Hang indefinitely
		}()

		_, err := google.ReadGeminiStreamWithThinking(ctx, pr, metrics, nil, nil)
		if err == nil {
			t.Fatalf("expected stream idle timeout, got nil")
		}
		if !errors.Is(err, domain.ErrStreamIdleTimeout) && !strings.Contains(err.Error(), "idle timeout") {
			t.Errorf("expected ErrStreamIdleTimeout, got: %v", err)
		}

		snap := metrics.Snapshot()
		if snap.StreamIdleTimeoutsTotal != 1 {
			t.Errorf("expected StreamIdleTimeoutsTotal = 1, got %d", snap.StreamIdleTimeoutsTotal)
		}
	})
}

func TestStreamAutoContinuation_Mandatory1_StaleUsageBug(t *testing.T) {
	// Initial round:
	// usage.CompletionTokens = 100, max_tokens = 100, finish = "length", text = incomplete
	// Continuation round:
	// usage.CompletionTokens = 30, finish = "stop", text = completes response
	// Expected:
	// exactly 1 continuation (2 rounds total)
	// final finish_reason = "stop"
	// no Round 3
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()
	maxTokens := 100

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:         "Once upon a time in a digital world (continued...)",
				FinishReason: "length",
				Usage: &domain.OpenAIUsage{
					PromptTokens:     20,
					CompletionTokens: 100,
					TotalTokens:      120,
				},
				ConversationID: "c_mand1",
			},
			{
				Text:         "they built an intelligent resilient gateway. The end.",
				FinishReason: "stop",
				Usage: &domain.OpenAIUsage{
					PromptTokens:     0,
					CompletionTokens: 30,
					TotalTokens:      30,
				},
				ConversationID: "c_mand1",
			},
			{
				Text:         "UNEXPECTED ROUND 3 - Should not be reached!",
				FinishReason: "stop",
				Usage: &domain.OpenAIUsage{
					CompletionTokens: 10,
				},
				ConversationID: "c_mand1",
			},
		},
		deltas: [][]string{
			{"Once upon a time in a digital world (continued...)"},
			{"they built an intelligent resilient gateway. The end."},
			{"UNEXPECTED ROUND 3 - Should not be reached!"},
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model:     "gemini-3.8-flash",
		MaxTokens: &maxTokens,
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Write a story"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "UNEXPECTED ROUND 3") {
		t.Fatalf("stale usage bug detected: Round 3 was executed unexpectedly!")
	}
	if codec.currentRound != 2 {
		t.Fatalf("expected exactly 2 rounds executed (1 initial + 1 continuation), got %d", codec.currentRound)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected final finish_reason 'stop', got output: %s", out)
	}
	if strings.Contains(out, `"finish_reason":"length"`) {
		t.Errorf("output must not have finish_reason length when completed, got: %s", out)
	}
}

func TestStreamAutoContinuation_Mandatory2_ContinuationFailureNotStop(t *testing.T) {
	// Initial round: incomplete
	// Continuation request: postGemini returns error (or demat fails)
	// Expected: final state != stop, finish_reason = length/incomplete-compatible value (never stop)

	t.Run("PostGeminiFails_FinalStateLengthNotStop", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()

		chatCalls := 0
		transport := configurableTransport{
			doRequest: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
				if strings.Contains(path, "/test") {
					chatCalls++
					if chatCalls > 1 {
						// Round 2 continuation fails
						return nil, errors.New("upstream connection reset")
					}
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			},
		}

		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{
					Text:           "Partial sentence that continues... (continued...)",
					ConversationID: "c_mand2_a",
				},
			},
			deltas: [][]string{
				{"Partial sentence that continues... (continued...)"},
			},
		}

		chatService := services.NewChatService(mr, repo, transport, codec, metrics)
		chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
			Enabled:          true,
			MaxContinuations: 2,
		}))

		var buf strings.Builder
		req := &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Continue text"},
			},
		}

		err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
		if err != nil {
			t.Fatalf("stream should end cleanly with incomplete state, got error: %v", err)
		}

		out := buf.String()
		if strings.Contains(out, `"finish_reason":"stop"`) {
			t.Fatalf("CRITICAL SEMANTIC BUG: incomplete response with failed continuation received finish_reason: stop!")
		}
		if !strings.Contains(out, `"finish_reason":"length"`) {
			t.Errorf("expected finish_reason 'length' for failed continuation on incomplete output, got: %s", out)
		}
	})

	t.Run("DematerializeFails_FinalStateLengthNotStop", func(t *testing.T) {
		mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
		repo := &mockSessionRepo{}
		metrics := domain.NewContractMetrics()

		codec := &multiRoundStreamCodec{
			rounds: []domain.GeminiReply{
				{
					Text:           "Partial sentence that continues... (continued...)",
					ConversationID: "c_mand2_b",
				},
			},
			deltas: [][]string{
				{"Partial sentence that continues... (continued...)"},
			},
			errors: []error{
				nil,
				errors.New("unexpected wire EOF"),
			},
		}

		chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
		chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
			Enabled:          true,
			MaxContinuations: 2,
		}))

		var buf strings.Builder
		req := &domain.OpenAIChatRequest{
			Model: "gemini-3.8-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Continue text"},
			},
		}

		err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
		if err != nil {
			t.Fatalf("stream should end cleanly with incomplete state, got error: %v", err)
		}

		out := buf.String()
		if strings.Contains(out, `"finish_reason":"stop"`) {
			t.Fatalf("CRITICAL SEMANTIC BUG: incomplete response with failed continuation demat received finish_reason: stop!")
		}
		if !strings.Contains(out, `"finish_reason":"length"`) {
			t.Errorf("expected finish_reason 'length' for failed continuation on incomplete output, got: %s", out)
		}
	})
}

func TestStreamAutoContinuation_Mandatory3_MaxContinuations(t *testing.T) {
	// All rounds incomplete. MaxContinuations = 2.
	// Expected: 2 continuation attempts only (3 rounds total: 1 initial + 2 continuations).
	// finish_reason = length, ContinuationExhausted metric +1, no infinite loop.
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{Text: "Part 1 (continued...)", ConversationID: "c_mand3"},
			{Text: "Part 2 (continued...)", ConversationID: "c_mand3"},
			{Text: "Part 3 (continued...)", ConversationID: "c_mand3"},
			{Text: "Part 4 (continued...)", ConversationID: "c_mand3"},
		},
		deltas: [][]string{
			{"Part 1 (continued...)"},
			{"Part 2 (continued...)"},
			{"Part 3 (continued...)"},
			{"Part 4 (continued...)"},
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 2,
	}))
	chatService.SetMetrics(metrics)

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Loop me"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if codec.currentRound != 3 {
		t.Errorf("expected exactly 3 rounds (1 initial + 2 continuations), got %d", codec.currentRound)
	}

	out := buf.String()
	if !strings.Contains(out, `"finish_reason":"length"`) {
		t.Errorf("expected final finish_reason 'length' when continuations exhausted, got: %s", out)
	}
	if strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("output must not contain finish_reason stop when budget exhausted")
	}

	snap := metrics.Snapshot()
	if snap.ContinuationsTotal != 2 {
		t.Errorf("expected ContinuationsTotal = 2, got %d", snap.ContinuationsTotal)
	}
	if snap.ContinuationExhaustedTotal != 1 {
		t.Errorf("expected ContinuationExhaustedTotal = 1, got %d", snap.ContinuationExhaustedTotal)
	}
}

func TestStreamAutoContinuation_Mandatory4_Round2CompleteStructurallyJSON(t *testing.T) {
	// Round 1: {\n  "name":
	// Round 2: "dezuxk"\n}
	// Round 2 finish reason = stop. Merged JSON parses successfully.
	// Expected: complete, stop, no Round 3.
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:           "{\n  \"name\":",
				ConversationID: "c_mand4",
			},
			{
				Text:           " \"dezuxk\"\n}",
				FinishReason:   "stop",
				ConversationID: "c_mand4",
			},
			{
				Text:           "Round 3 SHOULD NOT HAPPEN",
				ConversationID: "c_mand4",
			},
		},
		deltas: [][]string{
			{"{\n  \"name\":"},
			{" \"dezuxk\"\n}"},
			{"Round 3 SHOULD NOT HAPPEN"},
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Output JSON"},
		},
		ResponseFormat: "json_object",
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if codec.currentRound != 2 {
		t.Fatalf("expected exactly 2 rounds, got %d", codec.currentRound)
	}

	out := buf.String()
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected final finish_reason 'stop', got: %s", out)
	}
	if !strings.Contains(out, "dezuxk") {
		t.Errorf("expected output to contain dezuxk, got: %s", out)
	}
}

func TestStreamAutoContinuation_Mandatory5_CodeBlockCompletion(t *testing.T) {
	// Round 1: ```go\nfunc x() {\n
	// Round 2: }\n```
	// Expected: complete, stop
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:           "```go\nfunc x() {\n",
				ConversationID: "c_mand5",
			},
			{
				Text:           "}\n```",
				FinishReason:   "stop",
				ConversationID: "c_mand5",
			},
		},
		deltas: [][]string{
			{"```go\nfunc x() {\n"},
			{"}\n```"},
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Write go function"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if codec.currentRound != 2 {
		t.Fatalf("expected 2 rounds, got %d", codec.currentRound)
	}

	out := buf.String()
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected final finish_reason 'stop', got: %s", out)
	}
}

func TestStreamAutoContinuation_Mandatory6_ClientCancel(t *testing.T) {
	// Client cancels during continuation.
	// Expected: no further continuation, no [DONE], return context.Canceled.
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()

	ctx, cancel := context.WithCancel(context.Background())

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:           "Initial chunk (continued...)",
				ConversationID: "c_mand6",
			},
			{
				Text:           "Continuation chunk",
				ConversationID: "c_mand6",
			},
		},
		deltas: [][]string{
			{"Initial chunk (continued...)"},
			{"Continuation chunk"},
		},
		onDeltaHook: func(round int, delta string) {
			if round == 1 {
				cancel() // Client disconnects during continuation delta delivery
			}
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Tell story"},
		},
	}

	err := chatService.ExecuteChatStream(ctx, req, &buf, nil)
	if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled")) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "data: [DONE]") {
		t.Errorf("stream must NOT send [DONE] when client cancels during continuation, got: %s", out)
	}
}

func TestStreamAutoContinuation_Mandatory7_IdleTimeout(t *testing.T) {
	// Continuation round hangs.
	// Expected: ErrStreamIdleTimeout, stream_idle_timeout metric increments, not finish_reason=stop, no [DONE].
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:           "Initial chunk (continued...)",
				ConversationID: "c_mand7",
			},
		},
		deltas: [][]string{
			{"Initial chunk (continued...)"},
		},
		errors: []error{
			nil,
			domain.ErrStreamIdleTimeout, // round 2 hangs and times out
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))
	chatService.SetMetrics(metrics)

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Hang story"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err == nil {
		t.Fatalf("expected stream idle timeout error, got nil")
	}
	if !errors.Is(err, domain.ErrStreamIdleTimeout) && !strings.Contains(err.Error(), "idle timeout") {
		t.Errorf("expected ErrStreamIdleTimeout, got: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("idle timeout must NOT send finish_reason: stop, got: %s", out)
	}
	if strings.Contains(out, "data: [DONE]") {
		t.Errorf("idle timeout must NOT send [DONE], got: %s", out)
	}
}

func TestStreamAutoContinuation_Mandatory8_AggregateUsage(t *testing.T) {
	// Round 1: completion_tokens = 100
	// Round 2: completion_tokens = 40
	// Final usage: completion_tokens = 140
	// Completion decision for Round 2 must use 40, not 140 >= max_tokens
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()
	maxTokens := 100

	codec := &multiRoundStreamCodec{
		rounds: []domain.GeminiReply{
			{
				Text:         "Chapter 1 of the story was very detailed and long... (continued...)",
				FinishReason: "length",
				Usage: &domain.OpenAIUsage{
					PromptTokens:     25,
					CompletionTokens: 100,
					TotalTokens:      125,
				},
				ConversationID: "c_mand8",
			},
			{
				Text:         "Chapter 2 completes all storylines cleanly.",
				FinishReason: "stop",
				Usage: &domain.OpenAIUsage{
					PromptTokens:     0,
					CompletionTokens: 40,
					TotalTokens:      40,
				},
				ConversationID: "c_mand8",
			},
			{
				Text:         "Chapter 3 SHOULD NOT BE CALLED",
				FinishReason: "stop",
				Usage: &domain.OpenAIUsage{
					CompletionTokens: 10,
				},
				ConversationID: "c_mand8",
			},
		},
		deltas: [][]string{
			{"Chapter 1 of the story was very detailed and long... (continued...)"},
			{"Chapter 2 completes all storylines cleanly."},
			{"Chapter 3 SHOULD NOT BE CALLED"},
		},
	}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, codec, metrics)
	chatService.SetContinuationService(services.NewContinuationService(services.AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}))

	var buf strings.Builder
	req := &domain.OpenAIChatRequest{
		Model:     "gemini-3.8-flash",
		MaxTokens: &maxTokens,
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Write 2 chapters"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if codec.currentRound != 2 {
		t.Fatalf("expected exactly 2 rounds, got %d (if 3, aggregate tokens leaked into round decision)", codec.currentRound)
	}

	out := buf.String()
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected final finish_reason 'stop', got: %s", out)
	}
	if strings.Contains(out, "Chapter 3 SHOULD NOT BE CALLED") {
		t.Errorf("Chapter 3 was unexpectedly called")
	}

	// Verify aggregate usage in final chunk: completion_tokens = 140, prompt_tokens = 25, total_tokens = 165
	if !strings.Contains(out, `"completion_tokens":140`) {
		t.Errorf("expected final chunk to report aggregate completion_tokens: 140, got: %s", out)
	}
	if !strings.Contains(out, `"total_tokens":165`) {
		t.Errorf("expected final chunk to report aggregate total_tokens: 165, got: %s", out)
	}
}
