package domain_test

import (
	"encoding/json"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestGeminiPayloadMatchesNewChatVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Câu hỏi mở đầu cho cuộc trò chuyện mới",
		Locale:         "vi",
		ModelTier:      1,
		EnableThinking: false,
		ClientUUID:     "NEW_CHAT_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.GeminiFixtureSlots(false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, want)
}

func TestGeminiPayloadMatchesThinkingVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Hãy chứng minh định lý Fermat nhỏ và giải thích từng bước tư duy",
		Locale:         "vi",
		ModelTier:      3,
		EnableThinking: true,
		ClientUUID:     "THINKING_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.GeminiFixtureSlots(true)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, want)
}

func TestGeminiPayloadMatchesMultiTurnVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Xin chào Gemini, hôm nay thời tiết thế nào?",
		Locale:         "vi",
		ConversationID: "c_123456789",
		ResponseID:     "r_987654321",
		ChoiceID:       "rc_11223344",
		ContextBlob:    "!context_blob_token",
		ModelTier:      1,
		EnableThinking: false,
		ClientUUID:     "SESSION_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}

	contextIDs, ok := got[domain.SlotContextIDs].([]any)
	if !ok || len(contextIDs) < 3 {
		t.Fatalf("contextIDs slot = %#v", got[domain.SlotContextIDs])
	}
	if contextIDs[0] != "c_123456789" || contextIDs[1] != "r_987654321" || contextIDs[2] != "rc_11223344" {
		t.Fatalf("contextIDs mismatch: %#v", contextIDs)
	}
	if got[domain.SlotContextBlob] != "!context_blob_token" {
		t.Fatalf("contextBlob mismatch: %v", got[domain.SlotContextBlob])
	}
}

func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("got %s\nwant %s", gb, wb)
	}
}
