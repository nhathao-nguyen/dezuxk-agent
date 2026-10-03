package services_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestValidateJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string"},
			"start_line": {"type": "integer"},
			"mode": {"type": "string", "enum": ["read", "write"]}
		}
	}`)

	t.Run("ValidArguments", func(t *testing.T) {
		args := `{"path": "foo.txt", "start_line": 10, "mode": "read"}`
		if err := services.ValidateJSONSchema(schema, args); err != nil {
			t.Fatalf("expected valid, got: %v", err)
		}
	})

	t.Run("MissingRequiredField", func(t *testing.T) {
		args := `{"start_line": 10}`
		err := services.ValidateJSONSchema(schema, args)
		if err == nil || !strings.Contains(err.Error(), "thiếu tham số bắt buộc") {
			t.Fatalf("expected missing required parameter error, got: %v", err)
		}
	})

	t.Run("InvalidType", func(t *testing.T) {
		args := `{"path": 12345}`
		err := services.ValidateJSONSchema(schema, args)
		if err == nil || !strings.Contains(err.Error(), "kiểu dữ liệu không hợp lệ") {
			t.Fatalf("expected type error, got: %v", err)
		}
	})

	t.Run("InvalidEnumType", func(t *testing.T) {
		args := `{"path": "test.txt", "mode": "execute"}`
		err := services.ValidateJSONSchema(schema, args)
		if err == nil || !strings.Contains(err.Error(), "không nằm trong danh sách enum") {
			t.Fatalf("expected enum error, got: %v", err)
		}
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		args := `{"path": "incomplete`
		err := services.ValidateJSONSchema(schema, args)
		if err == nil {
			t.Fatal("expected error for malformed json")
		}
	})
}

func TestValidateAndNormalizeToolCalls(t *testing.T) {
	allowedTools := []domain.OpenAITool{
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name: "read_file",
				Parameters: json.RawMessage(`{
					"type": "object",
					"required": ["path"],
					"properties": {
						"path": {"type": "string"}
					}
				}`),
			},
		},
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name: "write_file",
				Parameters: json.RawMessage(`{
					"type": "object",
					"required": ["path", "content"],
					"properties": {
						"path": {"type": "string"},
						"content": {"type": "string"}
					}
				}`),
			},
		},
	}

	t.Run("RejectUnknownToolName", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				Function: domain.OpenAIFunctionCallData{
					Name:      "dangerous_exec",
					Arguments: `{"cmd": "rm -rf"}`,
				},
			},
		}
		_, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
		if err == nil || !strings.Contains(err.Error(), "không nằm trong danh sách được phép") {
			t.Fatalf("expected unknown tool rejection, got: %v", err)
		}
	})

	t.Run("RejectInvalidArgumentsSchema", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{}`, // missing required "path"
				},
			},
		}
		_, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
		if err == nil || !strings.Contains(err.Error(), "thiếu tham số bắt buộc") {
			t.Fatalf("expected schema validation error, got: %v", err)
		}
	})

	t.Run("ToolChoiceNone", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "a.txt"}`,
				},
			},
		}
		res, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "none")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("expected empty calls when tool_choice is none, got %d", len(res))
		}
	})

	t.Run("ToolChoiceRequired_Success", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "a.txt"}`,
				},
			},
		}
		res, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "required")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 1 {
			t.Fatalf("expected 1 call, got %d", len(res))
		}
	})

	t.Run("ToolChoiceRequired_Failure", func(t *testing.T) {
		var calls []domain.OpenAIToolCall
		_, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "required")
		if err == nil || !strings.Contains(err.Error(), "tool_choice là 'required'") {
			t.Fatalf("expected error when tool_choice required but no calls, got: %v", err)
		}
	})

	t.Run("ToolChoiceSpecificFunction", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "a.txt"}`,
				},
			},
		}
		tc := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "write_file",
			},
		}
		_, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, tc)
		if err == nil || !strings.Contains(err.Error(), "yêu cầu gọi hàm \"write_file\"") {
			t.Fatalf("expected error when required function not called, got: %v", err)
		}
	})

	t.Run("MultipleToolCallsAndPreserveID", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				ID: "custom_call_1",
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "a.txt"}`,
				},
			},
			{
				ID: "custom_call_2",
				Function: domain.OpenAIFunctionCallData{
					Name:      "write_file",
					Arguments: `{"path": "b.txt", "content": "hello"}`,
				},
			},
		}
		res, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("expected 2 calls, got %d", len(res))
		}
		if res[0].ID != "custom_call_1" || res[0].Index != 0 {
			t.Errorf("call 0 mismatch: %+v", res[0])
		}
		if res[1].ID != "custom_call_2" || res[1].Index != 1 {
			t.Errorf("call 1 mismatch: %+v", res[1])
		}
	})

	t.Run("DuplicateToolCallID_AutoDeduplication", func(t *testing.T) {
		calls := []domain.OpenAIToolCall{
			{
				ID: "duplicate_id",
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "a.txt"}`,
				},
			},
			{
				ID: "duplicate_id", // Trùng ID do LLM sinh
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: `{"path": "b.txt"}`,
				},
			},
		}
		res, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("expected 2 calls, got %d", len(res))
		}
		if res[0].ID == res[1].ID {
			t.Fatalf("tool_call_ids must be unique, both are: %s", res[0].ID)
		}
		if res[0].ID != "duplicate_id" || res[1].ID != "duplicate_id_2" {
			t.Errorf("unexpected deduplicated IDs: %s, %s", res[0].ID, res[1].ID)
		}
	})

	t.Run("OversizedArguments_Rejected", func(t *testing.T) {
		hugeArgs := `{"path": "` + strings.Repeat("A", 11*1024*1024) + `"}`
		calls := []domain.OpenAIToolCall{
			{
				ID: "oversized_call",
				Function: domain.OpenAIFunctionCallData{
					Name:      "read_file",
					Arguments: hugeArgs,
				},
			},
		}
		_, err := services.ValidateAndNormalizeToolCalls(calls, allowedTools, "auto")
		if err == nil || !strings.Contains(err.Error(), "vượt quá giới hạn kích thước") {
			t.Fatalf("expected oversized arguments error, got: %v", err)
		}
	})
}
