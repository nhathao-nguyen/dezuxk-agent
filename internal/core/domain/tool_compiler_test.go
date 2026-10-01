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
	if !strings.Contains(instruction, "[Tool Result (call_id: ...)]") {
		t.Errorf("expected instruction to explain tool result format, got: %s", instruction)
	}
}

func TestCompileToolsInstructionWithChoice(t *testing.T) {
	tools := []domain.OpenAITool{
		{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:        "read_file",
				Description: "Read file",
			},
		},
	}

	// 1. tool_choice: "none" -> return empty string
	if res := domain.CompileToolsInstructionWithChoice(tools, "none"); res != "" {
		t.Errorf("expected empty instruction when tool_choice is none, got: %s", res)
	}

	// 2. tool_choice: "required" -> must mandate tool call
	resReq := domain.CompileToolsInstructionWithChoice(tools, "required")
	if !strings.Contains(resReq, "CRITICAL REQUIREMENT") || !strings.Contains(resReq, "at least one tool") {
		t.Errorf("expected required requirement, got: %s", resReq)
	}

	// 3. tool_choice: specific function -> must mandate specific tool
	choiceMap := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "read_file",
		},
	}
	resSpecific := domain.CompileToolsInstructionWithChoice(tools, choiceMap)
	if !strings.Contains(resSpecific, `You MUST invoke the specific tool "read_file"`) {
		t.Errorf("expected specific tool requirement, got: %s", resSpecific)
	}
}
