package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FeedbackRequest chứa thông tin phản hồi chất lượng câu trả lời
type FeedbackRequest struct {
	ConversationID string `json:"conversation_id"`
	ResponseID     string `json:"response_id"`
	ChoiceID       string `json:"choice_id"`
	Rating         int    `json:"rating"`            // 1: Thích (Thumbs Up), 2: Không thích (Thumbs Down)
	Reasons        []int  `json:"reasons,omitempty"` // 1: Sai số liệu, 2: Không làm theo hướng dẫn, 3: Xúc phạm, 4: Không hữu ích, 5: Từ chối vô cớ, 6: Lặp từ
	Comment        string `json:"comment,omitempty"`
	Locale         string `json:"locale,omitempty"`
}

// BuildFeedbackRequest đóng gói payload batchexecute cho RPC uP80Sb
func BuildFeedbackRequest(convID, respID, choiceID string, rating int, reasons []int, comment, locale string) (string, error) {
	convID = strings.TrimSpace(convID)
	respID = strings.TrimSpace(respID)
	choiceID = strings.TrimSpace(choiceID)

	if convID == "" || respID == "" || choiceID == "" {
		return "", fmt.Errorf("convID, respID, choiceID không được để trống khi gửi feedback")
	}
	if rating != 1 && rating != 2 {
		rating = 1
	}
	if reasons == nil {
		reasons = []int{}
	}
	if locale == "" {
		locale = "vi"
	}

	innerArgs, err := json.Marshal([]any{
		convID,
		respID,
		choiceID,
		rating,
		reasons,
		comment,
		[]any{locale, nil},
	})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args uP80Sb: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"uP80Sb",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope uP80Sb: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseFeedbackResponse phân tích kết quả trả về từ RPC uP80Sb ([1])
func ParseFeedbackResponse(rawJSON string) (bool, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return false, fmt.Errorf("payload uP80Sb rỗng")
	}

	var arr []any
	if err := json.Unmarshal([]byte(rawJSON), &arr); err != nil {
		return false, fmt.Errorf("uP80Sb JSON không hợp lệ: %w", err)
	}

	if len(arr) == 0 {
		return false, fmt.Errorf("uP80Sb trả về mảng rỗng")
	}

	switch val := arr[0].(type) {
	case float64:
		return val == 1, nil
	case bool:
		return val, nil
	default:
		return false, fmt.Errorf("uP80Sb giá trị không xác định: %v", val)
	}
}
