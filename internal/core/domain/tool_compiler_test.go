package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestCompileToolsInstruction(t *testing.T) {
	// Case 1: Nil / Empty tools
	if res := domain.CompileToolsInstruction(nil); res != "" {
		t.Errorf("expected empty instruction for nil tools, got: %s", res)
	}
	if res := domain.CompileToolsInstruction([]domain.OpenAITool{}); res != "" {
		t.Errorf("expected empty instruction for empty tools, got: %s", res)
	}

	// Case 2: Standard tools
	tools := []domain.OpenAITool{
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:        "read_file",
				Description: "Read content of a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:        "run_terminal_command",
				Description: "Run shell command",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
			},
		},
	}

	instruction := domain.CompileToolsInstruction(tools)

	if !strings.Contains(instruction, "<tools>") || !strings.Contains(instruction, "</tools>") {
		t.Errorf("expected <tools> tags, got: %s", instruction)
	}
	if !strings.Contains(instruction, "read_file") || !strings.Contains(instruction, "run_terminal_command") {
		t.Errorf("expected function names in instruction, got: %s", instruction)
	}
	if !strings.Contains(instruction, "<tool_call>") || !strings.Contains(instruction, "</tool_call>") {
		t.Errorf("expected <tool_call> usage syntax in instruction, got: %s", instruction)
	}
}
