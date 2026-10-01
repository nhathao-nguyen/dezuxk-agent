package services_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func parseSSEChunks(output string) []domain.OpenAIChatResponse {
	var chunks []domain.OpenAIChatResponse
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			dataPart := strings.TrimPrefix(line, "data: ")
			var chunk domain.OpenAIChatResponse
			if err := json.Unmarshal([]byte(dataPart), &chunk); err == nil {
				chunks = append(chunks, chunk)
			}
		}
	}
	return chunks
}

func TestStreamToolFilter_NoTools(t *testing.T) {
	var buf bytes.Buffer
	flushed := false
	flusher := func() { flushed = true }

	filter := services.NewStreamToolFilter(false, &buf, flusher, &flushed, 123456, "gemini-3.8-flash", "c_test")

	err := filter.OnDelta("Xin chào ", "c_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = filter.OnDelta("bạn!", "c_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = filter.FlushRemaining()

	chunks := parseSSEChunks(buf.String())
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Choices[0].Delta.Content != "Xin chào " {
		t.Errorf("expected 'Xin chào ', got: %s", chunks[0].Choices[0].Delta.Content)
	}
	if chunks[1].Choices[0].Delta.Content != "bạn!" {
		t.Errorf("expected 'bạn!', got: %s", chunks[1].Choices[0].Delta.Content)
	}
}

func TestStreamToolFilter_WithToolCallSuppression(t *testing.T) {
	var buf bytes.Buffer
	flushed := false
	flusher := func() { flushed = true }

	filter := services.NewStreamToolFilter(true, &buf, flusher, &flushed, 123456, "gemini-3.8-flash", "c_test")

	// Preamble text
	_ = filter.OnDelta("Tôi sẽ đọc file.\n", "c_test")
	// Tool call starts
	_ = filter.OnDelta("<tool_", "c_test")
	_ = filter.OnDelta("call>\n", "c_test")
	_ = filter.OnDelta(`{"name": "read_file", "arguments": {"path": "main.go"}}`+"\n", "c_test")
	_ = filter.OnDelta("</tool_call>", "c_test")
	// Post-amble
	_ = filter.OnDelta("\nVui lòng chờ.", "c_test")
	_ = filter.FlushRemaining()

	chunks := parseSSEChunks(buf.String())
	var contentParts []string
	var toolCalls []domain.OpenAIToolCall

	for _, c := range chunks {
		delta := c.Choices[0].Delta
		if delta.Content != "" {
			contentParts = append(contentParts, delta.Content)
			if strings.Contains(delta.Content, "<tool_call>") || strings.Contains(delta.Content, "</tool_call>") {
				t.Errorf("leaked tool_call tag in Delta.Content: %s", delta.Content)
			}
		}
		if len(delta.ToolCalls) > 0 {
			toolCalls = append(toolCalls, delta.ToolCalls...)
		}
	}

	fullContent := strings.Join(contentParts, "")
	if !strings.Contains(fullContent, "Tôi sẽ đọc file.") {
		t.Errorf("missing preamble in content, got: %s", fullContent)
	}
	if !strings.Contains(fullContent, "Vui lòng chờ.") {
		t.Errorf("missing post-amble in content, got: %s", fullContent)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "read_file" {
		t.Errorf("expected read_file, got: %s", toolCalls[0].Function.Name)
	}
	if !strings.Contains(toolCalls[0].Function.Arguments, "main.go") {
		t.Errorf("expected main.go in arguments, got: %s", toolCalls[0].Function.Arguments)
	}
}
