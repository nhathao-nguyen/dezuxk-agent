package services

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

var (
	reToolCallXML = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)
	reToolCallMD  = regexp.MustCompile("(?s)```(?:tool_call|json)\n\\s*(\\{\\s*\"(?:name|tool)\"\\s*:.*?\\})\\s*\n```")
)

func generateToolCallID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "call_0123456789ab"
	}
	return "call_" + hex.EncodeToString(b)
}

// ExtractToolCalls bóc tách các lệnh gọi công cụ từ văn bản do mô hình sinh ra
func ExtractToolCalls(rawText string) (string, []domain.OpenAIToolCall) {
	var toolCalls []domain.OpenAIToolCall
	cleanText := rawText

	// 1. Quét định dạng chuẩn XML: <tool_call>...</tool_call>
	matches := reToolCallXML.FindAllStringSubmatchIndex(rawText, -1)
	if len(matches) > 0 {
		for i, match := range matches {
			contentStart, contentEnd := match[2], match[3]
			jsonBody := strings.TrimSpace(rawText[contentStart:contentEnd])

			call, ok := parseSingleToolCall(jsonBody, i)
			if ok {
				toolCalls = append(toolCalls, call)
			}
		}
		cleanText = reToolCallXML.ReplaceAllString(cleanText, "")
	}

	// 2. Quét định dạng fallback Markdown codeblock nếu chưa có tool call từ XML
	if len(toolCalls) == 0 {
		mdMatches := reToolCallMD.FindAllStringSubmatchIndex(cleanText, -1)
		for i, match := range mdMatches {
			contentStart, contentEnd := match[2], match[3]
			jsonBody := strings.TrimSpace(cleanText[contentStart:contentEnd])

			call, ok := parseSingleToolCall(jsonBody, i)
			if ok {
				toolCalls = append(toolCalls, call)
			}
		}
		if len(toolCalls) > 0 {
			cleanText = reToolCallMD.ReplaceAllString(cleanText, "")
		}
	}

	cleanText = strings.TrimSpace(cleanText)
	return cleanText, toolCalls
}

func parseSingleToolCall(jsonBody string, index int) (domain.OpenAIToolCall, bool) {
	var parsed struct {
		Name       string          `json:"name"`
		Tool       string          `json:"tool"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}

	if err := json.Unmarshal([]byte(jsonBody), &parsed); err != nil {
		return domain.OpenAIToolCall{}, false
	}

	funcName := strings.TrimSpace(parsed.Name)
	if funcName == "" {
		funcName = strings.TrimSpace(parsed.Tool)
	}
	if funcName == "" {
		return domain.OpenAIToolCall{}, false
	}

	argsRaw := parsed.Arguments
	if len(argsRaw) == 0 || string(argsRaw) == "null" {
		argsRaw = parsed.Parameters
	}
	if len(argsRaw) == 0 || string(argsRaw) == "null" {
		argsRaw = json.RawMessage(`{}`)
	}

	argsStr := string(argsRaw)
	// Đảm bảo Arguments là một chuỗi JSON hợp lệ
	var testJSON any
	if err := json.Unmarshal([]byte(argsStr), &testJSON); err != nil {
		argsStr = `{}`
	}

	return domain.OpenAIToolCall{
		Index: index,
		ID:    generateToolCallID(),
		Type:  "function",
		Function: domain.OpenAIFunctionCallData{
			Name:      funcName,
			Arguments: argsStr,
		},
	}, true
}
