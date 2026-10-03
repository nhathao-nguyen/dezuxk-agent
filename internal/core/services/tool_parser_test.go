package services_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
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

	// Case 3: Multiple tool calls (Multiple XML tags)
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

	// Case 4: Markdown fence inside XML <tool_call>
	t.Run("MarkdownFencesInsideXML", func(t *testing.T) {
		raw := `<tool_call>
` + "```json\n" + `{"name": "write_file", "arguments": {"path": "test.txt", "content": "hello"}}
` + "```\n" + `</tool_call>`

		clean, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(calls))
		}
		if calls[0].Function.Name != "write_file" {
			t.Errorf("expected write_file, got: %s", calls[0].Function.Name)
		}
		if clean != "" {
			t.Errorf("expected empty clean text, got: %q", clean)
		}
	})

	// Case 5: Literal unescaped newlines inside JSON string argument (code writing)
	t.Run("UnescapedNewlinesInJSON", func(t *testing.T) {
		raw := "<tool_call>\n" +
			`{"name": "write_file", "arguments": {"path": "main.go", "content": "package main` + "\n" +
			`func main() {` + "\n" +
			`	println(\"ok\")` + "\n" +
			`}"}}` + "\n</tool_call>"

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call after auto-repair, got %d", len(calls))
		}
		if calls[0].Function.Name != "write_file" {
			t.Errorf("expected write_file, got: %s", calls[0].Function.Name)
		}
		if !strings.Contains(calls[0].Function.Arguments, "package main") {
			t.Errorf("expected arguments to contain package main, got: %s", calls[0].Function.Arguments)
		}
	})

	// Case 6: JSON Array of tool calls inside single <tool_call>
	t.Run("JSONArrayOfToolCalls", func(t *testing.T) {
		raw := `<tool_call>
[
  {"name": "read_file", "arguments": {"path": "doc1.txt"}},
  {"name": "read_file", "arguments": {"path": "doc2.txt"}}
]
</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 2 {
			t.Fatalf("expected 2 tool calls from array, got %d", len(calls))
		}
		if calls[0].Function.Name != "read_file" || calls[1].Function.Name != "read_file" {
			t.Errorf("unexpected tool names from array")
		}
		if !strings.Contains(calls[0].Function.Arguments, "doc1.txt") {
			t.Errorf("expected doc1.txt in first call")
		}
		if !strings.Contains(calls[1].Function.Arguments, "doc2.txt") {
			t.Errorf("expected doc2.txt in second call")
		}
	})

	// Case 7: Fallback markdown block
	t.Run("FallbackMarkdownBlock", func(t *testing.T) {
		raw := "Tôi sẽ thực hiện lệnh:\n" +
			"```json\n" +
			`{"name": "run_command", "arguments": {"command": "dir"}}` + "\n" +
			"```\n" +
			"Vui lòng đợi kết quả."

		clean, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call from fallback markdown, got %d", len(calls))
		}
		if calls[0].Function.Name != "run_command" {
			t.Errorf("expected run_command, got: %s", calls[0].Function.Name)
		}
		if !strings.Contains(clean, "Tôi sẽ thực hiện lệnh:") || !strings.Contains(clean, "Vui lòng đợi kết quả.") {
			t.Errorf("expected surrounding text to be preserved, got: %s", clean)
		}
		if strings.Contains(clean, "run_command") {
			t.Errorf("clean text should not contain tool code block, got: %s", clean)
		}
	})

	// Case 8: Trailing comma inside arguments
	t.Run("TrailingCommaInArguments", func(t *testing.T) {
		raw := `<tool_call>
{"name": "test_tool", "arguments": {"a": 1, "b": 2,}}
</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call with trailing comma auto-repaired, got %d", len(calls))
		}
		if calls[0].Function.Name != "test_tool" {
			t.Errorf("expected test_tool, got: %s", calls[0].Function.Name)
		}
	})

	// Case 9: Unclosed outer braces
	t.Run("UnclosedBracesAutoRepair", func(t *testing.T) {
		raw := `<tool_call>
{"name": "edit_file", "arguments": {"path": "main.go"}
</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call with unclosed brace auto-repaired, got %d", len(calls))
		}
		if calls[0].Function.Name != "edit_file" {
			t.Errorf("expected edit_file, got: %s", calls[0].Function.Name)
		}
	})

	// Case 10: Single quotes syntax
	t.Run("SingleQuotesSyntax", func(t *testing.T) {
		raw := `<tool_call>
{'name': 'query_db', 'arguments': {'sql': 'SELECT 1'}}
</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call from single-quoted JSON, got %d", len(calls))
		}
		if calls[0].Function.Name != "query_db" {
			t.Errorf("expected query_db, got: %s", calls[0].Function.Name)
		}
	})

	// Case 11: Loose format extraction
	t.Run("LooseFormatExtraction", func(t *testing.T) {
		raw := `<tool_call>
I am invoking: name: "fetch_url", arguments: {"url": "https://example.com"}
</tool_call>`

		_, calls := services.ExtractToolCalls(raw)
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call from loose format, got %d", len(calls))
		}
		if calls[0].Function.Name != "fetch_url" {
			t.Errorf("expected fetch_url, got: %s", calls[0].Function.Name)
		}
		if !strings.Contains(calls[0].Function.Arguments, "https://example.com") {
			t.Errorf("expected url in arguments, got: %s", calls[0].Function.Arguments)
		}
	})

	// Case 12: package.json chứa "name" không bao giờ bị nuốt thành tool call (Khắc phục Bug H1)
	t.Run("PackageJsonNotSwallowedAsToolCall", func(t *testing.T) {
		raw := "Đây là package.json mẫu của bạn:\n" +
			"```json\n" +
			"{\n" +
			`  "name": "my-cool-frontend",` + "\n" +
			`  "version": "1.0.0",` + "\n" +
			`  "description": "Demo package"` + "\n" +
			"}\n" +
			"```\n" +
			"Chúc bạn thành công."

		clean, calls := services.ExtractToolCalls(raw)
		if len(calls) != 0 {
			t.Fatalf("expected 0 tool calls for package.json, got %d", len(calls))
		}
		if !strings.Contains(clean, "my-cool-frontend") {
			t.Errorf("expected package.json content to be preserved, got: %s", clean)
		}
		if !strings.Contains(clean, "```json") {
			t.Errorf("expected markdown code fences to be preserved, got: %s", clean)
		}
	})

	// Case 13: AllowedTools whitelist lọc bỏ công cụ không thuộc danh sách
	t.Run("AllowedToolsWhitelistFiltering", func(t *testing.T) {
		raw := "Gọi lệnh:\n" +
			"```json\n" +
			`{"name": "unauthorized_tool", "arguments": {"action": "delete"}}` + "\n" +
			"```\n" +
			"Hoàn tất."

		allowed := []domain.OpenAITool{
			{
				Type: "function",
				Function: domain.OpenAIFunctionDef{
					Name: "run_command",
				},
			},
		}

		clean, calls := services.ExtractToolCallsWithAllowed(raw, allowed)
		if len(calls) != 0 {
			t.Fatalf("expected 0 tool calls because unauthorized_tool is not allowed, got %d", len(calls))
		}
		if !strings.Contains(clean, "unauthorized_tool") {
			t.Errorf("expected clean text to preserve unauthorized snippet, got: %s", clean)
		}
	})
}
