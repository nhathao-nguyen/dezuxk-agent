package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/core/domain"
)

// TestClientCompat_OpenAISDK_ChatCompletions verifies OpenAI Python/Go SDK contract for sync & stream
func TestClientCompat_OpenAISDK_ChatCompletions(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-sdk-test",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Hello from Dezuxk Gateway!",
					},
					FinishReason: &stop,
				},
			},
			Usage: &domain.OpenAIUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// 1. Sync Request from OpenAI Python SDK: client.chat.completions.create(...)
	sdkReq := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{"role": "system", "content": "You are a test assistant"},
			{"role": "user", "content": "Ping"}
		],
		"stream": false,
		"temperature": 0.5,
		"top_p": 1.0,
		"max_tokens": 100
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(sdkReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp domain.OpenAIChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Object != "chat.completion" || resp.ID != "chatcmpl-sdk-test" {
		t.Errorf("unexpected response structure: %+v", resp)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from Dezuxk Gateway!" {
		t.Errorf("unexpected choice: %+v", resp.Choices)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("unexpected finish_reason")
	}

	// 2. Stream Request from OpenAI SDK
	mockCU.onStreamDo = func(ctx context.Context, w io.Writer, flusher func()) error {
		chunk1 := `data: {"id":"chatcmpl-stream","object":"chat.completion.chunk","created":1700000000,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"Streaming"},"finish_reason":null}]}` + "\n\n"
		chunk2 := `data: {"id":"chatcmpl-stream","object":"chat.completion.chunk","created":1700000000,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"content":" response"},"finish_reason":"stop"}]}` + "\n\n"
		done := "data: [DONE]\n\n"
		w.Write([]byte(chunk1))
		w.Write([]byte(chunk2))
		w.Write([]byte(done))
		flusher()
		return nil
	}

	streamReq := `{
		"model": "gemini-3.8-flash",
		"messages": [{"role": "user", "content": "Stream me"}],
		"stream": true
	}`
	reqStream := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(streamReq))
	reqStream.Header.Set("Content-Type", "application/json")
	recStream := httptest.NewRecorder()

	handler.HandleChatCompletions(recStream, reqStream)

	if recStream.Code != http.StatusOK {
		t.Fatalf("expected stream HTTP 200, got %d", recStream.Code)
	}
	if !strings.Contains(recStream.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got: %s", recStream.Header().Get("Content-Type"))
	}
	streamBody := recStream.Body.String()
	if !strings.Contains(streamBody, "Streaming") || !strings.Contains(streamBody, "[DONE]") {
		t.Errorf("stream body missing expected chunks: %s", streamBody)
	}
}

// TestClientCompat_CodexCLI_ResponsesAPI verifies compatibility with Codex CLI / Responses API
func TestClientCompat_CodexCLI_ResponsesAPI(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-codex",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "function answer",
					},
					FinishReason: &stop,
				},
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// Codex CLI sends instructions and array input with function_call_output and string
	codexReq := `{
		"model": "gemini-3.8-flash",
		"instructions": "You are Codex CLI backend engine.",
		"input": [
			{
				"type": "function_call",
				"call_id": "call_codex_1",
				"name": "exec_cmd",
				"arguments": "{\"cmd\": \"ls\"}"
			},
			{
				"type": "function_call_output",
				"call_id": "call_codex_1",
				"output": "file1.go\nfile2.go"
			},
			"Now inspect file1.go"
		],
		"stream": false,
		"max_output_tokens": 2048,
		"reasoning_effort": "high"
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(codexReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["object"] != "response" || resp["status"] != "completed" {
		t.Errorf("unexpected responses format: %+v", resp)
	}

	output, ok := resp["output"].([]any)
	if !ok || len(output) == 0 {
		t.Fatalf("expected output items in responses API: %+v", resp)
	}

	// Verify that instructions and function_call_output were mapped to messages in mockCU.lastReq
	if mockCU.lastReq == nil {
		t.Fatalf("mockCU did not receive request")
	}
	if len(mockCU.lastReq.Messages) != 4 {
		t.Fatalf("expected 4 messages (1 system, 1 assistant tool call, 1 tool output, 1 user string), got %d: %+v",
			len(mockCU.lastReq.Messages), mockCU.lastReq.Messages)
	}
	if mockCU.lastReq.Messages[0].Role != "system" || mockCU.lastReq.Messages[0].Content != "You are Codex CLI backend engine." {
		t.Errorf("system message mismatch: %+v", mockCU.lastReq.Messages[0])
	}
	if mockCU.lastReq.Messages[1].Role != "assistant" || len(mockCU.lastReq.Messages[1].ToolCalls) != 1 {
		t.Errorf("function_call mapping mismatch: %+v", mockCU.lastReq.Messages[1])
	}
	if mockCU.lastReq.Messages[2].Role != "tool" || mockCU.lastReq.Messages[2].ToolCallID != "call_codex_1" {
		t.Errorf("tool output mapping mismatch: %+v", mockCU.lastReq.Messages[2])
	}
	if mockCU.lastReq.Messages[3].Role != "user" || mockCU.lastReq.Messages[3].Content != "Now inspect file1.go" {
		t.Errorf("user string in input array mismatch: %+v", mockCU.lastReq.Messages[3])
	}
	if mockCU.lastReq.MaxOutputTokens == nil || *mockCU.lastReq.MaxOutputTokens != 2048 {
		t.Errorf("max_output_tokens not passed through: %+v", mockCU.lastReq.MaxOutputTokens)
	}
	if mockCU.lastReq.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort not passed through: %s", mockCU.lastReq.ReasoningEffort)
	}
}

// TestClientCompat_Cursor_MultimodalAndReasoning verifies Cursor style multimodal format and reasoning
func TestClientCompat_Cursor_MultimodalAndReasoning(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-cursor",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:             "assistant",
						Content:          "Parsed image successfully.",
						ReasoningContent: "Thinking about the pixels...",
					},
					FinishReason: &stop,
				},
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// Cursor sends multimodal with input_text or text, and direct string or object image_url
	cursorReq := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "input_text", "text": "Analyze this screenshot"},
					{"type": "image_url", "image_url": "https://example.com/screenshot.png"}
				]
			}
		],
		"tool_choice": "auto",
		"reasoning_effort": "medium"
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(cursorReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if mockCU.lastReq == nil || len(mockCU.lastReq.Messages) == 0 {
		t.Fatalf("request was not dispatched properly")
	}

	userMsg := mockCU.lastReq.Messages[0]
	if !strings.Contains(userMsg.Content, "Analyze this screenshot") {
		t.Errorf("input_text content was not parsed: %s", userMsg.Content)
	}
	urls := userMsg.GetImageURLs()
	if len(urls) != 1 || urls[0] != "https://example.com/screenshot.png" {
		t.Errorf("image_url string format was not parsed properly: %+v", urls)
	}
}

// TestClientCompat_ClineRooCode_ParallelToolCalls verifies Cline and Roo Code style parallel tool calls
func TestClientCompat_ClineRooCode_ParallelToolCalls(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	toolCallsReason := "tool_calls"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-cline",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role: "assistant",
						ToolCalls: []domain.OpenAIToolCall{
							{
								ID:   "call_read_1",
								Type: "function",
								Function: domain.OpenAIFunctionCallData{
									Name:      "read_file",
									Arguments: `{"path": "package.json"}`,
								},
							},
							{
								ID:   "call_read_2",
								Type: "function",
								Function: domain.OpenAIFunctionCallData{
									Name:      "read_file",
									Arguments: `{"path": "tsconfig.json"}`,
								},
							},
						},
					},
					FinishReason: &toolCallsReason,
				},
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// Cline/Roo Code sends complex tools schema and then provides parallel tool results
	clineReq := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{"role": "system", "content": "<role>You are Cline software engineer</role>"},
			{"role": "user", "content": "Read configuration files"},
			{
				"role": "assistant",
				"tool_calls": [
					{
						"id": "call_read_1",
						"type": "function",
						"function": {"name": "read_file", "arguments": "{\"path\": \"package.json\"}"}
					},
					{
						"id": "call_read_2",
						"type": "function",
						"function": {"name": "read_file", "arguments": "{\"path\": \"tsconfig.json\"}"}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_read_1",
				"content": "{\"name\": \"my-app\"}"
			},
			{
				"role": "tool",
				"tool_call_id": "call_read_2",
				"content": "{\"compilerOptions\": {}}"
			}
		],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "read_file",
					"description": "Read contents of file",
					"parameters": {
						"type": "object",
						"properties": {
							"path": {"type": "string"}
						},
						"required": ["path"]
					}
				}
			}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(clineReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp domain.OpenAIChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 parallel tool calls, got: %d", len(resp.Choices[0].Message.ToolCalls))
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason tool_calls, got: %v", resp.Choices[0].FinishReason)
	}
}

// TestClientCompat_ContinueDev_LegacyFunctionCall verifies Continue.dev legacy function_call conversion
func TestClientCompat_ContinueDev_LegacyFunctionCall(t *testing.T) {
	// Continue.dev sometimes sends legacy `function_call` inside message history
	legacyPayload := `{
		"role": "assistant",
		"content": null,
		"function_call": {
			"name": "edit_file",
			"arguments": "{\"path\":\"a.txt\",\"content\":\"hello\"}"
		}
	}`

	var msg domain.OpenAIMessage
	if err := json.Unmarshal([]byte(legacyPayload), &msg); err != nil {
		t.Fatalf("failed to unmarshal legacy function_call: %v", err)
	}

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected legacy function_call to be converted to 1 tool_call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "edit_file" {
		t.Errorf("function name mismatch: %s", msg.ToolCalls[0].Function.Name)
	}
	if msg.ToolCalls[0].Function.Arguments != `{"path":"a.txt","content":"hello"}` {
		t.Errorf("function arguments mismatch: %s", msg.ToolCalls[0].Function.Arguments)
	}
}

// TestClientCompat_OpenWebUI_ModelsDiscovery verifies OpenWebUI / LibreChat /v1/models endpoint
func TestClientCompat_OpenWebUI_ModelsDiscovery(t *testing.T) {
	catalog := domain.GetGeminiCatalog()
	mr := domain.NewModelRegistry(catalog)
	handler := adaptersHTTP.NewModelHandler(mr)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.HandleListModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rec.Code)
	}

	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode /v1/models: %v", err)
	}

	if resp.Object != "list" {
		t.Errorf("expected object 'list', got: %s", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Errorf("models list is empty")
	}

	foundGemini := false
	for _, m := range resp.Data {
		if m.Object != "model" {
			t.Errorf("each model item must have object='model', got: %s", m.Object)
		}
		if m.ID == "gemini-3.8-flash" {
			foundGemini = true
		}
	}
	if !foundGemini {
		t.Errorf("gemini-3.8-flash not found in /v1/models list")
	}
}
