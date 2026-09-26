package domain

import (
	"time"
)

// AlertPayload chứa dữ liệu cảnh báo khẩn cấp cần gửi qua Webhook (Telegram / Discord / Slack / Generic)
type AlertPayload struct {
	AccountID      string      `json:"account_id"`
	ErrorType      string      `json:"error_type"`
	Service        ServiceKind `json:"service,omitempty"`
	Reason         string      `json:"reason"`
	StatusCode     int         `json:"status_code,omitempty"`
	ActionRequired string      `json:"action_required"`
	Timestamp      time.Time   `json:"timestamp"`
}
