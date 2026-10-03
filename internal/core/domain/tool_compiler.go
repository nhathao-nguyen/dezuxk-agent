package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CompileToolsInstruction chuyển đổi danh sách OpenAITool thành chỉ dẫn hệ thống chuẩn XML
func CompileToolsInstruction(tools []OpenAITool) string {
	return CompileToolsInstructionWithChoice(tools, nil)
}

// CompileToolsInstructionWithChoice chuyển đổi danh sách OpenAITool và tool_choice thành chỉ dẫn hệ thống chuẩn XML
func CompileToolsInstructionWithChoice(tools []OpenAITool, toolChoice any) string {
	if len(tools) == 0 {
		return ""
	}

	// 1. Kiểm tra tool_choice
	var forcedToolName string
	isRequired := false

	if toolChoice != nil {
		switch tc := toolChoice.(type) {
		case string:
			switch strings.ToLower(strings.TrimSpace(tc)) {
			case "none":
				return "" // Không tiêm công cụ nếu tool_choice là none
			case "required":
				isRequired = true
			case "auto":
				// Mặc định
			}
		case map[string]any:
			// Dạng OpenAI: {"type": "function", "function": {"name": "my_tool"}}
			if fn, ok := tc["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok && strings.TrimSpace(n) != "" {
					forcedToolName = strings.TrimSpace(n)
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("# Available Tools\n")
	sb.WriteString("You have access to the following functions to inspect or modify the workspace:\n")
	sb.WriteString("<tools>\n")

	for _, t := range tools {
		toolJSON, err := json.Marshal(t)
		if err == nil {
			sb.WriteString(string(toolJSON))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("</tools>\n\n")

	sb.WriteString("# Tool Calling Rules & Format\n")
	sb.WriteString("1. If you need to perform an action (reading/writing files, executing shell commands, web search), you MUST call one of the tools defined in <tools>.\n")
	sb.WriteString("2. To invoke a tool, output strictly in this XML format:\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "tool_name", "arguments": {"param1": "value1"}}` + "\n")
	sb.WriteString("</tool_call>\n")
	sb.WriteString("3. The \"arguments\" field MUST be a valid JSON object matching the parameters schema. Do NOT use unescaped newlines inside strings.\n")
	sb.WriteString("4. You may invoke multiple tools in one turn by outputting multiple <tool_call>...</tool_call> blocks.\n")
	sb.WriteString("5. When you invoke a tool, the environment executes it and returns the result in the next turn as:\n")
	sb.WriteString("[Tool Result (call_id: ...)]:\n<tool_output>\n...\n</tool_output>\n")
	sb.WriteString("Carefully inspect the output to determine your next action or provide your final response.\n")
	sb.WriteString("6. NEVER wrap <tool_call> inside Markdown code fences. NEVER invent tools that are not listed inside <tools>.\n")

	if forcedToolName != "" {
		sb.WriteString(fmt.Sprintf("\nCRITICAL REQUIREMENT: You MUST invoke the specific tool %q in this turn using the <tool_call> format.\n", forcedToolName))
	} else if isRequired {
		sb.WriteString("\nCRITICAL REQUIREMENT: You MUST invoke at least one tool from <tools> in this turn using the <tool_call> format. Do NOT respond with plain text only.\n")
	} else {
		sb.WriteString("7. If no tool is needed or you are directly answering the user, respond directly in plain text without <tool_call> tags.\n")
	}

	return sb.String()
}
