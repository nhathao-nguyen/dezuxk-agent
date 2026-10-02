package domain

import (
	"encoding/json"
	"errors"
	"os"
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
	geminiTemplateMu   sync.RWMutex
	geminiBaseSlots    []any
	geminiThinkSlots   []any
	geminiTemplateErr  error
	geminiTemplatesInit bool
)

// GeminiTemplateConfig định nghĩa cấu trúc lưu trữ chuỗi template cho gemini
type GeminiTemplateConfig struct {
	Description     string `json:"description,omitempty"`
	Version         string `json:"version,omitempty"`
	NewChatTemplate string `json:"new_chat_template"`
	ThinkingTemplate string `json:"thinking_template"`
}

// LoadGeminiTemplatesFromFile nạp schema Gemini từ file JSON bên ngoài
func LoadGeminiTemplatesFromFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var cfg GeminiTemplateConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	return SetGeminiTemplates(cfg.NewChatTemplate, cfg.ThinkingTemplate)
}

// SetGeminiTemplates cập nhật trực tiếp chuỗi mẫu new_chat và thinking vào runtime
func SetGeminiTemplates(newChatFreq, thinkingFreq string) error {
	baseSlots, err := geminiSlotsFromFreq(newChatFreq)
	if err != nil {
		return err
	}
	thinkSlots, err := geminiSlotsFromFreq(thinkingFreq)
	if err != nil {
		return err
	}

	geminiTemplateMu.Lock()
	geminiBaseSlots = baseSlots
	geminiThinkSlots = thinkSlots
	geminiTemplateErr = nil
	geminiTemplatesInit = true
	geminiTemplateMu.Unlock()
	return nil
}

func loadGeminiTemplates() {
	geminiTemplateMu.Lock()
	defer geminiTemplateMu.Unlock()

	if geminiTemplatesInit {
		return
	}

	// Thử nạp từ configs/gemini_template.json nếu tồn tại
	candidates := []string{
		"configs/gemini_template.json",
		"../configs/gemini_template.json",
		"../../configs/gemini_template.json",
	}
	if envPath := os.Getenv("GEMINI_TEMPLATE_CONFIG_PATH"); envPath != "" {
		candidates = append([]string{envPath}, candidates...)
	}

	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			var cfg GeminiTemplateConfig
			if err := json.Unmarshal(data, &cfg); err == nil && cfg.NewChatTemplate != "" && cfg.ThinkingTemplate != "" {
				base, err1 := geminiSlotsFromFreq(cfg.NewChatTemplate)
				think, err2 := geminiSlotsFromFreq(cfg.ThinkingTemplate)
				if err1 == nil && err2 == nil {
					geminiBaseSlots = base
					geminiThinkSlots = think
					geminiTemplateErr = nil
					geminiTemplatesInit = true
					return
				}
			}
		}
	}

	// Fallback sang mẫu cố định đã được kiểm thử
	geminiBaseSlots, geminiTemplateErr = geminiSlotsFromFreq(geminiNewChatFreq)
	if geminiTemplateErr != nil {
		geminiTemplatesInit = true
		return
	}
	geminiThinkSlots, geminiTemplateErr = geminiSlotsFromFreq(geminiThinkingFreq)
	geminiTemplatesInit = true
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
	geminiTemplateMu.RLock()
	init := geminiTemplatesInit
	geminiTemplateMu.RUnlock()

	if !init {
		loadGeminiTemplates()
	}

	geminiTemplateMu.RLock()
	defer geminiTemplateMu.RUnlock()

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
