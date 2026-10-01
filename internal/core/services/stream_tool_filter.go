package services

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// StreamToolFilter bộ đệm và lọc luồng thời gian thực cho Tool Calling:
// 1. Nếu request có khai báo tools: ngăn chặn rò rỉ thẻ <tool_call>...</tool_call> vào delta.content
// 2. Tự động bóc tách và phát chunk tool_calls chuẩn OpenAI sang client (Cursor / Cline / Roo Code)
// 3. Nếu request không có tools: chuyển thẳng delta.content để đạt độ trễ tối thiểu (zero overhead)
type StreamToolFilter struct {
	hasTools        bool
	streamWriter    io.Writer
	flusher         func()
	flushedToClient *bool
	createdTime     int64
	model           string
	conversationID  string

	buffer           strings.Builder
	emittedToolCalls []domain.OpenAIToolCall
}

func NewStreamToolFilter(hasTools bool, w io.Writer, flusher func(), flushed *bool, created int64, model, cID string) *StreamToolFilter {
	return &StreamToolFilter{
		hasTools:        hasTools,
		streamWriter:    w,
		flusher:         flusher,
		flushedToClient: flushed,
		createdTime:     created,
		model:           model,
		conversationID:  cID,
	}
}

func (f *StreamToolFilter) SetConversationID(cID string) {
	if cID != "" {
		f.conversationID = cID
	}
}

func (f *StreamToolFilter) GetEmittedToolCalls() []domain.OpenAIToolCall {
	return f.emittedToolCalls
}

func (f *StreamToolFilter) EmittedCount() int {
	return len(f.emittedToolCalls)
}

func (f *StreamToolFilter) emitContent(content string) error {
	if content == "" {
		return nil
	}
	chunk := domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + f.conversationID,
		Object:         "chat.completion.chunk",
		Created:        f.createdTime,
		Model:          f.model,
		ConversationID: f.conversationID,
		Choices: []domain.OpenAIChoice{{
			Index: 0,
			Delta: domain.OpenAIDelta{Content: content},
		}},
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f.streamWriter, "data: %s\n\n", b); err != nil {
		return err
	}
	if f.flusher != nil {
		f.flusher()
	}
	if f.flushedToClient != nil {
		*f.flushedToClient = true
	}
	return nil
}

func (f *StreamToolFilter) emitToolCalls(calls []domain.OpenAIToolCall) error {
	if len(calls) == 0 {
		return nil
	}
	chunk := domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + f.conversationID,
		Object:         "chat.completion.chunk",
		Created:        f.createdTime,
		Model:          f.model,
		ConversationID: f.conversationID,
		Choices: []domain.OpenAIChoice{{
			Index: 0,
			Delta: domain.OpenAIDelta{ToolCalls: calls},
		}},
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f.streamWriter, "data: %s\n\n", b); err != nil {
		return err
	}
	if f.flusher != nil {
		f.flusher()
	}
	if f.flushedToClient != nil {
		*f.flushedToClient = true
	}
	return nil
}

func (f *StreamToolFilter) OnDelta(delta, cID string) error {
	if cID != "" {
		f.conversationID = cID
	}
	if !f.hasTools {
		return f.emitContent(delta)
	}

	f.buffer.WriteString(delta)
	bufStr := f.buffer.String()
	lowerBuf := strings.ToLower(bufStr)

	idx := strings.Index(lowerBuf, "<tool_call")
	if idx == -1 {
		// Kiểm tra xem đoạn đuôi có thể là tiền tố của "<tool_call" không (ví dụ "<", "<tool", "<tool_")
		tailLen := 0
		for l := 1; l < 11 && l <= len(lowerBuf); l++ {
			suffix := lowerBuf[len(lowerBuf)-l:]
			if strings.HasPrefix("<tool_call", suffix) {
				tailLen = l
				break
			}
		}

		safeLen := len(bufStr) - tailLen
		if safeLen > 0 {
			toEmit := bufStr[:safeLen]
			f.buffer.Reset()
			if tailLen > 0 {
				f.buffer.WriteString(bufStr[safeLen:])
			}
			return f.emitContent(toEmit)
		}
		return nil
	}

	// Đã xuất hiện <tool_call
	if idx > 0 {
		prefixText := bufStr[:idx]
		if err := f.emitContent(prefixText); err != nil {
			return err
		}
		remaining := bufStr[idx:]
		f.buffer.Reset()
		f.buffer.WriteString(remaining)
		bufStr = remaining
		lowerBuf = strings.ToLower(bufStr)
	}

	// Kiểm tra xem đã có thẻ đóng </tool_call> chưa
	closeIdx := strings.Index(lowerBuf, "</tool_call>")
	if closeIdx != -1 {
		toolCallEnd := closeIdx + len("</tool_call>")
		toolCallBlock := bufStr[:toolCallEnd]
		remaining := bufStr[toolCallEnd:]

		_, calls := ExtractToolCalls(toolCallBlock)
		if len(calls) > 0 {
			f.emittedToolCalls = append(f.emittedToolCalls, calls...)
			if err := f.emitToolCalls(calls); err != nil {
				return err
			}
		}

		f.buffer.Reset()
		if len(remaining) > 0 {
			return f.OnDelta(remaining, cID)
		}
	}

	return nil
}

func (f *StreamToolFilter) FlushRemaining() error {
	if !f.hasTools {
		return nil
	}
	remaining := f.buffer.String()
	f.buffer.Reset()
	if remaining == "" {
		return nil
	}

	// Thử extract tool calls từ phần còn lại (phòng trường hợp Gemini không đóng thẻ XML hoặc dùng markdown)
	clean, calls := ExtractToolCalls(remaining)
	if len(calls) > 0 {
		f.emittedToolCalls = append(f.emittedToolCalls, calls...)
		if clean != "" {
			if err := f.emitContent(clean); err != nil {
				return err
			}
		}
		return f.emitToolCalls(calls)
	}

	return f.emitContent(remaining)
}
