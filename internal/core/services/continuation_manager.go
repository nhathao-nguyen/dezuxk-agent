package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// CompletionState mô tả trạng thái hoàn tất của phản hồi văn bản / mô hình
type CompletionState struct {
	IsComplete     bool     `json:"is_complete"`
	Reason         string   `json:"reason"`
	StoppingPoint  string   `json:"stopping_point,omitempty"`
	UnclosedBlocks []string `json:"unclosed_blocks,omitempty"`
	ExpectedFormat string   `json:"expected_format,omitempty"`
}

// CompletionDetector kiểm tra tính nguyên vẹn của đầu ra mô hình
type CompletionDetector struct{}

// NewCompletionDetector khởi tạo CompletionDetector
func NewCompletionDetector() *CompletionDetector {
	return &CompletionDetector{}
}

// Analyze kiểm tra nội dung phản hồi và finish_reason để phát hiện phản hồi bị cắt ngang
func (d *CompletionDetector) Analyze(text string, finishReason string, expectedFormat string) CompletionState {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return CompletionState{
			IsComplete: false,
			Reason:     "empty_response",
		}
	}

	// 1. finish_reason = "length" là tín hiệu rõ ràng từ upstream về việc vượt quá output token budget
	if strings.EqualFold(finishReason, "length") {
		return CompletionState{
			IsComplete:     false,
			Reason:         "finish_reason_length",
			StoppingPoint:  extractStoppingPoint(trimmed),
			UnclosedBlocks: detectUnclosedMarkdownCodeBlocks(trimmed),
			ExpectedFormat: expectedFormat,
		}
	}

	// 2. Kiểm tra khối mã Markdown chưa đóng (```)
	unclosedCode := detectUnclosedMarkdownCodeBlocks(trimmed)
	if len(unclosedCode) > 0 {
		return CompletionState{
			IsComplete:     false,
			Reason:         "unclosed_code_block",
			StoppingPoint:  extractStoppingPoint(trimmed),
			UnclosedBlocks: unclosedCode,
			ExpectedFormat: expectedFormat,
		}
	}

	// 3. Kiểm tra JSON chưa hoàn chỉnh nếu yêu cầu JSON hoặc bắt đầu bằng { hoặc [
	if expectedFormat == "json" || expectedFormat == "json_object" || (strings.HasPrefix(trimmed, "{") && !strings.HasSuffix(trimmed, "}")) || (strings.HasPrefix(trimmed, "[") && !strings.HasSuffix(trimmed, "]")) {
		var js any
		if err := json.Unmarshal([]byte(trimmed), &js); err != nil {
			// Cú pháp JSON dở dang
			return CompletionState{
				IsComplete:     false,
				Reason:         "incomplete_json",
				StoppingPoint:  extractStoppingPoint(trimmed),
				ExpectedFormat: "json",
			}
		}
	}

	// 4. Kiểm tra văn bản dừng giữa chừng (dấu câu lửng lơ hoặc explicit "continued...")
	if isHangingSentence(trimmed) {
		return CompletionState{
			IsComplete:     false,
			Reason:         "hanging_sentence",
			StoppingPoint:  extractStoppingPoint(trimmed),
			ExpectedFormat: expectedFormat,
		}
	}

	return CompletionState{
		IsComplete:     true,
		Reason:         "completed",
		ExpectedFormat: expectedFormat,
	}
}

func detectUnclosedMarkdownCodeBlocks(text string) []string {
	lines := strings.Split(text, "\n")
	var openBlocks []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") {
			tag := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if len(openBlocks) > 0 {
				// Đóng code block gần nhất
				openBlocks = openBlocks[:len(openBlocks)-1]
			} else {
				// Mở code block mới
				openBlocks = append(openBlocks, tag)
			}
		}
	}
	return openBlocks
}

func extractStoppingPoint(text string) string {
	const maxStopLen = 120
	if len(text) <= maxStopLen {
		return text
	}
	return text[len(text)-maxStopLen:]
}

func isHangingSentence(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	trimmedPunct := strings.Trim(lower, "().[]{}'\"")
	if strings.HasSuffix(lower, "continued...") ||
		strings.HasSuffix(lower, "(continued...)") ||
		strings.HasSuffix(lower, "[continued...]") ||
		strings.HasSuffix(trimmedPunct, "continued...") ||
		strings.HasSuffix(lower, "to be continued...") ||
		strings.HasSuffix(trimmedPunct, "to be continued") ||
		strings.HasSuffix(lower, "(continues)") ||
		strings.HasSuffix(lower, "[continues]") ||
		strings.HasSuffix(trimmedPunct, "continues") {
		return true
	}

	// Kiểm tra ký tự kết thúc: nếu kết thúc bằng chữ thường hoặc dấu phẩy hoặc liên từ lửng
	runes := []rune(text)
	if len(runes) == 0 {
		return false
	}
	lastRune := runes[len(runes)-1]

	// Các dấu kết thúc hợp lệ của câu/đoạn
	switch lastRune {
	case '.', '!', '?', '"', '\'', '`', '}', ']', ')', ':', ';', '>', '—':
		return false
	}

	// Nếu kết thúc bằng dấu gạch ngang lửng hoặc dấu phẩy
	if lastRune == ',' || lastRune == '-' {
		return true
	}

	// Nếu kết thúc giữa từ (chữ cái thông thường không có dấu kết câu)
	if unicode.IsLetter(lastRune) || unicode.IsDigit(lastRune) {
		// Kiểm tra nếu là một từ nối lửng lơ ở cuối
		words := strings.Fields(text)
		if len(words) > 0 {
			lastWord := strings.ToLower(words[len(words)-1])
			switch lastWord {
			case "and", "or", "because", "the", "a", "an", "with", "to", "for", "in", "on", "at", "but", "if", "so", "va", "và", "nhưng", "bởi", "vì":
				return true
			}
		}
	}

	return false
}

// BuildContinuationPrompt xây dựng lời nhắc tiếp tục chính xác, không lặp lại
func BuildContinuationPrompt(taskGoal string, priorText string, state CompletionState) string {
	var sb strings.Builder
	sb.WriteString("Your previous response stopped unexpectedly before completion.\n")
	sb.WriteString("Instruction: Continue EXACTLY from the stopping point below. Do NOT restart from the beginning, do NOT repeat any already completed sentences or code blocks. Complete all remaining requirements seamlessly.\n\n")

	if len(state.UnclosedBlocks) > 0 {
		tag := state.UnclosedBlocks[len(state.UnclosedBlocks)-1]
		sb.WriteString(fmt.Sprintf("Context: The previous response ended inside an unclosed ```%s code block. Continue the code directly and close the block when finished.\n", tag))
	} else if state.ExpectedFormat == "json" || state.Reason == "incomplete_json" {
		sb.WriteString("Context: The previous response ended inside an incomplete JSON structure. Continue the remaining JSON keys/values directly and close the root JSON object/array cleanly.\n")
	}

	stopping := strings.TrimSpace(state.StoppingPoint)
	if stopping != "" {
		sb.WriteString(fmt.Sprintf("Exact stopping point:\n\"...%s\"\n\n", stopping))
	}

	sb.WriteString("Continue seamlessly now:")
	return sb.String()
}

// MergeContinuation ghép hai đoạn văn bản nối tiếp, tự động khử trùng lặp giao nhau (Overlap Deduplication)
func MergeContinuation(prior string, next string) string {
	prior = strings.TrimRight(prior, "\r\n")
	next = strings.TrimLeft(next, "\r\n")

	if prior == "" {
		return next
	}
	if next == "" {
		return prior
	}

	// Tìm đoạn trùng lặp lớn nhất giữa đuôi của prior và đầu của next
	maxOverlap := 250
	if len(prior) < maxOverlap {
		maxOverlap = len(prior)
	}
	if len(next) < maxOverlap {
		maxOverlap = len(next)
	}

	overlapLen := 0
	for l := maxOverlap; l >= 3; l-- {
		suffix := prior[len(prior)-l:]
		prefix := next[:l]
		if suffix == prefix {
			overlapLen = l
			break
		}
	}

	if overlapLen > 0 {
		return prior + next[overlapLen:]
	}

	// Nếu prior kết thúc bằng ``` và next bắt đầu bằng ``` (tránh mở/đóng code block rỗng)
	if strings.HasSuffix(prior, "```") && strings.HasPrefix(next, "```") {
		return prior[:len(prior)-3] + next[3:]
	}

	// Nối bình thường nếu không có overlap rõ rệt
	// Thêm dấu cách hoặc xuống dòng nếu cần thiết
	priorLastRune, _ := lastRune(prior)
	nextFirstRune, _ := firstRune(next)

	if unicode.IsLetter(priorLastRune) && unicode.IsLetter(nextFirstRune) {
		// Hai từ nối nhau
		return prior + " " + next
	}

	return prior + next
}

func lastRune(s string) (rune, bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, false
	}
	return runes[len(runes)-1], true
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

func extractExpectedFormat(rf any) string {
	if rf == nil {
		return ""
	}
	switch v := rf.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(v))
	case map[string]any:
		if t, ok := v["type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(t))
		}
	}
	return ""
}

// AutoContinuationConfig cấu hình tự động tiếp tục phản hồi dài
type AutoContinuationConfig struct {
	Enabled          bool `yaml:"enabled"`
	MaxContinuations int  `yaml:"max_continuations"`
}

// DefaultAutoContinuationConfig cấu hình mặc định an toàn
func DefaultAutoContinuationConfig() AutoContinuationConfig {
	return AutoContinuationConfig{
		Enabled:          true,
		MaxContinuations: 3,
	}
}

// ContinuationService điều phối việc phát hiện và tự động kéo dài phản hồi cho đến khi hoàn chỉnh
type ContinuationService struct {
	detector *CompletionDetector
	cfg      AutoContinuationConfig
	metrics  *domain.ContractMetrics
}

type noContinuationContextKey struct{}

// WithNoContinuation đánh dấu context không kích hoạt tự động tiếp tục đệ quy
func WithNoContinuation(ctx context.Context) context.Context {
	return context.WithValue(ctx, noContinuationContextKey{}, true)
}

// IsNoContinuation kiểm tra xem context có cấm tiếp tục tự động hay không
func IsNoContinuation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(noContinuationContextKey{}).(bool)
	return v
}

func NewContinuationService(cfg AutoContinuationConfig) *ContinuationService {
	if cfg.MaxContinuations <= 0 {
		cfg.MaxContinuations = 3
	}
	return &ContinuationService{
		detector: NewCompletionDetector(),
		cfg:      cfg,
	}
}

// AutoContinueResponse thực thi chu trình tự động tiếp tục nếu phản hồi chưa hoàn tất
func (cs *ContinuationService) AutoContinueResponse(
	ctx context.Context,
	chatUseCase ports.ChatUseCase,
	req *domain.OpenAIChatRequest,
	initialResp *domain.OpenAIChatResponse,
	taskGoal string,
) (*domain.OpenAIChatResponse, int, error) {
	if !cs.cfg.Enabled || initialResp == nil || len(initialResp.Choices) == 0 {
		return initialResp, 0, nil
	}

	currentResp := initialResp
	continuationsDone := 0

	for continuationsDone < cs.cfg.MaxContinuations {
		if ctx.Err() != nil {
			return currentResp, continuationsDone, ctx.Err()
		}

		choice := currentResp.Choices[0]
		// Nếu phản hồi có chứa tool calls, nhường lại cho ReAct engine điều phối
		if len(choice.Message.ToolCalls) > 0 {
			break
		}

		finishReason := ""
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}

		expectedFormat := extractExpectedFormat(req.ResponseFormat)

		state := cs.detector.Analyze(choice.Message.Content, finishReason, expectedFormat)
		if state.IsComplete {
			break
		}

		// Tạo prompt tiếp tục an toàn
		contPrompt := BuildContinuationPrompt(taskGoal, choice.Message.Content, state)

		// Xây dựng request tiếp nối
		nextReq := *req
		nextReq.Messages = append([]domain.OpenAIMessage{}, req.Messages...)
		nextReq.Messages = append(nextReq.Messages, choice.Message)
		nextReq.Messages = append(nextReq.Messages, domain.OpenAIMessage{
			Role:    "user",
			Content: contPrompt,
		})

		// Kế thừa ngữ cảnh hội thoại từ remote nếu có
		if currentResp.ConversationID != "" {
			nextReq.ConversationID = currentResp.ConversationID
			nextReq.ResponseID = currentResp.ResponseID
			nextReq.ChoiceID = currentResp.ChoiceID
		}

		nextResp, err := chatUseCase.ExecuteChatSync(ctx, &nextReq)
		if err != nil {
			// Nếu thất bại ở bước tiếp tục, trả về kết quả tốt nhất hiện tại kèm log
			return currentResp, continuationsDone, nil
		}
		if len(nextResp.Choices) == 0 {
			break
		}

		continuationsDone++
		if cs.metrics != nil {
			cs.metrics.IncrementContinuations()
		}
		mergedText := MergeContinuation(choice.Message.Content, nextResp.Choices[0].Message.Content)

		// Cập nhật phản hồi với nội dung đã được hợp nhất
		nextChoice := nextResp.Choices[0]
		nextChoice.Message.Content = mergedText

		currentResp.Choices[0] = nextChoice
		currentResp.ConversationID = nextResp.ConversationID
		currentResp.ResponseID = nextResp.ResponseID
		currentResp.ChoiceID = nextResp.ChoiceID
		if nextResp.Usage != nil && currentResp.Usage != nil {
			currentResp.Usage.CompletionTokens += nextResp.Usage.CompletionTokens
			currentResp.Usage.TotalTokens += nextResp.Usage.CompletionTokens
		}
	}

	if continuationsDone >= cs.cfg.MaxContinuations && len(currentResp.Choices) > 0 {
		finishReason := ""
		if currentResp.Choices[0].FinishReason != nil {
			finishReason = *currentResp.Choices[0].FinishReason
		}
		state := cs.detector.Analyze(currentResp.Choices[0].Message.Content, finishReason, "")
		if !state.IsComplete {
			lenReason := "length"
			currentResp.Choices[0].FinishReason = &lenReason
			if cs.metrics != nil {
				cs.metrics.IncrementContinuationExhausted()
			}
		}
	}

	return currentResp, continuationsDone, nil
}

func (cs *ContinuationService) SetMetrics(m *domain.ContractMetrics) {
	cs.metrics = m
}

// StreamOverlapDeduplicator thực hiện khử trùng lặp giao nhau trong luồng stream continuation
type StreamOverlapDeduplicator struct {
	priorTail     string
	buffer        strings.Builder
	overlapPassed bool
	maxBufferLen  int
}

// NewStreamOverlapDeduplicator khởi tạo StreamOverlapDeduplicator với đoạn cuối của phản hồi trước
func NewStreamOverlapDeduplicator(priorText string) *StreamOverlapDeduplicator {
	tail := priorText
	if len(tail) > 200 {
		tail = tail[len(tail)-200:]
	}
	return &StreamOverlapDeduplicator{
		priorTail:    tail,
		maxBufferLen: 200,
	}
}

// ProcessDelta lọc token delta, đệm đoạn đầu cho đến khi xác định xong vị trí overlap, sau đó stream trực tiếp
func (d *StreamOverlapDeduplicator) ProcessDelta(delta string) string {
	if d == nil {
		return delta
	}
	if d.overlapPassed || d.priorTail == "" {
		return delta
	}

	d.buffer.WriteString(delta)
	bufStr := d.buffer.String()

	// Nếu bufStr còn quá ngắn (< 15 ký tự), đệm tiếp để thu thập đủ ngữ cảnh
	if len(bufStr) < 15 {
		return ""
	}

	// Nếu bufStr vẫn nằm trọn trong priorTail, nó vẫn đang lặp lại nội dung cũ -> đệm tiếp
	if strings.Contains(d.priorTail, bufStr) && len(bufStr) < d.maxBufferLen {
		return ""
	}

	// Đã vượt qua đoạn overlap hoặc chạm ngưỡng đệm tối đa
	d.overlapPassed = true
	merged := MergeContinuation(d.priorTail, bufStr)
	if strings.HasPrefix(merged, d.priorTail) {
		return strings.TrimPrefix(merged, d.priorTail)
	}
	return bufStr
}

// Flush xả phần dữ liệu còn lại trong bộ đệm nếu luồng kết thúc trước khi vượt qua ngưỡng đệm
func (d *StreamOverlapDeduplicator) Flush() string {
	if d == nil || d.overlapPassed {
		return ""
	}
	d.overlapPassed = true
	bufStr := d.buffer.String()
	if bufStr == "" {
		return ""
	}
	merged := MergeContinuation(d.priorTail, bufStr)
	if strings.HasPrefix(merged, d.priorTail) {
		return strings.TrimPrefix(merged, d.priorTail)
	}
	return bufStr
}
