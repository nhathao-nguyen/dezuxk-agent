package services_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/services"
)

func TestExtractToolCalls(t *testing.T) {
	// Case 1: Plain text without tool call
	t.Run("PlainText", func(t *testing.T) {
		raw := "Xin chào! Tôi có thể giúp gì cho bạn?"
		clean, calls := services.ExtractToolCalls(raw)
		if clean != raw {
			t.Errorf("expected clean to match raw, got: %s", clean)
		}
		if len(calls) != 0 {
			t.Errorf("expected 0 tool calls, got %d", len(calls))
		}
	})

	// Case 2: XML <tool_call>
	t.Run("XMLToolCall", func(t *testing.T) {
		raw := `Tôi sẽ đọc file giúp bạn.
<tool_call>
{"name": "read_file", "arguments": {"path": "src/main.go"}}
</tool_call>
Vui lòng đợi một lát.`

		clean, calls := services.ExtractToolCalls(raw)
		if strings.Contains(clean, "<tool_call>") || strings.Contains(clean, "</tool_call>") {
			t.Errorf("clean text should not contain tool_call tags, got: %s", clean)
		}
		if !strings.Contains(clean, "Tôi sẽ đọc file giúp bạn.") {
			t.Errorf("clean text missing prefix, got: %s", clean)
		}
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(calls))
		}

		call := calls[0]
		if !strings.HasPrefix(call.ID, "call_") {
			t.Errorf("expected call ID to start with call_, got: %s", call.ID)
		}
		if call.Type != "function" {
			t.Errorf("expected type 'function', got: %s", call.Type)
		}
		if call.Function.Name != "read_file" {
			t.Errorf("expected function name read_file, got: %s", call.Function.Name)
		}
		if !strings.Contains(call.Function.Arguments, "src/main.go") {
			t.Errorf("expected arguments to contain src/main.go, got: %s", call.Function.Arguments)
		}
	})

	// Case 3: Multiple tool calls
	t.Run("MultipleToolCalls", func(t *testing.T) {
		raw := `<tool_call>{"name": "read_file", "arguments": {"path": "a.txt"}}</tool_call>
<tool_call>{"name": "read_file", "arguments": {"path": "b.txt"}}</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 2 {
			t.Fatalf("expected 2 tool calls, got %d", len(calls))
		}
		if calls[0].Function.Name != "read_file" || calls[1].Function.Name != "read_file" {
			t.Errorf("unexpected names in multiple calls")
		}
	})
}
