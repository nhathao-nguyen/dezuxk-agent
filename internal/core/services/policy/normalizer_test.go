package policy

import (
	"encoding/json"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestRepairJSONArguments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Valid JSON unchanged",
			input:    `{"path": "main.go", "content": "hello"}`,
			expected: `{"path": "main.go", "content": "hello"}`,
		},
		{
			name:     "Markdown code fence stripped",
			input:    "```json\n{\"path\": \"main.go\"}\n```",
			expected: `{"path": "main.go"}`,
		},
		{
			name:     "Trailing comma fixed",
			input:    `{"path": "test.txt", "lines": [1, 2, ],}`,
			expected: `{"path": "test.txt", "lines": [1, 2]}`,
		},
		{
			name:     "Unbalanced closing brace fixed",
			input:    `{"path": "test.txt", "lines": [1, 2`,
			expected: `{"path": "test.txt", "lines": [1, 2]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSONArguments(tt.input)
			var gotVal, expVal any
			if err := json.Unmarshal([]byte(got), &gotVal); err != nil {
				t.Fatalf("RepairJSONArguments() produced invalid JSON: %v, string: %s", err, got)
			}
			_ = json.Unmarshal([]byte(tt.expected), &expVal)
			gotJSON, _ := json.Marshal(gotVal)
			expJSON, _ := json.Marshal(expVal)
			if string(gotJSON) != string(expJSON) {
				t.Errorf("RepairJSONArguments() semantic mismatch: got %s, want %s", gotJSON, expJSON)
			}
		})
	}
}

func TestNormalizeToolCalls(t *testing.T) {
	raw := []domain.OpenAIToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: domain.OpenAIFunctionCallData{
				Name:      "read_file",
				Arguments: `{"path": "test.go",}`,
			},
		},
		{
			ID:   "call_1", // Duplicate ID
			Type: "function",
			Function: domain.OpenAIFunctionCallData{
				Name:      "write_file",
				Arguments: `{"path": "out.go"}`,
			},
		},
	}

	normalized, err := NormalizeToolCalls(raw)
	if err != nil {
		t.Fatalf("NormalizeToolCalls failed: %v", err)
	}

	if len(normalized) != 2 {
		t.Fatalf("expected 2 normalized calls, got %d", len(normalized))
	}

	if normalized[0].ID == normalized[1].ID {
		t.Errorf("expected IDs to be deduplicated, got both %s", normalized[0].ID)
	}

	if string(normalized[0].Arguments) != `{"path": "test.go"}` {
		t.Errorf("expected trailing comma repaired, got %s", string(normalized[0].Arguments))
	}
}
