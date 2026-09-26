package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CanvasArtifact đại diện cho một tài liệu độc lập trong Gemini Canvas Workspace
type CanvasArtifact struct {
	CanvasID       string         `json:"canvas_id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Title          string         `json:"title"`
	ContentType    string         `json:"content_type"` // MARKDOWN, PYTHON, HTML_JS, PLAIN_TEXT
	Content        string         `json:"content"`
	Version        int            `json:"version"`
	ShareURL       string         `json:"share_url,omitempty"`
	DiffOps        []CanvasDiffOp `json:"diff_ops,omitempty"`
}

// CanvasDiffOp đại diện cho một thao tác thay đổi delta
type CanvasDiffOp struct {
	Op    string `json:"op"`              // "retain", "delete", "insert"
	Count int    `json:"count,omitempty"` // Dùng cho retain và delete
	Text  string `json:"text,omitempty"`  // Dùng cho insert
}

// BuildCreateCanvasRequest đóng gói payload batchexecute cho RPC tVk3Sc
func BuildCreateCanvasRequest(convID, title, contentType, initialContent string) (string, error) {
	convID = strings.TrimSpace(convID)
	title = strings.TrimSpace(title)
	contentType = strings.ToUpper(strings.TrimSpace(contentType))

	if title == "" {
		title = "Untitled Canvas"
	}
	if contentType == "" {
		contentType = "MARKDOWN"
	}

	innerArgs, err := json.Marshal([]any{convID, title, contentType, initialContent})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args tVk3Sc: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"tVk3Sc",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope tVk3Sc: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseCreateCanvasResponse giải mã phản hồi RPC tVk3Sc: ["canvas_<UUID>", version, timestamp]
func ParseCreateCanvasResponse(rawJSON string) (string, int, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", 0, fmt.Errorf("payload tVk3Sc rỗng")
	}

	var root any
	if err := json.Unmarshal([]byte(rawJSON), &root); err != nil {
		return "", 0, fmt.Errorf("tVk3Sc JSON không hợp lệ: %w", err)
	}

	// Đệ quy tìm mảng có phần tử đầu là chuỗi bắt đầu bằng "canvas_"
	canvasID, version, found := findCanvasResult(root)
	if !found {
		// Fallback: nếu chuỗi trả về trực tiếp
		if arr, ok := root.([]any); ok && len(arr) >= 2 {
			if cid, ok := arr[0].(string); ok {
				var ver int
				if vFloat, ok := arr[1].(float64); ok {
					ver = int(vFloat)
				}
				return cid, ver, nil
			}
		}
		return "", 0, fmt.Errorf("không tìm thấy canvas_id trong phản hồi tVk3Sc")
	}

	return canvasID, version, nil
}

func findCanvasResult(node any) (string, int, bool) {
	if str, ok := node.(string); ok && (strings.HasPrefix(str, "[") || strings.HasPrefix(str, "{")) {
		var parsed any
		if err := json.Unmarshal([]byte(str), &parsed); err == nil {
			if cid, ver, ok := findCanvasResult(parsed); ok {
				return cid, ver, true
			}
		}
	}

	arr, ok := node.([]any)
	if !ok {
		return "", 0, false
	}

	if len(arr) >= 2 {
		if cid, ok := arr[0].(string); ok && (strings.HasPrefix(cid, "canvas_") || strings.HasPrefix(cid, "c_")) {
			var ver int
			if vFloat, ok := arr[1].(float64); ok {
				ver = int(vFloat)
			}
			return cid, ver, true
		}
	}

	for _, child := range arr {
		if cid, ver, ok := findCanvasResult(child); ok {
			return cid, ver, true
		}
	}

	return "", 0, false
}

// BuildUpdateCanvasDeltaRequest đóng gói payload batchexecute cho RPC sA4a8
func BuildUpdateCanvasDeltaRequest(canvasID string, baseVersion int, diffOps []CanvasDiffOp) (string, error) {
	canvasID = strings.TrimSpace(canvasID)
	if canvasID == "" {
		return "", fmt.Errorf("canvas_id không được để trống")
	}

	innerArgs, err := json.Marshal([]any{canvasID, baseVersion, diffOps})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args sA4a8: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"sA4a8",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope sA4a8: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseUpdateCanvasDeltaResponse giải mã phản hồi RPC sA4a8: ["canvas_<UUID>", new_version, "SUCCESS"]
func ParseUpdateCanvasDeltaResponse(rawJSON string) (string, int, string, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", 0, "", fmt.Errorf("payload sA4a8 rỗng")
	}

	var root any
	if err := json.Unmarshal([]byte(rawJSON), &root); err != nil {
		return "", 0, "", fmt.Errorf("sA4a8 JSON không hợp lệ: %w", err)
	}

	if arr, ok := root.([]any); ok && len(arr) >= 3 {
		cid, _ := arr[0].(string)
		var ver int
		if vFloat, ok := arr[1].(float64); ok {
			ver = int(vFloat)
		}
		status, _ := arr[2].(string)
		return cid, ver, status, nil
	}

	cid, ver, found := findCanvasResult(root)
	if found {
		return cid, ver, "SUCCESS", nil
	}

	return "", 0, "", fmt.Errorf("không đọc được kết quả cập nhật delta sA4a8")
}

// BuildPublishCanvasRequest đóng gói payload batchexecute cho RPC H8s0fe
func BuildPublishCanvasRequest(canvasID string, visibilityCode int, allowFork bool) (string, error) {
	canvasID = strings.TrimSpace(canvasID)
	if canvasID == "" {
		return "", fmt.Errorf("canvas_id không được để trống")
	}
	if visibilityCode <= 0 {
		visibilityCode = 1 // 1: Unlisted / Link, 2: Public
	}

	innerArgs, err := json.Marshal([]any{canvasID, visibilityCode, allowFork})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args H8s0fe: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"H8s0fe",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope H8s0fe: %w", err)
	}

	return string(payloadBytes), nil
}

// ParsePublishCanvasResponse giải mã phản hồi RPC H8s0fe: ["https://gemini.google.com/share/canvas/...", timestamp]
func ParsePublishCanvasResponse(rawJSON string) (string, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", fmt.Errorf("payload H8s0fe rỗng")
	}

	var root any
	if err := json.Unmarshal([]byte(rawJSON), &root); err != nil {
		return "", fmt.Errorf("H8s0fe JSON không hợp lệ: %w", err)
	}

	shareURL := findURLInJSON(root)
	if shareURL == "" {
		return "", fmt.Errorf("không tìm thấy liên kết chia sẻ trong phản hồi H8s0fe")
	}

	return shareURL, nil
}

func findURLInJSON(node any) string {
	switch v := node.(type) {
	case string:
		if strings.HasPrefix(v, "https://gemini.google.com/share/canvas/") || strings.HasPrefix(v, "https://") {
			return v
		}
		if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
			var parsed any
			if err := json.Unmarshal([]byte(v), &parsed); err == nil {
				if url := findURLInJSON(parsed); url != "" {
					return url
				}
			}
		}
	case []any:
		for _, item := range v {
			if url := findURLInJSON(item); url != "" {
				return url
			}
		}
	case map[string]any:
		for _, item := range v {
			if url := findURLInJSON(item); url != "" {
				return url
			}
		}
	}
	return ""
}
