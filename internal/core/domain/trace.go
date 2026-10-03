package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

type traceContextKey struct{}

// TraceContext chứa thông tin định danh và theo vết của một request xuyên suốt hệ thống
type TraceContext struct {
	RequestID       string    `json:"request_id"`
	TraceID         string    `json:"trace_id"`
	TenantID        string    `json:"tenant_id,omitempty"`
	KeyID           string    `json:"key_id,omitempty"`
	Model           string    `json:"model,omitempty"`
	MaskedAccountID string    `json:"masked_account_id,omitempty"`
	StartTime       time.Time `json:"start_time"`
}

// GenerateTraceID tạo ngẫu nhiên một chuỗi trace ID
func GenerateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateRequestID tạo ngẫu nhiên một chuỗi request ID
func GenerateRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// MaskAccountID ẩn các ký tự nhạy cảm của account ID (ví dụ: "acc_12345678" -> "acc_***678")
func MaskAccountID(id string) string {
	s := strings.TrimSpace(id)
	if len(s) <= 4 {
		return "***"
	}
	if len(s) <= 8 {
		return s[:2] + "***" + s[len(s)-2:]
	}
	return s[:4] + "***" + s[len(s)-3:]
}

// ContextWithTrace gắn TraceContext vào context.Context
func ContextWithTrace(ctx context.Context, tc *TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// TraceFromContext lấy TraceContext từ context.Context
func TraceFromContext(ctx context.Context) (*TraceContext, bool) {
	tc, ok := ctx.Value(traceContextKey{}).(*TraceContext)
	return tc, ok
}
