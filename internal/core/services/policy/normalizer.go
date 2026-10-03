package policy

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
	reTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)
	reCodeBlock     = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")
)

// GenerateToolCallID sinh ngẫu nhiên một Tool Call ID chuẩn OpenAI format (call_...)
func GenerateToolCallID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

// RepairJSONArguments tự động sửa chữa các lỗi JSON tham số phổ biến từ LLM
func RepairJSONArguments(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "{}"
	}

	// 1. Loại bỏ markdown code fence nếu có (```json ... ```)
	if matches := reCodeBlock.FindStringSubmatch(s); len(matches) > 1 {
		s = strings.TrimSpace(matches[1])
	}

	// 2. Nếu đã là JSON hợp lệ thì trả về ngay
	var js json.RawMessage
	if json.Unmarshal([]byte(s), &js) == nil {
		return s
	}

	// 3. Xóa trailing comma: {"a": 1,} -> {"a": 1}
	s = reTrailingComma.ReplaceAllString(s, "$1")

	// 4. Kiểm tra lại sau khi xóa trailing comma
	if json.Unmarshal([]byte(s), &js) == nil {
		return s
	}

	// 5. Tự động đóng ngoặc nếu LLM bị cắt chuỗi (Unbalanced braces/brackets)
	openBraces := 0
	openBrackets := 0
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if !inString {
			switch c {
			case '{':
				openBraces++
			case '}':
				if openBraces > 0 {
					openBraces--
				}
			case '[':
				openBrackets++
			case ']':
				if openBrackets > 0 {
					openBrackets--
				}
			}
		}
	}

	// Nếu chuỗi đang dở dang trong string literal, đóng ngoặc kép trước
	if inString {
		s += `"`
	}
	for openBrackets > 0 {
		s += "]"
		openBrackets--
	}
	for openBraces > 0 {
		s += "}"
		openBraces--
	}

	// Kiểm tra lại lần cuối
	if json.Unmarshal([]byte(s), &js) == nil {
		return s
	}

	// Nếu vẫn không parse được thì bọc raw string vào object arguments dự phòng
	return raw
}

// NormalizeToolCalls chuẩn hóa danh sách tool calls từ OpenAI format sang định dạng nội bộ
func NormalizeToolCalls(rawCalls []domain.OpenAIToolCall) ([]domain.ToolCall, error) {
	if len(rawCalls) == 0 {
		return nil, nil
	}

	seenIDs := make(map[string]bool)
	normalized := make([]domain.ToolCall, 0, len(rawCalls))

	for i, rc := range rawCalls {
		name := strings.TrimSpace(rc.Function.Name)
		if name == "" {
			continue
		}

		id := strings.TrimSpace(rc.ID)
		if id == "" || seenIDs[id] {
			id = fmt.Sprintf("%s_%d", GenerateToolCallID(), i)
		}
		seenIDs[id] = true

		repairedArgs := RepairJSONArguments(rc.Function.Arguments)

		normalized = append(normalized, domain.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: json.RawMessage(repairedArgs),
		})
	}

	return normalized, nil
}
