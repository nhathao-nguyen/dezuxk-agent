package services

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

var (
	// Bắt thẻ <tool_call>...</tool_call> bất kể hoa thường, khoảng trắng hay xuống dòng
	reToolCallXML = regexp.MustCompile(`(?is)<\s*tool_call\s*>(.*?)</\s*tool_call\s*>`)
	// Bắt khối code block ```tool_call hoặc ```json có chứa "name" hoặc "tool"
	reToolCallMD = regexp.MustCompile("(?is)```(?:tool_call|json)\\s*\\n(.*?)```")
	// Regex loại bỏ trailing commas trước } hoặc ]
	reTrailingCommas = regexp.MustCompile(`,\s*([}\]])`)
	// Regex bóc tách lỏng lẻo khi JSON bị vỡ hoàn toàn
	reLooseToolName = regexp.MustCompile(`(?i)"?(?:name|tool)"?\s*[:=]\s*"([a-zA-Z0-9_\-]+)"`)
	reLooseArgsJSON = regexp.MustCompile(`(?s)"?(?:arguments|parameters|args|input)"?\s*[:=]\s*(\{.*\}|\[.*\])`)
)

func generateToolCallID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "call_0123456789ab"
	}
	return "call_" + hex.EncodeToString(b)
}

// RepairMalformedJSON sửa các lỗi cú pháp phổ biến của JSON sinh bởi LLM
func RepairMalformedJSON(s string) string {
	return repairMalformedJSON(s)
}

// SanitizeJSONStringLiterals sửa các ký tự điều khiển chưa được escape trong chuỗi string JSON
func SanitizeJSONStringLiterals(s string) string {
	return sanitizeJSONStringLiterals(s)
}

// repairMalformedJSON sửa các lỗi cú pháp phổ biến của JSON sinh bởi LLM:
// 1. Dấu phẩy thừa ở cuối (trailing commas) trước } hoặc ]
// 2. Chuyển đổi nháy đơn sang nháy kép khi cần
// 3. Tự động đếm và bổ sung ngoặc đóng } hoặc ] còn thiếu
func repairMalformedJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// 1. Loại bỏ trailing commas
	s = reTrailingCommas.ReplaceAllString(s, "$1")

	// 2. Chuyển đổi nháy đơn sang nháy kép nếu chuỗi không chứa nháy kép
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		s = strings.ReplaceAll(s, "'", `"`)
	}

	// 3. Đếm số lượng ngoặc mở và đóng ngoài chuỗi ký tự
	var braceCount, bracketCount int
	inStr := false
	escaped := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inStr = false
			}
			continue
		}
		switch b {
		case '"':
			inStr = true
		case '{':
			braceCount++
		case '}':
			braceCount--
		case '[':
			bracketCount++
		case ']':
			bracketCount--
		}
	}

	// Bổ sung đóng ngoặc nếu còn thiếu
	if inStr {
		s += `"`
	}
	for bracketCount > 0 {
		s += "]"
		bracketCount--
	}
	for braceCount > 0 {
		s += "}"
		braceCount--
	}

	return s
}

// sanitizeJSONStringLiterals sửa các ký tự điều khiển chưa được escape (xuống dòng, tab)
// bên trong chuỗi string JSON để json.Unmarshal không bị lỗi "invalid character '\n' in string literal".
func sanitizeJSONStringLiterals(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 16)
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		b := s[i]
		if inString {
			if escaped {
				escaped = false
				sb.WriteByte(b)
				continue
			}
			if b == '\\' {
				escaped = true
				sb.WriteByte(b)
				continue
			}
			if b == '"' {
				inString = false
				sb.WriteByte(b)
				continue
			}
			switch b {
			case '\n':
				sb.WriteString(`\n`)
			case '\r':
				sb.WriteString(`\r`)
			case '\t':
				sb.WriteString(`\t`)
			default:
				if b < 0x20 {
					sb.WriteString(fmt.Sprintf(`\u%04x`, b))
				} else {
					sb.WriteByte(b)
				}
			}
		} else {
			if b == '"' {
				inString = true
			}
			sb.WriteByte(b)
		}
	}
	return sb.String()
}

// cleanToolCallBody loại bỏ markdown code block fences bên trong nội dung tool call (nếu Gemini sinh thừa)
func cleanToolCallBody(body string) string {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "```") {
		if idx := strings.IndexByte(body, '\n'); idx != -1 {
			body = body[idx+1:]
		}
		if idx := strings.LastIndex(body, "```"); idx != -1 {
			body = body[:idx]
		}
		body = strings.TrimSpace(body)
	}
	return body
}

// ExtractToolCalls bóc tách các lệnh gọi công cụ từ văn bản do mô hình sinh ra
func ExtractToolCalls(rawText string) (string, []domain.OpenAIToolCall) {
	var toolCalls []domain.OpenAIToolCall
	cleanText := rawText

	// 1. Quét định dạng chuẩn XML: <tool_call>...</tool_call>
	matches := reToolCallXML.FindAllStringSubmatchIndex(rawText, -1)
	if len(matches) > 0 {
		for _, match := range matches {
			contentStart, contentEnd := match[2], match[3]
			jsonBody := rawText[contentStart:contentEnd]
			parsed := parseToolCallBody(jsonBody, len(toolCalls))
			toolCalls = append(toolCalls, parsed...)
		}
		cleanText = reToolCallXML.ReplaceAllString(cleanText, "")
	}

	// 2. Quét định dạng fallback Markdown codeblock nếu chưa có tool call từ XML
	if len(toolCalls) == 0 {
		mdMatches := reToolCallMD.FindAllStringSubmatchIndex(cleanText, -1)
		for _, match := range mdMatches {
			contentStart, contentEnd := match[2], match[3]
			jsonBody := cleanText[contentStart:contentEnd]
			// Chỉ parse nếu có dấu hiệu của tool call ("name" hoặc "tool")
			if strings.Contains(jsonBody, `"name"`) || strings.Contains(jsonBody, `"tool"`) {
				parsed := parseToolCallBody(jsonBody, len(toolCalls))
				if len(parsed) > 0 {
					toolCalls = append(toolCalls, parsed...)
					fullStart, fullEnd := match[0], match[1]
					cleanText = cleanText[:fullStart] + cleanText[fullEnd:]
				}
			}
		}
	}

	cleanText = strings.TrimSpace(cleanText)
	return cleanText, toolCalls
}

// parseToolCallBody giải mã nội dung bên trong tool call (hỗ trợ cả JSON object, mảng JSON objects và tự động sửa lỗi)
func parseToolCallBody(rawBody string, baseIndex int) []domain.OpenAIToolCall {
	cleaned := cleanToolCallBody(rawBody)
	if cleaned == "" {
		return nil
	}

	sanitized := sanitizeJSONStringLiterals(cleaned)
	repaired := repairMalformedJSON(sanitized)

	// Thử giải mã theo thứ tự: repaired (đã sửa lỗi) -> sanitized -> cleaned
	for _, candidate := range []string{repaired, sanitized, cleaned} {
		trimmed := strings.TrimSpace(candidate)
		if strings.HasPrefix(trimmed, "[") {
			var list []map[string]any
			if err := json.Unmarshal([]byte(trimmed), &list); err == nil && len(list) > 0 {
				var results []domain.OpenAIToolCall
				for i, item := range list {
					if tc, ok := parseSingleToolCallMap(item, baseIndex+i); ok {
						results = append(results, tc)
					}
				}
				if len(results) > 0 {
					return results
				}
			}
		}

		// Single JSON object {"name": ...}
		var single map[string]any
		if err := json.Unmarshal([]byte(trimmed), &single); err == nil && len(single) > 0 {
			if tc, ok := parseSingleToolCallMap(single, baseIndex); ok {
				return []domain.OpenAIToolCall{tc}
			}
		}
	}

	// Fallback trường hợp 3: Bóc tách dạng lỏng (Loose Regex Extraction)
	if looseCalls := extractLooseToolCalls(cleaned, baseIndex); len(looseCalls) > 0 {
		return looseCalls
	}

	return nil
}

func extractLooseToolCalls(text string, baseIndex int) []domain.OpenAIToolCall {
	nameMatch := reLooseToolName.FindStringSubmatch(text)
	if len(nameMatch) < 2 || strings.TrimSpace(nameMatch[1]) == "" {
		return nil
	}
	name := strings.TrimSpace(nameMatch[1])

	argsStr := "{}"
	if argsMatch := reLooseArgsJSON.FindStringSubmatch(text); len(argsMatch) >= 2 {
		candidateArgs := repairMalformedJSON(sanitizeJSONStringLiterals(strings.TrimSpace(argsMatch[1])))
		var test any
		if json.Unmarshal([]byte(candidateArgs), &test) == nil {
			argsStr = candidateArgs
		}
	}

	return []domain.OpenAIToolCall{{
		Index: baseIndex,
		ID:    generateToolCallID(),
		Type:  "function",
		Function: domain.OpenAIFunctionCallData{
			Name:      name,
			Arguments: argsStr,
		},
	}}
}

func parseSingleToolCallMap(item map[string]any, index int) (domain.OpenAIToolCall, bool) {
	name, _ := item["name"].(string)
	if name == "" {
		name, _ = item["tool"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.OpenAIToolCall{}, false
	}

	// Arguments có thể ở các key khác nhau do Gemini sinh ra
	var rawArgs any
	for _, key := range []string{"arguments", "parameters", "args", "input"} {
		if val, exists := item[key]; exists && val != nil {
			rawArgs = val
			break
		}
	}

	var argsStr string
	switch v := rawArgs.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var test any
			if json.Unmarshal([]byte(trimmed), &test) == nil {
				argsStr = trimmed
			} else {
				sanitized := sanitizeJSONStringLiterals(trimmed)
				repaired := repairMalformedJSON(sanitized)
				if json.Unmarshal([]byte(repaired), &test) == nil {
					argsStr = repaired
				}
			}
		}
		if argsStr == "" {
			b, _ := json.Marshal(map[string]string{"input": trimmed})
			argsStr = string(b)
		}
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err == nil {
			argsStr = string(b)
		} else {
			argsStr = "{}"
		}
	default:
		argsStr = "{}"
	}

	// Đảm bảo argsStr luôn là chuỗi JSON hợp lệ
	var testValid any
	if json.Unmarshal([]byte(argsStr), &testValid) != nil {
		argsStr = repairMalformedJSON(argsStr)
		if json.Unmarshal([]byte(argsStr), &testValid) != nil {
			argsStr = "{}"
		}
	}

	return domain.OpenAIToolCall{
		Index: index,
		ID:    generateToolCallID(),
		Type:  "function",
		Function: domain.OpenAIFunctionCallData{
			Name:      name,
			Arguments: argsStr,
		},
	}, true
}
