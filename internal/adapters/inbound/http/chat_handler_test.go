package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/core/domain"
)

type mockChatUseCase struct {
	syncResp   *domain.OpenAIChatResponse
	syncErr    error
	streamErr  error
	lastReq    *domain.OpenAIChatRequest
	onStreamDo func(ctx context.Context, w io.Writer, flusher func()) error
}

func (m *mockChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	m.lastReq = req
	return m.syncResp, m.syncErr
}

func (m *mockChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, streamWriter io.Writer, flusher func()) error {
	m.lastReq = req
	if m.onStreamDo != nil {
		return m.onStreamDo(ctx, streamWriter, flusher)
	}
	return m.streamErr
}

func TestChatHandler_SyncWithMultimodalAndUsage(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Tôi đã nhận diện được bức ảnh.",
					},
					FinishReason: &stop,
				},
			},
			Usage: &domain.OpenAIUsage{
				PromptTokens:     265,
				CompletionTokens: 12,
				TotalTokens:      277,
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// Gửi request đa phương thức (Multimodal Image URL)
	reqBody := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "Ảnh này là gì?"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}
				]
			}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp domain.OpenAIChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Usage == nil || resp.Usage.TotalTokens != 277 {
		t.Errorf("expected Usage TotalTokens = 277, got %+v", resp.Usage)
	}

	if len(mockCU.lastReq.Messages) == 0 || !mockCU.lastReq.HasImages() {
		t.Errorf("expected request to have parsed multimodal images")
	}
}

func TestChatHandler_StreamWithUsage(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	mockCU := &mockChatUseCase{
		onStreamDo: func(ctx context.Context, w io.Writer, flusher func()) error {
			stop := "stop"
			deltaChunk := domain.OpenAIChatResponse{
				ID:      "chatcmpl-stream-test",
				Object:  "chat.completion.chunk",
				Created: 1234567890,
				Model:   "gemini-3.8-flash",
				Choices: []domain.OpenAIChoice{
					{Index: 0, Delta: domain.OpenAIDelta{Content: "Xin chào"}},
				},
			}
			data, _ := json.Marshal(deltaChunk)
			fmt.Fprintf(w, "data: %s\n\n", data)

			finalChunk := domain.OpenAIChatResponse{
				ID:      "chatcmpl-stream-test",
				Object:  "chat.completion.chunk",
				Created: 1234567890,
				Model:   "gemini-3.8-flash",
				Choices: []domain.OpenAIChoice{
					{Index: 0, Delta: domain.OpenAIDelta{}, FinishReason: &stop},
				},
				Usage: &domain.OpenAIUsage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
			}
			finalData, _ := json.Marshal(finalChunk)
			fmt.Fprintf(w, "data: %s\n\n", finalData)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher()
			}
			return nil
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	reqBody := `{
		"model": "gemini-3.8-flash",
		"stream": true,
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "chat.completion.chunk") {
		t.Errorf("expected chunk object in stream output")
	}
	if !strings.Contains(body, `"prompt_tokens":10`) {
		t.Errorf("expected Usage in stream output")
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE] in stream output")
	}
}

func TestChatHandler_StreamThroughTraceMiddleware(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	mockCU := &mockChatUseCase{
		onStreamDo: func(ctx context.Context, w io.Writer, flusher func()) error {
			chunk := domain.OpenAIChatResponse{
				ID:      "chatcmpl-test-stream",
				Object:  "chat.completion.chunk",
				Created: 1234567890,
				Model:   "gemini-3.8-flash",
				Choices: []domain.OpenAIChoice{
					{
						Index: 0,
						Delta: domain.OpenAIDelta{
							Content: "Hello from stream",
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher()
			}
			return nil
		},
	}

	chatHandler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)
	// Bọc qua RequestTraceMiddleware (chính là middleware gây lỗi 502 máy chủ không hỗ trợ luồng trước đó)
	pipeline := adaptersHTTP.RequestTraceMiddleware(false)(http.HandlerFunc(chatHandler.HandleChatCompletions))

	reqBody := `{
		"model": "gemini-3.8-flash",
		"stream": true,
		"messages": [
			{"role": "user", "content": "Test streaming through middleware"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	pipeline.ServeHTTP(rec, req)

	// Phải trả về HTTP 200 OK, TUYỆT ĐỐI KHÔNG ĐƯỢC 502
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got: %d, body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Hello from stream") {
		t.Errorf("expected stream content, got: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE], got: %s", body)
	}
}

func TestResponsesAPI_FunctionCallPreserved(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:     "resp-123",
			Object: "chat.completion",
			Model:  "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Hoàn tất đọc file.",
					},
				},
			},
		},
	}
	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	payload := `{
		"model": "gemini-3.8-flash",
		"input": [
			{"type": "message", "role": "user", "content": "Hãy đọc file"},
			{"type": "function_call", "call_id": "call_abc", "name": "read_file", "arguments": "{\"path\": \"hello.txt\"}"},
			{"type": "function_call_output", "call_id": "call_abc", "output": "Hello world"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d, body: %s", rec.Code, rec.Body.String())
	}

	msgs := mockCU.lastReq.Messages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Kiểm tra function_call đã được map thành assistant có ToolCalls
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant message with 1 tool call, got: %+v", msgs[1])
	}
	if msgs[1].ToolCalls[0].ID != "call_abc" || msgs[1].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("expected call_abc and read_file, got: %+v", msgs[1].ToolCalls[0])
	}
	// Kiểm tra tool output message
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_abc" {
		t.Errorf("expected tool message with call_abc, got: %+v", msgs[2])
	}
}

func TestResponsesAPI_ModelNotFoundReturns404JSON(t *testing.T) {
	// Registry chỉ có model không thuộc ServiceGemini
	mr := domain.NewModelRegistry([]domain.ModelDescriptor{
		{
			ID:            "claude-3-5-sonnet",
			TargetService: domain.ServiceKind("claude"),
			IsActive:      true,
		},
	})
	metrics := domain.NewContractMetrics()
	mockCU := &mockChatUseCase{}
	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	payload := `{
		"model": "non-existent-gemini-model-999",
		"input": "Hello"
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got: %d", rec.Code)
	}
	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected application/json content type, got: %s", contentType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "model_not_found") {
		t.Errorf("expected error type model_not_found, got: %s", body)
	}
}
