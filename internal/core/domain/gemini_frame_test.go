package domain

import "testing"

func TestApplySlotsRejectsShortFrame(t *testing.T) {
	builder := &GeminiPayloadBuilder{UserPrompt: "x", Locale: "vi"}
	if _, err := builder.applySlots(nil); err == nil {
		t.Fatal("khung nil phải trả lỗi")
	}
	if _, err := builder.applySlots(make([]any, SlotExtendedThinking)); err == nil {
		t.Fatal("khung ngắn hơn ô thinking phải trả lỗi")
	}
}
