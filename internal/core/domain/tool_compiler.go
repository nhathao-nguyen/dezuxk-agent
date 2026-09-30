package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CompileToolsInstruction chuyển đổi danh sách OpenAITool thành chỉ dẫn hệ thống chuẩn XML
func CompileToolsInstruction(tools []OpenAITool) string {
	if len(tools) == 0 {
		return ""
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
	sb.WriteString("1. If you need to perform an action (reading files, listing directories, writing files, or running commands), you MUST call one of the tools defined in <tools>.\n")
	sb.WriteString("2. To invoke a tool, output strictly in this XML format:\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "tool_name", "arguments": {"param1": "value1"}}` + "\n")
	sb.WriteString("</tool_call>\n")
	sb.WriteString("3. You may provide a brief explanation before or after the <tool_call>.\n")
	sb.WriteString("4. If no tool is needed or you are directly answering the user, respond directly in plain text without <tool_call> tags.\n")
	sb.WriteString(fmt.Sprintf("5. NEVER invent tools that are not listed inside <tools>.\n"))

	return sb.String()
}
