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
	File     *MessageImageURL `json:"file,omitempty"`
	Document *MessageImageURL `json:"document,omitempty"`
}

type MessageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type OpenAITool struct {
	Type     string            `json:"type"`
	Function OpenAIFunctionDef `json:"function"`
}

type OpenAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type OpenAIToolCall struct {
	Index    int                    `json:"index,omitempty"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function OpenAIFunctionCallData `json:"function"`
}

type OpenAIFunctionCallData struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAIMessage struct {
	Role             string               `json:"role"`
	Content          string               `json:"content"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
	ContentParts     []MessageContentPart `json:"content_parts,omitempty"`
	ToolCalls        []OpenAIToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string               `json:"tool_call_id,omitempty"`
}

func (m *OpenAIMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role             string               `json:"role"`
		Content          json.RawMessage      `json:"content"`
		ReasoningContent string               `json:"reasoning_content,omitempty"`
		ContentParts     []MessageContentPart `json:"content_parts,omitempty"`
		ToolCalls        []OpenAIToolCall     `json:"tool_calls,omitempty"`
		ToolCallID       string               `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.ReasoningContent = raw.ReasoningContent
	m.ContentParts = raw.ContentParts
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID

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
		Role             string           `json:"role"`
		Content          string           `json:"content"`
		ReasoningContent string           `json:"reasoning_content,omitempty"`
		ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string           `json:"tool_call_id,omitempty"`
	}{
		Role:             m.Role,
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
	})
}

func (m *OpenAIMessage) GetImageURLs() []string {
	if m == nil {
		return nil
	}
	var urls []string
	for _, part := range m.ContentParts {
		if part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			urls = append(urls, strings.TrimSpace(part.ImageURL.URL))
		} else if part.File != nil && strings.TrimSpace(part.File.URL) != "" {
			urls = append(urls, strings.TrimSpace(part.File.URL))
		} else if part.Document != nil && strings.TrimSpace(part.Document.URL) != "" {
			urls = append(urls, strings.TrimSpace(part.Document.URL))
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
	Tools            []OpenAITool       `json:"tools,omitempty"`
	ToolChoice       any                `json:"tool_choice,omitempty"`
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
	Role             string             `json:"role,omitempty"`
	Content          string             `json:"content,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	Thinking         string             `json:"thinking,omitempty"`
	ToolCalls        []OpenAIToolCall   `json:"tool_calls,omitempty"`
	Grounding        *GroundingMetadata `json:"grounding,omitempty"`
	CodeExecutions   []CodeExecution    `json:"code_executions,omitempty"`
	MediaURLs        []string           `json:"media_urls,omitempty"`
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

// ModelIdentityInstruction trả về System Instruction nhận diện cho từng model để model tự biết danh tính chuẩn xác
func ModelIdentityInstruction(modelID string) string {
	m := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(m, "flash-lite") || strings.Contains(m, "3.5"):
		return "You are Gemini 3.5 Flash-Lite, Google's fastest high-efficiency model. When asked about your identity or model name, identify as Gemini 3.5 Flash-Lite."
	case strings.Contains(m, "3.1-pro") || strings.Contains(m, "pro"):
		return "You are Gemini 3.1 Pro, Google's advanced reasoning model. When asked about your identity or model name, identify as Gemini 3.1 Pro."
	case strings.Contains(m, "3.8-flash") || strings.Contains(m, "flash"):
		return "You are Gemini 3.8 Flash, Google's versatile multimodal model. When asked about your identity or model name, identify as Gemini 3.8 Flash."
	default:
		return ""
	}
}

// FlattenMessages gộp System Instruction và toàn bộ lịch sử ngữ cảnh
func FlattenMessages(messages []OpenAIMessage) (system string, prompt string) {
	return FlattenMessagesForModel(messages, "")
}

// FlattenMessagesForModel gộp System Instruction, tiêm danh tính mô hình chính xác (nếu có), và toàn bộ lịch sử ngữ cảnh
func FlattenMessagesForModel(messages []OpenAIMessage, modelID string) (system string, prompt string) {
	var systemParts []string
	var dialogParts []string

	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			lastUserIdx = i
			break
		}
	}

	for i, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "user":
			if i == lastUserIdx {
				prompt = m.Content
			} else if strings.TrimSpace(m.Content) != "" {
				dialogParts = append(dialogParts, "User: "+m.Content)
			}
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				dialogParts = append(dialogParts, "Assistant: "+m.Content)
			}
		default:
			if strings.TrimSpace(m.Content) != "" {
				dialogParts = append(dialogParts, m.Role+": "+m.Content)
			}
		}
	}

	// Tiêm System Instruction định danh model nếu modelID được truyền vào
	if identity := ModelIdentityInstruction(modelID); identity != "" {
		hasIdentity := false
		for _, sp := range systemParts {
			if strings.Contains(sp, identity) {
				hasIdentity = true
				break
			}
		}
		if !hasIdentity {
			systemParts = append([]string{identity}, systemParts...)
		}
	}

	// Fallback nếu không có message nào role user
	if prompt == "" && len(messages) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role != "system" && strings.TrimSpace(messages[i].Content) != "" {
				prompt = messages[i].Content
				break
			}
		}
	}

	system = strings.Join(systemParts, "\n\n")
	if len(dialogParts) > 0 {
		historyContext := strings.Join(dialogParts, "\n")
		prompt = "[Conversation History:\n" + historyContext + "]\n\n" + prompt
	}
	if system != "" {
		prompt = "[System Instruction: " + system + "]\n\n" + prompt
	}

	return system, prompt
}
