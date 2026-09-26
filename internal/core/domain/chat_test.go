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
		"nzlxg",
		"cPZSdc",
		"HTrJv",
		"yBhWQ",
		"csbIsb",
		"jHPbke",
		"ngNC2",
		"Zzl0ze",
		"uW3g7e",
		"StreamChat",
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

