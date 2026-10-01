package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestGeminiPayloadBuilder_ThinkingMode(t *testing.T) {
	builderThinking := domain.GeminiPayloadBuilder{
		UserPrompt:     "Test Prompt Thinking",
		Locale:         "vi",
		ModelTier:      3,
		EnableThinking: true,
		ClientUUID:     "test-uuid-thinking",
	}

	slots, err := builderThinking.BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) < 99 {
		t.Fatalf("expected at least 99 slots, got %d", len(slots))
	}

	// Index 79: Model tier (3 for Pro)
	if tier, ok := slots[domain.SlotModelTier].(int); !ok || tier != 3 {
		t.Errorf("expected slots[%d] (ModelTier) to be 3, got %v", domain.SlotModelTier, slots[domain.SlotModelTier])
	}

	// Index 96: Extended Thinking Flag must be 1
	if flag, ok := slots[domain.SlotExtendedThinking].(int); !ok || flag != 1 {
		t.Errorf("expected slots[%d] (ExtendedThinking) to be 1 when enabled, got %v", domain.SlotExtendedThinking, slots[domain.SlotExtendedThinking])
	}

	// Test when thinking is disabled
	builderNormal := domain.GeminiPayloadBuilder{
		UserPrompt:     "Test Prompt Normal",
		Locale:         "en",
		ModelTier:      1,
		EnableThinking: false,
		ClientUUID:     "test-uuid-normal",
	}

	slotsNormal, err := builderNormal.BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	if flag, ok := slotsNormal[domain.SlotExtendedThinking].(int); !ok || flag != 0 {
		t.Errorf("expected slots[%d] (ExtendedThinking) to be 0 when disabled, got %v", domain.SlotExtendedThinking, slotsNormal[domain.SlotExtendedThinking])
	}
}

func TestGeminiPayloadBuilder_NewChatContext(t *testing.T) {
	builder := domain.GeminiPayloadBuilder{
		UserPrompt: "New Chat",
	}
	slots, err := builder.BuildArray()
	if err != nil {
		t.Fatal(err)
	}

	ctxArray, ok := slots[domain.SlotContextIDs].([]interface{})
	if !ok {
		t.Fatalf("expected slots[%d] to be an array, got %T", domain.SlotContextIDs, slots[domain.SlotContextIDs])
	}
	if len(ctxArray) < 3 {
		t.Fatalf("expected at least 3 context elements, got %d", len(ctxArray))
	}
	if ctxArray[0] != "" || ctxArray[1] != "" || ctxArray[2] != "" {
		t.Errorf("expected context IDs to be empty strings for new chat, got %v", ctxArray)
	}
}

func TestDefaultRpcRegistry(t *testing.T) {
	reg := domain.DefaultRpcRegistry()
	if reg == nil {
		t.Fatal("expected DefaultRpcRegistry to return non-nil registry")
	}

	expectedRPCs := []string{
		"StreamGenerate",
		"batchexecute_gemini",
		"usage",
		"I4z33b",
		"MaZiqc",
		"cZOhpc",
		"PCck7e",
		"VxUbXb",
		"uP80Sb",
		"wEb32b",
		"tVk3Sc",
		"sA4a8",
		"H8s0fe",
		"whPPme",
		"GPRiHf",
		"upload_handshake",
	}

	for _, rpcID := range expectedRPCs {
		ep, ok := reg.Get(rpcID)
		if !ok {
			t.Errorf("expected RPC %q in DefaultRpcRegistry, but not found", rpcID)
		}
		if ep.PathPattern == "" {
			t.Errorf("expected non-empty PathPattern for RPC %q", rpcID)
		}
	}
}

func TestOpenAIMessage_MultimodalParsing(t *testing.T) {
	// 1. Text-only message
	textJSON := `{"role": "user", "content": "Hello Gemini"}`
	var msg1 domain.OpenAIMessage
	if err := json.Unmarshal([]byte(textJSON), &msg1); err != nil {
		t.Fatalf("failed to unmarshal text message: %v", err)
	}
	if msg1.Content != "Hello Gemini" {
		t.Errorf("expected content 'Hello Gemini', got %q", msg1.Content)
	}
	if msg1.HasImages() {
		t.Error("expected no images in text message")
	}

	// 2. Multimodal message with text and image_url
	multiJSON := `{
		"role": "user",
		"content": [
			{"type": "text", "text": "What is in this picture?"},
			{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}},
			{"type": "image_url", "image_url": {"url": "https://example.com/test.jpg"}}
		]
	}`
	var msg2 domain.OpenAIMessage
	if err := json.Unmarshal([]byte(multiJSON), &msg2); err != nil {
		t.Fatalf("failed to unmarshal multimodal message: %v", err)
	}
	if msg2.Content != "What is in this picture?" {
		t.Errorf("expected extracted text 'What is in this picture?', got %q", msg2.Content)
	}
	if !msg2.HasImages() {
		t.Error("expected images in multimodal message")
	}
	urls := msg2.GetImageURLs()
	if len(urls) != 2 {
		t.Fatalf("expected 2 image URLs, got %d", len(urls))
	}
	if !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("expected data URL for first image, got %s", urls[0])
	}
	if urls[1] != "https://example.com/test.jpg" {
		t.Errorf("expected http URL for second image, got %s", urls[1])
	}

	// 3. Serialization of OpenAIMessage back to JSON
	serialized, err := json.Marshal(msg2)
	if err != nil {
		t.Fatalf("failed to marshal OpenAIMessage: %v", err)
	}
	var outMap map[string]string
	if err := json.Unmarshal(serialized, &outMap); err != nil {
		t.Fatalf("failed to unmarshal serialized message: %v", err)
	}
	if outMap["role"] != "user" || outMap["content"] != "What is in this picture?" {
		t.Errorf("unexpected serialized output: %v", outMap)
	}
}

func TestGeminiPayloadBuilder_Attachments(t *testing.T) {
	builder := domain.GeminiPayloadBuilder{
		UserPrompt: "Phân tích bức ảnh này",
		Locale:     "vi",
		ModelTier:  1,
		Attachments: []domain.GeminiAttachment{
			{StorageToken: "/contrib_service/ttl_1d/sample_token_123", MimeType: "image/png"},
		},
	}
	slots, err := builder.BuildArray()
	if err != nil {
		t.Fatal(err)
	}

	userPromptSlot, ok := slots[domain.SlotUserPrompt].([]any)
	if !ok {
		t.Fatalf("expected slots[0] to be array, got %T", slots[domain.SlotUserPrompt])
	}
	// userPromptSlot: [prompt, 0, nil, attachList, nil, nil, 0]
	if len(userPromptSlot) < 4 {
		t.Fatalf("userPromptSlot length too short: %d", len(userPromptSlot))
	}
	attachList, ok := userPromptSlot[3].([]any)
	if !ok {
		t.Fatalf("expected userPromptSlot[3] to be attachList, got %T", userPromptSlot[3])
	}
	if len(attachList) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachList))
	}
	firstAttach, ok := attachList[0].([]any)
	if !ok {
		t.Fatalf("expected attach item to be array, got %T", attachList[0])
	}
	item, ok := firstAttach[0].([]any)
	if !ok {
		t.Fatalf("expected inner item to be array, got %T", firstAttach[0])
	}
	if len(item) < 2 || item[0] != "/contrib_service/ttl_1d/sample_token_123" || item[1] != 1 {
		t.Fatalf("unexpected attachment inner format: %v", item)
	}
}

func TestFlattenMessages(t *testing.T) {
	// Trường hợp 1: Chuẩn OpenAI user -> assistant -> user
	messages1 := []domain.OpenAIMessage{
		{Role: "system", Content: "You are an AI assistant"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi, how can I help you?"},
		{Role: "user", Content: "What is 1+1?"},
	}
	sys, prompt := domain.FlattenMessages(messages1)
	if sys != "You are an AI assistant" {
		t.Errorf("expected system message to match, got %q", sys)
	}
	if !strings.Contains(prompt, "What is 1+1?") {
		t.Errorf("expected prompt to contain last user message, got %q", prompt)
	}
	if !strings.Contains(prompt, "Hello") || !strings.Contains(prompt, "Hi, how can I help you?") {
		t.Errorf("expected history context in prompt, got %q", prompt)
	}

	// Trường hợp 2: Tin nhắn cuối không phải user (assistant prefill)
	messages2 := []domain.OpenAIMessage{
		{Role: "system", Content: "Be concise"},
		{Role: "user", Content: "Translate to Vietnamese: Apple"},
		{Role: "assistant", Content: "Quả"},
	}
	_, prompt2 := domain.FlattenMessages(messages2)
	if !strings.Contains(prompt2, "Translate to Vietnamese: Apple") {
		t.Errorf("expected prompt2 to fallback to previous user message, got %q", prompt2)
	}
}

func TestFlattenMessagesForModel(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{Role: "user", Content: "Bạn là model gì?"},
	}

	// 1. Flash-Lite
	sys1, prompt1 := domain.FlattenMessagesForModel(messages, "gemini-3.5-flash-lite")
	if !strings.Contains(sys1, "Gemini 3.5 Flash-Lite") {
		t.Errorf("expected 3.5 Flash-Lite in system, got: %s", sys1)
	}
	if !strings.Contains(prompt1, "Gemini 3.5 Flash-Lite") {
		t.Errorf("expected 3.5 Flash-Lite in prompt, got: %s", prompt1)
	}

	// 2. Flash
	sys2, _ := domain.FlattenMessagesForModel(messages, "gemini-3.8-flash")
	if !strings.Contains(sys2, "Gemini 3.8 Flash") {
		t.Errorf("expected 3.8 Flash in system, got: %s", sys2)
	}

	// 3. Pro
	sys3, _ := domain.FlattenMessagesForModel(messages, "gemini-3.1-pro")
	if !strings.Contains(sys3, "Gemini 3.1 Pro") {
		t.Errorf("expected 3.1 Pro in system, got: %s", sys3)
	}

	// 4. Kèm custom system message
	customMessages := []domain.OpenAIMessage{
		{Role: "system", Content: "Bạn là chuyên gia lập trình Go"},
		{Role: "user", Content: "Xin chào"},
	}
	sysCustom, _ := domain.FlattenMessagesForModel(customMessages, "gemini-3.5-flash-lite")
	if !strings.Contains(sysCustom, "Gemini 3.5 Flash-Lite") || !strings.Contains(sysCustom, "chuyên gia lập trình Go") {
		t.Errorf("expected both identity and custom system, got: %s", sysCustom)
	}
}

func TestOpenAIChatRequest_ToolSerialization(t *testing.T) {
	rawJSON := `{
		"model": "gemini-3.8-flash",
		"messages": [
			{
				"role": "user",
				"content": "Hãy đọc file main.go"
			},
			{
				"role": "assistant",
				"content": "Tôi sẽ đọc file.",
				"reasoning_content": "Cần dùng tool read_file",
				"tool_calls": [
					{
						"id": "call_123",
						"type": "function",
						"function": {
							"name": "read_file",
							"arguments": "{\"path\":\"main.go\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_123",
				"content": "package main\n\nfunc main() {}"
			}
		],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "read_file",
					"description": "Read file",
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
		"tool_choice": "auto"
	}`

	var req domain.OpenAIChatRequest
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %s", req.Tools[0].Function.Name)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.ReasoningContent != "Cần dùng tool read_file" {
		t.Errorf("expected reasoning_content, got %s", asst.ReasoningContent)
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(asst.ToolCalls))
	}
	if asst.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("expected function name read_file, got %s", asst.ToolCalls[0].Function.Name)
	}
	if req.Messages[2].ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id call_123, got %s", req.Messages[2].ToolCallID)
	}

	// Verify Delta marshaling with tool calls and reasoning
	delta := domain.OpenAIDelta{
		Role:             "assistant",
		ReasoningContent: "Thinking step 1",
		ToolCalls: []domain.OpenAIToolCall{
			{
				Index: 0,
				ID:    "call_abc",
				Type:  "function",
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: "{\"path\":\"foo.go\"}",
				},
			},
		},
	}
	deltaBytes, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("Delta marshal failed: %v", err)
	}
	deltaStr := string(deltaBytes)
	if !strings.Contains(deltaStr, "reasoning_content") || !strings.Contains(deltaStr, "tool_calls") {
		t.Errorf("expected delta JSON to contain reasoning_content and tool_calls, got: %s", deltaStr)
	}
}

func TestFlattenMessages_WithToolCallsAndResults(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{
			Role:    "user",
			Content: "Đọc file main.go giúp tôi",
		},
		{
			Role:    "assistant",
			Content: "Đang đọc file...",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID:   "call_999",
					Type: "function",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: `{"path":"main.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_999",
			Content:    "package main\nfunc main() {}",
		},
		{
			Role:    "user",
			Content: "Bây giờ hãy sửa hàm main để in Hello",
		},
	}

	_, prompt := domain.FlattenMessagesForModel(messages, "gemini-3.8-flash")

	// Verify tool result formatting
	if !strings.Contains(prompt, "[Tool Result (call_id: call_999)]") {
		t.Errorf("expected prompt to contain '[Tool Result (call_id: call_999)]', got: %s", prompt)
	}
	// Verify assistant tool invocation formatting
	if !strings.Contains(prompt, "[Invoked Tool read_file") {
		t.Errorf("expected prompt to contain '[Invoked Tool read_file', got: %s", prompt)
	}
	// Verify last user prompt
	if !strings.Contains(prompt, "Bây giờ hãy sửa hàm main để in Hello") {
		t.Errorf("expected prompt to end with current user prompt, got: %s", prompt)
	}
}

func TestFlattenMessages_AgentLoopLastIsToolResult(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{
			Role:    "user",
			Content: "Kiểm tra và sửa lỗi trong file server.go",
		},
		{
			Role:    "assistant",
			Content: "Tôi sẽ đọc file trước.",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID:   "call_abc",
					Type: "function",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: `{"path":"server.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_abc",
			Content:    "syntax error at line 42: undefined variable",
		},
	}

	_, prompt := domain.FlattenMessagesForModel(messages, "gemini-3.8-flash")

	// Đảm bảo tin nhắn user gốc nằm trong Conversation History
	if !strings.Contains(prompt, "User: Kiểm tra và sửa lỗi trong file server.go") {
		t.Errorf("expected original user prompt in history, got: %s", prompt)
	}
	// Đảm bảo kết quả tool nằm trong Conversation History
	if !strings.Contains(prompt, "[Tool Result (call_id: call_abc)]") {
		t.Errorf("expected tool result in history, got: %s", prompt)
	}
	// Đảm bảo prompt cuối cùng hướng dẫn Gemini tiếp tục dựa trên kết quả tool và mục tiêu của user
	if !strings.Contains(prompt, "[Tool execution completed") || !strings.Contains(prompt, "Kiểm tra và sửa lỗi trong file server.go") {
		t.Errorf("expected tool execution completed prompt with user goal, got: %s", prompt)
	}
}

func TestFlattenMessagesForModelWithContext_RemoteHistoryOptimization(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{Role: "user", Content: "Tôi tên là An."},
		{Role: "assistant", Content: "Chào An! Rất vui được gặp bạn."},
		{Role: "user", Content: "Hôm nay tôi bao nhiêu tuổi?"},
	}

	// 1. Khi chưa có remote history (Turn đầu hoặc stateless): phải bao gồm toàn bộ lịch sử
	_, promptStateless := domain.FlattenMessagesForModelWithContext(messages, "gemini-3.8-flash", false)
	if !strings.Contains(promptStateless, "User: Tôi tên là An.") {
		t.Errorf("expected history in stateless prompt, got: %s", promptStateless)
	}
	if !strings.Contains(promptStateless, "Assistant: Chào An! Rất vui được gặp bạn.") {
		t.Errorf("expected assistant history in stateless prompt, got: %s", promptStateless)
	}

	// 2. Khi đã có remote history trên Google (hasRemoteHistory == true): bỏ qua các lượt cũ, chỉ gửi lượt mới
	_, promptOptimized := domain.FlattenMessagesForModelWithContext(messages, "gemini-3.8-flash", true)
	if strings.Contains(promptOptimized, "User: Tôi tên là An.") {
		t.Errorf("expected previous turn to be omitted when remote history exists, got: %s", promptOptimized)
	}
	if strings.Contains(promptOptimized, "Assistant: Chào An! Rất vui được gặp bạn.") {
		t.Errorf("expected previous assistant turn to be omitted when remote history exists, got: %s", promptOptimized)
	}
	if !strings.Contains(promptOptimized, "Hôm nay tôi bao nhiêu tuổi?") {
		t.Errorf("expected current prompt to be preserved, got: %s", promptOptimized)
	}
}



