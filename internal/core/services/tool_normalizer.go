package services

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// JSONSchemaDef mô tả định dạng JSON Schema cơ bản dùng cho function parameters
type JSONSchemaDef struct {
	Type                 string                        `json:"type"`
	Required             []string                      `json:"required"`
	Properties           map[string]JSONSchemaProperty `json:"properties"`
	AdditionalProperties *bool                         `json:"additionalProperties"`
}

// JSONSchemaProperty mô tả thuộc tính trong JSON Schema
type JSONSchemaProperty struct {
	Type        any                  `json:"type"` // string hoặc []any
	Description string               `json:"description,omitempty"`
	Enum        []any                `json:"enum,omitempty"`
	Items       *JSONSchemaProperty  `json:"items,omitempty"`
	Properties  map[string]JSONSchemaProperty `json:"properties,omitempty"`
	Required    []string             `json:"required,omitempty"`
}

// ValidateJSONSchema kiểm tra chuỗi arguments JSON có khớp với định nghĩa parameters JSON Schema không
func ValidateJSONSchema(schemaRaw json.RawMessage, argsJSON string) error {
	trimmedArgs := strings.TrimSpace(argsJSON)
	if trimmedArgs == "" {
		trimmedArgs = "{}"
	}

	var parsedArgs any
	if err := json.Unmarshal([]byte(trimmedArgs), &parsedArgs); err != nil {
		return fmt.Errorf("arguments không phải định dạng JSON hợp lệ: %w", err)
	}

	// Nếu không có schema khai báo hoặc schema rỗng, chỉ cần arguments là valid JSON
	if len(schemaRaw) == 0 || strings.TrimSpace(string(schemaRaw)) == "" || strings.TrimSpace(string(schemaRaw)) == "{}" {
		return nil
	}

	var schema JSONSchemaDef
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		// Nếu schema khai báo bị lỗi cú pháp, bỏ qua kiểm tra sâu để tránh crash
		return nil
	}

	argsMap, ok := parsedArgs.(map[string]any)
	if !ok {
		if schema.Type == "object" || len(schema.Properties) > 0 || len(schema.Required) > 0 {
			return fmt.Errorf("arguments phải là một JSON object, nhận được %T", parsedArgs)
		}
		return nil
	}

	// 1. Kiểm tra các trường bắt buộc (required)
	for _, reqKey := range schema.Required {
		val, exists := argsMap[reqKey]
		if !exists || val == nil {
			return fmt.Errorf("thiếu tham số bắt buộc %q", reqKey)
		}
	}

	// 2. Kiểm tra type và enum của từng tham số
	for k, val := range argsMap {
		prop, propExists := schema.Properties[k]
		if !propExists {
			if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
				return fmt.Errorf("tham số không hợp lệ %q: additionalProperties không được phép", k)
			}
			continue
		}

		if val == nil {
			continue
		}

		if err := validatePropertyValue(k, prop, val); err != nil {
			return err
		}
	}

	return nil
}

func validatePropertyValue(fieldName string, prop JSONSchemaProperty, val any) error {
	// Kiểm tra kiểu dữ liệu
	expectedTypes := getExpectedTypes(prop.Type)
	if len(expectedTypes) > 0 {
		matched := false
		for _, exp := range expectedTypes {
			if checkTypeMatch(exp, val) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("tham số %q có kiểu dữ liệu không hợp lệ: kỳ vọng %v, nhận được %T", fieldName, expectedTypes, val)
		}
	}

	// Kiểm tra enum
	if len(prop.Enum) > 0 {
		matchedEnum := false
		for _, e := range prop.Enum {
			if fmt.Sprintf("%v", e) == fmt.Sprintf("%v", val) {
				matchedEnum = true
				break
			}
		}
		if !matchedEnum {
			return fmt.Errorf("tham số %q có giá trị %v không nằm trong danh sách enum cho phép %v", fieldName, val, prop.Enum)
		}
	}

	return nil
}

func getExpectedTypes(rawType any) []string {
	if rawType == nil {
		return nil
	}
	switch v := rawType.(type) {
	case string:
		if v != "" {
			return []string{strings.ToLower(v)}
		}
	case []any:
		var res []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				res = append(res, strings.ToLower(s))
			}
		}
		return res
	}
	return nil
}

func checkTypeMatch(expectedType string, val any) bool {
	switch expectedType {
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		_, ok := val.(float64)
		return ok
	case "integer":
		if f, ok := val.(float64); ok {
			return f == math.Trunc(f)
		}
		return false
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		_, ok := val.([]any)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "null":
		return val == nil
	default:
		return true
	}
}

// ParseToolChoice phân giải cấu hình tool_choice của OpenAI
func ParseToolChoice(toolChoice any) (mode string, specificFunc string, err error) {
	if toolChoice == nil {
		return "auto", "", nil
	}

	switch v := toolChoice.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" || s == "auto" {
			return "auto", "", nil
		}
		if s == "none" {
			return "none", "", nil
		}
		if s == "required" {
			return "required", "", nil
		}
		// Tên function cụ thể dưới dạng string
		return "function", s, nil
	case map[string]any:
		// Định dạng {"type": "function", "function": {"name": "..."}}
		t, _ := v["type"].(string)
		if t == "function" {
			if fnMap, ok := v["function"].(map[string]any); ok {
				if fnName, ok := fnMap["name"].(string); ok && strings.TrimSpace(fnName) != "" {
					return "function", strings.TrimSpace(fnName), nil
				}
			}
		}
		return "auto", "", nil
	default:
		return "auto", "", nil
	}
}

// ValidateAndNormalizeToolCalls thực hiện quy trình chuẩn hóa và kiểm định nghiêm ngặt cho Tool Calls:
// 1. Thực thi cấu hình tool_choice ("none", "auto", "required", hoặc ép gọi hàm cụ thể)
// 2. Chặn và từ chối các tool name không tồn tại trong danh sách allowedTools
// 3. Kiểm định schema tham số (arguments) với JSON Schema của tool
// 4. Chuẩn hóa ID, index, type cho đúng chuẩn OpenAI
// 5. Bảo toàn ánh xạ tool_call_id
func ValidateAndNormalizeToolCalls(rawCalls []domain.OpenAIToolCall, allowedTools []domain.OpenAITool, toolChoice any) ([]domain.OpenAIToolCall, error) {
	mode, specificFunc, err := ParseToolChoice(toolChoice)
	if err != nil {
		return nil, fmt.Errorf("lỗi cấu hình tool_choice: %w", err)
	}

	// tool_choice == "none": Không cho phép gọi bất kỳ tool nào
	if mode == "none" {
		return nil, nil
	}

	// Nếu không có tool nào được khai báo trong request
	if len(allowedTools) == 0 {
		if len(rawCalls) > 0 {
			return nil, fmt.Errorf("không có công cụ nào được cho phép cho yêu cầu này")
		}
		if mode == "required" || mode == "function" {
			return nil, fmt.Errorf("tool_choice là %q nhưng danh sách công cụ (tools) bị rỗng", mode)
		}
		return nil, nil
	}

	// Tạo bảng tra cứu tool được phép
	allowedMap := make(map[string]domain.OpenAITool, len(allowedTools))
	for _, t := range allowedTools {
		allowedMap[t.Function.Name] = t
	}

	var normalized []domain.OpenAIToolCall
	idx := 0

	for _, call := range rawCalls {
		callName := strings.TrimSpace(call.Function.Name)
		if callName == "" {
			return nil, fmt.Errorf("tên công cụ trong tool_call không được để trống")
		}

		// Nếu ép buộc gọi một hàm cụ thể
		if mode == "function" && specificFunc != "" && callName != specificFunc {
			// Bỏ qua các tool khác không khớp với hàm được yêu cầu
			continue
		}

		toolDef, allowed := allowedMap[callName]
		if !allowed {
			return nil, fmt.Errorf("công cụ %q không nằm trong danh sách được phép", callName)
		}

		// Kiểm tra schema của arguments
		argsJSON := strings.TrimSpace(call.Function.Arguments)
		if argsJSON == "" {
			argsJSON = "{}"
		}
		if err := ValidateJSONSchema(toolDef.Function.Parameters, argsJSON); err != nil {
			return nil, fmt.Errorf("tham số của công cụ %q không hợp lệ với schema: %w", callName, err)
		}

		callID := call.ID
		if strings.TrimSpace(callID) == "" {
			callID = generateToolCallID()
		}

		normalized = append(normalized, domain.OpenAIToolCall{
			Index: idx,
			ID:    callID,
			Type:  "function",
			Function: domain.OpenAIFunctionCallData{
				Name:      callName,
				Arguments: argsJSON,
			},
		})
		idx++
	}

	// Kiểm tra ràng buộc tool_choice
	if mode == "required" && len(normalized) == 0 {
		return nil, fmt.Errorf("tool_choice là 'required' nhưng mô hình không gọi bất kỳ công cụ hợp lệ nào")
	}

	if mode == "function" && specificFunc != "" && len(normalized) == 0 {
		return nil, fmt.Errorf("tool_choice yêu cầu gọi hàm %q nhưng mô hình không thực hiện", specificFunc)
	}

	return normalized, nil
}
