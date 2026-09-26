package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

type GeminiAttachment struct {
	StorageToken string `json:"storage_token"`
	MimeType     string `json:"mime_type,omitempty"`
	FileName     string `json:"file_name,omitempty"`
}

type GroundingSource struct {
	Index   int    `json:"index"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Favicon string `json:"favicon"`
	Domain  string `json:"domain"`
}

type GroundingSupport struct {
	SegmentText   string    `json:"segment_text"`
	StartIndex    int       `json:"start_index"`
	EndIndex      int       `json:"end_index"`
	SourceIndices []int     `json:"source_indices"`
	Scores        []float64 `json:"scores,omitempty"`
}

type GroundingMetadata struct {
	SearchQueries []string           `json:"search_queries"`
	Sources       []GroundingSource  `json:"sources"`
	Supports      []GroundingSupport `json:"supports"`
}

type CodeImage struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64 chuỗi hoặc cdn url
	IsBase64 bool   `json:"is_base64"`
	URL      string `json:"url,omitempty"`
}

type CodeFile struct {
	FileName    string `json:"file_name"`
	DownloadURL string `json:"download_url"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
}

type CodeExecution struct {
	Language            string      `json:"language"`
	Code                string      `json:"code"`
	ExitCode            int         `json:"exit_code"`
	ExecutionDurationMs int64       `json:"execution_duration_ms,omitempty"`
	Stdout              string      `json:"stdout"`
	Stderr              string      `json:"stderr,omitempty"`
	Images              []CodeImage `json:"images,omitempty"`
	Files               []CodeFile  `json:"files,omitempty"`
}

type ThoughtBlock struct {
	Content    string `json:"content"`
	IsThinking bool   `json:"is_thinking"`
}

type MessageContentPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *MessageImageURL `json:"image_url,omitempty"`
}

type MessageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type OpenAIMessage struct {
	Role         string               `json:"role"`
	Content      string               `json:"content"`
	ContentParts []MessageContentPart `json:"content_parts,omitempty"`
}

func (m *OpenAIMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role         string               `json:"role"`
		Content      json.RawMessage      `json:"content"`
		ContentParts []MessageContentPart `json:"content_parts,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.ContentParts = raw.ContentParts

	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}

	// Trường hợp 1: Content là chuỗi văn bản thông thường
	var strContent string
	if err := json.Unmarshal(raw.Content, &strContent); err == nil {
		m.Content = strContent
		return nil
	}

	// Trường hợp 2: Content là mảng đa phương thức [{"type": "text", ...}, {"type": "image_url", ...}]
	var parts []MessageContentPart
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.ContentParts = append(m.ContentParts, parts...)
		var texts []string
		for _, part := range parts {
			if (part.Type == "text" || part.Type == "") && part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		m.Content = strings.Join(texts, "\n")
		return nil
	}

	return fmt.Errorf("không thể giải mã trường content của OpenAIMessage")
}

func (m OpenAIMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{
		Role:    m.Role,
		Content: m.Content,
	})
}

func (m *OpenAIMessage) GetImageURLs() []string {
	if m == nil {
		return nil
	}
	var urls []string
	for _, part := range m.ContentParts {
		if part.Type == "image_url" && part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			urls = append(urls, strings.TrimSpace(part.ImageURL.URL))
		}
	}
	return urls
}

func (m *OpenAIMessage) HasImages() bool {
	return len(m.GetImageURLs()) > 0
}

type OpenAIChatRequest struct {
	Model            string             `json:"model"`
	Messages         []OpenAIMessage    `json:"messages"`
	Stream           bool               `json:"stream"`
	Temperature      float64            `json:"temperature"`
	ConversationID   string             `json:"conversation_id,omitempty"`
	ResponseID       string             `json:"response_id,omitempty"`
	ChoiceID         string             `json:"choice_id,omitempty"`
	ContextBlob      string             `json:"context_blob,omitempty"`
	Thinking         *bool              `json:"thinking,omitempty"`
	SearchGrounding  *bool              `json:"grounding,omitempty"`
	CodeInterpreter  *bool              `json:"code_interpreter,omitempty"`
	Attachments      []GeminiAttachment `json:"attachments,omitempty"`
	ParentResponseID string             `json:"parent_response_id,omitempty"`
	ParentChoiceID   string             `json:"parent_choice_id,omitempty"`
}

func (r *OpenAIChatRequest) GetAllImageURLs() []string {
	if r == nil {
		return nil
	}
	var all []string
	for _, msg := range r.Messages {
		all = append(all, msg.GetImageURLs()...)
	}
	return all
}

func (r *OpenAIChatRequest) HasImages() bool {
	if r == nil {
		return false
	}
	return len(r.GetAllImageURLs()) > 0 || len(r.Attachments) > 0
}

type OpenAIChatResponse struct {
	ID             string             `json:"id"`
	Object         string             `json:"object"`
	Created        int64              `json:"created"`
	Model          string             `json:"model"`
	ConversationID string             `json:"conversation_id,omitempty"`
	ResponseID     string             `json:"response_id,omitempty"`
	ChoiceID       string             `json:"choice_id,omitempty"`
	Choices        []OpenAIChoice     `json:"choices"`
	Thinking       []ThoughtBlock     `json:"thinking,omitempty"`
	Grounding      *GroundingMetadata `json:"grounding,omitempty"`
	CodeExecutions []CodeExecution    `json:"code_executions,omitempty"`
	MediaURLs      []string           `json:"media_urls,omitempty"`
	Usage          *OpenAIUsage       `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message,omitempty"`
	Delta        OpenAIDelta   `json:"delta,omitempty"`
	FinishReason *string       `json:"finish_reason"`
}

type OpenAIDelta struct {
	Role           string             `json:"role,omitempty"`
	Content        string             `json:"content,omitempty"`
	Thinking       string             `json:"thinking,omitempty"`
	Grounding      *GroundingMetadata `json:"grounding,omitempty"`
	CodeExecutions []CodeExecution    `json:"code_executions,omitempty"`
	MediaURLs      []string           `json:"media_urls,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Các hằng số vị trí trong mảng slots của Google Gemini StreamGenerate (đã xác thực trong docs-2)
const (
	SlotUserPrompt       = 0
	SlotLocale           = 1
	SlotContextIDs       = 2
	SlotContextBlob      = 3
	SlotSearchGrounding  = 27
	SlotClientUUID       = 59
	SlotModelTier        = 79
	SlotExtendedThinking = 96
)

// GeminiPayloadBuilder xây dựng mảng nội bộ theo đúng giao thức Google StreamGenerate
type GeminiPayloadBuilder struct {
	UserPrompt            string
	Locale                string
	ConversationID        string // c_...
	ResponseID            string // r_...
	ChoiceID              string // rc_...
	ParentResponseID      string // r_... (dành cho rẽ nhánh DAG / edit turn)
	ParentChoiceID        string // rc_... (dành cho rẽ nhánh DAG / edit turn)
	ContextBlob           string // !...
	ModelTier             int    // 1: Flash, 3: Pro
	EnableThinking        bool
	EnableSearchGrounding bool
	EnableCodeExecution   bool
	Attachments           []GeminiAttachment
	ClientUUID            string
}

// FlattenMessages gộp System Instruction và toàn bộ lịch sử ngữ cảnh
func FlattenMessages(messages []OpenAIMessage) (system string, prompt string) {
	var systemParts []string
	var dialogParts []string

	for i, m := range messages {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "user":
			if i == len(messages)-1 {
				prompt = m.Content
			} else {
				dialogParts = append(dialogParts, "User: "+m.Content)
			}
		case "assistant":
			dialogParts = append(dialogParts, "Assistant: "+m.Content)
		}
	}

	system = strings.Join(systemParts, "\n\n")
	if len(dialogParts) > 0 {
		historyContext := strings.Join(dialogParts, "\n")
		prompt = "[Lịch sử hội thoại trước đó:\n" + historyContext + "]\n\n" + prompt
	}
	if system != "" {
		prompt = "[Chỉ dẫn hệ thống: " + system + "]\n\n" + prompt
	}

	return system, prompt
}
