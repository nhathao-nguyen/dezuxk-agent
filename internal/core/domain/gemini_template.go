package domain

import (
	"encoding/json"
	"errors"
	"sync"
)

// Spec 2026-09-23. Các ô không được gán trong BuildArray là phần bất biến của vector này.
const geminiNewChatFreq = `[null,"[[[\"Câu hỏi mở đầu cho cuộc trò chuyện mới\",0,null,null,null,null,0],[\"vi\"],[\"\",\"\",\"\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"NEW_CHAT_UUID\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,1,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,0,null,1]]"]`

const geminiThinkingFreq = `[null,"[[[\"Hãy chứng minh định lý Fermat nhỏ và giải thích từng bước tư duy\",0,null,null,null,null,0],[\"vi\"],[\"\",\"\",\"\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"THINKING_UUID\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,3,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,1,null,1]]"]`

const (
	geminiFixturePrompt   = "Câu hỏi mở đầu cho cuộc trò chuyện mới"
	geminiFixtureThinking = "Hãy chứng minh định lý Fermat nhỏ và giải thích từng bước tư duy"
	geminiFixtureUUID     = "NEW_CHAT_UUID"
	geminiFixtureThinkID  = "THINKING_UUID"
)

var (
	geminiTemplateOnce sync.Once
	geminiBaseSlots    []any
	geminiThinkSlots   []any
	geminiTemplateErr  error
)

func loadGeminiTemplates() {
	geminiBaseSlots, geminiTemplateErr = geminiSlotsFromFreq(geminiNewChatFreq)
	if geminiTemplateErr != nil {
		return
	}
	geminiThinkSlots, geminiTemplateErr = geminiSlotsFromFreq(geminiThinkingFreq)
}

func geminiSlotsFromFreq(freq string) ([]any, error) {
	var outer []any
	if err := json.Unmarshal([]byte(freq), &outer); err != nil {
		return nil, err
	}
	if len(outer) < 2 {
		return nil, errors.New("freq chat thiếu phần inner")
	}
	inner, ok := outer[1].(string)
	if !ok {
		return nil, errors.New("freq chat thiếu phần inner")
	}
	var slots []any
	if err := json.Unmarshal([]byte(inner), &slots); err != nil {
		return nil, err
	}
	if len(slots) == 1 {
		if wrapped, ok := slots[0].([]any); ok && len(wrapped) > 10 {
			return wrapped, nil
		}
	}
	return slots, nil
}

func cloneGeminiSlots(thinking bool) ([]any, error) {
	geminiTemplateOnce.Do(loadGeminiTemplates)
	if geminiTemplateErr != nil {
		return nil, geminiTemplateErr
	}
	source := geminiBaseSlots
	if thinking {
		source = geminiThinkSlots
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var slots []any
	if err := json.Unmarshal(raw, &slots); err != nil {
		return nil, err
	}
	return slots, nil
}

// GeminiFixtureSlots trả về bản sao vector chat để test đối chiếu.
func GeminiFixtureSlots(thinking bool) ([]any, error) {
	return cloneGeminiSlots(thinking)
}

// BuildArray điền các ô động đã đặt tên. Khung hỏng hoặc ngắn hơn ô cuối cùng thì trả lỗi, không trả mảng rỗng.
func (b *GeminiPayloadBuilder) BuildArray() ([]any, error) {
	slots, err := cloneGeminiSlots(b.EnableThinking)
	if err != nil {
		return nil, err
	}
	return b.applySlots(slots)
}

func (b *GeminiPayloadBuilder) applySlots(slots []any) ([]any, error) {
	if len(slots) <= SlotExtendedThinking {
		return nil, errors.New("khung chat không đủ ô")
	}
	locale := b.Locale
	if locale == "" {
		locale = "en"
	}

	// SlotUserPrompt [0]
	if len(b.Attachments) > 0 {
		var attachList []any
		for _, att := range b.Attachments {
			attachList = append(attachList, []any{[]any{att.StorageToken, 1}})
		}
		setGeminiSlot(slots, SlotUserPrompt, []any{b.UserPrompt, 0, nil, attachList, nil, nil, 0})
	} else {
		setGeminiSlot(slots, SlotUserPrompt, []any{b.UserPrompt, 0, nil, nil, nil, nil, 0})
	}

	setGeminiSlot(slots, SlotLocale, []any{locale})

	// SlotContextIDs [2]
	respID := b.ResponseID
	choiceID := b.ChoiceID
	if b.ParentResponseID != "" {
		respID = b.ParentResponseID
		choiceID = b.ParentChoiceID
	}
	setGeminiSlot(slots, SlotContextIDs, []any{b.ConversationID, respID, choiceID, nil, nil, nil, nil, nil, nil, ""})

	if b.ContextBlob != "" {
		setGeminiSlot(slots, SlotContextBlob, b.ContextBlob)
	}

	// SlotSearchGrounding [27]: 1 bật, 0 tắt (số nguyên, không phải slice)
	if b.EnableSearchGrounding || b.EnableCodeExecution {
		setGeminiSlot(slots, SlotSearchGrounding, 1)
	}

	setGeminiSlot(slots, SlotClientUUID, b.ClientUUID)
	setGeminiSlot(slots, SlotModelTier, b.ModelTier)
	thinking := 0
	if b.EnableThinking {
		thinking = 1
	}
	setGeminiSlot(slots, SlotExtendedThinking, thinking)
	return slots, nil
}

func setGeminiSlot(slots []any, index int, value any) {
	if index >= 0 && index < len(slots) {
		slots[index] = value
	}
}
