package http_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestOpenAISDKRequestDeserialization(t *testing.T) {
	// JSON payload được sinh bởi official OpenAI Python SDK:
	// client.chat.completions.create(model="gemini-3.8-flash", messages=[...], tools=[...], tool_choice="auto", max_tokens=1000, top_p=0.9, user="user_123", seed=42)
	sdkPayload := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{"role": "system", "content": "You are a helpful coding assistant."},
			{"role": "user", "content": "Check files in directory"}
		],
		"stream": false,
		"temperature": 0.7,
		"top_p": 0.9,
		"max_tokens": 1000,
		"max_completion_tokens": 1000,
		"user": "user_123",
		"seed": 42,
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "list_directory",
					"description": "List directory contents",
					"parameters": {
						"type": "object",
						"properties": {
							"path": {"type": "string"}
						},
						"required": ["path"]
					}
				}
			}
		],
		"tool_choice": "auto",
		"response_format": {"type": "json_object"}
	}`

	var req domain.OpenAIChatRequest
	if err := json.Unmarshal([]byte(sdkPayload), &req); err != nil {
		t.Fatalf("failed to deserialize OpenAI SDK request: %v", err)
	}

	if req.Model != "gemini-3.8-flash" {
		t.Errorf("model mismatch: %s", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Errorf("messages length mismatch: %d", len(req.Messages))
	}
	if req.MaxTokens == nil || *req.MaxTokens != 1000 {
		t.Errorf("max_tokens mismatch")
	}
	if req.TopP == nil || *req.TopP != 0.9 {
		t.Errorf("top_p mismatch")
	}
	if req.User != "user_123" {
		t.Errorf("user mismatch: %s", req.User)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "list_directory" {
		t.Errorf("tools mismatch: %+v", req.Tools)
	}
}

func TestOpenAISDKResponseSerialization(t *testing.T) {
	// Kiểm tra đối tượng phản hồi tuần tự hóa chuẩn OpenAI SDK
	stopReason := "tool_calls"
	resp := domain.OpenAIChatResponse{
		ID:      "chatcmpl-test-conformance-123",
		Object:  "chat.completion",
		Created: 1727788800,
		Model:   "gemini-3.8-flash",
		Choices: []domain.OpenAIChoice{
			{
				Index: 0,
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: "Tôi sẽ đọc file cho bạn.",
					ToolCalls: []domain.OpenAIToolCall{
						{
							Index: 0,
							ID:    "call_abc123",
							Type:  "function",
							Function: domain.OpenAIFunctionCallData{
								Name:      "read_file",
								Arguments: `{"path": "main.go"}`,
							},
						},
					},
				},
				FinishReason: &stopReason,
			},
		},
		Usage: &domain.OpenAIUsage{
			PromptTokens:     50,
			CompletionTokens: 25,
			TotalTokens:      75,
		},
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal OpenAIChatResponse: %v", err)
	}

	// Đọc lại bằng cấu trúc mong đợi của OpenAI SDK
	var decoded map[string]any
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if decoded["object"] != "chat.completion" {
		t.Errorf("object must be chat.completion, got: %v", decoded["object"])
	}
	if !strings.HasPrefix(decoded["id"].(string), "chatcmpl-") {
		t.Errorf("id must start with chatcmpl-, got: %v", decoded["id"])
	}

	choices, ok := decoded["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("choices array missing or empty")
	}

	firstChoice := choices[0].(map[string]any)
	if firstChoice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason must be tool_calls, got: %v", firstChoice["finish_reason"])
	}

	msg := firstChoice["message"].(map[string]any)
	if msg["role"] != "assistant" {
		t.Errorf("message role must be assistant, got: %v", msg["role"])
	}

	toolCalls := msg["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls must have 1 element, got: %d", len(toolCalls))
	}

	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_abc123" || tc["type"] != "function" {
		t.Errorf("tool_call structure mismatch: %+v", tc)
	}

	fn := tc["function"].(map[string]any)
	if fn["name"] != "read_file" || fn["arguments"] != `{"path": "main.go"}` {
		t.Errorf("function call data mismatch: %+v", fn)
	}

	usage := decoded["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 75 {
		t.Errorf("usage total_tokens mismatch: %v", usage["total_tokens"])
	}
}

func TestOpenAISDKErrorFormatting(t *testing.T) {
	ge := domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "Tham số model không hợp lệ")

	payload := map[string]any{
		"error": map[string]string{
			"message": ge.Message,
			"type":    string(ge.Class),
			"code":    string(ge.Class),
		},
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal error payload: %v", err)
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal standard OpenAI error: %v", err)
	}

	if parsed.Error.Message != "Tham số model không hợp lệ" {
		t.Errorf("error message mismatch: %s", parsed.Error.Message)
	}
	if parsed.Error.Type == "" || parsed.Error.Code == "" {
		t.Errorf("error type or code must not be empty")
	}
}

func TestOpenAIToolChoiceEnforcement(t *testing.T) {
	allowedTools := []domain.OpenAITool{
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:       "get_current_weather",
				Parameters: []byte(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
			},
		},
	}

	// Case 1: Model gọi đúng tool theo tool_choice auto
	calls := []domain.OpenAIToolCall{
		{
			Function: domain.OpenAIFunctionCallData{
				Name:      "get_current_weather",
				Arguments: `{"location": "Hà Nội"}`,
			},
		},
	}
	normCalls, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(normCalls) != 1 {
		t.Fatalf("expected 1 normalized tool call, got %d", len(normCalls))
	}

	// Case 2: Model trả về tool call nhưng tool_choice là "none" -> phải loại bỏ
	noneCalls, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(noneCalls) != 0 {
		t.Fatalf("expected 0 calls when tool_choice is none, got %d", len(noneCalls))
	}
}
