package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type callCountingChatUseCase struct {
	callCount int
	resp      *domain.OpenAIChatResponse
}

func (c *callCountingChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	c.callCount++
	return c.resp, nil
}

func (c *callCountingChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, streamWriter io.Writer, flusher func()) error {
	return nil
}


func TestChatHandler_CacheHitAndMiss(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &callCountingChatUseCase{
		resp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-cached",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Đây là câu trả lời được lưu cache.",
					},
					FinishReason: &stop,
				},
			},
		},
	}

	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 3600,
		Methods:    []string{"chat"},
	}
	respCache := services.NewResponseCache(cfg)

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)
	handler.SetCache(respCache)

	reqBody := []byte(`{
		"model": "gemini-3.8-flash",
		"messages": [
			{"role": "user", "content": "Thủ đô của Việt Nam là gì?"}
		],
		"temperature": 0.7
	}`)

	// 1. Request đầu tiên: Phải là Cache MISS
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody))
	w1 := httptest.NewRecorder()
	handler.HandleChatCompletions(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on first call, got %d: %s", w1.Code, w1.Body.String())
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache: MISS on first call, got %s", w1.Header().Get("X-Cache"))
	}
	if mockCU.callCount != 1 {
		t.Errorf("expected 1 call to ChatUseCase, got %d", mockCU.callCount)
	}

	// 2. Request thứ hai với prompt giống hệt: Phải là Cache HIT trong < 5ms
	start := time.Now()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBody))
	w2 := httptest.NewRecorder()
	handler.HandleChatCompletions(w2, req2)
	latency := time.Since(start)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on cached call, got %d", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT on second call, got %s", w2.Header().Get("X-Cache"))
	}
	if latency >= 5*time.Millisecond {
		t.Errorf("expected cache hit latency < 5ms, got %v", latency)
	}
	if mockCU.callCount != 1 {
		t.Errorf("expected ChatUseCase NOT to be called again on cache hit, callCount=%d", mockCU.callCount)
	}

	var parsedResp domain.OpenAIChatResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &parsedResp); err != nil {
		t.Fatalf("failed to parse cached response: %v", err)
	}
	if parsedResp.Choices[0].Message.Content != "Đây là câu trả lời được lưu cache." {
		t.Errorf("unexpected content in cached response: %s", parsedResp.Choices[0].Message.Content)
	}

	// 3. Request thứ ba với prompt khác: Phải là Cache MISS và tăng callCount
	differentReqBody := []byte(`{
		"model": "gemini-3.8-flash",
		"messages": [
			{"role": "user", "content": "Thời tiết hôm nay thế nào?"}
		],
		"temperature": 0.7
	}`)
	req3 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(differentReqBody))
	w3 := httptest.NewRecorder()
	handler.HandleChatCompletions(w3, req3)

	if w3.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache: MISS on different prompt, got %s", w3.Header().Get("X-Cache"))
	}
	if mockCU.callCount != 2 {
		t.Errorf("expected callCount to increment to 2, got %d", mockCU.callCount)
	}
}

type mockFlowCreditUseCase struct {
	callCount int
	accountID string
	balance   int
}

func (m *mockFlowCreditUseCase) GetCredits(ctx context.Context) (string, domain.FlowCreditBalance, error) {
	m.callCount++
	return m.accountID, domain.FlowCreditBalance{Amount: m.balance}, nil
}


func TestFlowHandler_CacheHitAndMiss(t *testing.T) {
	mockCredit := &mockFlowCreditUseCase{
		accountID: "flow_user_123",
		balance:   1050,
	}
	metrics := domain.NewContractMetrics()

	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 60,
		Methods:    []string{"credits"},
	}
	respCache := services.NewResponseCache(cfg)

	handler := adaptersHTTP.NewFlowHandler(mockCredit, metrics)
	handler.SetCache(respCache)

	// Lần 1: MISS
	req1 := httptest.NewRequest(http.MethodGet, "/v1/flow/credits", nil)
	w1 := httptest.NewRecorder()
	handler.HandleGetCredits(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache: MISS on first credits call, got %s", w1.Header().Get("X-Cache"))
	}
	if mockCredit.callCount != 1 {
		t.Errorf("expected 1 call to creditUseCase, got %d", mockCredit.callCount)
	}

	// Lần 2: HIT (<5ms)
	start := time.Now()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/flow/credits", nil)
	w2 := httptest.NewRecorder()
	handler.HandleGetCredits(w2, req2)
	latency := time.Since(start)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on cached credits call, got %d", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT on second credits call, got %s", w2.Header().Get("X-Cache"))
	}
	if latency >= 5*time.Millisecond {
		t.Errorf("expected cache hit latency < 5ms, got %v", latency)
	}
	if mockCredit.callCount != 1 {
		t.Errorf("expected creditUseCase NOT to be called on cache hit, callCount=%d", mockCredit.callCount)
	}
}
